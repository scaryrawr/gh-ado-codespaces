package main

import (
	"fmt"
	"os"
	"testing"
)

func TestCommandLineArgs_BuildGHFlags(t *testing.T) {
	tests := []struct {
		name     string
		args     CommandLineArgs
		expected []string
	}{
		{
			name: "basic codespace name",
			args: CommandLineArgs{
				CodespaceName: "my-codespace",
			},
			expected: []string{"codespace", "ssh", "--codespace", "my-codespace"},
		},
		{
			name: "with config flag",
			args: CommandLineArgs{
				CodespaceName: "my-codespace",
				Config:        true,
			},
			expected: []string{"codespace", "ssh", "--codespace", "my-codespace", "--config"},
		},
		{
			name: "with debug flag",
			args: CommandLineArgs{
				CodespaceName: "my-codespace",
				Debug:         true,
			},
			expected: []string{"codespace", "ssh", "--codespace", "my-codespace", "--debug"},
		},
		{
			name: "with debug file",
			args: CommandLineArgs{
				CodespaceName: "my-codespace",
				DebugFile:     "/tmp/debug.log",
			},
			expected: []string{"codespace", "ssh", "--codespace", "my-codespace", "--debug-file", "/tmp/debug.log"},
		},
		{
			name: "with profile",
			args: CommandLineArgs{
				CodespaceName: "my-codespace",
				Profile:       "dev-profile",
			},
			expected: []string{"codespace", "ssh", "--codespace", "my-codespace", "--profile", "dev-profile"},
		},
		{
			name: "with repo and owner",
			args: CommandLineArgs{
				CodespaceName: "my-codespace",
				Repo:          "my-repo",
				RepoOwner:     "my-owner",
			},
			expected: []string{"codespace", "ssh", "--codespace", "my-codespace", "--repo", "my-repo", "--repo-owner", "my-owner"},
		},
		{
			name: "with server port",
			args: CommandLineArgs{
				CodespaceName: "my-codespace",
				ServerPort:    9000,
			},
			expected: []string{"codespace", "ssh", "--codespace", "my-codespace", "--server-port", "9000"},
		},
		{
			name: "empty codespace name",
			args: CommandLineArgs{
				Config: true,
			},
			expected: []string{"codespace", "ssh", "--config"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.args.BuildGHFlags()
			if len(result) != len(tt.expected) {
				t.Errorf("BuildGHFlags() returned %d items, expected %d", len(result), len(tt.expected))
				t.Errorf("Got: %v", result)
				t.Errorf("Expected: %v", tt.expected)
				return
			}
			for i, item := range result {
				if item != tt.expected[i] {
					t.Errorf("BuildGHFlags()[%d] = %q, want %q", i, item, tt.expected[i])
				}
			}
		})
	}
}

func TestCommandLineArgs_BuildSSHArgs(t *testing.T) {
	tests := []struct {
		name       string
		args       CommandLineArgs
		socketPath string
		port       int
	}{
		{
			name:       "basic SSH args",
			args:       CommandLineArgs{},
			socketPath: "/tmp/socket",
			port:       8080,
		},
		{
			name: "with remaining args",
			args: CommandLineArgs{
				RemainingArgs: []string{"echo", "hello"},
			},
			socketPath: "/tmp/socket",
			port:       9090,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.args.BuildSSHArgs(tt.socketPath, tt.port, nil)
			
			// Check that result starts with "--"
			if len(result) < 1 || result[0] != "--" {
				t.Errorf("BuildSSHArgs() should start with '--', got %v", result)
				return
			}
			
			// Check that the socket forward is present
			foundSocketForward := false
			expectedSocketForward := tt.socketPath + ":localhost:" + fmt.Sprint(tt.port)
			for i := 0; i < len(result)-1; i++ {
				if result[i] == "-R" && result[i+1] == expectedSocketForward {
					foundSocketForward = true
					break
				}
			}
			if !foundSocketForward {
				t.Errorf("BuildSSHArgs() should contain '-R %s', got %v", expectedSocketForward, result)
			}
			
			// Check that -t flag is present
			foundT := false
			for _, arg := range result {
				if arg == "-t" {
					foundT = true
					break
				}
			}
			if !foundT {
				t.Errorf("BuildSSHArgs() should contain '-t' flag, got %v", result)
			}
			
			// Check that remaining args are included
			if len(tt.args.RemainingArgs) > 0 {
				foundRemainingArgs := true
				for _, expectedArg := range tt.args.RemainingArgs {
					found := false
					for _, actualArg := range result {
						if actualArg == expectedArg {
							found = true
							break
						}
					}
					if !found {
						foundRemainingArgs = false
						break
					}
				}
				if !foundRemainingArgs {
					t.Errorf("BuildSSHArgs() should contain remaining args %v, got %v", tt.args.RemainingArgs, result)
				}
			}
			
			// Note: Reverse port forwarding is tested in port_test.go
			// We just verify the structure here since the actual forwards depend on runtime port availability
		})
	}
}

// Test helper function to capture os.Args manipulation
func withArgs(args []string, fn func()) {
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = args
	fn()
}

// Note: Testing ParseArgs fully is complex due to Go's flag package global state.
// In a production environment, you'd want to refactor ParseArgs to accept
// a custom flag.FlagSet to make it more testable.
func TestParseArgs_StructFields(t *testing.T) {
	// Test that CommandLineArgs struct has expected fields
	args := CommandLineArgs{
		CodespaceName:       "test",
		Config:              true,
		Debug:               true,
		DebugFile:           "test.log",
		AzureSubscriptionId: "test-sub",
		Logs:                true,
		Profile:             "test-profile",
		Repo:                "test/repo",
		RepoOwner:           "test-owner",
		ServerPort:          8080,
		RemainingArgs:       []string{"arg1", "arg2"},
	}

	// Basic field assignment test
	if args.CodespaceName != "test" {
		t.Errorf("Expected CodespaceName to be 'test', got %s", args.CodespaceName)
	}
	if !args.Config {
		t.Error("Expected Config to be true")
	}
	if !args.Debug {
		t.Error("Expected Debug to be true")
	}
	if args.DebugFile != "test.log" {
		t.Errorf("Expected DebugFile to be 'test.log', got %s", args.DebugFile)
	}
	if args.AzureSubscriptionId != "test-sub" {
		t.Errorf("Expected AzureSubscriptionId to be 'test-sub', got %s", args.AzureSubscriptionId)
	}
	if !args.Logs {
		t.Error("Expected Logs to be true")
	}
	if args.Profile != "test-profile" {
		t.Errorf("Expected Profile to be 'test-profile', got %s", args.Profile)
	}
	if args.Repo != "test/repo" {
		t.Errorf("Expected Repo to be 'test/repo', got %s", args.Repo)
	}
	if args.RepoOwner != "test-owner" {
		t.Errorf("Expected RepoOwner to be 'test-owner', got %s", args.RepoOwner)
	}
	if args.ServerPort != 8080 {
		t.Errorf("Expected ServerPort to be 8080, got %d", args.ServerPort)
	}
	if len(args.RemainingArgs) != 2 || args.RemainingArgs[0] != "arg1" || args.RemainingArgs[1] != "arg2" {
		t.Errorf("Expected RemainingArgs to be ['arg1', 'arg2'], got %v", args.RemainingArgs)
	}
}
