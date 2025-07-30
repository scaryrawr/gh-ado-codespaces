package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/google/uuid"
)

// Global variables for auth logging
var (
	authLogFile *os.File
	authLogger  *log.Logger
)

// getAuthLogDirectory returns the temporary directory for auth logs.
// It's specific to auth to avoid conflicts if other parts of the app also use getLogDirectory.
func getAuthLogDirectory() string {
	tempDir := os.TempDir()
	return filepath.Join(tempDir, "gh-ado-codespaces", "logs")
}

// initAuthLogger initializes a logger that writes to a file for auth operations.
func initAuthLogger() error {
	logDir := getAuthLogDirectory()
	if err := os.MkdirAll(logDir, 0755); err != nil {
		// Cannot use logAuthMessage here as logger is not yet initialized.
		// Print to Stderr for critical initialization failures.
		fmt.Fprintf(os.Stderr, "CRITICAL: Failed to create auth log directory '%s': %v\\n", logDir, err)
		return fmt.Errorf("failed to create auth log directory: %w", err)
	}

	timestamp := time.Now().Format("2006-01-02_150405")
	pid := os.Getpid()
	logPath := filepath.Join(logDir, fmt.Sprintf("azure-auth-%s-pid%d.log", timestamp, pid))

	var err error
	authLogFile, err = os.Create(logPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "CRITICAL: Failed to create auth log file '%s': %v\\n", logPath, err)
		return fmt.Errorf("failed to create auth log file: %w", err)
	}

	authLogger = log.New(authLogFile, "AUTH: ", log.LstdFlags|log.Lmicroseconds)
	authLogger.Printf("Auth logging initialized to %s", logPath)
	// Inform user via stderr where logs are, as this is a critical setup step.
	// fmt.Fprintf(os.Stderr, "Azure auth logs will be written to: %s\\n", logPath)
	return nil
}

// logAuthMessage logs a message to the auth log file.
func logAuthMessage(format string, args ...interface{}) {
	if authLogger != nil {
		authLogger.Printf(format, args...)
	} else {
		// Fallback if logger is somehow not initialized, though this should ideally not happen post-SetupServer.
		fmt.Fprintf(os.Stderr, "FALLBACK AUTH LOG (logger not init): "+format+"\\n", args...)
	}
}

// startServer initializes and starts the local TCP server for authentication.
// It now takes a context for cancellation.
func startServer(ctx context.Context) (net.Listener, int, error) {
	listener, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		// logAuthMessage already called by SetupServer if this fails
		return nil, 0, fmt.Errorf("failed to start local server: %w", err)
	}

	cred, err := azidentity.NewAzureCLICredential(nil)
	if err != nil {
		listener.Close() // Clean up listener if credential creation fails
		// logAuthMessage already called by SetupServer
		return nil, 0, fmt.Errorf("failed to create Azure CLI credential: %w", err)
	}

	port := listener.Addr().(*net.TCPAddr).Port
	logAuthMessage("Local auth server listening on port %d", port)

	go func() {
		for {
			select {
			case <-ctx.Done():
				logAuthMessage("Server context for port %d canceled, stopping accept loop.", port)
				listener.Close() // Ensure listener is closed when context is done
				return
			default:
			}

			conn, err := listener.Accept()
			if err != nil {
				select {
				case <-ctx.Done():
					logAuthMessage("Accept loop for port %d: context canceled during Accept(): %v", port, err)
					return // Exit goroutine
				default:
					if strings.Contains(err.Error(), "use of closed network connection") {
						logAuthMessage("Accept loop for port %d: Listener closed normally.", port)
					} else if ne, ok := err.(net.Error); ok && ne.Temporary() {
						logAuthMessage("Temporary error accepting on port %d: %v. Retrying.", port, err)
						time.Sleep(100 * time.Millisecond) // Brief pause
						continue
					} else {
						logAuthMessage("Persistent error accepting on port %d: %v. Stopping loop.", port, err)
					}
					return // Stop loop for persistent or non-temporary errors
				}
			}
			logAuthMessage("Accepted new connection from %s on port %d", conn.RemoteAddr().String(), port)
			go handleConnection(ctx, conn, cred) // Pass context
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

// handleConnection processes a single client connection.
// It now takes a context for cancellation.
func handleConnection(ctx context.Context, conn net.Conn, cred *azidentity.AzureCLICredential) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	clientAddr := conn.RemoteAddr().String()

	logAuthMessage("Handling connection from %s", clientAddr)

	for {
		select {
		case <-ctx.Done():
			logAuthMessage("Context canceled for connection %s before reading.", clientAddr)
			return
		default:
		}

		line, err := reader.ReadString('\f')
		if err != nil {
			select {
			case <-ctx.Done():
				logAuthMessage("Context canceled while reading from %s: %v", clientAddr, err)
			default:
				if err.Error() == "EOF" || strings.Contains(err.Error(), "connection reset by peer") {
					logAuthMessage("Client %s closed connection (EOF or reset).", clientAddr)
				} else if strings.Contains(err.Error(), "use of closed network connection") {
					logAuthMessage("Connection %s closed locally while reading.", clientAddr)
				} else {
					logAuthMessage("Error reading from %s: %v", clientAddr, err)
				}
			}
			break // Exit loop on any read error or context cancellation
		}
		logAuthMessage("Raw data from %s: %s", clientAddr, line) // Verbose

		jsonData := line[:len(line)-1] // Trim the delimiter

		var tokenReq TokenRequest
		if err := json.Unmarshal([]byte(jsonData), &tokenReq); err != nil {
			logAuthMessage("Error unmarshalling request from %s: %v. JSON: %s", clientAddr, err, jsonData)
			continue
		}

		logAuthMessage("Request from %s - Type: '%s', Scopes: %v", clientAddr, tokenReq.Type, tokenReq.Data.Scopes)

		if tokenReq.Type == "getAccessToken" {
			var scopes []string
			if tokenReq.Data.Scopes == nil || *tokenReq.Data.Scopes == "" {
				scopes = []string{"499b84ac-1321-427f-aa17-267ca6975798/.default"}
				logAuthMessage("No scopes from %s, using default: %v", clientAddr, scopes)
			} else {
				scopes = strings.Split(*tokenReq.Data.Scopes, " ")
				logAuthMessage("Scopes from %s: %v", clientAddr, scopes)
			}

			token, err := cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: scopes}) // Pass context
			if err != nil {
				logAuthMessage("Error getting token for %s (scopes %v): %v", clientAddr, scopes, err)
				continue
			}

			logAuthMessage("Successfully obtained token for %s (scopes %v)", clientAddr, scopes) // Token itself not logged

			tokenResp := TokenResponse{
				Type: "accessToken",
				Data: token.Token,
			}

			respBytes, err := json.Marshal(tokenResp)
			if err != nil {
				logAuthMessage("Error marshalling response for %s: %v", clientAddr, err)
				continue
			}

			_, err = writer.Write(append(respBytes, '\f'))
			if err != nil {
				logAuthMessage("Error writing response to %s: %v", clientAddr, err)
				break
			}
			err = writer.Flush()
			if err != nil {
				logAuthMessage("Error flushing writer for %s: %v", clientAddr, err)
				break
			}
			logAuthMessage("Sent accessToken response to %s", clientAddr)
		} else {
			logAuthMessage("Received unknown message type '%s' from %s", tokenReq.Type, clientAddr)
		}
	}
	logAuthMessage("Finished handling connection for %s", clientAddr)
}

// ServerConfig holds configuration for the local auth server
type ServerConfig struct {
	SocketPath string
	Port       int
	Listener   net.Listener
	loggerFile *os.File // To manage log file lifecycle
}

// Close stops the listener and closes the log file.
func (sc *ServerConfig) Close() {
	logAuthMessage("Closing server resources for port %d...", sc.Port)
	if sc.Listener != nil {
		logAuthMessage("Closing listener for port %d.", sc.Port)
		sc.Listener.Close()
	}
	if sc.loggerFile != nil {
		logAuthMessage("Closing auth logger file: %s", sc.loggerFile.Name())
		sc.loggerFile.Close()
		// Clear global references to prevent use-after-close
		authLogFile = nil
		authLogger = nil
	}
	logAuthMessage("Server resources for port %d closed.", sc.Port)
}

// SetupServer initializes the local server and returns its configuration.
// It now takes a context for cancellation.
func SetupServer(ctx context.Context) (*ServerConfig, error) {
	if err := initAuthLogger(); err != nil {
		// initAuthLogger already prints to Stderr for critical failures.
		return nil, fmt.Errorf("failed to initialize auth logger: %w", err)
	}

	logAuthMessage("Attempting to start auth server...")
	listener, port, err := startServer(ctx) // Pass context
	if err != nil {
		logAuthMessage("Error starting server components: %v", err)
		// Ensure logger is closed if setup fails mid-way
		if authLogFile != nil {
			authLogFile.Close() // This will also be caught by ServerConfig.Close if it was set
			authLogFile = nil
			authLogger = nil
		}
		return nil, fmt.Errorf("error starting server: %w", err)
	}

	socketId := uuid.New()
	socketPath := "/tmp/ado-auth-" + socketId.String() + ".sock"

	logAuthMessage("Server successfully started on port %d, socket path %s", port, socketPath)

	return &ServerConfig{
		SocketPath: socketPath,
		Port:       port,
		Listener:   listener,
		loggerFile: authLogFile, // Store the log file handle
	}, nil
}
