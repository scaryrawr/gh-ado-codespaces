package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

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
