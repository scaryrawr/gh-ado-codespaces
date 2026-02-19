package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

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

	cfg, cfgErr := LoadAppConfig()
	if cfgErr != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to load config: %v\n", cfgErr)
		cfg = AppConfig{}
	}

	// Only resolve the current GitHub login when per-account reversePortForward
	// settings or a per-login Azure subscription override are actually needed,
	// to avoid an unnecessary `gh api user` network call on every run.
	needLogin := args.AzureSubscriptionId != ""
	if !needLogin {
		for _, acct := range cfg.Accounts {
			if len(acct.ReversePortForward) > 0 {
				needLogin = true
				break
			}
		}
	}
	var login string
	var loginErr error
	if needLogin {
		login, loginErr = currentGitHubLogin()
		if loginErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: unable to determine active GitHub login for config overrides: %v\n", loginErr)
		}
	}

	if needLogin && loginErr == nil {
		WellKnownPorts = cfg.ReversePortForwardsForLogin(login)
	} else {
		WellKnownPorts = MergeReversePortForwards(WellKnownPorts, cfg.ReversePortForward)
	}

	// Persist Azure subscription ID override early so subsequent auth setup sees it.
	if args.AzureSubscriptionId != "" {
		if loginErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: unable to determine GitHub login to store Azure subscription: %v\n", loginErr)
		} else {
			cfg.SetAzureSubscriptionForLogin(login, args.AzureSubscriptionId)
			if err := SaveAppConfig(cfg); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to save Azure subscription to config: %v\n", err)
			} else {
				fmt.Fprintf(os.Stderr, "Stored Azure subscription ID for login '%s' in config.\n", login)
			}
		}
	}

	// Setup server and (optionally) select codespace.
	// When we need to prompt for a codespace, run both in parallel since
	// SetupServer and SelectCodespace are independent.
	var serverConfig *ServerConfig
	if args.CodespaceName == "" {
		type serverResult struct {
			config *ServerConfig
			err    error
		}
		type codespaceResult struct {
			name string
			err  error
		}

		serverCh := make(chan serverResult, 1)
		codespaceCh := make(chan codespaceResult, 1)

		go func() {
			cfg, err := SetupServer(ctx)
			serverCh <- serverResult{cfg, err}
		}()

		go func() {
			name, err := SelectCodespace(ctx, args.Repo, args.RepoOwner)
			codespaceCh <- codespaceResult{name, err}
		}()

		sr := <-serverCh
		cr := <-codespaceCh

		if sr.err != nil || cr.err != nil {
			if sr.config != nil {
				sr.config.Listener.Close()
			}

			return
		}

		serverConfig = sr.config
		args.CodespaceName = cr.name
	} else {
		var err error
		serverConfig, err = SetupServer(ctx)
		if err != nil {
			return
		}
	}
	defer serverConfig.Listener.Close()

	// Initialize session ID now that we have the codespace name
	initializeSessionID(args.CodespaceName)

	// Start the browser service early so we can include its port in SSH args
	var browserService *BrowserService
	browserService, err := NewBrowserService(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to start browser service: %v\n", err)
		// Continue anyway, SSH will still work without browser forwarding
	} else {
		defer browserService.Stop()
	}

	// Start the notification service early so we can include its port in SSH args
	var notificationService *NotificationService
	notificationService, err = NewNotificationService(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to start notification service: %v\n", err)
		// Continue anyway, SSH will still work without notification forwarding
	} else {
		defer notificationService.Stop()
	}

	// Build command line arguments for gh
	ghFlags := args.BuildGHFlags()
	sshArgs := args.BuildSSHArgs(serverConfig.SocketPath, serverConfig.Port, browserService, notificationService)

	// Combine all arguments
	finalArgs := append(ghFlags, sshArgs...)

	// Upload all scripts and configure them in a single SSH call
	if err := prepareCodespaceScripts(ctx, args.CodespaceName, browserService != nil, notificationService != nil); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to prepare codespace scripts: %v\n", err)
	}

	// Print instructions for notification service if it's running and script upload succeeded
	if notificationService != nil {
		fmt.Fprintf(os.Stderr, "Command completion notifications available! To enable, add to your shell config:\n")
		fmt.Fprintf(os.Stderr, "  # For bash (~/.bashrc) or zsh (~/.zshrc)\n")
		fmt.Fprintf(os.Stderr, "  if [ -f \"$HOME/notification-sender.sh\" ]; then\n")
		fmt.Fprintf(os.Stderr, "      source \"$HOME/notification-sender.sh\"\n")
		fmt.Fprintf(os.Stderr, "  fi\n")
		fmt.Fprintf(os.Stderr, "  # For fish with done plugin (~/.config/fish/config.fish)\n")
		fmt.Fprintf(os.Stderr, "  set -U __done_allow_nongraphical 1\n")
		fmt.Fprintf(os.Stderr, "  set -U __done_notification_command \"~/notification-sender.sh send \\$title \\$message\"\n\n")
	}

	// Start the port monitor in the background
	monitorController, err := StartPortMonitor(ctx, args.CodespaceName)
	if err != nil {
		return
	}
	defer func() {
		monitorController.Stop() // Signal stop
		monitorController.Wait() // Wait for cleanup
	}()

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

// prepareCodespaceScripts writes all helper scripts to the codespace in a
// single SSH call using base64-encoded content, then makes them executable
// and creates symlinks. This avoids multiple expensive relay connections.
func prepareCodespaceScripts(ctx context.Context, codespaceName string, hasBrowserService, hasNotificationService bool) error {
	var cmdParts []string

	// Base64-encode and write auth helper to two destinations
	authB64 := base64.StdEncoding.EncodeToString([]byte(adoAuthHelperScript))
	cmdParts = append(cmdParts,
		fmt.Sprintf("printf %%s %s | base64 -d > ~/ado-auth-helper && cp ~/ado-auth-helper ~/azure-auth-helper", authB64))

	// Base64-encode and write port monitor script
	portB64 := base64.StdEncoding.EncodeToString([]byte(portMonitorScript))
	cmdParts = append(cmdParts,
		fmt.Sprintf("printf %%s %s | base64 -d > ~/port-monitor.sh", portB64))

	// Browser opener (only if browser service is available)
	if hasBrowserService {
		browserB64 := base64.StdEncoding.EncodeToString([]byte(browserOpenerScript))
		cmdParts = append(cmdParts,
			fmt.Sprintf("printf %%s %s | base64 -d > ~/browser-opener.sh", browserB64))
	}

	// Notification sender (only if notification service is available)
	if hasNotificationService {
		notifB64 := base64.StdEncoding.EncodeToString([]byte(notificationSenderScript))
		cmdParts = append(cmdParts,
			fmt.Sprintf("printf %%s %s | base64 -d > ~/notification-sender.sh", notifB64))
	}

	// xdg-open wrapper (always uploaded; handles its own fallbacks)
	xdgB64 := base64.StdEncoding.EncodeToString([]byte(xdgOpenScript))
	cmdParts = append(cmdParts,
		fmt.Sprintf("printf %%s %s | base64 -d > ~/xdg-open.sh", xdgB64))

	// Make all scripts executable
	chmodFiles := "~/ado-auth-helper ~/azure-auth-helper ~/port-monitor.sh ~/xdg-open.sh"
	if hasBrowserService {
		chmodFiles += " ~/browser-opener.sh"
	}
	if hasNotificationService {
		chmodFiles += " ~/notification-sender.sh"
	}
	cmdParts = append(cmdParts, "chmod +x "+chmodFiles)

	// Create symlinks for auth helpers
	cmdParts = append(cmdParts,
		"(test -L /usr/local/bin/ado-auth-helper || sudo ln -sf ~/ado-auth-helper /usr/local/bin/ado-auth-helper)")
	cmdParts = append(cmdParts,
		"(test -L /usr/local/bin/azure-auth-helper || sudo ln -sf ~/azure-auth-helper /usr/local/bin/azure-auth-helper)")
	cmdParts = append(cmdParts,
		"(test -L /usr/local/bin/xdg-open || sudo ln -sf ~/xdg-open.sh /usr/local/bin/xdg-open)")

	// Clean up stale sockets
	if cleanupCmd := buildStaleSocketCleanupCommand(hasBrowserService, hasNotificationService); cleanupCmd != "" {
		cmdParts = append(cmdParts, cleanupCmd)
	}

	fullCmd := strings.Join(cmdParts, " && ")

	args := append([]string{"codespace", "ssh", "--codespace", codespaceName, "--"}, wrapBashLoginCommand(fullCmd)...)
	_, stderr, err := gh.Exec(args...)
	if err != nil {
		return fmt.Errorf("error preparing scripts: %w\nStderr: %s", err, stderr.String())
	}

	// Print success messages
	fmt.Fprintln(os.Stderr, "ADO and Azure auth helpers uploaded to the codespace and made executable")
	fmt.Fprintln(os.Stderr, "xdg-open installed at /usr/local/bin/xdg-open")
	if hasBrowserService {
		fmt.Fprintf(os.Stderr, "\nBrowser opener available! To enable browser forwarding, add to your shell config:\n")
		fmt.Fprintf(os.Stderr, "  export BROWSER=\"$HOME/browser-opener.sh\"\n\n")
	}

	return nil
}

func buildStaleSocketCleanupCommand(hasBrowserService, hasNotificationService bool) string {
	var cleanupCommands []string

	if hasBrowserService {
		cleanupCommands = append(cleanupCommands, `for socket in /tmp/gh-ado-browser-*.sock; do [ -S "$socket" ] || continue; if ! curl -s --max-time 1 --unix-socket "$socket" "http://localhost/" >/dev/null 2>&1; then rm -f "$socket"; fi; done`)
	}

	if hasNotificationService {
		cleanupCommands = append(cleanupCommands, `for socket in /tmp/gh-ado-notification-*.sock; do [ -S "$socket" ] || continue; if ! curl -s --max-time 1 --unix-socket "$socket" "http://localhost/" >/dev/null 2>&1; then rm -f "$socket"; fi; done`)
	}

	if len(cleanupCommands) == 0 {
		return ""
	}

	return "if command -v curl >/dev/null 2>&1; then " + strings.Join(cleanupCommands, " ; ") + "; fi"
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
