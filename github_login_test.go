package main

import (
	"testing"
)

// Since currentGitHubLogin calls external gh command, we can't easily test it without mocking
// For now, we'll just verify it exists and can be called (though it might fail in test environment)
// In a real project, we'd want to refactor this to be more testable by dependency injection

func TestCurrentGitHubLogin_Exists(t *testing.T) {
	// This is primarily a smoke test to ensure the function signature is correct
	// and the function exists. In test environment without gh CLI, this will fail
	// but that's expected behavior.

	_, err := currentGitHubLogin()

	// We don't assert anything about the result since it depends on external tooling
	// In a test environment, this will likely fail with "gh not found" or similar
	// That's acceptable - we're just ensuring the function can be called

	// Log the error for debugging purposes, but don't fail the test
	if err != nil {
		t.Logf("currentGitHubLogin() returned error (expected in test environment): %v", err)
	}
}

// Integration test that could be run separately when gh is available
func TestCurrentGitHubLogin_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// This test is meant to be run in an environment where gh CLI is installed and authenticated
	// It's marked as an integration test that can be skipped

	result, err := currentGitHubLogin()

	if err != nil {
		// In CI/CD or environments without gh CLI setup, this is expected to fail
		t.Skipf("GitHub CLI not available or not authenticated: %v", err)
		return
	}

	// If no error, validate the result
	if result == "" {
		t.Error("currentGitHubLogin() returned empty string without error")
	}

	// Basic validation that it looks like a GitHub username
	// GitHub usernames can contain alphanumeric characters, hyphens, and underscores
	for _, char := range result {
		if !((char >= 'a' && char <= 'z') ||
			(char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') ||
			char == '-' || char == '_') {
			t.Errorf("currentGitHubLogin() returned invalid GitHub username format: %q", result)
			break
		}
	}

	// GitHub usernames have length restrictions
	if len(result) > 39 {
		t.Errorf("currentGitHubLogin() returned username longer than GitHub's 39 character limit: %q", result)
	}

	if len(result) < 1 {
		t.Error("currentGitHubLogin() returned empty username")
	}
}
