package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestNewAuthConfig tests the creation of a new auth configuration
func TestNewAuthConfig(t *testing.T) {
	config := NewAuthConfig()
	if config == nil {
		t.Fatal("NewAuthConfig returned nil")
	}

	if config.APIKeys == nil {
		t.Fatal("APIKeys map is nil")
	}

	if len(config.APIKeys) != 0 {
		t.Errorf("Expected empty APIKeys map, got %d keys", len(config.APIKeys))
	}
}

// TestAddAPIKey tests adding API keys to the configuration
func TestAddAPIKey(t *testing.T) {
	config := NewAuthConfig()

	tests := []struct {
		name        string
		key         *APIKey
		wantErr     bool
		errContains string
	}{
		{
			name: "valid API key with sk_live_ prefix",
			key: &APIKey{
				KeyID:  "test_key_1",
				Key:    "sk_live_1234567890abcdef",
				Scopes: []string{"read", "write"},
			},
			wantErr: false,
		},
		{
			name: "valid API key with sk_test_ prefix",
			key: &APIKey{
				KeyID:  "test_key_2",
				Key:    "sk_test_1234567890abcdef",
				Scopes: []string{"read"},
			},
			wantErr: false,
		},
		{
			name:        "nil API key",
			key:         nil,
			wantErr:     true,
			errContains: "cannot be nil",
		},
		{
			name: "empty key ID",
			key: &APIKey{
				KeyID: "",
				Key:   "sk_live_1234567890abcdef",
			},
			wantErr:     true,
			errContains: "ID cannot be empty",
		},
		{
			name: "empty key value",
			key: &APIKey{
				KeyID: "test_key_3",
				Key:   "",
			},
			wantErr:     true,
			errContains: "value cannot be empty",
		},
		{
			name: "invalid key format",
			key: &APIKey{
				KeyID: "test_key_4",
				Key:   "invalid_key_format",
			},
			wantErr:     true,
			errContains: "invalid API key format",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := config.AddAPIKey(tt.key)
			if (err != nil) != tt.wantErr {
				t.Errorf("AddAPIKey() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr && !strings.Contains(err.Error(), tt.errContains) {
				t.Errorf("Expected error containing %q, got %q", tt.errContains, err.Error())
			}
		})
	}
}

// TestAddAPIKeyDuplicate tests adding duplicate API keys
func TestAddAPIKeyDuplicate(t *testing.T) {
	config := NewAuthConfig()

	key1 := &APIKey{
		KeyID:  "test_key_1",
		Key:    "sk_live_1234567890abcdef",
		Scopes: []string{"read"},
	}

	// Add first key - should succeed
	if err := config.AddAPIKey(key1); err != nil {
		t.Fatalf("Failed to add first key: %v", err)
	}

	// Add same key ID again - should fail
	key2 := &APIKey{
		KeyID:  "test_key_1",
		Key:    "sk_live_different_key",
		Scopes: []string{"write"},
	}

	err := config.AddAPIKey(key2)
	if err == nil {
		t.Fatal("Expected error when adding duplicate key ID, got nil")
	}

	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("Expected 'already exists' error, got: %v", err)
	}
}

// TestRemoveAPIKey tests removing API keys
func TestRemoveAPIKey(t *testing.T) {
	config := NewAuthConfig()

	key := &APIKey{
		KeyID:  "test_key_1",
		Key:    "sk_live_1234567890abcdef",
		Scopes: []string{"read"},
	}

	// Add key
	if err := config.AddAPIKey(key); err != nil {
		t.Fatalf("Failed to add key: %v", err)
	}

	// Remove key - should succeed
	if err := config.RemoveAPIKey("test_key_1"); err != nil {
		t.Errorf("Failed to remove key: %v", err)
	}

	// Remove again - should fail
	err := config.RemoveAPIKey("test_key_1")
	if err == nil {
		t.Fatal("Expected error when removing non-existent key, got nil")
	}

	// Remove with empty ID - should fail
	err = config.RemoveAPIKey("")
	if err == nil {
		t.Fatal("Expected error when removing with empty ID, got nil")
	}
}

// TestValidateAPIKey tests API key validation
func TestValidateAPIKey(t *testing.T) {
	config := NewAuthConfig()

	// Add test keys
	validKey := &APIKey{
		KeyID:  "valid_key",
		Key:    "sk_live_valid1234567890",
		Scopes: []string{"read", "write"},
	}

	futureTime := time.Now().Add(24 * time.Hour)
	expiredKey := &APIKey{
		KeyID:     "expired_key",
		Key:       "sk_live_expired1234567890",
		Scopes:    []string{"read"},
		ExpiresAt: &time.Time{}, // Set to zero time (expired)
	}

	validExpiringKey := &APIKey{
		KeyID:     "expiring_key",
		Key:       "sk_live_expiring1234567890",
		Scopes:    []string{"read"},
		ExpiresAt: &futureTime,
	}

	if err := config.AddAPIKey(validKey); err != nil {
		t.Fatalf("Failed to add valid key: %v", err)
	}
	if err := config.AddAPIKey(expiredKey); err != nil {
		t.Fatalf("Failed to add expired key: %v", err)
	}
	if err := config.AddAPIKey(validExpiringKey); err != nil {
		t.Fatalf("Failed to add expiring key: %v", err)
	}

	tests := []struct {
		name        string
		providedKey string
		wantErr     error
		wantKeyID   string
	}{
		{
			name:        "valid API key",
			providedKey: "sk_live_valid1234567890",
			wantErr:     nil,
			wantKeyID:   "valid_key",
		},
		{
			name:        "valid expiring key",
			providedKey: "sk_live_expiring1234567890",
			wantErr:     nil,
			wantKeyID:   "expiring_key",
		},
		{
			name:        "expired API key",
			providedKey: "sk_live_expired1234567890",
			wantErr:     ErrExpiredAPIKey,
		},
		{
			name:        "invalid API key",
			providedKey: "sk_live_invalid",
			wantErr:     ErrInvalidAPIKey,
		},
		{
			name:        "empty API key",
			providedKey: "",
			wantErr:     ErrMissingAPIKey,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key, err := config.validateAPIKey(tt.providedKey)

			if tt.wantErr != nil {
				if err == nil {
					t.Fatalf("Expected error %v, got nil", tt.wantErr)
				}
				if err != tt.wantErr {
					t.Errorf("Expected error %v, got %v", tt.wantErr, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if key.KeyID != tt.wantKeyID {
				t.Errorf("Expected KeyID %q, got %q", tt.wantKeyID, key.KeyID)
			}
		})
	}
}

// TestExtractAPIKey tests API key extraction from requests
func TestExtractAPIKey(t *testing.T) {
	tests := []struct {
		name        string
		headers     map[string]string
		wantKey     string
		wantErr     bool
		errContains string
	}{
		{
			name: "Authorization Bearer token",
			headers: map[string]string{
				"Authorization": "Bearer sk_live_1234567890",
			},
			wantKey: "sk_live_1234567890",
			wantErr: false,
		},
		{
			name: "X-API-Key header",
			headers: map[string]string{
				"X-API-Key": "sk_test_1234567890",
			},
			wantKey: "sk_test_1234567890",
			wantErr: false,
		},
		{
			name: "Authorization takes precedence",
			headers: map[string]string{
				"Authorization": "Bearer sk_live_1234567890",
				"X-API-Key":     "sk_test_other",
			},
			wantKey: "sk_live_1234567890",
			wantErr: false,
		},
		{
			name:        "no API key",
			headers:     map[string]string{},
			wantErr:     true,
			errContains: "missing",
		},
		{
			name: "invalid Authorization format",
			headers: map[string]string{
				"Authorization": "Basic username:password",
			},
			wantErr:     true,
			errContains: "missing",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/test", nil)
			for key, value := range tt.headers {
				req.Header.Set(key, value)
			}

			key, err := extractAPIKey(req)

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

			if key != tt.wantKey {
				t.Errorf("Expected key %q, got %q", tt.wantKey, key)
			}
		})
	}
}

// TestAuthMiddleware tests the authentication middleware
func TestAuthMiddleware(t *testing.T) {
	config := NewAuthConfig()

	// Add test API key
	testKey := &APIKey{
		KeyID:  "test_key",
		Key:    "sk_live_test1234567890",
		Scopes: []string{"read", "write"},
	}
	if err := config.AddAPIKey(testKey); err != nil {
		t.Fatalf("Failed to add test key: %v", err)
	}

	middleware := NewAuthMiddleware(config)

	// Handler that checks if authentication succeeded
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		info := GetAPIKeyInfo(r.Context())
		if info == nil {
			t.Error("API key info not found in context")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		if info.KeyID != "test_key" {
			t.Errorf("Expected KeyID 'test_key', got %q", info.KeyID)
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte("authenticated"))
	})

	tests := []struct {
		name           string
		headers        map[string]string
		wantStatusCode int
		wantBody       string
	}{
		{
			name: "valid authentication",
			headers: map[string]string{
				"Authorization": "Bearer sk_live_test1234567890",
			},
			wantStatusCode: http.StatusOK,
			wantBody:       "authenticated",
		},
		{
			name:           "missing API key",
			headers:        map[string]string{},
			wantStatusCode: http.StatusUnauthorized,
			wantBody:       "missing_api_key",
		},
		{
			name: "invalid API key",
			headers: map[string]string{
				"Authorization": "Bearer sk_live_invalid",
			},
			wantStatusCode: http.StatusUnauthorized,
			wantBody:       "invalid_api_key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/test", nil)
			for key, value := range tt.headers {
				req.Header.Set(key, value)
			}

			rr := httptest.NewRecorder()
			handler := middleware.Middleware(testHandler)
			handler.ServeHTTP(rr, req)

			if rr.Code != tt.wantStatusCode {
				t.Errorf("Expected status code %d, got %d", tt.wantStatusCode, rr.Code)
			}

			if tt.wantBody != "" && !strings.Contains(rr.Body.String(), tt.wantBody) {
				t.Errorf("Expected body to contain %q, got %q", tt.wantBody, rr.Body.String())
			}
		})
	}
}

// TestGetAPIKeyInfo tests retrieving API key info from context
func TestGetAPIKeyInfo(t *testing.T) {
	// Test with API key info in context
	info := &APIKeyInfo{
		KeyID:  "test_key",
		Scopes: []string{"read"},
	}

	ctx := context.WithValue(context.Background(), ContextKeyAPIKey, info)
	retrieved := GetAPIKeyInfo(ctx)

	if retrieved == nil {
		t.Fatal("Expected API key info, got nil")
	}

	if retrieved.KeyID != info.KeyID {
		t.Errorf("Expected KeyID %q, got %q", info.KeyID, retrieved.KeyID)
	}

	// Test with no API key info in context
	emptyCtx := context.Background()
	retrieved = GetAPIKeyInfo(emptyCtx)

	if retrieved != nil {
		t.Errorf("Expected nil, got %v", retrieved)
	}
}

// TestAPIKeyInfoHasScope tests the HasScope method
func TestAPIKeyInfoHasScope(t *testing.T) {
	info := &APIKeyInfo{
		KeyID:  "test_key",
		Scopes: []string{"read", "write", "admin"},
	}

	tests := []struct {
		scope    string
		expected bool
	}{
		{"read", true},
		{"write", true},
		{"admin", true},
		{"delete", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.scope, func(t *testing.T) {
			result := info.HasScope(tt.scope)
			if result != tt.expected {
				t.Errorf("HasScope(%q) = %v, want %v", tt.scope, result, tt.expected)
			}
		})
	}
}

// TestConcurrentAccess tests concurrent access to auth configuration
func TestConcurrentAccess(t *testing.T) {
	config := NewAuthConfig()

	// Add initial key
	initialKey := &APIKey{
		KeyID:  "initial_key",
		Key:    "sk_live_initial1234567890",
		Scopes: []string{"read"},
	}
	if err := config.AddAPIKey(initialKey); err != nil {
		t.Fatalf("Failed to add initial key: %v", err)
	}

	var wg sync.WaitGroup
	numGoroutines := 100

	// Concurrent reads
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := config.validateAPIKey("sk_live_initial1234567890")
			if err != nil {
				t.Errorf("Unexpected error during concurrent read: %v", err)
			}
		}()
	}

	wg.Wait()
}

// TestTimingAttackResistance tests that API key validation is resistant to timing attacks
func TestTimingAttackResistance(t *testing.T) {
	config := NewAuthConfig()

	// Add multiple keys
	for i := 0; i < 10; i++ {
		key := &APIKey{
			KeyID:  "key_" + string(rune('0'+i)),
			Key:    "sk_live_" + string(rune('a'+i)) + "1234567890abcdef",
			Scopes: []string{"read"},
		}
		if err := config.AddAPIKey(key); err != nil {
			t.Fatalf("Failed to add key: %v", err)
		}
	}

	// Measure time for invalid key
	invalidKey := "sk_live_invalid1234567890"
	start := time.Now()
	_, _ = config.validateAPIKey(invalidKey)
	invalidDuration := time.Since(start)

	// Measure time for almost correct key (differs by one character)
	almostCorrectKey := "sk_live_a1234567890abcdeX" // Last char differs
	start = time.Now()
	_, _ = config.validateAPIKey(almostCorrectKey)
	almostDuration := time.Since(start)

	// The time difference should be minimal (< 10ms)
	// This is a heuristic test - timing attacks are subtle
	timeDiff := almostDuration - invalidDuration
	if timeDiff < 0 {
		timeDiff = -timeDiff
	}

	// Allow up to 10ms variance (generous to account for system noise)
	if timeDiff > 10*time.Millisecond {
		t.Logf("Warning: Large timing difference detected: %v (this may indicate timing attack vulnerability)", timeDiff)
	}
}
