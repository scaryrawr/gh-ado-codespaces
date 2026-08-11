package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildHerdrArgs(t *testing.T) {
	got := buildHerdrArgs("test-codespace")
	want := []string{"--remote", "test-codespace"}

	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("buildHerdrArgs() = %v, want %v", got, want)
	}
}

func TestBuildCodespaceSSHConfigArgs(t *testing.T) {
	args := &CommandLineArgs{
		CodespaceName: "test-codespace",
		Debug:         true,
		DebugFile:     "/tmp/gh-debug.log",
		Repo:          "owner/repo",
		RepoOwner:     "owner",
	}

	got := buildCodespaceSSHConfigArgs(args)
	want := []string{
		"codespace", "ssh",
		"--codespace", "test-codespace",
		"--config",
		"--debug",
		"--debug-file", "/tmp/gh-debug.log",
	}

	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("buildCodespaceSSHConfigArgs() = %v, want %v", got, want)
	}
}

func TestFindHerdrExecutableMissing(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	_, err := findHerdrExecutable()
	if err == nil {
		t.Fatal("findHerdrExecutable() expected an error")
	}
	if !strings.Contains(err.Error(), "https://herdr.dev") {
		t.Fatalf("findHerdrExecutable() error = %q, want installation URL", err)
	}
}

func TestValidateSSHHostAlias(t *testing.T) {
	tests := []struct {
		alias   string
		wantErr bool
	}{
		{alias: "friendly-space-123"},
		{alias: "codespace.example_test"},
		{alias: "", wantErr: true},
		{alias: "bad host", wantErr: true},
		{alias: "bad\nHost injected", wantErr: true},
		{alias: "*", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.alias, func(t *testing.T) {
			err := validateSSHHostAlias(tt.alias)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateSSHHostAlias(%q) error = %v, wantErr %t", tt.alias, err, tt.wantErr)
			}
		})
	}
}

func TestBuildHerdrSSHHostAliasIsSessionSpecific(t *testing.T) {
	longCodespaceName := strings.Repeat("a", 60)
	first := buildHerdrSSHHostAlias(longCodespaceName, longCodespaceName+"_session-one")
	second := buildHerdrSSHHostAlias(longCodespaceName, longCodespaceName+"_session-two")

	if first == second {
		t.Fatalf("session aliases must be unique, got %q", first)
	}
	if !strings.HasPrefix(first, strings.Repeat("a", 50)+"--gh-ado-") {
		t.Fatalf("unexpected session alias %q", first)
	}
}

func TestHostConfigFileNameAvoidsTruncationCollisions(t *testing.T) {
	commonPrefix := strings.Repeat("a", 50)
	first := hostConfigFileName(commonPrefix + "-first-codespace")
	second := hostConfigFileName(commonPrefix + "-second-codespace")

	if first == second {
		t.Fatalf("host config filenames must be unique, got %q", first)
	}
	if !strings.HasPrefix(first, commonPrefix+"-") {
		t.Fatalf("unexpected host config filename %q", first)
	}
	if !strings.HasSuffix(first, ".conf") {
		t.Fatalf("host config filename must use .conf extension, got %q", first)
	}
}

func TestBuildHerdrSessionConfig(t *testing.T) {
	originalPorts := WellKnownPorts
	WellKnownPorts = []ReversePortForward{{
		Port:          1234,
		Description:   "Test service",
		Enabled:       true,
		AlwaysForward: true,
	}}
	defer func() { WellKnownPorts = originalPorts }()

	t.Setenv("DISPLAY", ":0")
	serverConfig := &ServerConfig{SocketPath: "/tmp/auth.sock", Port: 8080}
	browserService := &BrowserService{SocketPath: "/tmp/browser.sock", Port: 8081}
	notificationService := &NotificationService{SocketPath: "/tmp/notification.sock", Port: 8082}

	got, err := buildHerdrSessionConfig(
		"Host cs.test-codespace.main\n  HostName example.com\n  User codespace\n",
		"test-codespace--gh-ado-session",
		serverConfig,
		browserService,
		notificationService,
	)
	if err != nil {
		t.Fatalf("buildHerdrSessionConfig() error = %v", err)
	}
	expected := []string{
		"Host test-codespace--gh-ado-session",
		"HostName example.com",
		"User codespace",
		"RemoteForward /tmp/auth.sock 127.0.0.1:8080",
		"RemoteForward /tmp/browser.sock 127.0.0.1:8081",
		"RemoteForward /tmp/notification.sock 127.0.0.1:8082",
		"RemoteForward 1234 localhost:1234",
		"ForwardX11 yes",
		"ForwardX11Trusted yes",
	}

	for _, snippet := range expected {
		if !strings.Contains(got, snippet) {
			t.Errorf("buildHerdrSessionConfig() missing %q:\n%s", snippet, got)
		}
	}
}

func TestReplaceSSHHostAliasMissing(t *testing.T) {
	_, err := replaceGeneratedSSHHostAlias("User codespace\n", "session-alias")
	if err == nil {
		t.Fatal("replaceGeneratedSSHHostAlias() expected an error")
	}
}

func TestHerdrSSHConfigWrite(t *testing.T) {
	homeDir := t.TempDir()
	paths := newHerdrSSHConfig(homeDir, "test-codespace", "test-session")

	existingConfig := "Host example\n  HostName example.com"
	if err := os.MkdirAll(filepath.Dir(paths.mainConfig), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.mainConfig, []byte(existingConfig), 0600); err != nil {
		t.Fatal(err)
	}

	if err := paths.write("Host test-codespace\n  HostName example\n", "Host test-codespace\n  RemoteForward /tmp/auth.sock 127.0.0.1:8080\n"); err != nil {
		t.Fatalf("write() error = %v", err)
	}
	if err := paths.write("Host test-codespace\n  HostName updated\n", "Host test-codespace\n  RemoteForward /tmp/auth.sock 127.0.0.1:8080\n"); err != nil {
		t.Fatalf("second write() error = %v", err)
	}

	mainConfig, err := os.ReadFile(paths.mainConfig)
	if err != nil {
		t.Fatal(err)
	}
	content := string(mainConfig)
	if !strings.Contains(content, existingConfig) {
		t.Fatalf("main SSH config was not preserved:\n%s", content)
	}
	if !strings.HasPrefix(content, herdrConfigBlockStart) {
		t.Fatalf("managed includes must precede user SSH settings:\n%s", content)
	}
	if strings.Count(content, herdrSessionsDirectory+"/*.conf") != 1 {
		t.Fatalf("session include was not idempotent:\n%s", content)
	}
	if strings.Count(content, herdrHostsDirectory+"/*.conf") != 1 {
		t.Fatalf("host include was not idempotent:\n%s", content)
	}
	if strings.Count(content, "Match all") != 3 {
		t.Fatalf("managed includes must reset prior Host or Match context:\n%s", content)
	}
	if strings.Index(content, herdrSessionsDirectory+"/*.conf") > strings.Index(content, herdrHostsDirectory+"/*.conf") {
		t.Fatalf("session include must precede host include:\n%s", content)
	}

	hostConfig, err := os.ReadFile(paths.hostFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(hostConfig), "HostName updated") {
		t.Fatalf("host config was not updated:\n%s", hostConfig)
	}

	for _, path := range []string{paths.mainConfig, paths.hostFile, paths.sessionFile} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0600 {
			t.Errorf("%s mode = %o, want 600", path, info.Mode().Perm())
		}
	}
}

func TestHerdrSSHConfigWritePreservesSymlink(t *testing.T) {
	homeDir := t.TempDir()
	paths := newHerdrSSHConfig(homeDir, "test-codespace", "test-session")
	targetDir := t.TempDir()
	targetConfig := filepath.Join(targetDir, "ssh-config")

	if err := os.MkdirAll(filepath.Dir(paths.mainConfig), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetConfig, []byte("Host example\n  HostName example.com\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(targetConfig, paths.mainConfig); err != nil {
		t.Fatal(err)
	}

	if err := paths.write("Host test-codespace\n  HostName example\n", "Host session-alias\n  HostName example\n"); err != nil {
		t.Fatalf("write() error = %v", err)
	}

	info, err := os.Lstat(paths.mainConfig)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("main SSH config symlink was replaced")
	}

	targetContent, err := os.ReadFile(targetConfig)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(targetContent), herdrConfigBlockStart) {
		t.Fatalf("symlink target was not updated:\n%s", targetContent)
	}
}

func TestRemoteForwardArgument(t *testing.T) {
	forward := remoteForward{remote: "/tmp/auth.sock", local: "127.0.0.1:8080"}
	if got := forward.argument(); got != "/tmp/auth.sock:127.0.0.1:8080" {
		t.Fatalf("argument() = %q", got)
	}
}
