# Command Completion Notifications

[← Back to README](../README.md)

The extension provides command completion notifications from your codespace to your local machine, inspired by the [done](https://github.com/franciscolourenco/done) project. Get desktop notifications when long-running commands finish:

1. When you connect, a notification sender script is uploaded to your codespace at `~/notification-sender.sh`
2. A local notification service is started that listens for notification requests
3. The service port is forwarded to the codespace via SSH reverse port forwarding
4. Users can enable notifications by adding to their shell config:
   
   **Bash or Zsh** (`~/.bashrc` or `~/.zshrc`):
   ```bash
   # For bash or zsh
   if [ -f "$HOME/notification-sender.sh" ]; then
       source "$HOME/notification-sender.sh"
   fi
   ```
   
   **Fish shell** (`~/.config/fish/config.fish`):
   
   For Fish shell users, we recommend using the [done](https://github.com/franciscolourenco/done) plugin which provides native Fish integration for command completion notifications.
   
   Install using Fisher:
   ```fish
   fisher install franciscolourenco/done
   ```
   
   Or install manually:
   ```fish
   curl -Lo ~/.config/fish/conf.d/done.fish --create-dirs https://raw.githubusercontent.com/franciscolourenco/done/master/conf.d/done.fish
   ```
   
   After installing the `done` plugin, configure it to use the gh-ado-codespaces notification system by creating a custom notification command in your Fish config:
   ```fish
   # Configure done plugin to use gh-ado-codespaces notification service
   set -U __done_notification_command "~/notification-sender.sh send '\$title' '\$message'"
   
   # Set minimum command duration (default is 5000 ms = 5 seconds)
   set -U __done_min_cmd_duration 5000
   ```
   
   You'll also need to create a helper wrapper script at `~/notification-sender.sh` in your codespace:
   ```bash
   #!/usr/bin/env bash
   # Wrapper for Fish done plugin to send notifications
   
   if [ "$1" = "send" ]; then
       title="$2"
       message="$3"
       
       # Find notification socket
       SOCKET=$(find /tmp -maxdepth 1 -name "gh-ado-notification-*.sock" -type s -exec ls -t {} + 2>/dev/null | head -1)
       
       if [ -n "$SOCKET" ]; then
           # Send notification via curl
           curl -s --max-time 2 --unix-socket "$SOCKET" -X POST \
               -H "Content-Type: application/json" \
               -d "{\"title\":\"$title\",\"message\":\"$message\"}" \
               "http://localhost/notify" >/dev/null 2>&1
       fi
   fi
   ```
   
   Make the script executable:
   ```bash
   chmod +x ~/notification-sender.sh
   ```

5. When a command takes longer than 5 seconds (configurable), you'll receive a desktop notification with:
   - Command status (completed or failed)
   - The command that was run
   - Duration and exit code

## Configuration

You can customize the notification behavior with environment variables:

```bash
# Set minimum command duration (in seconds) before triggering a notification
export NOTIFICATION_MIN_DURATION=10  # Default is 5 seconds
```

## Supported Shells

- Bash (via DEBUG trap and PROMPT_COMMAND)
- Zsh (via preexec and precmd hooks)
- Fish (via the [done](https://github.com/franciscolourenco/done) plugin - recommended for Fish users)

The notification system works cross-platform and uses:
- **macOS**: Native notification center
- **Linux**: notify-send (via D-Bus)
- **Windows**: Windows notification system

This is particularly useful for:
- Getting notified when builds, tests, or deployments finish
- Switching away from your terminal while waiting for long-running tasks
- Monitoring command failures even when not actively watching the terminal
- Improving productivity by staying informed of command completions

**Note:** After adding the configuration to your shell, reload it with `source ~/.bashrc` (or `~/.zshrc`) or start a new shell session.
