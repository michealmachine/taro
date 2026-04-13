package downloader

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

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

func TestPollingTask_Timeout(t *testing.T) {
	task := &PollingTask{
		EntryID:    "test-entry",
		TaskID:     "test-task",
		SubmitTime: time.Now().Add(-25 * time.Hour), // 25 hours ago
	}

	if time.Since(task.SubmitTime) <= 24*time.Hour {
		t.Errorf("expected task to be timed out")
	}
}

func TestPollingTask_NotTimeout(t *testing.T) {
	task := &PollingTask{
		EntryID:    "test-entry",
		TaskID:     "test-task",
		SubmitTime: time.Now().Add(-1 * time.Hour), // 1 hour ago
	}

	if time.Since(task.SubmitTime) > 24*time.Hour {
		t.Errorf("expected task not to be timed out")
	}
}

func TestAddRemoveQueue(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	cfg := &config.Config{
		PikPak: config.PikPakConfig{
			Username: "test@example.com",
			Password: "test-password",
		},
	}
	database := setupTestDB(t)
	defer database.Close()
	sm := state.NewStateMachine(database, logger)

	// Note: We can't actually create a downloader without valid PikPak credentials
	// So we'll test the queue management logic separately
	downloader := &PikPakDownloader{
		logger:       logger,
		config:       cfg,
		database:     database,
		sm:           sm,
		pollingQueue: make(map[string]*PollingTask),
		stopChan:     make(chan struct{}),
	}

	// Test add to queue
	downloader.addToQueue("entry-1", "task-1", time.Now())
	downloader.addToQueue("entry-2", "task-2", time.Now())

	downloader.queueMu.RLock()
	if len(downloader.pollingQueue) != 2 {
		t.Errorf("expected 2 tasks in queue, got %d", len(downloader.pollingQueue))
	}
	downloader.queueMu.RUnlock()

	// Test remove from queue
	downloader.removeFromQueue("entry-1")

	downloader.queueMu.RLock()
	if len(downloader.pollingQueue) != 1 {
		t.Errorf("expected 1 task in queue after removal, got %d", len(downloader.pollingQueue))
	}
	if _, exists := downloader.pollingQueue["entry-2"]; !exists {
		t.Errorf("expected entry-2 to still be in queue")
	}
	downloader.queueMu.RUnlock()
}

func TestResumePolling(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	cfg := &config.Config{
		PikPak: config.PikPakConfig{
			Username: "test@example.com",
			Password: "test-password",
		},
	}
	database := setupTestDB(t)
	defer database.Close()
	sm := state.NewStateMachine(database, logger)

	downloader := &PikPakDownloader{
		logger:       logger,
		config:       cfg,
		database:     database,
		sm:           sm,
		pollingQueue: make(map[string]*PollingTask),
		stopChan:     make(chan struct{}),
	}

	ctx := context.Background()

	// Create test entries in pending state first
	entry1 := &db.Entry{
		Title:     "Test Entry 1",
		MediaType: "movie",
		Status:    string(state.StatusPending),
		Source:    "manual",
		SourceID:  "test-1",
	}
	if err := database.CreateEntry(ctx, entry1); err != nil {
		t.Fatalf("failed to create entry: %v", err)
	}

	// Transition to downloading with PikPak task ID
	if err := sm.Transition(ctx, entry1.ID, state.StatusSearching, "test"); err != nil {
		t.Fatalf("failed to transition to searching: %v", err)
	}
	if err := sm.Transition(ctx, entry1.ID, state.StatusFound, "test"); err != nil {
		t.Fatalf("failed to transition to found: %v", err)
	}
	if err := sm.TransitionWithUpdate(ctx, entry1.ID, state.StatusDownloading, map[string]any{
		"pikpak_task_id": "task-123",
		"reason":         "test",
	}); err != nil {
		t.Fatalf("failed to transition to downloading: %v", err)
	}

	// Resume polling
	if err := downloader.ResumePolling(ctx); err != nil {
		t.Fatalf("ResumePolling() error = %v", err)
	}

	// Verify task was added to queue
	downloader.queueMu.RLock()
	if len(downloader.pollingQueue) != 1 {
		t.Errorf("expected 1 task in queue after resume, got %d", len(downloader.pollingQueue))
	}
	task, exists := downloader.pollingQueue[entry1.ID]
	if !exists {
		t.Errorf("expected entry to be in queue")
	}
	if task.TaskID != "task-123" {
		t.Errorf("expected task_id to be 'task-123', got %q", task.TaskID)
	}
	downloader.queueMu.RUnlock()
}

func TestResumeEntryPolling(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	cfg := &config.Config{}
	database := setupTestDB(t)
	defer database.Close()
	sm := state.NewStateMachine(database, logger)

	downloader := &PikPakDownloader{
		logger:       logger,
		config:       cfg,
		database:     database,
		sm:           sm,
		pollingQueue: make(map[string]*PollingTask),
		stopChan:     make(chan struct{}),
	}

	now := time.Now()
	if err := downloader.ResumeEntryPolling("entry-1", "task-1", now); err != nil {
		t.Fatalf("ResumeEntryPolling() error = %v", err)
	}

	downloader.queueMu.RLock()
	defer downloader.queueMu.RUnlock()

	if len(downloader.pollingQueue) != 1 {
		t.Fatalf("expected 1 task in queue, got %d", len(downloader.pollingQueue))
	}
	if downloader.pollingQueue["entry-1"].TaskID != "task-1" {
		t.Fatalf("expected task_id task-1, got %s", downloader.pollingQueue["entry-1"].TaskID)
	}
}

// Note: Integration tests with actual PikPak API would require valid credentials
// and should be run separately with environment variables
