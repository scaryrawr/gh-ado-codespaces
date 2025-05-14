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
  --profile string           Name of the SSH profile to use
  --repo, -R string          Filter codespace selection by repository name (user/repo)
  --repo-owner string        Filter codespace selection by repository owner (username or org)
  --server-port int          SSH server port number (0 => pick unused)
```

You can also pass additional SSH flags after `--`, for example:

```fish
gh ado-codespaces -- -L 3000:localhost:3000
```

## How It Works

### Authentication Flow

The extension leverages Azure CLI credentials on your local machine to authenticate with Azure DevOps:

1. A Node.js service using the `@azure/identity` package connects to your Azure CLI credentials
2. An SSH connection forwards this service to a Unix socket in the codespace
3. Development tools inside the codespace request tokens through the ADO Auth Helper

### Port Forwarding

The automatic port forwarding system detects when applications in your codespace start listening on network ports:

1. A port monitor runs in your codespace and detects new listening ports
2. Port events are sent to your local machine through the SSH connection
3. New SSH tunnels are created automatically for each detected port
4. Applications running in your codespace become accessible via `localhost:<port>` locally

## Limitations

- Authentication is tied to your local Azure CLI session
- Initial setup with Artifacts Helper is required in the codespace

## Acknowledgments

This project builds upon the work done in [ADO SSH Auth for GitHub Codespaces](https://github.com/scaryrawr/ado-ssh-auth), adapting it as a GitHub CLI extension with additional functionality.
