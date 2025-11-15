package config

import (
	"crypto/tls"
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	portalTLS "github.com/portal-project/portal-gateway/portal/tls"
)

// TLSConfigFile represents the structure of the TLS configuration file
type TLSConfigFile struct {
	CertFile           string   `yaml:"cert_file"`
	KeyFile            string   `yaml:"key_file"`
	CAFile             string   `yaml:"ca_file"`
	EnableACME         bool     `yaml:"enable_acme"`
	ACMEDomains        []string `yaml:"acme_domains"`
	ACMEEmail          string   `yaml:"acme_email"`
	ACMECacheDir       string   `yaml:"acme_cache_dir"`
	EnableMTLS         bool     `yaml:"enable_mtls"`
	VerifyClientCert   bool     `yaml:"verify_client_cert"`
}

// LoadTLSConfig loads TLS configuration from a file
func LoadTLSConfig(filePath string) (*portalTLS.Config, error) {
	if filePath == "" {
		return nil, errors.New("TLS config file path cannot be empty")
	}

	// Read configuration file
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("TLS config file not found: %s", filePath)
		}
		return nil, fmt.Errorf("failed to read TLS config file: %w", err)
	}

	// Parse YAML
	var configFile TLSConfigFile
	if err := yaml.Unmarshal(data, &configFile); err != nil {
		return nil, fmt.Errorf("invalid TLS config format: %w", err)
	}

	// Create TLS configuration
	tlsConfig := portalTLS.NewConfig()
	tlsConfig.CertFile = configFile.CertFile
	tlsConfig.KeyFile = configFile.KeyFile
	tlsConfig.CAFile = configFile.CAFile
	tlsConfig.EnableACME = configFile.EnableACME
	tlsConfig.ACMEDomains = configFile.ACMEDomains
	tlsConfig.ACMEEmail = configFile.ACMEEmail
	tlsConfig.ACMECacheDir = configFile.ACMECacheDir
	tlsConfig.EnableMTLS = configFile.EnableMTLS
	tlsConfig.VerifyClientCert = configFile.VerifyClientCert

	// Set client authentication type for mTLS
	if configFile.EnableMTLS {
		if configFile.VerifyClientCert {
			tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
		} else {
			tlsConfig.ClientAuth = tls.RequestClientCert
		}
	}

	// Validate configuration
	if err := validateTLSConfig(tlsConfig); err != nil {
		return nil, fmt.Errorf("TLS config validation failed: %w", err)
	}

	// Load certificates
	if tlsConfig.EnableACME {
		if err := tlsConfig.SetupACME(); err != nil {
			return nil, fmt.Errorf("failed to setup ACME: %w", err)
		}
	} else {
		if err := tlsConfig.LoadCertificate(); err != nil {
			return nil, fmt.Errorf("failed to load certificate: %w", err)
		}
	}

	// Validate certificate
	if err := tlsConfig.ValidateCertificate(); err != nil {
		// Log warning but don't fail - certificate might be valid but expiring soon
		fmt.Fprintf(os.Stderr, "Certificate validation warning: %v\n", err)
	}

	return tlsConfig, nil
}

// validateTLSConfig performs validation on the TLS configuration
func validateTLSConfig(config *portalTLS.Config) error {
	if config == nil {
		return errors.New("TLS config cannot be nil")
	}

	// Validate ACME configuration
	if config.EnableACME {
		if len(config.ACMEDomains) == 0 {
			return errors.New("ACME domains cannot be empty when ACME is enabled")
		}

		// Validate email (optional but recommended)
		if config.ACMEEmail == "" {
			fmt.Fprintf(os.Stderr, "WARNING: ACME email not set - recommended for Let's Encrypt notifications\n")
		}
	} else {
		// Manual certificate configuration
		if config.CertFile == "" {
			return errors.New("cert_file is required when ACME is not enabled")
		}

		if config.KeyFile == "" {
			return errors.New("key_file is required when ACME is not enabled")
		}
	}

	// Validate mTLS configuration
	if config.EnableMTLS {
		if config.CAFile == "" {
			return errors.New("ca_file is required when mTLS is enabled")
		}
	}

	return nil
}

// LoadTLSConfigFromEnv loads TLS configuration from environment variable
func LoadTLSConfigFromEnv() (*portalTLS.Config, error) {
	configPath := os.Getenv("TLS_CONFIG_PATH")
	if configPath == "" {
		return nil, errors.New("TLS_CONFIG_PATH environment variable not set")
	}

	return LoadTLSConfig(configPath)
}
