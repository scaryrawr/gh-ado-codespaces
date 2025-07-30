package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cli/go-gh/v2"
)

// Global variables for debug logging
var (
	debugLogFile *os.File
	debugLogger  *log.Logger
)

// initDebugLogger initializes a debug logger that writes to a file instead of stderr
func initDebugLogger() error {
	// Create log directory if it doesn't exist
	logDir := getLogDirectory()
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return fmt.Errorf("failed to create log directory: %w", err)
	}

	// Create log file with timestamp and process ID in name for uniqueness
	timestamp := time.Now().Format("2006-01-02_150405")
	pid := os.Getpid()
	logPath := filepath.Join(logDir, fmt.Sprintf("port-monitor-%s-pid%d.log", timestamp, pid))

	logFile, err := os.Create(logPath)
	if err != nil {
		return fmt.Errorf("failed to create log file: %w", err)
	}

	debugLogFile = logFile
	debugLogger = log.New(logFile, "", log.LstdFlags)

	debugLogger.Printf("Debug logging initialized to %s", logPath)
	return nil
}

// getLogDirectory returns the temporary directory for logs
func getLogDirectory() string {
	// Use the system's temporary directory
	tempDir := os.TempDir()
	return filepath.Join(tempDir, "gh-ado-codespaces", "logs")
}

// logDebug logs a message to the debug log file
func logDebug(format string, args ...interface{}) {
	if debugLogger != nil {
		debugLogger.Printf(format, args...)
	}
}

// closeDebugLogger closes the debug log file
func closeDebugLogger() {
	if debugLogFile != nil {
		logDebug("Closing debug logger")
		debugLogFile.Close()
		debugLogFile = nil
		debugLogger = nil
	}
}

// portMonitorScript contains the bash script that monitors ports on the codespace
const portMonitorScript = `#!/usr/bin/env bash

# Associative array to store currently bound ports
# Key: "protocol:port", Value: 1
declare -A bound_ports

# Function to send JSON messages to stdout
send_message() {
    local type="$1"
    # For type="port", $2=action, $3=port, $4=protocol
    # For type="log", $2=message
    local timestamp
    timestamp=$(date -u +"%Y-%m-%dT%H:%M:%S.%3NZ") # ISO 8601 format for Linux

    if [ "$type" = "port" ]; then
        local action="$2"
        local port_num="$3"
        local protocol_val="$4"
        jq -n -c \
          --arg type "port" \
          --arg action "$action" \
          --argjson port "$port_num" \
          --arg protocol "$protocol_val" \
          --arg timestamp "$timestamp" \
          '{type: $type, action: $action, port: $port, protocol: $protocol, timestamp: $timestamp}'
    elif [ "$type" = "log" ]; then
        local message="$2"
        jq -n -c \
          --arg type "log" \
          --arg message "$message" \
          --arg timestamp "$timestamp" \
          '{type: $type, message: $message, timestamp: $timestamp}'
    fi
}

# Cleanup function for graceful shutdown
cleanup() {
    send_message "log" "Signal received, shutting down port monitor..."
    exit 0
}

# Trap SIGINT (Ctrl+C) and SIGTERM signals
trap 'cleanup' SIGINT SIGTERM

# Initial starting message
send_message "log" "Port monitor starting..."

# Main monitoring loop
while true; do
    # Associative array to store ports found in the current scan
    declare -A current_ports_map
    unset current_ports_map
    declare -A current_ports_map

    # Read listening ports using ss
    # Process substitution <(...) is used to avoid issues with variables in subshells
    while IFS= read -r line; do
        # $1 is protocol (tcp/udp), $5 is LocalAddress:Port (e.g., 0.0.0.0:8080 or [::]:80)
        protocol=$(echo "$line" | awk '{print $1}')
        local_address_port=$(echo "$line" | awk '{print $5}')

        # Extract port from LocalAddress:Port (it's the part after the last colon)
        port=$(echo "$local_address_port" | awk -F: '{print $NF}')

        # Validate port is a number
        if ! [[ "$port" =~ ^[0-9]+$ ]]; then
            # Optional: send_message "log" "Failed to parse port from line: $line"
            continue
        fi

        # Filter out well-known ports (0-1023)
        if [ "$port" -le 1023 ]; then
            continue
        fi

        key="${protocol}:${port}"
        current_ports_map["$key"]=1

        # If this is a new port (not in our bound_ports list), record it and send 'bound' event
        if [[ -z "${bound_ports[$key]}" ]]; then
            bound_ports["$key"]=1
            send_message "port" "bound" "$port" "$protocol"
        fi
    done < <(ss -tulpn 2>/dev/null | grep LISTEN)

    # Check for unbound ports
    # Iterate over keys of bound_ports. If a key is not in current_ports_map, it means the port was unbound.
    for key_in_bound_ports in "${!bound_ports[@]}"; do
        if [[ -z "${current_ports_map[$key_in_bound_ports]}" ]]; then
            # Port is no longer bound
            protocol_val=$(echo "$key_in_bound_ports" | cut -d: -f1)
            port_val=$(echo "$key_in_bound_ports" | cut -d: -f2)

            send_message "port" "unbound" "$port_val" "$protocol_val"
            unset "bound_ports[$key_in_bound_ports]" # Remove from our tracked list
        fi
    done

    sleep 2 # Interval between checks
done`

// PortMessage represents a JSON message from port-monitor.sh
type PortMessage struct {
	Type      string `json:"type"`
	Action    string `json:"action"`
	Port      int    `json:"port"`
	Protocol  string `json:"protocol"`
	Timestamp string `json:"timestamp"`
}

// LogMessage represents a JSON log message from port-monitor.sh
type LogMessage struct {
	Type      string `json:"type"`
	Message   string `json:"message"`
	Timestamp string `json:"timestamp"`
}

// PortMonitorController allows controlling and waiting for the port monitor's cleanup.
type PortMonitorController struct {
	stopFunc  func()
	waitGroup *sync.WaitGroup
}

// Stop signals the port monitor to begin its shutdown process.
func (pmc *PortMonitorController) Stop() {
	if pmc.stopFunc != nil {
		logDebug("PortMonitorController: Stop() called")
		pmc.stopFunc()
	}
}

// Wait blocks until the port monitor has completed its shutdown and cleanup.
func (pmc *PortMonitorController) Wait() {
	if pmc.waitGroup != nil {
		logDebug("PortMonitorController: Wait() called, waiting for WaitGroup")
		pmc.waitGroup.Wait()
		logDebug("PortMonitorController: WaitGroup finished")
	}
}

// portForwardInfo tracks information about a port forwarding process
type portForwardInfo struct {
	active bool
	cmd    *exec.Cmd
}

// StartPortMonitor uploads and runs the port monitor script on the specified codespace
// It returns a PortMonitorController to manage the lifecycle of the monitor and an error if setup fails.
func StartPortMonitor(ctx context.Context, codespaceName string) (*PortMonitorController, error) {
	// Initialize the debug logger
	if err := initDebugLogger(); err != nil {
		return nil, fmt.Errorf("failed to initialize debug logger: %w", err)
	}

	// Print to stderr just once where logs are being written
	// logDir := getLogDirectory()
	// fmt.Fprintf(os.Stderr, "Port monitor logs will be written to: %s\n", logDir)

	logDebug("Starting port monitor for codespace: %s", codespaceName)

	// Create a new context with cancellation for the monitor itself
	monitorCtx, cancelMonitor := context.WithCancel(ctx)

	// Create a WaitGroup to wait for the main monitoring goroutine to finish
	var wg sync.WaitGroup
	wg.Add(1)

	// Start monitoring in a goroutine so it doesn't block the main thread
	go func() {
		// Ensure WaitGroup is decremented and debug logger is closed when this goroutine exits
		defer wg.Done()
		defer closeDebugLogger()

		logDebug("Port monitor goroutine started.")
		err := runPortMonitor(monitorCtx, codespaceName)
		if err != nil && err != context.Canceled && !strings.Contains(err.Error(), "context canceled") {
			logDebug("Error in port monitor: %v", err)
		} else {
			logDebug("Port monitor finished or was canceled.")
		}
	}()

	controller := &PortMonitorController{
		stopFunc:  cancelMonitor, // This function will cancel monitorCtx
		waitGroup: &wg,
	}

	logDebug("PortMonitorController created. Returning controller to caller.")
	return controller, nil
}

// runPortMonitor handles the actual port monitoring logic
func runPortMonitor(ctx context.Context, codespaceName string) error {
	// 1. Upload port-monitor.sh to the codespace
	err := uploadPortMonitorScript(ctx, codespaceName)
	if err != nil {
		return fmt.Errorf("failed to upload port monitor script: %w", err)
	}

	// 2. Make the script executable
	err = makeScriptExecutable(ctx, codespaceName)
	if err != nil {
		return fmt.Errorf("failed to make script executable: %w", err)
	}

	// 3. Run the script and process its output
	return runAndProcessOutput(ctx, codespaceName)
}

// uploadPortMonitorScript copies the port-monitor.sh script to the codespace
func uploadPortMonitorScript(ctx context.Context, codespaceName string) error {
	// Create a temporary file with the embedded script content
	tempFile, err := os.CreateTemp("", "port-monitor*.sh")
	if err != nil {
		return fmt.Errorf("failed to create temporary file: %w", err)
	}
	defer os.Remove(tempFile.Name())

	// Write the embedded script to the temporary file
	if _, err = tempFile.WriteString(portMonitorScript); err != nil {
		return fmt.Errorf("failed to write script to temporary file: %w", err)
	}
	tempFile.Close()

	// Use gh cs cp to copy the script to the codespace
	// Note: We use gh.Exec here because we just need the final result and don't need to track the process
	args := []string{"codespace", "cp", "-c", codespaceName, "-e", tempFile.Name(), "remote:~/port-monitor.sh"}
	_, stderr, err := gh.Exec(args...)
	if err != nil {
		return fmt.Errorf("error copying script to codespace: %w\nStderr: %s", err, stderr.String())
	}

	return nil
}

// makeScriptExecutable makes port-monitor.sh executable on the codespace
func makeScriptExecutable(ctx context.Context, codespaceName string) error {
	args := []string{"codespace", "ssh", "--codespace", codespaceName, "--", "chmod", "+x", "~/port-monitor.sh"}
	// Note: We use gh.Exec here because we just need to run the command once and get the result
	_, stderr, err := gh.Exec(args...)
	if err != nil {
		return fmt.Errorf("error making script executable: %w\nStderr: %s", err, stderr.String())
	}

	return nil
}

// runAndProcessOutput runs the port-monitor.sh script and processes its output
func runAndProcessOutput(ctx context.Context, codespaceName string) error {
	// Start the port-monitor.sh script on the codespace
	args := []string{"codespace", "ssh", "--codespace", codespaceName, "--", "~/port-monitor.sh"}

	// Note: We use exec.CommandContext instead of gh.Exec here because:
	// 1. We need to process the JSON output line-by-line as it's produced in real-time
	// 2. The port monitor is a long-running process that continuously outputs data
	cmd := exec.CommandContext(ctx, "gh", args...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to get stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to get stderr pipe: %w", err)
	}

	// Start the command
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start command: %w", err)
	}

	// Process stderr in a goroutine
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			logDebug("Port Monitor Error: %s", scanner.Text())
		}
	}()

	// Map to track active forwarded ports and their associated commands
	portForwards := make(map[int]portForwardInfo)

	// Make sure to clean up port forwards when this function returns
	defer func() {
		cleanupPortForwards(portForwards)
	}()

	// Create a separate context for port forwarding that we can cancel explicitly
	// when the function exits
	forwardingCtx, cancelForwarding := context.WithCancel(ctx)
	defer cancelForwarding()

	// Create a done channel to signal when processing is done
	done := make(chan struct{})

	// Process stdout in a separate goroutine
	go func() {
		defer close(done)
		scanner := bufio.NewScanner(stdout)

		for scanner.Scan() {
			line := scanner.Text()

			// Check if context was canceled
			select {
			case <-ctx.Done():
				logDebug("Context canceled while processing port monitor output")
				return
			default:
				// Continue processing
			}

			// Try to parse the JSON message
			var message json.RawMessage
			if err := json.Unmarshal([]byte(line), &message); err != nil {
				// Not JSON, just log it
				logDebug("Port Monitor: %s", line)
				continue
			}

			// Determine if it's a port message or a log message
			var typeCheck struct {
				Type string `json:"type"`
			}

			if err := json.Unmarshal(message, &typeCheck); err != nil {
				continue
			}

			switch typeCheck.Type {
			case "port":
				var portMsg PortMessage
				if err := json.Unmarshal(message, &portMsg); err != nil {
					continue
				}

				// Process port message
				handlePortMessage(forwardingCtx, codespaceName, portMsg, portForwards)

			case "log":
				var logMsg LogMessage
				if err := json.Unmarshal(message, &logMsg); err != nil {
					continue
				}

				// Just log it for debugging
				logDebug("Port Monitor Log: %s", logMsg.Message)
			}
		}

		if err := scanner.Err(); err != nil {
			logDebug("Error reading script output: %v", err)
		}
	}()

	// Wait for either the command to finish or the context to be canceled
	waitErrCh := make(chan error, 1)
	go func() {
		waitErrCh <- cmd.Wait()
	}()

	select {
	case <-ctx.Done():
		// Context was canceled, clean up and return
		logDebug("Context canceled, cleaning up port monitor")
		// Try to kill the process if it's still running
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
		<-done // Wait for stdout processing to complete
		return ctx.Err()
	case err := <-waitErrCh:
		// Command finished
		<-done // Wait for stdout processing to complete
		return err
	}
}

// handlePortMessage processes a port event message from the script
func handlePortMessage(ctx context.Context, codespaceName string, msg PortMessage, portForwards map[int]portForwardInfo) {
	switch msg.Action {
	case "bound":
		// If not already forwarded or if previously unbound, start port forwarding
		info := portForwards[msg.Port]
		if !info.active {
			logDebug("Port %d bound, starting port forwarding", msg.Port)
			cmd := startPortForwarding(ctx, codespaceName, msg.Port)
			portForwards[msg.Port] = portForwardInfo{active: true, cmd: cmd}
		}

	case "unbound":
		// Get the port forwarding info
		info := portForwards[msg.Port]
		if info.active && info.cmd != nil && info.cmd.Process != nil {
			logDebug("Port %d unbound, stopping port forwarding", msg.Port)
			// Kill the port forwarding process
			if err := info.cmd.Process.Kill(); err != nil {
				logDebug("Failed to kill port forwarding for port %d: %v", msg.Port, err)
			} else {
				logDebug("Stopped port forwarding for port %d", msg.Port)
			}
		}
		// Mark the port as inactive but keep the entry in the map to remember we've seen it
		portForwards[msg.Port] = portForwardInfo{active: false, cmd: nil}
	}
}

// cleanupPortForwards stops all active port forwarding processes
func cleanupPortForwards(portForwards map[int]portForwardInfo) {
	logDebug("Cleaning up %d port forwarding processes", len(portForwards))
	for port, info := range portForwards {
		if info.active && info.cmd != nil && info.cmd.Process != nil {
			logDebug("Terminating port forwarding for port %d", port)
			if err := info.cmd.Process.Kill(); err != nil {
				logDebug("Error terminating port forwarding process for port %d: %v", port, err)
			}
		}
	}
}

// startPortForwarding starts port forwarding for the specified port
// Returns the command being executed for tracking purposes
// Note: We use exec.CommandContext instead of gh.Exec here because:
// 1. We need a reference to the process to kill it later when the port is unbound
// 2. Port forwarding is a long-running process that needs to run asynchronously
func startPortForwarding(ctx context.Context, codespaceName string, port int) *exec.Cmd {
	// Construct command args
	args := []string{"codespace", "ports", "forward", fmt.Sprintf("%d:%d", port, port), "--codespace", codespaceName}

	// Create the command with the provided context for proper cancellation
	cmd := exec.CommandContext(ctx, "gh", args...)

	// Buffer for stdout/stderr
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Log that we're starting port forwarding
	logDebug("Starting port forwarding for port %d on codespace %s", port, codespaceName)

	// Start the command asynchronously
	go func() {
		err := cmd.Run()
		if err != nil {
			// Check if this is due to context cancellation
			if ctx.Err() != nil {
				logDebug("Port forwarding for port %d stopped due to context cancellation", port)
				return
			}

			// Otherwise log the actual error
			errOutput := strings.TrimSpace(stderr.String())
			if errOutput == "" {
				errOutput = err.Error()
			}
			logDebug("Port forwarding for port %d failed: %s", port, errOutput)
		}
	}()

	return cmd
}
