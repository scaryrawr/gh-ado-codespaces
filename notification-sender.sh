#!/usr/bin/env bash
# Notification sender script for codespace
# This script sends command completion notifications to the local machine
# It can be integrated with shell hooks to notify when commands finish
#
# To use this script with bash, add to your ~/.bashrc:
#   # Load notification support
#   if [ -f "$HOME/notification-sender.sh" ]; then
#       source "$HOME/notification-sender.sh"
#   fi
#
# To use this script with zsh, add to your ~/.zshrc:
#   # Load notification support
#   if [ -f "$HOME/notification-sender.sh" ]; then
#       source "$HOME/notification-sender.sh"
#   fi

# Handle "send" subcommand for direct invocation (e.g., fish done plugin)
# Usage: ~/notification-sender.sh send "title" "message"
if [ "${1:-}" = "send" ]; then
    __ns_title="$2"
    __ns_message="$3"

    if [ -z "$__ns_title" ] || [ -z "$__ns_message" ]; then
        exit 1
    fi

    # Require jq and curl
    if ! command -v jq >/dev/null 2>&1 || ! command -v curl >/dev/null 2>&1; then
        exit 0
    fi

    # Find the newest notification socket
    __ns_socket=$(find /tmp -maxdepth 1 -name "gh-ado-notification-*.sock" -type s -exec ls -t {} + 2>/dev/null | head -1)
    if [ -n "$__ns_socket" ]; then
        curl -s --max-time 2 --unix-socket "$__ns_socket" -X POST \
            -H "Content-Type: application/json" \
            -d "{\"title\":$(printf %s "$__ns_title" | jq -Rs .), \"message\":$(printf %s "$__ns_message" | jq -Rs .)}" \
            "http://localhost/notify" >/dev/null 2>&1
    fi
    exit 0
fi

# Configuration
NOTIFICATION_MIN_DURATION="${NOTIFICATION_MIN_DURATION:-5}"  # Minimum command duration in seconds to trigger notification

# Store command start time
__notification_cmd_start_time=0

# Cache for socket path (reset every 60 seconds)
__notification_socket_cache=""
__notification_socket_cache_time=0

# Function to find and cache notification socket
__find_notification_socket() {
    local current_time=$(date +%s)
    local cache_lifetime=60
    
    # Return cached socket if still valid
    if [ -n "$__notification_socket_cache" ] && [ $((current_time - __notification_socket_cache_time)) -lt $cache_lifetime ]; then
        echo "$__notification_socket_cache"
        return 0
    fi
    
    # Find all notification sockets in /tmp (pattern: gh-ado-notification-*.sock)
    # Sort by modification time (newest first) to prefer active sockets
    # Use find -exec for better portability across different xargs implementations
    local NOTIFICATION_SOCKETS=$(find /tmp -maxdepth 1 -name "gh-ado-notification-*.sock" -type s -exec ls -t {} + 2>/dev/null | head -1)
    
    if [ -n "$NOTIFICATION_SOCKETS" ]; then
        __notification_socket_cache="$NOTIFICATION_SOCKETS"
        __notification_socket_cache_time=$current_time
        echo "$__notification_socket_cache"
        return 0
    fi
    
    return 1
}

# Function to send notification
__send_notification() {
    local title="$1"
    local message="$2"
    
    # Check if jq is available
    if ! command -v jq >/dev/null 2>&1; then
        # jq not available, skip notification
        return 0
    fi
    
    # Check if curl is available
    if ! command -v curl >/dev/null 2>&1; then
        # curl not available, skip notification
        return 0
    fi

    # Try cached socket first, then all discovered sockets (newest first).
    # This avoids getting stuck on stale socket files from old sessions.
    local socket_candidates=""
    if [ -n "$__notification_socket_cache" ]; then
        socket_candidates="$__notification_socket_cache"$'\n'
    fi

    local discovered_sockets=$(find /tmp -maxdepth 1 -name "gh-ado-notification-*.sock" -type s -exec ls -t {} + 2>/dev/null)
    if [ -n "$discovered_sockets" ]; then
        socket_candidates="${socket_candidates}${discovered_sockets}"
    fi

    if [ -z "$socket_candidates" ]; then
        # No socket found - notification service not available
        return 0
    fi

    local NOTIFICATION_SOCKET
    while IFS= read -r NOTIFICATION_SOCKET; do
        [ -z "$NOTIFICATION_SOCKET" ] && continue

        # Send notification to the socket via HTTP POST using curl with --unix-socket
        # --max-time 2 ensures we fail fast on dead sockets
        if curl -s --max-time 2 --unix-socket "$NOTIFICATION_SOCKET" -X POST \
            -H "Content-Type: application/json" \
            -d "{\"title\":$(printf %s "$title" | jq -Rs .), \"message\":$(printf %s "$message" | jq -Rs .)}" \
            "http://localhost/notify" >/dev/null 2>&1; then
            __notification_socket_cache="$NOTIFICATION_SOCKET"
            __notification_socket_cache_time=$(date +%s)
            return 0
        fi
    done <<< "$socket_candidates"

    # Failed on all candidates - invalidate cache to force re-discovery next time.
    __notification_socket_cache=""
    return 1
}

# Function to send a command completion notification when duration threshold is met
__notification_send_command_completion() {
    local exit_code="$1"
    local end_time=$(date +%s)
    local duration=$((end_time - __notification_cmd_start_time))

    # Only notify if command took longer than minimum duration
    if [ $__notification_cmd_start_time -ne 0 ] && [ $duration -ge $NOTIFICATION_MIN_DURATION ]; then
        local cmd_status="completed"
        if [ $exit_code -ne 0 ]; then
            cmd_status="failed"
        fi

        local last_cmd=$(__notification_last_command)
        __send_notification "Command $cmd_status" "$last_cmd (${duration}s, exit: $exit_code)"
    fi

    __notification_cmd_start_time=0
}

# Bash-specific hooks
if [ -n "$BASH_VERSION" ]; then
    # Track if we're in a command execution to avoid nested timing
    __notification_in_command=0
    
    # Function called before each command
    __notification_preexec() {
        # Only set start time if we're not already in a command
        # This prevents nested commands from overwriting the start time
        if [ $__notification_in_command -eq 0 ]; then
            __notification_cmd_start_time=$(date +%s)
            __notification_in_command=1
        fi
    }
    
    # Function called after each command
    __notification_precmd() {
        local exit_code=$?

        # Only process if we were actually timing a command
        if [ $__notification_in_command -eq 0 ]; then
            return
        fi

        __notification_in_command=0
        __notification_send_command_completion "$exit_code"
    }

    __notification_last_command() {
        HISTTIMEFORMAT= history 1 | sed 's/^[ ]*[0-9]*[ ]*//'
    }
    
    # Set up bash hooks using DEBUG trap and PROMPT_COMMAND
    trap '__notification_preexec' DEBUG
    
    # Add to PROMPT_COMMAND (preserve existing commands)
    if [[ -z "$PROMPT_COMMAND" ]]; then
        PROMPT_COMMAND='__notification_precmd'
    elif [[ "$PROMPT_COMMAND" != *'__notification_precmd'* ]]; then
        PROMPT_COMMAND="__notification_precmd;$PROMPT_COMMAND"
    fi
fi

# Zsh-specific hooks
if [ -n "$ZSH_VERSION" ]; then
    # Function called before each command
    __notification_preexec() {
        __notification_cmd_start_time=$(date +%s)
    }
    
    # Function called after each command
    __notification_precmd() {
        local exit_code=$?

        __notification_send_command_completion "$exit_code"
    }

    __notification_last_command() {
        # In zsh, the last command is available in $history[1] or we can use fc
        fc -ln -1 2>/dev/null || echo "unknown command"
    }
    
    # Set up zsh hooks
    autoload -Uz add-zsh-hook 2>/dev/null || return
    add-zsh-hook preexec __notification_preexec
    add-zsh-hook precmd __notification_precmd
fi
