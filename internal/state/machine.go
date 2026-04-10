package state

import (
	"context"
	"database/sql"
	"fmt"
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
	mu       sync.Mutex
}

// NewStateMachine creates a new state machine
func NewStateMachine(database *db.DB) *StateMachine {
	return &StateMachine{
		database: database,
	}
}

// TransitionRequest contains the parameters for a state transition
type TransitionRequest struct {
	To      EntryStatus
	Reason  string
	Updates map[string]any // Optional additional field updates
}

// Transition executes a state transition and writes an audit log within a transaction
func (sm *StateMachine) Transition(ctx context.Context, entryID string, to EntryStatus, reason string) error {
	return sm.TransitionWithUpdate(ctx, entryID, to, map[string]any{"reason": reason})
}

// TransitionWithUpdate executes a state transition and updates additional fields within a transaction
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

		// Set failed_at for failed status
		if to == StatusFailed {
			entry.FailedAt = sql.NullTime{Time: now, Valid: true}
		}

		// Clear failed_at when recovering from failed
		if from == StatusFailed && to != StatusFailed {
			entry.FailedAt = sql.NullTime{Valid: false}
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
	OnDownloading  func(entryID, taskID string) error
	OnTransferring func(entryID, taskID string) error
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
			return fmt.Errorf("failed to reset entry %s: %w", entry.ID, err)
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
				if err := callbacks.OnDownloading(entry.ID, entry.PikPakTaskID.String); err != nil {
					return fmt.Errorf("failed to recover downloading entry %s: %w", entry.ID, err)
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
				if err := callbacks.OnTransferring(entry.ID, entry.TransferTaskID.String); err != nil {
					return fmt.Errorf("failed to recover transferring entry %s: %w", entry.ID, err)
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
