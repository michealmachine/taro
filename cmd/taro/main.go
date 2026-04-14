package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/michealmachine/taro/internal/config"
	"github.com/michealmachine/taro/internal/db"
	"github.com/michealmachine/taro/internal/downloader"
	"github.com/michealmachine/taro/internal/health"
	"github.com/michealmachine/taro/internal/notifier"
	"github.com/michealmachine/taro/internal/platform"
	"github.com/michealmachine/taro/internal/poller"
	"github.com/michealmachine/taro/internal/scheduler"
	"github.com/michealmachine/taro/internal/searcher"
	"github.com/michealmachine/taro/internal/service"
	"github.com/michealmachine/taro/internal/state"
	"github.com/michealmachine/taro/internal/transfer"
	"github.com/michealmachine/taro/internal/web"
	"github.com/michealmachine/taro/internal/webhook"
)

var version = "dev"

func main() {
	// Parse command line flags
	configPath := flag.String("config", "config.yaml", "path to config file")
	flag.Parse()

	// Run main logic
	if err := run(*configPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(configPath string) error {
	// ========================================
	// 4.2.1 配置加载与日志初始化
	// ========================================

	// Load configuration
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Initialize logger based on config
	logger := initLogger(cfg)
	logger.Info("taro starting", "version", version)

	// ========================================
	// 4.2.2 依赖注入与模块初始化
	// ========================================

	// Initialize database
	logger.Info("initializing database", "path", cfg.Server.DBPath)
	database, err := db.Open(cfg.Server.DBPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer database.Close()
	logger.Info("database initialized")

	// Initialize state machine
	sm := state.NewStateMachine(database, logger)
	logger.Info("state machine initialized")

	// Initialize notifier (optional - skip if not configured)
	var telegramNotifier *notifier.TelegramNotifier
	if cfg.Telegram.BotToken != "" && cfg.Telegram.ChatID != 0 {
		telegramNotifier, err = notifier.NewTelegramNotifier(cfg.Telegram.BotToken, cfg.Telegram.ChatID, logger)
		if err != nil {
			logger.Warn("failed to initialize telegram notifier, notifications disabled", "error", err)
		} else {
			logger.Info("telegram notifier initialized")
		}
	} else {
		logger.Warn("telegram not configured, notifications disabled")
	}

	// Initialize pollers (optional - skip if not configured)
	var bangumiPoller *poller.BangumiPoller
	if cfg.Bangumi.AccessToken != "" {
		bangumiPoller = poller.NewBangumiPoller(cfg, database, logger)
		logger.Info("bangumi poller initialized")
	} else {
		logger.Warn("bangumi not configured, skipping bangumi poller")
	}

	var traktPoller *poller.TraktPoller
	if cfg.Trakt.ClientID != "" && cfg.Trakt.AccessToken != "" {
		traktPoller = poller.NewTraktPoller(cfg, database, logger)
		logger.Info("trakt poller initialized")
	} else {
		logger.Warn("trakt not configured, skipping trakt poller")
	}

	// Initialize searcher (required)
	prowlarrSearcher := searcher.NewSearcher(cfg, database, sm, logger)
	logger.Info("prowlarr searcher initialized")

	// Auto-login to PikPak using credentials from config
	if err := ensurePikPakLogin(cfg, logger); err != nil {
		logger.Warn("failed to auto-login to pikpak, will retry on first use", "error", err)
	}

	// Initialize downloader (required)
	pikpakDownloader, err := downloader.NewPikPakDownloader(cfg, database, sm, logger)
	if err != nil {
		return fmt.Errorf("failed to initialize pikpak downloader: %w", err)
	}
	logger.Info("pikpak downloader initialized")

	// Initialize transfer coordinator (required)
	transferCoordinator := transfer.NewCoordinator(cfg, database, sm, logger)
	logger.Info("transfer coordinator initialized")

	// Initialize webhook handler
	jellyfinHandler := webhook.NewJellyfinHandler(database, sm, logger)
	jellyfinHandler.SetMountPath(cfg.OneDrive.MountPath)
	logger.Info("jellyfin webhook handler initialized")

	// Initialize platform updaters (optional - skip if not configured)
	var bangumiUpdater *platform.BangumiUpdater
	if cfg.Bangumi.AccessToken != "" {
		bangumiUpdater = platform.NewBangumiUpdater(cfg, logger)
		logger.Info("bangumi updater initialized")
	}

	var traktUpdater *platform.TraktUpdater
	if cfg.Trakt.ClientID != "" && cfg.Trakt.AccessToken != "" {
		traktUpdater = platform.NewTraktUpdater(cfg, logger)
		logger.Info("trakt updater initialized")
	}

	// Set up platform callback for webhook handler
	jellyfinHandler.SetOnInLibraryCallback(func(ctx context.Context, entry *db.Entry) error {
		// Call appropriate platform updater based on entry source
		switch entry.Source {
		case "bangumi":
			if bangumiUpdater != nil {
				return bangumiUpdater.MarkOwned(ctx, entry)
			}
		case "trakt":
			if traktUpdater != nil {
				return traktUpdater.MarkOwned(ctx, entry)
			}
		}
		return nil
	})

	// Initialize OneDrive health checker (optional - skip if not configured)
	var oneDriveChecker *health.OneDriveChecker
	if cfg.OneDrive.MountPath != "" {
		checkInterval := 10 * time.Minute
		if cfg.OneDrive.HealthCheckInterval != "" {
			if duration, err := time.ParseDuration(cfg.OneDrive.HealthCheckInterval); err == nil {
				checkInterval = duration
			} else {
				logger.Warn("invalid onedrive health_check_interval, using default 10m",
					"configured", cfg.OneDrive.HealthCheckInterval,
					"error", err)
			}
		}

		oneDriveChecker = health.NewOneDriveChecker(cfg.OneDrive.MountPath, checkInterval, logger)

		// Set up status change callback for notifications
		if telegramNotifier != nil {
			oneDriveChecker.SetOnStatusChangeCallback(func(isHealthy bool) {
				ctx := context.Background()
				if isHealthy {
					telegramNotifier.NotifyMountUp(ctx, cfg.OneDrive.MountPath)
				} else {
					telegramNotifier.NotifyMountDown(ctx, cfg.OneDrive.MountPath)
				}
			})
		}

		logger.Info("onedrive health checker initialized")
	} else {
		logger.Warn("onedrive not configured, skipping health checker")
	}

	// Initialize garbage collector
	gc := downloader.NewGarbageCollector(pikpakDownloader, database, cfg, logger)
	logger.Info("garbage collector initialized")

	// Initialize ActionService (for user actions: add, retry, cancel, select)
	actionService := service.NewActionService(database, sm, logger)
	logger.Info("action service initialized")

	// Recover on startup
	logger.Info("recovering state on startup")
	if err := sm.RecoverOnStartup(context.Background(), &state.RecoveryCallbacks{
		OnDownloading: func(entryID, taskID string, downloadStartedAt time.Time) error {
			return pikpakDownloader.ResumeEntryPolling(entryID, taskID, downloadStartedAt)
		},
		OnTransferring: func(entryID, taskID string, transferStartedAt time.Time) error {
			return transferCoordinator.ResumePolling(entryID, taskID, transferStartedAt)
		},
	}); err != nil {
		return fmt.Errorf("failed to recover on startup: %w", err)
	}
	logger.Info("startup recovery completed")

	// Declare scheduler variable first (will be initialized below)
	var sched *scheduler.Scheduler

	// Initialize scheduler
	sched = scheduler.NewScheduler(cfg, database, logger, &scheduler.TaskHandlers{
		OnPendingTask: func(ctx context.Context) error {
			// Process pending entries (trigger search)
			entries, err := database.ListEntriesByStatus(ctx, string(state.StatusPending))
			if err != nil {
				return err
			}

			// Use goroutines with semaphore to control concurrency
			var wg sync.WaitGroup
			for _, entry := range entries {
				wg.Add(1)
				go func(e *db.Entry) {
					defer wg.Done()

					// Acquire semaphore slot (blocks if all slots are taken)
					if err := sched.AcquireSearchSlot(ctx); err != nil {
						logger.Error("failed to acquire search slot", "entry_id", e.ID, "error", err)
						return
					}
					defer sched.ReleaseSearchSlot()

					// Perform search
					if err := prowlarrSearcher.Search(ctx, e); err != nil {
						logger.Error("failed to search entry", "entry_id", e.ID, "error", err)
					}
				}(entry)
			}

			// Wait for all searches to complete
			wg.Wait()
			return nil
		},
		OnFoundTask: func(ctx context.Context) error {
			// Process found entries (trigger download)
			entries, err := database.ListEntriesByStatus(ctx, string(state.StatusFound))
			if err != nil {
				return err
			}
			for _, entry := range entries {
				// Get selected resource
				if !entry.SelectedResourceID.Valid {
					logger.Error("found entry missing selected_resource_id", "entry_id", entry.ID)
					continue
				}
				resource, err := database.GetResource(ctx, entry.SelectedResourceID.String)
				if err != nil {
					logger.Error("failed to get selected resource", "entry_id", entry.ID, "error", err)
					continue
				}
				if err := pikpakDownloader.Submit(ctx, entry, resource.Magnet); err != nil {
					logger.Error("failed to submit download", "entry_id", entry.ID, "error", err)
				}
			}
			return nil
		},
		OnDownloadedTask: func(ctx context.Context) error {
			// Process downloaded entries (trigger transfer)
			entries, err := database.ListEntriesByStatus(ctx, string(state.StatusDownloaded))
			if err != nil {
				return err
			}
			for _, entry := range entries {
				if err := transferCoordinator.Submit(ctx, entry.ID); err != nil {
					logger.Error("failed to submit transfer", "entry_id", entry.ID, "error", err)
				}
			}
			return nil
		},
		OnSelectionTimeout: func(ctx context.Context) error {
			// Check needs_selection entries for timeout
			entries, err := database.ListEntriesByStatus(ctx, string(state.StatusNeedsSelection))
			if err != nil {
				return err
			}

			// Parse selection timeout from config
			selectionTimeout := 24 * time.Hour
			if cfg.Defaults.SelectionTimeout != "" {
				if duration, err := time.ParseDuration(cfg.Defaults.SelectionTimeout); err == nil {
					selectionTimeout = duration
				}
			}

			for _, entry := range entries {
				if time.Since(entry.UpdatedAt) > selectionTimeout {
					logger.Info("selection timeout, auto-selecting best resource", "entry_id", entry.ID)

					// Get all eligible resources for this entry
					resources, err := database.ListEligibleByEntry(ctx, entry.ID)
					if err != nil {
						logger.Error("failed to get resources for timeout selection", "entry_id", entry.ID, "error", err)
						continue
					}

					if len(resources) == 0 {
						// No eligible resources, transition to failed
						logger.Warn("no eligible resources for timeout selection", "entry_id", entry.ID)
						if err := sm.TransitionToFailed(ctx, entry.ID, state.FailureNoResources, "needs_selection", "selection timeout with no eligible resources"); err != nil {
							logger.Error("failed to transition to failed", "entry_id", entry.ID, "error", err)
						}
						continue
					}

					// Use searcher's selection logic to pick the best resource
					best := prowlarrSearcher.SelectBestResourceForEntry(entry, resources)
					if best == nil {
						logger.Error("failed to select best resource", "entry_id", entry.ID)
						continue
					}

					logger.Info("auto-selected resource after timeout",
						"entry_id", entry.ID,
						"resource_id", best.ID,
						"resolution", best.Resolution)

					// Mark resource as selected
					best.Selected = true
					if err := database.UpdateResource(ctx, best); err != nil {
						logger.Error("failed to mark resource as selected", "resource_id", best.ID, "error", err)
						continue
					}

					// Transition to found with selected resource
					if err := sm.TransitionWithUpdate(ctx, entry.ID, state.StatusFound, map[string]any{
						"selected_resource_id": best.ID,
						"resolution":           best.Resolution.String,
						"reason":               "auto-selected after timeout",
					}); err != nil {
						logger.Error("failed to transition to found", "entry_id", entry.ID, "error", err)
					}
				}
			}
			return nil
		},
		OnBangumiPoll: func(ctx context.Context) error {
			if bangumiPoller != nil {
				return bangumiPoller.Poll(ctx)
			}
			return nil
		},
		OnTraktPoll: func(ctx context.Context) error {
			if traktPoller != nil {
				return traktPoller.Poll(ctx)
			}
			return nil
		},
		OnHealthCheck: func(ctx context.Context) error {
			// Health check is handled by OneDriveChecker's own goroutine
			return nil
		},
		OnGarbageCollection: func(ctx context.Context) error {
			return gc.RunGC(ctx)
		},
	})
	logger.Info("scheduler initialized")

	// ========================================
	// 4.2.3 启动后台服务
	// ========================================

	// Create context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup

	// Start scheduler
	if err := sched.Start(); err != nil {
		return fmt.Errorf("failed to start scheduler: %w", err)
	}
	logger.Info("scheduler started")

	// Start downloader polling
	pikpakDownloader.StartPolling(ctx)
	logger.Info("pikpak downloader polling started")

	// Start transfer coordinator polling
	transferCoordinator.StartPolling()
	logger.Info("transfer coordinator polling started")

	// Start OneDrive health checker (if configured)
	if oneDriveChecker != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			oneDriveChecker.Start(ctx)
		}()
		logger.Info("onedrive health checker started")
	}

	// Start garbage collector
	gc.Start(ctx)
	logger.Info("garbage collector started")

	// Start HTTP server (webhook + health check + entry management)
	// Full WebUI will be implemented in Task 6.2
	httpServer := web.NewServer(cfg.Server.Port, jellyfinHandler, actionService, logger)
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := httpServer.Start(ctx); err != nil {
			logger.Error("HTTP server error", "error", err)
		}
	}()
	logger.Info("HTTP server started", "port", cfg.Server.Port)

	// TODO: Start TG Bot (Task 6.1)

	logger.Info("taro started successfully")

	// ========================================
	// 4.2.4 优雅关闭
	// ========================================

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	logger.Info("shutdown signal received, starting graceful shutdown")

	// Cancel context to stop all goroutines
	cancel()

	// Stop services in reverse order
	logger.Info("stopping garbage collector")
	gc.Stop()

	if oneDriveChecker != nil {
		logger.Info("stopping onedrive health checker")
		oneDriveChecker.Stop()
	}

	logger.Info("stopping transfer coordinator")
	transferCoordinator.Stop()

	logger.Info("stopping pikpak downloader")
	pikpakDownloader.Stop()

	logger.Info("stopping scheduler")
	sched.Stop()

	// Wait for all goroutines to finish (with timeout)
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		logger.Info("all goroutines stopped gracefully")
	case <-time.After(30 * time.Second):
		logger.Warn("shutdown timeout, forcing exit")
	}

	logger.Info("taro stopped")
	return nil
}

// initLogger initializes the logger based on config
func initLogger(cfg *config.Config) *slog.Logger {
	var level slog.Level
	switch cfg.Logging.Level {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	var handler slog.Handler
	if cfg.Logging.Format == "json" {
		handler = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			Level: level,
		})
	} else {
		handler = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			Level: level,
		})
	}

	return slog.New(handler)
}

// ensurePikPakLogin ensures pikpaktui is logged in using credentials from config
func ensurePikPakLogin(cfg *config.Config, logger *slog.Logger) error {
	// Run pikpaktui login with credentials from config
	cmd := exec.Command("pikpaktui", "login", "-u", cfg.PikPak.Username, "-p", cfg.PikPak.Password)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("pikpaktui login failed: %w (output: %s)", err, string(output))
	}
	logger.Info("pikpak auto-login successful", "user", cfg.PikPak.Username)
	return nil
}
