package handlers

import (
	"net/http"
	"sort"
	"time"

	"github.com/michealmachine/taro/internal/db"
	"github.com/michealmachine/taro/internal/web/templates"
)

type EntryDetailHandler struct {
	database *db.DB
}

func NewEntryDetailHandler(database *db.DB) *EntryDetailHandler {
	return &EntryDetailHandler{database: database}
}

func (h *EntryDetailHandler) HandleEntryDetail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	entryID := r.PathValue("id")
	if entryID == "" {
		http.Error(w, "entry id is required", http.StatusBadRequest)
		return
	}

	entry, err := h.database.GetEntry(ctx, entryID)
	if err != nil {
		http.Error(w, "entry not found", http.StatusNotFound)
		return
	}

	resources, _ := h.database.ListResourcesByEntry(ctx, entryID)
	logs, _ := h.database.ListStateLogsByEntry(ctx, entryID)

	resourceViews := make([]templates.ResourceView, 0, len(resources))
	for _, res := range resources {
		resourceViews = append(resourceViews, templates.ResourceView{
			ID:             res.ID,
			Title:          res.Title,
			Size:           res.Size.Int64,
			Seeders:        res.Seeders.Int64,
			Resolution:     res.Resolution.String,
			Codec:          res.Codec.String,
			Indexer:        res.Indexer.String,
			Eligible:       res.Eligible,
			Selected:       res.Selected,
			RejectedReason: res.RejectedReason.String,
		})
	}

	sort.Slice(resourceViews, func(i, j int) bool {
		if resourceViews[i].Selected {
			return true
		}
		if resourceViews[j].Selected {
			return false
		}
		return resourceViews[i].Title < resourceViews[j].Title
	})

	logViews := make([]templates.StateLogView, 0, len(logs))
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
		logViews = append(logViews, view)
	}

	page := templates.EntryDetailPageData{
		Meta: templates.PageMeta{
			Title: "Entry · taro",
			Path:  "/entries",
		},
		Entry: templates.EntryDetailView{
			ID:        entry.ID,
			Title:     entry.Title,
			Status:    entry.Status,
			MediaType: entry.MediaType,
			Source:    entry.Source,
			SourceID:  entry.SourceID,
			Season:    entry.Season,
			AskMode:   entry.AskMode,
			CreatedAt: entry.CreatedAt,
			UpdatedAt: entry.UpdatedAt,
		},
		Resources: resourceViews,
		Logs:      logViews,
		Now:       time.Now(),
	}

	if entry.Year.Valid {
		page.Entry.Year = int(entry.Year.Int64)
	}
	if entry.Resolution.Valid {
		page.Entry.Resolution = entry.Resolution.String
	}
	if entry.SelectedResourceID.Valid {
		page.Entry.SelectedResourceID = entry.SelectedResourceID.String
	}
	if entry.SearchStartedAt.Valid {
		page.Entry.SearchStartedAt = entry.SearchStartedAt.Time
	}
	if entry.DownloadStartedAt.Valid {
		page.Entry.DownloadStartedAt = entry.DownloadStartedAt.Time
	}
	if entry.TransferStartedAt.Valid {
		page.Entry.TransferStartedAt = entry.TransferStartedAt.Time
	}
	if entry.PikPakTaskID.Valid {
		page.Entry.PikPakTaskID = entry.PikPakTaskID.String
	}
	if entry.PikPakFileID.Valid {
		page.Entry.PikPakFileID = entry.PikPakFileID.String
	}
	if entry.PikPakFilePath.Valid {
		page.Entry.PikPakFilePath = entry.PikPakFilePath.String
	}
	if entry.TransferTaskID.Valid {
		page.Entry.TransferTaskID = entry.TransferTaskID.String
	}
	if entry.TargetPath.Valid {
		page.Entry.TargetPath = entry.TargetPath.String
	}
	if entry.FailedStage.Valid {
		page.Entry.FailedStage = entry.FailedStage.String
	}
	if entry.FailedReason.Valid {
		page.Entry.FailedReason = entry.FailedReason.String
	}
	if entry.FailureKind.Valid {
		page.Entry.FailureKind = entry.FailureKind.String
	}
	if entry.FailureCode.Valid {
		page.Entry.FailureCode = entry.FailureCode.String
	}
	if entry.FailedAt.Valid {
		page.Entry.FailedAt = entry.FailedAt.Time
	}

	body := templates.RenderEntryDetailPage(page)
	templates.RenderLayout(w, page.Meta, body)
}
