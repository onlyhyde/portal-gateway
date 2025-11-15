package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/portal-project/portal-gateway/portal/middleware"
)

// TestLoadLeaseRateLimitConfig tests loading lease rate limit configuration from file
func TestLoadLeaseRateLimitConfig(t *testing.T) {
	// Create temporary config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "rate-limits.yaml")

	configContent := `default_rate: 50.0
default_burst: 100
leases:
  - lease_id: "mcp-*"
    requests_per_second: 50.0
    burst_size: 100
  - lease_id: "n8n-*"
    requests_per_second: 200.0
    burst_size: 400
  - lease_id: "exact-lease"
    requests_per_second: 100.0
    burst_size: 200
`

	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to create config file: %v", err)
	}

	// Load configuration
	config, err := LoadLeaseRateLimitConfig(configPath)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Verify default settings
	if config.DefaultRate != 50.0 {
		t.Errorf("Expected DefaultRate 50.0, got %f", config.DefaultRate)
	}

	if config.DefaultBurst != 100 {
		t.Errorf("Expected DefaultBurst 100, got %d", config.DefaultBurst)
	}

	// Verify rules are loaded
	rules := config.ListRules()
	if len(rules) != 3 {
		t.Errorf("Expected 3 rules, got %d", len(rules))
	}

	// Verify exact match rule
	exactRule := config.GetRule("exact-lease")
	if exactRule == nil {
		t.Fatal("Expected to find exact-lease rule")
	}
	if exactRule.RequestsPerSecond != 100.0 {
		t.Errorf("Expected rate 100.0, got %f", exactRule.RequestsPerSecond)
	}

	// Verify wildcard matching
	mcpRule := config.GetRule("mcp-server-1")
	if mcpRule == nil {
		t.Fatal("Expected to find wildcard rule for mcp-server-1")
	}
	if mcpRule.RequestsPerSecond != 50.0 {
		t.Errorf("Expected rate 50.0, got %f", mcpRule.RequestsPerSecond)
	}

	// Verify default fallback
	rate, burst := config.GetRateLimit("unknown-lease")
	if rate != 50.0 || burst != 100 {
		t.Errorf("Expected default rate 50.0/100, got %f/%d", rate, burst)
	}
}

// TestLoadLeaseRateLimitConfigFileNotFound tests error handling for missing file
func TestLoadLeaseRateLimitConfigFileNotFound(t *testing.T) {
	_, err := LoadLeaseRateLimitConfig("/nonexistent/config.yaml")
	if err == nil {
		t.Fatal("Expected error for non-existent file, got nil")
	}
}

// TestLoadLeaseRateLimitConfigEmptyPath tests error handling for empty path
func TestLoadLeaseRateLimitConfigEmptyPath(t *testing.T) {
	_, err := LoadLeaseRateLimitConfig("")
	if err == nil {
		t.Fatal("Expected error for empty path, got nil")
	}
}

// TestLoadLeaseRateLimitConfigInvalidYAML tests error handling for invalid YAML
func TestLoadLeaseRateLimitConfigInvalidYAML(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "invalid.yaml")

	invalidContent := `default_rate: 50.0
leases:
  - lease_id: "mcp-*"
    invalid_field: [this is not valid yaml syntax
`

	if err := os.WriteFile(configPath, []byte(invalidContent), 0644); err != nil {
		t.Fatalf("Failed to create config file: %v", err)
	}

	_, err := LoadLeaseRateLimitConfig(configPath)
	if err == nil {
		t.Fatal("Expected error for invalid YAML, got nil")
	}
}

// TestLoadLeaseRateLimitConfigInvalidRule tests error handling for invalid rules
func TestLoadLeaseRateLimitConfigInvalidRule(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "invalid-rule.yaml")

	invalidContent := `default_rate: 50.0
default_burst: 100
leases:
  - lease_id: ""
    requests_per_second: 100.0
`

	if err := os.WriteFile(configPath, []byte(invalidContent), 0644); err != nil {
		t.Fatalf("Failed to create config file: %v", err)
	}

	_, err := LoadLeaseRateLimitConfig(configPath)
	if err == nil {
		t.Fatal("Expected error for invalid rule, got nil")
	}
}

// TestLoadLeaseRateLimitConfigDuplicateLeaseID tests error handling for duplicate lease IDs
func TestLoadLeaseRateLimitConfigDuplicateLeaseID(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "duplicate.yaml")

	duplicateContent := `default_rate: 50.0
default_burst: 100
leases:
  - lease_id: "test-lease"
    requests_per_second: 100.0
  - lease_id: "test-lease"
    requests_per_second: 200.0
`

	if err := os.WriteFile(configPath, []byte(duplicateContent), 0644); err != nil {
		t.Fatalf("Failed to create config file: %v", err)
	}

	_, err := LoadLeaseRateLimitConfig(configPath)
	if err == nil {
		t.Fatal("Expected error for duplicate lease ID, got nil")
	}
	if err != middleware.ErrLeaseRuleDuplicate && !contains(err.Error(), "already exists") {
		t.Errorf("Expected duplicate error, got: %v", err)
	}
}

// TestLoadLeaseRateLimitConfigFromEnv tests loading from environment variable
func TestLoadLeaseRateLimitConfigFromEnv(t *testing.T) {
	// Create temporary config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "rate-limits.yaml")

	configContent := `default_rate: 50.0
default_burst: 100
leases:
  - lease_id: "test-*"
    requests_per_second: 75.0
    burst_size: 150
`

	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to create config file: %v", err)
	}

	// Set environment variable
	os.Setenv("LEASE_RATE_LIMIT_CONFIG_PATH", configPath)
	defer os.Unsetenv("LEASE_RATE_LIMIT_CONFIG_PATH")

	// Load configuration
	config, err := LoadLeaseRateLimitConfigFromEnv()
	if err != nil {
		t.Fatalf("Failed to load config from env: %v", err)
	}

	if config.DefaultRate != 50.0 {
		t.Errorf("Expected DefaultRate 50.0, got %f", config.DefaultRate)
	}
}

// TestLoadLeaseRateLimitConfigFromEnvNotSet tests error when env var not set
func TestLoadLeaseRateLimitConfigFromEnvNotSet(t *testing.T) {
	os.Unsetenv("LEASE_RATE_LIMIT_CONFIG_PATH")

	_, err := LoadLeaseRateLimitConfigFromEnv()
	if err == nil {
		t.Fatal("Expected error when env var not set, got nil")
	}
}

// contains checks if a string contains a substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && findSubstring(s, substr))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
