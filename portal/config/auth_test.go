package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestNewAuthConfigLoader tests creating a new configuration loader
func TestNewAuthConfigLoader(t *testing.T) {
	loader := NewAuthConfigLoader("test-config.yaml")
	if loader == nil {
		t.Fatal("NewAuthConfigLoader returned nil")
	}

	if loader.filePath != "test-config.yaml" {
		t.Errorf("Expected filePath 'test-config.yaml', got %q", loader.filePath)
	}

	if loader.authConfig == nil {
		t.Fatal("authConfig is nil")
	}
}

// TestLoadConfigFile tests loading a configuration file
func TestLoadConfigFile(t *testing.T) {
	// Create temporary directory for test files
	tmpDir := t.TempDir()

	tests := []struct {
		name        string
		configData  string
		wantErr     bool
		errContains string
		validate    func(*testing.T, *AuthConfigLoader)
	}{
		{
			name: "valid configuration",
			configData: `
api_keys:
  - key_id: "test_key_1"
    key: "sk_live_1234567890abcdef"
    scopes:
      - "read"
      - "write"
  - key_id: "test_key_2"
    key: "sk_test_abcdef1234567890"
    scopes:
      - "read"
`,
			wantErr: false,
			validate: func(t *testing.T, loader *AuthConfigLoader) {
				config := loader.GetAuthConfig()
				if len(config.APIKeys) != 2 {
					t.Errorf("Expected 2 API keys, got %d", len(config.APIKeys))
				}
			},
		},
		{
			name: "valid configuration with expiration",
			configData: `
api_keys:
  - key_id: "expiring_key"
    key: "sk_live_expiring1234567890"
    scopes:
      - "read"
    expires_at: "2025-12-31T23:59:59Z"
`,
			wantErr: false,
			validate: func(t *testing.T, loader *AuthConfigLoader) {
				config := loader.GetAuthConfig()
				key, exists := config.APIKeys["expiring_key"]
				if !exists {
					t.Fatal("Expected 'expiring_key' to exist")
				}
				if key.ExpiresAt == nil {
					t.Fatal("Expected ExpiresAt to be set")
				}
			},
		},
		{
			name:        "empty configuration",
			configData:  `api_keys: []`,
			wantErr:     true,
			errContains: "no API keys configured",
		},
		{
			name: "duplicate key IDs",
			configData: `
api_keys:
  - key_id: "duplicate_key"
    key: "sk_live_1234567890abcdef"
    scopes:
      - "read"
  - key_id: "duplicate_key"
    key: "sk_live_different_key"
    scopes:
      - "write"
`,
			wantErr:     true,
			errContains: "duplicate",
		},
		{
			name: "empty key ID",
			configData: `
api_keys:
  - key_id: ""
    key: "sk_live_1234567890abcdef"
    scopes:
      - "read"
`,
			wantErr:     true,
			errContains: "cannot be empty",
		},
		{
			name: "empty key value",
			configData: `
api_keys:
  - key_id: "test_key"
    key: ""
    scopes:
      - "read"
`,
			wantErr:     true,
			errContains: "cannot be empty",
		},
		{
			name: "invalid expiration date format",
			configData: `
api_keys:
  - key_id: "test_key"
    key: "sk_live_1234567890abcdef"
    scopes:
      - "read"
    expires_at: "invalid-date"
`,
			wantErr:     true,
			errContains: "invalid expiration date",
		},
		{
			name:        "invalid YAML",
			configData:  `invalid: yaml: [[[`,
			wantErr:     true,
			errContains: "invalid configuration format",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary config file
			configPath := filepath.Join(tmpDir, tt.name+".yaml")
			if err := os.WriteFile(configPath, []byte(tt.configData), 0600); err != nil {
				t.Fatalf("Failed to create test config file: %v", err)
			}

			// Load configuration
			loader := NewAuthConfigLoader(configPath)
			err := loader.Load()

			if tt.wantErr {
				if err == nil {
					t.Fatal("Expected error, got nil")
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("Expected error containing %q, got %q", tt.errContains, err.Error())
				}
				return
			}

			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if tt.validate != nil {
				tt.validate(t, loader)
			}
		})
	}
}

// TestLoadConfigFileNotFound tests loading a non-existent configuration file
func TestLoadConfigFileNotFound(t *testing.T) {
	loader := NewAuthConfigLoader("/nonexistent/path/config.yaml")
	err := loader.Load()

	if err == nil {
		t.Fatal("Expected error for non-existent file, got nil")
	}

	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("Expected 'not found' error, got: %v", err)
	}
}

// TestLoadFromFile tests the convenience function
func TestLoadFromFile(t *testing.T) {
	tmpDir := t.TempDir()

	configData := `
api_keys:
  - key_id: "test_key"
    key: "sk_live_1234567890abcdef"
    scopes:
      - "read"
`

	configPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(configData), 0600); err != nil {
		t.Fatalf("Failed to create test config file: %v", err)
	}

	config, err := LoadFromFile(configPath)
	if err != nil {
		t.Fatalf("LoadFromFile failed: %v", err)
	}

	if config == nil {
		t.Fatal("Expected non-nil config")
	}

	if len(config.APIKeys) != 1 {
		t.Errorf("Expected 1 API key, got %d", len(config.APIKeys))
	}
}

// TestLoadFromEnv tests loading configuration from environment variable
func TestLoadFromEnv(t *testing.T) {
	tmpDir := t.TempDir()

	configData := `
api_keys:
  - key_id: "env_test_key"
    key: "sk_live_env1234567890"
    scopes:
      - "read"
`

	configPath := filepath.Join(tmpDir, "env-config.yaml")
	if err := os.WriteFile(configPath, []byte(configData), 0600); err != nil {
		t.Fatalf("Failed to create test config file: %v", err)
	}

	// Test with environment variable set
	t.Setenv("AUTH_CONFIG_PATH", configPath)

	config, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv failed: %v", err)
	}

	if config == nil {
		t.Fatal("Expected non-nil config")
	}

	// Test without environment variable
	os.Unsetenv("AUTH_CONFIG_PATH")

	_, err = LoadFromEnv()
	if err == nil {
		t.Fatal("Expected error when AUTH_CONFIG_PATH is not set, got nil")
	}
}

// TestReload tests reloading configuration
func TestReload(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "reload-config.yaml")

	// Initial configuration
	initialConfig := `
api_keys:
  - key_id: "initial_key"
    key: "sk_live_initial1234567890"
    scopes:
      - "read"
`

	if err := os.WriteFile(configPath, []byte(initialConfig), 0600); err != nil {
		t.Fatalf("Failed to create initial config: %v", err)
	}

	loader := NewAuthConfigLoader(configPath)
	if err := loader.Load(); err != nil {
		t.Fatalf("Failed to load initial config: %v", err)
	}

	// Verify initial configuration
	config := loader.GetAuthConfig()
	if len(config.APIKeys) != 1 {
		t.Fatalf("Expected 1 API key initially, got %d", len(config.APIKeys))
	}

	// Update configuration
	updatedConfig := `
api_keys:
  - key_id: "updated_key_1"
    key: "sk_live_updated11234567890"
    scopes:
      - "read"
  - key_id: "updated_key_2"
    key: "sk_live_updated21234567890"
    scopes:
      - "write"
`

	// Wait a bit to ensure file modification time changes
	time.Sleep(10 * time.Millisecond)

	if err := os.WriteFile(configPath, []byte(updatedConfig), 0600); err != nil {
		t.Fatalf("Failed to update config: %v", err)
	}

	// Reload configuration
	if err := loader.Reload(); err != nil {
		t.Fatalf("Failed to reload config: %v", err)
	}

	// Verify updated configuration
	config = loader.GetAuthConfig()
	if len(config.APIKeys) != 2 {
		t.Errorf("Expected 2 API keys after reload, got %d", len(config.APIKeys))
	}

	if _, exists := config.APIKeys["updated_key_1"]; !exists {
		t.Error("Expected 'updated_key_1' to exist after reload")
	}

	if _, exists := config.APIKeys["updated_key_2"]; !exists {
		t.Error("Expected 'updated_key_2' to exist after reload")
	}
}

// TestValidateConfig tests configuration validation
func TestValidateConfig(t *testing.T) {
	loader := NewAuthConfigLoader("dummy.yaml")

	tests := []struct {
		name        string
		config      *AuthConfigFile
		wantErr     bool
		errContains string
	}{
		{
			name: "valid configuration",
			config: &AuthConfigFile{
				APIKeys: []APIKeyConfig{
					{
						KeyID:  "key1",
						Key:    "sk_live_1234567890",
						Scopes: []string{"read"},
					},
				},
			},
			wantErr: false,
		},
		{
			name:        "nil configuration",
			config:      nil,
			wantErr:     true,
			errContains: "cannot be nil",
		},
		{
			name: "empty API keys",
			config: &AuthConfigFile{
				APIKeys: []APIKeyConfig{},
			},
			wantErr:     true,
			errContains: "no API keys",
		},
		{
			name: "empty key ID",
			config: &AuthConfigFile{
				APIKeys: []APIKeyConfig{
					{
						KeyID: "",
						Key:   "sk_live_1234567890",
					},
				},
			},
			wantErr:     true,
			errContains: "cannot be empty",
		},
		{
			name: "empty key value",
			config: &AuthConfigFile{
				APIKeys: []APIKeyConfig{
					{
						KeyID: "key1",
						Key:   "",
					},
				},
			},
			wantErr:     true,
			errContains: "cannot be empty",
		},
		{
			name: "duplicate key IDs",
			config: &AuthConfigFile{
				APIKeys: []APIKeyConfig{
					{
						KeyID: "duplicate",
						Key:   "sk_live_1111111111",
					},
					{
						KeyID: "duplicate",
						Key:   "sk_live_2222222222",
					},
				},
			},
			wantErr:     true,
			errContains: "duplicate",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := loader.validateConfig(tt.config)

			if tt.wantErr {
				if err == nil {
					t.Fatal("Expected error, got nil")
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("Expected error containing %q, got %q", tt.errContains, err.Error())
				}
				return
			}

			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
		})
	}
}

// TestParseAPIKey tests parsing API key configuration
func TestParseAPIKey(t *testing.T) {
	loader := NewAuthConfigLoader("dummy.yaml")

	tests := []struct {
		name        string
		config      *APIKeyConfig
		wantErr     bool
		errContains string
		validate    func(*testing.T, *APIKeyConfig)
	}{
		{
			name: "valid API key without expiration",
			config: &APIKeyConfig{
				KeyID:  "test_key",
				Key:    "sk_live_1234567890",
				Scopes: []string{"read", "write"},
			},
			wantErr: false,
			validate: func(t *testing.T, config *APIKeyConfig) {
				if config.KeyID != "test_key" {
					t.Errorf("Expected KeyID 'test_key', got %q", config.KeyID)
				}
				if len(config.Scopes) != 2 {
					t.Errorf("Expected 2 scopes, got %d", len(config.Scopes))
				}
			},
		},
		{
			name: "valid API key with expiration",
			config: &APIKeyConfig{
				KeyID:     "expiring_key",
				Key:       "sk_live_expiring1234567890",
				Scopes:    []string{"read"},
				ExpiresAt: "2025-12-31T23:59:59Z",
			},
			wantErr: false,
		},
		{
			name:        "nil configuration",
			config:      nil,
			wantErr:     true,
			errContains: "cannot be nil",
		},
		{
			name: "invalid expiration date",
			config: &APIKeyConfig{
				KeyID:     "test_key",
				Key:       "sk_live_1234567890",
				Scopes:    []string{"read"},
				ExpiresAt: "not-a-date",
			},
			wantErr:     true,
			errContains: "invalid expiration date",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			apiKey, err := loader.parseAPIKey(tt.config)

			if tt.wantErr {
				if err == nil {
					t.Fatal("Expected error, got nil")
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("Expected error containing %q, got %q", tt.errContains, err.Error())
				}
				return
			}

			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if apiKey == nil {
				t.Fatal("Expected non-nil API key")
			}

			if tt.validate != nil {
				tt.validate(t, tt.config)
			}
		})
	}
}

// TestGetAuthConfig tests thread-safe access to auth configuration
func TestGetAuthConfig(t *testing.T) {
	loader := NewAuthConfigLoader("dummy.yaml")

	config1 := loader.GetAuthConfig()
	config2 := loader.GetAuthConfig()

	if config1 != config2 {
		t.Error("Expected same config instance")
	}
}
