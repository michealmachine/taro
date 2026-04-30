package web

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/michealmachine/taro/internal/db"
	"github.com/michealmachine/taro/internal/service"
	"github.com/michealmachine/taro/internal/web/handlers"
	"github.com/michealmachine/taro/internal/webhook"
)

// Server represents the minimal HTTP server for webhooks and health checks
// Full WebUI will be implemented in Task 6.2
type Server struct {
	port           int
	server         *http.Server
	webhookHandler *webhook.JellyfinHandler
	entriesHandler *handlers.EntriesHandler
	entriesList    *handlers.EntriesListHandler
	entryDetail    *handlers.EntryDetailHandler
	pendingHandler *handlers.PendingHandler
	statusHandler  *handlers.StatusHandler
	logger         *slog.Logger
}

// NewServer creates a new HTTP server
func NewServer(port int, webhookHandler *webhook.JellyfinHandler, actionService *service.ActionService, database *db.DB, statusHandler *handlers.StatusHandler, logger *slog.Logger) *Server {
	return &Server{
		port:           port,
		webhookHandler: webhookHandler,
		entriesHandler: handlers.NewEntriesHandler(actionService, logger),
		entriesList:    handlers.NewEntriesListHandler(database),
		entryDetail:    handlers.NewEntryDetailHandler(database),
		pendingHandler: handlers.NewPendingHandler(database),
		statusHandler:  statusHandler,
		logger:         logger,
	}
}

// Start starts the HTTP server
func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()

	// Health check endpoint (required for Docker healthcheck)
	mux.HandleFunc("GET /health", s.handleHealth)

	// Root redirect
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/entries", http.StatusSeeOther)
	})

	// Jellyfin webhook endpoint (required for transferred -> in_library transition)
	mux.HandleFunc("POST /webhook/jellyfin", s.webhookHandler.HandleJellyfin)

	// Entry management endpoints (Task 6.2.2)
	mux.HandleFunc("POST /entries", s.entriesHandler.HandleAddEntry)
	mux.HandleFunc("GET /entries", s.entriesList.HandleEntries)
	mux.HandleFunc("GET /entries/{id}", s.entryDetail.HandleEntryDetail)
	mux.HandleFunc("POST /entries/{id}/retry", s.entriesHandler.HandleRetry)
	mux.HandleFunc("POST /entries/{id}/cancel", s.entriesHandler.HandleCancel)
	mux.HandleFunc("POST /entries/{id}/select", s.entriesHandler.HandleSelect)

	// Pending selection
	mux.HandleFunc("GET /pending", s.pendingHandler.HandlePending)

	// System status
	mux.HandleFunc("GET /status", s.statusHandler.HandleStatus)

	s.server = &http.Server{
		Addr:    fmt.Sprintf(":%d", s.port),
		Handler: mux,
	}

	s.logger.Info("starting HTTP server", "port", s.port)

	// Start server in goroutine
	go func() {
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.logger.Error("HTTP server error", "error", err)
		}
	}()

	// Wait for context cancellation
	<-ctx.Done()

	// Graceful shutdown
	s.logger.Info("shutting down HTTP server")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := s.server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("failed to shutdown HTTP server: %w", err)
	}

	s.logger.Info("HTTP server stopped")
	return nil
}

// handleHealth handles health check requests
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}
