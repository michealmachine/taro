package state

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/michealmachine/taro/internal/db"
)

// EntryStatus represents the status of an entry
type EntryStatus string

const (
	StatusPending        EntryStatus = "pending"
	StatusSearching      EntryStatus = "searching"
	StatusFound          EntryStatus = "found"
	StatusDownloading    EntryStatus = "downloading"
	StatusDownloaded     EntryStatus = "downloaded"
	StatusTransferring   EntryStatus = "transferring"
	StatusTransferred    EntryStatus = "transferred"
	StatusInLibrary      EntryStatus = "in_library"
	StatusNeedsSelection EntryStatus = "needs_selection"
	StatusFailed         EntryStatus = "failed"
	StatusCancelled      EntryStatus = "cancelled"
)

// FailureKind represents the type of failure
type FailureKind string

const (
	FailureRetryable FailureKind = "retryable" // 可重试：网络超时、服务不可达、临时认证失败
	FailurePermanent FailureKind = "permanent" // 不可重试：无资源、资源损坏、用户取消、配置错误
)

// FailureCode represents structured failure reasons
type FailureCode string

const (
	// Retryable failures（可重试）
	FailureNetworkTimeout     FailureCode = "network_timeout"
	FailureServiceUnreachable FailureCode = "service_unreachable"
	FailureAuthTemporary      FailureCode = "auth_temporary"
	FailurePikPakTimeout      FailureCode = "pikpak_timeout"
	FailureTransferTimeout    FailureCode = "transfer_timeout"

	// Permanent failures（不可重试）
	FailureNoResources       FailureCode = "no_resources"
	FailureAllCodecsExcluded FailureCode = "all_codecs_excluded"
	FailureUserCancelled     FailureCode = "user_cancelled"
	FailureConfigError       FailureCode = "config_error"
)

// FailureKindOf returns the FailureKind for a given FailureCode
func FailureKindOf(code FailureCode) FailureKind {
	switch code {
	case FailureNoResources, FailureAllCodecsExcluded, FailureUserCancelled, FailureConfigError:
		return FailurePermanent
	default:
		return FailureRetryable
	}
}

// validTransitions defines the legal state transitions
var validTransitions = map[EntryStatus][]EntryStatus{
	StatusPending:        {StatusSearching},
	StatusSearching:      {StatusFound, StatusNeedsSelection, StatusFailed, StatusPending}, // Allow reset to pending
	StatusFound:          {StatusDownloading},
	StatusDownloading:    {StatusDownloaded, StatusFailed},
	StatusDownloaded:     {StatusTransferring},
	StatusTransferring:   {StatusTransferred, StatusFailed},
	StatusTransferred:    {StatusInLibrary},
	StatusNeedsSelection: {StatusFound, StatusCancelled},
	StatusFailed:         {StatusPending, StatusDownloaded},
	// Any non-terminal state can transition to cancelled
}

// StateMachine manages entry state transitions
type StateMachine struct {
	database *db.DB
	logger   *slog.Logger
	mu       sync.Mutex
}

// NewStateMachine creates a new state machine
func NewStateMachine(database *db.DB, logger *slog.Logger) *StateMachine {
	return &StateMachine{
		database: database,
		logger:   logger,
	}
}

// Transition executes a state transition and writes an audit log within a transaction
func (sm *StateMachine) Transition(ctx context.Context, entryID string, to EntryStatus, reason string) error {
	return sm.TransitionWithUpdate(ctx, entryID, to, map[string]any{"reason": reason})
}

// TransitionToFailed transitions an entry to failed state with structured failure information
// failure_kind is automatically derived from the FailureCode using FailureKindOf()
func (sm *StateMachine) TransitionToFailed(ctx context.Context, entryID string, code FailureCode, stage, reason string) error {
	kind := FailureKindOf(code)

	updates := map[string]any{
		"failed_stage":  stage,
		"failed_reason": reason,
		"failure_kind":  string(kind),
		"failure_code":  string(code),
		"reason":        fmt.Sprintf("failed: %s", reason),
	}

	return sm.TransitionWithUpdate(ctx, entryID, StatusFailed, updates)
}

// TransitionWithUpdate executes a state transition and updates additional fields within a transaction
// 状态机自动设置阶段开始时间：
//   searching  -> 自动设置 search_started_at
//   downloading -> 自动设置 download_started_at
//   transferring -> 自动设置 transfer_started_at
func (sm *StateMachine) TransitionWithUpdate(ctx context.Context, entryID string, to EntryStatus, updates map[string]any) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	return sm.database.WithTx(ctx, func(tx *sqlx.Tx) error {
		// Get current entry within transaction
		entry, err := db.GetEntryTx(ctx, tx, entryID)
		if err != nil {
			return fmt.Errorf("failed to get entry: %w", err)
		}

		from := EntryStatus(entry.Status)

		// Validate transition
		if !sm.isValidTransition(from, to) {
			return fmt.Errorf("invalid transition from %s to %s", from, to)
		}

		// Update entry status
		now := time.Now()
		entry.Status = string(to)
		entry.UpdatedAt = now

		// Automatically set phase start times based on target status
		switch to {
		case StatusSearching:
			entry.SearchStartedAt = sql.NullTime{Time: now, Valid: true}
		case StatusDownloading:
			entry.DownloadStartedAt = sql.NullTime{Time: now, Valid: true}
		case StatusTransferring:
			entry.TransferStartedAt = sql.NullTime{Time: now, Valid: true}
		}

		// Set failed_at for failed status
		if to == StatusFailed {
			entry.FailedAt = sql.NullTime{Time: now, Valid: true}
		}

		// Clear failed_at when recovering from failed
		if from == StatusFailed && to != StatusFailed {
			entry.FailedAt = sql.NullTime{Valid: false}
			// Clear failure fields when recovering
			entry.FailedStage = sql.NullString{Valid: false}
			entry.FailedReason = sql.NullString{Valid: false}
			entry.FailureKind = sql.NullString{Valid: false}
			entry.FailureCode = sql.NullString{Valid: false}
		}

		// Apply additional updates
		sm.applyUpdates(entry, updates)

		// Update entry within transaction
		if err := db.UpdateEntryTx(ctx, tx, entry); err != nil {
			return fmt.Errorf("failed to update entry: %w", err)
		}

		// Write audit log within transaction
		reason := "state transition"
		if r, ok := updates["reason"].(string); ok && r != "" {
			reason = r
		}

		stateLog := &db.StateLog{
			EntryID:    entryID,
			FromStatus: string(from),
			ToStatus:   string(to),
			Reason:     sql.NullString{String: reason, Valid: true},
			CreatedAt:  now,
		}
		if err := db.CreateStateLogTx(ctx, tx, stateLog); err != nil {
			return fmt.Errorf("failed to create state log: %w", err)
		}

		return nil
	})
}

// applyUpdates applies additional field updates to an entry
func (sm *StateMachine) applyUpdates(entry *db.Entry, updates map[string]any) {
	for key, value := range updates {
		switch key {
		case "reason": // Skip reason, it's only for audit log
			continue
		case "pikpak_task_id":
			if v, ok := value.(string); ok {
				entry.PikPakTaskID = sql.NullString{String: v, Valid: v != ""}
			}
		case "pikpak_file_id":
			if v, ok := value.(string); ok {
				entry.PikPakFileID = sql.NullString{String: v, Valid: v != ""}
			}
		case "pikpak_file_path":
			if v, ok := value.(string); ok {
				entry.PikPakFilePath = sql.NullString{String: v, Valid: v != ""}
			}
		case "transfer_task_id":
			if v, ok := value.(string); ok {
				entry.TransferTaskID = sql.NullString{String: v, Valid: v != ""}
			}
		case "target_path":
			if v, ok := value.(string); ok {
				entry.TargetPath = sql.NullString{String: v, Valid: v != ""}
			}
		case "failed_stage":
			if v, ok := value.(string); ok {
				entry.FailedStage = sql.NullString{String: v, Valid: v != ""}
			}
		case "failed_reason":
			if v, ok := value.(string); ok {
				entry.FailedReason = sql.NullString{String: v, Valid: v != ""}
			}
		case "failure_kind":
			if v, ok := value.(string); ok {
				entry.FailureKind = sql.NullString{String: v, Valid: v != ""}
			}
		case "failure_code":
			if v, ok := value.(string); ok {
				entry.FailureCode = sql.NullString{String: v, Valid: v != ""}
			}
		case "selected_resource_id":
			if v, ok := value.(string); ok {
				entry.SelectedResourceID = sql.NullString{String: v, Valid: v != ""}
			}
		case "resolution":
			if v, ok := value.(string); ok {
				entry.Resolution = sql.NullString{String: v, Valid: v != ""}
			}
		}
	}
}

// RecoveryCallbacks contains callbacks for recovering downloading and transferring entries
type RecoveryCallbacks struct {
	// OnDownloading is called for each entry in downloading state
	// Parameters: entryID, taskID, downloadStartedAt
	OnDownloading func(entryID, taskID string, downloadStartedAt time.Time) error
	// OnTransferring is called for each entry in transferring state
	// Parameters: entryID, taskID, transferStartedAt
	OnTransferring func(entryID, taskID string, transferStartedAt time.Time) error
}

// RecoverOnStartup executes recovery logic on system startup
func (sm *StateMachine) RecoverOnStartup(ctx context.Context, callbacks *RecoveryCallbacks) error {
	// Reset all searching entries to pending
	searchingEntries, err := sm.database.ListEntriesByStatus(ctx, string(StatusSearching))
	if err != nil {
		return fmt.Errorf("failed to list searching entries: %w", err)
	}

	for _, entry := range searchingEntries {
		if err := sm.Transition(ctx, entry.ID, StatusPending, "reset on startup"); err != nil {
			// Log error but continue with other entries
			// Don't let one failed entry block the entire recovery process
			sm.logger.Error("failed to reset entry on startup", "entry_id", entry.ID, "error", err)
			continue
		}
	}

	// Recover downloading entries if callback provided
	if callbacks != nil && callbacks.OnDownloading != nil {
		downloadingEntries, err := sm.database.ListEntriesByStatus(ctx, string(StatusDownloading))
		if err != nil {
			return fmt.Errorf("failed to list downloading entries: %w", err)
		}

		for _, entry := range downloadingEntries {
			if entry.PikPakTaskID.Valid && entry.PikPakTaskID.String != "" {
				// Use download_started_at as the timeout baseline (not updated_at)
				if !entry.DownloadStartedAt.Valid {
					// Skip entries missing download_started_at - cannot determine accurate timeout
					sm.logger.Error("entry missing download_started_at, skipping recovery",
						"entry_id", entry.ID,
						"task_id", entry.PikPakTaskID.String)
					continue
				}

				if err := callbacks.OnDownloading(entry.ID, entry.PikPakTaskID.String, entry.DownloadStartedAt.Time); err != nil {
					// Log error but continue with other entries
					sm.logger.Error("failed to recover downloading entry", "entry_id", entry.ID, "error", err)
					continue
				}
			}
		}
	}

	// Recover transferring entries if callback provided
	if callbacks != nil && callbacks.OnTransferring != nil {
		transferringEntries, err := sm.database.ListEntriesByStatus(ctx, string(StatusTransferring))
		if err != nil {
			return fmt.Errorf("failed to list transferring entries: %w", err)
		}

		for _, entry := range transferringEntries {
			if entry.TransferTaskID.Valid && entry.TransferTaskID.String != "" {
				// Use transfer_started_at as the timeout baseline (not updated_at)
				if !entry.TransferStartedAt.Valid {
					// Skip entries missing transfer_started_at - cannot determine accurate timeout
					sm.logger.Error("entry missing transfer_started_at, skipping recovery",
						"entry_id", entry.ID,
						"task_id", entry.TransferTaskID.String)
					continue
				}

				if err := callbacks.OnTransferring(entry.ID, entry.TransferTaskID.String, entry.TransferStartedAt.Time); err != nil {
					// Log error but continue with other entries
					sm.logger.Error("failed to recover transferring entry", "entry_id", entry.ID, "error", err)
					continue
				}
			}
		}
	}

	return nil
}

// isValidTransition checks if a state transition is valid
func (sm *StateMachine) isValidTransition(from, to EntryStatus) bool {
	// Allow transition to cancelled from any non-terminal state
	if to == StatusCancelled && from != StatusInLibrary && from != StatusCancelled {
		return true
	}

	// Check valid transitions map
	validTargets, ok := validTransitions[from]
	if !ok {
		return false
	}

	for _, valid := range validTargets {
		if valid == to {
			return true
		}
	}

	return false
}
