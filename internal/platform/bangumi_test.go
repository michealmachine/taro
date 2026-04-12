package platform

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/michealmachine/taro/internal/config"
	"github.com/michealmachine/taro/internal/db"
)

func TestBangumiUpdater_MarkOwned(t *testing.T) {
	tests := []struct {
		name           string
		entry          *db.Entry
		serverResponse int
		serverBody     string
		wantErr        bool
	}{
		{
			name: "successful update",
			entry: &db.Entry{
				ID:       "test-entry-1",
				Source:   "bangumi",
				SourceID: "12345",
				Title:    "Test Anime",
			},
			serverResponse: 200,
			serverBody:     `{}`,
			wantErr:        false,
		},
		{
			name: "non-bangumi entry ignored",
			entry: &db.Entry{
				ID:       "test-entry-2",
				Source:   "trakt",
				SourceID: "12345",
				Title:    "Test Show",
			},
			wantErr: false,
		},
		{
			name: "invalid subject_id",
			entry: &db.Entry{
				ID:       "test-entry-3",
				Source:   "bangumi",
				SourceID: "invalid",
				Title:    "Test Anime",
			},
			wantErr: false, // Should not return error, just log
		},
		{
			name: "api error does not fail callback",
			entry: &db.Entry{
				ID:       "test-entry-4",
				Source:   "bangumi",
				SourceID: "12345",
				Title:    "Test Anime",
			},
			serverResponse: 500,
			serverBody:     `{"error": "internal server error"}`,
			wantErr:        false, // Platform failures don't fail callback
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test server
			var server *httptest.Server
			if tt.entry.Source == "bangumi" && tt.entry.SourceID != "invalid" {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/v0/users/-/collections/12345" && r.Method == "POST" {
						w.WriteHeader(tt.serverResponse)
						w.Write([]byte(tt.serverBody))
						return
					}
					w.WriteHeader(404)
				}))
				defer server.Close()
			}

			// Create updater
			cfg := &config.Config{
				Bangumi: config.BangumiConfig{
					AccessToken: "test-token",
				},
			}
			logger := slog.Default()
			updater := NewBangumiUpdater(cfg, logger)

			// Override API base if server exists
			if server != nil {
				updater.apiBase = server.URL
				updater.client = server.Client()
			}

			// Execute
			err := updater.MarkOwned(context.Background(), tt.entry)

			// Verify
			if (err != nil) != tt.wantErr {
				t.Errorf("MarkOwned() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestBangumiUpdater_RefreshToken(t *testing.T) {
	// Create test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/access_token" && r.Method == "POST" {
			resp := BangumiTokenResponse{
				AccessToken:  "new-access-token",
				RefreshToken: "new-refresh-token",
				ExpiresIn:    3600,
				TokenType:    "Bearer",
			}
			w.WriteHeader(200)
			json.NewEncoder(w).Encode(resp)
			return
		}
		w.WriteHeader(404)
	}))
	defer server.Close()

	// Create updater with expired token
	cfg := &config.Config{
		Bangumi: config.BangumiConfig{
			AccessToken:    "old-token",
			RefreshToken:   "refresh-token",
			TokenExpiresAt: time.Now().Add(-1 * time.Hour), // Expired
		},
	}
	logger := slog.Default()
	updater := NewBangumiUpdater(cfg, logger)
	updater.apiBase = server.URL
	updater.client = server.Client()

	// Execute refresh
	err := updater.refreshToken(context.Background())
	if err != nil {
		t.Fatalf("refreshToken() error = %v", err)
	}

	// Verify token was updated
	if cfg.Bangumi.AccessToken != "new-access-token" {
		t.Errorf("AccessToken not updated, got %s", cfg.Bangumi.AccessToken)
	}
	if cfg.Bangumi.RefreshToken != "new-refresh-token" {
		t.Errorf("RefreshToken not updated, got %s", cfg.Bangumi.RefreshToken)
	}
	if cfg.Bangumi.TokenExpiresAt.Before(time.Now()) {
		t.Errorf("TokenExpiresAt not updated correctly")
	}
}
