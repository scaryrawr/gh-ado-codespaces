#!/usr/bin/env bash

# Associative array to store currently bound ports
# Key: "protocol:port", Value: 1
declare -A bound_ports

# Function to send JSON messages to stdout
send_message() {
	local type="$1"
	# For type="port", $2=action, $3=port, $4=protocol
	# For type="log", $2=message
	local timestamp
	# Cross-platform date command (works on both Linux and macOS)
	if date --version >/dev/null 2>&1; then
		# GNU date (Linux)
		timestamp=$(date -u +"%Y-%m-%dT%H:%M:%S.%3NZ")
	else
		# BSD date (macOS)
		timestamp=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
	fi

	if [ "$type" = "port" ]; then
		local action="$2"
		local port_num="$3"
		local protocol_val="$4"
		jq -n -c \
			--arg type "port" \
			--arg action "$action" \
			--argjson port "$port_num" \
			--arg protocol "$protocol_val" \
			--arg timestamp "$timestamp" \
			'{type: $type, action: $action, port: $port, protocol: $protocol, timestamp: $timestamp}'
	elif [ "$type" = "log" ]; then
		local message="$2"
		jq -n -c \
			--arg type "log" \
			--arg message "$message" \
			--arg timestamp "$timestamp" \
			'{type: $type, message: $message, timestamp: $timestamp}'
	fi
}

# Cleanup function for graceful shutdown
cleanup() {
	send_message "log" "Signal received, shutting down port monitor..."
	exit 0
}

# Trap SIGINT (Ctrl+C) and SIGTERM signals
trap 'cleanup' SIGINT SIGTERM

# Initial starting message
send_message "log" "Port monitor starting..."

# Main monitoring loop
while true; do
	# Associative array to store ports found in the current scan
	declare -A current_ports_map=()

	# Read listening ports using ss
	# Process substitution <(...) is used to avoid issues with variables in subshells
	while IFS= read -r line; do
		# Parse line using bash built-in parameter expansion
		# $1 is protocol (tcp/udp), $5 is LocalAddress:Port (e.g., 0.0.0.0:8080 or [::]:80)
		read -r protocol _ _ _ local_address_port _ <<<"$line"

		# Extract port from LocalAddress:Port (it's the part after the last colon)
		port="${local_address_port##*:}"

		# Validate port is a number and filter out well-known ports (0-1023)
		if [[ "$port" =~ ^[0-9]+$ ]] && [ "$port" -gt 1023 ]; then
			key="${protocol}:${port}"
			current_ports_map["$key"]=1

			# If this is a new port (not in our bound_ports list), record it and send 'bound' event
			if [[ -z "${bound_ports[$key]}" ]]; then
				bound_ports["$key"]=1
				send_message "port" "bound" "$port" "$protocol"
			fi
		fi
	done < <(ss -tulpn 2>/dev/null | grep LISTEN)

	# Check for unbound ports
	# Iterate over keys of bound_ports. If a key is not in current_ports_map, it means the port was unbound.
	for key_in_bound_ports in "${!bound_ports[@]}"; do
		if [[ -z "${current_ports_map[$key_in_bound_ports]}" ]]; then
			# Port is no longer bound - use parameter expansion instead of cut
			protocol_val="${key_in_bound_ports%%:*}"
			port_val="${key_in_bound_ports##*:}"

			send_message "port" "unbound" "$port_val" "$protocol_val"
			unset "bound_ports[$key_in_bound_ports]" # Remove from our tracked list
		fi
	done

	sleep 2 # Interval between checks
done
