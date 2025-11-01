package main

import (
	"context"
	_ "embed"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"

	"github.com/cli/go-gh/v2"
	"github.com/google/uuid"
	"github.com/pkg/browser"
)

//go:embed browser-opener.sh
var browserOpenerScript string

// BrowserService manages the browser opener service
type BrowserService struct {
	Port       int
	SocketPath string
	server     *http.Server
	listener   net.Listener
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
}

// NewBrowserService creates and starts a new browser service
func NewBrowserService(ctx context.Context) (*BrowserService, error) {
	// Create a local TCP listener for browser requests
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("failed to create local listener: %w", err)
	}

	// Get the actual port that was assigned
	browserPort := listener.Addr().(*net.TCPAddr).Port
	
	// Generate a unique socket path for remote forwarding
	socketId := uuid.New()
	socketPath := "/tmp/gh-ado-browser-" + socketId.String() + ".sock"
	
	logDebug("Local browser HTTP service created on port: %d, socket path: %s", browserPort, socketPath)

	serviceCtx, cancel := context.WithCancel(ctx)

	service := &BrowserService{
		Port:       browserPort,
		SocketPath: socketPath,
		listener:   listener,
		ctx:        serviceCtx,
		cancel:     cancel,
	}

	// Create HTTP handler
	mux := http.NewServeMux()
	mux.HandleFunc("/open", service.handleOpenURL)

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
func (bs *BrowserService) serve() {
	defer bs.wg.Done()
	defer bs.listener.Close()

	logDebug("Browser HTTP service starting on port %d", bs.Port)

	err := bs.server.Serve(bs.listener)
	if err != nil && err != http.ErrServerClosed {
		logDebug("Browser HTTP service error: %v", err)
	}

	logDebug("Browser HTTP service stopped")
}

// handleOpenURL handles HTTP requests to open URLs
func (bs *BrowserService) handleOpenURL(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get URL from query parameter
	url := r.URL.Query().Get("url")
	if url == "" {
		http.Error(w, "Missing url parameter", http.StatusBadRequest)
		return
	}

	logDebug("Opening URL in browser: %s", url)

	// Open the URL in the default browser
	if err := browser.OpenURL(url); err != nil {
		logDebug("Error opening browser: %v", err)
		fmt.Fprintf(os.Stderr, "Warning: failed to open browser for URL: %s (%v)\n", url, err)
		http.Error(w, "Failed to open browser", http.StatusInternalServerError)
		return
	}

	logDebug("Successfully opened URL in browser")
	fmt.Fprintf(os.Stderr, "Opened in browser: %s\n", url)
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

// Stop stops the browser service
func (bs *BrowserService) Stop() {
	if bs.cancel != nil {
		logDebug("BrowserService: Stop() called")
		bs.server.Shutdown(bs.ctx)
		bs.cancel()
		bs.wg.Wait()
		logDebug("BrowserService: stopped")
	}
}

// UploadBrowserOpenerScript copies the browser-opener.sh script to the codespace
// The script searches for the browser socket dynamically, so it only needs to be uploaded once
func UploadBrowserOpenerScript(ctx context.Context, codespaceName string) error {
	// Create a temporary file with the embedded script content
	tempFile, err := os.CreateTemp("", "browser-opener*.sh")
	if err != nil {
		return fmt.Errorf("failed to create temporary file: %w", err)
	}
	defer os.Remove(tempFile.Name())

	// Write the script as-is (no port replacement needed)
	if _, err = tempFile.WriteString(browserOpenerScript); err != nil {
		return fmt.Errorf("failed to write script to temporary file: %w", err)
	}
	tempFile.Close()

	// Use gh cs cp to copy the script to the codespace
	args := []string{"codespace", "cp", "-c", codespaceName, "-e", tempFile.Name(), "remote:~/browser-opener.sh"}
	_, stderr, err := gh.Exec(args...)
	if err != nil {
		return fmt.Errorf("error copying script to codespace: %w\nStderr: %s", err, stderr.String())
	}

	// Make the script executable
	chmodArgs := []string{"codespace", "ssh", "--codespace", codespaceName, "--", "chmod", "+x", "~/browser-opener.sh"}
	_, stderr, err = gh.Exec(chmodArgs...)
	if err != nil {
		return fmt.Errorf("error making script executable: %w\nStderr: %s", err, stderr.String())
	}

	logDebug("Browser opener script uploaded and made executable")
	return nil
}
