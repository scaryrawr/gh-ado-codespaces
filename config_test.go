package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestAppConfig_AzureSubscriptionForLogin(t *testing.T) {
	tests := []struct {
		name       string
		config     AppConfig
		login      string
		wantSub    string
		wantExists bool
	}{
		{
			name:       "empty config",
			config:     AppConfig{},
			login:      "user1",
			wantSub:    "",
			wantExists: false,
		},
		{
			name: "login not found",
			config: AppConfig{Accounts: map[string]AccountConfig{
				"user1": {
					Azure: &AzureConfig{Subscription: "sub123"},
				},
			}},
			login:      "user2",
			wantSub:    "",
			wantExists: false,
		},
		{
			name: "login found, no azure config",
			config: AppConfig{Accounts: map[string]AccountConfig{
				"user1": {},
			}},
			login:      "user1",
			wantSub:    "",
			wantExists: false,
		},
		{
			name: "login found, valid subscription",
			config: AppConfig{Accounts: map[string]AccountConfig{
				"user1": {
					Azure: &AzureConfig{Subscription: "sub123"},
				},
			}},
			login:      "user1",
			wantSub:    "sub123",
			wantExists: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotSub, gotExists := tt.config.AzureSubscriptionForLogin(tt.login)
			if gotSub != tt.wantSub {
				t.Errorf("AzureSubscriptionForLogin() subscription = %v, want %v", gotSub, tt.wantSub)
			}
			if gotExists != tt.wantExists {
				t.Errorf("AzureSubscriptionForLogin() exists = %v, want %v", gotExists, tt.wantExists)
			}
		})
	}
}

func TestAppConfig_SetAzureSubscriptionForLogin(t *testing.T) {
	t.Run("set and clear subscription", func(t *testing.T) {
		cfg := AppConfig{}

		cfg.SetAzureSubscriptionForLogin("user1", "sub123")
		sub, ok := cfg.AzureSubscriptionForLogin("user1")
		if !ok || sub != "sub123" {
			t.Fatalf("expected stored subscription, got sub=%q ok=%v", sub, ok)
		}

		cfg.SetAzureSubscriptionForLogin("user1", "")
		if _, ok := cfg.AzureSubscriptionForLogin("user1"); ok {
			t.Fatal("expected subscription to be cleared")
		}
	})

	t.Run("clear subscription preserves reverse port config", func(t *testing.T) {
		cfg := AppConfig{Accounts: map[string]AccountConfig{
			"user1": {
				Azure: &AzureConfig{Subscription: "sub123"},
				ReversePortForward: []ReversePortForward{
					{Port: 4242, Description: "Custom", Enabled: true},
				},
			},
		}}

		cfg.SetAzureSubscriptionForLogin("user1", "")
		acct, ok := cfg.Accounts["user1"]
		if !ok {
			t.Fatal("expected account to remain because reverse port config exists")
		}
		if acct.Azure != nil {
			t.Fatal("expected azure config to be cleared")
		}
		if len(acct.ReversePortForward) != 1 || acct.ReversePortForward[0].Port != 4242 {
			t.Fatalf("expected reverse port config to remain, got %+v", acct.ReversePortForward)
		}
	})
}

func TestAppConfig_ReversePortForwardsForLogin(t *testing.T) {
	original := WellKnownPorts
	defer func() { WellKnownPorts = original }()

	WellKnownPorts = []ReversePortForward{
		{Port: 1234, Description: "LM Studio", Enabled: true},
		{Port: 11434, Description: "Ollama", Enabled: true},
	}

	cfg := AppConfig{
		ReversePortForward: []ReversePortForward{
			{Port: 1234, Description: "LM Studio override", Enabled: false},
			{Port: 8081, Description: "Top level", Enabled: true},
		},
		Accounts: map[string]AccountConfig{
			"user1": {
				ReversePortForward: []ReversePortForward{
					{Port: 8081, Description: "Per account", Enabled: false},
					{Port: 9090, Description: "Per account extra", Enabled: true},
				},
			},
		},
	}

	merged := cfg.ReversePortForwardsForLogin("user1")

	byPort := make(map[int]ReversePortForward)
	for _, forward := range merged {
		byPort[forward.Port] = forward
	}

	if got, ok := byPort[1234]; !ok || got.Enabled {
		t.Fatalf("expected top-level override for 1234 to disable default, got %+v", got)
	}
	if got, ok := byPort[8081]; !ok || got.Enabled {
		t.Fatalf("expected account override for 8081 to win, got %+v", got)
	}
	if _, ok := byPort[9090]; !ok {
		t.Fatal("expected account custom port 9090 to be present")
	}
}

func TestLoadAppConfig(t *testing.T) {
	tempDir := t.TempDir()

	tests := []struct {
		name        string
		configPath  string
		configData  string
		createFile  bool // create the file even when configData is empty
		expectError bool
		expected    AppConfig
	}{
		{
			name:       "non-existent file",
			configPath: filepath.Join(tempDir, "nonexistent.json"),
			expected:   AppConfig{},
		},
		{
			name:       "empty file",
			configPath: filepath.Join(tempDir, "empty.json"),
			configData: "",
			createFile: true,
			expected:   AppConfig{},
		},
		{
			name:       "whitespace only file",
			configPath: filepath.Join(tempDir, "whitespace.json"),
			configData: "   \n\t  ",
			expected:   AppConfig{},
		},
		{
			name:       "valid structured config",
			configPath: filepath.Join(tempDir, "structured.json"),
			configData: `{
"reversePortForward": [{"port": 8081, "description": "Top", "enabled": true}],
"accounts": {
"user1": {
"azure": {"subscription": "sub123"},
"reversePortForward": [{"port": 9090, "description": "User", "enabled": true}]
}
}
}`,
			expected: AppConfig{
				ReversePortForward: []ReversePortForward{{Port: 8081, Description: "Top", Enabled: true}},
				Accounts: map[string]AccountConfig{
					"user1": {
						Azure:              &AzureConfig{Subscription: "sub123"},
						ReversePortForward: []ReversePortForward{{Port: 9090, Description: "User", Enabled: true}},
					},
				},
			},
		},
		{
			name:       "valid legacy account keyed config",
			configPath: filepath.Join(tempDir, "legacy.json"),
			configData: `{
"user1": {"azure": {"subscription": "sub123"}}
}`,
			expected: AppConfig{Accounts: map[string]AccountConfig{
				"user1": {Azure: &AzureConfig{Subscription: "sub123"}},
			}},
		},
		{
			name:        "invalid json",
			configPath:  filepath.Join(tempDir, "invalid.json"),
			configData:  `{"invalid": json}`,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			originalEnv := os.Getenv(configEnvVar)
			defer os.Setenv(configEnvVar, originalEnv)

			if tt.configData != "" || tt.createFile {
				if err := os.WriteFile(tt.configPath, []byte(tt.configData), 0o644); err != nil {
					t.Fatalf("Failed to create test config file: %v", err)
				}
			}

			os.Setenv(configEnvVar, tt.configPath)

			result, err := LoadAppConfig()
			if tt.expectError {
				if err == nil {
					t.Errorf("Expected an error, but got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			resultJSON, _ := json.Marshal(result)
			expectedJSON, _ := json.Marshal(tt.expected)
			if string(resultJSON) != string(expectedJSON) {
				t.Errorf("Config mismatch\nGot:  %s\nWant: %s", resultJSON, expectedJSON)
			}
		})
	}
}

func TestSaveAppConfig(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")

	originalEnv := os.Getenv(configEnvVar)
	defer os.Setenv(configEnvVar, originalEnv)
	os.Setenv(configEnvVar, configPath)

	config := AppConfig{
		ReversePortForward: []ReversePortForward{
			{Port: 8081, Description: "Top", Enabled: true},
		},
		Accounts: map[string]AccountConfig{
			"user1": {
				Azure: &AzureConfig{Subscription: "sub123"},
				ReversePortForward: []ReversePortForward{
					{Port: 9090, Description: "User", Enabled: true},
				},
			},
		},
	}

	if err := SaveAppConfig(config); err != nil {
		t.Fatalf("Failed to save config: %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("Failed to read saved config file: %v", err)
	}

	var savedConfig AppConfig
	if err := json.Unmarshal(data, &savedConfig); err != nil {
		t.Fatalf("Failed to unmarshal saved config: %v", err)
	}

	savedJSON, _ := json.Marshal(savedConfig)
	wantJSON, _ := json.Marshal(config)
	if string(savedJSON) != string(wantJSON) {
		t.Errorf("Saved config mismatch\nGot:  %s\nWant: %s", savedJSON, wantJSON)
	}
}
