# gh-ado-codespaces

A GitHub CLI extension that enables Azure DevOps (ADO) authentication with GitHub Codespaces via SSH connections, without requiring VS Code to be running. It also provides automatic port forwarding capabilities.

## Overview

When working with GitHub Codespaces and Azure DevOps services, authentication can be challenging, especially when connecting via SSH rather than through VS Code. This extension bridges that gap by:

1. Securely forwarding your local Azure CLI credentials to your codespace
2. Automatically detecting and forwarding application ports from your codespace to your local machine
3. Providing a seamless development experience with GitHub Codespaces and Azure DevOps

## Requirements

- [GitHub CLI](https://cli.github.com/) (`gh`) installed and authenticated
- [Azure CLI](https://docs.microsoft.com/en-us/cli/azure/install-azure-cli) (`az`) installed and logged in to the appropriate tenant
- A GitHub Codespace with [Artifacts Helper](https://github.com/microsoft/codespace-features/tree/main/src/artifacts-helper) configured

## Installation

```fish
gh extension install scaryrawr/gh-ado-codespaces
```

## Usage

Before using the extension, make sure you're logged into Azure CLI with the appropriate scope:

```fish
az login --scope 499b84ac-1321-427f-aa17-267ca6975798/.default
```

Then, start a new session with your codespace:

```fish
gh ado-codespaces
```

The extension will:

1. Start a local authentication service
2. Prompt you to select a GitHub Codespace (if not specified)
3. Establish a secure port forwarding channel for authentication
4. Start an interactive SSH session
5. Automatically forward detected application ports from your codespace

### Command Line Options

```
Usage:
  gh ado-codespaces [flags] [-- ssh-flags...]

Flags:
  --codespace, -c string     Name of the codespace
  --config                   Write OpenSSH configuration to stdout
  --debug, -d                Log debug data to a file
  --debug-file string        Path of the file to log to
  --azure-subscription-id string  Azure subscription ID to use for authentication (persisted per GitHub account)
  --profile string           Name of the SSH profile to use
  --repo, -R string          Filter codespace selection by repository name (user/repo)
  --repo-owner string        Filter codespace selection by repository owner (username or org)
  --server-port int          SSH server port number (0 => pick unused)
```

You can also pass additional SSH flags after `--`, for example:

```fish
gh ado-codespaces -- -L 3000:localhost:3000
```

### Configuration

The extension can read optional configuration values that are scoped per GitHub login. By default it looks for a JSON file at:

```text
$OS_CONFIG_DIR/gh-ado-codespaces/config.json
```

Set the `GH_ADO_CODESPACES_CONFIG` environment variable to point at a different file if you prefer a custom location.

The configuration file is a JSON object keyed by GitHub login IDs returned by `gh auth switch`. Each account can provide Azure-specific overrides, such as the subscription to use when acquiring tokens via the Azure CLI:

```json
{
  "login-id-1": {},
  "login-id-2": {
    "azure": {
      "subscription": "00000000-0000-0000-0000-000000000000"
    }
  }
}
```

If a subscription is set, the extension requests tokens from the Azure CLI using that subscription. When no override is present, the Azure CLI's default subscription continues to be used.

You can create or update this setting directly from the command line by supplying the `--azure-subscription-id` flag once. The value will be persisted for the active GitHub login so future invocations do not need the flag unless you want to change or clear it. To clear the stored value, edit the config file and remove (or empty) the `subscription` field for your login.

## How It Works

### Authentication Flow

The extension leverages Azure CLI credentials on your local machine to authenticate with Azure DevOps:

1. A Node.js service using the `@azure/identity` package connects to your Azure CLI credentials
2. An SSH connection forwards this service to a Unix socket in the codespace
3. Development tools inside the codespace request tokens through the ADO Auth Helper

### Browser Opening

The extension provides automatic browser opening from your codespace to your local machine:

1. When you connect, a browser opener script is uploaded to your codespace
2. The `BROWSER` environment variable is automatically set to point to this script
3. When any tool in the codespace tries to open a URL (e.g., via `xdg-open`, `python -m webbrowser`, etc.), the URL is captured
4. The URL is sent to your local machine through a reverse SSH tunnel
5. Your local browser opens automatically with the requested URL

This is particularly useful for:
- Opening documentation links from CLI tools
- Viewing web-based development servers running in your codespace
- Accessing OAuth flows and authentication pages
- Opening links from terminal-based applications

The browser opener works cross-platform and will use your default browser on Windows, macOS, and Linux.

### Port Forwarding

The automatic port forwarding system works in two directions:

#### Forward Port Forwarding (Codespace → Local Machine)

The extension detects when applications in your codespace start listening on network ports:

1. A port monitor runs in your codespace and detects new listening ports
2. Port events are sent to your local machine through the SSH connection
3. New SSH tunnels are created automatically for each detected port
4. Applications running in your codespace become accessible via `localhost:<port>` locally

#### Reverse Port Forwarding (Local Machine → Codespace)

The extension automatically shares local AI services to your codespace:

1. When connecting, the extension checks if Ollama (port 11434) or LM Studio (port 1234) are running locally
2. If either service is detected, a reverse SSH tunnel is automatically created
3. Your codespace can then access these services via `localhost:1234` or `localhost:11434`
4. This enables you to use your local AI models and services from within your codespace

This is particularly useful for:
- Running Ollama models on your local machine while coding in a codespace
- Using LM Studio's local inference server from your codespace
- Sharing any locally-running AI services with your remote development environment

## Testing

This project includes a comprehensive unit test suite that covers:

- **Command line argument parsing and validation** (`args_test.go`)
  - Azure subscription ID format validation
  - Command line flag building for `gh codespace ssh`
  - SSH argument construction with browser service integration
  
- **Configuration file handling** (`config_test.go`)
  - Azure subscription storage and retrieval per GitHub account
  - JSON configuration file loading and saving
  - Error handling for malformed configuration files

- **Browser opening functionality** (`browser_test.go`)
  - Browser service creation and lifecycle management
  - Cross-platform URL opening support
  - Browser message parsing and handling
  - SSH argument integration with browser port forwarding

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

### Running Tests

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

## Limitations

- Authentication is tied to your local Azure CLI session
- Initial setup with Artifacts Helper is required in the codespace

## Acknowledgments

This project builds upon the work done in [ADO SSH Auth for GitHub Codespaces](https://github.com/scaryrawr/ado-ssh-auth), adapting it as a GitHub CLI extension with additional functionality.
