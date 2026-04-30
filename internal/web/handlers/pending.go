package handlers

import (
	"net/http"
	"sort"

	"github.com/michealmachine/taro/internal/db"
	"github.com/michealmachine/taro/internal/web/templates"
)

type PendingHandler struct {
	database *db.DB
}

func NewPendingHandler(database *db.DB) *PendingHandler {
	return &PendingHandler{database: database}
}

func (h *PendingHandler) HandlePending(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	entries, _ := h.database.ListEntriesByStatus(ctx, "needs_selection")

	items := make([]templates.PendingEntryView, 0, len(entries))
	for _, entry := range entries {
		resources, _ := h.database.ListResourcesByEntry(ctx, entry.ID)
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
			if resourceViews[i].Eligible != resourceViews[j].Eligible {
				return resourceViews[i].Eligible
			}
			return resourceViews[i].Title < resourceViews[j].Title
		})

		items = append(items, templates.PendingEntryView{
			ID:        entry.ID,
			Title:     entry.Title,
			MediaType: entry.MediaType,
			Source:    entry.Source,
			Resources: resourceViews,
		})
	}

	page := templates.PendingPageData{
		Meta: templates.PageMeta{
			Title: "Pending · taro",
			Path:  "/pending",
		},
		Entries: items,
	}

	body := templates.RenderPendingPage(page)
	templates.RenderLayout(w, page.Meta, body)
}
