package tls

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"golang.org/x/crypto/acme/autocert"
)

// Config holds TLS configuration
type Config struct {
	// Certificate file paths
	CertFile string
	KeyFile  string
	CAFile   string // For mTLS client certificate validation

	// Let's Encrypt / ACME configuration
	EnableACME   bool
	ACMEDomains  []string
	ACMEEmail    string
	ACMECacheDir string

	// mTLS configuration
	EnableMTLS         bool
	ClientAuth         tls.ClientAuthType
	VerifyClientCert   bool

	mu         sync.RWMutex
	tlsConfig  *tls.Config
	certManager *autocert.Manager
}

// Common errors
var (
	ErrNoCertificate      = errors.New("no certificate configured")
	ErrInvalidCertificate = errors.New("invalid certificate or key")
	ErrInvalidCAFile      = errors.New("invalid CA certificate file")
	ErrACMENotConfigured  = errors.New("ACME not properly configured")
)

// NewConfig creates a new TLS configuration
func NewConfig() *Config {
	return &Config{
		ClientAuth: tls.NoClientCert,
	}
}

// LoadCertificate loads a TLS certificate from files
func (c *Config) LoadCertificate() error {
	if c.CertFile == "" || c.KeyFile == "" {
		return ErrNoCertificate
	}

	// Load certificate and key
	cert, err := tls.LoadX509KeyPair(c.CertFile, c.KeyFile)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrInvalidCertificate, err.Error())
	}

	// Create TLS configuration
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
		CipherSuites: []uint16{
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
		},
		PreferServerCipherSuites: true,
	}

	// Configure mTLS if enabled
	if c.EnableMTLS {
		if err := c.configureMTLS(tlsConfig); err != nil {
			return fmt.Errorf("failed to configure mTLS: %w", err)
		}
	}

	c.mu.Lock()
	c.tlsConfig = tlsConfig
	c.mu.Unlock()

	return nil
}

// configureMTLS sets up mutual TLS authentication
func (c *Config) configureMTLS(tlsConfig *tls.Config) error {
	if c.CAFile == "" {
		return errors.New("CA file is required for mTLS")
	}

	// Load CA certificate
	caCert, err := os.ReadFile(c.CAFile)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrInvalidCAFile, err.Error())
	}

	// Create CA certificate pool
	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM(caCert) {
		return fmt.Errorf("%w: failed to parse CA certificate", ErrInvalidCAFile)
	}

	// Configure client authentication
	tlsConfig.ClientCAs = caCertPool

	if c.VerifyClientCert {
		tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
	} else {
		tlsConfig.ClientAuth = c.ClientAuth
	}

	// Set up client certificate verification callback
	if c.VerifyClientCert {
		tlsConfig.VerifyPeerCertificate = func(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error {
			// Additional custom verification logic can be added here
			if len(verifiedChains) == 0 {
				return errors.New("no verified certificate chains")
			}
			return nil
		}
	}

	return nil
}

// SetupACME configures automatic certificate management with Let's Encrypt
func (c *Config) SetupACME() error {
	if !c.EnableACME {
		return errors.New("ACME is not enabled")
	}

	if len(c.ACMEDomains) == 0 {
		return fmt.Errorf("%w: no domains specified", ErrACMENotConfigured)
	}

	if c.ACMECacheDir == "" {
		c.ACMECacheDir = "./autocert-cache"
	}

	// Ensure cache directory exists
	if err := os.MkdirAll(c.ACMECacheDir, 0700); err != nil {
		return fmt.Errorf("failed to create ACME cache directory: %w", err)
	}

	// Create autocert manager
	certManager := &autocert.Manager{
		Prompt:      autocert.AcceptTOS,
		HostPolicy:  autocert.HostWhitelist(c.ACMEDomains...),
		Cache:       autocert.DirCache(c.ACMECacheDir),
		Email:       c.ACMEEmail,
		RenewBefore: 30 * 24 * time.Hour, // Renew 30 days before expiration
	}

	// Create TLS configuration for ACME
	tlsConfig := certManager.TLSConfig()
	tlsConfig.MinVersion = tls.VersionTLS12
	tlsConfig.CipherSuites = []uint16{
		tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
		tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
		tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
		tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
	}
	tlsConfig.PreferServerCipherSuites = true

	// Configure mTLS if enabled (Note: ACME and mTLS is an advanced use case)
	if c.EnableMTLS {
		if err := c.configureMTLS(tlsConfig); err != nil {
			return fmt.Errorf("failed to configure mTLS with ACME: %w", err)
		}
	}

	c.mu.Lock()
	c.tlsConfig = tlsConfig
	c.certManager = certManager
	c.mu.Unlock()

	return nil
}

// GetTLSConfig returns the current TLS configuration
// This method is thread-safe
func (c *Config) GetTLSConfig() *tls.Config {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.tlsConfig
}

// GetCertManager returns the autocert manager if ACME is enabled
func (c *Config) GetCertManager() *autocert.Manager {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.certManager
}

// ReloadCertificate reloads the certificate from disk
// This can be used for manual certificate rotation
func (c *Config) ReloadCertificate() error {
	return c.LoadCertificate()
}

// ValidateCertificate checks if the certificate is valid and not expired
func (c *Config) ValidateCertificate() error {
	c.mu.RLock()
	tlsConfig := c.tlsConfig
	c.mu.RUnlock()

	if tlsConfig == nil {
		return ErrNoCertificate
	}

	if len(tlsConfig.Certificates) == 0 {
		return ErrNoCertificate
	}

	cert := tlsConfig.Certificates[0]

	// Parse certificate
	x509Cert, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return fmt.Errorf("failed to parse certificate: %w", err)
	}

	// Check if certificate is expired
	now := time.Now()
	if now.Before(x509Cert.NotBefore) {
		return fmt.Errorf("certificate not yet valid (valid from %v)", x509Cert.NotBefore)
	}

	if now.After(x509Cert.NotAfter) {
		return fmt.Errorf("certificate expired (expired on %v)", x509Cert.NotAfter)
	}

	// Warn if certificate will expire soon (within 30 days)
	expiresIn := x509Cert.NotAfter.Sub(now)
	if expiresIn < 30*24*time.Hour {
		// This is a warning, not an error
		fmt.Fprintf(os.Stderr, "WARNING: Certificate will expire in %v\n", expiresIn)
	}

	return nil
}

// GetCertificateInfo returns information about the current certificate
func (c *Config) GetCertificateInfo() (*CertificateInfo, error) {
	c.mu.RLock()
	tlsConfig := c.tlsConfig
	c.mu.RUnlock()

	if tlsConfig == nil {
		return nil, ErrNoCertificate
	}

	if len(tlsConfig.Certificates) == 0 {
		return nil, ErrNoCertificate
	}

	cert := tlsConfig.Certificates[0]

	// Parse certificate
	x509Cert, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("failed to parse certificate: %w", err)
	}

	info := &CertificateInfo{
		Subject:      x509Cert.Subject.String(),
		Issuer:       x509Cert.Issuer.String(),
		NotBefore:    x509Cert.NotBefore,
		NotAfter:     x509Cert.NotAfter,
		DNSNames:     x509Cert.DNSNames,
		SerialNumber: x509Cert.SerialNumber.String(),
	}

	return info, nil
}

// CertificateInfo holds information about a certificate
type CertificateInfo struct {
	Subject      string
	Issuer       string
	NotBefore    time.Time
	NotAfter     time.Time
	DNSNames     []string
	SerialNumber string
}

// IsExpired checks if the certificate is expired
func (info *CertificateInfo) IsExpired() bool {
	return time.Now().After(info.NotAfter)
}

// ExpiresIn returns the duration until certificate expiration
func (info *CertificateInfo) ExpiresIn() time.Duration {
	return info.NotAfter.Sub(time.Now())
}
