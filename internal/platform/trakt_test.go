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

func TestTraktUpdater_MarkOwned(t *testing.T) {
	tests := []struct {
		name             string
		entry            *db.Entry
		collectionStatus int
		watchlistStatus  int
		wantErr          bool
	}{
		{
			name: "successful movie update",
			entry: &db.Entry{
				ID:        "test-entry-1",
				Source:    "trakt",
				SourceID:  "12345",
				Title:     "Test Movie",
				MediaType: "movie",
			},
			collectionStatus: 201,
			watchlistStatus:  200,
			wantErr:          false,
		},
		{
			name: "successful tv show update",
			entry: &db.Entry{
				ID:        "test-entry-2",
				Source:    "trakt",
				SourceID:  "12345",
				Title:     "Test Show",
				MediaType: "tv",
				Season:    1,
			},
			collectionStatus: 201,
			watchlistStatus:  200,
			wantErr:          false,
		},
		{
			name: "successful anime update",
			entry: &db.Entry{
				ID:        "test-entry-3",
				Source:    "trakt",
				SourceID:  "12345",
				Title:     "Test Anime",
				MediaType: "anime",
				Season:    1,
			},
			collectionStatus: 201,
			watchlistStatus:  200,
			wantErr:          false,
		},
		{
			name: "non-trakt entry ignored",
			entry: &db.Entry{
				ID:       "test-entry-4",
				Source:   "bangumi",
				SourceID: "12345",
				Title:    "Test Anime",
			},
			wantErr: false,
		},
		{
			name: "collection api error does not fail callback",
			entry: &db.Entry{
				ID:        "test-entry-5",
				Source:    "trakt",
				SourceID:  "12345",
				Title:     "Test Movie",
				MediaType: "movie",
			},
			collectionStatus: 500,
			watchlistStatus:  200,
			wantErr:          false, // Platform failures don't fail callback
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test server
			var server *httptest.Server
			if tt.entry.Source == "trakt" {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/sync/collection" && r.Method == "POST" {
						w.WriteHeader(tt.collectionStatus)
						w.Write([]byte(`{"added": {"movies": 1}}`))
						return
					}
					if r.URL.Path == "/sync/watchlist/remove" && r.Method == "POST" {
						w.WriteHeader(tt.watchlistStatus)
						w.Write([]byte(`{"deleted": {"movies": 1}}`))
						return
					}
					w.WriteHeader(404)
				}))
				defer server.Close()
			}

			// Create updater
			cfg := &config.Config{
				Trakt: config.TraktConfig{
					ClientID:     "test-client-id",
					ClientSecret: "test-client-secret",
					AccessToken:  "test-token",
				},
			}
			logger := slog.Default()
			updater := NewTraktUpdater(cfg, logger)

			// Override client if server exists
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

func TestTraktUpdater_RefreshToken(t *testing.T) {
	// Create test server
	createdAt := time.Now().Unix()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/token" && r.Method == "POST" {
			resp := TraktTokenResponse{
				AccessToken:  "new-access-token",
				RefreshToken: "new-refresh-token",
				ExpiresIn:    7200,
				TokenType:    "Bearer",
				CreatedAt:    createdAt,
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
		Trakt: config.TraktConfig{
			ClientID:       "test-client-id",
			ClientSecret:   "test-client-secret",
			AccessToken:    "old-token",
			RefreshToken:   "refresh-token",
			TokenExpiresAt: time.Now().Add(-1 * time.Hour), // Expired
		},
	}
	logger := slog.Default()
	updater := NewTraktUpdater(cfg, logger)
	updater.apiBase = server.URL
	updater.client = server.Client()

	// Execute refresh
	err := updater.refreshToken(context.Background())
	if err != nil {
		t.Fatalf("refreshToken() error = %v", err)
	}

	// Verify token was updated
	if cfg.Trakt.AccessToken != "new-access-token" {
		t.Errorf("AccessToken not updated, got %s", cfg.Trakt.AccessToken)
	}
	if cfg.Trakt.RefreshToken != "new-refresh-token" {
		t.Errorf("RefreshToken not updated, got %s", cfg.Trakt.RefreshToken)
	}
	if cfg.Trakt.TokenExpiresAt.Before(time.Now()) {
		t.Errorf("TokenExpiresAt not updated correctly")
	}
}

func TestTraktUpdater_InvalidSourceID(t *testing.T) {
	cfg := &config.Config{
		Trakt: config.TraktConfig{
			ClientID:    "test-client-id",
			AccessToken: "test-token",
		},
	}
	logger := slog.Default()
	updater := NewTraktUpdater(cfg, logger)

	entry := &db.Entry{
		ID:        "test-entry-invalid",
		Source:    "trakt",
		SourceID:  "invalid-id",
		Title:     "Test Movie",
		MediaType: "movie",
	}

	// Should not return error (platform failures don't fail callback)
	err := updater.MarkOwned(context.Background(), entry)
	if err != nil {
		t.Errorf("MarkOwned() should not return error for invalid source_id, got %v", err)
	}
}
