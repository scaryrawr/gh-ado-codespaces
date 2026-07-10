package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image/png"
	"net"
	"net/http"
	"net/http/httptest"
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

	// Verify notification socket forward is included.
	expectedForward := fmt.Sprintf("%s:%s:%d", service.SocketPath, localServiceHost, service.Port)
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

func TestNotificationTruncation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	service, err := NewNotificationService(ctx)
	if err != nil {
		t.Fatalf("Failed to create notification service: %v", err)
	}
	defer service.Stop()

	// Wait a bit for the server to start
	time.Sleep(100 * time.Millisecond)

	// Create a very long title and message
	longTitle := strings.Repeat("A", 150)   // 150 characters, should be truncated to 100
	longMessage := strings.Repeat("B", 600) // 600 characters, should be truncated to 500

	testReq := NotificationRequest{
		Title:   longTitle,
		Message: longMessage,
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

	// The notification might fail to send in CI (no desktop environment)
	// but we're mainly testing that the truncation doesn't cause errors
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("Unexpected status code: %d", resp.StatusCode)
	}
}

func TestNotificationIconEmbedded(t *testing.T) {
	if len(notificationIcon) == 0 {
		t.Fatal("Expected embedded notification icon to be non-empty")
	}

	img, err := png.Decode(bytes.NewReader(notificationIcon))
	if err != nil {
		t.Fatalf("Expected embedded notification icon to decode as PNG: %v", err)
	}

	bounds := img.Bounds()
	if bounds.Dx() == 0 || bounds.Dy() == 0 {
		t.Fatalf("Expected embedded notification icon to have non-zero dimensions, got %v", bounds)
	}

	_, _, _, alpha := img.At(bounds.Min.X, bounds.Min.Y).RGBA()
	if alpha != 0 {
		t.Fatalf("Expected embedded notification icon corner to be transparent, got alpha=%d", alpha>>8)
	}
}

func TestNotificationHandlerUsesEmbeddedIcon(t *testing.T) {
	originalNotify := desktopNotify
	t.Cleanup(func() {
		desktopNotify = originalNotify
	})

	var gotTitle, gotMessage string
	var gotIcon any
	desktopNotify = func(title, message string, icon any) error {
		gotTitle = title
		gotMessage = message
		gotIcon = icon
		return nil
	}

	service := &NotificationService{}
	reqBody := `{"title":"Test Title","message":"Test Message"}`
	req := httptest.NewRequest(http.MethodPost, "/notify", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	service.handleNotification(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("Expected status %d, got %d", http.StatusOK, recorder.Code)
	}

	if gotTitle != "Test Title" || gotMessage != "Test Message" {
		t.Fatalf("Unexpected notification payload: title=%q message=%q", gotTitle, gotMessage)
	}

	iconBytes, ok := gotIcon.([]byte)
	if !ok {
		t.Fatalf("Expected embedded icon bytes, got %T", gotIcon)
	}

	if !bytes.Equal(iconBytes, notificationIcon) {
		t.Fatal("Expected handler to pass embedded notification icon bytes")
	}
}
