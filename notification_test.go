package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestNewNotificationService(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	service, err := NewNotificationService(ctx)
	if err != nil {
		t.Fatalf("Failed to create notification service: %v", err)
	}
	defer service.Stop()

	if service.Port == 0 {
		t.Error("Notification service port should not be 0")
	}

	if service.SocketPath == "" {
		t.Error("Notification service socket path should not be empty")
	}

	// Verify socket path follows expected pattern
	if !strings.HasPrefix(service.SocketPath, "/tmp/gh-ado-notification-") || !strings.HasSuffix(service.SocketPath, ".sock") {
		t.Errorf("Socket path has unexpected format: %s", service.SocketPath)
	}

	if service.listener == nil {
		t.Error("Notification service listener should not be nil")
	}

	if service.server == nil {
		t.Error("Notification service HTTP server should not be nil")
	}
}

func TestNotificationServiceHandlesHTTPRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	service, err := NewNotificationService(ctx)
	if err != nil {
		t.Fatalf("Failed to create notification service: %v", err)
	}
	defer service.Stop()

	// Wait a bit for the server to start
	time.Sleep(100 * time.Millisecond)

	// Send a test HTTP POST request to the notification service
	testReq := NotificationRequest{
		Title:   "Test Title",
		Message: "Test Message",
	}
	jsonData, err := json.Marshal(testReq)
	if err != nil {
		t.Fatalf("Failed to marshal JSON: %v", err)
	}

	resp, err := http.Post(
		fmt.Sprintf("http://localhost:%d/notify", service.Port),
		"application/json",
		bytes.NewBuffer(jsonData),
	)

	if err != nil {
		t.Fatalf("Failed to send HTTP request: %v", err)
	}
	defer resp.Body.Close()

	// We expect it to potentially fail to send notification (no desktop environment in CI), 
	// but the HTTP request should be processed
	// The status could be 500 if notification sending fails, but that's okay for this test
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("Unexpected status code: %d", resp.StatusCode)
	}
}

func TestNotificationServiceStop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	service, err := NewNotificationService(ctx)
	if err != nil {
		t.Fatalf("Failed to create notification service: %v", err)
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

func TestBuildSSHArgsWithNotificationService(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	service, err := NewNotificationService(ctx)
	if err != nil {
		t.Fatalf("Failed to create notification service: %v", err)
	}
	defer service.Stop()

	args := CommandLineArgs{}
	sshArgs := args.BuildSSHArgs("/tmp/test.sock", 8080, nil, service)

	// Verify notification socket forward is included (socket path -> localhost:port)
	expectedForward := fmt.Sprintf("%s:localhost:%d", service.SocketPath, service.Port)
	foundForward := false
	for i := 0; i < len(sshArgs)-1; i++ {
		if sshArgs[i] == "-R" && sshArgs[i+1] == expectedForward {
			foundForward = true
			break
		}
	}

	if !foundForward {
		t.Errorf("Notification socket forward not found in SSH args. Expected: %s, Got args: %v", expectedForward, sshArgs)
	}
}

func TestBuildSSHArgsWithoutNotificationService(t *testing.T) {
	args := CommandLineArgs{}
	sshArgs := args.BuildSSHArgs("/tmp/test.sock", 8080, nil, nil)

	// Verify no notification-specific port forwards are included when service is nil
	for i := 0; i < len(sshArgs)-1; i++ {
		if sshArgs[i] == "-R" {
			// Make sure it's not a notification port (should be the auth socket or AI services)
			if strings.Contains(sshArgs[i+1], "gh-ado-notification-") {
				t.Errorf("Unexpected notification socket forward found when service is nil: %s", sshArgs[i+1])
			}
		}
	}
}

func TestNotificationHTTPEndpointMethodValidation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	service, err := NewNotificationService(ctx)
	if err != nil {
		t.Fatalf("Failed to create notification service: %v", err)
	}
	defer service.Stop()

	// Wait a bit for the server to start
	time.Sleep(100 * time.Millisecond)

	// Test that GET requests are rejected
	resp, err := http.Get(fmt.Sprintf("http://localhost:%d/notify", service.Port))
	if err != nil {
		t.Fatalf("Failed to send GET request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("Expected status %d for GET request, got %d", http.StatusMethodNotAllowed, resp.StatusCode)
	}
}

func TestNotificationHTTPEndpointMissingTitle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	service, err := NewNotificationService(ctx)
	if err != nil {
		t.Fatalf("Failed to create notification service: %v", err)
	}
	defer service.Stop()

	// Wait a bit for the server to start
	time.Sleep(100 * time.Millisecond)

	// Test that requests without title are rejected
	testReq := NotificationRequest{
		Message: "Test Message",
	}
	jsonData, err := json.Marshal(testReq)
	if err != nil {
		t.Fatalf("Failed to marshal JSON: %v", err)
	}

	resp, err := http.Post(
		fmt.Sprintf("http://localhost:%d/notify", service.Port),
		"application/json",
		bytes.NewBuffer(jsonData),
	)
	if err != nil {
		t.Fatalf("Failed to send POST request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("Expected status %d for request without title, got %d", http.StatusBadRequest, resp.StatusCode)
	}
}

func TestNotificationHTTPEndpointMissingMessage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	service, err := NewNotificationService(ctx)
	if err != nil {
		t.Fatalf("Failed to create notification service: %v", err)
	}
	defer service.Stop()

	// Wait a bit for the server to start
	time.Sleep(100 * time.Millisecond)

	// Test that requests without message are rejected
	testReq := NotificationRequest{
		Title: "Test Title",
	}
	jsonData, err := json.Marshal(testReq)
	if err != nil {
		t.Fatalf("Failed to marshal JSON: %v", err)
	}

	resp, err := http.Post(
		fmt.Sprintf("http://localhost:%d/notify", service.Port),
		"application/json",
		bytes.NewBuffer(jsonData),
	)
	if err != nil {
		t.Fatalf("Failed to send POST request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("Expected status %d for request without message, got %d", http.StatusBadRequest, resp.StatusCode)
	}
}

func TestNotificationHTTPEndpointInvalidJSON(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	service, err := NewNotificationService(ctx)
	if err != nil {
		t.Fatalf("Failed to create notification service: %v", err)
	}
	defer service.Stop()

	// Wait a bit for the server to start
	time.Sleep(100 * time.Millisecond)

	// Test that requests with invalid JSON are rejected
	resp, err := http.Post(
		fmt.Sprintf("http://localhost:%d/notify", service.Port),
		"application/json",
		bytes.NewBufferString("not valid json"),
	)
	if err != nil {
		t.Fatalf("Failed to send POST request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("Expected status %d for invalid JSON, got %d", http.StatusBadRequest, resp.StatusCode)
	}
}
