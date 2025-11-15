package middleware

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestNewLeaseRateLimitConfig tests creating a new lease rate limit configuration
func TestNewLeaseRateLimitConfig(t *testing.T) {
	config := NewLeaseRateLimitConfig(50, 100)

	if config.DefaultRate != 50 {
		t.Errorf("Expected DefaultRate 50, got %f", config.DefaultRate)
	}

	if config.DefaultBurst != 100 {
		t.Errorf("Expected DefaultBurst 100, got %d", config.DefaultBurst)
	}

	if config.Rules == nil {
		t.Error("Rules map should be initialized")
	}
}

// TestNewLeaseRateLimitConfigDefaults tests default values
func TestNewLeaseRateLimitConfigDefaults(t *testing.T) {
	config := NewLeaseRateLimitConfig(0, 0)

	if config.DefaultRate <= 0 {
		t.Error("DefaultRate should have default value")
	}

	if config.DefaultBurst <= 0 {
		t.Error("DefaultBurst should have default value")
	}
}

// TestAddLeaseRule tests adding lease rate limit rules
func TestAddLeaseRule(t *testing.T) {
	config := NewLeaseRateLimitConfig(50, 100)

	tests := []struct {
		name        string
		rule        *LeaseRateLimitRule
		wantErr     bool
		errContains string
	}{
		{
			name: "valid rule",
			rule: &LeaseRateLimitRule{
				LeaseID:           "mcp-server-1",
				RequestsPerSecond: 100,
				BurstSize:         200,
			},
			wantErr: false,
		},
		{
			name: "valid rule with wildcard",
			rule: &LeaseRateLimitRule{
				LeaseID:           "n8n-*",
				RequestsPerSecond: 200,
				BurstSize:         400,
			},
			wantErr: false,
		},
		{
			name:        "nil rule",
			rule:        nil,
			wantErr:     true,
			errContains: "cannot be nil",
		},
		{
			name: "empty lease ID",
			rule: &LeaseRateLimitRule{
				LeaseID:           "",
				RequestsPerSecond: 100,
			},
			wantErr:     true,
			errContains: "cannot be empty",
		},
		{
			name: "invalid rate",
			rule: &LeaseRateLimitRule{
				LeaseID:           "test-lease",
				RequestsPerSecond: -1,
			},
			wantErr:     true,
			errContains: "must be positive",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := config.AddRule(tt.rule)
			if (err != nil) != tt.wantErr {
				t.Errorf("AddRule() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr && !strings.Contains(err.Error(), tt.errContains) {
				t.Errorf("Expected error containing %q, got %q", tt.errContains, err.Error())
			}
		})
	}
}

// TestAddDuplicateLeaseRule tests adding duplicate rules
func TestAddDuplicateLeaseRule(t *testing.T) {
	config := NewLeaseRateLimitConfig(50, 100)

	rule1 := &LeaseRateLimitRule{
		LeaseID:           "test-lease",
		RequestsPerSecond: 100,
		BurstSize:         200,
	}

	// Add first rule - should succeed
	if err := config.AddRule(rule1); err != nil {
		t.Fatalf("Failed to add first rule: %v", err)
	}

	// Add same lease ID again - should fail
	rule2 := &LeaseRateLimitRule{
		LeaseID:           "test-lease",
		RequestsPerSecond: 200,
		BurstSize:         400,
	}

	err := config.AddRule(rule2)
	if err == nil {
		t.Fatal("Expected error when adding duplicate lease ID, got nil")
	}

	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("Expected 'already exists' error, got: %v", err)
	}
}

// TestUpdateLeaseRule tests updating lease rules
func TestUpdateLeaseRule(t *testing.T) {
	config := NewLeaseRateLimitConfig(50, 100)

	// Add initial rule
	rule1 := &LeaseRateLimitRule{
		LeaseID:           "test-lease",
		RequestsPerSecond: 100,
		BurstSize:         200,
	}
	if err := config.AddRule(rule1); err != nil {
		t.Fatalf("Failed to add rule: %v", err)
	}

	// Update rule
	rule2 := &LeaseRateLimitRule{
		LeaseID:           "test-lease",
		RequestsPerSecond: 200,
		BurstSize:         400,
	}

	if err := config.UpdateRule(rule2); err != nil {
		t.Errorf("Failed to update rule: %v", err)
	}

	// Verify update
	updated := config.GetRule("test-lease")
	if updated.RequestsPerSecond != 200 {
		t.Errorf("Expected rate 200, got %f", updated.RequestsPerSecond)
	}
}

// TestRemoveLeaseRule tests removing lease rules
func TestRemoveLeaseRule(t *testing.T) {
	config := NewLeaseRateLimitConfig(50, 100)

	rule := &LeaseRateLimitRule{
		LeaseID:           "test-lease",
		RequestsPerSecond: 100,
		BurstSize:         200,
	}

	// Add rule
	if err := config.AddRule(rule); err != nil {
		t.Fatalf("Failed to add rule: %v", err)
	}

	// Remove rule - should succeed
	if err := config.RemoveRule("test-lease"); err != nil {
		t.Errorf("Failed to remove rule: %v", err)
	}

	// Remove again - should fail
	err := config.RemoveRule("test-lease")
	if err == nil {
		t.Fatal("Expected error when removing non-existent rule, got nil")
	}
}

// TestGetLeaseRule tests retrieving lease rules
func TestGetLeaseRule(t *testing.T) {
	config := NewLeaseRateLimitConfig(50, 100)

	// Add exact match rule
	exactRule := &LeaseRateLimitRule{
		LeaseID:           "exact-lease",
		RequestsPerSecond: 100,
		BurstSize:         200,
	}
	if err := config.AddRule(exactRule); err != nil {
		t.Fatalf("Failed to add exact rule: %v", err)
	}

	// Add wildcard rule
	wildcardRule := &LeaseRateLimitRule{
		LeaseID:           "mcp-*",
		RequestsPerSecond: 50,
		BurstSize:         100,
	}
	if err := config.AddRule(wildcardRule); err != nil {
		t.Fatalf("Failed to add wildcard rule: %v", err)
	}

	tests := []struct {
		name     string
		leaseID  string
		wantRate float64
	}{
		{
			name:     "exact match",
			leaseID:  "exact-lease",
			wantRate: 100,
		},
		{
			name:     "wildcard match",
			leaseID:  "mcp-server-1",
			wantRate: 50,
		},
		{
			name:     "no match (uses default)",
			leaseID:  "unknown-lease",
			wantRate: 50, // default
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rate, _ := config.GetRateLimit(tt.leaseID)
			if rate != tt.wantRate {
				t.Errorf("Expected rate %f, got %f", tt.wantRate, rate)
			}
		})
	}
}

// TestListLeaseRules tests listing all rules
func TestListLeaseRules(t *testing.T) {
	config := NewLeaseRateLimitConfig(50, 100)

	// Initially should be empty
	rules := config.ListRules()
	if len(rules) != 0 {
		t.Errorf("Expected 0 rules initially, got %d", len(rules))
	}

	// Add some rules
	for i := 1; i <= 3; i++ {
		rule := &LeaseRateLimitRule{
			LeaseID:           fmt.Sprintf("lease-%d", i),
			RequestsPerSecond: float64(i * 100),
			BurstSize:         i * 200,
		}
		if err := config.AddRule(rule); err != nil {
			t.Fatalf("Failed to add rule %d: %v", i, err)
		}
	}

	rules = config.ListRules()
	if len(rules) != 3 {
		t.Errorf("Expected 3 rules, got %d", len(rules))
	}
}

// TestLeaseRateLimitMiddleware tests the lease rate limit middleware
func TestLeaseRateLimitMiddleware(t *testing.T) {
	leaseConfig := NewLeaseRateLimitConfig(10, 10)

	// Add specific rule for test-lease
	rule := &LeaseRateLimitRule{
		LeaseID:           "test-lease",
		RequestsPerSecond: 5,
		BurstSize:         5,
	}
	if err := leaseConfig.AddRule(rule); err != nil {
		t.Fatalf("Failed to add rule: %v", err)
	}

	rateLimitConfig := NewRateLimitConfig(100, 200)
	middleware := NewLeaseRateLimitMiddleware(leaseConfig, rateLimitConfig)
	defer middleware.Stop()

	// Create test handler
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	})

	wrappedHandler := middleware.Middleware(handler)

	// Create request with lease ID and API key context
	req := httptest.NewRequest("GET", "/test", nil)
	ctx := context.WithValue(req.Context(), ContextKeyAPIKey, &APIKeyInfo{
		KeyID: "test_key",
	})
	ctx = context.WithValue(ctx, contextKey("lease_id"), "test-lease")
	req = req.WithContext(ctx)

	// First 5 requests should succeed (burst)
	for i := 0; i < 5; i++ {
		rr := httptest.NewRecorder()
		wrappedHandler.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("Request %d: expected status 200, got %d", i+1, rr.Code)
		}
	}

	// 6th request should be rate limited
	rr := httptest.NewRecorder()
	wrappedHandler.ServeHTTP(rr, req)

	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("Request 6: expected status 429, got %d", rr.Code)
	}
}

// TestLeaseRateLimitMiddlewareWithoutLeaseID tests fallback behavior
func TestLeaseRateLimitMiddlewareWithoutLeaseID(t *testing.T) {
	leaseConfig := NewLeaseRateLimitConfig(10, 10)
	rateLimitConfig := NewRateLimitConfig(100, 200)
	middleware := NewLeaseRateLimitMiddleware(leaseConfig, rateLimitConfig)
	defer middleware.Stop()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	wrappedHandler := middleware.Middleware(handler)

	// Request without lease ID should use default rate limiting
	req := httptest.NewRequest("GET", "/test", nil)
	ctx := context.WithValue(req.Context(), ContextKeyAPIKey, &APIKeyInfo{
		KeyID: "test_key",
	})
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	wrappedHandler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rr.Code)
	}
}

// TestLeaseRateLimitWildcardPrecedence tests wildcard matching precedence
func TestLeaseRateLimitWildcardPrecedence(t *testing.T) {
	config := NewLeaseRateLimitConfig(10, 20)

	// Add wildcard rule
	wildcardRule := &LeaseRateLimitRule{
		LeaseID:           "mcp-*",
		RequestsPerSecond: 50,
		BurstSize:         100,
	}
	if err := config.AddRule(wildcardRule); err != nil {
		t.Fatalf("Failed to add wildcard rule: %v", err)
	}

	// Add exact match rule
	exactRule := &LeaseRateLimitRule{
		LeaseID:           "mcp-server-1",
		RequestsPerSecond: 100,
		BurstSize:         200,
	}
	if err := config.AddRule(exactRule); err != nil {
		t.Fatalf("Failed to add exact rule: %v", err)
	}

	// Exact match should take precedence
	rate, _ := config.GetRateLimit("mcp-server-1")
	if rate != 100 {
		t.Errorf("Expected exact match rate 100, got %f", rate)
	}

	// Wildcard should match other mcp- leases
	rate, _ = config.GetRateLimit("mcp-server-2")
	if rate != 50 {
		t.Errorf("Expected wildcard match rate 50, got %f", rate)
	}

	// Non-matching should use default
	rate, _ = config.GetRateLimit("n8n-server-1")
	if rate != 10 {
		t.Errorf("Expected default rate 10, got %f", rate)
	}
}
