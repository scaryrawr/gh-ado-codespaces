package main

import (
	"fmt"
	"net"
	"testing"
	"time"
)

func TestIsPortBound(t *testing.T) {
	// Start a test server on a random port
	listener, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("Failed to create test listener: %v", err)
	}
	defer listener.Close()

	// Get the actual port
	addr := listener.Addr().(*net.TCPAddr)
	boundPort := addr.Port

	// Test that we can detect the bound port
	if !isPortBound(boundPort) {
		t.Errorf("isPortBound(%d) = false, expected true for bound port", boundPort)
	}

	// Test with an unlikely-to-be-bound high port
	unboundPort := 65432
	if isPortBound(unboundPort) {
		t.Logf("Warning: Port %d is bound, test may be unreliable", unboundPort)
	}
}

func TestGetBoundReverseForwards(t *testing.T) {
	// This test will vary depending on what's running on the system
	forwards := GetBoundReverseForwards()

	// Log what we found
	for _, forward := range forwards {
		t.Logf("Found bound port: %d (%s)", forward.Port, forward.Description)
	}

	// Verify the structure is correct
	for _, forward := range forwards {
		if forward.Port <= 0 {
			t.Errorf("Invalid port number: %d", forward.Port)
		}
		if forward.Description == "" {
			t.Errorf("Missing description for port %d", forward.Port)
		}
		// Verify the port is actually bound
		if !isPortBound(forward.Port) {
			t.Errorf("Port %d reported as bound but isPortBound() returns false", forward.Port)
		}
	}
}

func TestBuildReverseForwardArgs(t *testing.T) {
	tests := []struct {
		name     string
		forwards []ReversePortForward
		expected []string
	}{
		{
			name:     "empty forwards",
			forwards: []ReversePortForward{},
			expected: []string{},
		},
		{
			name: "single forward",
			forwards: []ReversePortForward{
				{Port: 1234, Description: "Test Service", Enabled: true},
			},
			expected: []string{"-R", "1234:localhost:1234"},
		},
		{
			name: "multiple forwards",
			forwards: []ReversePortForward{
				{Port: 1234, Description: "LM Studio", Enabled: true},
				{Port: 11434, Description: "Ollama", Enabled: true},
			},
			expected: []string{
				"-R", "1234:localhost:1234",
				"-R", "11434:localhost:11434",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BuildReverseForwardArgs(tt.forwards)

			if len(result) != len(tt.expected) {
				t.Errorf("BuildReverseForwardArgs() returned %d args, expected %d", len(result), len(tt.expected))
				t.Errorf("Got: %v", result)
				t.Errorf("Expected: %v", tt.expected)
				return
			}

			for i, arg := range result {
				if arg != tt.expected[i] {
					t.Errorf("BuildReverseForwardArgs()[%d] = %q, want %q", i, arg, tt.expected[i])
				}
			}
		})
	}
}

func TestWellKnownPorts(t *testing.T) {
	// Verify well-known ports are properly configured
	if len(WellKnownPorts) == 0 {
		t.Error("WellKnownPorts should not be empty")
	}

	seenPorts := make(map[int]bool)
	for _, forward := range WellKnownPorts {
		// Check for valid port number
		if forward.Port <= 0 || forward.Port > 65535 {
			t.Errorf("Invalid port number: %d", forward.Port)
		}

		// Check for duplicate ports
		if seenPorts[forward.Port] {
			t.Errorf("Duplicate port in WellKnownPorts: %d", forward.Port)
		}
		seenPorts[forward.Port] = true

		// Check for description
		if forward.Description == "" {
			t.Errorf("Missing description for port %d", forward.Port)
		}
	}

	// Verify expected ports are present
	expectedPorts := map[int]string{
		1234:   "LM Studio",
		11434:  "Ollama",
	}

	for port, desc := range expectedPorts {
		found := false
		for _, forward := range WellKnownPorts {
			if forward.Port == port {
				found = true
				if forward.Description != desc {
					t.Logf("Port %d description: got %q, expected %q", port, forward.Description, desc)
				}
				break
			}
		}
		if !found {
			t.Errorf("Expected port %d (%s) not found in WellKnownPorts", port, desc)
		}
	}
}

func TestReversePortForwardIntegration(t *testing.T) {
	// Create a test server to simulate a bound port
	listener, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("Failed to create test listener: %v", err)
	}
	defer listener.Close()

	addr := listener.Addr().(*net.TCPAddr)
	testPort := addr.Port

	// Temporarily add our test port to the configuration
	originalPorts := WellKnownPorts
	defer func() { WellKnownPorts = originalPorts }()

	WellKnownPorts = []ReversePortForward{
		{Port: testPort, Description: "Test Service", Enabled: true},
	}

	// Give the listener a moment to be fully ready
	time.Sleep(10 * time.Millisecond)

	// Get bound forwards
	forwards := GetBoundReverseForwards()

	// Should find our test port
	if len(forwards) != 1 {
		t.Errorf("Expected 1 forward, got %d", len(forwards))
	}

	if len(forwards) > 0 && forwards[0].Port != testPort {
		t.Errorf("Expected port %d, got %d", testPort, forwards[0].Port)
	}

	// Build SSH args
	args := BuildReverseForwardArgs(forwards)
	expectedArgs := []string{"-R", fmt.Sprintf("%d:localhost:%d", testPort, testPort)}

	if len(args) != len(expectedArgs) {
		t.Errorf("Expected %d args, got %d", len(expectedArgs), len(args))
	}

	for i, arg := range args {
		if arg != expectedArgs[i] {
			t.Errorf("args[%d] = %q, want %q", i, arg, expectedArgs[i])
		}
	}
}

func TestGetBoundReverseForwards_DisabledPorts(t *testing.T) {
	// Save original configuration
	originalPorts := WellKnownPorts
	defer func() { WellKnownPorts = originalPorts }()

	// Create a test server
	listener, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("Failed to create test listener: %v", err)
	}
	defer listener.Close()

	addr := listener.Addr().(*net.TCPAddr)
	testPort := addr.Port

	// Configure with a disabled port
	WellKnownPorts = []ReversePortForward{
		{Port: testPort, Description: "Disabled Service", Enabled: false},
	}

	// Get bound forwards - should be empty since the port is disabled
	forwards := GetBoundReverseForwards()

	if len(forwards) != 0 {
		t.Errorf("Expected 0 forwards for disabled port, got %d", len(forwards))
	}
}

// TestBuildSSHArgsWithReverseForwards verifies integration with args.go
func TestBuildSSHArgsWithReverseForwards(t *testing.T) {
	// Save original configuration
	originalPorts := WellKnownPorts
	defer func() { WellKnownPorts = originalPorts }()

	// Create a test server
	listener, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("Failed to create test listener: %v", err)
	}
	defer listener.Close()

	addr := listener.Addr().(*net.TCPAddr)
	testPort := addr.Port

	// Configure with our test port
	WellKnownPorts = []ReversePortForward{
		{Port: testPort, Description: "Test Service", Enabled: true},
	}

	// Build SSH args
	args := CommandLineArgs{}
	sshArgs := args.BuildSSHArgs("/tmp/test.sock", 8080)

	// Verify the test port is included
	expectedForward := fmt.Sprintf("%d:localhost:%d", testPort, testPort)
	found := false
	for i := 0; i < len(sshArgs)-1; i++ {
		if sshArgs[i] == "-R" && sshArgs[i+1] == expectedForward {
			found = true
			break
		}
	}

	if !found {
		t.Errorf("Expected reverse forward -R %s not found in SSH args: %v", expectedForward, sshArgs)
	}
}
