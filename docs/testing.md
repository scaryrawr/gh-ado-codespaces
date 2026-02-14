# Testing

[← Back to README](../README.md)

This project includes a comprehensive unit test suite that covers:

- **Command line argument parsing and validation** (`args_test.go`)
  - Azure subscription ID format validation
  - Command line flag building for `gh codespace ssh`
  - SSH argument construction with browser and notification service integration

- **Configuration file handling** (`config_test.go`)
  - Azure subscription storage and retrieval per GitHub account
  - JSON configuration file loading and saving
  - Error handling for malformed configuration files

- **Browser opening functionality** (`browser_test.go`)
  - HTTP-based browser service creation and lifecycle management
  - Cross-platform URL opening support via HTTP endpoint
  - HTTP method and parameter validation
  - SSH argument integration with browser port forwarding

- **Notification functionality** (`notification_test.go`)
  - Notification service creation and lifecycle management
  - Cross-platform desktop notification support via HTTP endpoint
  - HTTP method and JSON payload validation
  - SSH argument integration with notification socket forwarding

- **Utility functions** (`main_test.go`)
  - Filename sanitization for session directories  
  - Session ID generation and formatting
  - File size formatting for log file listings

- **Codespace operations** (`codespace_test.go`)
  - Codespace list item formatting with colors and status indicators
  - Git status indicators (ahead commits, uncommitted/unpushed changes)
  - Codespace sorting by availability status

- **GitHub integration** (`github_login_test.go`)
  - GitHub CLI authentication integration tests
  - GitHub username validation

## Running Tests

Run all tests:
```bash
go test -v ./...
```

Run tests with race detection:
```bash
go test -v -race ./...
```

Run only fast unit tests (skip integration tests):
```bash
go test -short -v ./...
```

The test suite maintains compatibility with the existing CI/CD pipeline and ensures all functionality works correctly without external dependencies in most cases.
