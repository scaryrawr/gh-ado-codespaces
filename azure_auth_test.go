package main

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestServerConfigCloseIsNilSafeAndIdempotent(t *testing.T) {
	(*ServerConfig)(nil).Close()

	file, err := os.CreateTemp(t.TempDir(), "auth.log")
	if err != nil {
		t.Fatal(err)
	}
	acceptDone := make(chan struct{})
	close(acceptDone)
	server := &ServerConfig{
		logger:     &authLog{file: file},
		acceptDone: acceptDone,
	}
	server.Close()
	server.Close()

	if _, err := file.WriteString("closed"); err == nil {
		t.Fatal("auth log file remained open")
	}
}

func TestServerConfigCloseWaitsForAcceptLoop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	listener, port, acceptDone, _, err := startServer(ctx, nil, &authLog{})
	if err != nil {
		t.Fatal(err)
	}
	server := &ServerConfig{
		Port:       port,
		Listener:   listener,
		logger:     &authLog{},
		acceptDone: acceptDone,
	}
	server.Close()

	select {
	case <-acceptDone:
	default:
		t.Fatal("ServerConfig.Close returned before the accept loop ended")
	}
}

func TestServerConfigCloseClosesActiveConnections(t *testing.T) {
	listener, port, acceptDone, connections, err := startServer(context.Background(), nil, &authLog{})
	if err != nil {
		t.Fatal(err)
	}
	server := &ServerConfig{
		Port:        port,
		Listener:    listener,
		logger:      &authLog{},
		acceptDone:  acceptDone,
		connections: connections,
	}
	client, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	eventually(t, func() bool { return connections.Count() == 1 })

	done := make(chan struct{})
	go func() {
		server.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("ServerConfig.Close did not close a partial request")
	}
	if connections.Count() != 0 {
		t.Fatalf("active connection count = %d, want 0", connections.Count())
	}
}

func TestNewAuthLogRestrictsPermissions(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "private", "agent.log")
	logger, err := newAuthLog(logPath)
	if err != nil {
		t.Fatal(err)
	}
	logger.Close()

	directoryInfo, err := os.Stat(filepath.Dir(logPath))
	if err != nil {
		t.Fatal(err)
	}
	if permissions := directoryInfo.Mode().Perm(); permissions != 0700 {
		t.Fatalf("auth log directory permissions = %04o, want 0700", permissions)
	}

	fileInfo, err := os.Stat(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if permissions := fileInfo.Mode().Perm(); permissions != 0600 {
		t.Fatalf("auth log file permissions = %04o, want 0600", permissions)
	}
}

func TestHandleConnectionForwardsMultipleScopes(t *testing.T) {
	serverConnection, clientConnection := net.Pipe()
	provider := &recordingTokenProvider{token: "test-token"}
	done := make(chan struct{})
	go func() {
		handleConnection(context.Background(), serverConnection, provider, &authLog{})
		close(done)
	}()

	request := `{"type":"getAccessToken","data":{"scopes":"scope-a scope-b"}}` + "\f"
	if _, err := clientConnection.Write([]byte(request)); err != nil {
		t.Fatal(err)
	}

	responseFrame, err := bufio.NewReader(clientConnection).ReadString('\f')
	if err != nil {
		t.Fatal(err)
	}
	var response TokenResponse
	if err := json.Unmarshal([]byte(responseFrame[:len(responseFrame)-1]), &response); err != nil {
		t.Fatal(err)
	}
	if response.Type != "accessToken" || response.Data != "test-token" {
		t.Fatalf("response = %#v, want access token response", response)
	}
	if provider.target.kind != tokenTargetScopes {
		t.Fatalf("target kind = %v, want tokenTargetScopes", provider.target.kind)
	}
	if want := []string{"scope-a", "scope-b"}; !reflect.DeepEqual(provider.target.values, want) {
		t.Fatalf("target values = %v, want %v", provider.target.values, want)
	}

	if err := clientConnection.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handleConnection did not stop after the client closed")
	}
}

type recordingTokenProvider struct {
	target tokenTarget
	token  string
}

func (p *recordingTokenProvider) GetAccessToken(_ context.Context, target tokenTarget) (string, error) {
	p.target = target

	return p.token, nil
}
