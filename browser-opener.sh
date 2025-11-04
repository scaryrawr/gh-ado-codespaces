#!/usr/bin/env bash
# Browser opener script for codespace
# This script can be set as the BROWSER environment variable in the codespace
# It forwards browser open requests to the local machine via HTTP through a Unix socket
#
# To use this script, add to your shell configuration (e.g., ~/.bashrc or ~/.zshrc):
#   export BROWSER="$HOME/browser-opener.sh"
#
# The script automatically finds the browser service socket (no manual configuration needed)

# Get the URL from arguments
URL="$1"

if [ -z "$URL" ]; then
    echo "Usage: $0 <url>" >&2
    exit 1
fi

# Find all browser sockets in /tmp (pattern: gh-ado-browser-*.sock)
# Try each socket until one succeeds (handles multiple instances and stale sockets)
BROWSER_SOCKETS=$(find /tmp -maxdepth 1 -name "gh-ado-browser-*.sock" -type s 2>/dev/null)

if [ -z "$BROWSER_SOCKETS" ]; then
    # No socket found - browser forwarding not available
    exit 0
fi

# Try each socket until one succeeds
for BROWSER_SOCKET in $BROWSER_SOCKETS; do
    # Send URL to the socket via HTTP POST using curl with --unix-socket
    # Using curl to send the URL as a query parameter
    if curl -s --unix-socket "$BROWSER_SOCKET" -X POST "http://localhost/open?url=$(printf %s "$URL" | jq -sRr @uri)" >/dev/null 2>&1; then
        # Success - exit immediately
        exit 0
    fi
done

# All sockets failed - browser forwarding not available
exit 0
