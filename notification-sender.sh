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

# Configuration
NOTIFICATION_MIN_DURATION="${NOTIFICATION_MIN_DURATION:-5}"  # Minimum command duration in seconds to trigger notification

# Store command start time
__notification_cmd_start_time=0

# Function to send notification
__send_notification() {
    local title="$1"
    local message="$2"
    
    # Find all notification sockets in /tmp (pattern: gh-ado-notification-*.sock)
    # Sort by modification time (newest first) to prefer active sockets
    local NOTIFICATION_SOCKETS=$(find /tmp -maxdepth 1 -name "gh-ado-notification-*.sock" -type s -print0 2>/dev/null | xargs -0 -r ls -t 2>/dev/null)
    
    if [ -z "$NOTIFICATION_SOCKETS" ]; then
        # No socket found - notification service not available
        return 0
    fi
    
    # Try each socket until one succeeds (with timeout to quickly skip dead sockets)
    for NOTIFICATION_SOCKET in $NOTIFICATION_SOCKETS; do
        # Send notification to the socket via HTTP POST using curl with --unix-socket
        # --max-time 2 ensures we fail fast on dead sockets
        if curl -s --max-time 2 --unix-socket "$NOTIFICATION_SOCKET" -X POST \
            -H "Content-Type: application/json" \
            -d "{\"title\":$(printf %s "$title" | jq -Rs .), \"message\":$(printf %s "$message" | jq -Rs .)}" \
            "http://localhost/notify" >/dev/null 2>&1; then
            # Success - exit immediately
            return 0
        fi
    done
    
    # All sockets failed - notification service not available
    return 0
}

# Bash-specific hooks
if [ -n "$BASH_VERSION" ]; then
    # Function called before each command
    __notification_preexec() {
        __notification_cmd_start_time=$(date +%s)
    }
    
    # Function called after each command
    __notification_precmd() {
        local exit_code=$?
        local end_time=$(date +%s)
        local duration=$((end_time - __notification_cmd_start_time))
        
        # Only notify if command took longer than minimum duration
        if [ $duration -ge $NOTIFICATION_MIN_DURATION ] && [ $__notification_cmd_start_time -ne 0 ]; then
            local cmd_status="completed"
            if [ $exit_code -ne 0 ]; then
                cmd_status="failed"
            fi
            
            local last_cmd=$(HISTTIMEFORMAT= history 1 | sed 's/^[ ]*[0-9]*[ ]*//')
            
            # Send notification
            __send_notification "Command $cmd_status" "$last_cmd (${duration}s, exit: $exit_code)"
        fi
        
        __notification_cmd_start_time=0
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
        local end_time=$(date +%s)
        local duration=$((end_time - __notification_cmd_start_time))
        
        # Only notify if command took longer than minimum duration
        if [ $duration -ge $NOTIFICATION_MIN_DURATION ] && [ $__notification_cmd_start_time -ne 0 ]; then
            local cmd_status="completed"
            if [ $exit_code -ne 0 ]; then
                cmd_status="failed"
            fi
            
            # In zsh, the last command is available in $history[1] or we can use fc
            local last_cmd=$(fc -ln -1 2>/dev/null || echo "unknown command")
            
            # Send notification
            __send_notification "Command $cmd_status" "$last_cmd (${duration}s, exit: $exit_code)"
        fi
        
        __notification_cmd_start_time=0
    }
    
    # Set up zsh hooks
    autoload -Uz add-zsh-hook 2>/dev/null || return
    add-zsh-hook preexec __notification_preexec
    add-zsh-hook precmd __notification_precmd
fi
