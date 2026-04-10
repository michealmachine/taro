package state

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/michealmachine/taro/internal/db"
)

func setupTestDB(t *testing.T) *db.DB {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("failed to create test database: %v", err)
	}
	return database
}

func TestStateMachine_ValidTransitions(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	sm := NewStateMachine(database)
	ctx := context.Background()

	// Create a test entry
	entry := &db.Entry{
		Source:    "test",
		SourceID:  "test-123",
		MediaType: "movie",
		Title:     "Test Movie",
		Season:    0,
		Status:    string(StatusPending),
	}
	if err := database.CreateEntry(ctx, entry); err != nil {
		t.Fatalf("failed to create entry: %v", err)
	}

	tests := []struct {
		name    string
		from    EntryStatus
		to      EntryStatus
		wantErr bool
	}{
		{"pending to searching", StatusPending, StatusSearching, false},
		{"searching to found", StatusSearching, StatusFound, false},
		{"searching to needs_selection", StatusSearching, StatusNeedsSelection, false},
		{"searching to failed", StatusSearching, StatusFailed, false},
		{"found to downloading", StatusFound, StatusDownloading, false},
		{"downloading to downloaded", StatusDownloading, StatusDownloaded, false},
		{"downloading to failed", StatusDownloading, StatusFailed, false},
		{"downloaded to transferring", StatusDownloaded, StatusTransferring, false},
		{"transferring to transferred", StatusTransferring, StatusTransferred, false},
		{"transferring to failed", StatusTransferring, StatusFailed, false},
		{"transferred to in_library", StatusTransferred, StatusInLibrary, false},
		{"needs_selection to found", StatusNeedsSelection, StatusFound, false},
		{"needs_selection to cancelled", StatusNeedsSelection, StatusCancelled, false},
		{"failed to pending", StatusFailed, StatusPending, false},
		{"failed to downloaded", StatusFailed, StatusDownloaded, false},
		
		// Invalid transitions
		{"pending to downloaded", StatusPending, StatusDownloaded, true},
		{"found to transferred", StatusFound, StatusTransferred, true},
		{"in_library to pending", StatusInLibrary, StatusPending, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set entry to from status
			entry.Status = string(tt.from)
			if err := database.UpdateEntry(ctx, entry); err != nil {
				t.Fatalf("failed to set entry status: %v", err)
			}

			// Attempt transition
			err := sm.Transition(ctx, entry.ID, tt.to, "test transition")
			if (err != nil) != tt.wantErr {
				t.Errorf("Transition() error = %v, wantErr %v", err, tt.wantErr)
			}

			// Verify status if transition should succeed
			if !tt.wantErr {
				updatedEntry, err := database.GetEntry(ctx, entry.ID)
				if err != nil {
					t.Fatalf("failed to get updated entry: %v", err)
				}
				if updatedEntry.Status != string(tt.to) {
					t.Errorf("expected status %s, got %s", tt.to, updatedEntry.Status)
				}
			}
		})
	}
}

func TestStateMachine_CancelledTransition(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	sm := NewStateMachine(database)
	ctx := context.Background()

	// Create a test entry
	entry := &db.Entry{
		Source:    "test",
		SourceID:  "test-456",
		MediaType: "movie",
		Title:     "Test Movie 2",
		Season:    0,
		Status:    string(StatusPending),
	}
	if err := database.CreateEntry(ctx, entry); err != nil {
		t.Fatalf("failed to create entry: %v", err)
	}

	// Test cancellation from various non-terminal states
	nonTerminalStates := []EntryStatus{
		StatusPending,
		StatusSearching,
		StatusFound,
		StatusDownloading,
		StatusDownloaded,
		StatusTransferring,
		StatusTransferred,
		StatusNeedsSelection,
		StatusFailed,
	}

	for _, from := range nonTerminalStates {
		t.Run(string(from)+" to cancelled", func(t *testing.T) {
			// Set entry to from status
			entry.Status = string(from)
			if err := database.UpdateEntry(ctx, entry); err != nil {
				t.Fatalf("failed to set entry status: %v", err)
			}

			// Attempt cancellation
			err := sm.Transition(ctx, entry.ID, StatusCancelled, "user cancelled")
			if err != nil {
				t.Errorf("Transition() to cancelled from %s failed: %v", from, err)
			}

			// Verify status
			updatedEntry, err := database.GetEntry(ctx, entry.ID)
			if err != nil {
				t.Fatalf("failed to get updated entry: %v", err)
			}
			if updatedEntry.Status != string(StatusCancelled) {
				t.Errorf("expected status cancelled, got %s", updatedEntry.Status)
			}
		})
	}

	// Test that in_library cannot be cancelled
	entry.Status = string(StatusInLibrary)
	if err := database.UpdateEntry(ctx, entry); err != nil {
		t.Fatalf("failed to set entry status: %v", err)
	}
	err := sm.Transition(ctx, entry.ID, StatusCancelled, "user cancelled")
	if err == nil {
		t.Error("expected error when cancelling in_library entry, got nil")
	}
}

func TestStateMachine_AuditLog(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	sm := NewStateMachine(database)
	ctx := context.Background()

	// Create a test entry
	entry := &db.Entry{
		Source:    "test",
		SourceID:  "test-789",
		MediaType: "movie",
		Title:     "Test Movie 3",
		Season:    0,
		Status:    string(StatusPending),
	}
	if err := database.CreateEntry(ctx, entry); err != nil {
		t.Fatalf("failed to create entry: %v", err)
	}

	// Perform several transitions
	transitions := []struct {
		to     EntryStatus
		reason string
	}{
		{StatusSearching, "start search"},
		{StatusFound, "resource found"},
		{StatusDownloading, "start download"},
		{StatusDownloaded, "download complete"},
	}

	for _, tr := range transitions {
		if err := sm.Transition(ctx, entry.ID, tr.to, tr.reason); err != nil {
			t.Fatalf("failed to transition to %s: %v", tr.to, err)
		}
	}

	// Verify audit logs
	logs, err := database.ListStateLogsByEntry(ctx, entry.ID)
	if err != nil {
		t.Fatalf("failed to list state logs: %v", err)
	}

	if len(logs) != len(transitions) {
		t.Errorf("expected %d logs, got %d", len(transitions), len(logs))
	}

	// Verify log content
	expectedTransitions := []struct {
		from string
		to   string
	}{
		{"pending", "searching"},
		{"searching", "found"},
		{"found", "downloading"},
		{"downloading", "downloaded"},
	}

	for i, log := range logs {
		if log.FromStatus != expectedTransitions[i].from {
			t.Errorf("log %d: expected from_status %s, got %s", i, expectedTransitions[i].from, log.FromStatus)
		}
		if log.ToStatus != expectedTransitions[i].to {
			t.Errorf("log %d: expected to_status %s, got %s", i, expectedTransitions[i].to, log.ToStatus)
		}
		if log.Reason.String != transitions[i].reason {
			t.Errorf("log %d: expected reason %s, got %s", i, transitions[i].reason, log.Reason.String)
		}
	}
}

func TestStateMachine_TransitionWithUpdate(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	sm := NewStateMachine(database)
	ctx := context.Background()

	// Create a test entry
	entry := &db.Entry{
		Source:    "test",
		SourceID:  "test-update",
		MediaType: "movie",
		Title:     "Test Movie Update",
		Season:    0,
		Status:    string(StatusFound),
	}
	if err := database.CreateEntry(ctx, entry); err != nil {
		t.Fatalf("failed to create entry: %v", err)
	}

	// Transition with additional updates
	updates := map[string]any{
		"pikpak_task_id": "task-123",
		"reason":         "download started",
	}

	err := sm.TransitionWithUpdate(ctx, entry.ID, StatusDownloading, updates)
	if err != nil {
		t.Fatalf("TransitionWithUpdate() failed: %v", err)
	}

	// Verify entry was updated
	updatedEntry, err := database.GetEntry(ctx, entry.ID)
	if err != nil {
		t.Fatalf("failed to get updated entry: %v", err)
	}

	if updatedEntry.Status != string(StatusDownloading) {
		t.Errorf("expected status downloading, got %s", updatedEntry.Status)
	}
	if updatedEntry.PikPakTaskID.String != "task-123" {
		t.Errorf("expected pikpak_task_id task-123, got %s", updatedEntry.PikPakTaskID.String)
	}
}

func TestStateMachine_FailedAtTimestamp(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	sm := NewStateMachine(database)
	ctx := context.Background()

	// Create a test entry
	entry := &db.Entry{
		Source:    "test",
		SourceID:  "test-failed",
		MediaType: "movie",
		Title:     "Test Failed Movie",
		Season:    0,
		Status:    string(StatusDownloading),
	}
	if err := database.CreateEntry(ctx, entry); err != nil {
		t.Fatalf("failed to create entry: %v", err)
	}

	// Transition to failed
	if err := sm.Transition(ctx, entry.ID, StatusFailed, "download error"); err != nil {
		t.Fatalf("failed to transition to failed: %v", err)
	}

	// Verify failed_at is set
	failedEntry, err := database.GetEntry(ctx, entry.ID)
	if err != nil {
		t.Fatalf("failed to get entry: %v", err)
	}
	if !failedEntry.FailedAt.Valid {
		t.Error("expected failed_at to be set")
	}

	// Transition back to pending (retry)
	time.Sleep(10 * time.Millisecond) // Ensure different timestamp
	if err := sm.Transition(ctx, entry.ID, StatusPending, "retry"); err != nil {
		t.Fatalf("failed to transition to pending: %v", err)
	}

	// Verify failed_at is cleared
	retriedEntry, err := database.GetEntry(ctx, entry.ID)
	if err != nil {
		t.Fatalf("failed to get entry: %v", err)
	}
	if retriedEntry.FailedAt.Valid {
		t.Error("expected failed_at to be cleared after retry")
	}
}

func TestStateMachine_RecoverOnStartup(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	sm := NewStateMachine(database)
	ctx := context.Background()

	// Create entries in various states
	entries := []struct {
		sourceID string
		status   EntryStatus
		taskID   string
	}{
		{"search-1", StatusSearching, ""},
		{"search-2", StatusSearching, ""},
		{"download-1", StatusDownloading, "task-download-1"},
		{"transfer-1", StatusTransferring, "task-transfer-1"},
		{"pending-1", StatusPending, ""},
	}

	for _, e := range entries {
		entry := &db.Entry{
			Source:    "test",
			SourceID:  e.sourceID,
			MediaType: "movie",
			Title:     "Test Movie",
			Season:    0,
			Status:    string(e.status),
		}
		if e.status == StatusDownloading && e.taskID != "" {
			entry.PikPakTaskID = sql.NullString{String: e.taskID, Valid: true}
		}
		if e.status == StatusTransferring && e.taskID != "" {
			entry.TransferTaskID = sql.NullString{String: e.taskID, Valid: true}
		}
		if err := database.CreateEntry(ctx, entry); err != nil {
			t.Fatalf("failed to create entry: %v", err)
		}
	}

	// Track recovered tasks
	downloadingRecovered := []string{}
	transferringRecovered := []string{}

	callbacks := &RecoveryCallbacks{
		OnDownloading: func(entryID, taskID string) error {
			downloadingRecovered = append(downloadingRecovered, taskID)
			return nil
		},
		OnTransferring: func(entryID, taskID string) error {
			transferringRecovered = append(transferringRecovered, taskID)
			return nil
		},
	}

	// Run recovery
	if err := sm.RecoverOnStartup(ctx, callbacks); err != nil {
		t.Fatalf("RecoverOnStartup() failed: %v", err)
	}

	// Verify searching entries were reset to pending
	searchingEntries, err := database.ListEntriesByStatus(ctx, string(StatusSearching))
	if err != nil {
		t.Fatalf("failed to list searching entries: %v", err)
	}
	if len(searchingEntries) != 0 {
		t.Errorf("expected 0 searching entries after recovery, got %d", len(searchingEntries))
	}

	// Verify downloading and transferring entries remain unchanged
	downloadingEntries, err := database.ListEntriesByStatus(ctx, string(StatusDownloading))
	if err != nil {
		t.Fatalf("failed to list downloading entries: %v", err)
	}
	if len(downloadingEntries) != 1 {
		t.Errorf("expected 1 downloading entry, got %d", len(downloadingEntries))
	}

	transferringEntries, err := database.ListEntriesByStatus(ctx, string(StatusTransferring))
	if err != nil {
		t.Fatalf("failed to list transferring entries: %v", err)
	}
	if len(transferringEntries) != 1 {
		t.Errorf("expected 1 transferring entry, got %d", len(transferringEntries))
	}

	// Verify callbacks were invoked
	if len(downloadingRecovered) != 1 || downloadingRecovered[0] != "task-download-1" {
		t.Errorf("expected downloading callback with task-download-1, got %v", downloadingRecovered)
	}
	if len(transferringRecovered) != 1 || transferringRecovered[0] != "task-transfer-1" {
		t.Errorf("expected transferring callback with task-transfer-1, got %v", transferringRecovered)
	}
}

func TestStateMachine_ConcurrentTransitions(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	sm := NewStateMachine(database)
	ctx := context.Background()

	// Create a test entry
	entry := &db.Entry{
		Source:    "test",
		SourceID:  "test-concurrent",
		MediaType: "movie",
		Title:     "Test Concurrent",
		Season:    0,
		Status:    string(StatusPending),
	}
	if err := database.CreateEntry(ctx, entry); err != nil {
		t.Fatalf("failed to create entry: %v", err)
	}

	// Try concurrent transitions (should be serialized by mutex)
	done := make(chan bool, 2)
	
	go func() {
		_ = sm.Transition(ctx, entry.ID, StatusSearching, "concurrent 1")
		done <- true
	}()
	
	go func() {
		_ = sm.Transition(ctx, entry.ID, StatusSearching, "concurrent 2")
		done <- true
	}()

	// Wait for both goroutines
	<-done
	<-done

	// Verify entry is in searching state
	finalEntry, err := database.GetEntry(ctx, entry.ID)
	if err != nil {
		t.Fatalf("failed to get entry: %v", err)
	}
	if finalEntry.Status != string(StatusSearching) {
		t.Errorf("expected status searching, got %s", finalEntry.Status)
	}

	// Verify only one state log was created (mutex protected)
	logs, err := database.ListStateLogsByEntry(ctx, entry.ID)
	if err != nil {
		t.Fatalf("failed to list state logs: %v", err)
	}
	if len(logs) != 1 {
		t.Errorf("expected 1 state log, got %d", len(logs))
	}
}
