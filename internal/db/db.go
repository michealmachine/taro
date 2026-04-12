package db

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

// DB wraps the database connection
type DB struct {
	*sqlx.DB
}

// Open opens a database connection and runs migrations
func Open(dbPath string) (*DB, error) {
	// Use DSN with pragma parameters to ensure they apply to all connections
	// Note: WAL mode is not supported for in-memory databases (:memory:)
	dsn := dbPath + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)"
	
	db, err := sqlx.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// 配置连接池
	// WAL mode allows multiple concurrent readers and one writer
	// Setting MaxOpenConns > 1 allows better concurrency
	db.SetMaxOpenConns(5) // Allow up to 5 concurrent connections
	db.SetMaxIdleConns(2) // Keep 2 idle connections for reuse

	// Verify PRAGMA settings (these are now set via DSN for all connections)
	var journalMode string
	if err := db.Get(&journalMode, "PRAGMA journal_mode"); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to verify journal mode: %w", err)
	}
	// WAL mode is not supported for in-memory databases, they use "memory" mode
	if journalMode != "wal" && journalMode != "memory" {
		db.Close()
		return nil, fmt.Errorf("unexpected journal mode: %s", journalMode)
	}

	var foreignKeys int
	if err := db.Get(&foreignKeys, "PRAGMA foreign_keys"); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to verify foreign keys: %w", err)
	}
	if foreignKeys != 1 {
		db.Close()
		return nil, fmt.Errorf("failed to enable foreign keys, got: %d", foreignKeys)
	}

	// 运行迁移
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	return &DB{DB: db}, nil
}

// migrate runs database migrations
func migrate(db *sqlx.DB) error {
	_, err := db.Exec(schemaSQL)
	if err != nil {
		return fmt.Errorf("failed to execute schema: %w", err)
	}
	return nil
}

// WithTx executes a function within a transaction
func (db *DB) WithTx(ctx context.Context, fn func(*sqlx.Tx) error) error {
	tx, err := db.BeginTxx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	if err := fn(tx); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			return fmt.Errorf("failed to rollback transaction (original error: %w): %v", err, rbErr)
		}
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}
