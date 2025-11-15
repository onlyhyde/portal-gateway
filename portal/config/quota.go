package config

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/portal-project/portal-gateway/portal/quota"
)

// QuotaConfigFile represents the structure of the quota config file
type QuotaConfigFile struct {
	DefaultMonthlyRequests        int64           `yaml:"default_monthly_requests"`
	DefaultMonthlyBytes           int64           `yaml:"default_monthly_bytes"`
	DefaultConcurrentConnections  int             `yaml:"default_concurrent_connections"`
	Storage                       StorageConfig   `yaml:"storage"`
	Quotas                        []QuotaRule     `yaml:"quotas"`
}

// StorageConfig represents storage configuration
type StorageConfig struct {
	Type string `yaml:"type"`
	Path string `yaml:"path"`
}

// QuotaRule represents a single quota rule in config
type QuotaRule struct {
	KeyID                 string `yaml:"key_id"`
	MonthlyRequests       int64  `yaml:"monthly_requests"`
	MonthlyBytes          int64  `yaml:"monthly_bytes"`
	ConcurrentConnections int    `yaml:"concurrent_connections"`
}

// LoadQuotaConfig loads quota configuration from a file
func LoadQuotaConfig(filePath string) (*quota.Manager, error) {
	if filePath == "" {
		return nil, errors.New("quota config file path cannot be empty")
	}

	// Read configuration file
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("quota config file not found: %s", filePath)
		}
		return nil, fmt.Errorf("failed to read quota config file: %w", err)
	}

	// Parse YAML
	var configFile QuotaConfigFile
	if err := yaml.Unmarshal(data, &configFile); err != nil {
		return nil, fmt.Errorf("invalid quota config format: %w", err)
	}

	// Validate storage configuration
	if configFile.Storage.Type == "" {
		configFile.Storage.Type = "sqlite"
	}
	if configFile.Storage.Type != "sqlite" {
		return nil, fmt.Errorf("unsupported storage type: %s (only 'sqlite' is supported)", configFile.Storage.Type)
	}
	if configFile.Storage.Path == "" {
		return nil, errors.New("storage path cannot be empty")
	}

	// Create storage
	storage, err := quota.NewSQLiteStorage(configFile.Storage.Path)
	if err != nil {
		return nil, fmt.Errorf("failed to create storage: %w", err)
	}

	// Create quota manager
	manager := quota.NewManager(
		storage,
		configFile.DefaultMonthlyRequests,
		configFile.DefaultMonthlyBytes,
		configFile.DefaultConcurrentConnections,
	)

	// Add quota rules
	for _, rule := range configFile.Quotas {
		limit := &quota.QuotaLimit{
			KeyID:                 rule.KeyID,
			MonthlyRequestLimit:   rule.MonthlyRequests,
			MonthlyBytesLimit:     rule.MonthlyBytes,
			ConcurrentConnections: rule.ConcurrentConnections,
		}

		if err := manager.SetLimit(limit); err != nil {
			// Clean up on error
			storage.Close()
			return nil, fmt.Errorf("failed to set quota limit for key %s: %w", rule.KeyID, err)
		}
	}

	return manager, nil
}

// LoadQuotaConfigFromEnv loads configuration from environment variable
func LoadQuotaConfigFromEnv() (*quota.Manager, error) {
	configPath := os.Getenv("QUOTA_CONFIG_PATH")
	if configPath == "" {
		return nil, errors.New("QUOTA_CONFIG_PATH environment variable not set")
	}

	return LoadQuotaConfig(configPath)
}

// matchWildcard checks if a string matches a wildcard pattern
// Only supports trailing wildcard (e.g., "prefix-*")
func matchWildcard(pattern, str string) bool {
	if !strings.HasSuffix(pattern, "*") {
		return pattern == str
	}

	prefix := strings.TrimSuffix(pattern, "*")
	return strings.HasPrefix(str, prefix)
}
