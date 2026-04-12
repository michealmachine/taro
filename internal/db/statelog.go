package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// StateLog represents a state transition audit log entry
type StateLog struct {
	ID         int64          `db:"id"`
	EntryID    string         `db:"entry_id"`
	FromStatus string         `db:"from_status"`
	ToStatus   string         `db:"to_status"`
	Reason     sql.NullString `db:"reason"`
	Metadata   sql.NullString `db:"metadata"` // JSON 格式元信息（v2 候选：记录 resource_id、token_refreshed 等）
	CreatedAt  time.Time      `db:"created_at"`
}

// CreateStateLog creates a new state log entry
func (db *DB) CreateStateLog(ctx context.Context, log *StateLog) error {
	log.CreatedAt = time.Now()

	query := `
		INSERT INTO state_logs (
			entry_id, from_status, to_status, reason, metadata, created_at
		) VALUES (
			:entry_id, :from_status, :to_status, :reason, :metadata, :created_at
		)
	`

	result, err := db.NamedExecContext(ctx, query, log)
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

// ListStateLogsByEntry lists all state logs for an entry
func (db *DB) ListStateLogsByEntry(ctx context.Context, entryID string) ([]*StateLog, error) {
	var logs []*StateLog
	query := `SELECT * FROM state_logs WHERE entry_id = ? ORDER BY created_at ASC`

	err := db.SelectContext(ctx, &logs, query, entryID)
	if err != nil {
		return nil, fmt.Errorf("failed to list state logs: %w", err)
	}

	return logs, nil
}

// DeleteStateLogsByEntry deletes all state logs for an entry
func (db *DB) DeleteStateLogsByEntry(ctx context.Context, entryID string) error {
	query := `DELETE FROM state_logs WHERE entry_id = ?`

	_, err := db.ExecContext(ctx, query, entryID)
	if err != nil {
		return fmt.Errorf("failed to delete state logs: %w", err)
	}

	return nil
}

// DeleteOldStateLogs deletes state logs older than the specified number of days
func (db *DB) DeleteOldStateLogs(ctx context.Context, days int) (int64, error) {
	if days <= 0 {
		return 0, nil // 0 means keep forever
	}

	query := `DELETE FROM state_logs WHERE created_at < datetime('now', '-' || ? || ' days')`

	result, err := db.ExecContext(ctx, query, days)
	if err != nil {
		return 0, fmt.Errorf("failed to delete old state logs: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to get rows affected: %w", err)
	}

	return rows, nil
}
