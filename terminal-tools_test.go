package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParseRemoteInvocation(t *testing.T) {
	tests := []struct {
		name    string
		body    []byte
		wantErr bool
		want    remoteInvocation
	}{
		{
			name: "preserves empty and shell arguments",
			body: []byte("/work/app\x00\x00$HOME && echo nope\x00"),
			want: remoteInvocation{cwd: "/work/app", argv: []string{"", "$HOME && echo nope"}},
		},
		{name: "requires final NUL", body: []byte("/work/app\x00arg"), wantErr: true},
		{name: "requires physical absolute cwd", body: []byte("work/app\x00"), wantErr: true},
		{name: "rejects non UTF-8", body: []byte("/work/app\x00\xff\x00"), wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseRemoteInvocation(bytes.NewReader(test.body))
			if (err != nil) != test.wantErr {
				t.Fatalf("parseRemoteInvocation() error = %v, wantErr %t", err, test.wantErr)
			}
			if !test.wantErr && (got.cwd != test.want.cwd || !equalStrings(got.argv, test.want.argv)) {
				t.Fatalf("parseRemoteInvocation() = %#v, want %#v", got, test.want)
			}
		})
	}

	tooMany := append([]byte("/work/app\x00"), bytes.Repeat([]byte("x\x00"), terminalToolArgLimit+1)...)
	if _, err := parseRemoteInvocation(bytes.NewReader(tooMany)); err == nil {
		t.Fatal("parseRemoteInvocation() accepted too many arguments")
	}
	tooLarge := append([]byte("/work/app\x00"), bytes.Repeat([]byte("x"), terminalToolArgSize+1)...)
	tooLarge = append(tooLarge, 0)
	if _, err := parseRemoteInvocation(bytes.NewReader(tooLarge)); err == nil {
		t.Fatal("parseRemoteInvocation() accepted an oversized argument")
	}
}

func TestDetectTerminalTools(t *testing.T) {
	tools := detectTerminalTools(func(name string) (string, error) {
		if name == "terminal-browser" {
			return "bin/terminal-browser", nil
		}
		return "", os.ErrNotExist
	})
	if got := terminalToolRemoteNames(tools); !equalStrings(got, []string{"terminal-browser"}) {
		t.Fatalf("terminalToolRemoteNames() = %q", got)
	}
	if !filepath.IsAbs(tools[terminalBrowserTool].executable) {
		t.Fatalf("detected executable is not absolute: %q", tools[terminalBrowserTool].executable)
	}
}

func TestTerminalToolPlans(t *testing.T) {
	route := sshRoute{alias: "space--tool", configPath: "/tmp/terminal.conf"}
	tests := []struct {
		name    string
		plan    func(remoteInvocation, sshRoute) ([]string, error)
		request remoteInvocation
		want    []string
		wantErr bool
	}{
		{
			name:    "terminal-browser defaults to a right split",
			plan:    planTerminalBrowser,
			request: remoteInvocation{cwd: "/work", argv: []string{"open", "localhost:3000"}},
			want:    []string{"open", "--ssh", "ssh -F /tmp/terminal.conf space--tool", "http://localhost:3000", "--split", "right"},
		},
		{
			name:    "terminal-browser preserves explicit split and size",
			plan:    planTerminalBrowser,
			request: remoteInvocation{cwd: "/work", argv: []string{"https://example.com", "--split", "down", "--size", "0.4"}},
			want:    []string{"open", "--ssh", "ssh -F /tmp/terminal.conf space--tool", "https://example.com", "--split", "down", "--size", "0.4"},
		},
		{
			name:    "terminal-browser accepts a host target with a path",
			plan:    planTerminalBrowser,
			request: remoteInvocation{cwd: "/work", argv: []string{"github.com/zenbu-labs"}},
			want:    []string{"open", "--ssh", "ssh -F /tmp/terminal.conf space--tool", "https://github.com/zenbu-labs", "--split", "right"},
		},
		{
			name:    "terminal-browser canonicalizes a bare port",
			plan:    planTerminalBrowser,
			request: remoteInvocation{cwd: "/work", argv: []string{"3000"}},
			want:    []string{"open", "--ssh", "ssh -F /tmp/terminal.conf space--tool", "http://localhost:3000", "--split", "right"},
		},
		{
			name:    "terminal-browser rejects caller SSH",
			plan:    planTerminalBrowser,
			request: remoteInvocation{cwd: "/work", argv: []string{"--ssh", "other"}},
			wantErr: true,
		},
		{
			name:    "terminal-browser rejects local files",
			plan:    planTerminalBrowser,
			request: remoteInvocation{cwd: "/work", argv: []string{"file:///tmp/local.html"}},
			wantErr: true,
		},
		{
			name:    "tode resolves a relative remote path",
			plan:    planTerminalCode,
			request: remoteInvocation{cwd: "/work/app", argv: []string{"src/../main.go", "--split", "left"}},
			want:    []string{"--ssh", "ssh -F /tmp/terminal.conf space--tool", "/work/app/main.go", "--split", "left"},
		},
		{
			name:    "tode uses remote cwd without a path",
			plan:    planTerminalCode,
			request: remoteInvocation{cwd: "/work/app", argv: nil},
			want:    []string{"--ssh", "ssh -F /tmp/terminal.conf space--tool", "/work/app", "--split", "right"},
		},
		{
			name:    "tode rejects wait",
			plan:    planTerminalCode,
			request: remoteInvocation{cwd: "/work/app", argv: []string{"--wait"}},
			wantErr: true,
		},
		{
			name:    "size requires split",
			plan:    planTerminalCode,
			request: remoteInvocation{cwd: "/work/app", argv: []string{"main.go", "--size", "0.4"}},
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := test.plan(test.request, route)
			if (err != nil) != test.wantErr {
				t.Fatalf("plan() error = %v, wantErr %t", err, test.wantErr)
			}
			if !test.wantErr && !equalStrings(got, test.want) {
				t.Fatalf("plan() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestBuildTerminalToolConfigUsesForwardsFreeRoute(t *testing.T) {
	route, config, err := buildTerminalToolConfig("Host codespace\n  HostName ssh.github.com\n  User codespace\n", "codespace", "session")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(config), "remoteforward") {
		t.Fatalf("tool config contains a RemoteForward:\n%s", config)
	}
	if strings.Contains(config, "Host codespace\n") {
		t.Fatalf("tool config did not replace the generated alias:\n%s", config)
	}
	if got, want := buildSSHTarget(sshRoute{alias: route.alias, configPath: "/tmp/tool.conf"}), "ssh -F /tmp/tool.conf "+route.alias; got != want {
		t.Fatalf("buildSSHTarget() = %q, want %q", got, want)
	}

	_, _, err = buildTerminalToolConfig("Host codespace\n  RemoteForward /tmp/socket 127.0.0.1:1\n", "codespace", "session")
	if err == nil {
		t.Fatal("buildTerminalToolConfig() accepted a RemoteForward")
	}
}

func TestBuildCodespacePreparationScriptPublishesDetectedTerminalTools(t *testing.T) {
	tools := map[terminalToolID]detectedTerminalTool{
		terminalBrowserTool: {spec: terminalToolCatalog[0], executable: "/opt/bin/terminal-browser"},
	}
	script := buildCodespacePreparationScript(false, false, tools)

	if !strings.Contains(script, `terminal-tool-shim.sh`) {
		t.Fatal("setup script does not stage the terminal shim")
	}
	if !strings.Contains(script, `"/usr/local/bin/terminal-browser"`) {
		t.Fatal("setup script does not publish terminal-browser")
	}
	if strings.Contains(script, `"/usr/local/bin/tode"`) {
		t.Fatal("setup script publishes an undetected tode")
	}
	if !strings.Contains(script, `is not managed by gh-ado-codespaces; not replacing it`) {
		t.Fatal("setup script can overwrite unrelated terminal binaries")
	}
	if !strings.Contains(terminalToolShimScript, terminalToolShimMarker) {
		t.Fatal("terminal shim is missing its ownership marker")
	}
	if !strings.Contains(script, `[ -L "$destination" ]`) {
		t.Fatal("setup script can overwrite a broken unrelated symlink")
	}
	command := exec.Command("bash", "-n")
	command.Stdin = strings.NewReader(script)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generated setup script has invalid syntax: %v\n%s", err, output)
	}
}

func TestTerminalShimExecutableProbe(t *testing.T) {
	remoteDir := t.TempDir()
	binDir := t.TempDir()
	output := filepath.Join(t.TempDir(), "argv")
	stdinOutput := output + ".stdin"
	t.Setenv("OUTPUT", output)
	t.Setenv("STDIN_OUTPUT", stdinOutput)

	browserExecutable := writeFakeTerminalTool(t, binDir, "local-terminal-browser")
	todeExecutable := writeFakeTerminalTool(t, binDir, "local-tode")
	writeShim(t, binDir, "terminal-browser")
	writeShim(t, binDir, "tode")

	route := sshRoute{alias: "codespace--tool", configPath: "/tmp/gh-ado-terminal-route.conf"}
	runServiceShim := func(tools map[terminalToolID]detectedTerminalTool, name string, args ...string) (string, error) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		service, err := newTerminalToolService(ctx, tools, route)
		if err != nil {
			t.Fatal(err)
		}
		defer service.Stop()
		stopBridge := startTerminalSocketBridge(t, service.SocketPath, service.Port)
		defer stopBridge()

		probe := exec.Command("curl", "--silent", "--unix-socket", service.SocketPath, "--output", "/dev/null", "--write-out", "%{http_code}", "http://localhost/v1/launch/"+name)
		probeOutput, probeErr := probe.CombinedOutput()
		if probeErr != nil || string(probeOutput) != "405" {
			t.Fatalf("socket bridge probe = %q, %v", probeOutput, probeErr)
		}
		payload := []byte(filepath.ToSlash(remoteDir) + "\x00localhost:3000\x00")
		launch := exec.Command("curl", "--silent", "--unix-socket", service.SocketPath, "--header", "Content-Type: "+terminalToolContentType, "--request", "POST", "--data-binary", "@-", "--output", "/dev/null", "--write-out", "%{http_code}", "http://localhost/v1/launch/"+name)
		launch.Stdin = bytes.NewReader(payload)
		launchOutput, launchErr := launch.CombinedOutput()
		if name == "terminal-browser" && (launchErr != nil || string(launchOutput) != "204") {
			t.Fatalf("socket bridge launch = %q, %v", launchOutput, launchErr)
		}
		_ = os.Remove(output)
		_ = os.Remove(stdinOutput)

		cmd := exec.Command(filepath.Join(binDir, name), args...)
		cmd.Dir = remoteDir
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		err = cmd.Run()
		return stderr.String(), err
	}

	browserTools := map[terminalToolID]detectedTerminalTool{
		terminalBrowserTool: {spec: terminalToolCatalog[0], executable: browserExecutable},
	}
	if stderr, err := runServiceShim(browserTools, "terminal-browser", "localhost:3000"); err != nil {
		t.Fatalf("default terminal-browser shim failed: %v\n%s", err, stderr)
	}
	assertArgvFile(t, output, []string{
		"open", "--ssh", "ssh -F /tmp/gh-ado-terminal-route.conf codespace--tool",
		"http://localhost:3000", "--split", "right",
	})
	if _, err := os.Stat(stdinOutput); !os.IsNotExist(err) {
		t.Fatalf("terminal-browser received interactive stdin: %v", err)
	}

	if stderr, err := runServiceShim(browserTools, "terminal-browser", "open", "localhost:3000", "--split", "down", "--size", "0.4"); err != nil {
		t.Fatalf("explicit terminal-browser shim failed: %v\n%s", err, stderr)
	}
	assertArgvFile(t, output, []string{
		"open", "--ssh", "ssh -F /tmp/gh-ado-terminal-route.conf codespace--tool",
		"http://localhost:3000", "--split", "down", "--size", "0.4",
	})

	if stderr, err := runServiceShim(browserTools, "tode"); err == nil || !strings.Contains(stderr, "no active gh-ado-codespaces session") {
		t.Fatalf("missing tode shim result = %v, stderr = %q", err, stderr)
	}

	todeTools := map[terminalToolID]detectedTerminalTool{
		terminalCodeTool: {spec: terminalToolCatalog[1], executable: todeExecutable},
	}
	if stderr, err := runServiceShim(todeTools, "tode", "src/main.go", "--split", "left"); err != nil {
		t.Fatalf("tode shim failed: %v\n%s", err, stderr)
	}
	physicalRemoteDir, err := filepath.EvalSymlinks(remoteDir)
	if err != nil {
		t.Fatal(err)
	}
	assertArgvFile(t, output, []string{
		"--ssh", "ssh -F /tmp/gh-ado-terminal-route.conf codespace--tool",
		filepath.ToSlash(filepath.Join(physicalRemoteDir, "src/main.go")), "--split", "left",
	})
}

func writeFakeTerminalTool(t *testing.T, directory, name string) string {
	t.Helper()
	path := filepath.Join(directory, name)
	script := "#!/bin/sh\nprintf '%s\\000' \"$@\" > \"$OUTPUT\"\nif IFS= read -r ignored; then printf received > \"$STDIN_OUTPUT\"; fi\n"
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeShim(t *testing.T, directory, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(directory, name), []byte(terminalToolShimScript), 0700); err != nil {
		t.Fatal(err)
	}
}

func assertArgvFile(t *testing.T, filename string, want []string) {
	t.Helper()
	got, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	gotArgs := bytes.Split(got[:len(got)-1], []byte{0})
	actual := make([]string, len(gotArgs))
	for index, arg := range gotArgs {
		actual[index] = string(arg)
	}
	if !equalStrings(actual, want) {
		t.Fatalf("fake tool argv = %#v, want %#v", actual, want)
	}
}

func startTerminalSocketBridge(t *testing.T, socket string, port int) func() {
	t.Helper()
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	done := make(chan struct{})
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				select {
				case <-done:
					return
				default:
					t.Errorf("socket bridge accept: %v", err)
					return
				}
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer connection.Close()
				upstream, err := net.Dial("tcp", net.JoinHostPort(localServiceHost, fmt.Sprint(port)))
				if err != nil {
					t.Errorf("socket bridge dial: %v", err)
					return
				}
				defer upstream.Close()
				go func() {
					_, _ = io.Copy(upstream, connection)
					_ = upstream.(*net.TCPConn).CloseWrite()
				}()
				_, _ = io.Copy(connection, upstream)
			}()
		}
	}()

	return func() {
		close(done)
		_ = listener.Close()
		wg.Wait()
		_ = os.Remove(socket)
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

func TestTerminalToolServiceRejectsInvalidRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service, err := newTerminalToolService(ctx, nil, sshRoute{})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Stop()

	request, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://%s:%d/v1/launch/tode", localServiceHost, service.Port), nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := (&http.Client{Timeout: time.Second}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET status = %d, want %d", response.StatusCode, http.StatusMethodNotAllowed)
	}
}

func TestTerminalShimRejectsAmbiguousSessions(t *testing.T) {
	binDir := t.TempDir()
	writeShim(t, binDir, "terminal-browser")
	tools := map[terminalToolID]detectedTerminalTool{
		terminalBrowserTool: {spec: terminalToolCatalog[0], executable: "/unused"},
	}

	var services []*terminalToolService
	var stopBridges []func()
	for range 2 {
		service, err := newTerminalToolService(context.Background(), tools, sshRoute{})
		if err != nil {
			t.Fatal(err)
		}
		services = append(services, service)
		stopBridges = append(stopBridges, startTerminalSocketBridge(t, service.SocketPath, service.Port))
	}
	defer func() {
		for _, stopBridge := range stopBridges {
			stopBridge()
		}
		for _, service := range services {
			service.Stop()
		}
	}()

	command := exec.Command(filepath.Join(binDir, "terminal-browser"), "localhost:3000")
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatal("terminal shim accepted ambiguous active sessions")
	}
	if !strings.Contains(string(output), "multiple active gh-ado-codespaces sessions") {
		t.Fatalf("terminal shim output = %q", output)
	}
}

func TestTerminalShimIgnoresSocketSymlinks(t *testing.T) {
	binDir := t.TempDir()
	output := filepath.Join(t.TempDir(), "argv")
	t.Setenv("OUTPUT", output)
	t.Setenv("STDIN_OUTPUT", output+".stdin")
	writeShim(t, binDir, "terminal-browser")
	tools := map[terminalToolID]detectedTerminalTool{
		terminalBrowserTool: {
			spec:       terminalToolCatalog[0],
			executable: writeFakeTerminalTool(t, binDir, "local-terminal-browser"),
		},
	}
	service, err := newTerminalToolService(context.Background(), tools, sshRoute{
		alias:      "codespace--tool",
		configPath: "/tmp/tool.conf",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Stop()
	stopBridge := startTerminalSocketBridge(t, service.SocketPath, service.Port)
	defer stopBridge()

	spoof := filepath.Join(os.TempDir(), "gh-ado-terminal-"+fmt.Sprint(time.Now().UnixNano())+".sock")
	if err := os.Symlink(service.SocketPath, spoof); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(spoof)

	command := exec.Command(filepath.Join(binDir, "terminal-browser"), "localhost:3000")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("terminal shim followed a socket symlink: %v\n%s", err, output)
	}
}

func TestTerminalToolServiceRedactsLocalLaunchErrors(t *testing.T) {
	privatePath := filepath.Join(t.TempDir(), "private-terminal-browser")
	tools := map[terminalToolID]detectedTerminalTool{
		terminalBrowserTool: {spec: terminalToolCatalog[0], executable: privatePath},
	}
	service, err := newTerminalToolService(context.Background(), tools, sshRoute{
		alias:      "codespace--tool",
		configPath: "/private/tool.conf",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Stop()

	body := bytes.NewReader([]byte("/work\x00localhost:3000\x00"))
	request, err := http.NewRequest(
		http.MethodPost,
		fmt.Sprintf("http://%s:%d/v1/launch/terminal-browser", localServiceHost, service.Port),
		body,
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", terminalToolContentType)
	response, err := (&http.Client{Timeout: time.Second}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusBadGateway {
		t.Fatalf("launch status = %d, want %d", response.StatusCode, http.StatusBadGateway)
	}
	if strings.Contains(string(responseBody), privatePath) || strings.Contains(string(responseBody), "/private/tool.conf") {
		t.Fatalf("launch response leaked a local path: %q", responseBody)
	}
}
