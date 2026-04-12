package downloader

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/michealmachine/taro/internal/config"
	"github.com/michealmachine/taro/internal/db"
)

// GarbageCollector handles PikPak file cleanup and database maintenance
type GarbageCollector struct {
	downloader *PikPakDownloader
	database   *db.DB
	config     *config.Config
	logger     *slog.Logger

	stopChan chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

// NewGarbageCollector creates a new garbage collector
func NewGarbageCollector(downloader *PikPakDownloader, database *db.DB, cfg *config.Config, logger *slog.Logger) *GarbageCollector {
	return &GarbageCollector{
		downloader: downloader,
		database:   database,
		config:     cfg,
		logger:     logger,
		stopChan:   make(chan struct{}),
	}
}

// Start starts the garbage collection goroutine
func (gc *GarbageCollector) Start(ctx context.Context) {
	gc.wg.Add(1)
	go gc.gcLoop(ctx)
	gc.logger.Info("started garbage collector")
}

// Stop stops the garbage collection goroutine
func (gc *GarbageCollector) Stop() {
	gc.stopOnce.Do(func() {
		close(gc.stopChan)
	})
	gc.wg.Wait()
	gc.logger.Info("stopped garbage collector")
}

// gcLoop runs the garbage collection loop
func (gc *GarbageCollector) gcLoop(ctx context.Context) {
	defer gc.wg.Done()

	// Parse GC interval from config (default to 24 hours)
	gcInterval := 24 * time.Hour
	if gc.config.PikPak.GCInterval != "" {
		if duration, err := time.ParseDuration(gc.config.PikPak.GCInterval); err == nil && duration > 0 {
			gcInterval = duration
		} else {
			gc.logger.Warn("invalid pikpak gc_interval, using default 24h", "configured", gc.config.PikPak.GCInterval, "error", err)
		}
	}

	ticker := time.NewTicker(gcInterval)
	defer ticker.Stop()

	// Run GC immediately on startup
	if err := gc.RunGC(ctx); err != nil {
		gc.logger.Error("failed to run initial GC", "error", err)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-gc.stopChan:
			return
		case <-ticker.C:
			if err := gc.RunGC(ctx); err != nil {
				gc.logger.Error("failed to run GC", "error", err)
			}
		}
	}
}

// RunGC runs garbage collection (idempotent)
func (gc *GarbageCollector) RunGC(ctx context.Context) error {
	gc.logger.Info("starting garbage collection")

	// Step 1: Clean PikPak files for failed/cancelled entries
	if err := gc.cleanPikPakFiles(ctx); err != nil {
		gc.logger.Error("failed to clean pikpak files", "error", err)
		// Continue with other cleanup tasks
	}

	// Step 2: Clean resources for terminal state entries
	if err := gc.cleanResources(ctx); err != nil {
		gc.logger.Error("failed to clean resources", "error", err)
		// Continue with other cleanup tasks
	}

	// Step 3: Clean old state logs
	if err := gc.cleanStateLogs(ctx); err != nil {
		gc.logger.Error("failed to clean state logs", "error", err)
		// Continue
	}

	gc.logger.Info("garbage collection completed")
	return nil
}

// cleanPikPakFiles cleans PikPak files for entries that meet cleanup criteria
func (gc *GarbageCollector) cleanPikPakFiles(ctx context.Context) error {
	// Get retention days from config (default to 7 days)
	retentionDays := 7
	if gc.config.PikPak.GCRetentionDays > 0 {
		retentionDays = gc.config.PikPak.GCRetentionDays
	}

	retentionThreshold := time.Now().Add(-time.Duration(retentionDays) * 24 * time.Hour)

	// Query entries that need cleanup:
	// 1. status='failed' AND failed_at < retention_threshold AND pikpak_cleaned=false
	// 2. status='cancelled' AND updated_at < retention_threshold AND pikpak_cleaned=false
	query := `
		SELECT * FROM entries 
		WHERE pikpak_cleaned = false 
		AND pikpak_file_id IS NOT NULL 
		AND pikpak_file_id != ''
		AND (
			(status = 'failed' AND failed_at < ?)
			OR (status = 'cancelled' AND updated_at < ?)
		)
	`

	var entries []*db.Entry
	if err := gc.database.SelectContext(ctx, &entries, query, retentionThreshold, retentionThreshold); err != nil {
		return fmt.Errorf("failed to query entries for cleanup: %w", err)
	}

	if len(entries) == 0 {
		gc.logger.Info("no pikpak files to clean")
		return nil
	}

	gc.logger.Info("found entries for pikpak cleanup", "count", len(entries))

	// Collect file IDs for batch deletion
	fileIDs := make([]string, 0, len(entries))
	entryIDToFileID := make(map[string]string) // entryID -> fileID

	for _, entry := range entries {
		if entry.PikPakFileID.Valid && entry.PikPakFileID.String != "" {
			fileIDs = append(fileIDs, entry.PikPakFileID.String)
			entryIDToFileID[entry.ID] = entry.PikPakFileID.String
		}
	}

	if len(fileIDs) == 0 {
		gc.logger.Info("no valid file IDs to delete")
		return nil
	}

	// Batch delete files from PikPak using pikpaktui CLI
	gc.logger.Info("deleting files from pikpak", "count", len(fileIDs))

	// Delete files one by one (pikpaktui rm supports single file ID)
	// We mark entries as cleaned regardless of deletion success (idempotent)
	for _, fileID := range fileIDs {
		if gc.downloader != nil {
			ctx2, cancel := context.WithTimeout(ctx, 30*time.Second)
			if err := gc.downloader.deleteFile(ctx2, fileID); err != nil {
				gc.logger.Warn("failed to delete file from pikpak", "file_id", fileID, "error", err)
			} else {
				gc.logger.Info("deleted file from pikpak", "file_id", fileID)
			}
			cancel()
		} else {
			gc.logger.Warn("downloader not available, skipping file deletion", "file_id", fileID)
		}
	}

	// Mark entries as cleaned (idempotent - safe to mark even if deletion failed)
	for _, entry := range entries {
		entry.PikPakCleaned = true
		if err := gc.database.UpdateEntry(ctx, entry); err != nil {
			gc.logger.Error("failed to mark entry as cleaned", "entry_id", entry.ID, "error", err)
			// Continue with other entries
		} else {
			gc.logger.Info("marked entry as cleaned", "entry_id", entry.ID, "file_id", entryIDToFileID[entry.ID])
		}
	}

	return nil
}

// cleanResources cleans resources for terminal state entries
func (gc *GarbageCollector) cleanResources(ctx context.Context) error {
	// Only clean if configured
	if !gc.config.Retention.CleanResourcesOnComplete {
		gc.logger.Debug("resource cleanup disabled in config")
		return nil
	}

	// Query terminal state entries: in_library, cancelled
	query := `
		SELECT id FROM entries 
		WHERE status IN ('in_library', 'cancelled')
	`

	var entryIDs []string
	if err := gc.database.SelectContext(ctx, &entryIDs, query); err != nil {
		return fmt.Errorf("failed to query terminal entries: %w", err)
	}

	if len(entryIDs) == 0 {
		gc.logger.Debug("no terminal entries found for resource cleanup")
		return nil
	}

	gc.logger.Info("cleaning resources for terminal entries", "count", len(entryIDs))

	// Delete resources for each entry
	deletedCount := 0
	for _, entryID := range entryIDs {
		if err := gc.database.DeleteResourcesByEntry(ctx, entryID); err != nil {
			gc.logger.Error("failed to delete resources", "entry_id", entryID, "error", err)
			// Continue with other entries
		} else {
			deletedCount++
		}
	}

	gc.logger.Info("cleaned resources", "entries_processed", deletedCount, "total", len(entryIDs))
	return nil
}

// cleanStateLogs cleans old state logs
func (gc *GarbageCollector) cleanStateLogs(ctx context.Context) error {
	retentionDays := gc.config.Retention.StateLogsDays
	
	// 0 means keep forever
	if retentionDays == 0 {
		gc.logger.Debug("state log retention set to 0 (keep forever)")
		return nil
	}

	if retentionDays < 0 {
		gc.logger.Warn("invalid state_logs_days, skipping cleanup", "value", retentionDays)
		return nil
	}

	retentionThreshold := time.Now().Add(-time.Duration(retentionDays) * 24 * time.Hour)

	query := `DELETE FROM state_logs WHERE created_at < ?`
	result, err := gc.database.ExecContext(ctx, query, retentionThreshold)
	if err != nil {
		return fmt.Errorf("failed to delete old state logs: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected > 0 {
		gc.logger.Info("cleaned old state logs", "count", rowsAffected, "retention_days", retentionDays)
	}

	return nil
}
