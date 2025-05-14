package main

import (
	"context"
	"fmt"
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
		sig := <-sigChan
		fmt.Printf("\\nMain: Received signal: %v. Initiating shutdown...\\n", sig)
		cancel() // Propagate cancellation through the context.
	}()

	// Parse command line arguments
	args := ParseArgs()

	// Setup server for authentication
	serverConfig, err := SetupServer(ctx)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer serverConfig.Listener.Close()

	// If no codespace name is provided, prompt for selection
	if args.CodespaceName == "" {
		selectedName, err := SelectCodespace(ctx, args.Repo, args.RepoOwner)
		if err != nil {
			fmt.Printf("Error selecting codespace: %v\n", err)
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
		fmt.Printf("Warning: Failed to start port monitor: %v\n", err)
		return
	}
	defer func() {
		fmt.Println("Main: Signaling port monitor to stop...")
		monitorController.Stop() // Signal stop
		fmt.Println("Main: Waiting for port monitor to clean up...")
		monitorController.Wait() // Wait for cleanup
		fmt.Println("Main: Port monitor cleanup complete.")
	}()

	// Upload auth helpers
	fmt.Println("Setting up Azure DevOps authentication...")
	if err := UploadAuthHelpers(ctx, args.CodespaceName); err != nil {
		fmt.Printf("Warning: Failed to set up auth helpers: %v\n", err)
		// Continue anyway, as SSH might still work without auth helpers
	} else {
		fmt.Println("Azure DevOps authentication setup complete.")
	}

	// Execute the command
	fmt.Println("Main: Executing gh codespace ssh command...")
	// Pass the cancellable context to gh.ExecInteractive
	gh.ExecInteractive(ctx, finalArgs...)
	fmt.Println("Main: gh.ExecInteractive finished.")
}
