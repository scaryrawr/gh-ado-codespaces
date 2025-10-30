#!/usr/bin/env bash
# Browser opener script for codespace
# This script can be set as the BROWSER environment variable in the codespace
# It forwards browser open requests to the local machine via HTTP
#
# To use this script, add to your shell configuration (e.g., ~/.bashrc or ~/.zshrc):
#   export BROWSER="$HOME/browser-opener.sh"
#   export GH_ADO_CODESPACES_BROWSER_PORT=<port>  # Set by gh-ado-codespaces

BROWSER_PORT="${GH_ADO_CODESPACES_BROWSER_PORT:-19876}"

# Get the URL from arguments
URL="$1"

if [ -z "$URL" ]; then
    echo "Usage: $0 <url>" >&2
    exit 1
fi

# Send URL to localhost:BROWSER_PORT via HTTP POST
# Using curl to send the URL as a query parameter
curl -s -X POST "http://localhost:${BROWSER_PORT}/open?url=$(printf %s "$URL" | jq -sRr @uri)" >/dev/null 2>&1

# If curl fails, silently ignore (browser forwarding not available)
exit 0
