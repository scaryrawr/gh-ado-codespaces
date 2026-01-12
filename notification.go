package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/cli/go-gh/v2"
	"github.com/gen2brain/beeep"
	"github.com/google/uuid"
)

//go:embed notification-sender.sh
var notificationSenderScript string

// NotificationRequest represents a notification request from the codespace
type NotificationRequest struct {
	Title   string `json:"title"`
	Message string `json:"message"`
}

// NotificationService manages the notification service
type NotificationService struct {
	Port       int
	SocketPath string
	server     *http.Server
	listener   net.Listener
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
}

// NewNotificationService creates and starts a new notification service
func NewNotificationService(ctx context.Context) (*NotificationService, error) {
	// Create a local TCP listener for notification requests
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("failed to create local listener: %w", err)
	}

	// Get the actual port that was assigned
	notificationPort := listener.Addr().(*net.TCPAddr).Port

	// Generate a unique socket path for remote forwarding
	socketId := uuid.New()
	socketPath := "/tmp/gh-ado-notification-" + socketId.String() + ".sock"

	logDebug("Local notification HTTP service created on port: %d, socket path: %s", notificationPort, socketPath)

	serviceCtx, cancel := context.WithCancel(ctx)

	service := &NotificationService{
		Port:       notificationPort,
		SocketPath: socketPath,
		listener:   listener,
		ctx:        serviceCtx,
		cancel:     cancel,
	}

	// Create HTTP handler
	mux := http.NewServeMux()
	mux.HandleFunc("/notify", service.handleNotification)

	// Create HTTP server
	service.server = &http.Server{
		Handler: mux,
	}

	// Start serving in a goroutine
	service.wg.Add(1)
	go service.serve()

	return service, nil
}

// serve starts the HTTP server
func (ns *NotificationService) serve() {
	defer ns.wg.Done()
	defer ns.listener.Close()

	logDebug("Notification HTTP service starting on port %d", ns.Port)

	err := ns.server.Serve(ns.listener)
	if err != nil && err != http.ErrServerClosed {
		logDebug("Notification HTTP service error: %v", err)
	}

	logDebug("Notification HTTP service stopped")
}

// handleNotification handles HTTP requests to send notifications
func (ns *NotificationService) handleNotification(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse JSON body
	var req NotificationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Validate required fields
	if req.Title == "" {
		http.Error(w, "Missing title", http.StatusBadRequest)
		return
	}

	if req.Message == "" {
		http.Error(w, "Missing message", http.StatusBadRequest)
		return
	}

	logDebug("Sending notification: title=%s, message=%s", req.Title, req.Message)

	// Send the notification using beeep
	if err := beeep.Notify(req.Title, req.Message, ""); err != nil {
		logDebug("Error sending notification: %v", err)
		fmt.Fprintf(os.Stderr, "Warning: failed to send notification: %v\n", err)
		http.Error(w, "Failed to send notification", http.StatusInternalServerError)
		return
	}

	logDebug("Successfully sent notification")
	fmt.Fprintf(os.Stderr, "Notification: %s - %s\n", req.Title, req.Message)
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

// Stop stops the notification service
func (ns *NotificationService) Stop() {
	if ns.cancel != nil {
		logDebug("NotificationService: Stop() called")

		// Use background context for shutdown to avoid race with cancel
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()

		ns.server.Shutdown(shutdownCtx)
		ns.cancel()
		ns.wg.Wait()

		// Clean up socket file
		cleanupNotificationSocketFile(ns.SocketPath)

		logDebug("NotificationService: stopped")
	}
}

// UploadNotificationSenderScript copies the notification-sender.sh script to the codespace
func UploadNotificationSenderScript(ctx context.Context, codespaceName string) error {
	// Create a temporary file with the embedded script content
	tempFile, err := os.CreateTemp("", "notification-sender*.sh")
	if err != nil {
		return fmt.Errorf("failed to create temporary file: %w", err)
	}
	defer os.Remove(tempFile.Name())

	// Write the script as-is (no port replacement needed)
	if _, err = tempFile.WriteString(notificationSenderScript); err != nil {
		return fmt.Errorf("failed to write script to temporary file: %w", err)
	}
	tempFile.Close()

	// Use gh cs cp to copy the script to the codespace
	args := []string{"codespace", "cp", "-c", codespaceName, "-e", tempFile.Name(), "remote:~/notification-sender.sh"}
	_, stderr, err := gh.Exec(args...)
	if err != nil {
		return fmt.Errorf("error copying script to codespace: %w\nStderr: %s", err, stderr.String())
	}

	// Make the script executable
	chmodArgs := []string{"codespace", "ssh", "--codespace", codespaceName, "--", "chmod", "+x", "~/notification-sender.sh"}
	_, stderr, err = gh.Exec(chmodArgs...)
	if err != nil {
		return fmt.Errorf("error making script executable: %w\nStderr: %s", err, stderr.String())
	}

	logDebug("Notification sender script uploaded and made executable")
	return nil
}

// cleanupNotificationSocketFile removes the socket file at the specified path
func cleanupNotificationSocketFile(socketPath string) {
	if socketPath != "" {
		if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
			logDebug("Failed to remove notification socket file %s: %v", socketPath, err)
		} else {
			logDebug("Cleaned up notification socket file: %s", socketPath)
		}
	}
}
