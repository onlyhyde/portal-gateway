package tls

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Helper function to generate test certificates
func generateTestCertificate(t *testing.T) (certPEM, keyPEM []byte) {
	t.Helper()

	// Generate private key
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("Failed to generate private key: %v", err)
	}

	// Create certificate template
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"Test Org"},
			CommonName:   "test.example.com",
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"test.example.com", "localhost"},
	}

	// Create certificate
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("Failed to create certificate: %v", err)
	}

	// Encode certificate to PEM
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	// Encode private key to PEM
	privBytes, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("Failed to marshal private key: %v", err)
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: privBytes})

	return certPEM, keyPEM
}

// TestNewConfig tests creating a new TLS configuration
func TestNewConfig(t *testing.T) {
	config := NewConfig()
	if config == nil {
		t.Fatal("NewConfig returned nil")
	}

	if config.ClientAuth != tls.NoClientCert {
		t.Errorf("Expected ClientAuth to be NoClientCert, got %v", config.ClientAuth)
	}
}

// TestLoadCertificate tests loading certificates from files
func TestLoadCertificate(t *testing.T) {
	tmpDir := t.TempDir()

	// Generate test certificate
	certPEM, keyPEM := generateTestCertificate(t)

	// Write certificate and key to files
	certFile := filepath.Join(tmpDir, "cert.pem")
	keyFile := filepath.Join(tmpDir, "key.pem")

	if err := os.WriteFile(certFile, certPEM, 0600); err != nil {
		t.Fatalf("Failed to write cert file: %v", err)
	}

	if err := os.WriteFile(keyFile, keyPEM, 0600); err != nil {
		t.Fatalf("Failed to write key file: %v", err)
	}

	// Test loading certificate
	config := NewConfig()
	config.CertFile = certFile
	config.KeyFile = keyFile

	err := config.LoadCertificate()
	if err != nil {
		t.Fatalf("Failed to load certificate: %v", err)
	}

	tlsConfig := config.GetTLSConfig()
	if tlsConfig == nil {
		t.Fatal("TLS config is nil")
	}

	if len(tlsConfig.Certificates) == 0 {
		t.Fatal("No certificates loaded")
	}

	if tlsConfig.MinVersion != tls.VersionTLS12 {
		t.Errorf("Expected MinVersion TLS 1.2, got %v", tlsConfig.MinVersion)
	}
}

// TestLoadCertificateErrors tests error cases for loading certificates
func TestLoadCertificateErrors(t *testing.T) {
	tests := []struct {
		name     string
		certFile string
		keyFile  string
	}{
		{
			name:     "missing certificate file",
			certFile: "",
			keyFile:  "",
		},
		{
			name:     "nonexistent certificate file",
			certFile: "/nonexistent/cert.pem",
			keyFile:  "/nonexistent/key.pem",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := NewConfig()
			config.CertFile = tt.certFile
			config.KeyFile = tt.keyFile

			err := config.LoadCertificate()
			if err == nil {
				t.Fatal("Expected error, got nil")
			}
		})
	}
}

// TestConfigureMTLS tests mTLS configuration
func TestConfigureMTLS(t *testing.T) {
	tmpDir := t.TempDir()

	// Generate CA certificate
	caCertPEM, caKeyPEM := generateTestCertificate(t)
	caFile := filepath.Join(tmpDir, "ca.pem")
	if err := os.WriteFile(caFile, caCertPEM, 0600); err != nil {
		t.Fatalf("Failed to write CA file: %v", err)
	}

	// Generate server certificate
	certPEM, keyPEM := generateTestCertificate(t)
	certFile := filepath.Join(tmpDir, "cert.pem")
	keyFile := filepath.Join(tmpDir, "key.pem")

	if err := os.WriteFile(certFile, certPEM, 0600); err != nil {
		t.Fatalf("Failed to write cert file: %v", err)
	}

	if err := os.WriteFile(keyFile, keyPEM, 0600); err != nil {
		t.Fatalf("Failed to write key file: %v", err)
	}

	// Test mTLS configuration
	config := NewConfig()
	config.CertFile = certFile
	config.KeyFile = keyFile
	config.CAFile = caFile
	config.EnableMTLS = true
	config.VerifyClientCert = true

	err := config.LoadCertificate()
	if err != nil {
		t.Fatalf("Failed to load certificate with mTLS: %v", err)
	}

	tlsConfig := config.GetTLSConfig()
	if tlsConfig == nil {
		t.Fatal("TLS config is nil")
	}

	if tlsConfig.ClientAuth != tls.RequireAndVerifyClientCert {
		t.Errorf("Expected RequireAndVerifyClientCert, got %v", tlsConfig.ClientAuth)
	}

	if tlsConfig.ClientCAs == nil {
		t.Error("ClientCAs should not be nil for mTLS")
	}

	// Keep caKeyPEM to avoid unused variable error
	_ = caKeyPEM
}

// TestSetupACME tests ACME configuration (without actually connecting to Let's Encrypt)
func TestSetupACME(t *testing.T) {
	tmpDir := t.TempDir()

	config := NewConfig()
	config.EnableACME = true
	config.ACMEDomains = []string{"example.com", "www.example.com"}
	config.ACMEEmail = "admin@example.com"
	config.ACMECacheDir = filepath.Join(tmpDir, "autocert-cache")

	err := config.SetupACME()
	if err != nil {
		t.Fatalf("Failed to setup ACME: %v", err)
	}

	tlsConfig := config.GetTLSConfig()
	if tlsConfig == nil {
		t.Fatal("TLS config is nil")
	}

	certManager := config.GetCertManager()
	if certManager == nil {
		t.Fatal("Cert manager is nil")
	}

	// Verify cache directory was created
	if _, err := os.Stat(config.ACMECacheDir); os.IsNotExist(err) {
		t.Error("ACME cache directory was not created")
	}
}

// TestSetupACMEErrors tests ACME configuration error cases
func TestSetupACMEErrors(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(*Config)
		wantErr error
	}{
		{
			name: "ACME not enabled",
			setup: func(c *Config) {
				c.EnableACME = false
			},
			wantErr: nil, // Should return a generic error
		},
		{
			name: "no domains specified",
			setup: func(c *Config) {
				c.EnableACME = true
				c.ACMEDomains = []string{}
			},
			wantErr: ErrACMENotConfigured,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := NewConfig()
			tt.setup(config)

			err := config.SetupACME()
			if err == nil {
				t.Fatal("Expected error, got nil")
			}
		})
	}
}

// TestValidateCertificate tests certificate validation
func TestValidateCertificate(t *testing.T) {
	tmpDir := t.TempDir()

	// Generate valid certificate
	certPEM, keyPEM := generateTestCertificate(t)
	certFile := filepath.Join(tmpDir, "cert.pem")
	keyFile := filepath.Join(tmpDir, "key.pem")

	if err := os.WriteFile(certFile, certPEM, 0600); err != nil {
		t.Fatalf("Failed to write cert file: %v", err)
	}

	if err := os.WriteFile(keyFile, keyPEM, 0600); err != nil {
		t.Fatalf("Failed to write key file: %v", err)
	}

	config := NewConfig()
	config.CertFile = certFile
	config.KeyFile = keyFile

	if err := config.LoadCertificate(); err != nil {
		t.Fatalf("Failed to load certificate: %v", err)
	}

	// Validate certificate
	err := config.ValidateCertificate()
	if err != nil {
		t.Errorf("Certificate validation failed: %v", err)
	}
}

// TestGetCertificateInfo tests retrieving certificate information
func TestGetCertificateInfo(t *testing.T) {
	tmpDir := t.TempDir()

	certPEM, keyPEM := generateTestCertificate(t)
	certFile := filepath.Join(tmpDir, "cert.pem")
	keyFile := filepath.Join(tmpDir, "key.pem")

	if err := os.WriteFile(certFile, certPEM, 0600); err != nil {
		t.Fatalf("Failed to write cert file: %v", err)
	}

	if err := os.WriteFile(keyFile, keyPEM, 0600); err != nil {
		t.Fatalf("Failed to write key file: %v", err)
	}

	config := NewConfig()
	config.CertFile = certFile
	config.KeyFile = keyFile

	if err := config.LoadCertificate(); err != nil {
		t.Fatalf("Failed to load certificate: %v", err)
	}

	info, err := config.GetCertificateInfo()
	if err != nil {
		t.Fatalf("Failed to get certificate info: %v", err)
	}

	if info.Subject == "" {
		t.Error("Subject should not be empty")
	}

	if info.Issuer == "" {
		t.Error("Issuer should not be empty")
	}

	if len(info.DNSNames) == 0 {
		t.Error("DNSNames should not be empty")
	}

	// Check if certificate is not expired
	if info.IsExpired() {
		t.Error("Certificate should not be expired")
	}

	// Check expiration duration
	expiresIn := info.ExpiresIn()
	if expiresIn <= 0 {
		t.Error("Expires in should be positive")
	}
}

// TestReloadCertificate tests certificate reloading
func TestReloadCertificate(t *testing.T) {
	tmpDir := t.TempDir()

	certPEM, keyPEM := generateTestCertificate(t)
	certFile := filepath.Join(tmpDir, "cert.pem")
	keyFile := filepath.Join(tmpDir, "key.pem")

	if err := os.WriteFile(certFile, certPEM, 0600); err != nil {
		t.Fatalf("Failed to write cert file: %v", err)
	}

	if err := os.WriteFile(keyFile, keyPEM, 0600); err != nil {
		t.Fatalf("Failed to write key file: %v", err)
	}

	config := NewConfig()
	config.CertFile = certFile
	config.KeyFile = keyFile

	// Initial load
	if err := config.LoadCertificate(); err != nil {
		t.Fatalf("Failed to load certificate: %v", err)
	}

	// Reload
	err := config.ReloadCertificate()
	if err != nil {
		t.Errorf("Failed to reload certificate: %v", err)
	}
}

// Helper function to check if error is or wraps expected error
func isErrorOrWraps(err, target error) bool {
	if err == target {
		return true
	}
	// Simple check - in production, use errors.Is()
	return false
}
