package middleware

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// contextKey is a type for context keys to avoid collisions
type contextKey string

const (
	// ContextKeyAPIKey is the context key for storing API key information
	ContextKeyAPIKey contextKey = "api_key_info"
)

// APIKeyInfo contains information about an authenticated API key
type APIKeyInfo struct {
	KeyID       string
	Scopes      []string
	ExpiresAt   *time.Time
	RateLimitID string
}

// APIKey represents a configured API key with its permissions
type APIKey struct {
	Key       string
	KeyID     string
	Scopes    []string
	ExpiresAt *time.Time
}

// AuthConfig holds the authentication configuration
type AuthConfig struct {
	APIKeys map[string]*APIKey // keyID -> APIKey
	mu      sync.RWMutex
}

// AuthMiddleware provides API key authentication
type AuthMiddleware struct {
	config *AuthConfig
}

// Common errors
var (
	ErrMissingAPIKey   = errors.New("missing API key")
	ErrInvalidAPIKey   = errors.New("invalid API key")
	ErrExpiredAPIKey   = errors.New("API key has expired")
	ErrInvalidKeyFormat = errors.New("invalid API key format")
)

// NewAuthConfig creates a new authentication configuration
func NewAuthConfig() *AuthConfig {
	return &AuthConfig{
		APIKeys: make(map[string]*APIKey),
	}
}

// AddAPIKey adds a new API key to the configuration
// Returns an error if the key already exists or validation fails
func (c *AuthConfig) AddAPIKey(key *APIKey) error {
	if key == nil {
		return errors.New("API key cannot be nil")
	}

	if key.KeyID == "" {
		return errors.New("API key ID cannot be empty")
	}

	if key.Key == "" {
		return errors.New("API key value cannot be empty")
	}

	// Validate key format (should start with sk_live_ or sk_test_)
	if !strings.HasPrefix(key.Key, "sk_live_") && !strings.HasPrefix(key.Key, "sk_test_") {
		return ErrInvalidKeyFormat
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.APIKeys[key.KeyID]; exists {
		return fmt.Errorf("API key with ID %s already exists", key.KeyID)
	}

	c.APIKeys[key.KeyID] = key
	return nil
}

// RemoveAPIKey removes an API key from the configuration
func (c *AuthConfig) RemoveAPIKey(keyID string) error {
	if keyID == "" {
		return errors.New("API key ID cannot be empty")
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.APIKeys[keyID]; !exists {
		return fmt.Errorf("API key with ID %s not found", keyID)
	}

	delete(c.APIKeys, keyID)
	return nil
}

// validateAPIKey performs constant-time comparison to prevent timing attacks
// Returns the APIKey if valid, or an error
func (c *AuthConfig) validateAPIKey(providedKey string) (*APIKey, error) {
	if providedKey == "" {
		return nil, ErrMissingAPIKey
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	// Check all keys with constant-time comparison
	// This prevents timing attacks that could leak information about valid keys
	var foundKey *APIKey
	var isValid bool

	for _, key := range c.APIKeys {
		// Use subtle.ConstantTimeCompare for timing attack prevention
		if subtle.ConstantTimeCompare([]byte(key.Key), []byte(providedKey)) == 1 {
			foundKey = key
			isValid = true
			// Don't break - continue checking all keys to maintain constant time
		}
	}

	if !isValid {
		return nil, ErrInvalidAPIKey
	}

	// Check expiration
	if foundKey.ExpiresAt != nil && time.Now().After(*foundKey.ExpiresAt) {
		return nil, ErrExpiredAPIKey
	}

	return foundKey, nil
}

// NewAuthMiddleware creates a new authentication middleware
func NewAuthMiddleware(config *AuthConfig) *AuthMiddleware {
	if config == nil {
		config = NewAuthConfig()
	}
	return &AuthMiddleware{
		config: config,
	}
}

// extractAPIKey extracts the API key from the request
// Supports multiple formats:
// - Authorization: Bearer <key>
// - X-API-Key: <key>
func extractAPIKey(r *http.Request) (string, error) {
	// Try Authorization header first (Bearer token)
	authHeader := r.Header.Get("Authorization")
	if authHeader != "" {
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) == 2 && strings.ToLower(parts[0]) == "bearer" {
			return strings.TrimSpace(parts[1]), nil
		}
	}

	// Try X-API-Key header
	apiKey := r.Header.Get("X-API-Key")
	if apiKey != "" {
		return strings.TrimSpace(apiKey), nil
	}

	return "", ErrMissingAPIKey
}

// Middleware returns an http.Handler that performs API key authentication
func (m *AuthMiddleware) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Extract API key from request
		apiKey, err := extractAPIKey(r)
		if err != nil {
			m.handleAuthError(w, err)
			return
		}

		// Validate API key
		keyInfo, err := m.config.validateAPIKey(apiKey)
		if err != nil {
			m.handleAuthError(w, err)
			return
		}

		// Create API key info for context
		info := &APIKeyInfo{
			KeyID:       keyInfo.KeyID,
			Scopes:      keyInfo.Scopes,
			ExpiresAt:   keyInfo.ExpiresAt,
			RateLimitID: keyInfo.KeyID, // Use KeyID for rate limiting
		}

		// Add API key info to request context
		ctx := context.WithValue(r.Context(), ContextKeyAPIKey, info)

		// Call next handler with authenticated context
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// handleAuthError writes an appropriate error response
func (m *AuthMiddleware) handleAuthError(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json")

	switch {
	case errors.Is(err, ErrMissingAPIKey):
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprintf(w, `{"error":"missing_api_key","message":"API key is required"}`)
	case errors.Is(err, ErrInvalidAPIKey):
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprintf(w, `{"error":"invalid_api_key","message":"Invalid API key"}`)
	case errors.Is(err, ErrExpiredAPIKey):
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprintf(w, `{"error":"expired_api_key","message":"API key has expired"}`)
	case errors.Is(err, ErrInvalidKeyFormat):
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, `{"error":"invalid_key_format","message":"API key must start with sk_live_ or sk_test_"}`)
	default:
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprintf(w, `{"error":"authentication_failed","message":"Authentication failed"}`)
	}
}

// GetAPIKeyInfo retrieves API key information from the request context
// Returns nil if no API key info is present
func GetAPIKeyInfo(ctx context.Context) *APIKeyInfo {
	info, ok := ctx.Value(ContextKeyAPIKey).(*APIKeyInfo)
	if !ok {
		return nil
	}
	return info
}

// HasScope checks if the API key has a specific scope
func (info *APIKeyInfo) HasScope(scope string) bool {
	for _, s := range info.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}
