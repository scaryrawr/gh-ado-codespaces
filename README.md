# gh-ado-codespaces

A GitHub CLI extension that enables Azure DevOps (ADO) authentication with GitHub Codespaces via SSH connections, without requiring VS Code to be running. It also provides automatic port forwarding capabilities.

## Overview

When working with GitHub Codespaces and Azure DevOps services, authentication can be challenging, especially when connecting via SSH rather than through VS Code. This extension bridges that gap by:

1. Securely forwarding your local Azure CLI credentials to your codespace
2. Automatically detecting and forwarding application ports from your codespace to your local machine
3. Enabling browser opening from your codespace to your local browser
4. Providing command completion notifications to your local desktop
5. Providing a seamless development experience with GitHub Codespaces and Azure DevOps

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

### X11 Tunneling

When the host has a non-empty `DISPLAY` environment variable, interactive sessions automatically add trusted X11 forwarding with `-Y`. Trusted forwarding lets codespace applications access the local X server, so use it only with codespaces you trust. Install and start an X11 server on the host first (for example, XQuartz on macOS).

To disable X11 forwarding for a session, pass `-x` after the separator:

```fish
gh ado-codespaces -- -x
```

### Configuration

The extension can read optional configuration values that are scoped per GitHub login. By default it looks for a JSON file at:

```text
$OS_CONFIG_DIR/gh-ado-codespaces/config.json
```

Set the `GH_ADO_CODESPACES_CONFIG` environment variable to point at a different file if you prefer a custom location.

The configuration file supports global settings plus per-account overrides keyed by your authenticated GitHub username (the `.login` value returned by `gh api user --jq .login`):

```json
{
  "reversePortForward": [
    { "port": 8081, "description": "Custom service", "enabled": true }
  ],
  "accounts": {
    "login-id-1": {},
    "login-id-2": {
      "azure": {
        "subscription": "00000000-0000-0000-0000-000000000000"
      },
      "reversePortForward": [
        { "port": 9090, "description": "Account-only service", "enabled": true }
      ]
    }
  }
}
```

If a subscription is set, the extension requests tokens from the Azure CLI using that subscription. When no override is present, the Azure CLI's default subscription continues to be used.

`reversePortForward` entries are merged in this order: built-in defaults, top-level config, then per-account config. Matching ports are overridden by later entries, so you can disable or update defaults per account.

You can create or update this setting directly from the command line by supplying the `--azure-subscription-id` flag once. The value will be persisted for the active GitHub login so future invocations do not need the flag unless you want to change or clear it. To clear the stored value, edit the config file and remove (or empty) the `subscription` field for your login.

## How It Works

| Feature | Description |
|---|---|
| [Authentication](docs/authentication.md) | Forwards local Azure CLI credentials to your codespace for ADO access |
| [Browser Opening](docs/browser-opening.md) | Opens URLs from your codespace in your local browser |
| [Notifications](docs/notifications.md) | Desktop notifications when long-running commands finish |
| [Port Forwarding](docs/port-forwarding.md) | Automatic bi-directional port forwarding (including local AI services) |
| [xdg-open shim](#xdg-open-shim) | Intelligent file and URL opener for SSH / tmux environments |

### xdg-open shim

`gh ado-codespaces` automatically installs an `xdg-open` shim into the codespace at `/usr/local/bin/xdg-open`. No configuration is needed.

The shim replaces the standard `xdg-open` and routes each request based on what is being opened and how you are connected.

**URLs** (`http://`, `https://`, `mailto:`, `ftp://`) are forwarded through the following chain, stopping at the first success:

1. The gh-ado browser socket (the same service that powers [browser opening](docs/browser-opening.md))
2. `$BROWSER`, if set
3. `code --open-url` (VS Code remote)
4. The real `/usr/bin/xdg-open`

**Files** are opened with a viewer chosen by file type:

| Type | Viewer |
|---|---|
| Images (jpg, png, gif, …) | `chafa` |
| PDFs | `pdftotext` + `less`, or `pdfinfo` |
| Markdown | `glow`, then `bat`, then `$EDITOR` |
| Everything else | `$EDITOR`, then `vi` |

The viewer is launched in whichever environment is active:

- **Inside tmux** — opens in a new vertical split pane. Editors (vim, nvim, …) open directly; non-interactive viewers (chafa, bat, …) pause with a "press enter" prompt so you can read the output.
- **SSH without tmux** — runs the viewer inline in the current terminal (blocking).
- **Not in SSH** — delegates to the real `/usr/bin/xdg-open` or VS Code, falling back to the inline viewer.

## Testing

Run all tests:
```bash
go test -v ./...
```

See [docs/testing.md](docs/testing.md) for the full test suite overview and additional commands.

## Limitations

- Authentication is tied to your local Azure CLI session
- Initial setup with Artifacts Helper is required in the codespace

## Acknowledgments

This project builds upon the work done in [ADO SSH Auth for GitHub Codespaces](https://github.com/scaryrawr/ado-ssh-auth), adapting it as a GitHub CLI extension with additional functionality.
