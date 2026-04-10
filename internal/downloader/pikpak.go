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

// Submit submits a magnet link for download
func (d *PikPakDownloader) Submit(ctx context.Context, entry *db.Entry, magnetURL string) error {
	d.logger.Info("submitting download", "entry_id", entry.ID, "title", entry.Title)

	// Ensure login is valid
	if err := d.ensureLogin(); err != nil {
		return fmt.Errorf("failed to ensure login: %w", err)
	}

	// Submit offline download task
	// OfflineDownload(name, fileUrl, parentId string) (*NewTask, error)
	newTask, err := d.client.OfflineDownload(entry.Title, magnetURL, "")
	if err != nil {
		return fmt.Errorf("failed to submit offline download: %w", err)
	}

	if newTask.Task == nil {
		return fmt.Errorf("no task returned from offline download")
	}

	taskID := newTask.Task.ID
	d.logger.Info("download submitted", "entry_id", entry.ID, "task_id", taskID)

	// Transition to downloading state with task ID
	if err := d.sm.TransitionWithUpdate(ctx, entry.ID, state.StatusDownloading, map[string]any{
		"pikpak_task_id": taskID,
		"reason":         "download submitted to pikpak",
	}); err != nil {
		return fmt.Errorf("failed to transition to downloading: %w", err)
	}

	// Add to polling queue
	d.addToQueue(entry.ID, taskID)

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
	close(d.stopChan)
	d.wg.Wait()
	d.logger.Info("stopped pikpak polling")
}

// ResumePolling resumes polling for entries in downloading state
func (d *PikPakDownloader) ResumePolling(ctx context.Context) error {
	entries, err := d.database.ListEntriesByStatus(ctx, string(state.StatusDownloading))
	if err != nil {
		return fmt.Errorf("failed to list downloading entries: %w", err)
	}

	for _, entry := range entries {
		if entry.PikPakTaskID.Valid && entry.PikPakTaskID.String != "" {
			d.addToQueue(entry.ID, entry.PikPakTaskID.String)
			d.logger.Info("resumed polling", "entry_id", entry.ID, "task_id", entry.PikPakTaskID.String)
		}
	}

	d.logger.Info("resumed polling for downloading entries", "count", len(entries))
	return nil
}

// pollingLoop polls PikPak for task status
func (d *PikPakDownloader) pollingLoop(ctx context.Context) {
	defer d.wg.Done()

	ticker := time.NewTicker(60 * time.Second) // Poll every 60 seconds
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
	// Check timeout (24 hours)
	if time.Since(task.SubmitTime) > 24*time.Hour {
		d.logger.Warn("task timeout", "entry_id", task.EntryID, "task_id", task.TaskID)
		d.removeFromQueue(task.EntryID)
		return d.sm.TransitionWithUpdate(ctx, task.EntryID, state.StatusFailed, map[string]any{
			"failed_stage":  "downloading",
			"failed_reason": "download timeout (>24h)",
			"reason":        "timeout",
		})
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
		// Task failed
		d.removeFromQueue(task.EntryID)
		return d.sm.TransitionWithUpdate(ctx, task.EntryID, state.StatusFailed, map[string]any{
			"failed_stage":  "downloading",
			"failed_reason": fmt.Sprintf("pikpak task failed: %s", taskInfo.Message),
			"reason":        "download failed",
		})

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

	// Transition to downloaded state
	return d.sm.TransitionWithUpdate(ctx, task.EntryID, state.StatusDownloaded, map[string]any{
		"pikpak_file_id":   fileID,
		"pikpak_file_path": fileName,
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
func (d *PikPakDownloader) addToQueue(entryID, taskID string) {
	d.queueMu.Lock()
	defer d.queueMu.Unlock()

	d.pollingQueue[entryID] = &PollingTask{
		EntryID:    entryID,
		TaskID:     taskID,
		SubmitTime: time.Now(),
	}
}

// removeFromQueue removes a task from the polling queue
func (d *PikPakDownloader) removeFromQueue(entryID string) {
	d.queueMu.Lock()
	defer d.queueMu.Unlock()

	delete(d.pollingQueue, entryID)
}
