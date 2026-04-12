package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	// Initialize task manager
	taskManager := NewTaskManager()

	// Initialize handler
	handler := NewHandler(taskManager)

	// Setup HTTP routes
	mux := http.NewServeMux()
	mux.HandleFunc("POST /transfer", handler.CreateTransfer)
	mux.HandleFunc("GET /transfer/{id}/status", handler.GetTransferStatus)
	mux.HandleFunc("GET /health", handler.Health)

	// Create server
	server := &http.Server{
		Addr:    ":7860",
		Handler: mux,
	}

	// Start server in goroutine
	go func() {
		log.Println("taro-transfer service starting on :7860")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("failed to start server: %v", err)
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("shutting down server...")

	// Graceful shutdown with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("server forced to shutdown: %v", err)
	}

	log.Println("server exited")
}
