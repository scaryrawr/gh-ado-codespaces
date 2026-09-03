package main

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

//go:embed terminal-tool-shim.sh
var terminalToolShimScript string

const (
	terminalToolContentType = "application/vnd.gh-ado.launch.v1"
	terminalToolBodyLimit   = 1 << 20
	terminalToolArgLimit    = 256
	terminalToolArgSize     = 64 << 10
	terminalToolErrorLimit  = 8 << 10
	terminalToolLaunchLimit = 4
	terminalToolTimeout     = 610 * time.Second
	terminalToolShimMarker  = "# gh-ado-codespaces terminal shim"
)

type terminalToolID uint8

const (
	terminalBrowserTool terminalToolID = iota + 1
	terminalCodeTool
)

type terminalToolSpec struct {
	id         terminalToolID
	remoteName string
	localName  string
	plan       func(remoteInvocation, sshRoute) ([]string, error)
}

var terminalToolCatalog = [...]terminalToolSpec{
	{
		id:         terminalBrowserTool,
		remoteName: "terminal-browser",
		localName:  "terminal-browser",
		plan:       planTerminalBrowser,
	},
	{
		id:         terminalCodeTool,
		remoteName: "tode",
		localName:  "tode",
		plan:       planTerminalCode,
	},
}

type detectedTerminalTool struct {
	spec       terminalToolSpec
	executable string
}

type remoteInvocation struct {
	cwd  string
	argv []string
}

type launchPlan struct {
	executable string
	argv       []string
}

type sshRoute struct {
	alias      string
	configPath string
}

type terminalToolService struct {
	tools      map[terminalToolID]detectedTerminalTool
	route      sshRoute
	Port       int
	SocketPath string
	server     *http.Server
	listener   net.Listener
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	slots      chan struct{}
}

func detectTerminalTools(lookPath func(string) (string, error)) map[terminalToolID]detectedTerminalTool {
	tools := make(map[terminalToolID]detectedTerminalTool)
	for _, spec := range terminalToolCatalog {
		executable, err := lookPath(spec.localName)
		if err != nil {
			continue
		}
		executable, err = filepath.Abs(executable)
		if err != nil {
			continue
		}
		tools[spec.id] = detectedTerminalTool{spec: spec, executable: executable}
	}

	return tools
}

func terminalToolRemoteNames(tools map[terminalToolID]detectedTerminalTool) []string {
	var names []string
	for _, spec := range terminalToolCatalog {
		if _, ok := tools[spec.id]; ok {
			names = append(names, spec.remoteName)
		}
	}

	return names
}

func startTerminalToolService(ctx context.Context, args *CommandLineArgs) (*terminalToolService, error) {
	tools := detectTerminalTools(exec.LookPath)
	if len(tools) == 0 {
		return nil, nil
	}
	if args.Profile != "" || args.ServerPort != 0 {
		return nil, fmt.Errorf("local terminal tools are unavailable with --profile or --server-port")
	}

	route, cleanupRoute, err := createTerminalToolRoute(ctx, args)
	if err != nil {
		return nil, err
	}

	service, err := newTerminalToolService(ctx, tools, route)
	if err != nil {
		cleanupRoute()
		return nil, err
	}
	return service, nil
}

func createTerminalToolRoute(ctx context.Context, args *CommandLineArgs) (sshRoute, func(), error) {
	generatedConfig, err := generateCodespaceSSHConfig(ctx, args)
	if err != nil {
		return sshRoute{}, nil, err
	}
	route, config, err := buildTerminalToolConfig(generatedConfig, args.CodespaceName, sessionID)
	if err != nil {
		return sshRoute{}, nil, err
	}

	file, err := os.CreateTemp("", "gh-ado-terminal-*.conf")
	if err != nil {
		return sshRoute{}, nil, fmt.Errorf("failed to create terminal tool SSH config: %w", err)
	}
	cleanup := func() { _ = os.Remove(file.Name()) }
	if strings.IndexFunc(file.Name(), unicode.IsSpace) >= 0 {
		_ = file.Close()
		cleanup()
		return sshRoute{}, nil, fmt.Errorf("terminal tool SSH config path contains whitespace")
	}
	if _, err := file.WriteString(config); err != nil {
		_ = file.Close()
		cleanup()
		return sshRoute{}, nil, fmt.Errorf("failed to write terminal tool SSH config: %w", err)
	}
	if err := file.Chmod(0600); err != nil {
		_ = file.Close()
		cleanup()
		return sshRoute{}, nil, fmt.Errorf("failed to secure terminal tool SSH config: %w", err)
	}
	if err := file.Close(); err != nil {
		cleanup()
		return sshRoute{}, nil, fmt.Errorf("failed to close terminal tool SSH config: %w", err)
	}

	route.configPath = file.Name()
	return route, cleanup, nil
}

func buildTerminalToolSSHHostAlias(codespaceName, currentSessionID string) string {
	return buildHerdrSSHHostAlias(codespaceName, currentSessionID) + "-tool"
}

func buildTerminalToolConfig(generatedConfig, codespaceName, currentSessionID string) (sshRoute, string, error) {
	if err := validateSSHHostAlias(codespaceName); err != nil {
		return sshRoute{}, "", err
	}
	if strings.Contains(strings.ToLower(generatedConfig), "remoteforward") {
		return sshRoute{}, "", fmt.Errorf("GitHub CLI SSH config unexpectedly contains a RemoteForward")
	}

	alias := buildTerminalToolSSHHostAlias(codespaceName, currentSessionID)
	config, err := replaceGeneratedSSHHostAlias(generatedConfig, alias)
	if err != nil {
		return sshRoute{}, "", err
	}

	return sshRoute{alias: alias}, config, nil
}

func newTerminalToolService(ctx context.Context, tools map[terminalToolID]detectedTerminalTool, route sshRoute) (*terminalToolService, error) {
	listener, err := net.Listen("tcp", localServiceHost+":0")
	if err != nil {
		return nil, fmt.Errorf("failed to create terminal tool listener: %w", err)
	}

	serviceCtx, cancel := context.WithCancel(ctx)
	service := &terminalToolService{
		tools:      tools,
		route:      route,
		Port:       listener.Addr().(*net.TCPAddr).Port,
		SocketPath: "/tmp/gh-ado-terminal-" + uuid.NewString() + ".sock",
		listener:   listener,
		cancel:     cancel,
		slots:      make(chan struct{}, terminalToolLaunchLimit),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/launch/terminal-browser", service.handleLaunch)
	mux.HandleFunc("/v1/launch/tode", service.handleLaunch)
	mux.HandleFunc("/v1/available/terminal-browser", service.handleAvailability)
	mux.HandleFunc("/v1/available/tode", service.handleAvailability)
	service.server = &http.Server{Handler: mux, BaseContext: func(net.Listener) context.Context {
		return serviceCtx
	}}
	service.wg.Add(1)
	go service.serve()

	return service, nil
}

func (service *terminalToolService) serve() {
	defer service.wg.Done()
	defer service.listener.Close()

	if err := service.server.Serve(service.listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logDebug("Terminal tool HTTP service error: %v", err)
	}
}

func (service *terminalToolService) RemoteForward() remoteForward {
	return remoteForward{
		remote: service.SocketPath,
		local:  fmt.Sprintf("%s:%d", localServiceHost, service.Port),
	}
}

func (service *terminalToolService) Stop() {
	if service.cancel == nil {
		return
	}
	service.cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	_ = service.server.Shutdown(shutdownCtx)
	service.wg.Wait()
	cleanupSocketFile(service.SocketPath)
	if service.route.configPath != "" {
		if err := os.Remove(service.route.configPath); err != nil && !os.IsNotExist(err) {
			logDebug("Failed to remove terminal tool SSH config %s: %v", service.route.configPath, err)
		}
	}
}

func (service *terminalToolService) handleLaunch(w http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if request.Header.Get("Content-Type") != terminalToolContentType {
		http.Error(w, "invalid content type", http.StatusUnsupportedMediaType)
		return
	}

	tool, ok := service.toolForPath(request.URL.Path)
	if !ok {
		http.Error(w, "terminal tool unavailable", http.StatusServiceUnavailable)
		return
	}
	invocation, err := parseRemoteInvocation(http.MaxBytesReader(w, request.Body, terminalToolBodyLimit))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	argv, err := tool.spec.plan(invocation, service.route)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	plan := launchPlan{executable: tool.executable, argv: argv}

	select {
	case service.slots <- struct{}{}:
		defer func() { <-service.slots }()
	default:
		http.Error(w, "terminal tool launcher is busy", http.StatusServiceUnavailable)
		return
	}

	if err := service.run(request.Context(), plan); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (service *terminalToolService) handleAvailability(w http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if _, ok := service.toolForPath(strings.Replace(request.URL.Path, "/v1/available/", "/v1/launch/", 1)); !ok {
		http.Error(w, "terminal tool unavailable", http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (service *terminalToolService) toolForPath(requestPath string) (detectedTerminalTool, bool) {
	for _, spec := range terminalToolCatalog {
		if requestPath != "/v1/launch/"+spec.remoteName {
			continue
		}
		tool, ok := service.tools[spec.id]
		return tool, ok
	}

	return detectedTerminalTool{}, false
}

func (service *terminalToolService) run(ctx context.Context, plan launchPlan) error {
	ctx, cancel := context.WithTimeout(ctx, terminalToolTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, plan.executable, plan.argv...)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.WaitDelay = 5 * time.Second
	var stderr limitedBuffer
	stderr.limit = terminalToolErrorLimit
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("terminal tool launch timed out")
		}
		logDebug("Terminal tool launch failed for %s: %v", plan.executable, err)
		message := strings.TrimSpace(stderr.String())
		if message != "" {
			logDebug("Terminal tool stderr: %s", message)
		}

		return fmt.Errorf("terminal tool launch failed")
	}

	return nil
}

type limitedBuffer struct {
	bytes.Buffer
	limit int
}

func (buffer *limitedBuffer) Write(data []byte) (int, error) {
	remaining := buffer.limit - buffer.Len()
	if remaining > 0 {
		if len(data) > remaining {
			_, _ = buffer.Buffer.Write(data[:remaining])
		} else {
			_, _ = buffer.Buffer.Write(data)
		}
	}

	return len(data), nil
}

func parseRemoteInvocation(reader io.Reader) (remoteInvocation, error) {
	body, err := io.ReadAll(reader)
	if err != nil {
		return remoteInvocation{}, fmt.Errorf("invalid request body")
	}
	if len(body) == 0 || body[len(body)-1] != 0 {
		return remoteInvocation{}, fmt.Errorf("request body must end with NUL")
	}

	fields := bytes.Split(body[:len(body)-1], []byte{0})
	if len(fields) == 0 || !utf8.Valid(fields[0]) {
		return remoteInvocation{}, fmt.Errorf("invalid remote working directory")
	}
	cwd := string(fields[0])
	if !isCleanAbsolutePOSIXPath(cwd) {
		return remoteInvocation{}, fmt.Errorf("remote working directory must be an absolute clean POSIX path")
	}
	if len(fields)-1 > terminalToolArgLimit {
		return remoteInvocation{}, fmt.Errorf("too many arguments")
	}

	argv := make([]string, len(fields)-1)
	for index, field := range fields[1:] {
		if len(field) > terminalToolArgSize || !utf8.Valid(field) {
			return remoteInvocation{}, fmt.Errorf("invalid argument")
		}
		argv[index] = string(field)
	}

	return remoteInvocation{cwd: cwd, argv: argv}, nil
}

func isCleanAbsolutePOSIXPath(value string) bool {
	return utf8.ValidString(value) && path.IsAbs(value) && path.Clean(value) == value && !strings.Contains(value, "\\")
}

type panePlacement struct {
	direction string
	size      string
	hasSize   bool
	explicit  bool
}

func parsePanePlacement(argv []string) (panePlacement, []string, error) {
	var placement panePlacement
	var remaining []string

	for index := 0; index < len(argv); index++ {
		switch argv[index] {
		case "--split":
			if placement.explicit || index+1 == len(argv) {
				return panePlacement{}, nil, fmt.Errorf("--split requires one direction")
			}
			index++
			if !validSplitDirection(argv[index]) {
				return panePlacement{}, nil, fmt.Errorf("unsupported split direction")
			}
			placement.direction = argv[index]
			placement.explicit = true
		case "--size":
			if placement.hasSize || index+1 == len(argv) {
				return panePlacement{}, nil, fmt.Errorf("--size requires one value")
			}
			index++
			if !validSplitSize(argv[index]) {
				return panePlacement{}, nil, fmt.Errorf("--size must be between 0.2 and 0.95")
			}
			placement.size = argv[index]
			placement.hasSize = true
		default:
			remaining = append(remaining, argv[index])
		}
	}
	if placement.hasSize && !placement.explicit {
		return panePlacement{}, nil, fmt.Errorf("--size requires --split")
	}
	if !placement.explicit {
		placement.direction = "right"
	}

	return placement, remaining, nil
}

func validSplitDirection(value string) bool {
	switch value {
	case "right", "left", "up", "down":
		return true
	default:
		return false
	}
}

func validSplitSize(value string) bool {
	size, err := strconv.ParseFloat(value, 64)
	return err == nil && size >= 0.2 && size <= 0.95
}

func planTerminalBrowser(invocation remoteInvocation, route sshRoute) ([]string, error) {
	placement, args, err := parsePanePlacement(invocation.argv)
	if err != nil {
		return nil, err
	}
	if len(args) > 0 && args[0] == "open" {
		args = args[1:]
	}
	if len(args) > 1 {
		return nil, fmt.Errorf("terminal-browser accepts at most one network target")
	}
	for index, arg := range args {
		if strings.HasPrefix(arg, "-") {
			return nil, fmt.Errorf("unsupported terminal-browser argument %q", arg)
		}
		args[index], err = normalizeNetworkTarget(arg)
		if err != nil {
			return nil, err
		}
	}

	plan := []string{"open", "--ssh", buildSSHTarget(route)}
	plan = append(plan, args...)
	plan = append(plan, "--split", placement.direction)
	if placement.hasSize {
		plan = append(plan, "--size", placement.size)
	}

	return plan, nil
}

func normalizeNetworkTarget(value string) (string, error) {
	if value == "" || strings.ContainsAny(value, "\\\n\r\t ") || strings.HasPrefix(value, "/") || strings.HasPrefix(value, ".") {
		return "", fmt.Errorf("unsupported terminal-browser target %q", value)
	}
	if strings.Contains(value, "://") {
		parsed, err := url.ParseRequestURI(value)
		if err != nil || parsed.Host == "" || parsed.Scheme != "http" && parsed.Scheme != "https" {
			return "", fmt.Errorf("unsupported terminal-browser target %q", value)
		}

		return parsed.String(), nil
	}
	if strings.HasPrefix(value, "file:") {
		return "", fmt.Errorf("unsupported terminal-browser target %q", value)
	}

	host := strings.SplitN(value, "/", 2)[0]
	if _, err := strconv.Atoi(value); err == nil {
		value = "localhost:" + value
		host = value
	}
	if host != "localhost" && !strings.ContainsAny(host, ".:") {
		return "", fmt.Errorf("unsupported terminal-browser target %q", value)
	}
	scheme := "https"
	if host == "localhost" || strings.Contains(host, ":") {
		scheme = "http"
	}
	parsed, err := url.ParseRequestURI(scheme + "://" + value)
	if err != nil || parsed.Host == "" {
		return "", fmt.Errorf("unsupported terminal-browser target %q", value)
	}

	return parsed.String(), nil
}

func planTerminalCode(invocation remoteInvocation, route sshRoute) ([]string, error) {
	placement, args, err := parsePanePlacement(invocation.argv)
	if err != nil {
		return nil, err
	}
	if len(args) > 1 {
		return nil, fmt.Errorf("tode accepts at most one remote path")
	}
	if len(args) == 1 && strings.HasPrefix(args[0], "-") {
		return nil, fmt.Errorf("unsupported tode argument %q", args[0])
	}

	remotePath := invocation.cwd
	if len(args) == 1 {
		if path.IsAbs(args[0]) {
			remotePath = path.Clean(args[0])
		} else {
			remotePath = path.Join(invocation.cwd, args[0])
		}
	}
	plan := []string{"--ssh", buildSSHTarget(route), remotePath, "--split", placement.direction}
	if placement.hasSize {
		plan = append(plan, "--size", placement.size)
	}

	return plan, nil
}

func buildSSHTarget(route sshRoute) string {
	return "ssh -F " + route.configPath + " " + route.alias
}
