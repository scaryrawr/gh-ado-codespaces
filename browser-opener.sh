#!/usr/bin/env bash
# Browser opener script for codespace
# This script can be set as the BROWSER environment variable in the codespace
# It forwards browser open requests to the local machine via HTTP through a Unix socket
#
# To use this script, add to your shell configuration (e.g., ~/.bashrc or ~/.zshrc):
#   export BROWSER="$HOME/browser-opener.sh"
#
# The script automatically finds the browser service socket (no manual configuration needed)

# Find the browser socket in /tmp (pattern: gh-ado-browser-*.sock)
BROWSER_SOCKET=$(find /tmp -maxdepth 1 -name "gh-ado-browser-*.sock" -type s 2>/dev/null | head -n 1)

if [ -z "$BROWSER_SOCKET" ]; then
    # No socket found - browser forwarding not available
    exit 0
fi

# Get the URL from arguments
URL="$1"

if [ -z "$URL" ]; then
    echo "Usage: $0 <url>" >&2
    exit 1
fi

# Send URL to the socket via HTTP POST using curl with --unix-socket
# Using curl to send the URL as a query parameter
curl -s --unix-socket "$BROWSER_SOCKET" -X POST "http://localhost/open?url=$(printf %s "$URL" | jq -sRr @uri)" >/dev/null 2>&1

# If curl fails, silently ignore (browser forwarding not available)
exit 0
