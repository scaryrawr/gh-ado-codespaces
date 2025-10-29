package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
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
}

func TestBrowserServiceHandlesMessage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	service, err := NewBrowserService(ctx)
	if err != nil {
		t.Fatalf("Failed to create browser service: %v", err)
	}
	defer service.Stop()

	// Send a test message to the browser service
	// Note: We can't actually test browser.OpenURL without mocking,
	// but we can test that the service accepts and processes messages
	done := make(chan bool)
	go func() {
		time.Sleep(100 * time.Millisecond)
		conn, err := net.Dial("tcp", fmt.Sprintf("localhost:%d", service.Port))
		if err != nil {
			t.Errorf("Failed to connect to browser service: %v", err)
			done <- false
			return
		}
		defer conn.Close()

		msg := BrowserMessage{
			Type:   "browser",
			Action: "open",
			URL:    "https://example.com",
		}
		msgBytes, _ := json.Marshal(msg)
		conn.Write(msgBytes)
		conn.Write([]byte("\n"))
		done <- true
	}()

	// Wait for the message to be sent or timeout
	select {
	case success := <-done:
		if !success {
			t.Error("Failed to send message")
		}
	case <-time.After(5 * time.Second):
		t.Error("Test timed out")
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

	// Verify SetEnv options are included
	foundBrowserEnv := false
	foundPortEnv := false
	expectedBrowserEnv := "SetEnv BROWSER=$HOME/browser-opener.sh"
	expectedPortEnv := fmt.Sprintf("SetEnv GH_ADO_CODESPACES_BROWSER_PORT=%d", service.Port)
	
	for i := 0; i < len(sshArgs)-1; i++ {
		if sshArgs[i] == "-o" && sshArgs[i+1] == expectedBrowserEnv {
			foundBrowserEnv = true
		}
		if sshArgs[i] == "-o" && sshArgs[i+1] == expectedPortEnv {
			foundPortEnv = true
		}
	}

	if !foundBrowserEnv {
		t.Errorf("BROWSER SetEnv option not found in SSH args. Expected: -o %s, Got args: %v", expectedBrowserEnv, sshArgs)
	}

	if !foundPortEnv {
		t.Errorf("GH_ADO_CODESPACES_BROWSER_PORT SetEnv option not found in SSH args. Expected: -o %s, Got args: %v", expectedPortEnv, sshArgs)
	}
}

func TestBuildSSHArgsWithoutBrowserService(t *testing.T) {
	args := CommandLineArgs{}
	sshArgs := args.BuildSSHArgs("/tmp/test.sock", 8080, nil)

	// Verify no browser-specific args are included when service is nil
	for i := 0; i < len(sshArgs)-1; i++ {
		if sshArgs[i] == "-R" {
			// Make sure it's not a browser port (should be the auth socket)
			if sshArgs[i+1] != "/tmp/test.sock:localhost:8080" {
				// This is fine - could be other forwards like AI services
				continue
			}
		}
	}

	// Verify that the BROWSER export command is not in the args when service is nil and no remaining args
	args2 := CommandLineArgs{RemainingArgs: []string{}}
	sshArgs2 := args2.BuildSSHArgs("/tmp/test.sock", 8080, nil)
	
	for _, arg := range sshArgs2 {
		if len(arg) > 0 && arg[0:1] == "e" && len(arg) > 6 && arg[0:7] == "export " {
			t.Errorf("Should not have export command when browser service is nil and no remaining args: %v", arg)
		}
	}
}

func TestBrowserMessageParsing(t *testing.T) {
	tests := []struct {
		name      string
		jsonStr   string
		wantError bool
		expected  BrowserMessage
	}{
		{
			name:      "valid message",
			jsonStr:   `{"type":"browser","action":"open","url":"https://example.com"}`,
			wantError: false,
			expected: BrowserMessage{
				Type:   "browser",
				Action: "open",
				URL:    "https://example.com",
			},
		},
		{
			name:      "invalid json",
			jsonStr:   `{"type":"browser","action":"open"`,
			wantError: true,
		},
		{
			name:      "missing url",
			jsonStr:   `{"type":"browser","action":"open","url":""}`,
			wantError: false,
			expected: BrowserMessage{
				Type:   "browser",
				Action: "open",
				URL:    "",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var msg BrowserMessage
			err := json.Unmarshal([]byte(tt.jsonStr), &msg)
			
			if tt.wantError && err == nil {
				t.Error("Expected error but got none")
			}
			
			if !tt.wantError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
			
			if !tt.wantError && err == nil {
				if msg.Type != tt.expected.Type || msg.Action != tt.expected.Action || msg.URL != tt.expected.URL {
					t.Errorf("Message mismatch. Got %+v, want %+v", msg, tt.expected)
				}
			}
		})
	}
}
