# Port Forwarding

[← Back to README](../README.md)

The automatic port forwarding system works in two directions:

## Forward Port Forwarding (Codespace → Local Machine)

The extension detects when applications in your codespace start listening on network ports:

1. A port monitor runs in your codespace and detects new listening ports
2. Port events are sent to your local machine through the SSH connection
3. New SSH tunnels are created automatically for each detected port
4. Applications running in your codespace become accessible via `localhost:<port>` locally

## Reverse Port Forwarding (Local Machine → Codespace)

The extension automatically shares local AI services to your codespace:

1. When connecting, the extension checks if Ollama (port 11434) or LM Studio (port 1234) are running locally
2. If either service is detected, a reverse SSH tunnel is automatically created
3. Your codespace can then access these services via `localhost:1234` or `localhost:11434`
4. This enables you to use your local AI models and services from within your codespace

You can add or override reverse-forwarded ports in `config.json` using `reversePortForward` at the top level (all accounts) or within `accounts.<login>.reversePortForward` for account-specific overrides.

This is particularly useful for:
- Running Ollama models on your local machine while coding in a codespace
- Using LM Studio's local inference server from your codespace
- Sharing any locally-running AI services with your remote development environment
