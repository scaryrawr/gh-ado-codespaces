#!/usr/bin/env bash
# Browser opener script for codespace
# This script is set as the BROWSER environment variable in the codespace
# It forwards browser open requests to the local machine via TCP

BROWSER_PORT="${GH_ADO_CODESPACES_BROWSER_PORT:-19876}"

# Get the URL from arguments
URL="$1"

if [ -z "$URL" ]; then
    echo "Usage: $0 <url>" >&2
    exit 1
fi

# Send URL to localhost:BROWSER_PORT as JSON
# Using jq for JSON formatting and nc for sending
echo "$URL" | jq -R -c '{type: "browser", action: "open", url: .}' | nc -q 1 localhost "$BROWSER_PORT" 2>/dev/null

# If nc fails, silently ignore (browser forwarding not available)
exit 0
