package scheduler

import (
	"context"
	"log/slog"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/michealmachine/taro/internal/config"
	"github.com/michealmachine/taro/internal/db"
)

func setupTestDB(t *testing.T) *db.DB {
	t.Helper()

	// 创建临时数据库文件
	tmpFile, err := os.CreateTemp("", "taro_test_*.db")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	tmpFile.Close()

	// 打开数据库
	database, err := db.Open(tmpFile.Name())
	if err != nil {
		os.Remove(tmpFile.Name())
		t.Fatalf("failed to open database: %v", err)
	}

	// 清理函数
	t.Cleanup(func() {
		database.Close()
		os.Remove(tmpFile.Name())
	})

	return database
}

func TestScheduler_Start_Stop(t *testing.T) {
	// Create test database
	database := setupTestDB(t)

	// Create test config
	cfg := &config.Config{
		Defaults: config.DefaultsConfig{
			MaxConcurrentSearches: 3,
		},
		Bangumi: config.BangumiConfig{
			PollInterval: "1h",
		},
		Trakt: config.TraktConfig{
			PollInterval: "1h",
		},
		OneDrive: config.OneDriveConfig{
			HealthCheckInterval: "10m",
		},
		PikPak: config.PikPakConfig{
			GCInterval: "24h",
		},
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// Create scheduler
	scheduler := NewScheduler(cfg, database, logger, nil)

	// Start scheduler
	if err := scheduler.Start(); err != nil {
		t.Fatalf("failed to start scheduler: %v", err)
	}

	// Wait a bit
	time.Sleep(100 * time.Millisecond)

	// Stop scheduler
	scheduler.Stop()
}

func TestScheduler_TaskExecution(t *testing.T) {
	// Create test database
	database := setupTestDB(t)

	// Create test config
	cfg := &config.Config{
		Defaults: config.DefaultsConfig{
			MaxConcurrentSearches: 3,
		},
		Bangumi: config.BangumiConfig{
			PollInterval: "1h",
		},
		Trakt: config.TraktConfig{
			PollInterval: "1h",
		},
		OneDrive: config.OneDriveConfig{
			HealthCheckInterval: "10m",
		},
		PikPak: config.PikPakConfig{
			GCInterval: "24h",
		},
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// Track task executions
	var pendingCalled atomic.Int32
	var foundCalled atomic.Int32
	var downloadedCalled atomic.Int32

	handlers := &TaskHandlers{
		OnPendingTask: func(ctx context.Context) error {
			pendingCalled.Add(1)
			return nil
		},
		OnFoundTask: func(ctx context.Context) error {
			foundCalled.Add(1)
			return nil
		},
		OnDownloadedTask: func(ctx context.Context) error {
			downloadedCalled.Add(1)
			return nil
		},
	}

	// Create scheduler
	scheduler := NewScheduler(cfg, database, logger, handlers)

	// Manually trigger tasks instead of waiting for cron
	ctx := context.Background()
	_ = scheduler.processPendingEntries(ctx)
	_ = scheduler.processFoundEntries(ctx)
	_ = scheduler.processDownloadedEntries(ctx)

	// Verify tasks were called
	if pendingCalled.Load() != 1 {
		t.Errorf("expected pending task to be called once, got %d", pendingCalled.Load())
	}
	if foundCalled.Load() != 1 {
		t.Errorf("expected found task to be called once, got %d", foundCalled.Load())
	}
	if downloadedCalled.Load() != 1 {
		t.Errorf("expected downloaded task to be called once, got %d", downloadedCalled.Load())
	}
}

func TestScheduler_SearchSemaphore(t *testing.T) {
	// Create test database
	database := setupTestDB(t)

	// Create test config with max 2 concurrent searches
	cfg := &config.Config{
		Defaults: config.DefaultsConfig{
			MaxConcurrentSearches: 2,
		},
		Bangumi: config.BangumiConfig{
			PollInterval: "1h",
		},
		Trakt: config.TraktConfig{
			PollInterval: "1h",
		},
		OneDrive: config.OneDriveConfig{
			HealthCheckInterval: "10m",
		},
		PikPak: config.PikPakConfig{
			GCInterval: "24h",
		},
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// Create scheduler
	scheduler := NewScheduler(cfg, database, logger, nil)

	ctx := context.Background()

	// Acquire 2 slots (should succeed)
	if err := scheduler.AcquireSearchSlot(ctx); err != nil {
		t.Fatalf("failed to acquire first slot: %v", err)
	}
	if err := scheduler.AcquireSearchSlot(ctx); err != nil {
		t.Fatalf("failed to acquire second slot: %v", err)
	}

	// Try to acquire third slot with timeout (should fail)
	ctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()

	if err := scheduler.AcquireSearchSlot(ctx); err == nil {
		t.Error("expected error when acquiring third slot, got nil")
	}

	// Release one slot
	scheduler.ReleaseSearchSlot()

	// Now should be able to acquire again
	ctx2 := context.Background()
	if err := scheduler.AcquireSearchSlot(ctx2); err != nil {
		t.Fatalf("failed to acquire slot after release: %v", err)
	}
}

func TestScheduler_SkipIfRunning(t *testing.T) {
	// Create test database
	database := setupTestDB(t)

	// Create test config
	cfg := &config.Config{
		Defaults: config.DefaultsConfig{
			MaxConcurrentSearches: 3,
		},
		Bangumi: config.BangumiConfig{
			PollInterval: "1h",
		},
		Trakt: config.TraktConfig{
			PollInterval: "1h",
		},
		OneDrive: config.OneDriveConfig{
			HealthCheckInterval: "10m",
		},
		PikPak: config.PikPakConfig{
			GCInterval: "24h",
		},
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// Track task executions
	var pendingCalled atomic.Int32
	blockChan := make(chan struct{})

	handlers := &TaskHandlers{
		OnPendingTask: func(ctx context.Context) error {
			pendingCalled.Add(1)
			// Block until channel is closed
			<-blockChan
			return nil
		},
	}

	// Create scheduler
	scheduler := NewScheduler(cfg, database, logger, handlers)

	// Manually trigger task twice
	go func() {
		_ = scheduler.processPendingEntries(context.Background())
	}()

	// Wait a bit to ensure first task is running
	time.Sleep(50 * time.Millisecond)

	// Try to trigger again (should be skipped)
	_ = scheduler.processPendingEntries(context.Background())

	// Unblock first task
	close(blockChan)

	// Wait for completion
	time.Sleep(100 * time.Millisecond)

	// Should only be called once (second call was skipped)
	if pendingCalled.Load() != 1 {
		t.Errorf("expected pending task to be called once, got %d", pendingCalled.Load())
	}
}

func TestIntervalToCron(t *testing.T) {
	tests := []struct {
		interval string
		want     string
		wantErr  bool
	}{
		{"30s", "*/30 * * * * *", false},
		{"5m", "0 */5 * * * *", false},
		{"10m", "0 */10 * * * *", false},
		{"1h", "0 0 */1 * * *", false},
		{"6h", "0 0 */6 * * *", false},
		{"24h", "0 0 0 * * *", false},
		{"48h", "0 0 0 * * *", false},
		{"invalid", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.interval, func(t *testing.T) {
			got, err := intervalToCron(tt.interval)
			if (err != nil) != tt.wantErr {
				t.Errorf("intervalToCron() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("intervalToCron() = %v, want %v", got, tt.want)
			}
		})
	}
}
