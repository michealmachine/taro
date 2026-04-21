package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/google/uuid"
)

// Handler handles HTTP requests
type Handler struct {
	taskManager      *TaskManager
	authToken        string
	rcloneSourceName string // rclone remote name for source (e.g., "pikpak")
	rcloneTargetName string // rclone remote name for target (e.g., "onedrive" or "E5_warmachine")
}

// NewHandler creates a new handler
func NewHandler(taskManager *TaskManager) *Handler {
	// Get auth token from environment variable (REQUIRED)
	authToken := os.Getenv("TARO_TRANSFER_TOKEN")
	if authToken == "" {
		log.Fatal("TARO_TRANSFER_TOKEN environment variable is required")
	}

	// Get rclone remote names from environment variables (with defaults)
	rcloneSourceName := os.Getenv("RCLONE_SOURCE_REMOTE")
	if rcloneSourceName == "" {
		rcloneSourceName = "pikpak" // Default to pikpak
	}

	rcloneTargetName := os.Getenv("RCLONE_TARGET_REMOTE")
	if rcloneTargetName == "" {
		rcloneTargetName = "onedrive" // Default to onedrive
	}

	log.Printf("rclone configuration: source=%s target=%s", rcloneSourceName, rcloneTargetName)

	return &Handler{
		taskManager:      taskManager,
		authToken:        authToken,
		rcloneSourceName: rcloneSourceName,
		rcloneTargetName: rcloneTargetName,
	}
}

// CreateTransferRequest represents the request body for creating a transfer
type CreateTransferRequest struct {
	SourcePath string `json:"source_path"` // PikPak internal path (without pikpak: prefix)
	TargetPath string `json:"target_path"` // OneDrive target path
}

// CreateTransferResponse represents the response for creating a transfer
type CreateTransferResponse struct {
	TaskID string `json:"task_id"`
}

// GetTransferStatusResponse represents the response for getting transfer status
type GetTransferStatusResponse struct {
	Status string `json:"status"` // "pending" | "running" | "done" | "failed" | "not_found"
	Error  string `json:"error,omitempty"`
}

// CreateTransfer handles POST /transfer
func (h *Handler) CreateTransfer(w http.ResponseWriter, r *http.Request) {
	// Verify authentication (always required)
	authHeader := r.Header.Get("Authorization")
	if authHeader != "Bearer "+h.authToken {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Parse request body
	var req CreateTransferRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	// Validate request
	if req.SourcePath == "" || req.TargetPath == "" {
		http.Error(w, "source_path and target_path are required", http.StatusBadRequest)
		return
	}

	// Generate task ID
	taskID := uuid.New().String()

	// Create task
	h.taskManager.CreateTask(taskID)

	// Start transfer in background
	go h.executeTransfer(taskID, req.SourcePath, req.TargetPath)

	// Return task ID
	resp := CreateTransferResponse{
		TaskID: taskID,
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(resp)
}

// GetTransferStatus handles GET /transfer/{id}/status
func (h *Handler) GetTransferStatus(w http.ResponseWriter, r *http.Request) {
	// Verify authentication (required for consistency with create endpoint)
	authHeader := r.Header.Get("Authorization")
	if authHeader != "Bearer "+h.authToken {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Extract task ID from path
	taskID := r.PathValue("id")
	if taskID == "" {
		http.Error(w, "task_id is required", http.StatusBadRequest)
		return
	}

	// Get task state
	state := h.taskManager.GetTask(taskID)
	if state == nil {
		// Task not found - return not_found status
		resp := GetTransferStatusResponse{
			Status: "not_found",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
		return
	}

	// Return task status
	resp := GetTransferStatusResponse{
		Status: string(state.Status),
		Error:  state.ErrorMessage,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// Health handles GET /health
func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status": "ok",
	})
}

// executeTransfer executes the rclone transfer in background
func (h *Handler) executeTransfer(taskID, sourcePath, targetPath string) {
	log.Printf("starting transfer: task_id=%s source=%s target=%s", taskID, sourcePath, targetPath)

	// Update status to running
	h.taskManager.UpdateTaskStatus(taskID, TaskStatusRunning, "")

	// Construct rclone command using configured remote names
	// Source: {rcloneSourceName}:{sourcePath}
	// Target: {rcloneTargetName}:{targetPath}
	source := fmt.Sprintf("%s:%s", h.rcloneSourceName, sourcePath)
	target := fmt.Sprintf("%s:%s", h.rcloneTargetName, targetPath)

	// Execute rclone copy with 30 minute timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "rclone", "copy", source, target, "-v")
	output, err := cmd.CombinedOutput()

	if err != nil {
		// Check if timeout
		if ctx.Err() == context.DeadlineExceeded {
			errorMsg := fmt.Sprintf("rclone copy timeout after 30 minutes: %s", string(output))
			log.Printf("transfer timeout: task_id=%s", taskID)
			h.taskManager.UpdateTaskStatus(taskID, TaskStatusFailed, errorMsg)
			return
		}

		// Transfer failed
		errorMsg := fmt.Sprintf("rclone copy failed: %v, output: %s", err, string(output))
		log.Printf("transfer failed: task_id=%s error=%s", taskID, errorMsg)
		h.taskManager.UpdateTaskStatus(taskID, TaskStatusFailed, errorMsg)
		return
	}

	log.Printf("transfer completed, deleting source: task_id=%s", taskID)

	// Transfer succeeded, delete source file from PikPak with 10 minute timeout
	deleteCtx, deleteCancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer deleteCancel()

	deleteCmd := exec.CommandContext(deleteCtx, "rclone", "delete", source, "-v")
	deleteOutput, deleteErr := deleteCmd.CombinedOutput()

	if deleteErr != nil {
		// Check if timeout
		if deleteCtx.Err() == context.DeadlineExceeded {
			log.Printf("warning: delete timeout after 10 minutes: task_id=%s", taskID)
		} else {
			// Delete failed, but transfer succeeded
			// Log warning but mark task as done
			log.Printf("warning: failed to delete source file: task_id=%s error=%v output=%s",
				taskID, deleteErr, string(deleteOutput))
		}
	}

	// Mark task as done
	log.Printf("transfer done: task_id=%s", taskID)
	h.taskManager.UpdateTaskStatus(taskID, TaskStatusDone, "")
}
