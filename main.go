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

	// Handle --logs flag
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

// ListRecentLogFiles lists recent log files in reverse chronological order
func ListRecentLogFiles() {
	logDir := getLogDirectory()

	// Check if log directory exists
	if _, err := os.Stat(logDir); os.IsNotExist(err) {
		fmt.Printf("No log directory found at: %s\n", logDir)
		return
	}

	// Read directory contents
	entries, err := os.ReadDir(logDir)
	if err != nil {
		fmt.Printf("Error reading log directory: %v\n", err)
		return
	}

	// Filter and collect log files with their info
	type logFileInfo struct {
		name    string
		path    string
		modTime time.Time
		size    int64
	}

	var logFiles []logFileInfo

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		// Match log file patterns: azure-auth-* and port-monitor-*
		if strings.HasPrefix(name, "azure-auth-") || strings.HasPrefix(name, "port-monitor-") {
			if strings.HasSuffix(name, ".log") {
				fullPath := filepath.Join(logDir, name)
				info, err := entry.Info()
				if err != nil {
					continue
				}

				logFiles = append(logFiles, logFileInfo{
					name:    name,
					path:    fullPath,
					modTime: info.ModTime(),
					size:    info.Size(),
				})
			}
		}
	}

	if len(logFiles) == 0 {
		fmt.Printf("No log files found in: %s\n", logDir)
		return
	}

	// Sort by modification time (newest first)
	sort.Slice(logFiles, func(i, j int) bool {
		return logFiles[i].modTime.After(logFiles[j].modTime)
	})

	fmt.Printf("Recent log files in %s:\n\n", logDir)

	for _, logFile := range logFiles {
		// Format file size
		sizeStr := formatFileSize(logFile.size)

		// Format timestamp
		timeStr := logFile.modTime.Format("2006-01-02 15:04:05")

		// Determine log type
		logType := "unknown"
		if strings.HasPrefix(logFile.name, "azure-auth-") {
			logType = "auth"
		} else if strings.HasPrefix(logFile.name, "port-monitor-") {
			logType = "port-monitor"
		}

		fmt.Printf("%-15s %s  %8s  %s\n", logType, timeStr, sizeStr, logFile.path)
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
