package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/michealmachine/taro/internal/config"
	"github.com/michealmachine/taro/internal/db"
	"github.com/robfig/cron/v3"
)

// Scheduler manages periodic tasks
type Scheduler struct {
	cron   *cron.Cron
	logger *slog.Logger
	config *config.Config
	db     *db.DB

	// Task handlers
	onPendingTask         func(ctx context.Context) error
	onFoundTask           func(ctx context.Context) error
	onDownloadedTask      func(ctx context.Context) error
	onSelectionTimeout    func(ctx context.Context) error
	onBangumiPoll         func(ctx context.Context) error
	onTraktPoll           func(ctx context.Context) error
	onHealthCheck         func(ctx context.Context) error
	onGarbageCollection   func(ctx context.Context) error

	// Semaphore for concurrent search limit
	searchSemaphore chan struct{}

	// Task running flags (skip if previous round not complete)
	pendingRunning   bool
	foundRunning     bool
	downloadedRunning bool
	mu               sync.Mutex

	// Context for graceful shutdown
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// TaskHandlers contains all task handler functions
type TaskHandlers struct {
	OnPendingTask       func(ctx context.Context) error
	OnFoundTask         func(ctx context.Context) error
	OnDownloadedTask    func(ctx context.Context) error
	OnSelectionTimeout  func(ctx context.Context) error
	OnBangumiPoll       func(ctx context.Context) error
	OnTraktPoll         func(ctx context.Context) error
	OnHealthCheck       func(ctx context.Context) error
	OnGarbageCollection func(ctx context.Context) error
}

// NewScheduler creates a new scheduler
func NewScheduler(cfg *config.Config, database *db.DB, logger *slog.Logger, handlers *TaskHandlers) *Scheduler {
	ctx, cancel := context.WithCancel(context.Background())

	s := &Scheduler{
		cron:   cron.New(cron.WithSeconds()),
		logger: logger,
		config: cfg,
		db:     database,
		ctx:    ctx,
		cancel: cancel,
	}

	// Initialize search semaphore
	maxConcurrent := cfg.Defaults.MaxConcurrentSearches
	if maxConcurrent <= 0 {
		maxConcurrent = 3
	}
	s.searchSemaphore = make(chan struct{}, maxConcurrent)

	// Set task handlers
	if handlers != nil {
		s.onPendingTask = handlers.OnPendingTask
		s.onFoundTask = handlers.OnFoundTask
		s.onDownloadedTask = handlers.OnDownloadedTask
		s.onSelectionTimeout = handlers.OnSelectionTimeout
		s.onBangumiPoll = handlers.OnBangumiPoll
		s.onTraktPoll = handlers.OnTraktPoll
		s.onHealthCheck = handlers.OnHealthCheck
		s.onGarbageCollection = handlers.OnGarbageCollection
	}

	return s
}

// Start starts the scheduler
func (s *Scheduler) Start() error {
	s.logger.Info("starting scheduler")

	// Register periodic tasks
	if err := s.registerTasks(); err != nil {
		return fmt.Errorf("failed to register tasks: %w", err)
	}

	// Start cron
	s.cron.Start()

	s.logger.Info("scheduler started")
	return nil
}

// Stop stops the scheduler gracefully
func (s *Scheduler) Stop() {
	s.logger.Info("stopping scheduler")

	// Stop accepting new cron jobs
	ctx := s.cron.Stop()

	// Cancel all running tasks
	s.cancel()

	// Wait for cron to finish (with timeout)
	select {
	case <-ctx.Done():
		s.logger.Info("cron stopped gracefully")
	case <-time.After(30 * time.Second):
		s.logger.Warn("cron stop timeout, forcing shutdown")
	}

	// Wait for all goroutines to finish
	s.wg.Wait()

	s.logger.Info("scheduler stopped")
}

// registerTasks registers all periodic tasks
func (s *Scheduler) registerTasks() error {
	// Every minute: process pending entries (trigger search)
	if _, err := s.cron.AddFunc("0 * * * * *", s.wrapTask("pending", s.processPendingEntries)); err != nil {
		return fmt.Errorf("failed to register pending task: %w", err)
	}

	// Every minute: process found entries (trigger download)
	if _, err := s.cron.AddFunc("0 * * * * *", s.wrapTask("found", s.processFoundEntries)); err != nil {
		return fmt.Errorf("failed to register found task: %w", err)
	}

	// Every minute: process downloaded entries (trigger transfer)
	if _, err := s.cron.AddFunc("0 * * * * *", s.wrapTask("downloaded", s.processDownloadedEntries)); err != nil {
		return fmt.Errorf("failed to register downloaded task: %w", err)
	}

	// Every 30 minutes: check needs_selection timeout
	if _, err := s.cron.AddFunc("0 */30 * * * *", s.wrapTask("selection_timeout", s.checkSelectionTimeout)); err != nil {
		return fmt.Errorf("failed to register selection timeout task: %w", err)
	}

	// Bangumi polling (configurable interval, default 24h)
	bangumiInterval := s.config.Bangumi.PollInterval
	if bangumiInterval == "" {
		bangumiInterval = "24h"
	}
	if bangumiCron, err := intervalToCron(bangumiInterval); err == nil {
		if _, err := s.cron.AddFunc(bangumiCron, s.wrapTask("bangumi_poll", s.pollBangumi)); err != nil {
			return fmt.Errorf("failed to register bangumi poll task: %w", err)
		}
	} else {
		s.logger.Warn("invalid bangumi poll interval, using default 24h", "interval", bangumiInterval, "error", err)
		if _, err := s.cron.AddFunc("0 0 * * * *", s.wrapTask("bangumi_poll", s.pollBangumi)); err != nil {
			return fmt.Errorf("failed to register bangumi poll task: %w", err)
		}
	}

	// Trakt polling (configurable interval, default 24h)
	traktInterval := s.config.Trakt.PollInterval
	if traktInterval == "" {
		traktInterval = "24h"
	}
	if traktCron, err := intervalToCron(traktInterval); err == nil {
		if _, err := s.cron.AddFunc(traktCron, s.wrapTask("trakt_poll", s.pollTrakt)); err != nil {
			return fmt.Errorf("failed to register trakt poll task: %w", err)
		}
	} else {
		s.logger.Warn("invalid trakt poll interval, using default 24h", "interval", traktInterval, "error", err)
		if _, err := s.cron.AddFunc("0 0 * * * *", s.wrapTask("trakt_poll", s.pollTrakt)); err != nil {
			return fmt.Errorf("failed to register trakt poll task: %w", err)
		}
	}

	// OneDrive health check (configurable interval, default 10m)
	healthInterval := s.config.OneDrive.HealthCheckInterval
	if healthInterval == "" {
		healthInterval = "10m"
	}
	if healthCron, err := intervalToCron(healthInterval); err == nil {
		if _, err := s.cron.AddFunc(healthCron, s.wrapTask("health_check", s.checkHealth)); err != nil {
			return fmt.Errorf("failed to register health check task: %w", err)
		}
	} else {
		s.logger.Warn("invalid health check interval, using default 10m", "interval", healthInterval, "error", err)
		if _, err := s.cron.AddFunc("0 */10 * * * *", s.wrapTask("health_check", s.checkHealth)); err != nil {
			return fmt.Errorf("failed to register health check task: %w", err)
		}
	}

	// Garbage collection (configurable interval, default 24h)
	gcInterval := s.config.PikPak.GCInterval
	if gcInterval == "" {
		gcInterval = "24h"
	}
	if gcCron, err := intervalToCron(gcInterval); err == nil {
		if _, err := s.cron.AddFunc(gcCron, s.wrapTask("garbage_collection", s.runGarbageCollection)); err != nil {
			return fmt.Errorf("failed to register garbage collection task: %w", err)
		}
	} else {
		s.logger.Warn("invalid gc interval, using default 24h", "interval", gcInterval, "error", err)
		if _, err := s.cron.AddFunc("0 0 * * * *", s.wrapTask("garbage_collection", s.runGarbageCollection)); err != nil {
			return fmt.Errorf("failed to register garbage collection task: %w", err)
		}
	}

	return nil
}

// wrapTask wraps a task function with error handling and panic recovery
func (s *Scheduler) wrapTask(name string, fn func(context.Context) error) func() {
	return func() {
		defer func() {
			if r := recover(); r != nil {
				s.logger.Error("task panicked", "task", name, "panic", r)
			}
		}()

		ctx, cancel := context.WithTimeout(s.ctx, 5*time.Minute)
		defer cancel()

		if err := fn(ctx); err != nil {
			s.logger.Error("task failed", "task", name, "error", err)
		}
	}
}

// processPendingEntries processes pending entries (trigger search)
func (s *Scheduler) processPendingEntries(ctx context.Context) error {
	// Check if previous round is still running
	s.mu.Lock()
	if s.pendingRunning {
		s.logger.Warn("previous pending task still running, skipping this round")
		s.mu.Unlock()
		return nil
	}
	s.pendingRunning = true
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.pendingRunning = false
		s.mu.Unlock()
	}()

	if s.onPendingTask != nil {
		return s.onPendingTask(ctx)
	}
	return nil
}

// processFoundEntries processes found entries (trigger download)
func (s *Scheduler) processFoundEntries(ctx context.Context) error {
	// Check if previous round is still running
	s.mu.Lock()
	if s.foundRunning {
		s.logger.Warn("previous found task still running, skipping this round")
		s.mu.Unlock()
		return nil
	}
	s.foundRunning = true
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.foundRunning = false
		s.mu.Unlock()
	}()

	if s.onFoundTask != nil {
		return s.onFoundTask(ctx)
	}
	return nil
}

// processDownloadedEntries processes downloaded entries (trigger transfer)
func (s *Scheduler) processDownloadedEntries(ctx context.Context) error {
	// Check if previous round is still running
	s.mu.Lock()
	if s.downloadedRunning {
		s.logger.Warn("previous downloaded task still running, skipping this round")
		s.mu.Unlock()
		return nil
	}
	s.downloadedRunning = true
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.downloadedRunning = false
		s.mu.Unlock()
	}()

	if s.onDownloadedTask != nil {
		return s.onDownloadedTask(ctx)
	}
	return nil
}

// checkSelectionTimeout checks for needs_selection entries that have timed out
func (s *Scheduler) checkSelectionTimeout(ctx context.Context) error {
	if s.onSelectionTimeout != nil {
		return s.onSelectionTimeout(ctx)
	}
	return nil
}

// pollBangumi polls Bangumi for new entries
func (s *Scheduler) pollBangumi(ctx context.Context) error {
	// Skip if Bangumi not configured
	if s.config.Bangumi.AccessToken == "" {
		s.logger.Debug("bangumi not configured, skipping poll")
		return nil
	}

	if s.onBangumiPoll != nil {
		return s.onBangumiPoll(ctx)
	}
	return nil
}

// pollTrakt polls Trakt for new entries
func (s *Scheduler) pollTrakt(ctx context.Context) error {
	// Skip if Trakt not configured
	if s.config.Trakt.ClientID == "" || s.config.Trakt.AccessToken == "" {
		s.logger.Debug("trakt not configured, skipping poll")
		return nil
	}

	if s.onTraktPoll != nil {
		return s.onTraktPoll(ctx)
	}
	return nil
}

// checkHealth checks OneDrive mount health
func (s *Scheduler) checkHealth(ctx context.Context) error {
	// Skip if OneDrive not configured
	if s.config.OneDrive.MountPath == "" {
		s.logger.Debug("onedrive not configured, skipping health check")
		return nil
	}

	if s.onHealthCheck != nil {
		return s.onHealthCheck(ctx)
	}
	return nil
}

// runGarbageCollection runs PikPak garbage collection
func (s *Scheduler) runGarbageCollection(ctx context.Context) error {
	if s.onGarbageCollection != nil {
		return s.onGarbageCollection(ctx)
	}
	return nil
}

// AcquireSearchSlot acquires a search semaphore slot (blocks if all slots are taken)
func (s *Scheduler) AcquireSearchSlot(ctx context.Context) error {
	select {
	case s.searchSemaphore <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ReleaseSearchSlot releases a search semaphore slot
func (s *Scheduler) ReleaseSearchSlot() {
	<-s.searchSemaphore
}

// intervalToCron converts a duration string (e.g. "5m", "1h", "24h") to a cron expression
func intervalToCron(interval string) (string, error) {
	d, err := time.ParseDuration(interval)
	if err != nil {
		return "", err
	}

	// Convert to cron expression
	switch {
	case d < time.Minute:
		// Less than 1 minute: every N seconds
		seconds := int(d.Seconds())
		return fmt.Sprintf("*/%d * * * * *", seconds), nil
	case d < time.Hour:
		// Less than 1 hour: every N minutes
		minutes := int(d.Minutes())
		return fmt.Sprintf("0 */%d * * * *", minutes), nil
	case d < 24*time.Hour:
		// Less than 24 hours: every N hours
		hours := int(d.Hours())
		return fmt.Sprintf("0 0 */%d * * *", hours), nil
	default:
		// 24 hours or more: once per day at midnight
		return "0 0 0 * * *", nil
	}
}
