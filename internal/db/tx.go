package db

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// Queryer is an interface that can be satisfied by *sqlx.DB or *sqlx.Tx
type Queryer interface {
	sqlx.ExtContext
	sqlx.PreparerContext
	GetContext(ctx context.Context, dest interface{}, query string, args ...interface{}) error
	SelectContext(ctx context.Context, dest interface{}, query string, args ...interface{}) error
	NamedExecContext(ctx context.Context, query string, arg interface{}) (sql.Result, error)
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
}

// CreateEntryTx creates a new entry within a transaction
func CreateEntryTx(ctx context.Context, q Queryer, entry *Entry) error {
	if entry.ID == "" {
		entry.ID = uuid.New().String()
	}
	// Note: time is set by application, not database default
	// This ensures consistent timezone handling

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

	_, err := q.NamedExecContext(ctx, query, entry)
	if err != nil {
		return fmt.Errorf("failed to create entry: %w", err)
	}

	return nil
}

// GetEntryTx retrieves an entry by ID within a transaction
func GetEntryTx(ctx context.Context, q Queryer, id string) (*Entry, error) {
	var entry Entry
	query := `SELECT * FROM entries WHERE id = ?`

	err := q.GetContext(ctx, &entry, query, id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("entry not found: %s", id)
		}
		return nil, fmt.Errorf("failed to get entry: %w", err)
	}

	return &entry, nil
}

// UpdateEntryTx updates an entry within a transaction
func UpdateEntryTx(ctx context.Context, q Queryer, entry *Entry) error {
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

	result, err := q.NamedExecContext(ctx, query, entry)
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

// CreateStateLogTx creates a new state log entry within a transaction
func CreateStateLogTx(ctx context.Context, q Queryer, log *StateLog) error {
	query := `
		INSERT INTO state_logs (
			entry_id, from_status, to_status, reason, created_at
		) VALUES (
			:entry_id, :from_status, :to_status, :reason, :created_at
		)
	`

	result, err := q.NamedExecContext(ctx, query, log)
	if err != nil {
		return fmt.Errorf("failed to create state log: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("failed to get last insert id: %w", err)
	}

	log.ID = id
	return nil
}
