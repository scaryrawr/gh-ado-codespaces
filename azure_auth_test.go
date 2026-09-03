package main

import (
	"context"
	"net"
	"os"
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
