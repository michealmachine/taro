package handlers

import (
	"encoding/json"
	"log/slog"
	"net/http"

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
	ID int64 `json:"id"`
}

// HandleAddEntry handles POST /entries
func (h *EntriesHandler) HandleAddEntry(w http.ResponseWriter, r *http.Request) {
	var req AddEntryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logger.Error("failed to decode request", "error", err)
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
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

	// Call ActionService to add entry
	entryID, err := h.actionService.AddEntry(r.Context(), req.Title, req.MediaType, req.Year, req.Season)
	if err != nil {
		h.logger.Error("failed to add entry", "error", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Return success response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(AddEntryResponse{ID: entryID})
}
