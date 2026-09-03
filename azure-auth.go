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
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/google/uuid"
)

type authLog struct {
	file   *os.File
	logger *log.Logger
	once   sync.Once
}

type activeConnections struct {
	mu       sync.Mutex
	closed   bool
	conns    map[net.Conn]struct{}
	handlers sync.WaitGroup
}

func newActiveConnections() *activeConnections {
	return &activeConnections{conns: make(map[net.Conn]struct{})}
}

func (c *activeConnections) Track(conn net.Conn) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		_ = conn.Close()
		return false
	}
	c.conns[conn] = struct{}{}
	c.handlers.Add(1)
	return true
}

func (c *activeConnections) Done(conn net.Conn) {
	c.mu.Lock()
	delete(c.conns, conn)
	c.mu.Unlock()
	c.handlers.Done()
}

func (c *activeConnections) Close() {
	c.mu.Lock()
	c.closed = true
	conns := make([]net.Conn, 0, len(c.conns))
	for conn := range c.conns {
		conns = append(conns, conn)
	}
	c.mu.Unlock()
	for _, conn := range conns {
		_ = conn.Close()
	}
	c.handlers.Wait()
}

func (c *activeConnections) Count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.conns)
}

// getAuthLogDirectory returns the temporary directory for auth logs.
// It's specific to auth to avoid conflicts if other parts of the app also use getLogDirectory.
func getAuthLogDirectory() string {
	tempDir := os.TempDir()
	return filepath.Join(tempDir, "gh-ado-codespaces", "logs")
}

func newAuthLog(logPath string) (*authLog, error) {
	if logPath == "" {
		if err := ensureSessionLogDirectory(); err != nil {
			fmt.Fprintf(os.Stderr, "CRITICAL: Failed to create session log directory: %v\\n", err)
			return nil, fmt.Errorf("failed to create session log directory: %w", err)
		}
		logPath = getSessionLogPath("azure-auth.log")
	} else if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		return nil, fmt.Errorf("failed to create auth log directory: %w", err)
	}

	file, err := os.Create(logPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "CRITICAL: Failed to create auth log file '%s': %v\\n", logPath, err)
		return nil, fmt.Errorf("failed to create auth log file: %w", err)
	}

	result := &authLog{file: file, logger: log.New(file, "AUTH: ", log.LstdFlags|log.Lmicroseconds)}
	result.Printf("Auth logging initialized to %s", logPath)
	return result, nil
}

func (l *authLog) Printf(format string, args ...any) {
	if l != nil && l.logger != nil {
		l.logger.Printf(format, args...)
	}
}

func (l *authLog) Close() {
	if l == nil {
		return
	}
	l.once.Do(func() {
		if l.file == nil {
			return
		}
		if err := l.file.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "failed to close auth log: %s\n", redactSecrets(err.Error()))
		}
	})
}

// startServer initializes and starts the local TCP server for authentication.
// It now takes a context for cancellation.
func startServer(ctx context.Context, cred azcore.TokenCredential, logger *authLog) (net.Listener, int, <-chan struct{}, *activeConnections, error) {
	listener, err := net.Listen("tcp", localServiceHost+":0")
	if err != nil {
		return nil, 0, nil, nil, fmt.Errorf("failed to start local server: %w", err)
	}

	port := listener.Addr().(*net.TCPAddr).Port
	logger.Printf("Local auth server listening on port %d", port)
	done := make(chan struct{})
	stopAccept := make(chan struct{})
	connections := newActiveConnections()
	var listenerCloseOnce sync.Once
	closeListener := func() {
		listenerCloseOnce.Do(func() {
			_ = listener.Close()
		})
	}
	go func() {
		select {
		case <-ctx.Done():
			closeListener()
		case <-stopAccept:
		}
	}()

	go func() {
		defer close(done)
		defer close(stopAccept)
		for {
			conn, err := listener.Accept()
			if err != nil {
				select {
				case <-ctx.Done():
					logger.Printf("Accept loop for port %d: context canceled during Accept(): %v", port, err)
					return // Exit goroutine
				default:
					if strings.Contains(err.Error(), "use of closed network connection") {
						logger.Printf("Accept loop for port %d: Listener closed normally.", port)
					} else if ne, ok := err.(net.Error); ok && ne.Temporary() {
						logger.Printf("Temporary error accepting on port %d: %v. Retrying.", port, err)
						time.Sleep(100 * time.Millisecond) // Brief pause
						continue
					} else {
						logger.Printf("Persistent error accepting on port %d: %v. Stopping loop.", port, err)
					}
					return // Stop loop for persistent or non-temporary errors
				}
			}
			logger.Printf("Accepted new connection from %s on port %d", conn.RemoteAddr().String(), port)
			if !connections.Track(conn) {
				continue
			}
			go func() {
				defer connections.Done(conn)
				handleConnection(ctx, conn, cred, logger)
			}()
		}
	}()

	return listener, port, done, connections, nil
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
func handleConnection(ctx context.Context, conn net.Conn, cred azcore.TokenCredential, logger *authLog) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	clientAddr := conn.RemoteAddr().String()

	logger.Printf("Handling connection from %s", clientAddr)

	for {
		select {
		case <-ctx.Done():
			logger.Printf("Context canceled for connection %s before reading.", clientAddr)
			return
		default:
		}

		line, err := reader.ReadString('\f')
		if err != nil {
			select {
			case <-ctx.Done():
				logger.Printf("Context canceled while reading from %s: %v", clientAddr, err)
			default:
				if err.Error() == "EOF" || strings.Contains(err.Error(), "connection reset by peer") {
					logger.Printf("Client %s closed connection (EOF or reset).", clientAddr)
				} else if strings.Contains(err.Error(), "use of closed network connection") {
					logger.Printf("Connection %s closed locally while reading.", clientAddr)
				} else {
					logger.Printf("Error reading from %s: %v", clientAddr, err)
				}
			}
			break // Exit loop on any read error or context cancellation
		}
		logger.Printf("Raw data from %s: %s", clientAddr, redactSecrets(line))

		jsonData := line[:len(line)-1] // Trim the delimiter

		var tokenReq TokenRequest
		if err := json.Unmarshal([]byte(jsonData), &tokenReq); err != nil {
			logger.Printf("Error unmarshalling request from %s: %v. JSON: %s", clientAddr, err, redactSecrets(jsonData))
			continue
		}

		logger.Printf("Request from %s - Type: '%s', Scopes: %v", clientAddr, tokenReq.Type, tokenReq.Data.Scopes)

		if tokenReq.Type == "getAccessToken" {
			var scopes []string
			if tokenReq.Data.Scopes == nil || *tokenReq.Data.Scopes == "" {
				scopes = []string{"499b84ac-1321-427f-aa17-267ca6975798/.default"}
				logger.Printf("No scopes from %s, using default: %v", clientAddr, scopes)
			} else {
				scopes = strings.Split(*tokenReq.Data.Scopes, " ")
				logger.Printf("Scopes from %s: %v", clientAddr, scopes)
			}

			token, err := cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: scopes}) // Pass context
			if err != nil {
				logger.Printf("Error getting token for %s (scopes %v): %s", clientAddr, scopes, redactSecrets(err.Error()))
				continue
			}

			logger.Printf("Successfully obtained token for %s (scopes %v)", clientAddr, scopes)

			tokenResp := TokenResponse{
				Type: "accessToken",
				Data: token.Token,
			}

			respBytes, err := json.Marshal(tokenResp)
			if err != nil {
				logger.Printf("Error marshalling response for %s: %s", clientAddr, redactSecrets(err.Error()))
				continue
			}

			_, err = writer.Write(append(respBytes, '\f'))
			if err != nil {
				logger.Printf("Error writing response to %s: %s", clientAddr, redactSecrets(err.Error()))
				break
			}
			err = writer.Flush()
			if err != nil {
				logger.Printf("Error flushing writer for %s: %s", clientAddr, redactSecrets(err.Error()))
				break
			}
			logger.Printf("Sent accessToken response to %s", clientAddr)
		} else {
			logger.Printf("Received unknown message type '%s' from %s", tokenReq.Type, clientAddr)
		}
	}
	logger.Printf("Finished handling connection for %s", clientAddr)
}

// ServerConfig holds configuration for the local auth server
type ServerConfig struct {
	SocketPath  string
	Port        int
	Listener    net.Listener
	logger      *authLog
	acceptDone  <-chan struct{}
	connections *activeConnections
	closeOnce   sync.Once
}

// Close stops the listener and closes the log file.
func (sc *ServerConfig) Close() {
	if sc == nil {
		return
	}
	sc.closeOnce.Do(func() {
		sc.logger.Printf("Closing server resources for port %d...", sc.Port)
		if sc.Listener != nil {
			sc.logger.Printf("Closing listener for port %d.", sc.Port)
			_ = sc.Listener.Close()
		}
		if sc.acceptDone != nil {
			<-sc.acceptDone
		}
		if sc.connections != nil {
			sc.connections.Close()
		}
		sc.logger.Printf("Server resources for port %d closed.", sc.Port)
		sc.logger.Close()
	})
}

// SetupServer initializes the local server and returns its configuration.
// It now takes a context for cancellation.
func SetupServer(ctx context.Context) (*ServerConfig, error) {
	return setupServer(ctx, "")
}

func setupAgentServer(ctx context.Context, agentID string) (*ServerConfig, error) {
	logPath := filepath.Join(getAuthLogDirectory(), "agent-"+sanitizeForFilename(agentID)+"-azure-auth.log")
	return setupServer(ctx, logPath)
}

func setupServer(ctx context.Context, logPath string) (*ServerConfig, error) {
	logger, err := newAuthLog(logPath)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize auth logger: %w", err)
	}

	logger.Printf("Attempting to start auth server...")

	var subscription string

	configPath, pathErr := getConfigFilePath()
	if pathErr != nil {
		logger.Printf("Unable to resolve config path: %s", redactSecrets(pathErr.Error()))
	} else {
		logger.Printf("Looking for config at %s", configPath)
	}

	cfg, err := LoadAppConfig()
	if err != nil {
		logger.Printf("Failed to load config: %s", redactSecrets(err.Error()))
	} else {
		if configPath != "" {
			if _, statErr := os.Stat(configPath); statErr == nil {
				logger.Printf("Loaded configuration from %s", configPath)
			} else if os.IsNotExist(statErr) {
				logger.Printf("Config file not found; using defaults")
			} else {
				logger.Printf("Unable to stat config file %s: %s", configPath, redactSecrets(statErr.Error()))
			}
		}

		login, loginErr := currentGitHubLogin()
		if loginErr != nil {
			logger.Printf("Unable to determine active GitHub login: %s", redactSecrets(loginErr.Error()))
		} else {
			logger.Printf("Active GitHub login: %s", login)
			if sub, ok := cfg.AzureSubscriptionForLogin(login); ok {
				subscription = sub
				logger.Printf("Using Azure subscription override '%s' for login '%s'", subscription, login)
			} else {
				logger.Printf("No Azure subscription override found for login '%s'", login)
			}
		}
	}

	var cred azcore.TokenCredential
	if strings.TrimSpace(subscription) == "" {
		cred, err = azidentity.NewAzureCLICredential(nil)
	} else {
		logger.Printf("Creating Azure CLI credential with subscription override %s", subscription)
		cred, err = azidentity.NewAzureCLICredential(&azidentity.AzureCLICredentialOptions{Subscription: subscription})
	}
	if err != nil {
		logger.Printf("Error creating Azure credential: %s", redactSecrets(err.Error()))
		logger.Close()
		return nil, fmt.Errorf("error creating Azure credential: %w", err)
	}

	listener, port, acceptDone, connections, err := startServer(ctx, cred, logger)
	if err != nil {
		logger.Printf("Error starting server components: %s", redactSecrets(err.Error()))
		logger.Close()
		return nil, fmt.Errorf("error starting server: %w", err)
	}

	socketId := uuid.New()
	socketPath := "/tmp/ado-auth-" + socketId.String() + ".sock"

	logger.Printf("Server successfully started on port %d, socket path %s", port, socketPath)

	return &ServerConfig{
		SocketPath:  socketPath,
		Port:        port,
		Listener:    listener,
		logger:      logger,
		acceptDone:  acceptDone,
		connections: connections,
	}, nil
}
