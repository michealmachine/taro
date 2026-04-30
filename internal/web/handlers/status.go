package handlers

import (
	"context"
	"net/http"
	"sort"
	"time"

	"github.com/michealmachine/taro/internal/db"
	"github.com/michealmachine/taro/internal/health"
	"github.com/michealmachine/taro/internal/monitor"
	"github.com/michealmachine/taro/internal/transfer"
	"github.com/michealmachine/taro/internal/web/templates"
)

type StatusHandler struct {
	database         *db.DB
	oneDriveChecker  *health.OneDriveChecker
	transfer         *transfer.Coordinator
	monitor          *monitor.TaskMonitor
	startTime        time.Time
	transferPollTime func() time.Time
	downloadPollTime func() time.Time
}

type transferSnapshot struct {
	EntryID       string
	Title         string
	TaskID        string
	Status        string
	Error         string
	StartedAt     time.Time
	LastCheckedAt time.Time
	TaskCreatedAt time.Time
	TaskUpdatedAt time.Time
	Elapsed       time.Duration
	TargetPath    string
}

func NewStatusHandler(database *db.DB, checker *health.OneDriveChecker, coordinator *transfer.Coordinator, taskMonitor *monitor.TaskMonitor, startTime time.Time, transferPollTime func() time.Time, downloadPollTime func() time.Time) *StatusHandler {
	return &StatusHandler{
		database:         database,
		oneDriveChecker:  checker,
		transfer:         coordinator,
		monitor:          taskMonitor,
		startTime:        startTime,
		transferPollTime: transferPollTime,
		downloadPollTime: downloadPollTime,
	}
}

func (h *StatusHandler) HandleStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	counts, _ := h.database.CountEntriesByStatus(ctx)
	logs, _ := h.database.ListRecentStateLogs(ctx, 50)
	recentFailed, _ := h.database.ListEntriesByStatusLimited(ctx, "failed", 12)
	pendingSelection, _ := h.database.ListEntriesByStatusLimited(ctx, "needs_selection", 12)

	activeTransfers := h.collectTransferSnapshots(ctx)

	var healthy *bool
	if h.oneDriveChecker != nil {
		status := h.oneDriveChecker.GetStatus()
		healthy = &status
	}

	var taskRecords []monitor.TaskRecord
	if h.monitor != nil {
		taskRecords = h.monitor.Snapshot()
	}

	pageData := templates.StatusPageData{
		Meta: templates.PageMeta{
			Title: "Status · taro",
			Path:  "/status",
		},
		Uptime:           time.Since(h.startTime),
		StartTime:        h.startTime,
		StatusCounts:     counts,
		RecentLogs:       mapStateLogs(logs),
		RecentFailed:     mapEntrySummaries(recentFailed),
		ActiveTransfers:  mapTransferSummaries(activeTransfers),
		PendingSelection: mapEntrySummaries(pendingSelection),
		OneDriveHealthy:  healthy,
		TaskRecords:      mapTaskRecords(taskRecords),
		TransferPollTime: h.lastTransferPollTime(),
		DownloadPollTime: h.lastDownloadPollTime(),
	}

	body := templates.RenderStatusPage(pageData)
	templates.RenderLayout(w, pageData.Meta, body)
}

func (h *StatusHandler) collectTransferSnapshots(ctx context.Context) []transferSnapshot {
	if h.transfer == nil {
		return nil
	}

	active := h.transfer.GetActiveTransfers()
	if len(active) == 0 {
		return nil
	}

	snapshots := make([]transferSnapshot, 0, len(active))
	for entryID, info := range active {
		entry, err := h.database.GetEntry(ctx, entryID)
		if err != nil {
			continue
		}
		snapshot := transferSnapshot{
			EntryID:       entry.ID,
			Title:         entry.Title,
			TaskID:        info.TaskID,
			Status:        info.LastStatus,
			Error:         info.LastError,
			StartedAt:     info.TransferStartedAt,
			LastCheckedAt: info.LastCheckedAt,
			TaskCreatedAt: info.TaskCreatedAt,
			TaskUpdatedAt: info.TaskUpdatedAt,
			Elapsed:       elapsedSince(info.TransferStartedAt),
		}
		if entry.TargetPath.Valid {
			snapshot.TargetPath = entry.TargetPath.String
		}
		snapshots = append(snapshots, snapshot)
	}

	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].StartedAt.After(snapshots[j].StartedAt)
	})

	return snapshots
}

func (h *StatusHandler) lastTransferPollTime() time.Time {
	if h.transferPollTime == nil {
		return time.Time{}
	}
	return h.transferPollTime()
}

func (h *StatusHandler) lastDownloadPollTime() time.Time {
	if h.downloadPollTime == nil {
		return time.Time{}
	}
	return h.downloadPollTime()
}

func elapsedSince(t time.Time) time.Duration {
	if t.IsZero() {
		return 0
	}
	return time.Since(t)
}

func mapEntrySummaries(entries []*db.Entry) []templates.EntrySummary {
	if len(entries) == 0 {
		return nil
	}
	result := make([]templates.EntrySummary, 0, len(entries))
	for _, entry := range entries {
		summary := templates.EntrySummary{
			ID:        entry.ID,
			Title:     entry.Title,
			Status:    entry.Status,
			MediaType: entry.MediaType,
			Source:    entry.Source,
			UpdatedAt: entry.UpdatedAt,
		}
		if entry.FailedStage.Valid {
			summary.FailedStage = entry.FailedStage.String
		}
		if entry.FailedReason.Valid {
			summary.FailedReason = entry.FailedReason.String
		}
		result = append(result, summary)
	}
	return result
}

func mapStateLogs(logs []*db.StateLog) []templates.StateLogView {
	if len(logs) == 0 {
		return nil
	}
	result := make([]templates.StateLogView, 0, len(logs))
	for _, log := range logs {
		view := templates.StateLogView{
			EntryID:    log.EntryID,
			FromStatus: log.FromStatus,
			ToStatus:   log.ToStatus,
			CreatedAt:  log.CreatedAt,
		}
		if log.Reason.Valid {
			view.Reason = log.Reason.String
		}
		result = append(result, view)
	}
	return result
}

func mapTransferSummaries(transfers []transferSnapshot) []templates.TransferSummary {
	if len(transfers) == 0 {
		return nil
	}
	result := make([]templates.TransferSummary, 0, len(transfers))
	for _, transferInfo := range transfers {
		result = append(result, templates.TransferSummary{
			EntryID:       transferInfo.EntryID,
			Title:         transferInfo.Title,
			TaskID:        transferInfo.TaskID,
			Status:        transferInfo.Status,
			Error:         transferInfo.Error,
			StartedAt:     transferInfo.StartedAt,
			LastCheckedAt: transferInfo.LastCheckedAt,
			TaskCreatedAt: transferInfo.TaskCreatedAt,
			TaskUpdatedAt: transferInfo.TaskUpdatedAt,
			Elapsed:       transferInfo.Elapsed,
			TargetPath:    transferInfo.TargetPath,
		})
	}
	return result
}

func mapTaskRecords(records []monitor.TaskRecord) []templates.TaskRecordView {
	if len(records) == 0 {
		return nil
	}
	result := make([]templates.TaskRecordView, 0, len(records))
	for _, record := range records {
		result = append(result, templates.TaskRecordView{
			Name:         record.Name,
			LastStarted:  record.LastStarted,
			LastFinished: record.LastFinished,
			LastDuration: record.LastDuration,
			LastError:    record.LastError,
		})
	}
	return result
}
