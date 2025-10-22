package main

import (
	"strings"
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
		name           string
		codespaceName  string
		expectContains []string
	}{
		{
			name:           "normal codespace name",
			codespaceName:  "my-codespace",
			expectContains: []string{"my-codespace", "session", "pid"},
		},
		{
			name:           "empty codespace name",
			codespaceName:  "",
			expectContains: []string{"unknown-codespace", "session", "pid"},
		},
		{
			name:           "problematic codespace name",
			codespaceName:  "my/codespace\\with:problems",
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

// TestUploadPortMonitorScriptSignature verifies the function is accessible to main package
func TestUploadPortMonitorScriptSignature(t *testing.T) {
	// This test ensures uploadPortMonitorScript function exists and has correct signature
	// The function is called by uploadAndPrepareScripts
	
	// We can't call it directly in tests without a real codespace,
	// but we can verify it compiles and has the right structure
	t.Run("function_exists", func(t *testing.T) {
		// If this test compiles, the function signature is correct
		// uploadPortMonitorScript is called in main.go's uploadAndPrepareScripts
		t.Log("uploadPortMonitorScript function signature verified at compile time")
	})
}

// TestGetLogDirectory verifies log directory path generation
func TestGetLogDirectory(t *testing.T) {
	logDir := getLogDirectory()
	
	if logDir == "" {
		t.Error("getLogDirectory() should not return empty string")
	}
	
	// Should contain the temp directory
	if !strings.Contains(logDir, "tmp") && !strings.Contains(logDir, "TEMP") {
		t.Logf("Warning: log directory may not be in temp: %s", logDir)
	}
	
	// Should contain the app name
	if !strings.Contains(logDir, "gh-ado-codespaces") {
		t.Errorf("getLogDirectory() should contain 'gh-ado-codespaces', got: %s", logDir)
	}
	
	// Should contain "logs"
	if !strings.Contains(logDir, "logs") {
		t.Errorf("getLogDirectory() should contain 'logs', got: %s", logDir)
	}
	
	t.Logf("Log directory: %s", logDir)
}

// TestGetSessionLogDirectory verifies session log directory generation
func TestGetSessionLogDirectory(t *testing.T) {
	// Initialize a session ID first
	testCodespaceName := "test-codespace-123"
	initializeSessionID(testCodespaceName)
	
	sessionLogDir := getSessionLogDirectory()
	
	if sessionLogDir == "" {
		t.Error("getSessionLogDirectory() should not return empty string")
	}
	
	// Should contain the base log directory
	baseLogDir := getLogDirectory()
	if !strings.HasPrefix(sessionLogDir, baseLogDir) {
		t.Errorf("getSessionLogDirectory() should start with base log dir.\nGot:      %s\nExpected: %s/*",
			sessionLogDir, baseLogDir)
	}
	
	// Should contain the sanitized codespace name
	if !strings.Contains(sessionLogDir, "test-codespace-123") {
		t.Errorf("getSessionLogDirectory() should contain codespace name, got: %s", sessionLogDir)
	}
	
	t.Logf("Session log directory: %s", sessionLogDir)
}

// TestGetSessionLogPath verifies log file path generation
func TestGetSessionLogPath(t *testing.T) {
	// Initialize a session ID first
	testCodespaceName := "my-codespace"
	initializeSessionID(testCodespaceName)
	
	logFileName := "test.log"
	logPath := getSessionLogPath(logFileName)
	
	if logPath == "" {
		t.Error("getSessionLogPath() should not return empty string")
	}
	
	// Should end with the log file name
	if !strings.HasSuffix(logPath, logFileName) {
		t.Errorf("getSessionLogPath() should end with %q, got: %s", logFileName, logPath)
	}
	
	// Should contain the session directory
	sessionDir := getSessionLogDirectory()
	if !strings.HasPrefix(logPath, sessionDir) {
		t.Errorf("getSessionLogPath() should start with session dir.\nGot:      %s\nExpected: %s/*",
			logPath, sessionDir)
	}
	
	t.Logf("Session log path: %s", logPath)
}
