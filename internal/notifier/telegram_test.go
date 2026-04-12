package notifier

import (
	"strings"
	"testing"
)

func TestEscapeMarkdown(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "no special characters",
			input:    "Hello World",
			expected: "Hello World",
		},
		{
			name:     "underscore",
			input:    "Hello_World",
			expected: "Hello\\_World",
		},
		{
			name:     "asterisk",
			input:    "Hello*World",
			expected: "Hello\\*World",
		},
		{
			name:     "brackets",
			input:    "Hello[World]",
			expected: "Hello\\[World]",
		},
		{
			name:     "backtick",
			input:    "Hello`World",
			expected: "Hello\\`World",
		},
		{
			name:     "multiple special characters",
			input:    "Hello_World*Test[123]",
			expected: "Hello\\_World\\*Test\\[123]",
		},
		{
			name:     "path with slashes",
			input:    "/media/anime/show.mkv",
			expected: "/media/anime/show.mkv",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := escapeMarkdown(tt.input)
			if result != tt.expected {
				t.Errorf("escapeMarkdown() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestParseCallbackData(t *testing.T) {
	tests := []struct {
		name              string
		data              string
		wantAction        string
		wantEntryID       string
		wantResourceIndex int
		wantErr           bool
	}{
		{
			name:              "select action with resource index",
			data:              "select:entry-123:2",
			wantAction:        "select",
			wantEntryID:       "entry-123",
			wantResourceIndex: 2,
			wantErr:           false,
		},
		{
			name:              "cancel action without resource index",
			data:              "cancel:entry-456",
			wantAction:        "cancel",
			wantEntryID:       "entry-456",
			wantResourceIndex: 0,
			wantErr:           false,
		},
		{
			name:              "retry action",
			data:              "retry:entry-789",
			wantAction:        "retry",
			wantEntryID:       "entry-789",
			wantResourceIndex: 0,
			wantErr:           false,
		},
		{
			name:              "invalid format - missing parts",
			data:              "select",
			wantAction:        "",
			wantEntryID:       "",
			wantResourceIndex: 0,
			wantErr:           true,
		},
		{
			name:              "invalid resource index",
			data:              "select:entry-123:invalid",
			wantAction:        "",
			wantEntryID:       "",
			wantResourceIndex: 0,
			wantErr:           true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			action, entryID, resourceIndex, err := ParseCallbackData(tt.data)

			if (err != nil) != tt.wantErr {
				t.Errorf("ParseCallbackData() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				if action != tt.wantAction {
					t.Errorf("ParseCallbackData() action = %v, want %v", action, tt.wantAction)
				}
				if entryID != tt.wantEntryID {
					t.Errorf("ParseCallbackData() entryID = %v, want %v", entryID, tt.wantEntryID)
				}
				if resourceIndex != tt.wantResourceIndex {
					t.Errorf("ParseCallbackData() resourceIndex = %v, want %v", resourceIndex, tt.wantResourceIndex)
				}
			}
		})
	}
}

func TestStringsReplaceAll(t *testing.T) {
	// Test that strings.ReplaceAll works as expected
	tests := []struct {
		name     string
		s        string
		old      string
		new      string
		expected string
	}{
		{
			name:     "simple replacement",
			s:        "hello world",
			old:      "world",
			new:      "golang",
			expected: "hello golang",
		},
		{
			name:     "multiple occurrences",
			s:        "test test test",
			old:      "test",
			new:      "demo",
			expected: "demo demo demo",
		},
		{
			name:     "no match",
			s:        "hello world",
			old:      "foo",
			new:      "bar",
			expected: "hello world",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := strings.ReplaceAll(tt.s, tt.old, tt.new)
			if result != tt.expected {
				t.Errorf("strings.ReplaceAll() = %q, want %q", result, tt.expected)
			}
		})
	}
}
