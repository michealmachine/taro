package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Entry represents a media entry in the database
type Entry struct {
	ID                 string         `db:"id"`
	Title              string         `db:"title"`
	Year               sql.NullInt64  `db:"year"`       // Year for movies
	MediaType          string         `db:"media_type"` // 'anime' | 'movie' | 'tv'
	Source             string         `db:"source"`     // 'bangumi' | 'trakt' | 'manual'
	SourceID           string         `db:"source_id"`
	Season             int            `db:"season"`
	Status             string         `db:"status"`
	AskMode            int            `db:"ask_mode"` // 0=全局配置 1=强制询问 2=强制自动
	Resolution         sql.NullString `db:"resolution"`
	SelectedResourceID sql.NullString `db:"selected_resource_id"`
	PikPakTaskID       sql.NullString `db:"pikpak_task_id"`
	PikPakFileID       sql.NullString `db:"pikpak_file_id"`
	PikPakFilePath     sql.NullString `db:"pikpak_file_path"`
	PikPakCleaned      bool           `db:"pikpak_cleaned"`
	TransferTaskID     sql.NullString `db:"transfer_task_id"`
	TargetPath         sql.NullString `db:"target_path"`
	FailedStage        sql.NullString `db:"failed_stage"`
	FailedReason       sql.NullString `db:"failed_reason"`
	FailedAt           sql.NullTime   `db:"failed_at"`
	CreatedAt          time.Time      `db:"created_at"`
	UpdatedAt          time.Time      `db:"updated_at"`
}

// CreateEntry creates a new entry
func (db *DB) CreateEntry(ctx context.Context, entry *Entry) error {
	if entry.ID == "" {
		entry.ID = uuid.New().String()
	}
	entry.CreatedAt = time.Now()
	entry.UpdatedAt = time.Now()

	query := `
		INSERT INTO entries (
			id, title, media_type, source, source_id, season, status, ask_mode, resolution,
			selected_resource_id, pikpak_task_id, pikpak_file_id, pikpak_file_path, pikpak_cleaned,
			transfer_task_id, target_path, failed_stage, failed_reason, failed_at,
			created_at, updated_at
		) VALUES (
			:id, :title, :media_type, :source, :source_id, :season, :status, :ask_mode, :resolution,
			:selected_resource_id, :pikpak_task_id, :pikpak_file_id, :pikpak_file_path, :pikpak_cleaned,
			:transfer_task_id, :target_path, :failed_stage, :failed_reason, :failed_at,
			:created_at, :updated_at
		)
	`

	_, err := db.NamedExecContext(ctx, query, entry)
	if err != nil {
		return fmt.Errorf("failed to create entry: %w", err)
	}

	return nil
}

// GetEntry retrieves an entry by ID
func (db *DB) GetEntry(ctx context.Context, id string) (*Entry, error) {
	var entry Entry
	query := `SELECT * FROM entries WHERE id = ?`

	err := db.GetContext(ctx, &entry, query, id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("entry not found: %s", id)
		}
		return nil, fmt.Errorf("failed to get entry: %w", err)
	}

	return &entry, nil
}

// UpdateEntry updates an entry
func (db *DB) UpdateEntry(ctx context.Context, entry *Entry) error {
	entry.UpdatedAt = time.Now()

	query := `
		UPDATE entries SET
			title = :title,
			media_type = :media_type,
			source = :source,
			source_id = :source_id,
			season = :season,
			status = :status,
			ask_mode = :ask_mode,
			resolution = :resolution,
			selected_resource_id = :selected_resource_id,
			pikpak_task_id = :pikpak_task_id,
			pikpak_file_id = :pikpak_file_id,
			pikpak_file_path = :pikpak_file_path,
			pikpak_cleaned = :pikpak_cleaned,
			transfer_task_id = :transfer_task_id,
			target_path = :target_path,
			failed_stage = :failed_stage,
			failed_reason = :failed_reason,
			failed_at = :failed_at,
			updated_at = :updated_at
		WHERE id = :id
	`

	result, err := db.NamedExecContext(ctx, query, entry)
	if err != nil {
		return fmt.Errorf("failed to update entry: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("entry not found: %s", entry.ID)
	}

	return nil
}

// ListEntries lists all entries with optional filters
func (db *DB) ListEntries(ctx context.Context, filters map[string]interface{}) ([]*Entry, error) {
	query := `SELECT * FROM entries WHERE 1=1`
	args := []interface{}{}

	if status, ok := filters["status"].(string); ok && status != "" {
		query += ` AND status = ?`
		args = append(args, status)
	}

	if source, ok := filters["source"].(string); ok && source != "" {
		query += ` AND source = ?`
		args = append(args, source)
	}

	query += ` ORDER BY created_at DESC`

	var entries []*Entry
	err := db.SelectContext(ctx, &entries, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list entries: %w", err)
	}

	return entries, nil
}

// ListEntriesByStatus lists entries by status
func (db *DB) ListEntriesByStatus(ctx context.Context, status string) ([]*Entry, error) {
	var entries []*Entry
	query := `SELECT * FROM entries WHERE status = ? ORDER BY created_at DESC`

	err := db.SelectContext(ctx, &entries, query, status)
	if err != nil {
		return nil, fmt.Errorf("failed to list entries by status: %w", err)
	}

	return entries, nil
}

// EntryExists checks if an entry exists by source and source_id
func (db *DB) EntryExists(ctx context.Context, source, sourceID string, season int) (bool, error) {
	var count int
	query := `SELECT COUNT(*) FROM entries WHERE source = ? AND source_id = ? AND season = ?`

	err := db.GetContext(ctx, &count, query, source, sourceID, season)
	if err != nil {
		return false, fmt.Errorf("failed to check entry existence: %w", err)
	}

	return count > 0, nil
}
