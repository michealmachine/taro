package main

import (
	"sync"
	"time"
)

// TaskStatus represents the status of a transfer task
type TaskStatus string

const (
	TaskStatusPending TaskStatus = "pending"
	TaskStatusRunning TaskStatus = "running"
	TaskStatusDone    TaskStatus = "done"
	TaskStatusFailed  TaskStatus = "failed"
)

// TaskState represents the state of a transfer task
type TaskState struct {
	mu           sync.RWMutex
	Status       TaskStatus
	ErrorMessage string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Copy returns a deep copy of the task state (safe for concurrent access)
func (ts *TaskState) Copy() TaskState {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return TaskState{
		Status:       ts.Status,
		ErrorMessage: ts.ErrorMessage,
		CreatedAt:    ts.CreatedAt,
		UpdatedAt:    ts.UpdatedAt,
	}
}

// TaskManager manages transfer tasks in memory
type TaskManager struct {
	tasks sync.Map // map[string]*TaskState
}

// NewTaskManager creates a new task manager
func NewTaskManager() *TaskManager {
	return &TaskManager{}
}

// CreateTask creates a new task with pending status
func (tm *TaskManager) CreateTask(taskID string) {
	now := time.Now()
	state := &TaskState{
		Status:    TaskStatusPending,
		CreatedAt: now,
		UpdatedAt: now,
	}
	tm.tasks.Store(taskID, state)
}

// GetTask retrieves a task by ID
// Returns a copy of the task state (safe for concurrent access)
// Returns nil if task not found
func (tm *TaskManager) GetTask(taskID string) *TaskState {
	value, ok := tm.tasks.Load(taskID)
	if !ok {
		return nil
	}
	state := value.(*TaskState)
	copy := state.Copy()
	return &copy
}

// UpdateTaskStatus updates the status of a task
func (tm *TaskManager) UpdateTaskStatus(taskID string, status TaskStatus, errorMessage string) {
	value, ok := tm.tasks.Load(taskID)
	if !ok {
		return
	}

	state := value.(*TaskState)
	state.mu.Lock()
	state.Status = status
	state.ErrorMessage = errorMessage
	state.UpdatedAt = time.Now()
	state.mu.Unlock()
}
