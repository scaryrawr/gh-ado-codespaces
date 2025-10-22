package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CommandLineArgs holds all the command line arguments
type CommandLineArgs struct {
	CodespaceName       string
	Config              bool
	Debug               bool
	DebugFile           string
	AzureSubscriptionId string
	Logs                bool
	Profile             string
	Repo                string
	RepoOwner           string
	ServerPort          int
	RemainingArgs       []string
}

// ParseArgs parses command line arguments and returns a CommandLineArgs struct
func ParseArgs() CommandLineArgs {
	codespaceName := flag.String("codespace", "", "Name of the codespace")
	cFlag := flag.String("c", "", "Name of the codespace (shorthand for --codespace)")
	configFlag := flag.Bool("config", false, "Write OpenSSH configuration to stdout")
	debugFlag := flag.Bool("debug", false, "Log debug data to a file")
	dFlag := flag.Bool("d", false, "Log debug data to a file (shorthand for --debug)")
	debugFile := flag.String("debug-file", "", "Path of the file log to")
	logsFlag := flag.Bool("logs", false, "List recent log files and exit")
	azureSub := flag.String("azure-subscription-id", "", "Azure subscription ID to use for authentication (persisted per GitHub account)")
	// Allow an alternate flag name without -id suffix for convenience
	azureSubAlt := flag.String("azure-subscription", "", "Azure subscription ID to use for authentication (alias of --azure-subscription-id)")
	profile := flag.String("profile", "", "Name of the SSH profile to use")
	repo := flag.String("repo", "", "Filter codespace selection by repository name (user/repo)")
	RFlag := flag.String("R", "", "Filter codespace selection by repository name (user/repo) (shorthand for --repo)")
	repoOwner := flag.String("repo-owner", "", "Filter codespace selection by repository owner (username or org)")
	serverPort := flag.Int("server-port", 0, "SSH server port number (0 => pick unused)")

	flag.Parse()

	// Resolve conflicting flags
	actualCodespaceName := *codespaceName
	if *cFlag != "" {
		actualCodespaceName = *cFlag
	}

	actualRepo := *repo
	if *RFlag != "" { // This is the -R flag for gh, not for ssh
		actualRepo = *RFlag
	}

	actualDebug := *debugFlag || *dFlag

	// Resolve azure subscription flag precedence (primary then alias)
	actualAzureSub := *azureSub
	if actualAzureSub == "" && *azureSubAlt != "" {
		actualAzureSub = *azureSubAlt
	}

	return CommandLineArgs{
		CodespaceName:       actualCodespaceName,
		Config:              *configFlag,
		Debug:               actualDebug,
		DebugFile:           *debugFile,
		AzureSubscriptionId: strings.TrimSpace(actualAzureSub),
		Logs:                *logsFlag,
		Profile:             *profile,
		Repo:                actualRepo,
		RepoOwner:           *repoOwner,
		ServerPort:          *serverPort,
		RemainingArgs:       flag.Args(),
	}
}

// BuildGHFlags builds the arguments for the 'gh codespace ssh' command
func (args *CommandLineArgs) BuildGHFlags() []string {
	ghFlags := []string{"codespace", "ssh"}

	if args.CodespaceName != "" {
		ghFlags = append(ghFlags, "--codespace", args.CodespaceName)
	}

	if args.Config {
		ghFlags = append(ghFlags, "--config")
	}

	if args.Debug {
		ghFlags = append(ghFlags, "--debug")
	}

	if args.DebugFile != "" {
		ghFlags = append(ghFlags, "--debug-file", args.DebugFile)
	}

	if args.Profile != "" {
		ghFlags = append(ghFlags, "--profile", args.Profile)
	}

	if args.Repo != "" {
		ghFlags = append(ghFlags, "--repo", args.Repo)
	}

	if args.RepoOwner != "" {
		ghFlags = append(ghFlags, "--repo-owner", args.RepoOwner)
	}

	if args.ServerPort != 0 {
		ghFlags = append(ghFlags, "--server-port", fmt.Sprint(args.ServerPort))
	}

	return ghFlags
}

// BuildSSHArgs builds the arguments for the SSH command
func (args *CommandLineArgs) BuildSSHArgs(socketPath string, port int) []string {
	sshArgs := []string{"--"} // Start with the separator

	// Add the auth socket forward
	forwardSpec := fmt.Sprintf("%s:localhost:%d", socketPath, port)
	sshArgs = append(sshArgs, "-R", forwardSpec)

	// Detect and add reverse port forwards for local AI services
	boundForwards := GetBoundReverseForwards()
	if len(boundForwards) > 0 {
		LogReverseForwards(boundForwards)
		reverseArgs := BuildReverseForwardArgs(boundForwards)
		sshArgs = append(sshArgs, reverseArgs...)
	}

	sshArgs = append(sshArgs, "-t")

	// Append remaining user-provided arguments (ssh flags or command)
	sshArgs = append(sshArgs, args.RemainingArgs...)

	return sshArgs
}

// GetSSHControlPath returns the path for the SSH control socket for a given codespace
func GetSSHControlPath(codespaceName string) string {
	// Use a unique control path per codespace in the temp directory
	// This follows the pattern: /tmp/gh-ado-codespaces/ssh-control-<codespace-name>
	tempDir := os.TempDir()
	controlDir := filepath.Join(tempDir, "gh-ado-codespaces", "ssh-control")
	
	// Create the directory if it doesn't exist
	os.MkdirAll(controlDir, 0700)
	
	// Sanitize codespace name for use in filename
	safeName := sanitizeCodespaceNameForControl(codespaceName)
	return filepath.Join(controlDir, safeName)
}

// sanitizeCodespaceNameForControl sanitizes a codespace name for use in control socket path
func sanitizeCodespaceNameForControl(name string) string {
	if name == "" {
		return "unknown"
	}
	
	// Replace problematic characters with dashes
	result := strings.ReplaceAll(name, "/", "-")
	result = strings.ReplaceAll(result, "\\", "-")
	result = strings.ReplaceAll(result, ":", "-")
	result = strings.ReplaceAll(result, " ", "-")
	result = strings.ReplaceAll(result, "*", "-")
	result = strings.ReplaceAll(result, "?", "-")
	
	// Remove leading/trailing dashes and limit length
	result = strings.Trim(result, "-")
	if len(result) > 100 {
		result = result[:100]
	}
	
	return result
}

// BuildSSHMultiplexArgs builds SSH multiplexing arguments for a given control path and mode
func BuildSSHMultiplexArgs(controlPath string, isMaster bool) []string {
	var args []string
	
	if isMaster {
		// Master connection: auto-create the control socket if it doesn't exist
		// and allow reuse if it already exists
		args = append(args, "-o", "ControlMaster=auto")
	} else {
		// Slave connection: use existing control socket but don't create one
		args = append(args, "-o", "ControlMaster=no")
	}
	
	// Set the control path
	args = append(args, "-o", fmt.Sprintf("ControlPath=%s", controlPath))
	
	// Set a reasonable persist time (10 minutes after last use)
	args = append(args, "-o", "ControlPersist=600")
	
	return args
}
