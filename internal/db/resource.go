package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// Resource represents a candidate resource for an entry
type Resource struct {
	ID             string         `db:"id"`
	EntryID        string         `db:"entry_id"`
	Title          string         `db:"title"`
	Magnet         string         `db:"magnet"`
	Size           sql.NullInt64  `db:"size"`
	Seeders        sql.NullInt64  `db:"seeders"`
	Resolution     sql.NullString `db:"resolution"`
	Codec          sql.NullString `db:"codec"` // 解析出的编码（'x264'|'x265'|'av1'|'unknown'）
	Indexer        sql.NullString `db:"indexer"`
	Eligible       bool           `db:"eligible"`        // 是否可选（0=被过滤，不参与自动选择和 UI 展示）
	Score          sql.NullInt64  `db:"score"`           // 综合评分快照（仅 eligible=1 时有意义）
	Selected       bool           `db:"selected"`        // 是否被选中（最终选中的资源）
	RejectedReason sql.NullString `db:"rejected_reason"` // 被过滤的原因（仅 eligible=0 时有意义）
	CreatedAt      time.Time      `db:"created_at"`
}

// BatchCreateResources creates multiple resources in a single transaction
func (db *DB) BatchCreateResources(ctx context.Context, resources []*Resource) error {
	if len(resources) == 0 {
		return nil
	}

	return db.WithTx(ctx, func(tx *sqlx.Tx) error {
		for _, resource := range resources {
			if err := CreateResourceTx(ctx, tx, resource); err != nil {
				return err
			}
		}
		return nil
	})
}

// CreateResourceTx creates a resource within a transaction
func CreateResourceTx(ctx context.Context, tx *sqlx.Tx, resource *Resource) error {
	query := `
		INSERT INTO resources (
			id, entry_id, title, magnet, size, seeders, resolution, codec, indexer,
			eligible, score, selected, rejected_reason, created_at
		) VALUES (
			:id, :entry_id, :title, :magnet, :size, :seeders, :resolution, :codec, :indexer,
			:eligible, :score, :selected, :rejected_reason, :created_at
		)
	`

	if resource.ID == "" {
		resource.ID = uuid.New().String()
	}
	resource.CreatedAt = time.Now()

	_, err := tx.NamedExecContext(ctx, query, resource)
	if err != nil {
		return fmt.Errorf("failed to create resource: %w", err)
	}

	return nil
}

// ListResourcesByEntry lists all resources for an entry
func (db *DB) ListResourcesByEntry(ctx context.Context, entryID string) ([]*Resource, error) {
	var resources []*Resource
	query := `SELECT * FROM resources WHERE entry_id = ? ORDER BY created_at DESC`

	err := db.SelectContext(ctx, &resources, query, entryID)
	if err != nil {
		return nil, fmt.Errorf("failed to list resources: %w", err)
	}

	return resources, nil
}

// ListEligibleByEntry lists only eligible resources for an entry (eligible=1)
// Used for auto-selection and UI display
func (db *DB) ListEligibleByEntry(ctx context.Context, entryID string) ([]*Resource, error) {
	var resources []*Resource
	query := `SELECT * FROM resources WHERE entry_id = ? AND eligible = 1 ORDER BY score DESC, created_at DESC`

	err := db.SelectContext(ctx, &resources, query, entryID)
	if err != nil {
		return nil, fmt.Errorf("failed to list eligible resources: %w", err)
	}

	return resources, nil
}

// GetResource retrieves a resource by ID
func (db *DB) GetResource(ctx context.Context, id string) (*Resource, error) {
	var resource Resource
	query := `SELECT * FROM resources WHERE id = ?`

	err := db.GetContext(ctx, &resource, query, id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("resource not found: %s", id)
		}
		return nil, fmt.Errorf("failed to get resource: %w", err)
	}

	return &resource, nil
}

// DeleteResourcesByEntry deletes all resources for an entry
func (db *DB) DeleteResourcesByEntry(ctx context.Context, entryID string) error {
	query := `DELETE FROM resources WHERE entry_id = ?`

	_, err := db.ExecContext(ctx, query, entryID)
	if err != nil {
		return fmt.Errorf("failed to delete resources: %w", err)
	}

	return nil
}

// DeleteResource deletes a single resource by ID
func (db *DB) DeleteResource(ctx context.Context, id string) error {
	query := `DELETE FROM resources WHERE id = ?`

	result, err := db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to delete resource: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("resource not found: %s", id)
	}

	return nil
}

// UpdateResource updates a resource
func (db *DB) UpdateResource(ctx context.Context, resource *Resource) error {
	query := `
		UPDATE resources SET
			title = :title,
			magnet = :magnet,
			size = :size,
			seeders = :seeders,
			resolution = :resolution,
			codec = :codec,
			indexer = :indexer,
			eligible = :eligible,
			score = :score,
			selected = :selected,
			rejected_reason = :rejected_reason
		WHERE id = :id
	`

	result, err := db.NamedExecContext(ctx, query, resource)
	if err != nil {
		return fmt.Errorf("failed to update resource: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("resource not found: %s", resource.ID)
	}

	return nil
}
