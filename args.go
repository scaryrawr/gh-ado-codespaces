package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

const localServiceHost = "127.0.0.1"

// CommandLineArgs holds all the command line arguments
type CommandLineArgs struct {
	CodespaceName       string
	Config              bool
	Debug               bool
	DebugFile           string
	AzureSubscriptionId string
	Herdr               bool
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
	herdrFlag := flag.Bool("herdr", false, "Connect with Herdr instead of an interactive SSH session")
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
		Herdr:               *herdrFlag,
		Logs:                *logsFlag,
		Profile:             *profile,
		Repo:                actualRepo,
		RepoOwner:           *repoOwner,
		ServerPort:          *serverPort,
		RemainingArgs:       flag.Args(),
	}
}

// Validate checks command-line option combinations that cannot be supported.
func (args *CommandLineArgs) Validate() error {
	if !args.Herdr {
		return nil
	}

	if args.Config {
		return fmt.Errorf("--config cannot be combined with --herdr")
	}

	if args.Profile != "" {
		return fmt.Errorf("--profile cannot be combined with --herdr because GitHub CLI does not support it with generated SSH config")
	}

	if args.ServerPort != 0 {
		return fmt.Errorf("--server-port cannot be combined with --herdr because GitHub CLI does not support it with generated SSH config")
	}

	if len(args.RemainingArgs) > 0 {
		return fmt.Errorf("SSH arguments after -- cannot be used with --herdr")
	}

	return nil
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
func (args *CommandLineArgs) BuildSSHArgs(socketPath string, port int, browserService *BrowserService, notificationService *NotificationService) []string {
	sshArgs := []string{"--"} // Start with the separator

	for _, forward := range buildRemoteForwards(socketPath, port, browserService, notificationService) {
		sshArgs = append(sshArgs, "-R", forward.argument())
	}

	if supportsX11Tunneling() {
		sshArgs = append(sshArgs, "-Y")
	}

	sshArgs = append(sshArgs, "-t")

	// Append remaining user-provided arguments (ssh flags or command)
	sshArgs = append(sshArgs, args.RemainingArgs...)

	return sshArgs
}

type remoteForward struct {
	remote string
	local  string
}

func (forward remoteForward) argument() string {
	return forward.remote + ":" + forward.local
}

func buildRemoteForwards(socketPath string, port int, browserService *BrowserService, notificationService *NotificationService) []remoteForward {
	forwards := []remoteForward{{
		remote: socketPath,
		local:  fmt.Sprintf("%s:%d", localServiceHost, port),
	}}

	if browserService != nil {
		forwards = append(forwards, remoteForward{
			remote: browserService.SocketPath,
			local:  fmt.Sprintf("%s:%d", localServiceHost, browserService.Port),
		})
	}

	if notificationService != nil {
		forwards = append(forwards, remoteForward{
			remote: notificationService.SocketPath,
			local:  fmt.Sprintf("%s:%d", localServiceHost, notificationService.Port),
		})
	}

	boundForwards := GetBoundReverseForwards()
	if len(boundForwards) > 0 {
		LogReverseForwards(boundForwards)
		for _, forward := range boundForwards {
			forwards = append(forwards, remoteForward{
				remote: fmt.Sprint(forward.Port),
				local:  fmt.Sprintf("localhost:%d", forward.Port),
			})
		}
	}

	return forwards
}

// supportsX11Tunneling reports whether the host has an X11 display available for forwarding.
func supportsX11Tunneling() bool {
	display, present := os.LookupEnv("DISPLAY")
	return present && strings.TrimSpace(display) != ""
}
