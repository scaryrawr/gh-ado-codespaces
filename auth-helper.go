package main

import (
	"context"
	"fmt"
	"os"

	"github.com/cli/go-gh/v2"
)

// adoAuthHelperScript contains the Python script that implements the ADO auth helper
const adoAuthHelperScript = `#!/usr/bin/env python3

# Helper script for Azure DevOps authentication
# This script connects to the local authentication service via the socket
# and returns tokens in Git credential helper format.

import sys
import os
import socket
import json
import glob
import re

def read_stdin():
    """Read all input from stdin until EOF."""
    lines = []
    try:
        for line in sys.stdin:
            lines.append(line)
    except KeyboardInterrupt:
        pass
    return ''.join(lines)

def get_access_token_from_socket(socket_path, scopes=None):
    """
    Connect to a Unix socket and request an access token.
    
    Args:
        socket_path: Path to the Unix socket
        scopes: Optional space-separated scopes
        
    Returns:
        The token string on success, None on failure
    """
    # Create request JSON
    request_data = {"type": "getAccessToken"}
    
    # Only include scopes in the data if they're provided
    if scopes:
        request_data["data"] = {"scopes": scopes}
    else:
        # Send an empty data object instead of one with null/empty scopes
        request_data["data"] = {}
        
    # Ensure compact JSON output (no whitespace, single line)
    json_data = json.dumps(request_data, separators=(',', ':')) + '\f'
    
    try:
        # Connect to the Unix socket
        sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        sock.settimeout(60)  # 60 second timeout
        sock.connect(socket_path)
        
        # Send request
        sock.sendall(json_data.encode('utf-8'))
        
        # Receive response
        response_data = sock.recv(16384)
        sock.close()
        
        if not response_data:
            return None
            
        # Parse JSON response - handle form feed character if present
        response_str = response_data.decode('utf-8', errors='ignore')
        if '\f' in response_str:
            # Split on form feed and take the first part that contains the JSON
            response_str = response_str.split('\f')[0]
            
        try:
            response = json.loads(response_str)
            
            # Extract token
            if response and 'data' in response:
                return response['data']
        except json.JSONDecodeError:
            return None
            
    except Exception:
        # Any error means we couldn't get a token from this socket
        pass
        
    return None

def get_access_token(scopes=None):
    """
    Find all valid auth sockets and try to get a token from each.
    
    Args:
        scopes: Optional space-separated scopes
        
    Returns:
        A token string or exits with error if no token found
    """
    # Find all ado-auth sockets
    socket_paths = glob.glob('/tmp/ado-auth-*.sock')
    
    if not socket_paths:
        sys.exit(1)
    
    # Try each socket
    for socket_path in socket_paths:
        token = get_access_token_from_socket(socket_path, scopes)
        if token:
            return token
    
    # If we get here, all sockets failed
    sys.exit(1)

def is_git_asking_for_ado_repo():
    """Check if Git is asking for an Azure DevOps repository."""
    input_text = read_stdin()
    if re.search(r'dev\.azure\.com|\.visualstudio\.com', input_text):
        return True
    return False

def main():
    """Main entry point."""
    if len(sys.argv) < 2:
        sys.exit(1)
        
    command = sys.argv[1]
    
    # Handle "get" command
    if command == "get":
        if is_git_asking_for_ado_repo():
            token = get_access_token()
            print("username=token")
            print("password=" + token)
    
    # Handle "get-access-token" command
    elif command == "get-access-token":
        scope = None
        # For azure-auth-helper, check if we have a scope parameter
        script_name = os.path.basename(sys.argv[0])
        if script_name == "azure-auth-helper" and len(sys.argv) > 2:
            scope = sys.argv[2]
            
        token = get_access_token(scope)
        print(token)
    
    # Flush stdout to ensure output is sent immediately
    sys.stdout.flush()

if __name__ == "__main__":
    main()
`

// UploadAuthHelpers uploads and configures the ADO and Azure auth helper scripts to the specified codespace
func UploadAuthHelpers(ctx context.Context, codespaceName string) error {
	// Create temporary files for both helper scripts
	adoHelperFile, err := os.CreateTemp("", "ado-auth-helper*.py")
	if err != nil {
		return fmt.Errorf("failed to create temporary file for ADO helper: %w", err)
	}
	defer os.Remove(adoHelperFile.Name())

	// Both helpers use the same script
	if _, err = adoHelperFile.WriteString(adoAuthHelperScript); err != nil {
		return fmt.Errorf("failed to write ADO helper script to temporary file: %w", err)
	}
	adoHelperFile.Close()

	// Upload the ADO helper script
	args := []string{"codespace", "cp", "-c", codespaceName, "-e", adoHelperFile.Name(), "remote:~/ado-auth-helper"}
	_, stderr, err := gh.Exec(args...)
	if err != nil {
		return fmt.Errorf("error copying ADO helper script to codespace: %w\nStderr: %s", err, stderr.String())
	}

	// Upload the same script as Azure helper
	args = []string{"codespace", "cp", "-c", codespaceName, "-e", adoHelperFile.Name(), "remote:~/azure-auth-helper"}
	_, stderr, err = gh.Exec(args...)
	if err != nil {
		return fmt.Errorf("error copying Azure helper script to codespace: %w\nStderr: %s", err, stderr.String())
	}

	// Make both scripts executable
	err = makeHelpersExecutable(ctx, codespaceName)
	if err != nil {
		return fmt.Errorf("failed to make helper scripts executable: %w", err)
	}

	fmt.Println("ADO and Azure auth helpers installed in the codespace")
	return nil
}

// makeHelpersExecutable makes the auth helper scripts executable on the codespace
func makeHelpersExecutable(ctx context.Context, codespaceName string) error {
	args := []string{"codespace", "ssh", "--codespace", codespaceName, "--", "chmod", "+x", "~/ado-auth-helper", "~/azure-auth-helper"}
	_, stderr, err := gh.Exec(args...)
	if err != nil {
		return fmt.Errorf("error making helper scripts executable: %w\nStderr: %s", err, stderr.String())
	}

	return nil
}
