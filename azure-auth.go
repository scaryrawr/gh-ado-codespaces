package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/google/uuid"
)

func startServer() (net.Listener, int, error) {
	// Start a local server to listen for the redirect
	listener, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		return nil, 0, fmt.Errorf("failed to start local server: %w", err)
	}

	cred, err := azidentity.NewAzureCLICredential(nil)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to create Azure CLI credential: %w", err)
	}

	// Get the port number
	port := listener.Addr().(*net.TCPAddr).Port

	// Start a goroutine to handle incoming connections
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				fmt.Println("Error accepting connection:", err)
				continue
			}
			go handleConnection(conn, cred)
		}
	}()

	return listener, port, nil
}

type TokenRequest struct {
	Type string `json:"type"`
	Data struct {
		Scopes *string `json:"scopes"`
	} `json:"data"`
}

type TokenResponse struct {
	Type string `json:"type"`
	Data string `json:"data"`
}

func handleConnection(conn net.Conn, cred *azidentity.AzureCLICredential) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)

	for {
		line, err := reader.ReadString('\f')
		if err != nil {
			// Client closed
			break
		}
		// fmt.Print("Received from client:", line)

		// Trim the delimiter
		jsonData := line[:len(line)-1]

		var tokenReq TokenRequest
		if err := json.Unmarshal([]byte(jsonData), &tokenReq); err != nil {
			fmt.Println("Error unmarshalling request:", err)
			// Optionally send an error response back to the client
			continue
		}

		if tokenReq.Type == "getAccessToken" {
			var scopes []string
			if tokenReq.Data.Scopes == nil || *tokenReq.Data.Scopes == "" {
				// Scopes not provided or empty, using default scope
				scopes = []string{"499b84ac-1321-427f-aa17-267ca6975798/.default"}
			} else {
				scopes = strings.Split(*tokenReq.Data.Scopes, " ")
			}

			token, err := cred.GetToken(context.Background(), policy.TokenRequestOptions{Scopes: scopes})
			if err != nil {
				fmt.Println("Error getting token:", err)
				// Optionally send an error response back to the client
				continue
			}

			tokenResp := TokenResponse{
				Type: "accessToken",
				Data: token.Token,
			}

			respBytes, err := json.Marshal(tokenResp)
			if err != nil {
				fmt.Println("Error marshalling response:", err)
				continue
			}

			_, err = writer.Write(append(respBytes, '\f'))
			if err != nil {
				fmt.Println("Error writing response:", err)
				break
			}
			writer.Flush()
			// Sent accessToken response to client
		} else {
			fmt.Println("Received unknown message type:", tokenReq.Type)
		}
	}
}

// ServerConfig holds configuration for the local auth server
type ServerConfig struct {
	SocketPath string
	Port       int
	Listener   net.Listener
}

// SetupServer initializes the local server and returns its configuration
func SetupServer(ctx context.Context) (*ServerConfig, error) {
	listener, port, err := startServer()
	if err != nil {
		return nil, fmt.Errorf("error starting server: %w", err)
	}

	socketId := uuid.New()
	socketPath := "/tmp/ado-auth-" + socketId.String() + ".sock"

	fmt.Printf("Server started on port %d\n", port)

	return &ServerConfig{
		SocketPath: socketPath,
		Port:       port,
		Listener:   listener,
	}, nil
}
