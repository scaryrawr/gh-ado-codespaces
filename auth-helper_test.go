package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestADOAuthHelper(t *testing.T) {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not available")
	}

	helperPath := filepath.Join(t.TempDir(), "ado-auth-helper")
	if err := os.WriteFile(helperPath, []byte(adoAuthHelperScript), 0o700); err != nil {
		t.Fatalf("failed to write auth helper: %v", err)
	}

	socketFile, err := os.CreateTemp("/tmp", "ado-auth-test-*.sock")
	if err != nil {
		t.Fatalf("failed to reserve test socket path: %v", err)
	}
	socketPath := socketFile.Name()
	if err := socketFile.Close(); err != nil {
		t.Fatalf("failed to close test socket path: %v", err)
	}
	if err := os.Remove(socketPath); err != nil {
		t.Fatalf("failed to prepare test socket path: %v", err)
	}

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("failed to create test auth socket: %v", err)
	}
	defer listener.Close()
	defer os.Remove(socketPath)

	serverDone := make(chan error, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			serverDone <- acceptErr
			return
		}
		defer connection.Close()

		request, readErr := bufio.NewReader(connection).ReadString('\f')
		if readErr != nil {
			serverDone <- readErr
			return
		}
		if request != "{\"type\":\"getAccessToken\",\"data\":{}}\f" {
			serverDone <- fmt.Errorf("unexpected request: %q", request)
			return
		}

		_, writeErr := connection.Write([]byte("{\"type\":\"accessToken\",\"data\":\"test-token\"}\f"))
		serverDone <- writeErr
	}()

	command := exec.Command(nodePath, helperPath, "get-access-token")
	command.Env = append(os.Environ(), "GH_ADO_CODESPACES_AUTH_SOCKET="+socketPath)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("auth helper failed: %v\nOutput: %s", err, output)
	}
	if strings.TrimSpace(string(output)) != "test-token" {
		t.Fatalf("auth helper output = %q, want %q", output, "test-token")
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("test auth server failed: %v", err)
	}
}
