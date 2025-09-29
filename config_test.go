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
			name:       "nil config",
			config:     nil,
			login:      "user1",
			wantSub:    "",
			wantExists: false,
		},
		{
			name:       "empty config",
			config:     AppConfig{},
			login:      "user1",
			wantSub:    "",
			wantExists: false,
		},
		{
			name: "login not found",
			config: AppConfig{
				"user1": AccountConfig{
					Azure: &AzureConfig{
						Subscription: "sub123",
					},
				},
			},
			login:      "user2",
			wantSub:    "",
			wantExists: false,
		},
		{
			name: "login found, no azure config",
			config: AppConfig{
				"user1": AccountConfig{},
			},
			login:      "user1",
			wantSub:    "",
			wantExists: false,
		},
		{
			name: "login found, empty subscription",
			config: AppConfig{
				"user1": AccountConfig{
					Azure: &AzureConfig{
						Subscription: "",
					},
				},
			},
			login:      "user1",
			wantSub:    "",
			wantExists: false,
		},
		{
			name: "login found, whitespace subscription",
			config: AppConfig{
				"user1": AccountConfig{
					Azure: &AzureConfig{
						Subscription: "   ",
					},
				},
			},
			login:      "user1",
			wantSub:    "",
			wantExists: false,
		},
		{
			name: "login found, valid subscription",
			config: AppConfig{
				"user1": AccountConfig{
					Azure: &AzureConfig{
						Subscription: "sub123",
					},
				},
			},
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
	tests := []struct {
		name         string
		config       AppConfig
		login        string
		subscription string
		wantConfig   AppConfig
	}{
		{
			name:         "nil config",
			config:       nil,
			login:        "user1",
			subscription: "sub123",
			wantConfig:   nil, // Should remain nil, function should not panic
		},
		{
			name:         "empty login",
			config:       AppConfig{},
			login:        "",
			subscription: "sub123",
			wantConfig:   AppConfig{},
		},
		{
			name:         "whitespace login",
			config:       AppConfig{},
			login:        "   ",
			subscription: "sub123",
			wantConfig:   AppConfig{},
		},
		{
			name:   "set subscription for new user",
			config: AppConfig{},
			login:  "user1",
			subscription: "sub123",
			wantConfig: AppConfig{
				"user1": AccountConfig{
					Azure: &AzureConfig{
						Subscription: "sub123",
					},
				},
			},
		},
		{
			name: "update existing user subscription",
			config: AppConfig{
				"user1": AccountConfig{
					Azure: &AzureConfig{
						Subscription: "old-sub",
					},
				},
			},
			login:        "user1",
			subscription: "new-sub",
			wantConfig: AppConfig{
				"user1": AccountConfig{
					Azure: &AzureConfig{
						Subscription: "new-sub",
					},
				},
			},
		},
		{
			name: "clear subscription",
			config: AppConfig{
				"user1": AccountConfig{
					Azure: &AzureConfig{
						Subscription: "sub123",
					},
				},
			},
			login:        "user1",
			subscription: "",
			wantConfig:   AppConfig{}, // Entry should be removed entirely
		},
		{
			name: "clear subscription with whitespace",
			config: AppConfig{
				"user1": AccountConfig{
					Azure: &AzureConfig{
						Subscription: "sub123",
					},
				},
			},
			login:        "user1",
			subscription: "   ",
			wantConfig:   AppConfig{}, // Entry should be removed entirely
		},
		{
			name: "set subscription for user with no azure config",
			config: AppConfig{
				"user1": AccountConfig{},
			},
			login:        "user1",
			subscription: "sub123",
			wantConfig: AppConfig{
				"user1": AccountConfig{
					Azure: &AzureConfig{
						Subscription: "sub123",
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.config != nil {
				tt.config.SetAzureSubscriptionForLogin(tt.login, tt.subscription)
			}

			// Compare the results
			if tt.wantConfig == nil {
				if tt.config != nil {
					t.Errorf("Expected config to remain nil, but got %v", tt.config)
				}
				return
			}

			if tt.config == nil {
				t.Errorf("Expected non-nil config, but got nil")
				return
			}

			// Deep comparison
			for login, wantAccount := range tt.wantConfig {
				gotAccount, exists := tt.config[login]
				if !exists {
					t.Errorf("Expected login %s to exist in config", login)
					continue
				}

				if (gotAccount.Azure == nil) != (wantAccount.Azure == nil) {
					t.Errorf("Azure config mismatch for login %s: got nil=%v, want nil=%v", 
						login, gotAccount.Azure == nil, wantAccount.Azure == nil)
					continue
				}

				if gotAccount.Azure != nil && wantAccount.Azure != nil {
					if gotAccount.Azure.Subscription != wantAccount.Azure.Subscription {
						t.Errorf("Subscription mismatch for login %s: got %s, want %s", 
							login, gotAccount.Azure.Subscription, wantAccount.Azure.Subscription)
					}
				}
			}
		})
	}
}

func TestLoadAppConfig(t *testing.T) {
	// Create a temporary directory for test config files
	tempDir := t.TempDir()
	
	tests := []struct {
		name        string
		configPath  string
		configData  string
		expectError bool
		expected    AppConfig
	}{
		{
			name:       "non-existent file",
			configPath: filepath.Join(tempDir, "nonexistent.json"),
			expected:   AppConfig{},
		},
		{
			name:        "empty file",
			configPath:  filepath.Join(tempDir, "empty.json"),
			configData:  "",
			expected:    AppConfig{},
		},
		{
			name:        "whitespace only file",
			configPath:  filepath.Join(tempDir, "whitespace.json"),
			configData:  "   \n\t  ",
			expected:    AppConfig{},
		},
		{
			name:       "valid config",
			configPath: filepath.Join(tempDir, "valid.json"),
			configData: `{
				"user1": {
					"azure": {
						"subscription": "sub123"
					}
				}
			}`,
			expected: AppConfig{
				"user1": AccountConfig{
					Azure: &AzureConfig{
						Subscription: "sub123",
					},
				},
			},
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
			// Set up test environment
			originalEnv := os.Getenv(configEnvVar)
			defer os.Setenv(configEnvVar, originalEnv)

			if tt.configData != "" {
				// Write test config file
				if err := os.WriteFile(tt.configPath, []byte(tt.configData), 0644); err != nil {
					t.Fatalf("Failed to create test config file: %v", err)
				}
			}

			// Set config path environment variable
			os.Setenv(configEnvVar, tt.configPath)

			// Test LoadAppConfig
			result, err := LoadAppConfig()

			if tt.expectError {
				if err == nil {
					t.Errorf("Expected an error, but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			// Compare results
			if len(result) != len(tt.expected) {
				t.Errorf("Config length mismatch: got %d, want %d", len(result), len(tt.expected))
				return
			}

			for login, expectedAccount := range tt.expected {
				gotAccount, exists := result[login]
				if !exists {
					t.Errorf("Expected login %s to exist in result", login)
					continue
				}

				if (gotAccount.Azure == nil) != (expectedAccount.Azure == nil) {
					t.Errorf("Azure config mismatch for login %s: got nil=%v, want nil=%v",
						login, gotAccount.Azure == nil, expectedAccount.Azure == nil)
					continue
				}

				if gotAccount.Azure != nil && expectedAccount.Azure != nil {
					if gotAccount.Azure.Subscription != expectedAccount.Azure.Subscription {
						t.Errorf("Subscription mismatch for login %s: got %s, want %s",
							login, gotAccount.Azure.Subscription, expectedAccount.Azure.Subscription)
					}
				}
			}
		})
	}
}

func TestSaveAppConfig(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")

	// Set up test environment
	originalEnv := os.Getenv(configEnvVar)
	defer os.Setenv(configEnvVar, originalEnv)
	os.Setenv(configEnvVar, configPath)

	config := AppConfig{
		"user1": AccountConfig{
			Azure: &AzureConfig{
				Subscription: "sub123",
			},
		},
		"user2": AccountConfig{
			Azure: &AzureConfig{
				Subscription: "sub456",
			},
		},
	}

	// Save config
	err := SaveAppConfig(config)
	if err != nil {
		t.Fatalf("Failed to save config: %v", err)
	}

	// Read the file and verify
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("Failed to read saved config file: %v", err)
	}

	var savedConfig AppConfig
	if err := json.Unmarshal(data, &savedConfig); err != nil {
		t.Fatalf("Failed to unmarshal saved config: %v", err)
	}

	// Compare
	if len(savedConfig) != len(config) {
		t.Errorf("Saved config length mismatch: got %d, want %d", len(savedConfig), len(config))
		return
	}

	for login, expectedAccount := range config {
		gotAccount, exists := savedConfig[login]
		if !exists {
			t.Errorf("Expected login %s to exist in saved config", login)
			continue
		}

		if (gotAccount.Azure == nil) != (expectedAccount.Azure == nil) {
			t.Errorf("Azure config mismatch for login %s in saved config", login)
			continue
		}

		if gotAccount.Azure != nil && expectedAccount.Azure != nil {
			if gotAccount.Azure.Subscription != expectedAccount.Azure.Subscription {
				t.Errorf("Subscription mismatch for login %s in saved config: got %s, want %s",
					login, gotAccount.Azure.Subscription, expectedAccount.Azure.Subscription)
			}
		}
	}
}