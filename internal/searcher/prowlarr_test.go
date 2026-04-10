package searcher

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/michealmachine/taro/internal/config"
	"github.com/michealmachine/taro/internal/db"
	"github.com/michealmachine/taro/internal/state"
)

func setupTestDB(t *testing.T) *db.DB {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("failed to create test database: %v", err)
	}
	return database
}

func TestBuildSearchQuery(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	cfg := &config.Config{
		Prowlarr: config.ProwlarrConfig{
			URL:    "http://localhost:9696",
			APIKey: "test-key",
		},
	}
	database := setupTestDB(t)
	defer database.Close()
	sm := state.NewStateMachine(database)
	s := NewSearcher(cfg, database, sm, logger)

	tests := []struct {
		name     string
		entry    *db.Entry
		expected string
	}{
		{
			name: "anime with season",
			entry: &db.Entry{
				Title:     "進撃の巨人",
				MediaType: "anime",
				Season:    1,
			},
			expected: "進撃の巨人 S01",
		},
		{
			name: "tv show with season 2",
			entry: &db.Entry{
				Title:     "Breaking Bad",
				MediaType: "tv",
				Season:    2,
			},
			expected: "Breaking Bad S02",
		},
		{
			name: "movie",
			entry: &db.Entry{
				Title:     "Inception",
				MediaType: "movie",
			},
			expected: "Inception",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := s.buildSearchQuery(tt.entry)
			if result != tt.expected {
				t.Errorf("buildSearchQuery() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestExtractResolution(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	cfg := &config.Config{
		Prowlarr: config.ProwlarrConfig{
			URL:    "http://localhost:9696",
			APIKey: "test-key",
		},
	}
	database := setupTestDB(t)
	defer database.Close()
	sm := state.NewStateMachine(database)
	s := NewSearcher(cfg, database, sm, logger)

	tests := []struct {
		name     string
		title    string
		expected string
	}{
		{
			name:     "1080p in title",
			title:    "[SubsPlease] Attack on Titan - 01 (1080p) [ABCD1234].mkv",
			expected: "1080p",
		},
		{
			name:     "1080i in title",
			title:    "Breaking Bad S01E01 1080i HDTV",
			expected: "1080i",
		},
		{
			name:     "720p in title",
			title:    "Inception.2010.720p.BluRay.x264",
			expected: "720p",
		},
		{
			name:     "480p in title",
			title:    "Old Movie 480p WEB-DL",
			expected: "480p",
		},
		{
			name:     "no resolution",
			title:    "Some Random Title Without Resolution",
			expected: "other",
		},
		{
			name:     "case insensitive",
			title:    "Movie Title 1080P BluRay",
			expected: "1080P",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := s.extractResolution(tt.title)
			if result != tt.expected {
				t.Errorf("extractResolution(%q) = %q, want %q", tt.title, result, tt.expected)
			}
		})
	}
}

func TestExtractCodec(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	cfg := &config.Config{
		Prowlarr: config.ProwlarrConfig{
			URL:    "http://localhost:9696",
			APIKey: "test-key",
		},
	}
	database := setupTestDB(t)
	defer database.Close()
	sm := state.NewStateMachine(database)
	s := NewSearcher(cfg, database, sm, logger)

	tests := []struct {
		name     string
		title    string
		expected string
	}{
		{
			name:     "x264 codec",
			title:    "Movie.2020.1080p.BluRay.x264-GROUP",
			expected: "x264",
		},
		{
			name:     "x265 codec",
			title:    "Movie.2020.1080p.WEB-DL.x265-GROUP",
			expected: "x265",
		},
		{
			name:     "HEVC codec",
			title:    "Movie.2020.1080p.BluRay.HEVC-GROUP",
			expected: "x265",
		},
		{
			name:     "H.264 codec",
			title:    "Movie.2020.1080p.H.264-GROUP",
			expected: "x264",
		},
		{
			name:     "H.265 codec",
			title:    "Movie.2020.1080p.H.265-GROUP",
			expected: "x265",
		},
		{
			name:     "AV1 codec",
			title:    "Movie.2020.1080p.WEB-DL.AV1-GROUP",
			expected: "av1",
		},
		{
			name:     "AVC codec",
			title:    "Movie.2020.1080p.BluRay.AVC-GROUP",
			expected: "x264",
		},
		{
			name:     "no codec",
			title:    "Movie.2020.1080p.BluRay",
			expected: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := s.extractCodec(tt.title)
			if result != tt.expected {
				t.Errorf("extractCodec(%q) = %q, want %q", tt.title, result, tt.expected)
			}
		})
	}
}

func TestCodecFiltering(t *testing.T) {
	// Create mock Prowlarr server
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := ProwlarrSearchResponse{
			{
				Title:     "Test Anime 1080p x264",
				GUID:      "guid-1",
				MagnetURL: "magnet:?xt=urn:btih:1234567890",
				Size:      1024 * 1024 * 500,
				Seeders:   100,
				Indexer:   "Nyaa",
			},
			{
				Title:     "Test Anime 1080p AV1",
				GUID:      "guid-2",
				MagnetURL: "magnet:?xt=urn:btih:0987654321",
				Size:      1024 * 1024 * 300,
				Seeders:   150,
			},
			{
				Title:     "Test Anime 1080p x265",
				GUID:      "guid-3",
				MagnetURL: "magnet:?xt=urn:btih:1111111111",
				Size:      1024 * 1024 * 400,
				Seeders:   80,
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer mockServer.Close()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	cfg := &config.Config{
		Prowlarr: config.ProwlarrConfig{
			URL:    mockServer.URL,
			APIKey: "test-key",
		},
		Defaults: config.DefaultsConfig{
			ExcludedCodecs: []string{"av1", "x265"},
		},
	}
	database := setupTestDB(t)
	defer database.Close()
	sm := state.NewStateMachine(database)
	s := NewSearcher(cfg, database, sm, logger)

	ctx := context.Background()

	// Create test entry with ask_mode=2 (auto-select)
	entry := &db.Entry{
		Title:     "Test Anime",
		MediaType: "anime",
		Season:    1,
		Status:    string(state.StatusPending),
		Source:    "manual",
		SourceID:  "test-1",
		AskMode:   2,
	}
	if err := database.CreateEntry(ctx, entry); err != nil {
		t.Fatalf("failed to create test entry: %v", err)
	}

	// Search should filter out AV1 and x265
	err := s.Search(ctx, entry)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}

	// Verify only x264 resource was saved
	resources, err := database.ListResourcesByEntry(ctx, entry.ID)
	if err != nil {
		t.Fatalf("failed to list resources: %v", err)
	}
	if len(resources) != 1 {
		t.Errorf("got %d resources, want 1 (AV1 and x265 should be filtered)", len(resources))
	}

	// Verify the remaining resource is x264
	if len(resources) > 0 && !strings.Contains(resources[0].Title, "x264") {
		t.Errorf("expected x264 resource, got %q", resources[0].Title)
	}
}

func TestSelectBestResource(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	cfg := &config.Config{
		Prowlarr: config.ProwlarrConfig{
			URL:    "http://localhost:9696",
			APIKey: "test-key",
		},
	}
	database := setupTestDB(t)
	defer database.Close()
	sm := state.NewStateMachine(database)
	s := NewSearcher(cfg, database, sm, logger)

	tests := []struct {
		name       string
		entry      *db.Entry
		resources  []*db.Resource
		expectedID string // ID of expected best resource
	}{
		{
			name: "prefer 1080p over 720p",
			entry: &db.Entry{
				Resolution: sql.NullString{String: "1080p", Valid: true},
			},
			resources: []*db.Resource{
				{ID: "1", Resolution: sql.NullString{String: "720p", Valid: true}, Seeders: sql.NullInt64{Int64: 100, Valid: true}},
				{ID: "2", Resolution: sql.NullString{String: "1080p", Valid: true}, Seeders: sql.NullInt64{Int64: 50, Valid: true}},
			},
			expectedID: "2", // 1080p wins despite fewer seeders
		},
		{
			name: "prefer more seeders with same resolution",
			entry: &db.Entry{
				Resolution: sql.NullString{String: "1080p", Valid: true},
			},
			resources: []*db.Resource{
				{ID: "1", Resolution: sql.NullString{String: "1080p", Valid: true}, Seeders: sql.NullInt64{Int64: 50, Valid: true}},
				{ID: "2", Resolution: sql.NullString{String: "1080p", Valid: true}, Seeders: sql.NullInt64{Int64: 200, Valid: true}},
			},
			expectedID: "2", // More seeders wins
		},
		{
			name:  "resolution priority: 1080p > 1080i > 720p",
			entry: &db.Entry{},
			resources: []*db.Resource{
				{ID: "1", Resolution: sql.NullString{String: "720p", Valid: true}, Seeders: sql.NullInt64{Int64: 100, Valid: true}},
				{ID: "2", Resolution: sql.NullString{String: "1080i", Valid: true}, Seeders: sql.NullInt64{Int64: 50, Valid: true}},
				{ID: "3", Resolution: sql.NullString{String: "1080p", Valid: true}, Seeders: sql.NullInt64{Int64: 10, Valid: true}},
			},
			expectedID: "3", // 1080p wins
		},
		{
			name:       "empty resources",
			entry:      &db.Entry{},
			resources:  []*db.Resource{},
			expectedID: "", // nil expected
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := s.selectBestResource(tt.entry, tt.resources)
			if tt.expectedID == "" {
				if result != nil {
					t.Errorf("selectBestResource() = %v, want nil", result)
				}
			} else {
				if result == nil {
					t.Fatalf("selectBestResource() = nil, want resource %q", tt.expectedID)
				}
				if result.ID != tt.expectedID {
					t.Errorf("selectBestResource() = resource %q, want resource %q", result.ID, tt.expectedID)
				}
			}
		})
	}
}

func TestSearchProwlarr(t *testing.T) {
	// Create mock Prowlarr server
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request
		if r.Header.Get("X-Api-Key") != "test-key" {
			t.Errorf("missing or incorrect API key header")
		}
		if r.Header.Get("Accept") != "application/json" {
			t.Errorf("missing or incorrect Accept header")
		}

		query := r.URL.Query().Get("query")
		if query == "" {
			t.Errorf("missing query parameter")
		}

		// Return mock response
		response := ProwlarrSearchResponse{
			{
				Title:     "[SubsPlease] Attack on Titan - 01 (1080p) [ABCD1234].mkv",
				GUID:      "guid-1",
				MagnetURL: "magnet:?xt=urn:btih:1234567890",
				Size:      1024 * 1024 * 500, // 500MB
				Seeders:   100,
				IndexerID: 1,
				Indexer:   "Nyaa",
			},
			{
				Title:       "[HorribleSubs] Attack on Titan - 01 (720p).mkv",
				GUID:        "guid-2",
				DownloadURL: "http://example.com/download/2",
				Size:        1024 * 1024 * 300, // 300MB
				Seeders:     50,
				IndexerID:   2,
				Indexer:     "TokyoTosho",
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer mockServer.Close()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	cfg := &config.Config{
		Prowlarr: config.ProwlarrConfig{
			URL:    mockServer.URL,
			APIKey: "test-key",
		},
	}
	database := setupTestDB(t)
	defer database.Close()
	sm := state.NewStateMachine(database)
	s := NewSearcher(cfg, database, sm, logger)

	ctx := context.Background()
	results, err := s.searchProwlarr(ctx, "Attack on Titan S01")
	if err != nil {
		t.Fatalf("searchProwlarr() error = %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("searchProwlarr() returned %d results, want 2", len(results))
	}

	// Verify first result
	if results[0].Title != "[SubsPlease] Attack on Titan - 01 (1080p) [ABCD1234].mkv" {
		t.Errorf("results[0].Title = %q, want %q", results[0].Title, "[SubsPlease] Attack on Titan - 01 (1080p) [ABCD1234].mkv")
	}
	if results[0].Magnet != "magnet:?xt=urn:btih:1234567890" {
		t.Errorf("results[0].Magnet = %q, want %q", results[0].Magnet, "magnet:?xt=urn:btih:1234567890")
	}
	if results[0].Seeders != 100 {
		t.Errorf("results[0].Seeders = %d, want 100", results[0].Seeders)
	}

	// Verify second result (should use DownloadURL as fallback)
	if results[1].Magnet != "http://example.com/download/2" {
		t.Errorf("results[1].Magnet = %q, want %q", results[1].Magnet, "http://example.com/download/2")
	}
}

func TestSearchNoResults(t *testing.T) {
	// Create mock Prowlarr server that returns empty results
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ProwlarrSearchResponse{})
	}))
	defer mockServer.Close()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	cfg := &config.Config{
		Prowlarr: config.ProwlarrConfig{
			URL:    mockServer.URL,
			APIKey: "test-key",
		},
	}
	database := setupTestDB(t)
	defer database.Close()
	sm := state.NewStateMachine(database)
	s := NewSearcher(cfg, database, sm, logger)

	ctx := context.Background()

	// Create test entry
	entry := &db.Entry{
		Title:     "NonExistentAnime",
		MediaType: "anime",
		Season:    1,
		Status:    string(state.StatusPending),
		Source:    "manual",
		SourceID:  "test-1",
	}
	if err := database.CreateEntry(ctx, entry); err != nil {
		t.Fatalf("failed to create test entry: %v", err)
	}

	// Search should transition to failed
	err := s.Search(ctx, entry)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}

	// Verify entry status is failed
	updatedEntry, err := database.GetEntry(ctx, entry.ID)
	if err != nil {
		t.Fatalf("failed to get updated entry: %v", err)
	}

	if updatedEntry.Status != string(state.StatusFailed) {
		t.Errorf("entry status = %q, want %q", updatedEntry.Status, state.StatusFailed)
	}
	if !updatedEntry.FailedStage.Valid || updatedEntry.FailedStage.String != "searching" {
		t.Errorf("entry failed_stage = %q, want %q", updatedEntry.FailedStage.String, "searching")
	}
}

func TestSearchAutoSelect(t *testing.T) {
	// Create mock Prowlarr server
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := ProwlarrSearchResponse{
			{
				Title:     "Test Anime 1080p",
				GUID:      "guid-1",
				MagnetURL: "magnet:?xt=urn:btih:1234567890",
				Size:      1024 * 1024 * 500,
				Seeders:   100,
				Indexer:   "Nyaa",
			},
			{
				Title:     "Test Anime 720p",
				GUID:      "guid-2",
				MagnetURL: "magnet:?xt=urn:btih:0987654321",
				Size:      1024 * 1024 * 300,
				Seeders:   50,
				Indexer:   "Nyaa",
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer mockServer.Close()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	cfg := &config.Config{
		Prowlarr: config.ProwlarrConfig{
			URL:    mockServer.URL,
			APIKey: "test-key",
		},
	}
	database := setupTestDB(t)
	defer database.Close()
	sm := state.NewStateMachine(database)
	s := NewSearcher(cfg, database, sm, logger)

	ctx := context.Background()

	// Create test entry with ask_mode=2 (auto-select)
	entry := &db.Entry{
		Title:     "Test Anime",
		MediaType: "anime",
		Season:    1,
		Status:    string(state.StatusPending),
		Source:    "manual",
		SourceID:  "test-1",
		AskMode:   2,
	}
	if err := database.CreateEntry(ctx, entry); err != nil {
		t.Fatalf("failed to create test entry: %v", err)
	}

	// Search should auto-select best resource
	err := s.Search(ctx, entry)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}

	// Verify entry status is found
	updatedEntry, err := database.GetEntry(ctx, entry.ID)
	if err != nil {
		t.Fatalf("failed to get updated entry: %v", err)
	}

	if updatedEntry.Status != string(state.StatusFound) {
		t.Errorf("entry status = %q, want %q", updatedEntry.Status, state.StatusFound)
	}

	// Verify selected_resource_id is set
	if !updatedEntry.SelectedResourceID.Valid || updatedEntry.SelectedResourceID.String == "" {
		t.Errorf("selected_resource_id not set")
	}

	// Verify resources were saved
	resources, err := database.ListResourcesByEntry(ctx, entry.ID)
	if err != nil {
		t.Fatalf("failed to list resources: %v", err)
	}
	if len(resources) != 2 {
		t.Errorf("got %d resources, want 2", len(resources))
	}

	// Verify best resource was selected (1080p)
	selectedResource, err := database.GetResource(ctx, updatedEntry.SelectedResourceID.String)
	if err != nil {
		t.Fatalf("failed to get selected resource: %v", err)
	}
	if selectedResource.Resolution.String != "1080p" {
		t.Errorf("selected resource resolution = %q, want %q", selectedResource.Resolution.String, "1080p")
	}
}
