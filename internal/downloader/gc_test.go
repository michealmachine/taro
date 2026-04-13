package downloader

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/michealmachine/taro/internal/config"
	"github.com/michealmachine/taro/internal/db"
	"github.com/michealmachine/taro/internal/state"
)

func TestGarbageCollector_CleanPikPakFiles(t *testing.T) {
	// Setup test database
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("failed to create test database: %v", err)
	}
	defer database.Close()

	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// Create test config
	cfg := &config.Config{
		PikPak: config.PikPakConfig{
			Username:        "test@example.com",
			Password:        "testpass",
			GCRetentionDays: 7,
		},
	}

	// Use nil downloader to simulate deletion unavailable path.
	// With current logic, entries should NOT be marked cleaned unless deletion succeeds.
	var downloader *PikPakDownloader

	// Create GC
	gc := NewGarbageCollector(downloader, database, cfg, logger)

	// Test Case 1: Entry with pikpak_file_id, failed status, old failed_at
	oldFailedEntry := &db.Entry{
		ID:            "entry-1",
		Title:         "Old Failed Entry",
		MediaType:     "anime",
		Source:        "manual",
		SourceID:      "test-1",
		Season:        1,
		Status:        string(state.StatusFailed),
		PikPakFileID:  sql.NullString{String: "file-123", Valid: true},
		PikPakCleaned: false,
		FailedAt:      sql.NullTime{Time: time.Now().Add(-10 * 24 * time.Hour), Valid: true},
		CreatedAt:     time.Now().Add(-10 * 24 * time.Hour),
		UpdatedAt:     time.Now().Add(-10 * 24 * time.Hour),
	}
	if err := database.CreateEntry(ctx, oldFailedEntry); err != nil {
		t.Fatalf("failed to create test entry: %v", err)
	}

	// Test Case 2: Entry with pikpak_file_id, cancelled status, old updated_at
	oldCancelledEntry := &db.Entry{
		ID:            "entry-2",
		Title:         "Old Cancelled Entry",
		MediaType:     "anime",
		Source:        "manual",
		SourceID:      "test-2",
		Season:        1,
		Status:        string(state.StatusCancelled),
		PikPakFileID:  sql.NullString{String: "file-456", Valid: true},
		PikPakCleaned: false,
		CreatedAt:     time.Now().Add(-10 * 24 * time.Hour),
		UpdatedAt:     time.Now().Add(-10 * 24 * time.Hour),
	}
	if err := database.CreateEntry(ctx, oldCancelledEntry); err != nil {
		t.Fatalf("failed to create test entry: %v", err)
	}
	// Force updated_at to old time (CreateEntry overwrites it)
	if _, err := database.ExecContext(ctx, `UPDATE entries SET updated_at = ? WHERE id = ?`,
		time.Now().Add(-10*24*time.Hour), "entry-2"); err != nil {
		t.Fatalf("failed to set updated_at: %v", err)
	}

	// Test Case 3: Entry with pikpak_file_id, failed status, recent failed_at (should NOT be cleaned)
	recentFailedEntry := &db.Entry{
		ID:            "entry-3",
		Title:         "Recent Failed Entry",
		MediaType:     "anime",
		Source:        "manual",
		SourceID:      "test-3",
		Season:        1,
		Status:        string(state.StatusFailed),
		PikPakFileID:  sql.NullString{String: "file-789", Valid: true},
		PikPakCleaned: false,
		FailedAt:      sql.NullTime{Time: time.Now().Add(-3 * 24 * time.Hour), Valid: true},
		CreatedAt:     time.Now().Add(-3 * 24 * time.Hour),
		UpdatedAt:     time.Now().Add(-3 * 24 * time.Hour),
	}
	if err := database.CreateEntry(ctx, recentFailedEntry); err != nil {
		t.Fatalf("failed to create test entry: %v", err)
	}

	// Test Case 4: Entry already cleaned (should be idempotent)
	alreadyCleanedEntry := &db.Entry{
		ID:            "entry-4",
		Title:         "Already Cleaned Entry",
		MediaType:     "anime",
		Source:        "manual",
		SourceID:      "test-4",
		Season:        1,
		Status:        string(state.StatusFailed),
		PikPakFileID:  sql.NullString{String: "file-999", Valid: true},
		PikPakCleaned: true, // Already cleaned
		FailedAt:      sql.NullTime{Time: time.Now().Add(-10 * 24 * time.Hour), Valid: true},
		CreatedAt:     time.Now().Add(-10 * 24 * time.Hour),
		UpdatedAt:     time.Now().Add(-10 * 24 * time.Hour),
	}
	if err := database.CreateEntry(ctx, alreadyCleanedEntry); err != nil {
		t.Fatalf("failed to create test entry: %v", err)
	}

	// Run cleanup
	if err := gc.cleanPikPakFiles(ctx); err != nil {
		t.Logf("cleanPikPakFiles returned error: %v", err)
	}

	// Verify entry-1 and entry-2 are NOT marked as cleaned (deletion did not run)
	entry1, _ := database.GetEntry(ctx, "entry-1")
	if entry1.PikPakCleaned {
		t.Errorf("entry-1 should NOT be marked as cleaned without successful deletion")
	}

	entry2, _ := database.GetEntry(ctx, "entry-2")
	if entry2.PikPakCleaned {
		t.Errorf("entry-2 should NOT be marked as cleaned without successful deletion")
	}

	// Verify entry-3 is NOT marked as cleaned (too recent)
	entry3, _ := database.GetEntry(ctx, "entry-3")
	if entry3.PikPakCleaned {
		t.Errorf("entry-3 should NOT be marked as cleaned (too recent)")
	}

	// Verify entry-4 remains cleaned
	entry4, _ := database.GetEntry(ctx, "entry-4")
	if !entry4.PikPakCleaned {
		t.Errorf("entry-4 should remain cleaned")
	}

	// Run cleanup again (idempotent test)
	if err := gc.cleanPikPakFiles(ctx); err != nil {
		t.Logf("second cleanPikPakFiles returned error: %v", err)
	}

	// Verify entries remain in same state
	entry1Again, _ := database.GetEntry(ctx, "entry-1")
	if entry1Again.PikPakCleaned {
		t.Errorf("entry-1 should still NOT be marked as cleaned after second run")
	}
}

func TestGarbageCollector_CleanResources(t *testing.T) {
	// Setup test database
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("failed to create test database: %v", err)
	}
	defer database.Close()

	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// Create test config with resource cleanup enabled
	cfg := &config.Config{
		PikPak: config.PikPakConfig{
			Username: "test@example.com",
			Password: "testpass",
		},
		Retention: config.RetentionConfig{
			CleanResourcesOnComplete: true,
		},
	}

	// Create state machine
	sm := state.NewStateMachine(database, logger)

	// Create downloader
	downloader, _ := NewPikPakDownloader(cfg, database, sm, logger)

	// Create GC
	gc := NewGarbageCollector(downloader, database, cfg, logger)

	// Create terminal state entry (in_library)
	terminalEntry := &db.Entry{
		ID:        "entry-terminal",
		Title:     "Terminal Entry",
		MediaType: "anime",
		Source:    "manual",
		SourceID:  "test-terminal",
		Season:    1,
		Status:    string(state.StatusInLibrary),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := database.CreateEntry(ctx, terminalEntry); err != nil {
		t.Fatalf("failed to create terminal entry: %v", err)
	}

	// Create resources for terminal entry
	resources := []*db.Resource{
		{
			EntryID:  "entry-terminal",
			Title:    "Resource 1",
			Magnet:   "magnet:?xt=urn:btih:1",
			Eligible: true,
		},
		{
			EntryID:  "entry-terminal",
			Title:    "Resource 2",
			Magnet:   "magnet:?xt=urn:btih:2",
			Eligible: true,
		},
	}
	if err := database.BatchCreateResources(ctx, resources); err != nil {
		t.Fatalf("failed to create resources: %v", err)
	}

	// Create non-terminal entry (downloading)
	nonTerminalEntry := &db.Entry{
		ID:        "entry-non-terminal",
		Title:     "Non-Terminal Entry",
		MediaType: "anime",
		Source:    "manual",
		SourceID:  "test-non-terminal",
		Season:    1,
		Status:    string(state.StatusDownloading),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := database.CreateEntry(ctx, nonTerminalEntry); err != nil {
		t.Fatalf("failed to create non-terminal entry: %v", err)
	}

	// Create resources for non-terminal entry
	nonTerminalResources := []*db.Resource{
		{
			EntryID:  "entry-non-terminal",
			Title:    "Resource 3",
			Magnet:   "magnet:?xt=urn:btih:3",
			Eligible: true,
		},
	}
	if err := database.BatchCreateResources(ctx, nonTerminalResources); err != nil {
		t.Fatalf("failed to create resources: %v", err)
	}

	// Run resource cleanup
	if err := gc.cleanResources(ctx); err != nil {
		t.Fatalf("cleanResources failed: %v", err)
	}

	// Verify terminal entry resources are deleted
	terminalResources, err := database.ListResourcesByEntry(ctx, "entry-terminal")
	if err != nil {
		t.Fatalf("failed to list terminal resources: %v", err)
	}
	if len(terminalResources) != 0 {
		t.Errorf("expected 0 resources for terminal entry, got %d", len(terminalResources))
	}

	// Verify non-terminal entry resources are NOT deleted
	nonTerminalResourcesAfter, err := database.ListResourcesByEntry(ctx, "entry-non-terminal")
	if err != nil {
		t.Fatalf("failed to list non-terminal resources: %v", err)
	}
	if len(nonTerminalResourcesAfter) != 1 {
		t.Errorf("expected 1 resource for non-terminal entry, got %d", len(nonTerminalResourcesAfter))
	}
}

func TestGarbageCollector_CleanStateLogs(t *testing.T) {
	// Setup test database
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("failed to create test database: %v", err)
	}
	defer database.Close()

	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// Create test config with state log retention
	cfg := &config.Config{
		PikPak: config.PikPakConfig{
			Username: "test@example.com",
			Password: "testpass",
		},
		Retention: config.RetentionConfig{
			StateLogsDays: 7,
		},
	}

	// Create state machine
	sm := state.NewStateMachine(database, logger)

	// Create downloader
	downloader, _ := NewPikPakDownloader(cfg, database, sm, logger)

	// Create GC
	gc := NewGarbageCollector(downloader, database, cfg, logger)

	// Create test entry
	entry := &db.Entry{
		ID:        "entry-logs",
		Title:     "Entry with Logs",
		MediaType: "anime",
		Source:    "manual",
		SourceID:  "test-logs",
		Season:    1,
		Status:    string(state.StatusPending),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := database.CreateEntry(ctx, entry); err != nil {
		t.Fatalf("failed to create entry: %v", err)
	}

	// Create old state log (should be deleted)
	oldLog := &db.StateLog{
		EntryID:    "entry-logs",
		FromStatus: string(state.StatusPending),
		ToStatus:   string(state.StatusSearching),
		Reason:     sql.NullString{String: "test transition", Valid: true},
		CreatedAt:  time.Now().Add(-10 * 24 * time.Hour),
	}
	if err := database.CreateStateLog(ctx, oldLog); err != nil {
		t.Fatalf("failed to create old state log: %v", err)
	}
	// Force created_at to old time (CreateStateLog overwrites it)
	if _, err := database.ExecContext(ctx, `UPDATE state_logs SET created_at = ? WHERE id = ?`,
		time.Now().Add(-10*24*time.Hour), oldLog.ID); err != nil {
		t.Fatalf("failed to set created_at: %v", err)
	}

	// Create recent state log (should NOT be deleted)
	recentLog := &db.StateLog{
		EntryID:    "entry-logs",
		FromStatus: string(state.StatusSearching),
		ToStatus:   string(state.StatusFound),
		Reason:     sql.NullString{String: "test transition", Valid: true},
		CreatedAt:  time.Now().Add(-3 * 24 * time.Hour),
	}
	if err := database.CreateStateLog(ctx, recentLog); err != nil {
		t.Fatalf("failed to create recent state log: %v", err)
	}

	// Run state log cleanup
	if err := gc.cleanStateLogs(ctx); err != nil {
		t.Fatalf("cleanStateLogs failed: %v", err)
	}

	// Verify old log is deleted and recent log remains
	logs, err := database.ListStateLogsByEntry(ctx, "entry-logs")
	if err != nil {
		t.Fatalf("failed to list state logs: %v", err)
	}

	if len(logs) != 1 {
		t.Errorf("expected 1 state log remaining, got %d", len(logs))
	}

	if len(logs) > 0 && logs[0].ToStatus != string(state.StatusFound) {
		t.Errorf("expected recent log to remain, got log with to_status=%s", logs[0].ToStatus)
	}
}

func TestGarbageCollector_CleanStateLogs_KeepForever(t *testing.T) {
	// Setup test database
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("failed to create test database: %v", err)
	}
	defer database.Close()

	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// Create test config with state_logs_days=0 (keep forever)
	cfg := &config.Config{
		PikPak: config.PikPakConfig{
			Username: "test@example.com",
			Password: "testpass",
		},
		Retention: config.RetentionConfig{
			StateLogsDays: 0, // Keep forever
		},
	}

	// Create state machine
	sm := state.NewStateMachine(database, logger)

	// Create downloader
	downloader, _ := NewPikPakDownloader(cfg, database, sm, logger)

	// Create GC
	gc := NewGarbageCollector(downloader, database, cfg, logger)

	// Create test entry
	entry := &db.Entry{
		ID:        "entry-logs-forever",
		Title:     "Entry with Logs",
		MediaType: "anime",
		Source:    "manual",
		SourceID:  "test-logs-forever",
		Season:    1,
		Status:    string(state.StatusPending),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := database.CreateEntry(ctx, entry); err != nil {
		t.Fatalf("failed to create entry: %v", err)
	}

	// Create very old state log
	oldLog := &db.StateLog{
		EntryID:    "entry-logs-forever",
		FromStatus: string(state.StatusPending),
		ToStatus:   string(state.StatusSearching),
		Reason:     sql.NullString{String: "test transition", Valid: true},
		CreatedAt:  time.Now().Add(-365 * 24 * time.Hour), // 1 year old
	}
	if err := database.CreateStateLog(ctx, oldLog); err != nil {
		t.Fatalf("failed to create old state log: %v", err)
	}
	// Force created_at to old time
	if _, err := database.ExecContext(ctx, `UPDATE state_logs SET created_at = ? WHERE id = ?`,
		time.Now().Add(-365*24*time.Hour), oldLog.ID); err != nil {
		t.Fatalf("failed to set created_at: %v", err)
	}

	// Run state log cleanup
	if err := gc.cleanStateLogs(ctx); err != nil {
		t.Fatalf("cleanStateLogs failed: %v", err)
	}

	// Verify log is NOT deleted (retention=0 means keep forever)
	logs, err := database.ListStateLogsByEntry(ctx, "entry-logs-forever")
	if err != nil {
		t.Fatalf("failed to list state logs: %v", err)
	}

	if len(logs) != 1 {
		t.Errorf("expected 1 state log (keep forever), got %d", len(logs))
	}
}
