package poller

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
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
	SubjectID int            `json:"subject_id"`
	Subject   BangumiSubject `json:"subject"`
	Type      int            `json:"type"` // 1=想看 2=看过 3=在看 4=搁置 5=抛弃
	Rate      int            `json:"rate"`
	UpdatedAt string         `json:"updated_at"`
}

// BangumiSubject represents anime/show metadata
type BangumiSubject struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`    // Japanese name (preferred)
	NameCN  string `json:"name_cn"` // Chinese name
	Type    int    `json:"type"`    // 2=anime
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

// BangumiTokenResponse represents the OAuth2 token response
type BangumiTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"` // seconds
	TokenType    string `json:"token_type"`
}

// BangumiCollectionResponse represents the API response
type BangumiCollectionResponse struct {
	Total  int                 `json:"total"`
	Limit  int                 `json:"limit"`
	Offset int                 `json:"offset"`
	Data   []BangumiCollection `json:"data"`
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

		// Update config with UID (best effort, don't fail if save fails)
		p.cfg.Bangumi.UID = uid
		if err := p.cfg.Save(); err != nil {
			p.logger.Warn("failed to persist UID to config file, will retry on next startup",
				"uid", uid, "error", err)
		} else {
			p.logger.Info("saved bangumi UID to config", "uid", uid)
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
		if title == "" {
			p.logger.Warn("skipping entry with empty title", "subject_id", sourceID)
			continue
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

// fetchCollections fetches the user's anime collections with pagination support
func (p *BangumiPoller) fetchCollections(ctx context.Context, uid int) ([]BangumiCollection, error) {
	allCollections := []BangumiCollection{}
	offset := 0
	limit := 100

	for {
		url := fmt.Sprintf("%s/v0/users/%d/collections?subject_type=2&type=1&limit=%d&offset=%d",
			bangumiAPIBase, uid, limit, offset)

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

		allCollections = append(allCollections, response.Data...)

		// Check if we've fetched all items
		if len(response.Data) < limit || offset+len(response.Data) >= response.Total {
			break
		}

		offset += limit
	}

	return allCollections, nil
}

// refreshToken refreshes the OAuth2 access token
func (p *BangumiPoller) refreshToken(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.logger.Info("refreshing bangumi token")

	// Check if refresh token is available
	if p.cfg.Bangumi.RefreshToken == "" {
		return fmt.Errorf("bangumi refresh token not configured, please re-authenticate")
	}

	// Prepare refresh request
	data := fmt.Sprintf("grant_type=refresh_token&refresh_token=%s", p.cfg.Bangumi.RefreshToken)
	req, err := http.NewRequestWithContext(ctx, "POST", "https://bgm.tv/oauth/access_token",
		strings.NewReader(data))
	if err != nil {
		return fmt.Errorf("failed to create refresh request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", userAgent)

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute refresh request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("token refresh failed with status %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp BangumiTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return fmt.Errorf("failed to decode token response: %w", err)
	}

	// Calculate expiration time
	expiresAt := time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)

	// Update config and save
	if err := p.cfg.UpdateBangumiToken(tokenResp.AccessToken, tokenResp.RefreshToken, expiresAt); err != nil {
		p.logger.Error("failed to save refreshed token", "error", err)
		// Still update in-memory config even if save fails
		p.cfg.Bangumi.AccessToken = tokenResp.AccessToken
		p.cfg.Bangumi.RefreshToken = tokenResp.RefreshToken
		p.cfg.Bangumi.TokenExpiresAt = expiresAt
		return fmt.Errorf("token refreshed but failed to save to config: %w", err)
	}

	p.logger.Info("bangumi token refreshed successfully", "expires_at", expiresAt)
	return nil
}
