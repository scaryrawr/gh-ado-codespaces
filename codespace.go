package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/cli/go-gh/v2"
)

// Codespace represents a GitHub Codespace with the fields we need
type Codespace struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	Repository  string `json:"repository"`
	GitStatus   struct {
		Ahead                 int    `json:"ahead"`
		Behind                int    `json:"behind"`
		HasUncommittedChanges bool   `json:"hasUncommittedChanges"`
		HasUnpushedChanges    bool   `json:"hasUnpushedChanges"`
		Ref                   string `json:"ref"`
	} `json:"gitStatus"`
	State      string    `json:"state"`
	LastUsedAt time.Time `json:"lastUsedAt"`
}

// fetchCodespaces gets the list of available codespaces using gh cs list
func fetchCodespaces(repoFilter, ownerFilter string) ([]Codespace, error) {
	args := []string{"codespace", "list", "--json", "name,displayName,repository,gitStatus,state,lastUsedAt"}

	if repoFilter != "" {
		args = append(args, "--repo", repoFilter)
	}
	if ownerFilter != "" {
		args = append(args, "--repo-owner", ownerFilter)
	}

	stdout, stderr, err := gh.Exec(args...)
	if err != nil {
		return nil, fmt.Errorf("error listing codespaces: %w\nStderr: %s", err, stderr.String())
	}

	var codespaces []Codespace
	if err := json.Unmarshal(stdout.Bytes(), &codespaces); err != nil {
		return nil, fmt.Errorf("error parsing codespace list: %w", err)
	}

	return codespaces, nil
}

// ANSI color codes for base16 compatibility
const (
	colorReset     = "\033[0m"
	colorGreen     = "\033[32m" // base16 green for running/available
	colorYellow    = "\033[33m" // base16 yellow for starting
	colorRed       = "\033[31m" // base16 red for shutdown
	colorBrightRed = "\033[91m" // bright red for unknown states
)

// formatTimeAgo formats time relative to now for recent times, or absolute date for older times
func formatTimeAgo(t time.Time) string {
	if t.IsZero() {
		return "never"
	}

	now := time.Now()
	duration := now.Sub(t)
	
	// If less than a week, show relative time
	if duration < 7*24*time.Hour {
		switch {
		case duration < time.Minute:
			return "just now"
		case duration < time.Hour:
			minutes := int(duration.Minutes())
			if minutes == 1 {
				return "1 minute ago"
			}
			return fmt.Sprintf("%d minutes ago", minutes)
		case duration < 24*time.Hour:
			hours := int(duration.Hours())
			if hours == 1 {
				return "1 hour ago"
			}
			return fmt.Sprintf("%d hours ago", hours)
		default:
			days := int(duration.Hours() / 24)
			if days == 1 {
				return "1 day ago"
			}
			return fmt.Sprintf("%d days ago", days)
		}
	}
	
	// If more than a week, show the date
	return t.Format("Jan 2, 2006")
}

// formatCodespaceListItem formats a codespace for display in the selection prompt
func formatCodespaceListItem(cs Codespace) string {
	displayName := cs.DisplayName
	if displayName == "" {
		displayName = cs.Name
	}

	var state, color string
	switch cs.State {
	case "Available":
		state = "✓"
		color = colorGreen
	case "Starting":
		state = "…"
		color = colorYellow
	case "Shutdown":
		state = "⊘"
		color = colorRed
	default:
		state = "?"
		color = colorBrightRed
	}

	prefix := color + state + colorReset + " " + color + displayName + colorReset
	timeAgo := formatTimeAgo(cs.LastUsedAt)

	return fmt.Sprintf("%s - %s (last used %s)", prefix, cs.Repository, timeAgo)
}

// SelectCodespace prompts the user to select a codespace from a list
func SelectCodespace(ctx context.Context, repoFilter, ownerFilter string) (string, error) {
	codespaces, err := fetchCodespaces(repoFilter, ownerFilter)
	if err != nil {
		return "", err
	}

	if len(codespaces) == 0 {
		return "", fmt.Errorf("no codespaces found")
	}

	// Sort codespaces: Available first, then Starting, then others
	sort.Slice(codespaces, func(i, j int) bool {
		stateOrder := map[string]int{
			"Available": 0,
			"Starting":  1,
		}
		iOrder, iExists := stateOrder[codespaces[i].State]
		jOrder, jExists := stateOrder[codespaces[j].State]

		if !iExists {
			iOrder = 99
		}
		if !jExists {
			jOrder = 99
		}

		if iOrder != jOrder {
			return iOrder < jOrder
		}

		return codespaces[i].Name < codespaces[j].Name
	})

	// Create display options for the selection
	options := make([]string, len(codespaces))
	for i, cs := range codespaces {
		options[i] = formatCodespaceListItem(cs)
	}

	selectedIndex, err := showSelection(options)
	if err != nil {
		return "", fmt.Errorf("codespace selection failed: %w", err)
	}

	return codespaces[selectedIndex].Name, nil
}
