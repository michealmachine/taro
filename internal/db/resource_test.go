package db

import (
	"context"
	"database/sql"
	"testing"
)

func TestBatchCreateResources(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	// 创建测试条目
	entry := &Entry{
		Title:     "Test Entry",
		MediaType: "anime",
		Source:    "bangumi",
		SourceID:  "12345",
		Season:    1,
		Status:    "searching",
	}

	if err := db.CreateEntry(ctx, entry); err != nil {
		t.Fatalf("failed to create entry: %v", err)
	}

	// 批量创建资源
	resources := []*Resource{
		{
			EntryID:    entry.ID,
			Title:      "[SubGroup] Anime S01E01 1080p",
			Magnet:     "magnet:?xt=urn:btih:1234567890",
			Size:       sql.NullInt64{Int64: 1024 * 1024 * 500, Valid: true},
			Seeders:    sql.NullInt64{Int64: 100, Valid: true},
			Resolution: sql.NullString{String: "1080p", Valid: true},
			Indexer:    sql.NullString{String: "Nyaa", Valid: true},
		},
		{
			EntryID:    entry.ID,
			Title:      "[SubGroup] Anime S01E01 720p",
			Magnet:     "magnet:?xt=urn:btih:0987654321",
			Size:       sql.NullInt64{Int64: 1024 * 1024 * 300, Valid: true},
			Seeders:    sql.NullInt64{Int64: 50, Valid: true},
			Resolution: sql.NullString{String: "720p", Valid: true},
			Indexer:    sql.NullString{String: "Nyaa", Valid: true},
		},
	}

	err := db.BatchCreateResources(ctx, resources)
	if err != nil {
		t.Fatalf("failed to batch create resources: %v", err)
	}

	// 验证资源已创建
	for _, resource := range resources {
		if resource.ID == "" {
			t.Error("resource ID should be generated")
		}
		if resource.CreatedAt.IsZero() {
			t.Error("created_at should be set")
		}
	}

	// 验证数据库中的资源数量
	var count int
	if err := db.Get(&count, `SELECT COUNT(*) FROM resources WHERE entry_id = ?`, entry.ID); err != nil {
		t.Fatalf("failed to count resources: %v", err)
	}

	if count != 2 {
		t.Errorf("expected 2 resources, got %d", count)
	}
}

func TestBatchCreateResourcesEmpty(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	// 测试空数组
	err := db.BatchCreateResources(ctx, []*Resource{})
	if err != nil {
		t.Errorf("batch create with empty array should not error: %v", err)
	}
}

func TestListResourcesByEntry(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	// 创建两个测试条目
	entry1 := &Entry{
		Title: "Entry 1", MediaType: "anime", Source: "bangumi", SourceID: "1", Season: 1, Status: "searching",
	}
	entry2 := &Entry{
		Title: "Entry 2", MediaType: "anime", Source: "bangumi", SourceID: "2", Season: 1, Status: "searching",
	}

	if err := db.CreateEntry(ctx, entry1); err != nil {
		t.Fatalf("failed to create entry1: %v", err)
	}
	if err := db.CreateEntry(ctx, entry2); err != nil {
		t.Fatalf("failed to create entry2: %v", err)
	}

	// 为 entry1 创建资源
	resources1 := []*Resource{
		{EntryID: entry1.ID, Title: "Resource 1-1", Magnet: "magnet:1"},
		{EntryID: entry1.ID, Title: "Resource 1-2", Magnet: "magnet:2"},
	}

	// 为 entry2 创建资源
	resources2 := []*Resource{
		{EntryID: entry2.ID, Title: "Resource 2-1", Magnet: "magnet:3"},
	}

	if err := db.BatchCreateResources(ctx, resources1); err != nil {
		t.Fatalf("failed to create resources1: %v", err)
	}
	if err := db.BatchCreateResources(ctx, resources2); err != nil {
		t.Fatalf("failed to create resources2: %v", err)
	}

	// 列出 entry1 的资源
	results, err := db.ListResourcesByEntry(ctx, entry1.ID)
	if err != nil {
		t.Fatalf("failed to list resources: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("expected 2 resources for entry1, got %d", len(results))
	}

	for _, resource := range results {
		if resource.EntryID != entry1.ID {
			t.Errorf("expected entry_id %s, got %s", entry1.ID, resource.EntryID)
		}
	}

	// 列出 entry2 的资源
	results, err = db.ListResourcesByEntry(ctx, entry2.ID)
	if err != nil {
		t.Fatalf("failed to list resources: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("expected 1 resource for entry2, got %d", len(results))
	}
}

func TestGetResource(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	// 创建测试条目和资源
	entry := &Entry{
		Title: "Test Entry", MediaType: "movie", Source: "manual", SourceID: "test", Season: 0, Status: "searching",
	}
	if err := db.CreateEntry(ctx, entry); err != nil {
		t.Fatalf("failed to create entry: %v", err)
	}

	resources := []*Resource{
		{
			EntryID:    entry.ID,
			Title:      "Test Resource",
			Magnet:     "magnet:test",
			Resolution: sql.NullString{String: "1080p", Valid: true},
		},
	}

	if err := db.BatchCreateResources(ctx, resources); err != nil {
		t.Fatalf("failed to create resources: %v", err)
	}

	// 获取资源
	resource, err := db.GetResource(ctx, resources[0].ID)
	if err != nil {
		t.Fatalf("failed to get resource: %v", err)
	}

	if resource.ID != resources[0].ID {
		t.Errorf("expected ID %s, got %s", resources[0].ID, resource.ID)
	}

	if resource.Title != "Test Resource" {
		t.Errorf("expected title 'Test Resource', got %s", resource.Title)
	}

	// 测试不存在的资源
	_, err = db.GetResource(ctx, "non-existent-id")
	if err == nil {
		t.Error("expected error for non-existent resource")
	}
}

func TestDeleteResourcesByEntry(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	// 创建测试条目和资源
	entry := &Entry{
		Title: "Test Entry", MediaType: "anime", Source: "bangumi", SourceID: "123", Season: 1, Status: "searching",
	}
	if err := db.CreateEntry(ctx, entry); err != nil {
		t.Fatalf("failed to create entry: %v", err)
	}

	resources := []*Resource{
		{EntryID: entry.ID, Title: "Resource 1", Magnet: "magnet:1"},
		{EntryID: entry.ID, Title: "Resource 2", Magnet: "magnet:2"},
		{EntryID: entry.ID, Title: "Resource 3", Magnet: "magnet:3"},
	}

	if err := db.BatchCreateResources(ctx, resources); err != nil {
		t.Fatalf("failed to create resources: %v", err)
	}

	// 删除所有资源
	if err := db.DeleteResourcesByEntry(ctx, entry.ID); err != nil {
		t.Fatalf("failed to delete resources: %v", err)
	}

	// 验证资源已删除
	results, err := db.ListResourcesByEntry(ctx, entry.ID)
	if err != nil {
		t.Fatalf("failed to list resources: %v", err)
	}

	if len(results) != 0 {
		t.Errorf("expected 0 resources after deletion, got %d", len(results))
	}
}

func TestDeleteResource(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	// 创建测试条目和资源
	entry := &Entry{
		Title: "Test Entry", MediaType: "movie", Source: "manual", SourceID: "test", Season: 0, Status: "searching",
	}
	if err := db.CreateEntry(ctx, entry); err != nil {
		t.Fatalf("failed to create entry: %v", err)
	}

	resources := []*Resource{
		{EntryID: entry.ID, Title: "Resource 1", Magnet: "magnet:1"},
		{EntryID: entry.ID, Title: "Resource 2", Magnet: "magnet:2"},
	}

	if err := db.BatchCreateResources(ctx, resources); err != nil {
		t.Fatalf("failed to create resources: %v", err)
	}

	// 删除单个资源
	if err := db.DeleteResource(ctx, resources[0].ID); err != nil {
		t.Fatalf("failed to delete resource: %v", err)
	}

	// 验证资源已删除
	_, err := db.GetResource(ctx, resources[0].ID)
	if err == nil {
		t.Error("expected error when getting deleted resource")
	}

	// 验证另一个资源仍然存在
	_, err = db.GetResource(ctx, resources[1].ID)
	if err != nil {
		t.Errorf("expected resource 2 to still exist: %v", err)
	}

	// 测试删除不存在的资源
	err = db.DeleteResource(ctx, "non-existent-id")
	if err == nil {
		t.Error("expected error when deleting non-existent resource")
	}
}
