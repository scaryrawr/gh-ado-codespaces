package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	// Added for time.Sleep
	"github.com/cli/go-gh/v2"
)

// Global session ID for this application instance
var sessionID string

func main() {
	// Create a cancellable context from context.Background().
	// cancel will be called when main exits or when an OS signal is received.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // Ensure cancel is called eventually

	// Set up a channel to listen for OS interrupt signals.
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start a goroutine to handle received signals.
	go func() {
		<-sigChan // Just receive the signal, no need to store it
		cancel()  // Propagate cancellation through the context.
	}()

	// Parse command line arguments
	args := ParseArgs()

	// Handle --logs flag (before initializing session)
	if args.Logs {
		ListRecentLogFiles()
		return
	}

	// Setup server for authentication
	serverConfig, err := SetupServer(ctx)
	if err != nil {
		return
	}
	defer serverConfig.Listener.Close()

	// If no codespace name is provided, prompt for selection
	if args.CodespaceName == "" {
		selectedName, err := SelectCodespace(ctx, args.Repo, args.RepoOwner)
		if err != nil {
			return
		}
		args.CodespaceName = selectedName
	}

	// Initialize session ID now that we have the codespace name
	initializeSessionID(args.CodespaceName)
	// Build command line arguments for gh
	ghFlags := args.BuildGHFlags()
	sshArgs := args.BuildSSHArgs(serverConfig.SocketPath, serverConfig.Port)

	// Combine all arguments
	finalArgs := append(ghFlags, sshArgs...)

	// Start the port monitor in the background
	monitorController, err := StartPortMonitor(ctx, args.CodespaceName)
	if err != nil {
		return
	}
	defer func() {
		monitorController.Stop() // Signal stop
		monitorController.Wait() // Wait for cleanup
	}()

	// Upload auth helpers
	if err := UploadAuthHelpers(ctx, args.CodespaceName); err != nil {
		// Continue anyway, as SSH might still work without auth helpers
	}

	// Execute the command
	// Pass the cancellable context to gh.ExecInteractive
	gh.ExecInteractive(ctx, finalArgs...)
}

// initializeSessionID creates a session ID including the codespace name
func initializeSessionID(codespaceName string) {
	timestamp := time.Now().Format("2006-01-02_150405")
	pid := os.Getpid()

	// Sanitize codespace name for use in directory name
	safeName := sanitizeForFilename(codespaceName)
	if safeName == "" {
		safeName = "unknown-codespace"
	}

	sessionID = fmt.Sprintf("%s_session-%s-pid%d", safeName, timestamp, pid)
}

// sanitizeForFilename removes or replaces characters that aren't safe for filenames
func sanitizeForFilename(name string) string {
	if name == "" {
		return ""
	}

	// Replace problematic characters with safe alternatives
	result := strings.ReplaceAll(name, "/", "-")
	result = strings.ReplaceAll(result, "\\", "-")
	result = strings.ReplaceAll(result, ":", "-")
	result = strings.ReplaceAll(result, "*", "-")
	result = strings.ReplaceAll(result, "?", "-")
	result = strings.ReplaceAll(result, "\"", "-")
	result = strings.ReplaceAll(result, "<", "-")
	result = strings.ReplaceAll(result, ">", "-")
	result = strings.ReplaceAll(result, "|", "-")
	result = strings.ReplaceAll(result, " ", "-")

	// Remove leading/trailing dashes and limit length
	result = strings.Trim(result, "-")
	if len(result) > 50 {
		result = result[:50]
	}

	return result
}

// getSessionLogDirectory returns the session-specific log directory
func getSessionLogDirectory() string {
	baseLogDir := getLogDirectory()
	return filepath.Join(baseLogDir, sessionID)
}

// getSessionLogPath returns the full path for a specific log file in the current session
func getSessionLogPath(logFileName string) string {
	sessionDir := getSessionLogDirectory()
	return filepath.Join(sessionDir, logFileName)
}

// ensureSessionLogDirectory creates the session log directory if it doesn't exist
func ensureSessionLogDirectory() error {
	sessionDir := getSessionLogDirectory()
	return os.MkdirAll(sessionDir, 0755)
}

// ListRecentLogFiles lists recent log files in reverse chronological order
func ListRecentLogFiles() {
	logDir := getLogDirectory()

	// Check if log directory exists
	if _, err := os.Stat(logDir); os.IsNotExist(err) {
		fmt.Printf("No log directory found at: %s\n", logDir)
		return
	}

	// Read directory contents to find session directories
	entries, err := os.ReadDir(logDir)
	if err != nil {
		fmt.Printf("Error reading log directory: %v\n", err)
		return
	}

	// Filter and collect session directories with their info
	type sessionLogFile struct {
		name    string
		path    string
		size    int64
		logType string
	}

	type sessionInfo struct {
		name          string
		path          string
		modTime       time.Time
		codespaceName string
		logFiles      []sessionLogFile
	}
	var sessions []sessionInfo

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		name := entry.Name()
		var codespaceName string

		// Match both old and new session directory patterns
		if strings.HasPrefix(name, "session-") {
			// Old format: session-timestamp-pid
			codespaceName = "unknown"
		} else if strings.Contains(name, "_session-") {
			// New format: codespacename_session-timestamp-pid
			parts := strings.SplitN(name, "_session-", 2)
			codespaceName = parts[0]
		} else {
			continue
		}

		sessionPath := filepath.Join(logDir, name)
		info, err := entry.Info()
		if err != nil {
			continue
		}

		// Read log files in this session directory
		sessionEntries, err := os.ReadDir(sessionPath)
		if err != nil {
			continue
		}

		var logFiles []sessionLogFile
		for _, sessionEntry := range sessionEntries {
			if sessionEntry.IsDir() {
				continue
			}

			fileName := sessionEntry.Name()
			if strings.HasSuffix(fileName, ".log") {
				fileInfo, err := sessionEntry.Info()
				if err != nil {
					continue
				}

				logType := "unknown"
				if fileName == "azure-auth.log" {
					logType = "auth"
				} else if fileName == "port-monitor.log" {
					logType = "port-monitor"
				}

				logFiles = append(logFiles, sessionLogFile{
					name:    fileName,
					path:    filepath.Join(sessionPath, fileName),
					size:    fileInfo.Size(),
					logType: logType,
				})
			}
		}

		if len(logFiles) > 0 {
			sessions = append(sessions, sessionInfo{
				name:          name,
				path:          sessionPath,
				modTime:       info.ModTime(),
				codespaceName: codespaceName,
				logFiles:      logFiles,
			})
		}
	}

	if len(sessions) == 0 {
		fmt.Printf("No session log directories found in: %s\n", logDir)
		return
	}

	// Sort sessions by modification time (newest first)
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].modTime.After(sessions[j].modTime)
	})

	fmt.Printf("Recent log sessions in %s:\n\n", logDir)

	for _, session := range sessions {
		// Format timestamp
		timeStr := session.modTime.Format("2006-01-02 15:04:05")

		fmt.Printf("Session: %s (%s) - Codespace: %s\n", session.name, timeStr, session.codespaceName)

		for _, logFile := range session.logFiles {
			// Format file size
			sizeStr := formatFileSize(logFile.size)
			fmt.Printf("  %-15s %8s  %s\n", logFile.logType, sizeStr, logFile.path)
		}
		fmt.Println()
	}
}

// formatFileSize formats file size in human-readable format
func formatFileSize(bytes int64) string {
	if bytes < 1024 {
		return fmt.Sprintf("%d B", bytes)
	} else if bytes < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(bytes)/1024)
	} else {
		return fmt.Sprintf("%.1f MB", float64(bytes)/(1024*1024))
	}
}
