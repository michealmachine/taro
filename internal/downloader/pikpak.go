package downloader

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	pikpakgo "github.com/lyqingye/pikpak-go"
	"github.com/michealmachine/taro/internal/config"
	"github.com/michealmachine/taro/internal/db"
	"github.com/michealmachine/taro/internal/state"
)

// PikPakDownloader manages downloads via PikPak
type PikPakDownloader struct {
	client   *pikpakgo.PikPakClient
	config   *config.Config
	database *db.DB
	sm       *state.StateMachine
	logger   *slog.Logger

	// Polling queue management
	pollingQueue map[string]*PollingTask // entryID -> task
	queueMu      sync.RWMutex
	stopChan     chan struct{}
	stopOnce     sync.Once // Protect against double-close
	wg           sync.WaitGroup
}

// PollingTask represents a task being polled
type PollingTask struct {
	EntryID    string
	TaskID     string
	SubmitTime time.Time
}

// NewPikPakDownloader creates a new PikPak downloader
func NewPikPakDownloader(cfg *config.Config, database *db.DB, sm *state.StateMachine, logger *slog.Logger) (*PikPakDownloader, error) {
	client, err := pikpakgo.NewPikPakClient(cfg.PikPak.Username, cfg.PikPak.Password)
	if err != nil {
		return nil, fmt.Errorf("failed to create pikpak client: %w", err)
	}

	logger.Info("successfully created pikpak client", "username", cfg.PikPak.Username)

	return &PikPakDownloader{
		client:       client,
		config:       cfg,
		database:     database,
		sm:           sm,
		logger:       logger,
		pollingQueue: make(map[string]*PollingTask),
		stopChan:     make(chan struct{}),
	}, nil
}

// Submit submits a magnet link for download with idempotent strategy
func (d *PikPakDownloader) Submit(ctx context.Context, entry *db.Entry, magnetURL string) error {
	d.logger.Info("submitting download", "entry_id", entry.ID, "title", entry.Title)

	// Ensure login is valid
	if err := d.ensureLogin(); err != nil {
		return fmt.Errorf("failed to ensure login: %w", err)
	}

	// Step 1: Check if entry.pikpak_task_id already exists (idempotent check)
	var taskID string
	if entry.PikPakTaskID.Valid && entry.PikPakTaskID.String != "" {
		existingTaskID := entry.PikPakTaskID.String
		d.logger.Info("found existing task_id, checking if still active", "entry_id", entry.ID, "task_id", existingTaskID)

		// Query PikPak to verify task is still active
		var taskInfo *pikpakgo.Task
		err := d.client.OfflineListIterator(func(t *pikpakgo.Task) bool {
			if t.ID == existingTaskID {
				taskInfo = t
				return false // Stop iteration
			}
			return true // Continue iteration
		})
		if err != nil {
			d.logger.Error("failed to query existing task", "task_id", existingTaskID, "error", err)
			// Continue to create new task
		} else if taskInfo != nil {
			// Task still exists and is active
			d.logger.Info("existing task is still active, resuming polling", "entry_id", entry.ID, "task_id", existingTaskID)
			
			// Use download_started_at as timeout baseline (not updated_at)
			startTime := entry.DownloadStartedAt.Time
			if !entry.DownloadStartedAt.Valid {
				// Fallback to updated_at for backward compatibility
				d.logger.Warn("entry missing download_started_at, using updated_at as fallback", "entry_id", entry.ID)
				startTime = entry.UpdatedAt
			}
			
			// Add to polling queue without creating new task
			d.addToQueue(entry.ID, existingTaskID, startTime)
			return nil
		}
		// If task not found, continue to create new task
		d.logger.Info("existing task not found in PikPak, creating new task", "entry_id", entry.ID)
	}

	// Step 2: Create remote task
	newTask, err := d.client.OfflineDownload(entry.Title, magnetURL, "")
	if err != nil {
		// Submit failure -> TransitionToFailed with service_unreachable
		if transErr := d.sm.TransitionToFailed(ctx, entry.ID, state.FailureServiceUnreachable, "downloading", fmt.Sprintf("failed to submit offline download: %v", err)); transErr != nil {
			d.logger.Error("failed to transition to failed after submit error", "entry_id", entry.ID, "error", transErr)
		}
		return fmt.Errorf("failed to submit offline download: %w", err)
	}

	if newTask.Task == nil {
		if transErr := d.sm.TransitionToFailed(ctx, entry.ID, state.FailureServiceUnreachable, "downloading", "no task returned from offline download"); transErr != nil {
			d.logger.Error("failed to transition to failed after no task", "entry_id", entry.ID, "error", transErr)
		}
		return fmt.Errorf("no task returned from offline download")
	}

	taskID = newTask.Task.ID
	d.logger.Info("download submitted", "entry_id", entry.ID, "task_id", taskID)

	// Step 3: Transition to downloading state with task ID
	// StateMachine will automatically set download_started_at
	if err := d.sm.TransitionWithUpdate(ctx, entry.ID, state.StatusDownloading, map[string]any{
		"pikpak_task_id": taskID,
		"reason":         "download submitted to pikpak",
	}); err != nil {
		// State transition failed, but remote task already created
		// This is a known edge case: the remote task exists but local state is inconsistent
		// Recovery mechanism will handle this:
		// - On next Submit() call, step 1 will detect the existing task_id is empty
		// - A new task will be created (PikPak may have duplicate tasks, but this is acceptable)
		// - Alternatively, RecoverOnStartup will skip this entry if download_started_at is missing
		d.logger.Error("failed to transition to downloading after task creation",
			"entry_id", entry.ID,
			"task_id", taskID,
			"error", err)
		return fmt.Errorf("failed to transition to downloading: %w", err)
	}

	// Step 4: Add to polling queue
	// Use download_started_at from the entry (just set by StateMachine)
	updatedEntry, err := d.database.GetEntry(ctx, entry.ID)
	if err != nil {
		d.logger.Error("failed to get updated entry after transition, cannot add to polling queue", "entry_id", entry.ID, "error", err)
		// Don't add to queue with incorrect timeout baseline - let recovery handle it
		return fmt.Errorf("failed to get updated entry: %w", err)
	}
	
	if !updatedEntry.DownloadStartedAt.Valid {
		d.logger.Error("download_started_at not set after transition, cannot add to polling queue", "entry_id", entry.ID)
		// Don't add to queue with incorrect timeout baseline - let recovery handle it
		return fmt.Errorf("download_started_at not set after transition")
	}
	
	d.addToQueue(entry.ID, taskID, updatedEntry.DownloadStartedAt.Time)

	return nil
}

// StartPolling starts the polling goroutine
func (d *PikPakDownloader) StartPolling(ctx context.Context) {
	d.wg.Add(1)
	go d.pollingLoop(ctx)
	d.logger.Info("started pikpak polling")
}

// Stop stops the polling goroutine
func (d *PikPakDownloader) Stop() {
	d.stopOnce.Do(func() {
		close(d.stopChan)
	})
	d.wg.Wait()
	d.logger.Info("stopped pikpak polling")
}

// ResumePolling resumes polling for entries in downloading state
// Uses download_started_at as timeout baseline (not updated_at)
func (d *PikPakDownloader) ResumePolling(ctx context.Context) error {
	entries, err := d.database.ListEntriesByStatus(ctx, string(state.StatusDownloading))
	if err != nil {
		return fmt.Errorf("failed to list downloading entries: %w", err)
	}

	for _, entry := range entries {
		if entry.PikPakTaskID.Valid && entry.PikPakTaskID.String != "" {
			// Use download_started_at as timeout baseline (not updated_at)
			if !entry.DownloadStartedAt.Valid {
				// Skip entries missing download_started_at - cannot determine accurate timeout
				d.logger.Error("entry missing download_started_at, skipping resume",
					"entry_id", entry.ID,
					"task_id", entry.PikPakTaskID.String)
				continue
			}
			
			d.addToQueue(entry.ID, entry.PikPakTaskID.String, entry.DownloadStartedAt.Time)
			d.logger.Info("resumed polling", "entry_id", entry.ID, "task_id", entry.PikPakTaskID.String, "submit_time", entry.DownloadStartedAt.Time)
		}
	}

	d.logger.Info("resumed polling for downloading entries", "count", len(entries))
	return nil
}

// pollingLoop polls PikPak for task status
func (d *PikPakDownloader) pollingLoop(ctx context.Context) {
	defer d.wg.Done()

	// Use configured poll interval
	pollInterval := 60 * time.Second // Default to 60 seconds
	if d.config.PikPak.PollInterval != "" {
		if duration, err := time.ParseDuration(d.config.PikPak.PollInterval); err == nil && duration > 0 {
			pollInterval = duration
		} else {
			d.logger.Warn("invalid pikpak poll_interval, using default 60s", "configured", d.config.PikPak.PollInterval, "error", err)
		}
	}

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-d.stopChan:
			return
		case <-ticker.C:
			d.pollTasks(ctx)
		}
	}
}

// pollTasks polls all tasks in the queue
func (d *PikPakDownloader) pollTasks(ctx context.Context) {
	d.queueMu.RLock()
	tasks := make([]*PollingTask, 0, len(d.pollingQueue))
	for _, task := range d.pollingQueue {
		tasks = append(tasks, task)
	}
	d.queueMu.RUnlock()

	for _, task := range tasks {
		if err := d.pollTask(ctx, task); err != nil {
			d.logger.Error("failed to poll task", "entry_id", task.EntryID, "task_id", task.TaskID, "error", err)
		}
	}
}

// pollTask polls a single task
func (d *PikPakDownloader) pollTask(ctx context.Context, task *PollingTask) error {
	// Check timeout (24 hours) using download_started_at as baseline
	if time.Since(task.SubmitTime) > 24*time.Hour {
		d.logger.Warn("task timeout", "entry_id", task.EntryID, "task_id", task.TaskID)
		d.removeFromQueue(task.EntryID)
		return d.sm.TransitionToFailed(ctx, task.EntryID, state.FailurePikPakTimeout, "downloading", "download timeout (>24h)")
	}

	// Ensure login is valid
	if err := d.ensureLogin(); err != nil {
		d.logger.Error("failed to ensure login", "error", err)
		return err
	}

	// Query task status by iterating through offline list
	var taskInfo *pikpakgo.Task
	err := d.client.OfflineListIterator(func(t *pikpakgo.Task) bool {
		if t.ID == task.TaskID {
			taskInfo = t
			return false // Stop iteration
		}
		return true // Continue iteration
	})
	if err != nil {
		d.logger.Error("failed to get task info", "task_id", task.TaskID, "error", err)
		return err
	}

	if taskInfo == nil {
		d.logger.Warn("task not found in offline list", "task_id", task.TaskID)
		// Task might have been removed or completed and cleaned up
		// Keep polling for now, will timeout eventually
		return nil
	}

	d.logger.Debug("polled task", "entry_id", task.EntryID, "task_id", task.TaskID, "phase", taskInfo.Phase, "progress", taskInfo.Progress)

	// Phase values: PHASE_TYPE_PENDING, PHASE_TYPE_RUNNING, PHASE_TYPE_COMPLETE, PHASE_TYPE_ERROR
	switch taskInfo.Phase {
	case "PHASE_TYPE_COMPLETE":
		// Task completed successfully
		d.removeFromQueue(task.EntryID)
		return d.handleTaskCompleted(ctx, task, taskInfo)

	case "PHASE_TYPE_ERROR":
		// Task failed -> TransitionToFailed with service_unreachable
		d.removeFromQueue(task.EntryID)
		return d.sm.TransitionToFailed(ctx, task.EntryID, state.FailureServiceUnreachable, "downloading", fmt.Sprintf("pikpak task failed: %s", taskInfo.Message))

	case "PHASE_TYPE_PENDING", "PHASE_TYPE_RUNNING":
		// Still in progress, continue polling
		return nil

	default:
		d.logger.Warn("unknown task phase", "phase", taskInfo.Phase, "task_id", task.TaskID)
		return nil
	}
}

// handleTaskCompleted handles a completed download task
func (d *PikPakDownloader) handleTaskCompleted(ctx context.Context, task *PollingTask, taskInfo *pikpakgo.Task) error {
	d.logger.Info("download completed", "entry_id", task.EntryID, "task_id", task.TaskID)

	// Get file info
	fileID := taskInfo.FileID
	fileName := taskInfo.FileName

	if fileID == "" {
		return fmt.Errorf("completed task has no file_id")
	}

	// NOTE: taskInfo.FileName may only contain the filename, not the full path
	// According to design spec, pikpak_file_path should store the full internal path
	// (e.g., "/downloads/進撃の巨人/episode01.mkv") without the "pikpak:" prefix
	// The transfer service will prepend "pikpak:" when calling rclone
	// TODO: Verify if pikpak-go SDK provides a full path field, or if we need to
	// query the file details separately to get the complete path
	// For now, using FileName as a placeholder - this may need adjustment

	// Transition to downloaded state
	return d.sm.TransitionWithUpdate(ctx, task.EntryID, state.StatusDownloaded, map[string]any{
		"pikpak_file_id":   fileID,
		"pikpak_file_path": fileName, // May need to be full path instead of just filename
		"reason":           "download completed",
	})
}

// ensureLogin ensures the client is logged in
func (d *PikPakDownloader) ensureLogin() error {
	// Try to get user info to check if logged in
	_, err := d.client.Me()
	if err != nil {
		// Login expired, re-login
		d.logger.Info("pikpak login expired, re-logging in")
		if err := d.client.Login(); err != nil {
			return fmt.Errorf("failed to re-login: %w", err)
		}
		d.logger.Info("successfully re-logged in to pikpak")
	}
	return nil
}

// addToQueue adds a task to the polling queue
func (d *PikPakDownloader) addToQueue(entryID, taskID string, submitTime time.Time) {
	d.queueMu.Lock()
	defer d.queueMu.Unlock()

	d.pollingQueue[entryID] = &PollingTask{
		EntryID:    entryID,
		TaskID:     taskID,
		SubmitTime: submitTime,
	}
}

// removeFromQueue removes a task from the polling queue
func (d *PikPakDownloader) removeFromQueue(entryID string) {
	d.queueMu.Lock()
	defer d.queueMu.Unlock()

	delete(d.pollingQueue, entryID)
}
