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
    timestamp=$(date -u +"%Y-%m-%dT%H:%M:%S.%3NZ") # ISO 8601 format for Linux

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
    declare -A current_ports_map
    unset current_ports_map
    declare -A current_ports_map

    # Read listening ports using ss
    # Process substitution <(...) is used to avoid issues with variables in subshells
    while IFS= read -r line; do
        # $1 is protocol (tcp/udp), $5 is LocalAddress:Port (e.g., 0.0.0.0:8080 or [::]:80)
        protocol=$(echo "$line" | awk '{print $1}')
        local_address_port=$(echo "$line" | awk '{print $5}')

        # Extract port from LocalAddress:Port (it's the part after the last colon)
        port=$(echo "$local_address_port" | awk -F: '{print $NF}')

        # Validate port is a number
        if ! [[ "$port" =~ ^[0-9]+$ ]]; then
            # Optional: send_message "log" "Failed to parse port from line: $line"
            continue
        fi

        # Filter out well-known ports (0-1023)
        if [ "$port" -le 1023 ]; then
            continue
        fi

        key="${protocol}:${port}"
        current_ports_map["$key"]=1

        # If this is a new port (not in our bound_ports list), record it and send 'bound' event
        if [[ -z "${bound_ports[$key]}" ]]; then
            bound_ports["$key"]=1
            send_message "port" "bound" "$port" "$protocol"
        fi
    done < <(ss -tulpn 2>/dev/null | grep LISTEN)

    # Check for unbound ports
    # Iterate over keys of bound_ports. If a key is not in current_ports_map, it means the port was unbound.
    for key_in_bound_ports in "${!bound_ports[@]}"; do
        if [[ -z "${current_ports_map[$key_in_bound_ports]}" ]]; then
            # Port is no longer bound
            protocol_val=$(echo "$key_in_bound_ports" | cut -d: -f1)
            port_val=$(echo "$key_in_bound_ports" | cut -d: -f2)

            send_message "port" "unbound" "$port_val" "$protocol_val"
            unset "bound_ports[$key_in_bound_ports]" # Remove from our tracked list
        fi
    done

    sleep 2 # Interval between checks
done
