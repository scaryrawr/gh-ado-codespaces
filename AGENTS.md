# Agent Development Guide

This document provides coding guidelines and conventions for AI agents working on the gh-ado-codespaces project.

## Project Overview

This is a GitHub CLI extension written in Go that enables Azure DevOps authentication with GitHub Codespaces via SSH, without requiring VS Code. It provides automatic port forwarding and browser opening capabilities.

## Build, Test, and Lint Commands

### Build
```bash
# Build the extension
go build -v .

# Build with output name
go build -v -o gh-ado-codespaces .
```

### Test
```bash
# Run all tests
go test -v ./...

# Run tests with race detection (CI standard)
go test -v -race .

# Run only fast unit tests (skip integration tests)
go test -short -v ./...

# Run a specific test
go test -v -run TestFunctionName

# Run a specific test file's tests
go test -v -run "TestArgs.*"

# Run tests with coverage
go test -v -cover ./...
```

### Format and Lint
```bash
# Format code (always run before committing)
go fmt ./...

# Run static analysis
go vet ./...

# Install and run golangci-lint (recommended)
golangci-lint run
```

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

## Common Pitfalls to Avoid

- Do not use `cd` in command execution; use proper working directory parameters
- Do not ignore context cancellation in loops
- Do not forget to close resources (files, connections, listeners)
- Do not log sensitive information (tokens, credentials)
- Do not use panic for error handling; return errors instead
- Do not modify global state without synchronization
