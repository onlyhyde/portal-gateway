package config

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/portal-project/portal-gateway/portal/middleware"
)

// AuthConfigFile represents the structure of the auth configuration file
type AuthConfigFile struct {
	APIKeys []APIKeyConfig `yaml:"api_keys"`
}

// APIKeyConfig represents a single API key configuration
type APIKeyConfig struct {
	KeyID     string   `yaml:"key_id"`
	Key       string   `yaml:"key"`
	Scopes    []string `yaml:"scopes"`
	ExpiresAt string   `yaml:"expires_at,omitempty"` // RFC3339 format
}

// AuthConfigLoader handles loading and reloading of authentication configuration
type AuthConfigLoader struct {
	filePath   string
	authConfig *middleware.AuthConfig
	mu         sync.RWMutex
}

// Common errors
var (
	ErrConfigFileNotFound = errors.New("configuration file not found")
	ErrInvalidConfig      = errors.New("invalid configuration format")
	ErrEmptyAPIKeys       = errors.New("no API keys configured")
)

// NewAuthConfigLoader creates a new configuration loader
func NewAuthConfigLoader(filePath string) *AuthConfigLoader {
	return &AuthConfigLoader{
		filePath:   filePath,
		authConfig: middleware.NewAuthConfig(),
	}
}

// Load reads and parses the authentication configuration file
// Returns an error if the file cannot be read or parsed
func (l *AuthConfigLoader) Load() error {
	if l.filePath == "" {
		return errors.New("configuration file path cannot be empty")
	}

	// Read configuration file
	data, err := os.ReadFile(l.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %s", ErrConfigFileNotFound, l.filePath)
		}
		return fmt.Errorf("failed to read config file: %w", err)
	}

	// Parse YAML
	var configFile AuthConfigFile
	if err := yaml.Unmarshal(data, &configFile); err != nil {
		return fmt.Errorf("%w: %s", ErrInvalidConfig, err.Error())
	}

	// Validate configuration
	if err := l.validateConfig(&configFile); err != nil {
		return fmt.Errorf("configuration validation failed: %w", err)
	}

	// Create new auth config
	newConfig := middleware.NewAuthConfig()

	// Parse and add API keys
	for _, keyConfig := range configFile.APIKeys {
		apiKey, err := l.parseAPIKey(&keyConfig)
		if err != nil {
			return fmt.Errorf("failed to parse API key %s: %w", keyConfig.KeyID, err)
		}

		if err := newConfig.AddAPIKey(apiKey); err != nil {
			return fmt.Errorf("failed to add API key %s: %w", keyConfig.KeyID, err)
		}
	}

	// Replace old configuration with new one
	l.mu.Lock()
	l.authConfig = newConfig
	l.mu.Unlock()

	return nil
}

// validateConfig performs validation on the configuration file
func (l *AuthConfigLoader) validateConfig(config *AuthConfigFile) error {
	if config == nil {
		return errors.New("configuration cannot be nil")
	}

	if len(config.APIKeys) == 0 {
		return ErrEmptyAPIKeys
	}

	// Check for duplicate key IDs
	keyIDs := make(map[string]bool)
	for _, keyConfig := range config.APIKeys {
		if keyConfig.KeyID == "" {
			return errors.New("API key ID cannot be empty")
		}

		if keyIDs[keyConfig.KeyID] {
			return fmt.Errorf("duplicate API key ID: %s", keyConfig.KeyID)
		}
		keyIDs[keyConfig.KeyID] = true

		if keyConfig.Key == "" {
			return fmt.Errorf("API key value cannot be empty for key ID: %s", keyConfig.KeyID)
		}
	}

	return nil
}

// parseAPIKey converts a configuration API key to a middleware API key
func (l *AuthConfigLoader) parseAPIKey(config *APIKeyConfig) (*middleware.APIKey, error) {
	if config == nil {
		return nil, errors.New("API key configuration cannot be nil")
	}

	apiKey := &middleware.APIKey{
		Key:    config.Key,
		KeyID:  config.KeyID,
		Scopes: config.Scopes,
	}

	// Parse expiration date if provided
	if config.ExpiresAt != "" {
		expiresAt, err := time.Parse(time.RFC3339, config.ExpiresAt)
		if err != nil {
			return nil, fmt.Errorf("invalid expiration date format (expected RFC3339): %w", err)
		}
		apiKey.ExpiresAt = &expiresAt
	}

	return apiKey, nil
}

// Reload reloads the configuration from the file
// This can be called in response to a SIGHUP signal for zero-downtime config updates
func (l *AuthConfigLoader) Reload() error {
	return l.Load()
}

// GetAuthConfig returns the current authentication configuration
// This method is thread-safe
func (l *AuthConfigLoader) GetAuthConfig() *middleware.AuthConfig {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.authConfig
}

// LoadFromFile is a convenience function that creates a loader and loads the config
func LoadFromFile(filePath string) (*middleware.AuthConfig, error) {
	loader := NewAuthConfigLoader(filePath)
	if err := loader.Load(); err != nil {
		return nil, err
	}
	return loader.GetAuthConfig(), nil
}

// LoadFromEnv loads configuration from environment variables
// This is useful for containerized deployments where config files may not be ideal
func LoadFromEnv() (*middleware.AuthConfig, error) {
	configPath := os.Getenv("AUTH_CONFIG_PATH")
	if configPath == "" {
		return nil, errors.New("AUTH_CONFIG_PATH environment variable not set")
	}

	return LoadFromFile(configPath)
}

// WatchConfig watches the configuration file for changes and reloads automatically
// This function blocks and should be run in a goroutine
// Cancel the context to stop watching
func (l *AuthConfigLoader) WatchConfig(reloadInterval time.Duration) error {
	if reloadInterval <= 0 {
		reloadInterval = 30 * time.Second
	}

	ticker := time.NewTicker(reloadInterval)
	defer ticker.Stop()

	// Get initial file info
	lastModTime, err := l.getFileModTime()
	if err != nil {
		return fmt.Errorf("failed to get initial file modification time: %w", err)
	}

	for range ticker.C {
		modTime, err := l.getFileModTime()
		if err != nil {
			// File might have been temporarily deleted, continue watching
			continue
		}

		// Check if file was modified
		if modTime.After(lastModTime) {
			if err := l.Reload(); err != nil {
				// Log error but continue watching
				fmt.Fprintf(os.Stderr, "Failed to reload config: %v\n", err)
				continue
			}
			lastModTime = modTime
		}
	}

	return nil
}

// getFileModTime returns the modification time of the configuration file
func (l *AuthConfigLoader) getFileModTime() (time.Time, error) {
	info, err := os.Stat(l.filePath)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to stat config file: %w", err)
	}
	return info.ModTime(), nil
}
