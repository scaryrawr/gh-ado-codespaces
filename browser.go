package main

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sync"

	"github.com/cli/go-gh/v2"
	"github.com/pkg/browser"
)

//go:embed browser-opener.sh
var browserOpenerScript string

// BrowserMessage represents a JSON message from browser-opener.sh
type BrowserMessage struct {
	Type   string `json:"type"`
	Action string `json:"action"`
	URL    string `json:"url"`
}

// BrowserService manages the browser opener service
type BrowserService struct {
	Port     int
	listener net.Listener
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
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
	logDebug("Local browser listener created on port: %d", browserPort)

	serviceCtx, cancel := context.WithCancel(ctx)
	
	service := &BrowserService{
		Port:     browserPort,
		listener: listener,
		ctx:      serviceCtx,
		cancel:   cancel,
	}

	// Start accepting connections
	service.wg.Add(1)
	go service.acceptLoop()

	return service, nil
}

// acceptLoop accepts and handles browser connections
func (bs *BrowserService) acceptLoop() {
	defer bs.wg.Done()
	defer bs.listener.Close()

	// Create a channel for accepting connections
	connChan := make(chan net.Conn)
	errChan := make(chan error)

	// Start accept goroutine
	go func() {
		for {
			conn, err := bs.listener.Accept()
			if err != nil {
				errChan <- err
				return
			}
			connChan <- conn
		}
	}()

	for {
		select {
		case <-bs.ctx.Done():
			logDebug("Browser service context canceled, stopping accept loop")
			return
		case conn := <-connChan:
			// Handle the connection in a goroutine
			go bs.handleConnection(conn)
		case err := <-errChan:
			if bs.ctx.Err() != nil {
				return
			}
			logDebug("Error accepting browser connection: %v", err)
			return
		}
	}
}

// handleConnection handles a single browser open request
func (bs *BrowserService) handleConnection(conn net.Conn) {
	defer conn.Close()

	scanner := bufio.NewScanner(conn)
	if scanner.Scan() {
		line := scanner.Text()

		var msg BrowserMessage
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			logDebug("Error parsing browser message: %v", err)
			return
		}

		if msg.Type == "browser" && msg.Action == "open" && msg.URL != "" {
			logDebug("Opening URL in browser: %s", msg.URL)

			// Open the URL in the default browser
			if err := browser.OpenURL(msg.URL); err != nil {
				logDebug("Error opening browser: %v", err)
				fmt.Fprintf(os.Stderr, "Warning: failed to open browser for URL: %s (%v)\n", msg.URL, err)
			} else {
				logDebug("Successfully opened URL in browser")
				fmt.Fprintf(os.Stderr, "Opened in browser: %s\n", msg.URL)
			}
		}
	}
}

// Stop stops the browser service
func (bs *BrowserService) Stop() {
	if bs.cancel != nil {
		logDebug("BrowserService: Stop() called")
		bs.cancel()
		bs.wg.Wait()
		logDebug("BrowserService: stopped")
	}
}

// UploadBrowserOpenerScript copies the browser-opener.sh script to the codespace
func UploadBrowserOpenerScript(ctx context.Context, codespaceName string) error {
	// Create a temporary file with the embedded script content
	tempFile, err := os.CreateTemp("", "browser-opener*.sh")
	if err != nil {
		return fmt.Errorf("failed to create temporary file: %w", err)
	}
	defer os.Remove(tempFile.Name())

	// Write the embedded script to the temporary file
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
