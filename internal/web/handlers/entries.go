package handlers

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/michealmachine/taro/internal/service"
)

// EntriesHandler handles entry-related HTTP requests
type EntriesHandler struct {
	actionService *service.ActionService
	logger        *slog.Logger
}

// NewEntriesHandler creates a new entries handler
func NewEntriesHandler(actionService *service.ActionService, logger *slog.Logger) *EntriesHandler {
	return &EntriesHandler{
		actionService: actionService,
		logger:        logger,
	}
}

// AddEntryRequest represents the request body for adding a new entry
type AddEntryRequest struct {
	Title     string `json:"title"`
	MediaType string `json:"media_type"`
	Year      int    `json:"year,omitempty"`
	Season    int    `json:"season,omitempty"`
}

// AddEntryResponse represents the response for adding a new entry
type AddEntryResponse struct {
	ID string `json:"id"`
}

// HandleAddEntry handles POST /entries
func (h *EntriesHandler) HandleAddEntry(w http.ResponseWriter, r *http.Request) {
	var req AddEntryRequest
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			h.logger.Error("failed to decode request", "error", err)
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
	} else {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}
		req.Title = r.FormValue("title")
		req.MediaType = r.FormValue("media_type")
		req.Year = parseInt(r.FormValue("year"))
		season := parseInt(r.FormValue("season"))
		if season == 0 {
			season = 1
		}
		req.Season = season
	}

	// Validate required fields
	if req.Title == "" {
		http.Error(w, "title is required", http.StatusBadRequest)
		return
	}
	if req.MediaType == "" {
		http.Error(w, "media_type is required", http.StatusBadRequest)
		return
	}
	if req.MediaType == "movie" {
		req.Season = 0
	} else if req.Season == 0 {
		req.Season = 1
	}

	// Call ActionService to add entry
	entryID, err := h.actionService.AddEntry(r.Context(), req.Title, req.MediaType, req.Year, req.Season)
	if err != nil {
		h.logger.Error("failed to add entry", "error", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Return success response
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(AddEntryResponse{ID: entryID})
		return
	}
	redirectBack(w, r, "/entries/"+entryID)
}

// HandleRetry handles POST /entries/{id}/retry
func (h *EntriesHandler) HandleRetry(w http.ResponseWriter, r *http.Request) {
	entryID := r.PathValue("id")
	if entryID == "" {
		http.Error(w, "entry id is required", http.StatusBadRequest)
		return
	}

	if err := h.actionService.RetryEntry(r.Context(), entryID); err != nil {
		h.logger.Error("failed to retry entry", "entry_id", entryID, "error", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	redirectBack(w, r, "/entries/"+entryID)
}

// HandleCancel handles POST /entries/{id}/cancel
func (h *EntriesHandler) HandleCancel(w http.ResponseWriter, r *http.Request) {
	entryID := r.PathValue("id")
	if entryID == "" {
		http.Error(w, "entry id is required", http.StatusBadRequest)
		return
	}

	if err := h.actionService.CancelEntry(r.Context(), entryID); err != nil {
		h.logger.Error("failed to cancel entry", "entry_id", entryID, "error", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	redirectBack(w, r, "/entries/"+entryID)
}

// HandleSelect handles POST /entries/{id}/select
func (h *EntriesHandler) HandleSelect(w http.ResponseWriter, r *http.Request) {
	entryID := r.PathValue("id")
	if entryID == "" {
		http.Error(w, "entry id is required", http.StatusBadRequest)
		return
	}

	resourceID := r.FormValue("resource_id")
	if resourceID == "" {
		http.Error(w, "resource_id is required", http.StatusBadRequest)
		return
	}

	if err := h.actionService.SelectResource(r.Context(), entryID, resourceID); err != nil {
		h.logger.Error("failed to select resource", "entry_id", entryID, "resource_id", resourceID, "error", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	redirectBack(w, r, "/entries/"+entryID)
}

func redirectBack(w http.ResponseWriter, r *http.Request, fallback string) {
	location := r.Referer()
	if location == "" {
		location = fallback
	}
	http.Redirect(w, r, location, http.StatusSeeOther)
}

func parseInt(value string) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return parsed
}
