package main

import (
	"flag"
	"fmt"
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
func (args *CommandLineArgs) BuildSSHArgs(socketPath string, port int, browserService *BrowserService) []string {
	sshArgs := []string{"--"} // Start with the separator

	// Add the auth socket forward
	forwardSpec := fmt.Sprintf("%s:localhost:%d", socketPath, port)
	sshArgs = append(sshArgs, "-R", forwardSpec)

	// Add browser port forward if browser service is available
	if browserService != nil {
		browserForwardSpec := fmt.Sprintf("%d:localhost:%d", browserService.Port, browserService.Port)
		sshArgs = append(sshArgs, "-R", browserForwardSpec)
	}

	// Detect and add reverse port forwards for local AI services
	boundForwards := GetBoundReverseForwards()
	if len(boundForwards) > 0 {
		LogReverseForwards(boundForwards)
		reverseArgs := BuildReverseForwardArgs(boundForwards)
		sshArgs = append(sshArgs, reverseArgs...)
	}

	sshArgs = append(sshArgs, "-t")

	// Append remaining user-provided arguments (ssh flags or command)
	if len(args.RemainingArgs) > 0 {
		sshArgs = append(sshArgs, args.RemainingArgs...)
	} else if browserService != nil {
		// If no command specified and browser service is active, set up environment
		sshArgs = append(sshArgs, fmt.Sprintf("export BROWSER='$HOME/browser-opener.sh' GH_ADO_CODESPACES_BROWSER_PORT=%d; bash -l", browserService.Port))
	}

	return sshArgs
}
