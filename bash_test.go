package main

import (
	"testing"
)

func TestQuoteForShell(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "empty string",
			input:    "",
			expected: "''",
		},
		{
			name:     "simple command",
			input:    "echo hello",
			expected: "'echo hello'",
		},
		{
			name:     "command with single quotes",
			input:    "echo 'hello world'",
			expected: "'echo '\"'\"'hello world'\"'\"''",
		},
		{
			name:     "command with double quotes",
			input:    `echo "hello world"`,
			expected: `'echo "hello world"'`,
		},
		{
			name:     "command with semicolons",
			input:    "cmd1; cmd2",
			expected: "'cmd1; cmd2'",
		},
		{
			name:     "command with AND operator",
			input:    "cmd1 && cmd2",
			expected: "'cmd1 && cmd2'",
		},
		{
			name:     "command with OR operator",
			input:    "cmd1 || cmd2",
			expected: "'cmd1 || cmd2'",
		},
		{
			name:     "command with pipe",
			input:    "echo hello | grep h",
			expected: "'echo hello | grep h'",
		},
		{
			name:     "command with backticks",
			input:    "echo `whoami`",
			expected: "'echo `whoami`'",
		},
		{
			name:     "command with dollar expansion",
			input:    "echo $(whoami)",
			expected: "'echo $(whoami)'",
		},
		{
			name:     "command with multiple single quotes",
			input:    "it's a 'test'",
			expected: "'it'\"'\"'s a '\"'\"'test'\"'\"''",
		},
		{
			name:     "command with special characters",
			input:    "chmod +x ~/script.sh && ./script.sh",
			expected: "'chmod +x ~/script.sh && ./script.sh'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := quoteForShell(tt.input)
			if result != tt.expected {
				t.Errorf("quoteForShell(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestWrapBashLoginCommand(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "simple command",
			input:    "echo hello",
			expected: []string{"bash", "-lc", "'echo hello'"},
		},
		{
			name:     "empty command",
			input:    "",
			expected: []string{"bash", "-lc", "''"},
		},
		{
			name:     "complex command with operators",
			input:    "set -e; cmd1 && cmd2",
			expected: []string{"bash", "-lc", "'set -e; cmd1 && cmd2'"},
		},
		{
			name:     "command with single quotes",
			input:    "echo 'hello'",
			expected: []string{"bash", "-lc", "'echo '\"'\"'hello'\"'\"''"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := wrapBashLoginCommand(tt.input)
			if len(result) != len(tt.expected) {
				t.Fatalf("wrapBashLoginCommand(%q) returned %d elements, want %d", tt.input, len(result), len(tt.expected))
			}
			for i := range result {
				if result[i] != tt.expected[i] {
					t.Errorf("wrapBashLoginCommand(%q)[%d] = %q, want %q", tt.input, i, result[i], tt.expected[i])
				}
			}
		})
	}
}
