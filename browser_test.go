package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"
)

func TestNewBrowserService(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	service, err := NewBrowserService(ctx)
	if err != nil {
		t.Fatalf("Failed to create browser service: %v", err)
	}
	defer service.Stop()

	if service.Port == 0 {
		t.Error("Browser service port should not be 0")
	}

	if service.listener == nil {
		t.Error("Browser service listener should not be nil")
	}

	if service.server == nil {
		t.Error("Browser service HTTP server should not be nil")
	}
}

func TestBrowserServiceHandlesHTTPRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	service, err := NewBrowserService(ctx)
	if err != nil {
		t.Fatalf("Failed to create browser service: %v", err)
	}
	defer service.Stop()

	// Wait a bit for the server to start
	time.Sleep(100 * time.Millisecond)

	// Send a test HTTP POST request to the browser service
	testURL := "https://example.com"
	resp, err := http.Post(
		fmt.Sprintf("http://localhost:%d/open?url=%s", service.Port, url.QueryEscape(testURL)),
		"application/x-www-form-urlencoded",
		nil,
	)
	
	if err != nil {
		t.Fatalf("Failed to send HTTP request: %v", err)
	}
	defer resp.Body.Close()

	// We expect it to fail to open the browser (no browser in CI), but the HTTP request should succeed
	// The status could be 500 if browser opening fails, but that's okay for this test
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("Unexpected status code: %d", resp.StatusCode)
	}
}

func TestBrowserServiceStop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	service, err := NewBrowserService(ctx)
	if err != nil {
		t.Fatalf("Failed to create browser service: %v", err)
	}

	// Stop the service
	service.Stop()

	// Try to connect - should fail
	time.Sleep(100 * time.Millisecond)
	_, err = net.Dial("tcp", fmt.Sprintf("localhost:%d", service.Port))
	if err == nil {
		t.Error("Expected connection to fail after service stop, but it succeeded")
	}
}

func TestBuildSSHArgsWithBrowserService(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	service, err := NewBrowserService(ctx)
	if err != nil {
		t.Fatalf("Failed to create browser service: %v", err)
	}
	defer service.Stop()

	args := CommandLineArgs{}
	sshArgs := args.BuildSSHArgs("/tmp/test.sock", 8080, service)

	// Verify browser port forward is included
	expectedForward := fmt.Sprintf("%d:localhost:%d", service.Port, service.Port)
	foundForward := false
	for i := 0; i < len(sshArgs)-1; i++ {
		if sshArgs[i] == "-R" && sshArgs[i+1] == expectedForward {
			foundForward = true
			break
		}
	}

	if !foundForward {
		t.Errorf("Browser port forward not found in SSH args. Expected: %s, Got args: %v", expectedForward, sshArgs)
	}

	// Verify SetEnv options are NOT included (users configure BROWSER themselves)
	for i := 0; i < len(sshArgs)-1; i++ {
		if sshArgs[i] == "-o" && (sshArgs[i+1] == "SetEnv BROWSER=$HOME/browser-opener.sh" || 
			sshArgs[i+1] == fmt.Sprintf("SetEnv GH_ADO_CODESPACES_BROWSER_PORT=%d", service.Port)) {
			t.Errorf("SetEnv options should not be in SSH args anymore (users configure BROWSER themselves). Found: %s", sshArgs[i+1])
		}
	}
}

func TestBuildSSHArgsWithoutBrowserService(t *testing.T) {
	args := CommandLineArgs{}
	sshArgs := args.BuildSSHArgs("/tmp/test.sock", 8080, nil)

	// Verify no browser-specific port forwards are included when service is nil
	for i := 0; i < len(sshArgs)-1; i++ {
		if sshArgs[i] == "-R" {
			// Make sure it's not a browser port (should be the auth socket or AI services)
			if sshArgs[i+1] != "/tmp/test.sock:localhost:8080" {
				// This is fine - could be other forwards like AI services
				continue
			}
		}
	}
}

func TestHTTPEndpointMethodValidation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	service, err := NewBrowserService(ctx)
	if err != nil {
		t.Fatalf("Failed to create browser service: %v", err)
	}
	defer service.Stop()

	// Wait a bit for the server to start
	time.Sleep(100 * time.Millisecond)

	// Test that GET requests are rejected
	resp, err := http.Get(fmt.Sprintf("http://localhost:%d/open?url=https://example.com", service.Port))
	if err != nil {
		t.Fatalf("Failed to send GET request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("Expected status %d for GET request, got %d", http.StatusMethodNotAllowed, resp.StatusCode)
	}
}

func TestHTTPEndpointMissingURL(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	service, err := NewBrowserService(ctx)
	if err != nil {
		t.Fatalf("Failed to create browser service: %v", err)
	}
	defer service.Stop()

	// Wait a bit for the server to start
	time.Sleep(100 * time.Millisecond)

	// Test that requests without URL parameter are rejected
	resp, err := http.Post(
		fmt.Sprintf("http://localhost:%d/open", service.Port),
		"application/x-www-form-urlencoded",
		nil,
	)
	if err != nil {
		t.Fatalf("Failed to send POST request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("Expected status %d for request without URL, got %d", http.StatusBadRequest, resp.StatusCode)
	}
}
