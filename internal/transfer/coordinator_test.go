package transfer

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
			input:    "/Media/Anime/Title/",
			expected: "/media/anime/title/",
		},
		{
			name:     "unix path without trailing slash",
			input:    "/Media/Anime/Title",
			expected: "/media/anime/title/",
		},
		{
			name:     "windows path",
			input:    "\\Media\\Anime\\Title",
			expected: "/media/anime/title/",
		},
		{
			name:     "mixed separators",
			input:    "/Media\\Anime/Title",
			expected: "/media/anime/title/",
		},
		{
			name:     "special characters",
			input:    "/Media/Anime/Title: Season 1?/",
			expected: "/media/anime/title_ season 1_/",
		},
		{
			name:     "consecutive slashes",
			input:    "/Media//Anime///Title/",
			expected: "/media/anime/title/",
		},
		{
			name:     "all special chars",
			input:    `/Media/Test:*?"<>|/`,
			expected: "/media/test_______/",
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

func TestGenerateTargetPath(t *testing.T) {
	// This test would require mocking the config and database
	// For now, we'll test the normalizePath function which is the core logic
	// Full integration tests should be added when implementing the main service
}
