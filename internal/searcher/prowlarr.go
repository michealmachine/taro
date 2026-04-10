package searcher

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/michealmachine/taro/internal/config"
	"github.com/michealmachine/taro/internal/db"
	"github.com/michealmachine/taro/internal/state"
)

// resolutionPriority defines the priority order for resolution selection
var resolutionPriority = map[string]int{
	"1080p": 4,
	"1080i": 3,
	"720p":  2,
	"480p":  1,
	"other": 0,
}

// resolutionRegex matches resolution patterns in titles
var resolutionRegex = regexp.MustCompile(`(?i)(1080p|1080i|720p|480p)`)

// codecRegex matches codec patterns in titles
var codecRegex = regexp.MustCompile(`(?i)(av1|avc|x264|x265|hevc|h\.?264|h\.?265)`)

// SearchResult represents a search result from Prowlarr
type SearchResult struct {
	ID         string
	Title      string
	Magnet     string
	Size       int64
	Seeders    int
	Resolution string
	Indexer    string
}

// ProwlarrSearchResponse represents the Prowlarr API response
type ProwlarrSearchResponse []ProwlarrSearchResult

// ProwlarrSearchResult represents a single search result from Prowlarr
type ProwlarrSearchResult struct {
	Title       string `json:"title"`
	GUID        string `json:"guid"`
	DownloadURL string `json:"downloadUrl"`
	MagnetURL   string `json:"magnetUrl"`
	Size        int64  `json:"size"`
	Seeders     int    `json:"seeders"`
	IndexerID   int    `json:"indexerId"`
	Indexer     string `json:"indexer"`
}

// Searcher handles resource searching via Prowlarr
type Searcher struct {
	prowlarrURL    string
	apiKey         string
	database       *db.DB
	sm             *state.StateMachine
	client         *http.Client
	logger         *slog.Logger
	excludedCodecs []string // Codecs to exclude from results
	config         *config.Config
}

// NewSearcher creates a new Prowlarr searcher
func NewSearcher(cfg *config.Config, database *db.DB, sm *state.StateMachine, logger *slog.Logger) *Searcher {
	return &Searcher{
		prowlarrURL: cfg.Prowlarr.URL,
		apiKey:      cfg.Prowlarr.APIKey,
		database:    database,
		sm:          sm,
		client: &http.Client{
			Timeout: 60 * time.Second, // Prowlarr searches can take longer
		},
		logger:         logger,
		excludedCodecs: cfg.Defaults.ExcludedCodecs,
		config:         cfg,
	}
}

// Search searches for resources for the given entry
func (s *Searcher) Search(ctx context.Context, entry *db.Entry) error {
	s.logger.Info("starting search", "entry_id", entry.ID, "title", entry.Title)

	// Transition to searching state
	if err := s.sm.Transition(ctx, entry.ID, state.StatusSearching, "starting resource search"); err != nil {
		return fmt.Errorf("failed to transition to searching: %w", err)
	}

	// Build search query
	query := s.buildSearchQuery(entry)
	s.logger.Info("search query", "entry_id", entry.ID, "query", query)

	// Call Prowlarr API
	results, err := s.searchProwlarr(ctx, query)
	if err != nil {
		// Transition to failed on search error
		failErr := s.sm.TransitionWithUpdate(ctx, entry.ID, state.StatusFailed, map[string]any{
			"failed_stage":  "searching",
			"failed_reason": fmt.Sprintf("prowlarr search failed: %v", err),
			"reason":        "search failed",
		})
		if failErr != nil {
			s.logger.Error("failed to transition to failed state", "error", failErr)
		}
		return fmt.Errorf("prowlarr search failed: %w", err)
	}

	s.logger.Info("search completed", "entry_id", entry.ID, "results_count", len(results))

	// Handle no results
	if len(results) == 0 {
		if err := s.sm.TransitionWithUpdate(ctx, entry.ID, state.StatusFailed, map[string]any{
			"failed_stage":  "searching",
			"failed_reason": "no search results found",
			"reason":        "no results",
		}); err != nil {
			return fmt.Errorf("failed to transition to failed: %w", err)
		}
		return nil
	}

	// Parse resolutions and create resource records
	resources := make([]*db.Resource, 0, len(results))
	for _, result := range results {
		resolution := s.extractResolution(result.Title)
		codec := s.extractCodec(result.Title)

		// Filter out excluded codecs
		if s.isCodecExcluded(codec) {
			s.logger.Info("skipping resource with excluded codec",
				"entry_id", entry.ID,
				"title", result.Title,
				"codec", codec)
			continue
		}

		resource := &db.Resource{
			EntryID:    entry.ID,
			Title:      result.Title,
			Magnet:     result.Magnet,
			Size:       sql.NullInt64{Int64: result.Size, Valid: result.Size > 0},
			Seeders:    sql.NullInt64{Int64: int64(result.Seeders), Valid: true},
			Resolution: sql.NullString{String: resolution, Valid: resolution != ""},
			Indexer:    sql.NullString{String: result.Indexer, Valid: result.Indexer != ""},
		}
		resources = append(resources, resource)
	}

	// Check if all resources were filtered out
	if len(resources) == 0 {
		if err := s.sm.TransitionWithUpdate(ctx, entry.ID, state.StatusFailed, map[string]any{
			"failed_stage":  "searching",
			"failed_reason": "all resources filtered by codec exclusion",
			"reason":        "no suitable resources",
		}); err != nil {
			return fmt.Errorf("failed to transition to failed: %w", err)
		}
		return nil
	}

	// Save resources to database
	if err := s.database.BatchCreateResources(ctx, resources); err != nil {
		return fmt.Errorf("failed to save resources: %w", err)
	}

	s.logger.Info("saved resources", "entry_id", entry.ID, "count", len(resources))

	// Decide next state based on ask mode
	if err := s.decideNextState(ctx, entry, resources); err != nil {
		return fmt.Errorf("failed to decide next state: %w", err)
	}

	return nil
}

// buildSearchQuery constructs the search query based on entry type
func (s *Searcher) buildSearchQuery(entry *db.Entry) string {
	switch entry.MediaType {
	case "anime":
		// For Bangumi anime, don't add season suffix as Bangumi treats each season as a separate entry
		// The title already contains season information if applicable (e.g., "進撃の巨人 Season 3")
		if entry.Source == "bangumi" {
			return entry.Title
		}
		// For other sources (like Trakt), add season suffix
		return fmt.Sprintf("%s S%02d", entry.Title, entry.Season)
	case "tv":
		// TV shows always use season format
		return fmt.Sprintf("%s S%02d", entry.Title, entry.Season)
	case "movie":
		// Format: "{title} {year}" if year is available
		if entry.Year.Valid && entry.Year.Int64 > 0 {
			return fmt.Sprintf("%s %d", entry.Title, entry.Year.Int64)
		}
		return entry.Title
	default:
		return entry.Title
	}
}

// searchProwlarr calls the Prowlarr search API
func (s *Searcher) searchProwlarr(ctx context.Context, query string) ([]SearchResult, error) {
	// Build URL with query parameters
	u, err := url.Parse(fmt.Sprintf("%s/api/v1/search", s.prowlarrURL))
	if err != nil {
		return nil, fmt.Errorf("failed to parse prowlarr URL: %w", err)
	}

	q := u.Query()
	q.Set("query", query)
	q.Set("type", "search")
	u.RawQuery = q.Encode()

	// Create request
	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("X-Api-Key", s.apiKey)
	req.Header.Set("Accept", "application/json")

	// Execute request
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}

	// Parse response
	var prowlarrResults ProwlarrSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&prowlarrResults); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Convert to internal format
	results := make([]SearchResult, 0, len(prowlarrResults))
	for _, pr := range prowlarrResults {
		// Prefer magnet URL, fallback to download URL
		magnetURL := pr.MagnetURL
		if magnetURL == "" {
			magnetURL = pr.DownloadURL
		}

		if magnetURL == "" {
			s.logger.Warn("skipping result without download URL", "title", pr.Title)
			continue
		}

		results = append(results, SearchResult{
			ID:      pr.GUID,
			Title:   pr.Title,
			Magnet:  magnetURL,
			Size:    pr.Size,
			Seeders: pr.Seeders,
			Indexer: pr.Indexer,
		})
	}

	return results, nil
}

// extractResolution extracts resolution from title using regex
func (s *Searcher) extractResolution(title string) string {
	matches := resolutionRegex.FindStringSubmatch(title)
	if len(matches) > 1 {
		// Normalize to lowercase
		return matches[1]
	}
	return "other"
}

// extractCodec extracts codec from title using regex
func (s *Searcher) extractCodec(title string) string {
	matches := codecRegex.FindStringSubmatch(title)
	if len(matches) > 1 {
		codec := strings.ToLower(matches[1])
		// Normalize codec names
		switch codec {
		case "h.264", "h264", "avc":
			return "x264"
		case "h.265", "h265", "hevc":
			return "x265"
		default:
			return codec
		}
	}
	return "unknown"
}

// isCodecExcluded checks if a codec is in the exclusion list
func (s *Searcher) isCodecExcluded(codec string) bool {
	if len(s.excludedCodecs) == 0 {
		return false
	}

	codecLower := strings.ToLower(codec)
	for _, excluded := range s.excludedCodecs {
		if strings.ToLower(excluded) == codecLower {
			return true
		}
	}
	return false
}

// decideNextState decides the next state based on ask mode and available resources
func (s *Searcher) decideNextState(ctx context.Context, entry *db.Entry, resources []*db.Resource) error {
	// Determine ask mode (0=global, 1=force ask, 2=force auto)
	askMode := entry.AskMode

	shouldAsk := false
	if askMode == 1 {
		// Force ask
		shouldAsk = true
	} else if askMode == 0 {
		// Use global config
		// Note: config.Defaults.AskMode is a bool where true = ask, false = auto
		shouldAsk = s.config.Defaults.AskMode
	}
	// askMode == 2 means force auto, shouldAsk remains false

	if shouldAsk {
		// Transition to needs_selection
		return s.sm.Transition(ctx, entry.ID, state.StatusNeedsSelection, "multiple resources found, user selection required")
	}

	// Auto-select best resource
	best := s.selectBestResource(entry, resources)
	if best == nil {
		return fmt.Errorf("no suitable resource found")
	}

	s.logger.Info("auto-selected resource", "entry_id", entry.ID, "resource_id", best.ID, "resolution", best.Resolution)

	// Transition to found with selected resource
	return s.sm.TransitionWithUpdate(ctx, entry.ID, state.StatusFound, map[string]any{
		"selected_resource_id": best.ID,
		"resolution":           best.Resolution,
		"reason":               "auto-selected best resource",
	})
}

// selectBestResource selects the best resource based on resolution priority and seeders
func (s *Searcher) selectBestResource(entry *db.Entry, resources []*db.Resource) *db.Resource {
	if len(resources) == 0 {
		return nil
	}

	// Get preferred resolution (entry-level override or global default)
	preferredResolution := entry.Resolution.String
	if preferredResolution == "" {
		// Use global default from config
		preferredResolution = s.config.Defaults.Resolution
		if preferredResolution == "" {
			preferredResolution = "1080p" // Fallback default
		}
	}

	// Sort resources by priority
	sort.Slice(resources, func(i, j int) bool {
		resI := resources[i].Resolution.String
		resJ := resources[j].Resolution.String

		// First priority: exact match with preferred resolution
		matchI := (resI == preferredResolution)
		matchJ := (resJ == preferredResolution)

		if matchI != matchJ {
			return matchI // Preferred resolution comes first
		}

		// Second priority: resolution quality (if neither matches preferred)
		priI := resolutionPriority[resI]
		priJ := resolutionPriority[resJ]

		if priI != priJ {
			return priI > priJ
		}

		// Third priority: more seeders
		return resources[i].Seeders.Int64 > resources[j].Seeders.Int64
	})

	// Return the best resource
	return resources[0]
}
