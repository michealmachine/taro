package webhook

import (
	"testing"
)

func TestNormalizePath(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "unix path with trailing slash",
			input:    "/mnt/media/Anime/Title/",
			expected: "/mnt/media/anime/title/",
		},
		{
			name:     "unix path without trailing slash",
			input:    "/mnt/media/Anime/Title",
			expected: "/mnt/media/anime/title/",
		},
		{
			name:     "windows UNC path",
			input:    "\\\\server\\media\\Anime\\Title",
			expected: "/server/media/anime/title/",
		},
		{
			name:     "mixed separators",
			input:    "/mnt/media\\Anime/Title",
			expected: "/mnt/media/anime/title/",
		},
		{
			name:     "consecutive slashes",
			input:    "/mnt//media///Anime/Title/",
			expected: "/mnt/media/anime/title/",
		},
		{
			name:     "uppercase to lowercase",
			input:    "/MNT/MEDIA/ANIME/TITLE/",
			expected: "/mnt/media/anime/title/",
		},
		{
			name:     "mixed case",
			input:    "/Mnt/MeDia/AnImE/TiTlE",
			expected: "/mnt/media/anime/title/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizePath(tt.input)
			if result != tt.expected {
				t.Errorf("normalizePath(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestPathMatching(t *testing.T) {
	tests := []struct {
		name         string
		webhookPath  string
		targetPath   string
		shouldMatch  bool
	}{
		{
			name:        "exact match",
			webhookPath: "/media/anime/title/season 1/",
			targetPath:  "/media/anime/title/season 1/",
			shouldMatch: true,
		},
		{
			name:        "webhook path is subdirectory",
			webhookPath: "/media/anime/title/season 1/episode1.mkv",
			targetPath:  "/media/anime/title/season 1/",
			shouldMatch: true,
		},
		{
			name:        "case insensitive match",
			webhookPath: "/MEDIA/ANIME/TITLE/SEASON 1/episode1.mkv",
			targetPath:  "/media/anime/title/season 1/",
			shouldMatch: true,
		},
		{
			name:        "different paths",
			webhookPath: "/media/movies/title/",
			targetPath:  "/media/anime/title/",
			shouldMatch: false,
		},
		{
			name:        "partial name match should not match",
			webhookPath: "/media/anime/title2/season 1/",
			targetPath:  "/media/anime/title/season 1/",
			shouldMatch: false,
		},
		{
			name:        "windows vs unix paths",
			webhookPath: "\\\\server\\media\\anime\\title\\season 1\\episode1.mkv",
			targetPath:  "/server/media/anime/title/season 1/",
			shouldMatch: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			normalizedWebhook := normalizePath(tt.webhookPath)
			normalizedTarget := normalizePath(tt.targetPath)

			// Use HasPrefix for matching (same logic as in processItemAdded)
			matched := len(normalizedWebhook) >= len(normalizedTarget) &&
				normalizedWebhook[:len(normalizedTarget)] == normalizedTarget

			if matched != tt.shouldMatch {
				t.Errorf("path matching failed:\n  webhook: %q\n  target:  %q\n  normalized webhook: %q\n  normalized target:  %q\n  got match: %v, want: %v",
					tt.webhookPath, tt.targetPath,
					normalizedWebhook, normalizedTarget,
					matched, tt.shouldMatch)
			}
		})
	}
}
