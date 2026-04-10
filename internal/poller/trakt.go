package poller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/michealmachine/taro/internal/config"
	"github.com/michealmachine/taro/internal/db"
)

const (
	traktAPIBase = "https://api.trakt.tv"
)

// TraktPoller polls Trakt watchlist for new movies and shows
type TraktPoller struct {
	config   *config.Config
	database *db.DB
	client   *http.Client
	logger   *slog.Logger
}

// TraktWatchlistItem represents a single item in the watchlist
type TraktWatchlistItem struct {
	ListedAt string      `json:"listed_at"`
	Type     string      `json:"type"` // "movie" or "show"
	Movie    *TraktMovie `json:"movie,omitempty"`
	Show     *TraktShow  `json:"show,omitempty"`
}

// TraktMovie represents a movie from Trakt API
type TraktMovie struct {
	Title string       `json:"title"`
	Year  int          `json:"year"`
	IDs   TraktMediaID `json:"ids"`
}

// TraktShow represents a show from Trakt API
type TraktShow struct {
	Title string       `json:"title"`
	Year  int          `json:"year"`
	IDs   TraktMediaID `json:"ids"`
}

// TraktMediaID contains various IDs for a media item
type TraktMediaID struct {
	Trakt int    `json:"trakt"`
	Slug  string `json:"slug"`
	IMDB  string `json:"imdb"`
	TMDB  int    `json:"tmdb"`
}

// TraktTokenResponse represents OAuth2 token response
type TraktTokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
	CreatedAt    int64  `json:"created_at"`
}

// NewTraktPoller creates a new Trakt poller
func NewTraktPoller(cfg *config.Config, database *db.DB, logger *slog.Logger) *TraktPoller {
	return &TraktPoller{
		config:   cfg,
		database: database,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		logger: logger,
	}
}

// Poll fetches the watchlist and creates new entries
func (p *TraktPoller) Poll(ctx context.Context) error {
	p.logger.Info("starting trakt poll")

	// Check if token is expired and refresh if needed
	if time.Now().After(p.config.Trakt.TokenExpiresAt) {
		p.logger.Info("trakt token expired, refreshing")
		if err := p.refreshToken(ctx); err != nil {
			return fmt.Errorf("failed to refresh token: %w", err)
		}
	}

	// Fetch movies from watchlist
	movies, err := p.fetchWatchlist(ctx, "movies")
	if err != nil {
		return fmt.Errorf("failed to fetch movies watchlist: %w", err)
	}

	// Fetch shows from watchlist
	shows, err := p.fetchWatchlist(ctx, "shows")
	if err != nil {
		return fmt.Errorf("failed to fetch shows watchlist: %w", err)
	}

	p.logger.Info("fetched watchlist", "movies", len(movies), "shows", len(shows))

	// Process movies
	for _, item := range movies {
		if item.Movie == nil {
			continue
		}

		if err := p.createMovieEntry(ctx, item.Movie); err != nil {
			p.logger.Error("failed to create movie entry", "error", err, "title", item.Movie.Title)
		}
	}

	// Process shows
	for _, item := range shows {
		if item.Show == nil {
			continue
		}

		if err := p.createShowEntry(ctx, item.Show); err != nil {
			p.logger.Error("failed to create show entry", "error", err, "title", item.Show.Title)
		}
	}

	p.logger.Info("trakt poll completed")
	return nil
}

// fetchWatchlist fetches watchlist items of a specific type
func (p *TraktPoller) fetchWatchlist(ctx context.Context, itemType string) ([]TraktWatchlistItem, error) {
	url := fmt.Sprintf("%s/sync/watchlist/%s", traktAPIBase, itemType)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set required headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", p.config.Trakt.AccessToken))
	req.Header.Set("trakt-api-version", "2")
	req.Header.Set("trakt-api-key", p.config.Trakt.ClientID)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 {
		return nil, fmt.Errorf("unauthorized: token may be invalid or expired")
	}

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}

	var items []TraktWatchlistItem
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return items, nil
}

// createMovieEntry creates a new entry for a movie
func (p *TraktPoller) createMovieEntry(ctx context.Context, movie *TraktMovie) error {
	sourceID := fmt.Sprintf("%d", movie.IDs.Trakt)

	// Check if entry already exists
	exists, err := p.database.EntryExists(ctx, "trakt", sourceID, 0)
	if err != nil {
		return fmt.Errorf("failed to check if entry exists: %w", err)
	}

	if exists {
		p.logger.Debug("movie entry already exists", "title", movie.Title, "source_id", sourceID)
		return nil
	}

	// Create new entry
	entry := &db.Entry{
		Title:     movie.Title,
		MediaType: "movie",
		Season:    0, // Movies don't have seasons
		Source:    "trakt",
		SourceID:  sourceID,
		Status:    "pending",
		AskMode:   0, // Use global default
	}

	if err := p.database.CreateEntry(ctx, entry); err != nil {
		return fmt.Errorf("failed to create entry: %w", err)
	}

	p.logger.Info("created movie entry", "title", movie.Title, "year", movie.Year, "entry_id", entry.ID)
	return nil
}

// createShowEntry creates a new entry for a show (season 1 by default)
func (p *TraktPoller) createShowEntry(ctx context.Context, show *TraktShow) error {
	sourceID := fmt.Sprintf("%d", show.IDs.Trakt)
	season := 1 // Default to season 1

	// Check if entry already exists
	exists, err := p.database.EntryExists(ctx, "trakt", sourceID, season)
	if err != nil {
		return fmt.Errorf("failed to check if entry exists: %w", err)
	}

	if exists {
		p.logger.Debug("show entry already exists", "title", show.Title, "source_id", sourceID, "season", season)
		return nil
	}

	// Create new entry
	entry := &db.Entry{
		Title:     show.Title,
		MediaType: "tv",
		Season:    season,
		Source:    "trakt",
		SourceID:  sourceID,
		Status:    "pending",
		AskMode:   0, // Use global default
	}

	if err := p.database.CreateEntry(ctx, entry); err != nil {
		return fmt.Errorf("failed to create entry: %w", err)
	}

	p.logger.Info("created show entry", "title", show.Title, "year", show.Year, "season", season, "entry_id", entry.ID)
	return nil
}

// refreshToken refreshes the OAuth2 access token
func (p *TraktPoller) refreshToken(ctx context.Context) error {
	p.logger.Info("refreshing trakt token")

	url := fmt.Sprintf("%s/oauth/token", traktAPIBase)

	payload := map[string]string{
		"refresh_token": p.config.Trakt.RefreshToken,
		"client_id":     p.config.Trakt.ClientID,
		"client_secret": p.config.Trakt.ClientSecret,
		"redirect_uri":  "urn:ietf:wg:oauth:2.0:oob",
		"grant_type":    "refresh_token",
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Body = io.NopCloser(bytes.NewReader(body))

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("token refresh failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	var tokenResp TraktTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return fmt.Errorf("failed to decode token response: %w", err)
	}

	// Calculate expiration time
	expiresAt := time.Unix(tokenResp.CreatedAt, 0).Add(time.Duration(tokenResp.ExpiresIn) * time.Second)

	// Update config and save to file
	if err := p.config.UpdateTraktToken(tokenResp.AccessToken, tokenResp.RefreshToken, expiresAt); err != nil {
		p.logger.Warn("failed to save updated trakt token to config", "error", err)
		// Don't return error, token is still updated in memory
	}

	p.logger.Info("trakt token refreshed successfully", "expires_at", expiresAt)
	return nil
}
