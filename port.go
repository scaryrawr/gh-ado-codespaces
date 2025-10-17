package main

import (
	"fmt"
	"net"
	"os"
)

// ReversePortForward represents a reverse port forward configuration
type ReversePortForward struct {
	Port        int
	Description string
	Enabled     bool
}

// WellKnownPorts defines commonly used AI service ports that should be forwarded
var WellKnownPorts = []ReversePortForward{
	{Port: 1234, Description: "LM Studio", Enabled: true},
	{Port: 11434, Description: "Ollama", Enabled: true},
}

// isPortBound checks if a port is bound on the local machine
func isPortBound(port int) bool {
	conn, err := net.Dial("tcp", fmt.Sprintf("localhost:%d", port))
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// GetBoundReverseForwards returns a list of ports that should be reverse forwarded
// based on what's currently bound on the local machine
func GetBoundReverseForwards() []ReversePortForward {
	var boundPorts []ReversePortForward

	for _, forward := range WellKnownPorts {
		if !forward.Enabled {
			continue
		}

		if isPortBound(forward.Port) {
			boundPorts = append(boundPorts, forward)
		}
	}

	return boundPorts
}

// LogReverseForwards logs information about detected reverse port forwards
func LogReverseForwards(forwards []ReversePortForward) {
	if len(forwards) == 0 {
		return
	}

	fmt.Fprintf(os.Stderr, "Detected local services for reverse forwarding:\n")
	for _, forward := range forwards {
		fmt.Fprintf(os.Stderr, "  • %s (port %d) → will be accessible in codespace\n", forward.Description, forward.Port)
	}
}

// BuildReverseForwardArgs generates SSH -R arguments for the given port forwards
func BuildReverseForwardArgs(forwards []ReversePortForward) []string {
	var args []string

	for _, forward := range forwards {
		forwardSpec := fmt.Sprintf("%d:localhost:%d", forward.Port, forward.Port)
		args = append(args, "-R", forwardSpec)
	}

	return args
}
