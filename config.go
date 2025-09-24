package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const configEnvVar = "GH_ADO_CODESPACES_CONFIG"

// AzureConfig captures Azure-specific overrides for an account.
type AzureConfig struct {
	Subscription string `json:"subscription"`
}

// AccountConfig captures per-login configuration.
type AccountConfig struct {
	Azure *AzureConfig `json:"azure"`
}

// AppConfig is keyed by GitHub login ID.
type AppConfig map[string]AccountConfig

// getConfigFilePath resolves the configuration file path.
func getConfigFilePath() (string, error) {
	if override := strings.TrimSpace(os.Getenv(configEnvVar)); override != "" {
		return override, nil
	}

	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config dir: %w", err)
	}

	return filepath.Join(configDir, "gh-ado-codespaces", "config.json"), nil
}

// LoadAppConfig loads the configuration file, returning an empty configuration if the file is absent.
func LoadAppConfig() (AppConfig, error) {
	path, err := getConfigFilePath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return AppConfig{}, nil
	}

	if err != nil {
		return nil, fmt.Errorf("read config file %s: %w", path, err)
	}

	if len(strings.TrimSpace(string(data))) == 0 {
		return AppConfig{}, nil
	}

	var cfg AppConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config file %s: %w", path, err)
	}

	return cfg, nil
}

// AzureSubscriptionForLogin returns the Azure subscription override for a GitHub login, if present.
func (c AppConfig) AzureSubscriptionForLogin(login string) (string, bool) {
	if c == nil {
		return "", false
	}

	acct, ok := c[login]
	if !ok || acct.Azure == nil {
		return "", false
	}

	subscription := strings.TrimSpace(acct.Azure.Subscription)
	if subscription == "" {
		return "", false
	}

	return subscription, true
}

// SetAzureSubscriptionForLogin sets (or clears if empty) the Azure subscription for a given login.
func (c AppConfig) SetAzureSubscriptionForLogin(login, subscription string) {
	if c == nil {
		return
	}
	login = strings.TrimSpace(login)
	if login == "" {
		return
	}
	sub := strings.TrimSpace(subscription)
	if sub == "" {
		// Clear existing if present
		if acct, ok := c[login]; ok {
			if acct.Azure != nil {
				acct.Azure.Subscription = ""
			}
			c[login] = acct
		}
		return
	}
	acct := c[login] // zero value if not exists
	if acct.Azure == nil {
		acct.Azure = &AzureConfig{}
	}
	acct.Azure.Subscription = sub
	c[login] = acct
}

// SaveAppConfig persists the configuration to disk, creating directories as needed.
func SaveAppConfig(cfg AppConfig) error {
	path, err := getConfigFilePath()
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create config dir %s: %w", dir, err)
	}

	// Marshal with indentation for readability
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return fmt.Errorf("write temp config file %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace config file %s: %w", path, err)
	}
	return nil
}
