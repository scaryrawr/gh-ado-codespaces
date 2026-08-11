package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/cli/go-gh/v2"
)

const (
	herdrSSHDirectoryName  = "gh-ado-codespaces"
	herdrHostsDirectory    = "hosts"
	herdrSessionsDirectory = "sessions"
	herdrConfigBlockStart  = "# gh-ado-codespaces managed includes"
	herdrConfigBlockEnd    = "# end gh-ado-codespaces managed includes"
)

type herdrSSHConfig struct {
	rootDir     string
	hostFile    string
	sessionFile string
	mainConfig  string
}

func newHerdrSSHConfig(homeDir, codespaceName, currentSessionID string) herdrSSHConfig {
	rootDir := filepath.Join(homeDir, ".ssh", herdrSSHDirectoryName)

	return herdrSSHConfig{
		rootDir:     rootDir,
		hostFile:    filepath.Join(rootDir, herdrHostsDirectory, hostConfigFileName(codespaceName)),
		sessionFile: filepath.Join(rootDir, herdrSessionsDirectory, sessionConfigFileName(currentSessionID)),
		mainConfig:  filepath.Join(homeDir, ".ssh", "config"),
	}
}

func findHerdrExecutable() (string, error) {
	path, err := exec.LookPath("herdr")
	if err != nil {
		return "", fmt.Errorf("herdr is not installed or is not on PATH; install it from https://herdr.dev: %w", err)
	}

	return path, nil
}

func buildHerdrArgs(codespaceName string) []string {
	return []string{"--remote", codespaceName}
}

func buildCodespaceSSHConfigArgs(args *CommandLineArgs) []string {
	configArgs := []string{"codespace", "ssh", "--codespace", args.CodespaceName, "--config"}

	if args.Debug {
		configArgs = append(configArgs, "--debug")
	}
	if args.DebugFile != "" {
		configArgs = append(configArgs, "--debug-file", args.DebugFile)
	}

	return configArgs
}

func runHerdr(ctx context.Context, executable, codespaceName string) error {
	cmd := exec.CommandContext(ctx, executable, buildHerdrArgs(codespaceName)...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("herdr remote session failed: %w", err)
	}

	return nil
}

func generateCodespaceSSHConfig(ctx context.Context, args *CommandLineArgs) (string, error) {
	ghExe, err := gh.Path()
	if err != nil {
		return "", fmt.Errorf("failed to locate gh executable: %w", err)
	}

	cmd := exec.CommandContext(ctx, ghExe, buildCodespaceSSHConfigArgs(args)...)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to generate SSH config for codespace %q: %w\n%s", args.CodespaceName, err, strings.TrimSpace(stderr.String()))
	}

	if strings.TrimSpace(stdout.String()) == "" {
		return "", fmt.Errorf("gh returned an empty SSH config for codespace %q", args.CodespaceName)
	}

	return stdout.String(), nil
}

func setupHerdrSSHConfig(ctx context.Context, codespaceName string, args *CommandLineArgs, serverConfig *ServerConfig, browserService *BrowserService, notificationService *NotificationService) (string, func(), error) {
	if err := validateSSHHostAlias(codespaceName); err != nil {
		return "", nil, err
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", nil, fmt.Errorf("failed to determine home directory: %w", err)
	}

	generatedConfig, err := generateCodespaceSSHConfig(ctx, args)
	if err != nil {
		return "", nil, err
	}

	herdrHostAlias := buildHerdrSSHHostAlias(codespaceName, sessionID)
	paths := newHerdrSSHConfig(homeDir, codespaceName, sessionID)
	sessionConfig, err := buildHerdrSessionConfig(generatedConfig, herdrHostAlias, serverConfig, browserService, notificationService)
	if err != nil {
		return "", nil, err
	}
	if err := paths.write(generatedConfig, sessionConfig); err != nil {
		return "", nil, err
	}

	cleanup := func() {
		if err := os.Remove(paths.sessionFile); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "Warning: failed to remove Herdr SSH session config: %v\n", err)
		}
	}

	return herdrHostAlias, cleanup, nil
}

func buildHerdrSSHHostAlias(codespaceName, currentSessionID string) string {
	sessionHash := sha256.Sum256([]byte(currentSessionID))

	return sanitizeForFilename(codespaceName) + fmt.Sprintf("--gh-ado-%x", sessionHash[:8])
}

func sessionConfigFileName(currentSessionID string) string {
	sessionHash := sha256.Sum256([]byte(currentSessionID))

	return fmt.Sprintf("session-%x.conf", sessionHash[:8])
}

func hostConfigFileName(codespaceName string) string {
	nameHash := sha256.Sum256([]byte(codespaceName))

	return fmt.Sprintf("%s-%x.conf", sanitizeForFilename(codespaceName), nameHash[:8])
}

func validateSSHHostAlias(alias string) error {
	if alias == "" {
		return fmt.Errorf("codespace name cannot be empty")
	}

	for _, character := range alias {
		isLetter := character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z'
		isDigit := character >= '0' && character <= '9'
		if !isLetter && !isDigit && character != '-' && character != '_' && character != '.' {
			return fmt.Errorf("codespace name %q cannot be used as an SSH host alias", alias)
		}
	}

	return nil
}

func buildHerdrSessionConfig(generatedConfig, herdrHostAlias string, serverConfig *ServerConfig, browserService *BrowserService, notificationService *NotificationService) (string, error) {
	aliasedConfig, err := replaceGeneratedSSHHostAlias(generatedConfig, herdrHostAlias)
	if err != nil {
		return "", err
	}

	var config strings.Builder
	config.WriteString(aliasedConfig)
	if !strings.HasSuffix(aliasedConfig, "\n") {
		config.WriteString("\n")
	}
	fmt.Fprintf(&config, "\nHost %s\n", herdrHostAlias)
	for _, forward := range buildRemoteForwards(serverConfig.SocketPath, serverConfig.Port, browserService, notificationService) {
		fmt.Fprintf(&config, "  RemoteForward %s %s\n", forward.remote, forward.local)
	}

	if supportsX11Tunneling() {
		config.WriteString("  ForwardX11 yes\n")
		config.WriteString("  ForwardX11Trusted yes\n")
	}

	return config.String(), nil
}

func replaceGeneratedSSHHostAlias(config, newAlias string) (string, error) {
	lines := strings.Split(config, "\n")
	oldAlias := ""

	for index, line := range lines {
		fields := strings.Fields(line)
		if len(fields) != 2 || !strings.EqualFold(fields[0], "Host") {
			continue
		}

		if oldAlias == "" {
			oldAlias = fields[1]
		}
		if fields[1] != oldAlias {
			return "", fmt.Errorf("GitHub CLI SSH config contained multiple Host aliases")
		}

		indentLength := len(line) - len(strings.TrimLeft(line, " \t"))
		lines[index] = line[:indentLength] + "Host " + newAlias
	}

	if oldAlias == "" {
		return "", fmt.Errorf("GitHub CLI SSH config did not contain a Host entry")
	}

	return strings.Join(lines, "\n"), nil
}

func (config herdrSSHConfig) write(hostConfig, sessionConfig string) error {
	if err := os.MkdirAll(filepath.Dir(config.hostFile), 0700); err != nil {
		return fmt.Errorf("failed to create Herdr SSH host directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(config.sessionFile), 0700); err != nil {
		return fmt.Errorf("failed to create Herdr SSH session directory: %w", err)
	}
	if err := os.Chmod(config.rootDir, 0700); err != nil {
		return fmt.Errorf("failed to secure Herdr SSH config directory: %w", err)
	}
	if err := os.Chmod(filepath.Dir(config.hostFile), 0700); err != nil {
		return fmt.Errorf("failed to secure Herdr SSH host directory: %w", err)
	}
	if err := os.Chmod(filepath.Dir(config.sessionFile), 0700); err != nil {
		return fmt.Errorf("failed to secure Herdr SSH session directory: %w", err)
	}

	if err := writeFileAtomically(config.hostFile, []byte(hostConfig), 0600); err != nil {
		return fmt.Errorf("failed to write Codespaces SSH config: %w", err)
	}
	if err := writeFileAtomically(config.sessionFile, []byte(sessionConfig), 0600); err != nil {
		return fmt.Errorf("failed to write Herdr SSH session config: %w", err)
	}
	if err := ensureHerdrSSHIncludes(config.mainConfig, config.rootDir); err != nil {
		_ = os.Remove(config.sessionFile)

		return err
	}

	return nil
}

func ensureHerdrSSHIncludes(mainConfig, rootDir string) error {
	if err := os.MkdirAll(filepath.Dir(mainConfig), 0700); err != nil {
		return fmt.Errorf("failed to create SSH directory: %w", err)
	}

	writePath, err := resolveSSHConfigWritePath(mainConfig)
	if err != nil {
		return err
	}

	existing, err := os.ReadFile(writePath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to read SSH config: %w", err)
	}

	sessionInclude := fmt.Sprintf("Include %s", strconv.Quote(filepath.Join(rootDir, herdrSessionsDirectory, "*.conf")))
	hostInclude := fmt.Sprintf("Include %s", strconv.Quote(filepath.Join(rootDir, herdrHostsDirectory, "*.conf")))
	content := string(existing)
	managedBlock := strings.Join([]string{
		herdrConfigBlockStart,
		"Match all",
		sessionInclude,
		"Match all",
		hostInclude,
		"Match all",
		herdrConfigBlockEnd,
	}, "\n")

	content = removeManagedSSHConfigBlock(content)
	content = strings.TrimLeft(content, "\n")
	if content == "" {
		content = managedBlock + "\n"
	} else {
		content = managedBlock + "\n\n" + content
	}

	if string(existing) == content {
		return nil
	}

	if err := writeFileAtomically(writePath, []byte(content), 0600); err != nil {
		return fmt.Errorf("failed to update SSH config includes: %w", err)
	}

	return nil
}

func resolveSSHConfigWritePath(mainConfig string) (string, error) {
	info, err := os.Lstat(mainConfig)
	if os.IsNotExist(err) {
		return mainConfig, nil
	}
	if err != nil {
		return "", fmt.Errorf("failed to inspect SSH config: %w", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return mainConfig, nil
	}

	resolved, err := filepath.EvalSymlinks(mainConfig)
	if err != nil {
		return "", fmt.Errorf("failed to resolve SSH config symlink: %w", err)
	}

	return resolved, nil
}

func removeManagedSSHConfigBlock(content string) string {
	blockStart := strings.Index(content, herdrConfigBlockStart)
	blockEnd := strings.Index(content, herdrConfigBlockEnd)
	if blockStart >= 0 && blockEnd >= blockStart {
		blockEnd += len(herdrConfigBlockEnd)
		return content[:blockStart] + content[blockEnd:]
	}

	return content
}

func writeFileAtomically(path string, content []byte, mode os.FileMode) error {
	tempFile, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tempPath := tempFile.Name()
	defer os.Remove(tempPath)

	if err := tempFile.Chmod(mode); err != nil {
		_ = tempFile.Close()

		return err
	}
	if _, err := tempFile.Write(content); err != nil {
		_ = tempFile.Close()

		return err
	}
	if err := tempFile.Close(); err != nil {
		return err
	}

	return os.Rename(tempPath, path)
}
