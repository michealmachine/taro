package service

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"testing"

	"github.com/michealmachine/taro/internal/db"
	"github.com/michealmachine/taro/internal/state"
)

func setupActionTestDB(t *testing.T) *db.DB {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

func setupActionService(t *testing.T) (*ActionService, *db.DB, context.Context) {
	t.Helper()
	database := setupActionTestDB(t)
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	sm := state.NewStateMachine(database, logger)
	service := NewActionService(database, sm, logger)
	return service, database, context.Background()
}

func createFailedEntryForRetry(t *testing.T, database *db.DB, ctx context.Context, id string, failedStage sql.NullString, hasPikPakFile bool, failureKind state.FailureKind) *db.Entry {
	t.Helper()

	entry := &db.Entry{
		ID:          id,
		Title:       "Retry Target",
		MediaType:   "anime",
		Source:      "manual",
		SourceID:    id,
		Season:      1,
		Status:      string(state.StatusFailed),
		FailedStage: failedStage,
		FailureKind: sql.NullString{String: string(failureKind), Valid: true},
		FailureCode: sql.NullString{String: string(state.FailureServiceUnreachable), Valid: true},
	}
	if hasPikPakFile {
		entry.PikPakFileID = sql.NullString{String: "file-1", Valid: true}
	}

	if err := database.CreateEntry(ctx, entry); err != nil {
		t.Fatalf("failed to create failed entry: %v", err)
	}
	return entry
}

func TestRetryEntry_StageMapping(t *testing.T) {
	svc, database, ctx := setupActionService(t)

	tests := []struct {
		name         string
		id           string
		failedStage  sql.NullString
		hasPikPak    bool
		expectStatus state.EntryStatus
	}{
		{
			name:         "transferring with file goes to downloaded",
			id:           "entry-transfer-file",
			failedStage:  sql.NullString{String: "transferring", Valid: true},
			hasPikPak:    true,
			expectStatus: state.StatusDownloaded,
		},
		{
			name:         "transferring without file goes to pending",
			id:           "entry-transfer-no-file",
			failedStage:  sql.NullString{String: "transferring", Valid: true},
			hasPikPak:    false,
			expectStatus: state.StatusPending,
		},
		{
			name:         "searching failure goes to pending",
			id:           "entry-searching",
			failedStage:  sql.NullString{String: "searching", Valid: true},
			hasPikPak:    false,
			expectStatus: state.StatusPending,
		},
		{
			name:         "downloading failure goes to pending",
			id:           "entry-downloading",
			failedStage:  sql.NullString{String: "downloading", Valid: true},
			hasPikPak:    false,
			expectStatus: state.StatusPending,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			createFailedEntryForRetry(t, database, ctx, tt.id, tt.failedStage, tt.hasPikPak, state.FailureRetryable)

			if err := svc.RetryEntry(ctx, tt.id); err != nil {
				t.Fatalf("RetryEntry() failed: %v", err)
			}

			updated, err := database.GetEntry(ctx, tt.id)
			if err != nil {
				t.Fatalf("failed to load updated entry: %v", err)
			}
			if updated.Status != string(tt.expectStatus) {
				t.Fatalf("expected status %s, got %s", tt.expectStatus, updated.Status)
			}
		})
	}
}

func TestRetryEntry_PermanentFailureRejected(t *testing.T) {
	svc, database, ctx := setupActionService(t)

	entry := createFailedEntryForRetry(
		t,
		database,
		ctx,
		"entry-permanent",
		sql.NullString{String: "searching", Valid: true},
		false,
		state.FailurePermanent,
	)

	err := svc.RetryEntry(ctx, entry.ID)
	if err == nil {
		t.Fatalf("expected permanent failure retry to be rejected")
	}
}

func TestRetryEntry_MissingFailureFields(t *testing.T) {
	svc, database, ctx := setupActionService(t)

	entryMissingKind := &db.Entry{
		ID:          "entry-missing-kind",
		Title:       "Missing Kind",
		MediaType:   "anime",
		Source:      "manual",
		SourceID:    "entry-missing-kind",
		Season:      1,
		Status:      string(state.StatusFailed),
		FailedStage: sql.NullString{String: "searching", Valid: true},
	}
	if err := database.CreateEntry(ctx, entryMissingKind); err != nil {
		t.Fatalf("failed to create missing-kind entry: %v", err)
	}

	if err := svc.RetryEntry(ctx, entryMissingKind.ID); err == nil {
		t.Fatalf("expected retry to fail when failure_kind is missing")
	}

	entryMissingStage := &db.Entry{
		ID:          "entry-missing-stage",
		Title:       "Missing Stage",
		MediaType:   "anime",
		Source:      "manual",
		SourceID:    "entry-missing-stage",
		Season:      1,
		Status:      string(state.StatusFailed),
		FailureKind: sql.NullString{String: string(state.FailureRetryable), Valid: true},
	}
	if err := database.CreateEntry(ctx, entryMissingStage); err != nil {
		t.Fatalf("failed to create missing-stage entry: %v", err)
	}

	if err := svc.RetryEntry(ctx, entryMissingStage.ID); err != nil {
		t.Fatalf("expected retry with missing failed_stage to default to pending, got: %v", err)
	}

	updated, err := database.GetEntry(ctx, entryMissingStage.ID)
	if err != nil {
		t.Fatalf("failed to get updated entry: %v", err)
	}
	if updated.Status != string(state.StatusPending) {
		t.Fatalf("expected status pending, got %s", updated.Status)
	}
}
