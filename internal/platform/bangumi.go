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
	bangumiAPIBase = "https://api.bgm.tv"
	userAgent      = "michealmachine/taro"
)

// BangumiUpdater handles Bangumi platform callbacks
type BangumiUpdater struct {
	cfg     *config.Config
	client  *http.Client
	logger  *slog.Logger
	mu      sync.Mutex
	apiBase string // Allow override for testing
}

// BangumiTokenResponse represents the OAuth2 token response
type BangumiTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"` // seconds
	TokenType    string `json:"token_type"`
}

// NewBangumiUpdater creates a new Bangumi updater
func NewBangumiUpdater(cfg *config.Config, logger *slog.Logger) *BangumiUpdater {
	return &BangumiUpdater{
		cfg: cfg,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		logger:  logger,
		apiBase: bangumiAPIBase,
	}
}

// MarkOwned marks a subject as owned (in collection) on Bangumi
// This is called when an entry reaches in_library status
func (u *BangumiUpdater) MarkOwned(ctx context.Context, entry *db.Entry) error {
	// Only process Bangumi entries
	if entry.Source != "bangumi" {
		return nil
	}

	// Parse subject_id from source_id
	subjectID, err := strconv.Atoi(entry.SourceID)
	if err != nil {
		u.logger.Error("invalid bangumi subject_id", "source_id", entry.SourceID, "error", err)
		return nil // Don't fail the callback, just log
	}

	u.logger.Info("marking bangumi subject as owned",
		"entry_id", entry.ID,
		"subject_id", subjectID,
		"title", entry.Title)

	// Call Bangumi API to update collection status
	// POST /v0/users/-/collections/{subject_id}
	// Body: {"type": 3} where 3 = "在看" (watching/collecting)
	if err := u.updateCollection(ctx, subjectID, 3); err != nil {
		u.logger.Error("failed to update bangumi collection",
			"entry_id", entry.ID,
			"subject_id", subjectID,
			"error", err)
		// Don't return error - platform callback failures shouldn't affect entry status
		return nil
	}

	u.logger.Info("successfully marked bangumi subject as owned",
		"entry_id", entry.ID,
		"subject_id", subjectID)

	return nil
}

// updateCollection updates the collection status for a subject
func (u *BangumiUpdater) updateCollection(ctx context.Context, subjectID, collectionType int) error {
	return u.updateCollectionWithRetry(ctx, subjectID, collectionType, false)
}

// updateCollectionWithRetry updates collection with token refresh retry
func (u *BangumiUpdater) updateCollectionWithRetry(ctx context.Context, subjectID, collectionType int, retried bool) error {
	url := fmt.Sprintf("%s/v0/users/-/collections/%d", u.apiBase, subjectID)

	// Prepare request body
	reqBody := map[string]int{
		"type": collectionType,
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+u.cfg.Bangumi.AccessToken)

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
		// Try to refresh token
		u.logger.Info("bangumi token expired, refreshing")
		if err := u.refreshToken(ctx); err != nil {
			return fmt.Errorf("failed to refresh token: %w", err)
		}
		// Retry with new token (only once)
		return u.updateCollectionWithRetry(ctx, subjectID, collectionType, true)
	}

	// Accept both 200 OK and 204 No Content as success
	if resp.StatusCode != 200 && resp.StatusCode != 204 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// refreshToken refreshes the OAuth2 access token
func (u *BangumiUpdater) refreshToken(ctx context.Context) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	// Check if another goroutine already refreshed the token
	if time.Now().Before(u.cfg.Bangumi.TokenExpiresAt) {
		u.logger.Debug("token already refreshed by another goroutine")
		return nil
	}

	// Prepare refresh token request
	// Note: Bangumi OAuth2 token refresh may not require client credentials
	// This follows the standard OAuth2 refresh flow
	reqBody := map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": u.cfg.Bangumi.RefreshToken,
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", u.apiBase+"/oauth/access_token", bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)

	resp, err := u.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute request: %w", err)
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

	// Update config and persist to file
	if err := u.cfg.UpdateBangumiToken(tokenResp.AccessToken, tokenResp.RefreshToken, expiresAt); err != nil {
		u.logger.Warn("failed to persist bangumi token to config file", "error", err)
		// Continue anyway - token is updated in memory
	}

	u.logger.Info("bangumi token refreshed successfully", "expires_at", expiresAt)
	return nil
}
