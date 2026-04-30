package handlers

import (
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/michealmachine/taro/internal/db"
	"github.com/michealmachine/taro/internal/web/templates"
)

type EntriesListHandler struct {
	database *db.DB
}

func NewEntriesListHandler(database *db.DB) *EntriesListHandler {
	return &EntriesListHandler{database: database}
}

func (h *EntriesListHandler) HandleEntries(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	statusFilter := strings.TrimSpace(r.URL.Query().Get("status"))
	if statusFilter == "all" {
		statusFilter = ""
	}
	sourceFilter := strings.TrimSpace(r.URL.Query().Get("source"))

	filters := map[string]interface{}{}
	if statusFilter != "" {
		filters["status"] = statusFilter
	}
	if sourceFilter != "" {
		filters["source"] = sourceFilter
	}

	entries, _ := h.database.ListEntries(ctx, filters)
	viewEntries := make([]templates.EntryListItem, 0, len(entries))
	for _, entry := range entries {
		item := templates.EntryListItem{
			ID:        entry.ID,
			Title:     entry.Title,
			Status:    entry.Status,
			MediaType: entry.MediaType,
			Source:    entry.Source,
			Season:    entry.Season,
			UpdatedAt: entry.UpdatedAt,
			CreatedAt: entry.CreatedAt,
		}
		if entry.Year.Valid {
			item.Year = int(entry.Year.Int64)
		}
		if entry.FailedStage.Valid {
			item.FailedStage = entry.FailedStage.String
		}
		if entry.FailedReason.Valid {
			item.FailedReason = entry.FailedReason.String
		}
		if entry.SelectedResourceID.Valid {
			item.SelectedResourceID = entry.SelectedResourceID.String
		}
		if entry.TargetPath.Valid {
			item.TargetPath = entry.TargetPath.String
		}
		viewEntries = append(viewEntries, item)
	}

	sort.Slice(viewEntries, func(i, j int) bool {
		return viewEntries[i].UpdatedAt.After(viewEntries[j].UpdatedAt)
	})

	page := templates.EntriesPageData{
		Meta: templates.PageMeta{
			Title: "Entries · taro",
			Path:  "/entries",
		},
		Entries:      viewEntries,
		StatusFilter: statusFilter,
		SourceFilter: sourceFilter,
		Now:          time.Now(),
	}

	body := templates.RenderEntriesPage(page)
	templates.RenderLayout(w, page.Meta, body)
}
