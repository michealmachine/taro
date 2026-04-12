package service

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/michealmachine/taro/internal/db"
	"github.com/michealmachine/taro/internal/state"
)

// ActionService encapsulates user-triggered actions
// All state changes must go through StateMachine
type ActionService struct {
	database     *db.DB
	stateMachine *state.StateMachine
	logger       *slog.Logger
}

// NewActionService creates a new action service
func NewActionService(
	database *db.DB,
	stateMachine *state.StateMachine,
	logger *slog.Logger,
) *ActionService {
	return &ActionService{
		database:     database,
		stateMachine: stateMachine,
		logger:       logger,
	}
}

// RetryEntry retries a failed entry with intelligent retry logic
// - permanent failures: return error (no retry)
// - retryable failures: determine retry starting point based on failed_stage
func (s *ActionService) RetryEntry(ctx context.Context, entryID string) error {
	entry, err := s.database.GetEntry(ctx, entryID)
	if err != nil {
		return fmt.Errorf("failed to get entry: %w", err)
	}

	// Validate entry is in failed state
	if entry.Status != string(state.StatusFailed) {
		return fmt.Errorf("entry must be in failed state, current: %s", entry.Status)
	}

	// Check failure_kind
	if !entry.FailureKind.Valid {
		return fmt.Errorf("entry missing failure_kind")
	}

	failureKind := state.FailureKind(entry.FailureKind.String)

	// Permanent failures cannot be retried
	if failureKind == state.FailurePermanent {
		return fmt.Errorf("cannot retry permanent failure: %s", entry.FailureCode.String)
	}

	// Determine retry starting point based on failed_stage
	var targetStatus state.EntryStatus
	var reason string

	if !entry.FailedStage.Valid || entry.FailedStage.String == "" {
		// No failed_stage recorded, default to pending
		targetStatus = state.StatusPending
		reason = "retry from beginning"
	} else {
		switch entry.FailedStage.String {
		case "searching":
			// Search failed, retry from pending
			targetStatus = state.StatusPending
			reason = "retry search"

		case "downloading":
			// Download failed, retry from pending (re-search may find better resources)
			targetStatus = state.StatusPending
			reason = "retry from pending after download failure"

		case "transferring":
			// Transfer failed, check if PikPak file still exists
			if entry.PikPakFileID.Valid && entry.PikPakFileID.String != "" {
				// File exists, retry from downloaded
				targetStatus = state.StatusDownloaded
				reason = "retry transfer (file exists)"
			} else {
				// File doesn't exist, retry from pending
				targetStatus = state.StatusPending
				reason = "retry from pending (file missing)"
			}

		default:
			// Unknown stage, default to pending
			s.logger.Warn("unknown failed_stage, defaulting to pending",
				"entry_id", entryID,
				"failed_stage", entry.FailedStage.String)
			targetStatus = state.StatusPending
			reason = "retry from pending (unknown stage)"
		}
	}

	// Transition to target status
	if err := s.stateMachine.Transition(ctx, entryID, targetStatus, reason); err != nil {
		return fmt.Errorf("failed to transition to %s: %w", targetStatus, err)
	}

	s.logger.Info("entry retry initiated",
		"entry_id", entryID,
		"failed_stage", entry.FailedStage.String,
		"target_status", targetStatus,
		"reason", reason)

	return nil
}

// CancelEntry cancels an entry (user-triggered cancellation)
func (s *ActionService) CancelEntry(ctx context.Context, entryID string) error {
	entry, err := s.database.GetEntry(ctx, entryID)
	if err != nil {
		return fmt.Errorf("failed to get entry: %w", err)
	}

	// Check if entry is already in terminal state
	status := state.EntryStatus(entry.Status)
	if status == state.StatusInLibrary || status == state.StatusCancelled {
		return fmt.Errorf("entry already in terminal state: %s", status)
	}

	// Transition to cancelled (not failed)
	// State machine allows transition to cancelled from any non-terminal state
	if err := s.stateMachine.Transition(ctx, entryID, state.StatusCancelled, "cancelled by user"); err != nil {
		return fmt.Errorf("failed to cancel entry: %w", err)
	}

	s.logger.Info("entry cancelled",
		"entry_id", entryID,
		"previous_status", entry.Status)

	return nil
}

// SelectResource selects a specific resource for an entry
// Validates resource belongs to entry and is eligible
// Uses transaction to ensure atomicity of resource selection and state transition
func (s *ActionService) SelectResource(ctx context.Context, entryID, resourceID string) error {
	// Execute in transaction to ensure atomicity
	return s.database.WithTx(ctx, func(tx *sqlx.Tx) error {
		// Get entry
		entry, err := db.GetEntryTx(ctx, tx, entryID)
		if err != nil {
			return fmt.Errorf("failed to get entry: %w", err)
		}

		// Validate entry is in needs_selection state
		if entry.Status != string(state.StatusNeedsSelection) {
			return fmt.Errorf("entry must be in needs_selection state, current: %s", entry.Status)
		}

		// Get resource
		resource, err := db.GetResourceTx(ctx, tx, resourceID)
		if err != nil {
			return fmt.Errorf("failed to get resource: %w", err)
		}

		// Validate resource belongs to this entry
		if resource.EntryID != entryID {
			return fmt.Errorf("resource does not belong to this entry")
		}

		// Validate resource is eligible
		if !resource.Eligible {
			return fmt.Errorf("resource is not eligible (filtered out)")
		}

		// Mark resource as selected
		resource.Selected = true
		if err := db.UpdateResourceTx(ctx, tx, resource); err != nil {
			return fmt.Errorf("failed to update resource: %w", err)
		}

		// Transition to found and record selected_resource_id
		// This calls TransitionWithUpdateTx which handles state machine logic in transaction
		updates := map[string]any{
			"selected_resource_id": resourceID,
			"resolution":           resource.Resolution.String,
			"reason":               "resource selected by user",
		}
		if err := s.stateMachine.TransitionWithUpdateTx(ctx, tx, entryID, state.StatusFound, updates); err != nil {
			return fmt.Errorf("failed to transition to found: %w", err)
		}

		s.logger.Info("resource selected",
			"entry_id", entryID,
			"resource_id", resourceID,
			"title", resource.Title)

		return nil
	})
}

// AddEntry creates a new manual entry
func (s *ActionService) AddEntry(ctx context.Context, title, mediaType string, year, season int) (string, error) {
	// Validate media type
	validMediaTypes := map[string]bool{
		"anime": true,
		"movie": true,
		"tv":    true,
	}
	if !validMediaTypes[mediaType] {
		return "", fmt.Errorf("invalid media_type: %s (must be anime|movie|tv)", mediaType)
	}

	// Create entry
	entry := &db.Entry{
		ID:        uuid.New().String(),
		Title:     title,
		MediaType: mediaType,
		Source:    "manual",
		SourceID:  uuid.New().String(), // Use UUID for manual entries
		Season:    season,
		Status:    string(state.StatusPending),
		AskMode:   0, // Use global config
	}

	// Set year for movies
	if mediaType == "movie" && year > 0 {
		entry.Year.Int64 = int64(year)
		entry.Year.Valid = true
	}

	// Create entry in database
	if err := s.database.CreateEntry(ctx, entry); err != nil {
		return "", fmt.Errorf("failed to create entry: %w", err)
	}

	s.logger.Info("manual entry created",
		"entry_id", entry.ID,
		"title", title,
		"media_type", mediaType,
		"year", year,
		"season", season)

	return entry.ID, nil
}
