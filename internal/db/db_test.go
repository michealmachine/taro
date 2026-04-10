package db

import (
	"context"
	"os"
	"testing"

	"github.com/jmoiron/sqlx"
)

func setupTestDB(t *testing.T) *DB {
	t.Helper()

	// 创建临时数据库文件
	tmpFile, err := os.CreateTemp("", "taro_test_*.db")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	tmpFile.Close()

	// 打开数据库
	db, err := Open(tmpFile.Name())
	if err != nil {
		os.Remove(tmpFile.Name())
		t.Fatalf("failed to open database: %v", err)
	}

	// 清理函数
	t.Cleanup(func() {
		db.Close()
		os.Remove(tmpFile.Name())
	})

	return db
}

func TestOpen(t *testing.T) {
	db := setupTestDB(t)

	// 验证数据库连接
	if err := db.Ping(); err != nil {
		t.Errorf("failed to ping database: %v", err)
	}

	// 验证表是否创建
	tables := []string{"entries", "resources", "state_logs"}
	for _, table := range tables {
		var count int
		query := `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`
		if err := db.Get(&count, query, table); err != nil {
			t.Errorf("failed to check table %s: %v", table, err)
		}
		if count != 1 {
			t.Errorf("table %s not created", table)
		}
	}
}

func TestWithTx(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	t.Run("successful transaction", func(t *testing.T) {
		err := db.WithTx(ctx, func(tx *sqlx.Tx) error {
			_, err := tx.Exec(`INSERT INTO entries (id, title, media_type, source, source_id, season, status) VALUES (?, ?, ?, ?, ?, ?, ?)`,
				"test-id", "Test Title", "movie", "manual", "test-source-id", 0, "pending")
			return err
		})

		if err != nil {
			t.Errorf("transaction failed: %v", err)
		}

		// 验证数据已提交
		var count int
		if err := db.Get(&count, `SELECT COUNT(*) FROM entries WHERE id = ?`, "test-id"); err != nil {
			t.Errorf("failed to verify transaction: %v", err)
		}
		if count != 1 {
			t.Errorf("expected 1 entry, got %d", count)
		}
	})

	t.Run("failed transaction rollback", func(t *testing.T) {
		err := db.WithTx(ctx, func(tx *sqlx.Tx) error {
			_, err := tx.Exec(`INSERT INTO entries (id, title, media_type, source, source_id, season, status) VALUES (?, ?, ?, ?, ?, ?, ?)`,
				"test-id-2", "Test Title 2", "movie", "manual", "test-source-id-2", 0, "pending")
			if err != nil {
				return err
			}
			// 强制返回错误以触发回滚
			return os.ErrInvalid
		})

		if err == nil {
			t.Error("expected transaction to fail")
		}

		// 验证数据已回滚
		var count int
		if err := db.Get(&count, `SELECT COUNT(*) FROM entries WHERE id = ?`, "test-id-2"); err != nil {
			t.Errorf("failed to verify rollback: %v", err)
		}
		if count != 0 {
			t.Errorf("expected 0 entries after rollback, got %d", count)
		}
	})
}
