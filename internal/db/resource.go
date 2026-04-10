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
	ID         string         `db:"id"`
	EntryID    string         `db:"entry_id"`
	Title      string         `db:"title"`
	Magnet     string         `db:"magnet"`
	Size       sql.NullInt64  `db:"size"`
	Seeders    sql.NullInt64  `db:"seeders"`
	Resolution sql.NullString `db:"resolution"`
	Indexer    sql.NullString `db:"indexer"`
	CreatedAt  time.Time      `db:"created_at"`
}

// BatchCreateResources creates multiple resources in a single transaction
func (db *DB) BatchCreateResources(ctx context.Context, resources []*Resource) error {
	if len(resources) == 0 {
		return nil
	}

	return db.WithTx(ctx, func(tx *sqlx.Tx) error {
		query := `
			INSERT INTO resources (
				id, entry_id, title, magnet, size, seeders, resolution, indexer, created_at
			) VALUES (
				:id, :entry_id, :title, :magnet, :size, :seeders, :resolution, :indexer, :created_at
			)
		`

		for _, resource := range resources {
			if resource.ID == "" {
				resource.ID = uuid.New().String()
			}
			resource.CreatedAt = time.Now()

			_, err := tx.NamedExecContext(ctx, query, resource)
			if err != nil {
				return fmt.Errorf("failed to create resource: %w", err)
			}
		}

		return nil
	})
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
