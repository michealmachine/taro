package state

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
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

func setupTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestStateMachine_ValidTransitions(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	sm := NewStateMachine(database, setupTestLogger())
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

	sm := NewStateMachine(database, setupTestLogger())
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

	sm := NewStateMachine(database, setupTestLogger())
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

	sm := NewStateMachine(database, setupTestLogger())
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

	sm := NewStateMachine(database, setupTestLogger())
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

	sm := NewStateMachine(database, setupTestLogger())
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
			entry.DownloadStartedAt = sql.NullTime{Time: time.Now().Add(-1 * time.Hour), Valid: true}
		}
		if e.status == StatusTransferring && e.taskID != "" {
			entry.TransferTaskID = sql.NullString{String: e.taskID, Valid: true}
			entry.TransferStartedAt = sql.NullTime{Time: time.Now().Add(-1 * time.Hour), Valid: true}
		}
		if err := database.CreateEntry(ctx, entry); err != nil {
			t.Fatalf("failed to create entry: %v", err)
		}
	}

	// Track recovered tasks
	downloadingRecovered := []string{}
	transferringRecovered := []string{}

	callbacks := &RecoveryCallbacks{
		OnDownloading: func(entryID, taskID string, downloadStartedAt time.Time) error {
			downloadingRecovered = append(downloadingRecovered, taskID)
			return nil
		},
		OnTransferring: func(entryID, taskID string, transferStartedAt time.Time) error {
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

	sm := NewStateMachine(database, setupTestLogger())
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

func TestStateMachine_UpdateFields_TransferStartedAt(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	sm := NewStateMachine(database, setupTestLogger())
	ctx := context.Background()

	entry := &db.Entry{
		Source:    "manual",
		SourceID:  "update-fields-transfer-started-at",
		MediaType: "anime",
		Title:     "Update Fields Test",
		Season:    1,
		Status:    string(StatusTransferring),
	}
	if err := database.CreateEntry(ctx, entry); err != nil {
		t.Fatalf("failed to create entry: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	if err := sm.UpdateFields(ctx, entry.ID, map[string]any{
		"transfer_started_at": now,
	}); err != nil {
		t.Fatalf("UpdateFields() failed: %v", err)
	}

	updated, err := database.GetEntry(ctx, entry.ID)
	if err != nil {
		t.Fatalf("failed to get updated entry: %v", err)
	}

	if !updated.TransferStartedAt.Valid {
		t.Fatalf("expected transfer_started_at to be set")
	}
	got := updated.TransferStartedAt.Time.UTC().Truncate(time.Second)
	if !got.Equal(now) {
		t.Fatalf("unexpected transfer_started_at, got %v want %v", got, now)
	}
}

// TestFailureKindOf tests the FailureKindOf function
func TestFailureKindOf(t *testing.T) {
	tests := []struct {
		code FailureCode
		want FailureKind
	}{
		// Retryable failures
		{FailureNetworkTimeout, FailureRetryable},
		{FailureServiceUnreachable, FailureRetryable},
		{FailureAuthTemporary, FailureRetryable},
		{FailurePikPakTimeout, FailureRetryable},
		{FailureTransferTimeout, FailureRetryable},
		// Permanent failures
		{FailureNoResources, FailurePermanent},
		{FailureAllCodecsExcluded, FailurePermanent},
		{FailureUserCancelled, FailurePermanent},
		{FailureConfigError, FailurePermanent},
	}

	for _, tt := range tests {
		t.Run(string(tt.code), func(t *testing.T) {
			got := FailureKindOf(tt.code)
			if got != tt.want {
				t.Errorf("FailureKindOf(%s) = %s, want %s", tt.code, got, tt.want)
			}
		})
	}
}

// TestStateMachine_TransitionToFailed tests the TransitionToFailed method
func TestStateMachine_TransitionToFailed(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	logger := setupTestLogger()
	sm := NewStateMachine(database, logger)

	ctx := context.Background()

	// Create test entry
	entry := &db.Entry{
		ID:        "test-entry",
		Title:     "Test Entry",
		MediaType: "anime",
		Source:    "manual",
		SourceID:  "test-123",
		Season:    1,
		Status:    string(StatusPending),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := database.CreateEntry(ctx, entry); err != nil {
		t.Fatalf("failed to create entry: %v", err)
	}

	// Transition to searching first
	if err := sm.Transition(ctx, entry.ID, StatusSearching, "start search"); err != nil {
		t.Fatalf("failed to transition to searching: %v", err)
	}

	// Test TransitionToFailed with retryable failure
	err := sm.TransitionToFailed(ctx, entry.ID, FailureNetworkTimeout, "searching", "network timeout occurred")
	if err != nil {
		t.Fatalf("TransitionToFailed() failed: %v", err)
	}

	// Verify entry state
	updatedEntry, err := database.GetEntry(ctx, entry.ID)
	if err != nil {
		t.Fatalf("failed to get entry: %v", err)
	}

	if updatedEntry.Status != string(StatusFailed) {
		t.Errorf("expected status %s, got %s", StatusFailed, updatedEntry.Status)
	}

	if !updatedEntry.FailedAt.Valid {
		t.Error("expected failed_at to be set")
	}

	if updatedEntry.FailedStage.String != "searching" {
		t.Errorf("expected failed_stage 'searching', got %s", updatedEntry.FailedStage.String)
	}

	if updatedEntry.FailedReason.String != "network timeout occurred" {
		t.Errorf("expected failed_reason 'network timeout occurred', got %s", updatedEntry.FailedReason.String)
	}

	if updatedEntry.FailureKind.String != string(FailureRetryable) {
		t.Errorf("expected failure_kind 'retryable', got %s", updatedEntry.FailureKind.String)
	}

	if updatedEntry.FailureCode.String != string(FailureNetworkTimeout) {
		t.Errorf("expected failure_code 'network_timeout', got %s", updatedEntry.FailureCode.String)
	}

	// Test permanent failure
	entry2 := &db.Entry{
		ID:        "test-entry-2",
		Title:     "Test Entry 2",
		MediaType: "anime",
		Source:    "manual",
		SourceID:  "test-456",
		Season:    1,
		Status:    string(StatusSearching),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := database.CreateEntry(ctx, entry2); err != nil {
		t.Fatalf("failed to create entry2: %v", err)
	}

	err = sm.TransitionToFailed(ctx, entry2.ID, FailureNoResources, "searching", "no resources found")
	if err != nil {
		t.Fatalf("TransitionToFailed() failed: %v", err)
	}

	updatedEntry2, err := database.GetEntry(ctx, entry2.ID)
	if err != nil {
		t.Fatalf("failed to get entry2: %v", err)
	}

	if updatedEntry2.FailureKind.String != string(FailurePermanent) {
		t.Errorf("expected failure_kind 'permanent', got %s", updatedEntry2.FailureKind.String)
	}

	if updatedEntry2.FailureCode.String != string(FailureNoResources) {
		t.Errorf("expected failure_code 'no_resources', got %s", updatedEntry2.FailureCode.String)
	}
}

// TestStateMachine_PhaseStartTimes tests automatic phase start time setting
func TestStateMachine_PhaseStartTimes(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	logger := setupTestLogger()
	sm := NewStateMachine(database, logger)

	ctx := context.Background()

	// Create test entry
	entry := &db.Entry{
		ID:        "test-entry",
		Title:     "Test Entry",
		MediaType: "anime",
		Source:    "manual",
		SourceID:  "test-123",
		Season:    1,
		Status:    string(StatusPending),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := database.CreateEntry(ctx, entry); err != nil {
		t.Fatalf("failed to create entry: %v", err)
	}

	// Test search_started_at
	beforeSearch := time.Now()
	if err := sm.Transition(ctx, entry.ID, StatusSearching, "start search"); err != nil {
		t.Fatalf("failed to transition to searching: %v", err)
	}
	afterSearch := time.Now()

	updatedEntry, err := database.GetEntry(ctx, entry.ID)
	if err != nil {
		t.Fatalf("failed to get entry: %v", err)
	}

	if !updatedEntry.SearchStartedAt.Valid {
		t.Error("expected search_started_at to be set")
	} else {
		searchTime := updatedEntry.SearchStartedAt.Time
		if searchTime.Before(beforeSearch) || searchTime.After(afterSearch) {
			t.Errorf("search_started_at %v not in expected range [%v, %v]", searchTime, beforeSearch, afterSearch)
		}
	}

	// Test download_started_at
	if err := sm.Transition(ctx, entry.ID, StatusFound, "found resource"); err != nil {
		t.Fatalf("failed to transition to found: %v", err)
	}

	beforeDownload := time.Now()
	if err := sm.TransitionWithUpdate(ctx, entry.ID, StatusDownloading, map[string]any{
		"pikpak_task_id": "task-123",
		"reason":         "start download",
	}); err != nil {
		t.Fatalf("failed to transition to downloading: %v", err)
	}
	afterDownload := time.Now()

	updatedEntry, err = database.GetEntry(ctx, entry.ID)
	if err != nil {
		t.Fatalf("failed to get entry: %v", err)
	}

	if !updatedEntry.DownloadStartedAt.Valid {
		t.Error("expected download_started_at to be set")
	} else {
		downloadTime := updatedEntry.DownloadStartedAt.Time
		if downloadTime.Before(beforeDownload) || downloadTime.After(afterDownload) {
			t.Errorf("download_started_at %v not in expected range [%v, %v]", downloadTime, beforeDownload, afterDownload)
		}
	}

	// Test transfer_started_at
	if err := sm.Transition(ctx, entry.ID, StatusDownloaded, "download complete"); err != nil {
		t.Fatalf("failed to transition to downloaded: %v", err)
	}

	beforeTransfer := time.Now()
	if err := sm.TransitionWithUpdate(ctx, entry.ID, StatusTransferring, map[string]any{
		"transfer_task_id": "transfer-123",
		"reason":           "start transfer",
	}); err != nil {
		t.Fatalf("failed to transition to transferring: %v", err)
	}
	afterTransfer := time.Now()

	updatedEntry, err = database.GetEntry(ctx, entry.ID)
	if err != nil {
		t.Fatalf("failed to get entry: %v", err)
	}

	if !updatedEntry.TransferStartedAt.Valid {
		t.Error("expected transfer_started_at to be set")
	} else {
		transferTime := updatedEntry.TransferStartedAt.Time
		if transferTime.Before(beforeTransfer) || transferTime.After(afterTransfer) {
			t.Errorf("transfer_started_at %v not in expected range [%v, %v]", transferTime, beforeTransfer, afterTransfer)
		}
	}
}
