package platform

import (
	"bytes"
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

var (
	traktAPIBase    = "https://api.trakt.tv"
	traktAPIVersion = "2"
)

// TraktUpdater handles Trakt platform callbacks
type TraktUpdater struct {
	cfg     *config.Config
	client  *http.Client
	logger  *slog.Logger
	mu      sync.Mutex
	apiBase string // Allow override for testing
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

// NewTraktUpdater creates a new Trakt updater
func NewTraktUpdater(cfg *config.Config, logger *slog.Logger) *TraktUpdater {
	return &TraktUpdater{
		cfg: cfg,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		logger:  logger,
		apiBase: traktAPIBase,
	}
}

// MarkOwned marks a media item as owned (in collection) on Trakt
// This is called when an entry reaches in_library status
func (u *TraktUpdater) MarkOwned(ctx context.Context, entry *db.Entry) error {
	// Only process Trakt entries
	if entry.Source != "trakt" {
		return nil
	}

	u.logger.Info("marking trakt item as owned",
		"entry_id", entry.ID,
		"source_id", entry.SourceID,
		"title", entry.Title,
		"media_type", entry.MediaType)

	// Add to collection
	if err := u.addToCollection(ctx, entry); err != nil {
		u.logger.Error("failed to add to trakt collection",
			"entry_id", entry.ID,
			"error", err)
		// Don't return error - platform callback failures shouldn't affect entry status
		// But skip removing from watchlist since collection add failed
		return nil
	}

	// Remove from watchlist (only if collection add succeeded)
	if err := u.removeFromWatchlist(ctx, entry); err != nil {
		u.logger.Error("failed to remove from trakt watchlist",
			"entry_id", entry.ID,
			"error", err)
		// Don't return error - just log
	}

	u.logger.Info("successfully marked trakt item as owned",
		"entry_id", entry.ID)

	return nil
}

// addToCollection adds an item to Trakt collection
// POST /sync/collection
func (u *TraktUpdater) addToCollection(ctx context.Context, entry *db.Entry) error {
	return u.addToCollectionWithRetry(ctx, entry, false)
}

// addToCollectionWithRetry adds to collection with token refresh retry
func (u *TraktUpdater) addToCollectionWithRetry(ctx context.Context, entry *db.Entry, retried bool) error {
	url := fmt.Sprintf("%s/sync/collection", u.apiBase)

	// Build request body based on media type
	var reqBody map[string]interface{}

	traktID, err := strconv.Atoi(entry.SourceID)
	if err != nil {
		return fmt.Errorf("invalid trakt ID: %w", err)
	}

	switch entry.MediaType {
	case "movie":
		reqBody = map[string]interface{}{
			"movies": []map[string]interface{}{
				{
					"ids": map[string]int{
						"trakt": traktID,
					},
				},
			},
		}
	case "tv", "anime":
		// For shows, add the specific season
		reqBody = map[string]interface{}{
			"shows": []map[string]interface{}{
				{
					"ids": map[string]int{
						"trakt": traktID,
					},
					"seasons": []map[string]int{
						{
							"number": entry.Season,
						},
					},
				},
			},
		}
	default:
		return fmt.Errorf("unsupported media type: %s", entry.MediaType)
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+u.cfg.Trakt.AccessToken)
	req.Header.Set("trakt-api-version", traktAPIVersion)
	req.Header.Set("trakt-api-key", u.cfg.Trakt.ClientID)

	resp, err := u.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	// Handle 401 Unauthorized - token expired
	if resp.StatusCode == 401 {
		if retried {
			return fmt.Errorf("still unauthorized after token refresh")
		}
		u.logger.Info("trakt token expired, refreshing")
		if err := u.refreshToken(ctx); err != nil {
			return fmt.Errorf("failed to refresh token: %w", err)
		}
		// Retry with new token (only once)
		return u.addToCollectionWithRetry(ctx, entry, true)
	}

	// 201 Created is the expected success response
	if resp.StatusCode != 201 && resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// removeFromWatchlist removes an item from Trakt watchlist
// POST /sync/watchlist/remove
func (u *TraktUpdater) removeFromWatchlist(ctx context.Context, entry *db.Entry) error {
	return u.removeFromWatchlistWithRetry(ctx, entry, false)
}

// removeFromWatchlistWithRetry removes from watchlist with token refresh retry
func (u *TraktUpdater) removeFromWatchlistWithRetry(ctx context.Context, entry *db.Entry, retried bool) error {
	url := fmt.Sprintf("%s/sync/watchlist/remove", u.apiBase)

	// Build request body based on media type
	var reqBody map[string]interface{}

	traktID, err := strconv.Atoi(entry.SourceID)
	if err != nil {
		return fmt.Errorf("invalid trakt ID: %w", err)
	}

	switch entry.MediaType {
	case "movie":
		reqBody = map[string]interface{}{
			"movies": []map[string]interface{}{
				{
					"ids": map[string]int{
						"trakt": traktID,
					},
				},
			},
		}
	case "tv", "anime":
		// For shows, remove the entire show from watchlist
		reqBody = map[string]interface{}{
			"shows": []map[string]interface{}{
				{
					"ids": map[string]int{
						"trakt": traktID,
					},
				},
			},
		}
	default:
		return fmt.Errorf("unsupported media type: %s", entry.MediaType)
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+u.cfg.Trakt.AccessToken)
	req.Header.Set("trakt-api-version", traktAPIVersion)
	req.Header.Set("trakt-api-key", u.cfg.Trakt.ClientID)

	resp, err := u.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	// Handle 401 Unauthorized - token expired
	if resp.StatusCode == 401 {
		if retried {
			return fmt.Errorf("still unauthorized after token refresh")
		}
		u.logger.Info("trakt token expired, refreshing")
		if err := u.refreshToken(ctx); err != nil {
			return fmt.Errorf("failed to refresh token: %w", err)
		}
		// Retry with new token (only once)
		return u.removeFromWatchlistWithRetry(ctx, entry, true)
	}

	// 200 OK is the expected success response
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// refreshToken refreshes the OAuth2 access token
func (u *TraktUpdater) refreshToken(ctx context.Context) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	// Check if another goroutine already refreshed the token
	if time.Now().Before(u.cfg.Trakt.TokenExpiresAt) {
		u.logger.Debug("token already refreshed by another goroutine")
		return nil
	}

	// Prepare refresh token request
	redirectURI := u.cfg.Trakt.RedirectURI
	if redirectURI == "" {
		redirectURI = "urn:ietf:wg:oauth:2.0:oob" // default for CLI/desktop apps
	}

	reqBody := map[string]string{
		"refresh_token": u.cfg.Trakt.RefreshToken,
		"client_id":     u.cfg.Trakt.ClientID,
		"client_secret": u.cfg.Trakt.ClientSecret,
		"redirect_uri":  redirectURI,
		"grant_type":    "refresh_token",
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", u.apiBase+"/oauth/token", bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := u.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("token refresh failed with status %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp TraktTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return fmt.Errorf("failed to decode token response: %w", err)
	}

	// Calculate expiration time
	expiresAt := time.Unix(tokenResp.CreatedAt, 0).Add(time.Duration(tokenResp.ExpiresIn) * time.Second)

	// Update config and persist to file
	if err := u.cfg.UpdateTraktToken(tokenResp.AccessToken, tokenResp.RefreshToken, expiresAt); err != nil {
		u.logger.Warn("failed to persist trakt token to config file", "error", err)
		// Continue anyway - token is updated in memory
	}

	u.logger.Info("trakt token refreshed successfully", "expires_at", expiresAt)
	return nil
}
