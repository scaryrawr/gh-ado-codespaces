package main

import (
	"testing"
)

func TestFormatCodespaceListItem(t *testing.T) {
	tests := []struct {
		name      string
		codespace Codespace
		expected  []string // parts that should be present in the output
	}{
		{
			name: "basic available codespace",
			codespace: Codespace{
				Name:        "codespace-123",
				DisplayName: "My Codespace",
				Repository:  "user/repo",
				State:       "Available",
			},
			expected: []string{"✓", "My Codespace", "user/repo"},
		},
		{
			name: "starting codespace",
			codespace: Codespace{
				Name:        "codespace-456",
				DisplayName: "Test Codespace",
				Repository:  "user/test-repo",
				State:       "Starting",
			},
			expected: []string{"…", "Test Codespace", "user/test-repo"},
		},
		{
			name: "shutdown codespace",
			codespace: Codespace{
				Name:        "codespace-789",
				DisplayName: "Old Codespace",
				Repository:  "user/old-repo",
				State:       "Shutdown",
			},
			expected: []string{"⊘", "Old Codespace", "user/old-repo"},
		},
		{
			name: "unknown state codespace",
			codespace: Codespace{
				Name:        "codespace-unknown",
				DisplayName: "Unknown Codespace",
				Repository:  "user/unknown-repo",
				State:       "Unknown",
			},
			expected: []string{"?", "Unknown Codespace", "user/unknown-repo"},
		},
		{
			name: "no display name uses name",
			codespace: Codespace{
				Name:       "codespace-no-display",
				Repository: "user/repo",
				State:      "Available",
			},
			expected: []string{"✓", "codespace-no-display", "user/repo"},
		},
		{
			name: "with git status indicators",
			codespace: Codespace{
				Name:        "codespace-git",
				DisplayName: "Git Codespace",
				Repository:  "user/git-repo",
				State:       "Available",
				GitStatus: struct {
					Ahead                 int    `json:"ahead"`
					Behind                int    `json:"behind"`
					HasUncommittedChanges bool   `json:"hasUncommittedChanges"`
					HasUnpushedChanges    bool   `json:"hasUnpushedChanges"`
					Ref                   string `json:"ref"`
				}{
					Ahead:                 5,
					HasUncommittedChanges: true,
					HasUnpushedChanges:    true,
				},
			},
			expected: []string{"✓", "Git Codespace", "user/git-repo", "+5", "uncommitted changes", "unpushed changes"},
		},
		{
			name: "only ahead commits",
			codespace: Codespace{
				Name:        "codespace-ahead",
				DisplayName: "Ahead Codespace",
				Repository:  "user/ahead-repo",
				State:       "Available",
				GitStatus: struct {
					Ahead                 int    `json:"ahead"`
					Behind                int    `json:"behind"`
					HasUncommittedChanges bool   `json:"hasUncommittedChanges"`
					HasUnpushedChanges    bool   `json:"hasUnpushedChanges"`
					Ref                   string `json:"ref"`
				}{
					Ahead: 3,
				},
			},
			expected: []string{"✓", "Ahead Codespace", "user/ahead-repo", "+3"},
		},
		{
			name: "only uncommitted changes",
			codespace: Codespace{
				Name:        "codespace-uncommitted",
				DisplayName: "Uncommitted Codespace",
				Repository:  "user/uncommitted-repo",
				State:       "Available",
				GitStatus: struct {
					Ahead                 int    `json:"ahead"`
					Behind                int    `json:"behind"`
					HasUncommittedChanges bool   `json:"hasUncommittedChanges"`
					HasUnpushedChanges    bool   `json:"hasUnpushedChanges"`
					Ref                   string `json:"ref"`
				}{
					HasUncommittedChanges: true,
				},
			},
			expected: []string{"✓", "Uncommitted Codespace", "user/uncommitted-repo", "uncommitted changes"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatCodespaceListItem(tt.codespace)
			
			// Check that all expected parts are present in the output
			for _, expected := range tt.expected {
				if !containsSubstring(result, expected) {
					t.Errorf("formatCodespaceListItem() result missing %q\nGot: %q", expected, result)
				}
			}
			
			// Check that the result is not empty
			if result == "" {
				t.Error("formatCodespaceListItem() returned empty string")
			}
		})
	}
}

func TestCodespaceSorting(t *testing.T) {
	codespaces := []Codespace{
		{Name: "cs1", State: "Shutdown"},
		{Name: "cs2", State: "Available"},
		{Name: "cs3", State: "Unknown"},
		{Name: "cs4", State: "Starting"},
		{Name: "cs5", State: "Available"},
		{Name: "cs6", State: "Starting"},
	}

	// Test the sorting logic used in SelectCodespace
	// We'll replicate the sorting logic here for testing
	stateOrder := map[string]int{
		"Available": 0,
		"Starting":  1,
	}
	
	// Sort codespaces like in SelectCodespace
	sortedCodespaces := make([]Codespace, len(codespaces))
	copy(sortedCodespaces, codespaces)
	
	// Simple bubble sort to replicate the sorting logic
	for i := 0; i < len(sortedCodespaces); i++ {
		for j := i + 1; j < len(sortedCodespaces); j++ {
			iOrder, iExists := stateOrder[sortedCodespaces[i].State]
			jOrder, jExists := stateOrder[sortedCodespaces[j].State]
			
			if !iExists {
				iOrder = 99
			}
			if !jExists {
				jOrder = 99
			}
			
			// Sort by state order first, then by name
			if iOrder > jOrder || (iOrder == jOrder && sortedCodespaces[i].Name > sortedCodespaces[j].Name) {
				// Swap
				sortedCodespaces[i], sortedCodespaces[j] = sortedCodespaces[j], sortedCodespaces[i]
			}
		}
	}

	// Verify sorting results
	expectedOrder := []string{"Available", "Available", "Starting", "Starting", "Shutdown", "Unknown"}
	
	if len(sortedCodespaces) != len(expectedOrder) {
		t.Fatalf("Expected %d codespaces, got %d", len(expectedOrder), len(sortedCodespaces))
	}
	
	for i, expected := range expectedOrder {
		if sortedCodespaces[i].State != expected {
			t.Errorf("Position %d: expected state %q, got %q", i, expected, sortedCodespaces[i].State)
		}
	}
}

func TestCodespace_StateRepresentation(t *testing.T) {
	tests := []struct {
		state            string
		expectedSymbol   string
		shouldHaveColor  bool
	}{
		{
			state:           "Available",
			expectedSymbol:  "✓",
			shouldHaveColor: true,
		},
		{
			state:           "Starting",
			expectedSymbol:  "…",
			shouldHaveColor: true,
		},
		{
			state:           "Shutdown",
			expectedSymbol:  "⊘",
			shouldHaveColor: true,
		},
		{
			state:           "UnknownState",
			expectedSymbol:  "?",
			shouldHaveColor: true,
		},
		{
			state:           "",
			expectedSymbol:  "?",
			shouldHaveColor: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.state, func(t *testing.T) {
			cs := Codespace{
				Name:        "test",
				DisplayName: "Test",
				Repository:  "test/repo",
				State:       tt.state,
			}
			
			result := formatCodespaceListItem(cs)
			
			if !containsSubstring(result, tt.expectedSymbol) {
				t.Errorf("Expected symbol %q not found in result: %q", tt.expectedSymbol, result)
			}
			
			// Check for ANSI color codes if expected
			if tt.shouldHaveColor {
				hasColorCode := containsSubstring(result, "\033[")
				if !hasColorCode {
					t.Errorf("Expected ANSI color codes in result, but found none: %q", result)
				}
			}
		})
	}
}

// Test helper to simulate codespace struct creation with git status
func createTestCodespace(name, displayName, repo, state string, ahead int, uncommitted, unpushed bool) Codespace {
	return Codespace{
		Name:        name,
		DisplayName: displayName,
		Repository:  repo,
		State:       state,
		GitStatus: struct {
			Ahead                 int    `json:"ahead"`
			Behind                int    `json:"behind"`
			HasUncommittedChanges bool   `json:"hasUncommittedChanges"`
			HasUnpushedChanges    bool   `json:"hasUnpushedChanges"`
			Ref                   string `json:"ref"`
		}{
			Ahead:                 ahead,
			HasUncommittedChanges: uncommitted,
			HasUnpushedChanges:    unpushed,
		},
	}
}

func TestCreateTestCodespace(t *testing.T) {
	cs := createTestCodespace("test", "Test Codespace", "user/repo", "Available", 5, true, false)
	
	if cs.Name != "test" {
		t.Errorf("Expected name 'test', got %q", cs.Name)
	}
	if cs.GitStatus.Ahead != 5 {
		t.Errorf("Expected ahead count 5, got %d", cs.GitStatus.Ahead)
	}
	if !cs.GitStatus.HasUncommittedChanges {
		t.Error("Expected HasUncommittedChanges to be true")
	}
	if cs.GitStatus.HasUnpushedChanges {
		t.Error("Expected HasUnpushedChanges to be false")
	}
}