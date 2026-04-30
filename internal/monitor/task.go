package monitor

import (
	"sort"
	"sync"
	"time"
)

// TaskRecord tracks execution details for a scheduled task.
type TaskRecord struct {
	Name         string
	LastStarted  time.Time
	LastFinished time.Time
	LastDuration time.Duration
	LastError    string
}

// TaskMonitor records task execution status for observability.
type TaskMonitor struct {
	mu    sync.RWMutex
	tasks map[string]*TaskRecord
}

// NewTaskMonitor creates a new task monitor.
func NewTaskMonitor() *TaskMonitor {
	return &TaskMonitor{
		tasks: make(map[string]*TaskRecord),
	}
}

// Start marks a task start time.
func (m *TaskMonitor) Start(name string) {
	if name == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	record := m.ensure(name)
	record.LastStarted = time.Now()
	record.LastError = ""
}

// Finish marks a task finish time and records error if present.
func (m *TaskMonitor) Finish(name string, err error) {
	if name == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	record := m.ensure(name)
	record.LastFinished = time.Now()
	if !record.LastStarted.IsZero() {
		record.LastDuration = record.LastFinished.Sub(record.LastStarted)
	}
	if err != nil {
		record.LastError = err.Error()
	}
}

// Snapshot returns a copy of task records sorted by name.
func (m *TaskMonitor) Snapshot() []TaskRecord {
	m.mu.RLock()
	records := make([]TaskRecord, 0, len(m.tasks))
	for _, record := range m.tasks {
		records = append(records, *record)
	}
	m.mu.RUnlock()

	sort.Slice(records, func(i, j int) bool {
		return records[i].Name < records[j].Name
	})
	return records
}

func (m *TaskMonitor) ensure(name string) *TaskRecord {
	record, ok := m.tasks[name]
	if !ok {
		record = &TaskRecord{Name: name}
		m.tasks[name] = record
	}
	return record
}
