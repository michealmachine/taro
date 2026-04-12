package health

import (
	"context"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// OneDriveChecker checks OneDrive mount health using rclone
type OneDriveChecker struct {
	mountPath      string
	checkInterval  time.Duration
	logger         *slog.Logger
	mu             sync.RWMutex
	lastStatus     bool
	onStatusChange func(isHealthy bool) // Callback for status changes
	stopCh         chan struct{}
	stoppedCh      chan struct{}
	stopOnce       sync.Once
	started        bool
}

// NewOneDriveChecker creates a new OneDrive health checker
func NewOneDriveChecker(mountPath string, checkInterval time.Duration, logger *slog.Logger) *OneDriveChecker {
	if checkInterval == 0 {
		checkInterval = 10 * time.Minute // Default 10 minutes
	}

	return &OneDriveChecker{
		mountPath:     mountPath,
		checkInterval: checkInterval,
		logger:        logger,
		lastStatus:    true, // Assume healthy initially
		stopCh:        make(chan struct{}),
		stoppedCh:     make(chan struct{}),
	}
}

// SetOnStatusChangeCallback sets the callback for status changes
func (c *OneDriveChecker) SetOnStatusChangeCallback(callback func(isHealthy bool)) {
	c.onStatusChange = callback
}

// Start starts the health check loop
func (c *OneDriveChecker) Start(ctx context.Context) {
	c.mu.Lock()
	c.started = true
	c.mu.Unlock()
	
	c.logger.Info("starting OneDrive health checker",
		"mount_path", c.mountPath,
		"check_interval", c.checkInterval)

	// Perform initial check
	c.performCheck()

	ticker := time.NewTicker(c.checkInterval)
	defer ticker.Stop()
	defer close(c.stoppedCh)

	for {
		select {
		case <-ctx.Done():
			c.logger.Info("OneDrive health checker stopped by context")
			return
		case <-c.stopCh:
			c.logger.Info("OneDrive health checker stopped")
			return
		case <-ticker.C:
			c.performCheck()
		}
	}
}

// Stop stops the health check loop
func (c *OneDriveChecker) Stop() {
	c.stopOnce.Do(func() {
		// Check if Start() was ever called
		c.mu.RLock()
		wasStarted := c.started
		c.mu.RUnlock()
		
		if !wasStarted {
			c.logger.Debug("Stop() called but Start() was never called")
			return
		}
		
		close(c.stopCh)
		<-c.stoppedCh
	})
}

// performCheck performs a single health check
func (c *OneDriveChecker) performCheck() {
	isHealthy := c.CheckMount()

	c.mu.Lock()
	previousStatus := c.lastStatus
	c.lastStatus = isHealthy
	c.mu.Unlock()

	// Trigger callback if status changed
	if previousStatus != isHealthy {
		c.logger.Info("OneDrive mount status changed",
			"previous", previousStatus,
			"current", isHealthy,
			"mount_path", c.mountPath)

		if c.onStatusChange != nil {
			c.onStatusChange(isHealthy)
		}
	}
}

// CheckMount checks if the OneDrive mount is accessible using rclone
// Returns true if healthy, false otherwise
func (c *OneDriveChecker) CheckMount() bool {
	// Use rclone lsd to check if the mount is accessible
	// rclone lsd onedrive: will list directories in the root
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "rclone", "lsd", "onedrive:")
	output, err := cmd.CombinedOutput()

	if err != nil {
		c.logger.Error("OneDrive mount check failed",
			"mount_path", c.mountPath,
			"error", err,
			"output", string(output))
		return false
	}

	// Check if output contains valid directory listing
	// rclone lsd returns lines like: "          -1 2024-01-01 00:00:00        -1 Documents"
	outputStr := strings.TrimSpace(string(output))
	if outputStr == "" {
		// Empty output might mean no directories, but mount is still accessible
		c.logger.Debug("OneDrive mount accessible but empty", "mount_path", c.mountPath)
		return true
	}

	// If we got here, mount is accessible
	c.logger.Debug("OneDrive mount check passed",
		"mount_path", c.mountPath,
		"directories_found", strings.Count(outputStr, "\n")+1)
	return true
}

// GetStatus returns the current health status
func (c *OneDriveChecker) GetStatus() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lastStatus
}
