package config

import (
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/portal-project/portal-gateway/portal/middleware"
)

// LeaseRateLimitConfigFile represents the structure of the lease rate limit config file
type LeaseRateLimitConfigFile struct {
	DefaultRate  float64                 `yaml:"default_rate"`
	DefaultBurst int                     `yaml:"default_burst"`
	Leases       []LeaseRateLimitRule    `yaml:"leases"`
}

// LeaseRateLimitRule represents a single lease rate limit rule in config
type LeaseRateLimitRule struct {
	LeaseID           string  `yaml:"lease_id"`
	RequestsPerSecond float64 `yaml:"requests_per_second"`
	BurstSize         int     `yaml:"burst_size"`
}

// LoadLeaseRateLimitConfig loads lease rate limit configuration from a file
func LoadLeaseRateLimitConfig(filePath string) (*middleware.LeaseRateLimitConfig, error) {
	if filePath == "" {
		return nil, errors.New("lease rate limit config file path cannot be empty")
	}

	// Read configuration file
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("lease rate limit config file not found: %s", filePath)
		}
		return nil, fmt.Errorf("failed to read lease rate limit config file: %w", err)
	}

	// Parse YAML
	var configFile LeaseRateLimitConfigFile
	if err := yaml.Unmarshal(data, &configFile); err != nil {
		return nil, fmt.Errorf("invalid lease rate limit config format: %w", err)
	}

	// Create configuration
	config := middleware.NewLeaseRateLimitConfig(configFile.DefaultRate, configFile.DefaultBurst)

	// Add lease-specific rules
	for _, rule := range configFile.Leases {
		middlewareRule := &middleware.LeaseRateLimitRule{
			LeaseID:           rule.LeaseID,
			RequestsPerSecond: rule.RequestsPerSecond,
			BurstSize:         rule.BurstSize,
		}

		if err := config.AddRule(middlewareRule); err != nil {
			return nil, fmt.Errorf("failed to add rule for lease %s: %w", rule.LeaseID, err)
		}
	}

	return config, nil
}

// LoadLeaseRateLimitConfigFromEnv loads configuration from environment variable
func LoadLeaseRateLimitConfigFromEnv() (*middleware.LeaseRateLimitConfig, error) {
	configPath := os.Getenv("LEASE_RATE_LIMIT_CONFIG_PATH")
	if configPath == "" {
		return nil, errors.New("LEASE_RATE_LIMIT_CONFIG_PATH environment variable not set")
	}

	return LoadLeaseRateLimitConfig(configPath)
}
