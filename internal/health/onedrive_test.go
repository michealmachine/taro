package health

import (
	"context"
	"log/slog"
	"testing"
	"time"
)

func TestOneDriveChecker_CheckMount(t *testing.T) {
	// Note: This test requires rclone to be installed and configured
	// In CI/CD, you might want to skip this test or mock the rclone command
	t.Skip("Skipping test that requires rclone installation and configuration")

	logger := slog.Default()
	checker := NewOneDriveChecker("onedrive:", 1*time.Minute, logger)

	// Test check mount
	isHealthy := checker.CheckMount()
	t.Logf("OneDrive mount health: %v", isHealthy)
}

func TestOneDriveChecker_StatusChange(t *testing.T) {
	logger := slog.Default()
	checker := NewOneDriveChecker("onedrive:", 100*time.Millisecond, logger)

	// Track status changes
	statusChanges := []bool{}
	checker.SetOnStatusChangeCallback(func(isHealthy bool) {
		statusChanges = append(statusChanges, isHealthy)
	})

	// Start checker
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	go checker.Start(ctx)

	// Wait for context to finish
	<-ctx.Done()
	checker.Stop()

	// Note: Without mocking rclone, we can't reliably test status changes
	// This test mainly verifies that the checker doesn't crash
	t.Logf("Status changes recorded: %d", len(statusChanges))
}

func TestOneDriveChecker_GetStatus(t *testing.T) {
	logger := slog.Default()
	checker := NewOneDriveChecker("onedrive:", 1*time.Minute, logger)

	// Initial status should be true (healthy)
	if !checker.GetStatus() {
		t.Errorf("Initial status should be true, got false")
	}

	// Manually set status to false
	checker.mu.Lock()
	checker.lastStatus = false
	checker.mu.Unlock()

	if checker.GetStatus() {
		t.Errorf("Status should be false after manual update, got true")
	}
}

func TestOneDriveChecker_DefaultInterval(t *testing.T) {
	logger := slog.Default()
	checker := NewOneDriveChecker("onedrive:", 0, logger)

	expectedInterval := 10 * time.Minute
	if checker.checkInterval != expectedInterval {
		t.Errorf("Default interval should be %v, got %v", expectedInterval, checker.checkInterval)
	}
}

func TestOneDriveChecker_Stop(t *testing.T) {
	logger := slog.Default()
	checker := NewOneDriveChecker("onedrive:", 100*time.Millisecond, logger)

	ctx := context.Background()
	go checker.Start(ctx)

	// Wait a bit then stop
	time.Sleep(50 * time.Millisecond)
	checker.Stop()

	// Verify stopped
	select {
	case <-checker.stoppedCh:
		// Successfully stopped
	case <-time.After(1 * time.Second):
		t.Error("Checker did not stop within timeout")
	}
}
