// Package downloader manages PikPak offline downloads via the pikpaktui CLI.
// pikpaktui (https://github.com/Bengerthelorf/pikpaktui) is an actively maintained
// Rust CLI for PikPak that supports offline download management.
// We call it as an external process, similar to how we call rclone.
package downloader

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/michealmachine/taro/internal/config"
	"github.com/michealmachine/taro/internal/db"
	"github.com/michealmachine/taro/internal/state"
)

// PikPakDownloader manages downloads via PikPak using the pikpaktui CLI
type PikPakDownloader struct {
	config   *config.Config
	database *db.DB
	sm       *state.StateMachine
	logger   *slog.Logger

	// Polling queue management
	pollingQueue map[string]*PollingTask // entryID -> task
	queueMu      sync.RWMutex
	stopChan     chan struct{}
	stopOnce     sync.Once
	wg           sync.WaitGroup
}

// PollingTask represents a task being polled
type PollingTask struct {
	EntryID    string
	TaskID     string
	SubmitTime time.Time
}

// pikpakTaskStatus represents a task from `pikpaktui tasks list --json`
type pikpakTaskStatus struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Phase     string `json:"phase"` // "PHASE_TYPE_PENDING" | "PHASE_TYPE_RUNNING" | "PHASE_TYPE_COMPLETE" | "PHASE_TYPE_ERROR"
	Progress  int    `json:"progress"`
	FileID    string `json:"file_id"`
	FileSize  string `json:"file_size"`
	Message   string `json:"message"`
	CreatedAt string `json:"created_time"`
}

// NewPikPakDownloader creates a new PikPak downloader
func NewPikPakDownloader(cfg *config.Config, database *db.DB, sm *state.StateMachine, logger *slog.Logger) (*PikPakDownloader, error) {
	// Verify pikpaktui is available
	if _, err := exec.LookPath("pikpaktui"); err != nil {
		return nil, fmt.Errorf("pikpaktui CLI not found in PATH: %w (install from https://github.com/Bengerthelorf/pikpaktui)", err)
	}

	logger.Info("pikpaktui CLI found, initializing downloader")

	return &PikPakDownloader{
		config:       cfg,
		database:     database,
		sm:           sm,
		logger:       logger,
		pollingQueue: make(map[string]*PollingTask),
		stopChan:     make(chan struct{}),
	}, nil
}

// runCLI runs a pikpaktui command and returns stdout output
func (d *PikPakDownloader) runCLI(ctx context.Context, args ...string) ([]byte, error) {
	// Always pass credentials via env vars (pikpaktui supports PIKPAK_USER / PIKPAK_PASS)
	cmd := exec.CommandContext(ctx, "pikpaktui", args...)
	cmd.Env = append(cmd.Environ(),
		"PIKPAK_USER="+d.config.PikPak.Username,
		"PIKPAK_PASS="+d.config.PikPak.Password,
	)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("pikpaktui %s failed (exit %d): %s", strings.Join(args, " "), exitErr.ExitCode(), string(exitErr.Stderr))
		}
		return nil, fmt.Errorf("pikpaktui %s failed: %w", strings.Join(args, " "), err)
	}
	return out, nil
}

// Submit submits a magnet link for offline download (idempotent)
func (d *PikPakDownloader) Submit(ctx context.Context, entry *db.Entry, magnetURL string) error {
	d.logger.Info("submitting download", "entry_id", entry.ID, "title", entry.Title)

	// Step 1: Check if entry.pikpak_task_id already exists (idempotent check)
	if entry.PikPakTaskID.Valid && entry.PikPakTaskID.String != "" {
		existingTaskID := entry.PikPakTaskID.String
		d.logger.Info("found existing task_id, checking if still active", "entry_id", entry.ID, "task_id", existingTaskID)

		taskInfo, err := d.getTaskStatus(ctx, existingTaskID)
		if err != nil {
			d.logger.Error("failed to query existing task", "task_id", existingTaskID, "error", err)
			// Continue to create new task
		} else if taskInfo != nil {
			d.logger.Info("existing task is still active, resuming polling", "entry_id", entry.ID, "task_id", existingTaskID, "phase", taskInfo.Phase)

			startTime := entry.DownloadStartedAt.Time
			if !entry.DownloadStartedAt.Valid {
				d.logger.Warn("entry missing download_started_at, using now as fallback", "entry_id", entry.ID)
				startTime = time.Now()
			}
			d.addToQueue(entry.ID, existingTaskID, startTime)
			return nil
		}
		d.logger.Info("existing task not found, creating new task", "entry_id", entry.ID)
	}

	// Step 2: Submit offline download via pikpaktui CLI
	// pikpaktui offline <url> [--parent <parent_dir>]
	// Output format:
	// Offline task created: <name>
	//   ID:    <task_id>
	//   Phase: <phase>
	//   File:
	args := []string{"offline", magnetURL}

	// Add --parent flag if download_dir is configured
	if d.config.PikPak.DownloadDir != "" {
		args = append(args, "--parent", d.config.PikPak.DownloadDir)
		d.logger.Debug("submitting download to specific directory",
			"entry_id", entry.ID,
			"download_dir", d.config.PikPak.DownloadDir)
	}

	out, err := d.runCLI(ctx, args...)
	if err != nil {
		if transErr := d.sm.TransitionToFailed(ctx, entry.ID, state.FailureServiceUnreachable, "downloading",
			fmt.Sprintf("failed to submit offline download: %v", err)); transErr != nil {
			d.logger.Error("failed to transition to failed", "entry_id", entry.ID, "error", transErr)
		}
		return fmt.Errorf("failed to submit offline download: %w", err)
	}

	// Parse task ID from text output
	output := string(out)
	taskID := ""

	// Look for "ID:    <task_id>" pattern (note: multiple spaces after ID:)
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "ID:") {
			// Extract ID value after "ID:" and trim spaces
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				taskID = strings.TrimSpace(parts[1])
				break
			}
		}
	}

	if taskID == "" {
		d.logger.Error("failed to parse task ID from pikpaktui output", "output", output)
		return fmt.Errorf("failed to parse task ID from pikpaktui output: %s", output)
	}

	d.logger.Info("download submitted", "entry_id", entry.ID, "task_id", taskID)

	// Step 3: Transition to downloading (StateMachine sets download_started_at)
	if err := d.sm.TransitionWithUpdate(ctx, entry.ID, state.StatusDownloading, map[string]any{
		"pikpak_task_id": taskID,
		"reason":         "download submitted to pikpak",
	}); err != nil {
		d.logger.Error("failed to transition to downloading", "entry_id", entry.ID, "task_id", taskID, "error", err)
		return fmt.Errorf("failed to transition to downloading: %w", err)
	}

	// Step 4: Add to polling queue using the just-set download_started_at
	updatedEntry, err := d.database.GetEntry(ctx, entry.ID)
	if err != nil {
		d.logger.Error("failed to get updated entry", "entry_id", entry.ID, "error", err)
		return fmt.Errorf("failed to get updated entry: %w", err)
	}
	if !updatedEntry.DownloadStartedAt.Valid {
		return fmt.Errorf("download_started_at not set after transition")
	}
	d.addToQueue(entry.ID, taskID, updatedEntry.DownloadStartedAt.Time)

	return nil
}

// getTaskStatus queries the status of a single offline task
// Returns nil if task not found
func (d *PikPakDownloader) getTaskStatus(ctx context.Context, taskID string) (*pikpakTaskStatus, error) {
	// pikpaktui tasks list --json
	// Returns array of all tasks, we need to find the one matching taskID
	out, err := d.runCLI(ctx, "tasks", "list", "--json")
	if err != nil {
		return nil, fmt.Errorf("failed to list tasks: %w", err)
	}

	var tasks []pikpakTaskStatus
	if err := json.Unmarshal(out, &tasks); err != nil {
		return nil, fmt.Errorf("failed to parse tasks list: %w", err)
	}

	// Find task by ID
	for _, task := range tasks {
		if task.ID == taskID {
			return &task, nil
		}
	}

	// Task not found
	return nil, nil
}

// deleteFile deletes a file from PikPak by file ID
func (d *PikPakDownloader) deleteFile(ctx context.Context, fileID string) error {
	// pikpaktui rm <file_id>
	_, err := d.runCLI(ctx, "rm", fileID)
	return err
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
func (d *PikPakDownloader) ResumePolling(ctx context.Context) error {
	entries, err := d.database.ListEntriesByStatus(ctx, string(state.StatusDownloading))
	if err != nil {
		return fmt.Errorf("failed to list downloading entries: %w", err)
	}

	for _, entry := range entries {
		if entry.PikPakTaskID.Valid && entry.PikPakTaskID.String != "" {
			if !entry.DownloadStartedAt.Valid {
				d.logger.Error("entry missing download_started_at, skipping resume",
					"entry_id", entry.ID, "task_id", entry.PikPakTaskID.String)
				continue
			}
			d.addToQueue(entry.ID, entry.PikPakTaskID.String, entry.DownloadStartedAt.Time)
			d.logger.Info("resumed polling", "entry_id", entry.ID, "task_id", entry.PikPakTaskID.String)
		}
	}

	d.logger.Info("resumed polling for downloading entries", "count", len(entries))
	return nil
}

// ResumeEntryPolling resumes polling for a single downloading entry.
// Used by startup recovery callback to avoid repeated full-table scans.
func (d *PikPakDownloader) ResumeEntryPolling(entryID, taskID string, downloadStartedAt time.Time) error {
	if entryID == "" || taskID == "" {
		return fmt.Errorf("entryID and taskID are required")
	}
	d.addToQueue(entryID, taskID, downloadStartedAt)
	d.logger.Info("resumed polling for single entry", "entry_id", entryID, "task_id", taskID)
	return nil
}

// pollingLoop polls PikPak for task status
func (d *PikPakDownloader) pollingLoop(ctx context.Context) {
	defer d.wg.Done()

	pollInterval := 60 * time.Second
	if d.config.PikPak.PollInterval != "" {
		if duration, err := time.ParseDuration(d.config.PikPak.PollInterval); err == nil && duration > 0 {
			pollInterval = duration
		} else {
			d.logger.Warn("invalid pikpak poll_interval, using default 60s", "configured", d.config.PikPak.PollInterval)
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
	// Check timeout (24 hours)
	if time.Since(task.SubmitTime) > 24*time.Hour {
		d.logger.Warn("task timeout", "entry_id", task.EntryID, "task_id", task.TaskID)
		d.removeFromQueue(task.EntryID)
		return d.sm.TransitionToFailed(ctx, task.EntryID, state.FailurePikPakTimeout, "downloading", "download timeout (>24h)")
	}

	taskInfo, err := d.getTaskStatus(ctx, task.TaskID)
	if err != nil {
		d.logger.Error("failed to get task info", "task_id", task.TaskID, "error", err)
		return err
	}

	if taskInfo == nil {
		d.logger.Warn("task not found in offline list", "task_id", task.TaskID)
		return nil
	}

	d.logger.Debug("polled task", "entry_id", task.EntryID, "task_id", task.TaskID, "phase", taskInfo.Phase, "progress", taskInfo.Progress)

	switch taskInfo.Phase {
	case "PHASE_TYPE_COMPLETE":
		d.removeFromQueue(task.EntryID)
		return d.handleTaskCompleted(ctx, task, taskInfo)

	case "PHASE_TYPE_ERROR":
		d.removeFromQueue(task.EntryID)
		return d.sm.TransitionToFailed(ctx, task.EntryID, state.FailureServiceUnreachable, "downloading",
			fmt.Sprintf("pikpak task failed: %s", taskInfo.Message))

	case "PHASE_TYPE_PENDING", "PHASE_TYPE_RUNNING":
		return nil

	default:
		d.logger.Warn("unknown task phase", "phase", taskInfo.Phase, "task_id", task.TaskID)
		return nil
	}
}

// handleTaskCompleted handles a completed download task
func (d *PikPakDownloader) handleTaskCompleted(ctx context.Context, task *PollingTask, taskInfo *pikpakTaskStatus) error {
	d.logger.Info("download completed", "entry_id", task.EntryID, "task_id", task.TaskID)

	fileID := taskInfo.FileID
	if fileID == "" {
		return fmt.Errorf("completed task has no file_id")
	}

	// Construct file path: <download_dir>/<filename>
	// The transfer service will prepend "pikpak:" to construct the rclone source path
	filePath := taskInfo.Name
	if d.config.PikPak.DownloadDir != "" {
		// Ensure download_dir has trailing slash for path.Join behavior
		downloadDir := d.config.PikPak.DownloadDir
		if !strings.HasSuffix(downloadDir, "/") {
			downloadDir += "/"
		}
		filePath = downloadDir + taskInfo.Name
	}

	d.logger.Info("file path constructed",
		"entry_id", task.EntryID,
		"file_path", filePath,
		"download_dir", d.config.PikPak.DownloadDir)

	return d.sm.TransitionWithUpdate(ctx, task.EntryID, state.StatusDownloaded, map[string]any{
		"pikpak_file_id":   fileID,
		"pikpak_file_path": filePath,
		"reason":           "download completed",
	})
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
