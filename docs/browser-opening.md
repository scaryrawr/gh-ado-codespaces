# Browser Opening

[← Back to README](../README.md)

The extension provides browser opening from your codespace to your local machine:

1. When you connect, a browser opener script is uploaded to your codespace at `~/browser-opener.sh`
2. The script is automatically configured with the browser service port (no manual configuration needed)
3. A local HTTP service is started that listens for browser open requests
4. The service port is forwarded to the codespace via SSH reverse port forwarding
5. Users can configure their shell to use the browser opener by adding to their shell config (e.g., `~/.bashrc` or `~/.zshrc`):
   ```bash
   export BROWSER="$HOME/browser-opener.sh"
   ```
6. When any tool in the codespace tries to open a URL (e.g., via `xdg-open`, `python -m webbrowser`, etc.), it uses the `BROWSER` environment variable
7. The script sends an HTTP request to the local machine with the URL
8. Your local browser opens automatically with the requested URL

This is particularly useful for:
- Opening documentation links from CLI tools
- Viewing web-based development servers running in your codespace
- Accessing OAuth flows and authentication pages
- Opening links from terminal-based applications

The browser opener works cross-platform and will use your default browser on Windows, macOS, and Linux.

**Note:** You only need to set the `BROWSER` environment variable once in your shell configuration. The port is automatically configured in the script when it's uploaded.
