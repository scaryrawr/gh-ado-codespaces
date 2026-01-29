---
name: gh-ado-codespaces-developer
description: Expert Go developer for the gh-ado-codespaces GitHub CLI extension, specializing in Azure DevOps authentication, SSH integration, and GitHub Codespaces.
tools:
  - bash
  - edit
  - view
  - grep
  - glob
infer: true
metadata:
  language: go
  framework: github-cli-extension
---

# Agent Development Guide

This document provides coding guidelines and conventions for AI agents working on the gh-ado-codespaces project.

## Project Overview

This is a GitHub CLI extension written in Go that enables Azure DevOps authentication with GitHub Codespaces via SSH, without requiring VS Code. It provides automatic port forwarding and browser opening capabilities.

**Key Technologies:**
- Go 1.21+
- GitHub CLI (`gh`) extension framework
- Azure SDK for Go (`github.com/Azure/azure-sdk-for-go/sdk`)
- SSH protocol and port forwarding
- Node.js authentication helper scripts

## Repository Structure

```
gh-ado-codespaces/
├── main.go                 # Entry point and main execution flow
├── args.go                 # Command-line argument parsing
├── auth-helper.go          # Azure authentication helper service
├── azure-auth.go           # Azure SDK integration and token management
├── browser.go              # Browser opening service and HTTP handler
├── codespace.go            # GitHub Codespaces API integration
├── config.go               # Configuration file management
├── github_login.go         # GitHub authentication integration
├── port.go                 # Port forwarding logic and detection
├── port-monitor.go         # Port monitoring service
├── ui.go                   # Terminal UI and formatting
├── *_test.go              # Test files (parallel to source files)
├── ado-auth-helper.py     # Python script for Azure auth in codespace
├── browser-opener.sh       # Bash script for browser opening in codespace
├── port-monitor.sh         # Bash script for port monitoring in codespace
├── go.mod, go.sum          # Go module dependencies
├── AGENTS.md               # This file - agent instructions
├── README.md               # User-facing documentation
└── .github/
    └── workflows/          # CI/CD pipeline definitions
```

**Key files you'll work with:**
- `main.go`: Orchestrates the entire flow, sets up services, manages SSH connection
- `args.go`: Parses CLI flags, builds SSH command arguments
- `auth-helper.go`: Starts Node.js server for Azure authentication
- `browser.go`: HTTP service for opening URLs from codespace
- `port.go`: SSH port forwarding setup and management
- `config.go`: Reads/writes user configuration (Azure subscription overrides)

## Boundaries and Restrictions

**What you MUST NOT do:**
- Never commit secrets, tokens, or credentials to the repository
- Do not modify or remove existing tests without explicit justification
- Do not change the core authentication flow without thorough testing
- Do not log sensitive information (tokens, credentials, user data)
- Do not use `panic` for error handling; always return errors
- Do not modify `.github/workflows` files without understanding CI implications
- Do not introduce breaking changes to the command-line interface
- Do not remove or change existing log file structures (session-based logging)

**What you should be cautious about:**
- Changes to SSH connection logic require careful testing
- Port forwarding logic is complex; understand the full flow before modifying
- Azure authentication flow must maintain backward compatibility
- Browser opening functionality works cross-platform; test on all platforms
- Configuration file format changes require migration path for existing users

## Build, Test, and Lint Commands

**IMPORTANT:** Always run these commands from the repository root directory.

### Build
```bash
# Build the extension (REQUIRED before manual testing)
go build -v .

# Build with specific output name
go build -v -o gh-ado-codespaces .
```

**When to build:**
- After making any code changes to Go files
- Before manually testing the extension
- To verify compilation succeeds

### Test
```bash
# Run all tests (ALWAYS run before committing)
go test -v ./...

# Run tests with race detection (matches CI, use before pushing)
go test -v -race .

# Run only fast unit tests (when iterating on changes)
go test -short -v ./...

# Run a specific test by name
go test -v -run TestFunctionName

# Run tests for a specific file
go test -v -run "TestArgs.*"

# Run tests with coverage report
go test -v -cover ./...
```

**Testing workflow:**
1. Run targeted tests during development: `go test -v -run TestSpecific`
2. Run all tests before committing: `go test -v ./...`
3. Run with race detector before pushing: `go test -v -race .`

### Format and Lint
```bash
# Format code (ALWAYS run before committing)
go fmt ./...

# Run static analysis (checks for common mistakes)
go vet ./...

# Run golangci-lint if available (recommended)
golangci-lint run
```

**Code quality workflow:**
1. Format all code: `go fmt ./...`
2. Check for issues: `go vet ./...`
3. Run linter if available: `golangci-lint run`
4. Fix any reported issues before committing

## Code Style Guidelines

### General Principles
- Follow standard Go conventions and idioms
- Prefer clarity over cleverness
- Write self-documenting code with meaningful names
- Keep functions focused and single-purpose

### Imports
- Use standard library imports first, then third-party, then local packages
- Group imports with blank lines between categories
- Do not use named imports unless absolutely necessary to avoid conflicts

Example:
```go
import (
    "context"
    "fmt"
    "os"
    
    "github.com/cli/go-gh/v2"
    "github.com/google/uuid"
)
```

### Formatting
- Use `gofmt` for all code formatting (tabs for indentation, not spaces)
- Line length: aim for 100-120 characters, but readability takes precedence
- Use blank lines to separate logical blocks within functions
- Always include a blank line before `return` statements when preceded by other code

### Types and Structs
- Use PascalCase for exported types and functions
- Use camelCase for unexported types and functions
- Add struct field tags for JSON marshaling: `` `json:"field_name"` ``
- Document exported types with comments starting with the type name

Example:
```go
// CommandLineArgs holds all the command line arguments
type CommandLineArgs struct {
    CodespaceName       string
    Debug               bool
    AzureSubscriptionId string
}
```

### Variable Naming
- Use descriptive names; avoid single-letter variables except in very short scopes (e.g., `i` in loops)
- Use camelCase for variables and function parameters
- For acronyms in names, use consistent casing: `HTTP`, `ID`, `URL` when starting a name; `http`, `id`, `url` mid-name
- Examples: `ServerConfig`, `codespaceName`, `azureSubscriptionId`, `HTTPClient`

### Error Handling
- Always check errors; never ignore them with `_`
- Wrap errors with context using `fmt.Errorf("context: %w", err)`
- Log errors appropriately using the logging infrastructure (authLogger, debugLogger)
- Return errors up the stack rather than handling them deep in the call chain
- Use descriptive error messages that help with debugging

Example:
```go
listener, err := net.Listen("tcp", "localhost:0")
if err != nil {
    return nil, fmt.Errorf("failed to start local server: %w", err)
}
```

### Context Usage
- Always pass `context.Context` as the first parameter to functions that may block
- Use `ctx` as the variable name for contexts
- Respect context cancellation in loops and long-running operations
- Use `context.WithCancel` for cleanup and graceful shutdown

Example:
```go
func startServer(ctx context.Context, cred azcore.TokenCredential) (net.Listener, int, error) {
    // Implementation that respects ctx
}
```

### Concurrency
- Use goroutines for non-blocking operations (server accept loops, port monitoring)
- Always use `defer` to ensure cleanup (e.g., closing connections, files)
- Use channels for goroutine communication
- Use `sync.WaitGroup` to wait for goroutine completion
- Protect shared state with mutexes when necessary

Example:
```go
go func() {
    defer wg.Done()
    // goroutine work
}()
```

### Logging
- Use the established logging infrastructure:
  - `logAuthMessage()` for authentication operations (writes to azure-auth.log)
  - `logDebug()` for port monitoring operations (writes to port-monitor.log)
- Log important state changes and errors
- Include relevant context in log messages (ports, addresses, scopes)
- Use `fmt.Fprintf(os.Stderr, ...)` for user-facing messages
- Do NOT log sensitive information like tokens (only log that they were obtained)

### Comments
- Write doc comments for all exported functions, types, and constants
- Doc comments should be complete sentences starting with the name of the thing being documented
- Use inline comments to explain "why" not "what"
- Comment non-obvious behavior or workarounds

Example:
```go
// SetupServer initializes the local server and returns its configuration.
// It now takes a context for cancellation.
func SetupServer(ctx context.Context) (*ServerConfig, error) {
    // Implementation
}
```

### Function Design
- Keep functions short and focused (generally under 50 lines)
- Use early returns to reduce nesting
- Return errors as the last return value
- Use named return values sparingly, only when they improve clarity

### Testing
- Write table-driven tests for functions with multiple cases
- Use meaningful test names: `TestFunctionName_Scenario`
- Test both success and error cases
- Use `t.Run()` for subtests to improve readability and enable running specific scenarios
- Mock external dependencies (GitHub CLI, SSH commands) in tests
- Keep tests deterministic and isolated

Example:
```go
func TestSanitizeForFilename(t *testing.T) {
    tests := []struct {
        name     string
        input    string
        expected string
    }{
        {"empty string", "", ""},
        {"simple name", "hello", "hello"},
        {"with spaces", "hello world", "hello-world"},
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            result := sanitizeForFilename(tt.input)
            if result != tt.expected {
                t.Errorf("got %q, want %q", result, tt.expected)
            }
        })
    }
}
```

## Project-Specific Patterns

### Session Management
- All log files use session-based directory structure
- Session IDs include codespace name, timestamp, and PID
- Use `getSessionLogPath()` to get log file paths
- Use `ensureSessionLogDirectory()` before creating log files

### GitHub CLI Integration
- Use `github.com/cli/go-gh/v2` package for GitHub operations
- Use `gh.Exec()` for non-interactive commands
- Use `gh.ExecInteractive()` for interactive SSH sessions
- Pass context to interactive commands for cancellation support

### Azure Authentication
- Use Azure SDK packages from `github.com/Azure/azure-sdk-for-go/sdk`
- Support per-account subscription overrides via config
- Log authentication operations for debugging

## Git Workflow and Commit Conventions

### Branch Strategy
- Work on feature branches, not `main`
- Branch names should be descriptive: `feature/add-xyz`, `fix/issue-123`, `docs/update-readme`

### Commit Messages
- Use clear, descriptive commit messages
- Start with a verb in present tense: "Add", "Fix", "Update", "Remove"
- Keep the first line under 72 characters
- Add details in the body if needed

**Good examples:**
```
Add browser port forwarding feature
Fix race condition in port monitor
Update AGENTS.md with best practices
```

**Bad examples:**
```
changes
fixed stuff
WIP
```

### Before Committing
Always complete this checklist:
1. ✓ Run `go fmt ./...` to format code
2. ✓ Run `go vet ./...` to check for issues
3. ✓ Run `go test -v ./...` to ensure tests pass
4. ✓ Review your changes: `git diff`
5. ✓ Stage only relevant files (no build artifacts, temp files, or node_modules)

### Pull Request Guidelines
- Ensure all CI checks pass
- Write descriptive PR titles and descriptions
- Reference any related issues
- Keep PRs focused on a single change or feature
- Respond to review feedback promptly

## Common Pitfalls to Avoid

- Do not use `cd` in command execution; use proper working directory parameters
- Do not ignore context cancellation in loops
- Do not forget to close resources (files, connections, listeners)
- Do not log sensitive information (tokens, credentials)
- Do not use panic for error handling; return errors instead
- Do not modify global state without synchronization

## Development Tips and Troubleshooting

### Testing the Extension Locally
1. Build: `go build -v .`
2. Test the built binary: `./gh-ado-codespaces --help`
3. For GitHub CLI integration testing, you may need: `gh extension remove ado-codespaces && gh extension install .`

### Debugging
- Use `--debug` flag to enable debug logging
- Check log files in `~/.config/gh-ado-codespaces/sessions/`
- Use `fmt.Fprintf(os.Stderr, "...")` for development debugging (remove before commit)
- Run with verbose test output: `go test -v -run TestName`

### Common Issues
- **Build failures**: Check `go.mod` dependencies are up to date
- **Test failures**: Ensure you're running from repo root
- **SSH issues**: Verify `gh` CLI is installed and authenticated
- **Port forwarding**: Check firewall/network settings allow localhost connections

### Understanding the Flow
1. User runs `gh ado-codespaces`
2. Parse arguments (`args.go`)
3. Load config (`config.go`)
4. Start auth helper service (`auth-helper.go`)
5. Start browser service (`browser.go`)
6. Upload helper scripts to codespace (`main.go`)
7. Establish SSH connection with port forwarding (`gh codespace ssh`)
8. Monitor ports in codespace (`port-monitor.sh` → `port.go`)
9. Forward detected ports automatically
10. Open URLs from codespace to local browser

### Making Changes
When modifying code:
1. **Understand the full impact**: Trace the flow through related files
2. **Write tests first**: Add test cases before implementation
3. **Run targeted tests**: `go test -v -run TestYourFunction`
4. **Check all tests**: `go test -v ./...`
5. **Format**: `go fmt ./...`
6. **Static analysis**: `go vet ./...`
7. **Manual test**: Build and run the extension with a real codespace
8. **Review logs**: Check session logs for any unexpected behavior
9. **Commit**: Follow git workflow guidelines above

### Resources
- [GitHub CLI Extension docs](https://docs.github.com/en/github-cli/github-cli/creating-github-cli-extensions)
- [Azure SDK for Go docs](https://pkg.go.dev/github.com/Azure/azure-sdk-for-go)
- [Go testing best practices](https://go.dev/doc/tutorial/add-a-test)
- Project README: `/README.md`
