package main

import (
	"fmt"
	"strings"

	"github.com/cli/go-gh/v2"
)

// currentGitHubLogin returns the active GitHub login as reported by gh.
func currentGitHubLogin() (string, error) {
	stdout, stderr, err := gh.Exec("api", "user", "--cache", "1m", "--jq", ".login")
	if err != nil {
		trimmed := strings.TrimSpace(stderr.String())
		if trimmed != "" {
			return "", fmt.Errorf("gh api user failed: %w: %s", err, trimmed)
		}
		return "", fmt.Errorf("gh api user failed: %w", err)
	}

	login := strings.TrimSpace(stdout.String())
	if login == "" {
		return "", fmt.Errorf("gh api user returned empty login")
	}

	return login, nil
}
