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
	Azure              *AzureConfig         `json:"azure,omitempty"`
	ReversePortForward []ReversePortForward `json:"reversePortForward,omitempty"`
}

// AppConfig captures global and per-login configuration.
type AppConfig struct {
	ReversePortForward []ReversePortForward     `json:"reversePortForward,omitempty"`
	Accounts           map[string]AccountConfig `json:"accounts,omitempty"`
}

// UnmarshalJSON supports both the current structured format and the legacy
// login-keyed object format.
func (c *AppConfig) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	// Use type-based detection to distinguish structured from legacy format.
	// In structured format, "reversePortForward" must be a JSON array and
	// "accounts" must be a JSON object. Any other top-level key, or wrong value
	// type for a known key, indicates a legacy login-keyed config.
	isStructured := len(raw) > 0
	for key, val := range raw {
		switch key {
		case "reversePortForward":
			if !jsonIsArray(val) {
				isStructured = false
			}
		case "accounts":
			if !jsonIsObject(val) {
				isStructured = false
			}
		default:
			isStructured = false
		}
		if !isStructured {
			break
		}
	}
	if isStructured {
		type appConfigAlias AppConfig
		var alias appConfigAlias
		if err := json.Unmarshal(data, &alias); err != nil {
			return err
		}
		*c = AppConfig(alias)
		if c.Accounts == nil {
			c.Accounts = make(map[string]AccountConfig)
		}
		return nil
	}

	var legacy map[string]AccountConfig
	if err := json.Unmarshal(data, &legacy); err != nil {
		return err
	}
	c.Accounts = legacy
	if c.Accounts == nil {
		c.Accounts = make(map[string]AccountConfig)
	}
	return nil
}

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
		return AppConfig{}, err
	}

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return AppConfig{}, nil
	}

	if err != nil {
		return AppConfig{}, fmt.Errorf("read config file %s: %w", path, err)
	}

	if len(strings.TrimSpace(string(data))) == 0 {
		return AppConfig{}, nil
	}

	var cfg AppConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return AppConfig{}, fmt.Errorf("parse config file %s: %w", path, err)
	}
	if cfg.Accounts == nil {
		cfg.Accounts = make(map[string]AccountConfig)
	}

	return cfg, nil
}

// AzureSubscriptionForLogin returns the Azure subscription override for a GitHub login, if present.
func (c AppConfig) AzureSubscriptionForLogin(login string) (string, bool) {
	acct, ok := c.Accounts[login]
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
func (c *AppConfig) SetAzureSubscriptionForLogin(login, subscription string) {
	if c == nil {
		return
	}
	if c.Accounts == nil {
		c.Accounts = make(map[string]AccountConfig)
	}
	login = strings.TrimSpace(login)
	if login == "" {
		return
	}
	sub := strings.TrimSpace(subscription)
	if sub == "" {
		// Clear existing if present
		if acct, ok := c.Accounts[login]; ok {
			acct.Azure = nil
			// If AccountConfig is now empty, remove the login entry entirely
			if acct.Azure == nil && len(acct.ReversePortForward) == 0 {
				delete(c.Accounts, login)
			} else {
				c.Accounts[login] = acct
			}
		}
		return
	}
	acct := c.Accounts[login] // zero value if not exists
	if acct.Azure == nil {
		acct.Azure = &AzureConfig{}
	}
	acct.Azure.Subscription = sub
	c.Accounts[login] = acct
}

// ReversePortForwardsForLogin returns defaults merged with top-level and per-login overrides.
func (c AppConfig) ReversePortForwardsForLogin(login string) []ReversePortForward {
	accountForwards := []ReversePortForward(nil)
	if acct, ok := c.Accounts[login]; ok {
		accountForwards = acct.ReversePortForward
	}

	return MergeReversePortForwards(WellKnownPorts, c.ReversePortForward, accountForwards)
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

// jsonIsArray reports whether raw JSON data represents an array ([...]).
func jsonIsArray(data json.RawMessage) bool {
	for _, b := range data {
		if b == ' ' || b == '\t' || b == '\n' || b == '\r' {
			continue
		}
		return b == '['
	}
	return false
}

// jsonIsObject reports whether raw JSON data represents an object ({...}).
func jsonIsObject(data json.RawMessage) bool {
	for _, b := range data {
		if b == ' ' || b == '\t' || b == '\n' || b == '\r' {
			continue
		}
		return b == '{'
	}
	return false
}
