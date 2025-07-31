package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

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
	State string `json:"state"`
}

// fetchCodespaces gets the list of available codespaces using gh cs list
func fetchCodespaces(repoFilter, ownerFilter string) ([]Codespace, error) {
	args := []string{"codespace", "list", "--json", "name,displayName,repository,gitStatus,state"}

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

	var indicators []string
	if cs.GitStatus.Ahead > 0 {
		indicators = append(indicators, fmt.Sprintf("+%d", cs.GitStatus.Ahead))
	}
	if cs.GitStatus.HasUncommittedChanges {
		indicators = append(indicators, "uncommitted changes")
	}
	if cs.GitStatus.HasUnpushedChanges {
		indicators = append(indicators, "unpushed changes")
	}

	suffix := cs.Repository
	if len(indicators) > 0 {
		suffix = fmt.Sprintf("%s (%s)", suffix, strings.Join(indicators, ", "))
	}

	return fmt.Sprintf("%s - %s", prefix, suffix)
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
