package transfer

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/michealmachine/taro/internal/config"
	"github.com/michealmachine/taro/internal/db"
	"github.com/michealmachine/taro/internal/state"
)

// Coordinator manages file transfers from PikPak to OneDrive
type Coordinator struct {
	cfg          *config.Config
	database     *db.DB
	stateMachine *state.StateMachine
	logger       *slog.Logger
	httpClient   *http.Client

	// Polling management
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	stopOnce sync.Once

	// Active transfers being polled
	// map[entryID]*TransferInfo
	activeTransfers sync.Map
}

// TransferInfo stores information about an active transfer
type TransferInfo struct {
	TaskID            string
	TransferStartedAt time.Time
}

// NewCoordinator creates a new transfer coordinator
func NewCoordinator(
	cfg *config.Config,
	database *db.DB,
	stateMachine *state.StateMachine,
	logger *slog.Logger,
) *Coordinator {
	ctx, cancel := context.WithCancel(context.Background())

	return &Coordinator{
		cfg:          cfg,
		database:     database,
		stateMachine: stateMachine,
		logger:       logger,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		ctx:    ctx,
		cancel: cancel,
	}
}

// Submit submits a transfer task (idempotent)
func (c *Coordinator) Submit(ctx context.Context, entryID string) error {
	entry, err := c.database.GetEntry(ctx, entryID)
	if err != nil {
		return fmt.Errorf("failed to get entry: %w", err)
	}

	// Validate entry is in downloaded state
	if entry.Status != string(state.StatusDownloaded) {
		return fmt.Errorf("entry must be in downloaded state, current: %s", entry.Status)
	}

	// Check if transfer_task_id already exists (idempotent check)
	if entry.TransferTaskID.Valid && entry.TransferTaskID.String != "" {
		// Task ID exists, check if task is still active
		status, err := c.getTransferStatus(ctx, entry.TransferTaskID.String)
		if err != nil {
			c.logger.Warn("failed to check existing transfer status",
				"entry_id", entryID,
				"task_id", entry.TransferTaskID.String,
				"error", err)
			// Service unreachable, keep entry in downloaded state for retry
			return fmt.Errorf("transfer service unreachable: %w", err)
		}

		switch status {
		case "pending", "running":
			// Task is active, add to polling queue
			// Validate transfer_started_at exists
			if !entry.TransferStartedAt.Valid {
				c.logger.Error("entry has transfer_task_id but missing transfer_started_at",
					"entry_id", entryID,
					"task_id", entry.TransferTaskID.String)
				// Cannot resume without valid start time, fall through to create new task
			} else {
				c.logger.Info("existing transfer task is active, resuming polling",
					"entry_id", entryID,
					"task_id", entry.TransferTaskID.String,
					"status", status)
				c.addToPolling(entryID, entry.TransferTaskID.String, entry.TransferStartedAt.Time)
				return nil
			}
		case "done":
			// Task already completed, transition to transferred
			c.logger.Info("existing transfer task already completed",
				"entry_id", entryID,
				"task_id", entry.TransferTaskID.String)
			return c.stateMachine.Transition(ctx, entryID, state.StatusTransferred, "transfer completed")
		case "failed":
			// Task failed, will create new task below
			c.logger.Info("existing transfer task failed, creating new task",
				"entry_id", entryID,
				"task_id", entry.TransferTaskID.String)
		case "not_found":
			// Task not found (HF Space restarted), create new task
			c.logger.Info("existing transfer task not found, creating new task",
				"entry_id", entryID,
				"task_id", entry.TransferTaskID.String)
		}
	}

	// Generate target path
	targetPath := c.generateTargetPath(entry)

	// Create transfer task
	taskID, err := c.createTransferTask(ctx, entry.PikPakFilePath.String, targetPath)
	if err != nil {
		c.logger.Error("failed to create transfer task",
			"entry_id", entryID,
			"error", err)
		// Service unreachable, keep entry in downloaded state for retry
		return fmt.Errorf("failed to create transfer task: %w", err)
	}

	// Transition to transferring and record task_id
	// StateMachine will automatically set transfer_started_at
	updates := map[string]any{
		"transfer_task_id": taskID,
		"target_path":      targetPath,
		"reason":           "transfer task created",
	}
	if err := c.stateMachine.TransitionWithUpdate(ctx, entryID, state.StatusTransferring, updates); err != nil {
		return fmt.Errorf("failed to transition to transferring: %w", err)
	}

	c.logger.Info("transfer task created",
		"entry_id", entryID,
		"task_id", taskID,
		"target_path", targetPath)

	// Add to polling queue
	// Use the transfer_started_at that was just set by StateMachine
	entry, _ = c.database.GetEntry(ctx, entryID)
	c.addToPolling(entryID, taskID, entry.TransferStartedAt.Time)

	return nil
}

// generateTargetPath generates the target path for a media entry
// Path normalization: unified `/` separator, trailing `/`, special chars replaced with `_`
func (c *Coordinator) generateTargetPath(entry *db.Entry) string {
	mediaRoot := c.cfg.OneDrive.MediaRoot
	if mediaRoot == "" {
		mediaRoot = "/media" // lowercase, consistent with design
	}

	// Sanitize title BEFORE joining (replace special characters including /)
	sanitizedTitle := sanitizeTitle(entry.Title)

	var targetPath string
	switch entry.MediaType {
	case "anime":
		// Anime: /media/anime/{Title}/Season 01/
		targetPath = path.Join(mediaRoot, "anime", sanitizedTitle, fmt.Sprintf("Season %02d", entry.Season))
	case "tv":
		// TV: /media/tv/{Title}/Season 01/
		targetPath = path.Join(mediaRoot, "tv", sanitizedTitle, fmt.Sprintf("Season %02d", entry.Season))
	case "movie":
		// Movie: /media/movies/{Title} ({year})/
		yearStr := ""
		if entry.Year.Valid {
			yearStr = fmt.Sprintf(" (%d)", entry.Year.Int64)
		}
		targetPath = path.Join(mediaRoot, "movies", sanitizedTitle+yearStr)
	default:
		// Fallback
		targetPath = path.Join(mediaRoot, "other", sanitizedTitle)
	}

	// Normalize path
	return normalizePath(targetPath)
}

// normalizePath normalizes a path for comparison and storage:
// - Unified `/` separator (replace `\` with `/`)
// - Convert to lowercase for case-insensitive comparison
// - Special characters replaced with `_` (for filesystem compatibility)
// - Remove consecutive `//`
// - Trailing `/`
// sanitizeTitle replaces special characters in title to prevent path issues
// Must be called BEFORE path.Join to avoid / being treated as separator
func sanitizeTitle(title string) string {
	// Replace all filesystem-unsafe characters with underscore
	// Including / and \ which would be treated as path separators
	specialChars := []string{"/", "\\", ":", "*", "?", "\"", "<", ">", "|"}
	result := title
	for _, char := range specialChars {
		result = strings.ReplaceAll(result, char, "_")
	}
	return result
}

func normalizePath(inputPath string) string {
	// Replace backslashes with forward slashes
	inputPath = strings.ReplaceAll(inputPath, "\\", "/")

	// Convert to lowercase for case-insensitive comparison
	inputPath = strings.ToLower(inputPath)

	// Replace special characters with underscore (filesystem compatibility)
	// This is needed for both generated paths and webhook paths
	specialChars := []string{":", "*", "?", "\"", "<", ">", "|"}
	for _, char := range specialChars {
		inputPath = strings.ReplaceAll(inputPath, char, "_")
	}

	// Remove consecutive slashes
	for strings.Contains(inputPath, "//") {
		inputPath = strings.ReplaceAll(inputPath, "//", "/")
	}

	// Ensure trailing slash
	if !strings.HasSuffix(inputPath, "/") {
		inputPath += "/"
	}

	return inputPath
}

// createTransferTask creates a transfer task via taro-transfer API
func (c *Coordinator) createTransferTask(ctx context.Context, sourcePath, targetPath string) (string, error) {
	url := fmt.Sprintf("%s/transfer", strings.TrimSuffix(c.cfg.Transfer.URL, "/"))

	reqBody := map[string]string{
		"source_path": sourcePath,
		"target_path": targetPath,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(bodyBytes)))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.cfg.Transfer.Token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("unexpected status code: %d, body: %s", resp.StatusCode, string(body))
	}

	var respBody struct {
		TaskID string `json:"task_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&respBody); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	return respBody.TaskID, nil
}

// getTransferStatus gets the status of a transfer task
func (c *Coordinator) getTransferStatus(ctx context.Context, taskID string) (string, error) {
	url := fmt.Sprintf("%s/transfer/%s/status", strings.TrimSuffix(c.cfg.Transfer.URL, "/"), taskID)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	// Add Authorization header
	req.Header.Set("Authorization", "Bearer "+c.cfg.Transfer.Token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("unexpected status code: %d, body: %s", resp.StatusCode, string(body))
	}

	var respBody struct {
		Status string `json:"status"`
		Error  string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&respBody); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	return respBody.Status, nil
}

// StartPolling starts the polling goroutine
func (c *Coordinator) StartPolling() {
	c.wg.Add(1)
	go c.pollLoop()
}

// pollLoop polls transfer tasks periodically
func (c *Coordinator) pollLoop() {
	defer c.wg.Done()

	// Parse poll interval from config
	pollInterval := 60 * time.Second // default
	if c.cfg.Transfer.PollInterval != "" {
		if duration, err := time.ParseDuration(c.cfg.Transfer.PollInterval); err != nil {
			c.logger.Warn("failed to parse transfer.poll_interval, using default 60s",
				"value", c.cfg.Transfer.PollInterval,
				"error", err)
		} else {
			pollInterval = duration
		}
	}

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	c.logger.Info("transfer polling started", "interval", pollInterval)

	for {
		select {
		case <-c.ctx.Done():
			c.logger.Info("transfer polling stopped")
			return
		case <-ticker.C:
			c.pollActiveTransfers()
		}
	}
}

// pollActiveTransfers polls all active transfers
func (c *Coordinator) pollActiveTransfers() {
	c.activeTransfers.Range(func(key, value any) bool {
		entryID := key.(string)

		// Poll this transfer
		if err := c.pollTransfer(entryID); err != nil {
			c.logger.Error("failed to poll transfer",
				"entry_id", entryID,
				"error", err)
		}

		return true
	})
}

// pollTransfer polls a single transfer task
func (c *Coordinator) pollTransfer(entryID string) error {
	ctx := context.Background()

	entry, err := c.database.GetEntry(ctx, entryID)
	if err != nil {
		return fmt.Errorf("failed to get entry: %w", err)
	}

	// Skip if not in transferring state
	if entry.Status != string(state.StatusTransferring) {
		c.removeFromPolling(entryID)
		return nil
	}

	// Check if task_id exists
	if !entry.TransferTaskID.Valid || entry.TransferTaskID.String == "" {
		c.logger.Error("entry in transferring state but missing transfer_task_id",
			"entry_id", entryID)
		c.removeFromPolling(entryID)
		return nil
	}

	taskID := entry.TransferTaskID.String

	// Check timeout (use transfer_started_at as baseline)
	if entry.TransferStartedAt.Valid {
		elapsed := time.Since(entry.TransferStartedAt.Time)
		if elapsed > 24*time.Hour {
			c.logger.Warn("transfer timeout",
				"entry_id", entryID,
				"task_id", taskID,
				"elapsed", elapsed)
			c.removeFromPolling(entryID)
			return c.stateMachine.TransitionToFailed(ctx, entryID,
				state.FailureTransferTimeout,
				"transferring",
				fmt.Sprintf("transfer timeout after %v", elapsed))
		}
	}

	// Get transfer status
	status, err := c.getTransferStatus(ctx, taskID)
	if err != nil {
		c.logger.Warn("failed to get transfer status",
			"entry_id", entryID,
			"task_id", taskID,
			"error", err)
		// Service unreachable, keep polling
		return nil
	}

	switch status {
	case "done":
		// Transfer completed
		c.logger.Info("transfer completed",
			"entry_id", entryID,
			"task_id", taskID)
		c.removeFromPolling(entryID)
		return c.stateMachine.Transition(ctx, entryID, state.StatusTransferred, "transfer completed")

	case "failed":
		// Transfer failed
		c.logger.Error("transfer failed",
			"entry_id", entryID,
			"task_id", taskID)
		c.removeFromPolling(entryID)
		return c.stateMachine.TransitionToFailed(ctx, entryID,
			state.FailureServiceUnreachable,
			"transferring",
			"transfer task failed")

	case "not_found":
		// Task not found (HF Space restarted), resubmit task
		c.logger.Warn("transfer task not found, resubmitting",
			"entry_id", entryID,
			"task_id", taskID)
		c.removeFromPolling(entryID)

		// Generate target path
		targetPath := c.generateTargetPath(entry)

		// Create new transfer task
		newTaskID, err := c.createTransferTask(ctx, entry.PikPakFilePath.String, targetPath)
		if err != nil {
			c.logger.Error("failed to resubmit transfer task",
				"entry_id", entryID,
				"error", err)
			// Service unreachable, will retry on next poll
			return nil
		}

		// Update transfer_task_id and reset transfer_started_at atomically
		// Use UpdateFields to avoid illegal transferring -> transferring self-loop transition
		now := time.Now()
		updates := map[string]any{
			"transfer_task_id":    newTaskID,
			"transfer_started_at": now, // Reset timeout baseline
		}
		if err := c.stateMachine.UpdateFields(ctx, entryID, updates); err != nil {
			c.logger.Error("failed to update entry after resubmit",
				"entry_id", entryID,
				"error", err)
			return nil
		}

		c.logger.Info("transfer task resubmitted",
			"entry_id", entryID,
			"old_task_id", taskID,
			"new_task_id", newTaskID)

		// Add back to polling with fresh transfer_started_at
		c.addToPolling(entryID, newTaskID, now)

	case "pending", "running":
		// Still in progress, continue polling
		c.logger.Debug("transfer in progress",
			"entry_id", entryID,
			"task_id", taskID,
			"status", status)
	}

	return nil
}

// ResumePolling resumes polling for a transfer task (used during startup recovery)
func (c *Coordinator) ResumePolling(entryID, taskID string, transferStartedAt time.Time) error {
	c.logger.Info("resuming transfer polling",
		"entry_id", entryID,
		"task_id", taskID,
		"transfer_started_at", transferStartedAt)

	c.addToPolling(entryID, taskID, transferStartedAt)
	return nil
}

// addToPolling adds an entry to the polling queue
func (c *Coordinator) addToPolling(entryID, taskID string, transferStartedAt time.Time) {
	c.activeTransfers.Store(entryID, &TransferInfo{
		TaskID:            taskID,
		TransferStartedAt: transferStartedAt,
	})
	c.logger.Debug("added to transfer polling",
		"entry_id", entryID,
		"task_id", taskID,
		"transfer_started_at", transferStartedAt)
}

// removeFromPolling removes an entry from the polling queue
func (c *Coordinator) removeFromPolling(entryID string) {
	c.activeTransfers.Delete(entryID)
	c.logger.Debug("removed from transfer polling", "entry_id", entryID)
}

// Stop stops the coordinator
func (c *Coordinator) Stop() {
	c.stopOnce.Do(func() {
		c.logger.Info("stopping transfer coordinator")
		c.cancel()
		c.wg.Wait()
		c.logger.Info("transfer coordinator stopped")
	})
}
