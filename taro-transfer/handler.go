package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os/exec"

	"github.com/google/uuid"
)

// Handler handles HTTP requests
type Handler struct {
	taskManager *TaskManager
	authToken   string
}

// NewHandler creates a new handler
func NewHandler(taskManager *TaskManager) *Handler {
	// Get auth token from environment variable
	// If not set, use empty string (no authentication)
	authToken := ""
	// TODO: Read from environment variable when deploying
	
	return &Handler{
		taskManager: taskManager,
		authToken:   authToken,
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
	// Verify authentication if token is set
	if h.authToken != "" {
		authHeader := r.Header.Get("Authorization")
		if authHeader != "Bearer "+h.authToken {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
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

	// Construct rclone command
	// Source: pikpak:{sourcePath}
	// Target: onedrive:{targetPath}
	source := fmt.Sprintf("pikpak:%s", sourcePath)
	target := fmt.Sprintf("onedrive:%s", targetPath)

	// Execute rclone copy
	cmd := exec.Command("rclone", "copy", source, target, "-v")
	output, err := cmd.CombinedOutput()
	
	if err != nil {
		// Transfer failed
		errorMsg := fmt.Sprintf("rclone copy failed: %v, output: %s", err, string(output))
		log.Printf("transfer failed: task_id=%s error=%s", taskID, errorMsg)
		h.taskManager.UpdateTaskStatus(taskID, TaskStatusFailed, errorMsg)
		return
	}

	log.Printf("transfer completed, deleting source: task_id=%s", taskID)

	// Transfer succeeded, delete source file from PikPak
	deleteCmd := exec.Command("rclone", "delete", source, "-v")
	deleteOutput, deleteErr := deleteCmd.CombinedOutput()
	
	if deleteErr != nil {
		// Delete failed, but transfer succeeded
		// Log warning but mark task as done
		log.Printf("warning: failed to delete source file: task_id=%s error=%v output=%s", 
			taskID, deleteErr, string(deleteOutput))
	}

	// Mark task as done
	log.Printf("transfer done: task_id=%s", taskID)
	h.taskManager.UpdateTaskStatus(taskID, TaskStatusDone, "")
}
