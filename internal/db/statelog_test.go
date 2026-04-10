package db

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func TestCreateStateLog(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	// 创建测试条目
	entry := &Entry{
		Title: "Test Entry", MediaType: "movie", Source: "manual", SourceID: "test", Season: 0, Status: "pending",
	}
	if err := db.CreateEntry(ctx, entry); err != nil {
		t.Fatalf("failed to create entry: %v", err)
	}

	// 创建状态日志
	log := &StateLog{
		EntryID:    entry.ID,
		FromStatus: "pending",
		ToStatus:   "searching",
		Reason:     sql.NullString{String: "scheduled search", Valid: true},
	}

	err := db.CreateStateLog(ctx, log)
	if err != nil {
		t.Fatalf("failed to create state log: %v", err)
	}

	if log.ID == 0 {
		t.Error("state log ID should be generated")
	}

	if log.CreatedAt.IsZero() {
		t.Error("created_at should be set")
	}
}

func TestListStateLogsByEntry(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	// 创建两个测试条目
	entry1 := &Entry{
		Title: "Entry 1", MediaType: "movie", Source: "manual", SourceID: "1", Season: 0, Status: "pending",
	}
	entry2 := &Entry{
		Title: "Entry 2", MediaType: "anime", Source: "bangumi", SourceID: "2", Season: 1, Status: "pending",
	}

	if err := db.CreateEntry(ctx, entry1); err != nil {
		t.Fatalf("failed to create entry1: %v", err)
	}
	if err := db.CreateEntry(ctx, entry2); err != nil {
		t.Fatalf("failed to create entry2: %v", err)
	}

	// 为 entry1 创建多个状态日志
	logs1 := []*StateLog{
		{EntryID: entry1.ID, FromStatus: "pending", ToStatus: "searching"},
		{EntryID: entry1.ID, FromStatus: "searching", ToStatus: "found"},
		{EntryID: entry1.ID, FromStatus: "found", ToStatus: "downloading"},
	}

	for _, log := range logs1 {
		if err := db.CreateStateLog(ctx, log); err != nil {
			t.Fatalf("failed to create state log: %v", err)
		}
		time.Sleep(time.Millisecond) // 确保时间戳不同
	}

	// 为 entry2 创建一个状态日志
	log2 := &StateLog{EntryID: entry2.ID, FromStatus: "pending", ToStatus: "searching"}
	if err := db.CreateStateLog(ctx, log2); err != nil {
		t.Fatalf("failed to create state log: %v", err)
	}

	// 列出 entry1 的状态日志
	results, err := db.ListStateLogsByEntry(ctx, entry1.ID)
	if err != nil {
		t.Fatalf("failed to list state logs: %v", err)
	}

	if len(results) != 3 {
		t.Errorf("expected 3 state logs for entry1, got %d", len(results))
	}

	// 验证按时间升序排列
	for i := 0; i < len(results)-1; i++ {
		if results[i].CreatedAt.After(results[i+1].CreatedAt) {
			t.Error("state logs should be ordered by created_at ASC")
		}
	}

	// 验证状态转换顺序
	expectedTransitions := []struct{ from, to string }{
		{"pending", "searching"},
		{"searching", "found"},
		{"found", "downloading"},
	}

	for i, expected := range expectedTransitions {
		if results[i].FromStatus != expected.from || results[i].ToStatus != expected.to {
			t.Errorf("expected transition %s -> %s, got %s -> %s",
				expected.from, expected.to, results[i].FromStatus, results[i].ToStatus)
		}
	}

	// 列出 entry2 的状态日志
	results, err = db.ListStateLogsByEntry(ctx, entry2.ID)
	if err != nil {
		t.Fatalf("failed to list state logs: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("expected 1 state log for entry2, got %d", len(results))
	}
}

func TestDeleteStateLogsByEntry(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	// 创建测试条目
	entry := &Entry{
		Title: "Test Entry", MediaType: "movie", Source: "manual", SourceID: "test", Season: 0, Status: "pending",
	}
	if err := db.CreateEntry(ctx, entry); err != nil {
		t.Fatalf("failed to create entry: %v", err)
	}

	// 创建多个状态日志
	logs := []*StateLog{
		{EntryID: entry.ID, FromStatus: "pending", ToStatus: "searching"},
		{EntryID: entry.ID, FromStatus: "searching", ToStatus: "found"},
		{EntryID: entry.ID, FromStatus: "found", ToStatus: "downloading"},
	}

	for _, log := range logs {
		if err := db.CreateStateLog(ctx, log); err != nil {
			t.Fatalf("failed to create state log: %v", err)
		}
	}

	// 删除所有状态日志
	if err := db.DeleteStateLogsByEntry(ctx, entry.ID); err != nil {
		t.Fatalf("failed to delete state logs: %v", err)
	}

	// 验证状态日志已删除
	results, err := db.ListStateLogsByEntry(ctx, entry.ID)
	if err != nil {
		t.Fatalf("failed to list state logs: %v", err)
	}

	if len(results) != 0 {
		t.Errorf("expected 0 state logs after deletion, got %d", len(results))
	}
}

func TestDeleteOldStateLogs(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	// 创建测试条目
	entry := &Entry{
		Title: "Test Entry", MediaType: "movie", Source: "manual", SourceID: "test", Season: 0, Status: "pending",
	}
	if err := db.CreateEntry(ctx, entry); err != nil {
		t.Fatalf("failed to create entry: %v", err)
	}

	// 创建一个旧的状态日志（手动设置旧时间）
	oldTime := time.Now().AddDate(0, 0, -100) // 100 天前
	_, err := db.Exec(`
		INSERT INTO state_logs (entry_id, from_status, to_status, created_at)
		VALUES (?, ?, ?, ?)
	`, entry.ID, "pending", "searching", oldTime)
	if err != nil {
		t.Fatalf("failed to create old state log: %v", err)
	}

	// 创建一个新的状态日志
	newLog := &StateLog{
		EntryID:    entry.ID,
		FromStatus: "searching",
		ToStatus:   "found",
	}
	if err := db.CreateStateLog(ctx, newLog); err != nil {
		t.Fatalf("failed to create new state log: %v", err)
	}

	// 删除 90 天前的日志
	deleted, err := db.DeleteOldStateLogs(ctx, 90)
	if err != nil {
		t.Fatalf("failed to delete old state logs: %v", err)
	}

	if deleted != 1 {
		t.Errorf("expected 1 deleted log, got %d", deleted)
	}

	// 验证只剩下新日志
	results, err := db.ListStateLogsByEntry(ctx, entry.ID)
	if err != nil {
		t.Fatalf("failed to list state logs: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("expected 1 remaining log, got %d", len(results))
	}

	if results[0].ToStatus != "found" {
		t.Errorf("expected remaining log to be the new one (to_status=found), got %s", results[0].ToStatus)
	}
}

func TestDeleteOldStateLogsZeroDays(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	// 创建测试条目和日志
	entry := &Entry{
		Title: "Test Entry", MediaType: "movie", Source: "manual", SourceID: "test", Season: 0, Status: "pending",
	}
	if err := db.CreateEntry(ctx, entry); err != nil {
		t.Fatalf("failed to create entry: %v", err)
	}

	log := &StateLog{EntryID: entry.ID, FromStatus: "pending", ToStatus: "searching"}
	if err := db.CreateStateLog(ctx, log); err != nil {
		t.Fatalf("failed to create state log: %v", err)
	}

	// days=0 表示永久保留，不应删除任何日志
	deleted, err := db.DeleteOldStateLogs(ctx, 0)
	if err != nil {
		t.Fatalf("failed to delete old state logs: %v", err)
	}

	if deleted != 0 {
		t.Errorf("expected 0 deleted logs when days=0, got %d", deleted)
	}

	// 验证日志仍然存在
	results, err := db.ListStateLogsByEntry(ctx, entry.ID)
	if err != nil {
		t.Fatalf("failed to list state logs: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("expected 1 log to remain, got %d", len(results))
	}
}

func TestStateLogWithReason(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	// 创建测试条目
	entry := &Entry{
		Title: "Test Entry", MediaType: "movie", Source: "manual", SourceID: "test", Season: 0, Status: "pending",
	}
	if err := db.CreateEntry(ctx, entry); err != nil {
		t.Fatalf("failed to create entry: %v", err)
	}

	// 创建带原因的状态日志
	log := &StateLog{
		EntryID:    entry.ID,
		FromStatus: "downloading",
		ToStatus:   "failed",
		Reason:     sql.NullString{String: "PikPak task timeout", Valid: true},
	}

	if err := db.CreateStateLog(ctx, log); err != nil {
		t.Fatalf("failed to create state log: %v", err)
	}

	// 获取并验证
	results, err := db.ListStateLogsByEntry(ctx, entry.ID)
	if err != nil {
		t.Fatalf("failed to list state logs: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 log, got %d", len(results))
	}

	if !results[0].Reason.Valid || results[0].Reason.String != "PikPak task timeout" {
		t.Errorf("expected reason 'PikPak task timeout', got %v", results[0].Reason)
	}
}
