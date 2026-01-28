package main

import (
	"context"
	_ "embed"
	"fmt"
	"os"

	"github.com/cli/go-gh/v2"
)

//go:embed ado-auth-helper.py
var adoAuthHelperScript string

// UploadAuthHelpers uploads and configures the ADO and Azure auth helper scripts to the specified codespace
func UploadAuthHelpers(ctx context.Context, codespaceName string) error {
	// Create temporary files for both helper scripts
	adoHelperFile, err := os.CreateTemp("", "ado-auth-helper*.py")
	if err != nil {
		return fmt.Errorf("failed to create temporary file for ADO helper: %w", err)
	}
	defer os.Remove(adoHelperFile.Name())

	// Both helpers use the same script
	if _, err = adoHelperFile.WriteString(adoAuthHelperScript); err != nil {
		return fmt.Errorf("failed to write ADO helper script to temporary file: %w", err)
	}
	adoHelperFile.Close()

	// Upload the ADO helper script
	args := []string{"codespace", "cp", "-c", codespaceName, "-e", adoHelperFile.Name(), "remote:~/ado-auth-helper"}
	_, stderr, err := gh.Exec(args...)
	if err != nil {
		return fmt.Errorf("error copying ADO helper script to codespace: %w\nStderr: %s", err, stderr.String())
	}

	// Upload the same script as Azure helper
	args = []string{"codespace", "cp", "-c", codespaceName, "-e", adoHelperFile.Name(), "remote:~/azure-auth-helper"}
	_, stderr, err = gh.Exec(args...)
	if err != nil {
		return fmt.Errorf("error copying Azure helper script to codespace: %w\nStderr: %s", err, stderr.String())
	}

	// Make the uploaded scripts executable
	chmodArgs := []string{"codespace", "ssh", "--codespace", codespaceName, "--", "chmod", "+x", "~/ado-auth-helper", "~/azure-auth-helper"}
	_, stderr, err = gh.Exec(chmodArgs...)
	if err != nil {
		return fmt.Errorf("error making auth helper scripts executable: %w\nStderr: %s", err, stderr.String())
	}

	// Create symbolic links in /usr/local/bin if they don't already exist
	symlinkCmd := "test -L /usr/local/bin/ado-auth-helper || sudo ln -sf ~/ado-auth-helper /usr/local/bin/ado-auth-helper; " +
		"test -L /usr/local/bin/azure-auth-helper || sudo ln -sf ~/azure-auth-helper /usr/local/bin/azure-auth-helper"
	symlinkArgs := []string{"codespace", "ssh", "--codespace", codespaceName, "--", "bash", "-c", symlinkCmd}
	_, stderr, err = gh.Exec(symlinkArgs...)
	if err != nil {
		return fmt.Errorf("error creating symbolic links in /usr/local/bin: %w\nStderr: %s", err, stderr.String())
	}

	fmt.Fprintln(os.Stderr, "ADO and Azure auth helpers uploaded to the codespace and made executable")
	return nil
}
