package db

import (
	"context"
	"database/sql"
	"testing"
)

func TestCreateEntry(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	entry := &Entry{
		Title:     "Test Movie",
		MediaType: "movie",
		Source:    "manual",
		SourceID:  "test-source-1",
		Season:    0,
		Status:    "pending",
		AskMode:   0,
	}

	err := db.CreateEntry(ctx, entry)
	if err != nil {
		t.Fatalf("failed to create entry: %v", err)
	}

	if entry.ID == "" {
		t.Error("entry ID should be generated")
	}

	if entry.CreatedAt.IsZero() {
		t.Error("created_at should be set")
	}

	if entry.UpdatedAt.IsZero() {
		t.Error("updated_at should be set")
	}
}

func TestGetEntry(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	// 创建测试条目
	entry := &Entry{
		Title:     "Test Anime",
		MediaType: "anime",
		Source:    "bangumi",
		SourceID:  "12345",
		Season:    1,
		Status:    "pending",
	}

	if err := db.CreateEntry(ctx, entry); err != nil {
		t.Fatalf("failed to create entry: %v", err)
	}

	// 获取条目
	retrieved, err := db.GetEntry(ctx, entry.ID)
	if err != nil {
		t.Fatalf("failed to get entry: %v", err)
	}

	if retrieved.ID != entry.ID {
		t.Errorf("expected ID %s, got %s", entry.ID, retrieved.ID)
	}

	if retrieved.Title != entry.Title {
		t.Errorf("expected title %s, got %s", entry.Title, retrieved.Title)
	}

	// 测试不存在的条目
	_, err = db.GetEntry(ctx, "non-existent-id")
	if err == nil {
		t.Error("expected error for non-existent entry")
	}
}

func TestUpdateEntry(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	// 创建测试条目
	entry := &Entry{
		Title:     "Original Title",
		MediaType: "tv",
		Source:    "trakt",
		SourceID:  "test-show",
		Season:    1,
		Status:    "pending",
	}

	if err := db.CreateEntry(ctx, entry); err != nil {
		t.Fatalf("failed to create entry: %v", err)
	}

	// 更新条目
	entry.Status = "searching"
	entry.Title = "Updated Title"

	if err := db.UpdateEntry(ctx, entry); err != nil {
		t.Fatalf("failed to update entry: %v", err)
	}

	// 验证更新
	retrieved, err := db.GetEntry(ctx, entry.ID)
	if err != nil {
		t.Fatalf("failed to get entry: %v", err)
	}

	if retrieved.Status != "searching" {
		t.Errorf("expected status searching, got %s", retrieved.Status)
	}

	if retrieved.Title != "Updated Title" {
		t.Errorf("expected title 'Updated Title', got %s", retrieved.Title)
	}

	// 测试更新不存在的条目
	nonExistent := &Entry{ID: "non-existent"}
	err = db.UpdateEntry(ctx, nonExistent)
	if err == nil {
		t.Error("expected error when updating non-existent entry")
	}
}

func TestListEntries(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	// 创建多个测试条目
	entries := []*Entry{
		{Title: "Movie 1", MediaType: "movie", Source: "manual", SourceID: "m1", Season: 0, Status: "pending"},
		{Title: "Anime 1", MediaType: "anime", Source: "bangumi", SourceID: "a1", Season: 1, Status: "searching"},
		{Title: "TV 1", MediaType: "tv", Source: "trakt", SourceID: "t1", Season: 1, Status: "pending"},
	}

	for _, entry := range entries {
		if err := db.CreateEntry(ctx, entry); err != nil {
			t.Fatalf("failed to create entry: %v", err)
		}
	}

	t.Run("list all entries", func(t *testing.T) {
		results, err := db.ListEntries(ctx, nil)
		if err != nil {
			t.Fatalf("failed to list entries: %v", err)
		}

		if len(results) != 3 {
			t.Errorf("expected 3 entries, got %d", len(results))
		}
	})

	t.Run("filter by status", func(t *testing.T) {
		filters := map[string]interface{}{"status": "pending"}
		results, err := db.ListEntries(ctx, filters)
		if err != nil {
			t.Fatalf("failed to list entries: %v", err)
		}

		if len(results) != 2 {
			t.Errorf("expected 2 pending entries, got %d", len(results))
		}

		for _, entry := range results {
			if entry.Status != "pending" {
				t.Errorf("expected status pending, got %s", entry.Status)
			}
		}
	})

	t.Run("filter by source", func(t *testing.T) {
		filters := map[string]interface{}{"source": "bangumi"}
		results, err := db.ListEntries(ctx, filters)
		if err != nil {
			t.Fatalf("failed to list entries: %v", err)
		}

		if len(results) != 1 {
			t.Errorf("expected 1 bangumi entry, got %d", len(results))
		}

		if results[0].Source != "bangumi" {
			t.Errorf("expected source bangumi, got %s", results[0].Source)
		}
	})
}

func TestListEntriesByStatus(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	// 创建测试条目
	statuses := []string{"pending", "searching", "pending", "found"}
	for i, status := range statuses {
		entry := &Entry{
			Title:     "Test Entry",
			MediaType: "movie",
			Source:    "manual",
			SourceID:  string(rune('a' + i)),
			Season:    0,
			Status:    status,
		}
		if err := db.CreateEntry(ctx, entry); err != nil {
			t.Fatalf("failed to create entry: %v", err)
		}
	}

	results, err := db.ListEntriesByStatus(ctx, "pending")
	if err != nil {
		t.Fatalf("failed to list entries by status: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("expected 2 pending entries, got %d", len(results))
	}
}

func TestEntryExists(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	// 创建测试条目
	entry := &Entry{
		Title:     "Test Entry",
		MediaType: "anime",
		Source:    "bangumi",
		SourceID:  "12345",
		Season:    1,
		Status:    "pending",
	}

	if err := db.CreateEntry(ctx, entry); err != nil {
		t.Fatalf("failed to create entry: %v", err)
	}

	t.Run("entry exists", func(t *testing.T) {
		exists, err := db.EntryExists(ctx, "bangumi", "12345", 1)
		if err != nil {
			t.Fatalf("failed to check entry existence: %v", err)
		}

		if !exists {
			t.Error("expected entry to exist")
		}
	})

	t.Run("entry does not exist", func(t *testing.T) {
		exists, err := db.EntryExists(ctx, "bangumi", "99999", 1)
		if err != nil {
			t.Fatalf("failed to check entry existence: %v", err)
		}

		if exists {
			t.Error("expected entry to not exist")
		}
	})

	t.Run("different season", func(t *testing.T) {
		exists, err := db.EntryExists(ctx, "bangumi", "12345", 2)
		if err != nil {
			t.Fatalf("failed to check entry existence: %v", err)
		}

		if exists {
			t.Error("expected entry with different season to not exist")
		}
	})
}

func TestEntryUniqueConstraint(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	// 创建第一个条目
	entry1 := &Entry{
		Title:     "Test Entry",
		MediaType: "anime",
		Source:    "bangumi",
		SourceID:  "12345",
		Season:    1,
		Status:    "pending",
	}

	if err := db.CreateEntry(ctx, entry1); err != nil {
		t.Fatalf("failed to create first entry: %v", err)
	}

	// 尝试创建重复条目（相同 source, source_id, season）
	entry2 := &Entry{
		Title:     "Duplicate Entry",
		MediaType: "anime",
		Source:    "bangumi",
		SourceID:  "12345",
		Season:    1,
		Status:    "pending",
	}

	err := db.CreateEntry(ctx, entry2)
	if err == nil {
		t.Error("expected error when creating duplicate entry")
	}

	// 创建不同季的条目应该成功
	entry3 := &Entry{
		Title:     "Different Season",
		MediaType: "anime",
		Source:    "bangumi",
		SourceID:  "12345",
		Season:    2,
		Status:    "pending",
	}

	if err := db.CreateEntry(ctx, entry3); err != nil {
		t.Errorf("failed to create entry with different season: %v", err)
	}
}

func TestEntryNullableFields(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	entry := &Entry{
		Title:        "Test Entry",
		MediaType:    "movie",
		Source:       "manual",
		SourceID:     "test-1",
		Season:       0,
		Status:       "downloading",
		PikPakTaskID: sql.NullString{String: "pikpak-task-123", Valid: true},
		Resolution:   sql.NullString{String: "1080p", Valid: true},
	}

	if err := db.CreateEntry(ctx, entry); err != nil {
		t.Fatalf("failed to create entry: %v", err)
	}

	retrieved, err := db.GetEntry(ctx, entry.ID)
	if err != nil {
		t.Fatalf("failed to get entry: %v", err)
	}

	if !retrieved.PikPakTaskID.Valid || retrieved.PikPakTaskID.String != "pikpak-task-123" {
		t.Errorf("expected pikpak_task_id 'pikpak-task-123', got %v", retrieved.PikPakTaskID)
	}

	if !retrieved.Resolution.Valid || retrieved.Resolution.String != "1080p" {
		t.Errorf("expected resolution '1080p', got %v", retrieved.Resolution)
	}
}
