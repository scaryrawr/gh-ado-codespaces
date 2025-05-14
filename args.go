package main

import (
	"flag"
	"fmt"
)

// CommandLineArgs holds all the command line arguments
type CommandLineArgs struct {
	CodespaceName string
	Config        bool
	Debug         bool
	DebugFile     string
	Profile       string
	Repo          string
	RepoOwner     string
	ServerPort    int
	RemainingArgs []string
}

// ParseArgs parses command line arguments and returns a CommandLineArgs struct
func ParseArgs() CommandLineArgs {
	codespaceName := flag.String("codespace", "", "Name of the codespace")
	cFlag := flag.String("c", "", "Name of the codespace (shorthand for --codespace)")
	configFlag := flag.Bool("config", false, "Write OpenSSH configuration to stdout")
	debugFlag := flag.Bool("debug", false, "Log debug data to a file")
	dFlag := flag.Bool("d", false, "Log debug data to a file (shorthand for --debug)")
	debugFile := flag.String("debug-file", "", "Path of the file log to")
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

	return CommandLineArgs{
		CodespaceName: actualCodespaceName,
		Config:        *configFlag,
		Debug:         actualDebug,
		DebugFile:     *debugFile,
		Profile:       *profile,
		Repo:          actualRepo,
		RepoOwner:     *repoOwner,
		ServerPort:    *serverPort,
		RemainingArgs: flag.Args(),
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
	sshArgs = append(sshArgs, "-R", fmt.Sprintf("%s:localhost:%d", socketPath, port))
	sshArgs = append(sshArgs, "-t")

	// Append remaining user-provided arguments (ssh flags or command)
	sshArgs = append(sshArgs, args.RemainingArgs...)

	return sshArgs
}
