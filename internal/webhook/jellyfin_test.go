package webhook

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"testing"

	"github.com/michealmachine/taro/internal/db"
	"github.com/michealmachine/taro/internal/state"
)

func TestNormalizePath(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "unix path with trailing slash",
			input:    "/mnt/media/Anime/Title/",
			expected: "/mnt/media/anime/title/",
		},
		{
			name:     "unix path without trailing slash",
			input:    "/mnt/media/Anime/Title",
			expected: "/mnt/media/anime/title/",
		},
		{
			name:     "windows UNC path",
			input:    "\\\\server\\media\\Anime\\Title",
			expected: "/server/media/anime/title/",
		},
		{
			name:     "mixed separators",
			input:    "/mnt/media\\Anime/Title",
			expected: "/mnt/media/anime/title/",
		},
		{
			name:     "consecutive slashes",
			input:    "/mnt//media///Anime/Title/",
			expected: "/mnt/media/anime/title/",
		},
		{
			name:     "uppercase to lowercase",
			input:    "/MNT/MEDIA/ANIME/TITLE/",
			expected: "/mnt/media/anime/title/",
		},
		{
			name:     "mixed case",
			input:    "/Mnt/MeDia/AnImE/TiTlE",
			expected: "/mnt/media/anime/title/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizePath(tt.input)
			if result != tt.expected {
				t.Errorf("normalizePath(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestPathMatching(t *testing.T) {
	tests := []struct {
		name        string
		webhookPath string
		targetPath  string
		shouldMatch bool
	}{
		{
			name:        "exact match",
			webhookPath: "/media/anime/title/season 1/",
			targetPath:  "/media/anime/title/season 1/",
			shouldMatch: true,
		},
		{
			name:        "webhook path is subdirectory",
			webhookPath: "/media/anime/title/season 1/episode1.mkv",
			targetPath:  "/media/anime/title/season 1/",
			shouldMatch: true,
		},
		{
			name:        "case insensitive match",
			webhookPath: "/MEDIA/ANIME/TITLE/SEASON 1/episode1.mkv",
			targetPath:  "/media/anime/title/season 1/",
			shouldMatch: true,
		},
		{
			name:        "different paths",
			webhookPath: "/media/movies/title/",
			targetPath:  "/media/anime/title/",
			shouldMatch: false,
		},
		{
			name:        "partial name match should not match",
			webhookPath: "/media/anime/title2/season 1/",
			targetPath:  "/media/anime/title/season 1/",
			shouldMatch: false,
		},
		{
			name:        "windows vs unix paths",
			webhookPath: "\\\\server\\media\\anime\\title\\season 1\\episode1.mkv",
			targetPath:  "/server/media/anime/title/season 1/",
			shouldMatch: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			normalizedWebhook := normalizePath(tt.webhookPath)
			normalizedTarget := normalizePath(tt.targetPath)

			// Use HasPrefix for matching (same logic as in processItemAdded)
			matched := len(normalizedWebhook) >= len(normalizedTarget) &&
				normalizedWebhook[:len(normalizedTarget)] == normalizedTarget

			if matched != tt.shouldMatch {
				t.Errorf("path matching failed:\n  webhook: %q\n  target:  %q\n  normalized webhook: %q\n  normalized target:  %q\n  got match: %v, want: %v",
					tt.webhookPath, tt.targetPath,
					normalizedWebhook, normalizedTarget,
					matched, tt.shouldMatch)
			}
		})
	}
}

func TestProcessItemAdded_MountPathMapping(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	defer database.Close()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	sm := state.NewStateMachine(database, logger)
	h := NewJellyfinHandler(database, sm, logger)
	h.SetMountPath("/mnt/onedrive")

	entry := &db.Entry{
		ID:         "entry-1",
		Title:      "Attack on Titan",
		MediaType:  "anime",
		Source:     "manual",
		SourceID:   "manual-1",
		Season:     1,
		Status:     string(state.StatusTransferred),
		TargetPath: sql.NullString{String: "/media/anime/attack on titan/season 01/", Valid: true},
	}
	if err := database.CreateEntry(context.Background(), entry); err != nil {
		t.Fatalf("failed to create entry: %v", err)
	}

	payload := &JellyfinItemAddedPayload{
		NotificationType: "ItemAdded",
		ItemType:         "Episode",
		Path:             "/mnt/onedrive/media/anime/Attack On Titan/Season 01/episode01.mkv",
	}

	if err := h.processItemAdded(context.Background(), payload); err != nil {
		t.Fatalf("processItemAdded() failed: %v", err)
	}

	updated, err := database.GetEntry(context.Background(), entry.ID)
	if err != nil {
		t.Fatalf("failed to fetch updated entry: %v", err)
	}

	if updated.Status != string(state.StatusInLibrary) {
		t.Fatalf("expected status %s, got %s", state.StatusInLibrary, updated.Status)
	}
}

func TestProcessItemAdded_OnlyMatchesTransferredAndFirstMatchWins(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	defer database.Close()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	sm := state.NewStateMachine(database, logger)
	h := NewJellyfinHandler(database, sm, logger)
	h.SetMountPath("/mnt/onedrive")

	transferred := &db.Entry{
		ID:         "entry-transferred",
		Title:      "Transferred Show",
		MediaType:  "tv",
		Source:     "manual",
		SourceID:   "s1",
		Season:     1,
		Status:     string(state.StatusTransferred),
		TargetPath: sql.NullString{String: "/media/tv/transferred show/season 01/", Valid: true},
	}
	nonTransferred := &db.Entry{
		ID:         "entry-downloaded",
		Title:      "Downloaded Show",
		MediaType:  "tv",
		Source:     "manual",
		SourceID:   "s2",
		Season:     1,
		Status:     string(state.StatusDownloaded),
		TargetPath: sql.NullString{String: "/media/tv/downloaded show/season 01/", Valid: true},
	}
	for _, entry := range []*db.Entry{transferred, nonTransferred} {
		if err := database.CreateEntry(context.Background(), entry); err != nil {
			t.Fatalf("failed to create entry %s: %v", entry.ID, err)
		}
	}

	payload := &JellyfinItemAddedPayload{
		NotificationType: "ItemAdded",
		ItemType:         "Episode",
		Path:             "/mnt/onedrive/media/tv/Transferred Show/Season 01/Episode01.mkv",
	}

	if err := h.processItemAdded(context.Background(), payload); err != nil {
		t.Fatalf("first processItemAdded() failed: %v", err)
	}

	updatedTransferred, err := database.GetEntry(context.Background(), transferred.ID)
	if err != nil {
		t.Fatalf("failed to fetch transferred entry: %v", err)
	}
	if updatedTransferred.Status != string(state.StatusInLibrary) {
		t.Fatalf("expected transferred entry to become in_library, got %s", updatedTransferred.Status)
	}

	updatedNonTransferred, err := database.GetEntry(context.Background(), nonTransferred.ID)
	if err != nil {
		t.Fatalf("failed to fetch non-transferred entry: %v", err)
	}
	if updatedNonTransferred.Status != string(state.StatusDownloaded) {
		t.Fatalf("expected non-transferred entry to remain downloaded, got %s", updatedNonTransferred.Status)
	}

	// Second episode webhook for same series should not re-match because status is no longer transferred.
	payload.Path = "/mnt/onedrive/media/tv/Transferred Show/Season 01/Episode02.mkv"
	if err := h.processItemAdded(context.Background(), payload); err != nil {
		t.Fatalf("second processItemAdded() failed: %v", err)
	}

	logs, err := database.ListStateLogsByEntry(context.Background(), transferred.ID)
	if err != nil {
		t.Fatalf("failed to list state logs: %v", err)
	}

	inLibraryTransitions := 0
	for _, log := range logs {
		if log.ToStatus == string(state.StatusInLibrary) {
			inLibraryTransitions++
		}
	}
	if inLibraryTransitions != 1 {
		t.Fatalf("expected exactly 1 in_library transition, got %d", inLibraryTransitions)
	}
}
