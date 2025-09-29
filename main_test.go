package main

import (
	"testing"
)

func TestSanitizeForFilename(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "clean filename",
			input:    "clean-filename",
			expected: "clean-filename",
		},
		{
			name:     "forward slash",
			input:    "path/to/file",
			expected: "path-to-file",
		},
		{
			name:     "backward slash",
			input:    "path\\to\\file",
			expected: "path-to-file",
		},
		{
			name:     "colon",
			input:    "time:stamp",
			expected: "time-stamp",
		},
		{
			name:     "asterisk",
			input:    "wild*card",
			expected: "wild-card",
		},
		{
			name:     "question mark",
			input:    "what?now",
			expected: "what-now",
		},
		{
			name:     "quotes",
			input:    "quoted\"string",
			expected: "quoted-string",
		},
		{
			name:     "angle brackets",
			input:    "<tag>content</tag>",
			expected: "tag-content--tag",
		},
		{
			name:     "pipe",
			input:    "cmd|grep",
			expected: "cmd-grep",
		},
		{
			name:     "spaces",
			input:    "hello world test",
			expected: "hello-world-test",
		},
		{
			name:     "multiple problematic characters",
			input:    "path/to\\file:with*problems?<yes>|no",
			expected: "path-to-file-with-problems--yes--no",
		},
		{
			name:     "leading and trailing dashes",
			input:    "/path/",
			expected: "path",
		},
		{
			name:     "only problematic characters",
			input:    "///***\\\\\\",
			expected: "",
		},
		{
			name:     "long filename gets truncated",
			input:    "this-is-a-very-long-filename-that-should-be-truncated-because-it-exceeds-the-fifty-character-limit",
			expected: "this-is-a-very-long-filename-that-should-be-trunca",
		},
		{
			name:     "exactly 50 characters",
			input:    "12345678901234567890123456789012345678901234567890",
			expected: "12345678901234567890123456789012345678901234567890",
		},
		{
			name:     "51 characters gets truncated",
			input:    "123456789012345678901234567890123456789012345678901",
			expected: "12345678901234567890123456789012345678901234567890",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeForFilename(tt.input)
			if result != tt.expected {
				t.Errorf("sanitizeForFilename(%q) = %q, want %q", tt.input, result, tt.expected)
			}

			// Additional checks for length constraint
			if len(result) > 50 {
				t.Errorf("sanitizeForFilename(%q) returned string longer than 50 characters: %d", tt.input, len(result))
			}
		})
	}
}

func TestInitializeSessionID(t *testing.T) {
	tests := []struct {
		name          string
		codespaceName string
		expectContains []string
	}{
		{
			name:          "normal codespace name",
			codespaceName: "my-codespace",
			expectContains: []string{"my-codespace", "session", "pid"},
		},
		{
			name:          "empty codespace name",
			codespaceName: "",
			expectContains: []string{"unknown-codespace", "session", "pid"},
		},
		{
			name:          "problematic codespace name",
			codespaceName: "my/codespace\\with:problems",
			expectContains: []string{"my-codespace-with-problems", "session", "pid"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear global sessionID
			sessionID = ""
			
			initializeSessionID(tt.codespaceName)
			
			if sessionID == "" {
				t.Error("sessionID should not be empty after initialization")
			}
			
			// Check that expected components are present
			for _, expected := range tt.expectContains {
				if !containsSubstring(sessionID, expected) {
					t.Errorf("sessionID %q should contain %q", sessionID, expected)
				}
			}
			
			// Check format roughly matches expected pattern
			if len(sessionID) < 20 { // Should be longer than this due to timestamp and PID
				t.Errorf("sessionID %q seems too short", sessionID)
			}
		})
	}
}

// Helper function since strings.Contains might not be available in test context
func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && 
		   func() bool {
			   for i := 0; i <= len(s)-len(substr); i++ {
				   if s[i:i+len(substr)] == substr {
					   return true
				   }
			   }
			   return false
		   }()
}

func TestFormatFileSize(t *testing.T) {
	tests := []struct {
		name     string
		bytes    int64
		expected string
	}{
		{
			name:     "zero bytes",
			bytes:    0,
			expected: "0 B",
		},
		{
			name:     "bytes less than 1KB",
			bytes:    123,
			expected: "123 B",
		},
		{
			name:     "exactly 1KB",
			bytes:    1024,
			expected: "1.0 KB",
		},
		{
			name:     "KB size",
			bytes:    1536, // 1.5 KB
			expected: "1.5 KB",
		},
		{
			name:     "KB with decimal",
			bytes:    2560, // 2.5 KB
			expected: "2.5 KB",
		},
		{
			name:     "exactly 1MB",
			bytes:    1024 * 1024,
			expected: "1.0 MB",
		},
		{
			name:     "MB size",
			bytes:    1024 * 1024 * 2, // 2 MB
			expected: "2.0 MB",
		},
		{
			name:     "MB with decimal",
			bytes:    1024 * 1024 * 1.5, // 1.5 MB
			expected: "1.5 MB",
		},
		{
			name:     "large MB size",
			bytes:    1024 * 1024 * 1024, // 1 GB (but formatted as MB)
			expected: "1024.0 MB",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatFileSize(tt.bytes)
			if result != tt.expected {
				t.Errorf("formatFileSize(%d) = %q, want %q", tt.bytes, result, tt.expected)
			}
		})
	}
}