package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/michealmachine/taro/internal/db"
	"github.com/michealmachine/taro/internal/state"
)

// JellyfinHandler handles Jellyfin webhook notifications
type JellyfinHandler struct {
	database     *db.DB
	stateMachine *state.StateMachine
	logger       *slog.Logger
	// Platform updaters (will be set later)
	onInLibrary func(ctx context.Context, entry *db.Entry) error
}

// NewJellyfinHandler creates a new Jellyfin webhook handler
func NewJellyfinHandler(
	database *db.DB,
	stateMachine *state.StateMachine,
	logger *slog.Logger,
) *JellyfinHandler {
	return &JellyfinHandler{
		database:     database,
		stateMachine: stateMachine,
		logger:       logger,
	}
}

// SetOnInLibraryCallback sets the callback for when an item is added to library
func (h *JellyfinHandler) SetOnInLibraryCallback(callback func(ctx context.Context, entry *db.Entry) error) {
	h.onInLibrary = callback
}

// JellyfinItemAddedPayload represents the webhook payload from Jellyfin
// Users must configure Jellyfin webhook plugin with a custom JSON template that includes:
// {
//   "NotificationType": "{{NotificationType}}",
//   "ItemType": "{{ItemType}}",
//   "Name": "{{Name}}",
//   "Path": "{{Path}}"  // This must be added manually in the template
// }
// Note: Jellyfin webhook plugin doesn't provide Path by default, users need to add it
type JellyfinItemAddedPayload struct {
	NotificationType string `json:"NotificationType"`
	ItemType         string `json:"ItemType"`
	Name             string `json:"Name"`
	Path             string `json:"Path"` // File path on disk
}

// HandleJellyfin handles POST /webhook/jellyfin
func (h *JellyfinHandler) HandleJellyfin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Only accept POST
	if r.Method != "POST" {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Read request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.logger.Error("failed to read request body", "error", err)
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// Parse JSON payload
	var payload JellyfinItemAddedPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		h.logger.Error("failed to parse JSON payload", "error", err, "body", string(body))
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	h.logger.Info("received Jellyfin webhook",
		"notification_type", payload.NotificationType,
		"item_type", payload.ItemType,
		"name", payload.Name,
		"path", payload.Path)

	// Only process ItemAdded notifications
	if payload.NotificationType != "ItemAdded" {
		h.logger.Debug("ignoring non-ItemAdded notification", "type", payload.NotificationType)
		w.WriteHeader(http.StatusOK)
		return
	}

	// Validate Path field exists
	if payload.Path == "" {
		h.logger.Warn("ItemAdded notification missing Path field - please configure Jellyfin webhook template to include Path")
		w.WriteHeader(http.StatusOK)
		return
	}

	// Process the webhook
	if err := h.processItemAdded(ctx, &payload); err != nil {
		h.logger.Error("failed to process ItemAdded webhook",
			"error", err,
			"path", payload.Path)
		// Return 200 OK even on error - Jellyfin unreachability shouldn't affect system
		w.WriteHeader(http.StatusOK)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// processItemAdded processes an ItemAdded webhook notification
func (h *JellyfinHandler) processItemAdded(ctx context.Context, payload *JellyfinItemAddedPayload) error {
	// Query all entries in transferred status
	transferredEntries, err := h.database.ListEntriesByStatus(ctx, string(state.StatusTransferred))
	if err != nil {
		return fmt.Errorf("failed to list transferred entries: %w", err)
	}

	if len(transferredEntries) == 0 {
		h.logger.Debug("no transferred entries to match")
		return nil
	}

	// Normalize webhook path
	webhookPath := normalizePath(payload.Path)

	// Try to match with transferred entries
	for _, entry := range transferredEntries {
		if !entry.TargetPath.Valid || entry.TargetPath.String == "" {
			continue
		}

		entryTargetPath := normalizePath(entry.TargetPath.String)

		// Match using prefix matching (normalized paths)
		if strings.HasPrefix(webhookPath, entryTargetPath) {
			h.logger.Info("matched Jellyfin webhook to entry",
				"entry_id", entry.ID,
				"title", entry.Title,
				"webhook_path", webhookPath,
				"target_path", entryTargetPath)

			// Transition to in_library
			if err := h.stateMachine.Transition(ctx, entry.ID, state.StatusInLibrary, "detected in Jellyfin library"); err != nil {
				return fmt.Errorf("failed to transition to in_library: %w", err)
			}

			// Trigger platform callback if set
			if h.onInLibrary != nil {
				// Fetch the updated entry to pass to callback
				updatedEntry, err := h.database.GetEntry(ctx, entry.ID)
				if err != nil {
					h.logger.Error("failed to fetch updated entry for platform callback",
						"entry_id", entry.ID,
						"error", err)
				} else {
					if err := h.onInLibrary(ctx, updatedEntry); err != nil {
						// Log error but don't fail the webhook processing
						h.logger.Error("platform callback failed",
							"entry_id", entry.ID,
							"error", err)
					}
				}
			}

			// Only match once (first match wins)
			// For TV series with multiple episodes, only the first episode triggers the transition
			return nil
		}
	}

	h.logger.Debug("no matching entry found for webhook path", "path", webhookPath)
	return nil
}

// normalizePath normalizes a file path for comparison
// - Unified `/` separator (replace `\` with `/`)
// - Convert to lowercase for case-insensitive comparison
// - Special characters replaced with `_` (for filesystem compatibility)
// - Remove consecutive `//`
// - Trailing `/`
func normalizePath(inputPath string) string {
	// Replace backslashes with forward slashes
	inputPath = strings.ReplaceAll(inputPath, "\\", "/")

	// Convert to lowercase for case-insensitive comparison
	inputPath = strings.ToLower(inputPath)
	
	// Replace special characters with underscore (filesystem compatibility)
	specialChars := []string{":", "*", "?", "\"", "<", ">", "|"}
	for _, char := range specialChars {
		inputPath = strings.ReplaceAll(inputPath, char, "_")
	}

	// Remove consecutive slashes
	for strings.Contains(inputPath, "//") {
		inputPath = strings.ReplaceAll(inputPath, "//", "/")
	}

	// Ensure trailing slash
	if !strings.HasSuffix(inputPath, "/") {
		inputPath += "/"
	}

	return inputPath
}
