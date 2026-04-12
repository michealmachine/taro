package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestCreateTransfer(t *testing.T) {
	// Set required environment variable for testing
	os.Setenv("TARO_TRANSFER_TOKEN", "test-token")
	defer os.Unsetenv("TARO_TRANSFER_TOKEN")

	taskManager := NewTaskManager()
	handler := NewHandler(taskManager)

	// Create request
	reqBody := CreateTransferRequest{
		SourcePath: "/downloads/test.mkv",
		TargetPath: "/media/test/",
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/transfer", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()

	// Execute
	handler.CreateTransfer(w, req)

	// Assert
	if w.Code != http.StatusCreated {
		t.Errorf("expected status 201, got %d", w.Code)
	}

	var resp CreateTransferResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.TaskID == "" {
		t.Error("expected task_id to be non-empty")
	}

	// Verify task was created
	time.Sleep(10 * time.Millisecond) // Give goroutine time to start
	state := taskManager.GetTask(resp.TaskID)
	if state == nil {
		t.Fatal("task not found in task manager")
	}

	// Task should be pending or running
	if state.Status != TaskStatusPending && state.Status != TaskStatusRunning {
		t.Errorf("expected status pending or running, got %s", state.Status)
	}
}

func TestCreateTransferUnauthorized(t *testing.T) {
	os.Setenv("TARO_TRANSFER_TOKEN", "test-token")
	defer os.Unsetenv("TARO_TRANSFER_TOKEN")

	taskManager := NewTaskManager()
	handler := NewHandler(taskManager)

	reqBody := CreateTransferRequest{
		SourcePath: "/downloads/test.mkv",
		TargetPath: "/media/test/",
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/transfer", bytes.NewReader(body))
	// No Authorization header
	w := httptest.NewRecorder()

	handler.CreateTransfer(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", w.Code)
	}
}

func TestCreateTransferMissingFields(t *testing.T) {
	os.Setenv("TARO_TRANSFER_TOKEN", "test-token")
	defer os.Unsetenv("TARO_TRANSFER_TOKEN")

	taskManager := NewTaskManager()
	handler := NewHandler(taskManager)

	tests := []struct {
		name string
		body CreateTransferRequest
	}{
		{
			name: "missing source_path",
			body: CreateTransferRequest{
				TargetPath: "/media/test/",
			},
		},
		{
			name: "missing target_path",
			body: CreateTransferRequest{
				SourcePath: "/downloads/test.mkv",
			},
		},
		{
			name: "missing both",
			body: CreateTransferRequest{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, _ := json.Marshal(tt.body)
			req := httptest.NewRequest("POST", "/transfer", bytes.NewReader(body))
			req.Header.Set("Authorization", "Bearer test-token")
			w := httptest.NewRecorder()

			handler.CreateTransfer(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("expected status 400, got %d", w.Code)
			}
		})
	}
}

func TestGetTransferStatus(t *testing.T) {
	os.Setenv("TARO_TRANSFER_TOKEN", "test-token")
	defer os.Unsetenv("TARO_TRANSFER_TOKEN")

	taskManager := NewTaskManager()
	handler := NewHandler(taskManager)

	// Create a task
	taskID := "test-task-id"
	taskManager.CreateTask(taskID)

	// Create request with path parameter
	req := httptest.NewRequest("GET", "/transfer/"+taskID+"/status", nil)
	req.SetPathValue("id", taskID)
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()

	// Execute
	handler.GetTransferStatus(w, req)

	// Assert
	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var resp GetTransferStatusResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Status != string(TaskStatusPending) {
		t.Errorf("expected status pending, got %s", resp.Status)
	}
}

func TestGetTransferStatusUnauthorized(t *testing.T) {
	os.Setenv("TARO_TRANSFER_TOKEN", "test-token")
	defer os.Unsetenv("TARO_TRANSFER_TOKEN")

	taskManager := NewTaskManager()
	handler := NewHandler(taskManager)

	taskID := "test-task-id"
	taskManager.CreateTask(taskID)

	req := httptest.NewRequest("GET", "/transfer/"+taskID+"/status", nil)
	req.SetPathValue("id", taskID)
	// No Authorization header
	w := httptest.NewRecorder()

	handler.GetTransferStatus(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", w.Code)
	}
}

func TestGetTransferStatusNotFound(t *testing.T) {
	os.Setenv("TARO_TRANSFER_TOKEN", "test-token")
	defer os.Unsetenv("TARO_TRANSFER_TOKEN")

	taskManager := NewTaskManager()
	handler := NewHandler(taskManager)

	// Request non-existent task
	req := httptest.NewRequest("GET", "/transfer/non-existent/status", nil)
	req.SetPathValue("id", "non-existent")
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()

	// Execute
	handler.GetTransferStatus(w, req)

	// Assert
	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var resp GetTransferStatusResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Status != "not_found" {
		t.Errorf("expected status not_found, got %s", resp.Status)
	}
}

func TestHealth(t *testing.T) {
	os.Setenv("TARO_TRANSFER_TOKEN", "test-token")
	defer os.Unsetenv("TARO_TRANSFER_TOKEN")

	taskManager := NewTaskManager()
	handler := NewHandler(taskManager)

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()

	handler.Health(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["status"] != "ok" {
		t.Errorf("expected status ok, got %s", resp["status"])
	}
}

func TestTaskManager(t *testing.T) {
	tm := NewTaskManager()

	// Test CreateTask
	taskID := "test-task"
	tm.CreateTask(taskID)

	// Test GetTask
	state := tm.GetTask(taskID)
	if state == nil {
		t.Fatal("task not found")
	}
	if state.Status != TaskStatusPending {
		t.Errorf("expected status pending, got %s", state.Status)
	}

	// Test UpdateTaskStatus
	tm.UpdateTaskStatus(taskID, TaskStatusRunning, "")
	state = tm.GetTask(taskID)
	if state.Status != TaskStatusRunning {
		t.Errorf("expected status running, got %s", state.Status)
	}

	// Test UpdateTaskStatus with error
	tm.UpdateTaskStatus(taskID, TaskStatusFailed, "test error")
	state = tm.GetTask(taskID)
	if state.Status != TaskStatusFailed {
		t.Errorf("expected status failed, got %s", state.Status)
	}
	if state.ErrorMessage != "test error" {
		t.Errorf("expected error message 'test error', got %s", state.ErrorMessage)
	}

	// Test GetTask for non-existent task
	nonExistent := tm.GetTask("non-existent")
	if nonExistent != nil {
		t.Error("expected nil for non-existent task")
	}
}

// TestTaskStateConcurrency tests concurrent access to TaskState
func TestTaskStateConcurrency(t *testing.T) {
	tm := NewTaskManager()
	taskID := "concurrent-test"
	tm.CreateTask(taskID)

	// Simulate concurrent reads and writes
	done := make(chan bool)

	// Writer goroutine
	go func() {
		for i := 0; i < 100; i++ {
			tm.UpdateTaskStatus(taskID, TaskStatusRunning, "updating")
		}
		done <- true
	}()

	// Reader goroutines
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				state := tm.GetTask(taskID)
				if state == nil {
					t.Error("task should exist")
				}
			}
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 11; i++ {
		<-done
	}
}
