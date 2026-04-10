package poller

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/michealmachine/taro/internal/config"
	"github.com/michealmachine/taro/internal/db"
)

const (
	bangumiAPIBase = "https://api.bgm.tv"
	userAgent      = "michealmachine/taro"
)

// BangumiPoller polls Bangumi for anime watchlist
type BangumiPoller struct {
	cfg    *config.Config
	db     *db.DB
	client *http.Client
	mu     sync.Mutex
	logger *slog.Logger
}

// BangumiUser represents the current user info
type BangumiUser struct {
	ID       int    `json:"id"`
	Username string `json:"username"`
}

// BangumiCollection represents a collection item
type BangumiCollection struct {
	SubjectID   int             `json:"subject_id"`
	Subject     BangumiSubject  `json:"subject"`
	Type        int             `json:"type"` // 1=想看 2=看过 3=在看 4=搁置 5=抛弃
	Rate        int             `json:"rate"`
	UpdatedAt   string          `json:"updated_at"`
}

// BangumiSubject represents anime/show metadata
type BangumiSubject struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`     // Japanese name (preferred)
	NameCN  string `json:"name_cn"`  // Chinese name
	Type    int    `json:"type"`     // 2=anime
	Eps     int    `json:"eps"`
	AirDate string `json:"air_date"`
	Images  struct {
		Large  string `json:"large"`
		Common string `json:"common"`
		Medium string `json:"medium"`
		Small  string `json:"small"`
		Grid   string `json:"grid"`
	} `json:"images"`
}

// BangumiCollectionResponse represents the API response
type BangumiCollectionResponse struct {
	Total  int                  `json:"total"`
	Limit  int                  `json:"limit"`
	Offset int                  `json:"offset"`
	Data   []BangumiCollection  `json:"data"`
}

// NewBangumiPoller creates a new Bangumi poller
func NewBangumiPoller(cfg *config.Config, database *db.DB, logger *slog.Logger) *BangumiPoller {
	return &BangumiPoller{
		cfg: cfg,
		db:  database,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		logger: logger,
	}
}

// Name returns the poller name
func (p *BangumiPoller) Name() string {
	return "bangumi"
}

// Poll fetches the watchlist from Bangumi
func (p *BangumiPoller) Poll(ctx context.Context) error {
	p.logger.Info("starting bangumi poll")

	// Get user ID if not configured
	uid := p.cfg.Bangumi.UID
	if uid == 0 {
		user, err := p.getCurrentUser(ctx)
		if err != nil {
			return fmt.Errorf("failed to get current user: %w", err)
		}
		uid = user.ID
		
		// Update config with UID
		p.cfg.Bangumi.UID = uid
		if err := p.cfg.Save(); err != nil {
			p.logger.Warn("failed to save UID to config", "error", err)
		}
	}

	// Fetch collections (watchlist)
	collections, err := p.fetchCollections(ctx, uid)
	if err != nil {
		return fmt.Errorf("failed to fetch collections: %w", err)
	}

	p.logger.Info("fetched bangumi collections", "count", len(collections))

	// Process each collection
	newCount := 0
	for _, coll := range collections {
		// Only process "想看" (type=1)
		if coll.Type != 1 {
			continue
		}

		// Check if entry already exists
		sourceID := strconv.Itoa(coll.SubjectID)
		exists, err := p.db.EntryExists(ctx, "bangumi", sourceID, 1)
		if err != nil {
			p.logger.Error("failed to check entry existence", "subject_id", sourceID, "error", err)
			continue
		}

		if exists {
			continue
		}

		// Create new entry
		title := coll.Subject.Name // Prefer Japanese name
		if title == "" {
			title = coll.Subject.NameCN // Fallback to Chinese name
		}

		entry := &db.Entry{
			Source:    "bangumi",
			SourceID:  sourceID,
			MediaType: "anime",
			Title:     title,
			Season:    1, // Default to season 1
			Status:    "pending",
			AskMode:   0, // Use global config
		}

		if err := p.db.CreateEntry(ctx, entry); err != nil {
			p.logger.Error("failed to create entry", "subject_id", sourceID, "error", err)
			continue
		}

		p.logger.Info("created new entry", "subject_id", sourceID, "title", title)
		newCount++
	}

	p.logger.Info("bangumi poll completed", "new_entries", newCount)
	return nil
}

// getCurrentUser fetches the current user info
func (p *BangumiPoller) getCurrentUser(ctx context.Context) (*BangumiUser, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", bangumiAPIBase+"/v0/me", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Authorization", "Bearer "+p.cfg.Bangumi.AccessToken)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 {
		// Token expired, try to refresh
		if err := p.refreshToken(ctx); err != nil {
			return nil, fmt.Errorf("failed to refresh token: %w", err)
		}
		// Retry with new token
		return p.getCurrentUser(ctx)
	}

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}

	var user BangumiUser
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &user, nil
}

// fetchCollections fetches the user's anime collections
func (p *BangumiPoller) fetchCollections(ctx context.Context, uid int) ([]BangumiCollection, error) {
	url := fmt.Sprintf("%s/v0/users/%d/collections?subject_type=2&type=1&limit=100", bangumiAPIBase, uid)
	
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Authorization", "Bearer "+p.cfg.Bangumi.AccessToken)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 {
		// Token expired, try to refresh
		if err := p.refreshToken(ctx); err != nil {
			return nil, fmt.Errorf("failed to refresh token: %w", err)
		}
		// Retry with new token
		return p.fetchCollections(ctx, uid)
	}

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}

	var response BangumiCollectionResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return response.Data, nil
}

// refreshToken refreshes the OAuth2 access token
func (p *BangumiPoller) refreshToken(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.logger.Info("refreshing bangumi token")

	// TODO: Implement OAuth2 token refresh
	// For now, return an error indicating manual refresh is needed
	return fmt.Errorf("bangumi token expired, please refresh manually")
}
