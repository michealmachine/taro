package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
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
	// 阶段开始时间（用于超时判断和恢复逻辑）
	SearchStartedAt   sql.NullTime   `db:"search_started_at"`
	DownloadStartedAt sql.NullTime   `db:"download_started_at"`
	TransferStartedAt sql.NullTime   `db:"transfer_started_at"`
	PikPakTaskID      sql.NullString `db:"pikpak_task_id"`
	PikPakFileID      sql.NullString `db:"pikpak_file_id"`
	PikPakFilePath    sql.NullString `db:"pikpak_file_path"`
	PikPakCleaned     bool           `db:"pikpak_cleaned"`
	TransferTaskID    sql.NullString `db:"transfer_task_id"`
	TargetPath        sql.NullString `db:"target_path"`
	// 失败信息（结构化）
	FailedStage  sql.NullString `db:"failed_stage"`
	FailedReason sql.NullString `db:"failed_reason"`
	FailureKind  sql.NullString `db:"failure_kind"` // 'retryable' | 'permanent'
	FailureCode  sql.NullString `db:"failure_code"` // 结构化失败代码
	FailedAt     sql.NullTime   `db:"failed_at"`
	CreatedAt    time.Time      `db:"created_at"`
	UpdatedAt    time.Time      `db:"updated_at"`
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
			id, title, year, media_type, source, source_id, season, status, ask_mode, resolution,
			selected_resource_id, search_started_at, download_started_at, transfer_started_at,
			pikpak_task_id, pikpak_file_id, pikpak_file_path, pikpak_cleaned,
			transfer_task_id, target_path, failed_stage, failed_reason, failure_kind, failure_code, failed_at,
			created_at, updated_at
		) VALUES (
			:id, :title, :year, :media_type, :source, :source_id, :season, :status, :ask_mode, :resolution,
			:selected_resource_id, :search_started_at, :download_started_at, :transfer_started_at,
			:pikpak_task_id, :pikpak_file_id, :pikpak_file_path, :pikpak_cleaned,
			:transfer_task_id, :target_path, :failed_stage, :failed_reason, :failure_kind, :failure_code, :failed_at,
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
			year = :year,
			media_type = :media_type,
			source = :source,
			source_id = :source_id,
			season = :season,
			status = :status,
			ask_mode = :ask_mode,
			resolution = :resolution,
			selected_resource_id = :selected_resource_id,
			search_started_at = :search_started_at,
			download_started_at = :download_started_at,
			transfer_started_at = :transfer_started_at,
			pikpak_task_id = :pikpak_task_id,
			pikpak_file_id = :pikpak_file_id,
			pikpak_file_path = :pikpak_file_path,
			pikpak_cleaned = :pikpak_cleaned,
			transfer_task_id = :transfer_task_id,
			target_path = :target_path,
			failed_stage = :failed_stage,
			failed_reason = :failed_reason,
			failure_kind = :failure_kind,
			failure_code = :failure_code,
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

// ListEntriesByStatuses lists entries matching any of the provided statuses.
// Optional limit (<=0 means no limit). Order by updated_at desc.
func (db *DB) ListEntriesByStatuses(ctx context.Context, statuses []string, limit int) ([]*Entry, error) {
	if len(statuses) == 0 {
		return []*Entry{}, nil
	}

	placeholders := make([]string, 0, len(statuses))
	args := make([]interface{}, 0, len(statuses))
	for _, status := range statuses {
		placeholders = append(placeholders, "?")
		args = append(args, status)
	}

	query := fmt.Sprintf("SELECT * FROM entries WHERE status IN (%s) ORDER BY updated_at DESC", strings.Join(placeholders, ","))
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}

	var entries []*Entry
	if err := db.SelectContext(ctx, &entries, query, args...); err != nil {
		return nil, fmt.Errorf("failed to list entries by statuses: %w", err)
	}

	return entries, nil
}

// ListEntriesByStatusLimited lists entries by status with a limit, ordered by updated_at desc.
func (db *DB) ListEntriesByStatusLimited(ctx context.Context, status string, limit int) ([]*Entry, error) {
	query := `SELECT * FROM entries WHERE status = ? ORDER BY updated_at DESC`
	args := []interface{}{status}
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}

	var entries []*Entry
	if err := db.SelectContext(ctx, &entries, query, args...); err != nil {
		return nil, fmt.Errorf("failed to list entries by status: %w", err)
	}

	return entries, nil
}

// CountEntriesByStatus returns a map of status to count.
func (db *DB) CountEntriesByStatus(ctx context.Context) (map[string]int, error) {
	rows, err := db.QueryxContext(ctx, `SELECT status, COUNT(*) AS count FROM entries GROUP BY status`)
	if err != nil {
		return nil, fmt.Errorf("failed to count entries by status: %w", err)
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, fmt.Errorf("failed to scan status count: %w", err)
		}
		counts[status] = count
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate status counts: %w", err)
	}

	return counts, nil
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
