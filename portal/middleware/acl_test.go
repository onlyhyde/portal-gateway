package middleware

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestNewACLConfig tests creating a new ACL configuration
func TestNewACLConfig(t *testing.T) {
	config := NewACLConfig()
	if config == nil {
		t.Fatal("NewACLConfig returned nil")
	}

	if config.Rules == nil {
		t.Fatal("Rules map is nil")
	}

	if len(config.Rules) != 0 {
		t.Errorf("Expected empty Rules map, got %d rules", len(config.Rules))
	}
}

// TestAddRule tests adding ACL rules
func TestAddRule(t *testing.T) {
	config := NewACLConfig()

	tests := []struct {
		name        string
		rule        *ACLRule
		wantErr     bool
		errContains string
	}{
		{
			name: "valid rule",
			rule: &ACLRule{
				LeaseID:       "lease-001",
				AllowedKeyIDs: []string{"key1", "key2"},
			},
			wantErr: false,
		},
		{
			name: "valid rule with wildcard",
			rule: &ACLRule{
				LeaseID:       "mcp-*",
				AllowedKeyIDs: []string{"key1"},
			},
			wantErr: false,
		},
		{
			name: "valid rule with IP ranges",
			rule: &ACLRule{
				LeaseID:         "lease-002",
				AllowedKeyIDs:   []string{"key1"},
				AllowedIPRanges: []*net.IPNet{mustParseCIDR("192.168.1.0/24")},
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
			rule: &ACLRule{
				LeaseID:       "",
				AllowedKeyIDs: []string{"key1"},
			},
			wantErr:     true,
			errContains: "invalid lease ID",
		},
		{
			name: "invalid wildcard pattern (multiple asterisks)",
			rule: &ACLRule{
				LeaseID:       "mcp-*-*",
				AllowedKeyIDs: []string{"key1"},
			},
			wantErr:     true,
			errContains: "wildcard",
		},
		{
			name: "invalid wildcard pattern (asterisk not at end)",
			rule: &ACLRule{
				LeaseID:       "*-mcp",
				AllowedKeyIDs: []string{"key1"},
			},
			wantErr:     true,
			errContains: "wildcard",
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

// TestRemoveRule tests removing ACL rules
func TestRemoveRule(t *testing.T) {
	config := NewACLConfig()

	// Add a rule
	rule := &ACLRule{
		LeaseID:       "lease-001",
		AllowedKeyIDs: []string{"key1"},
	}
	if err := config.AddRule(rule); err != nil {
		t.Fatalf("Failed to add rule: %v", err)
	}

	// Remove rule - should succeed
	if err := config.RemoveRule("lease-001"); err != nil {
		t.Errorf("Failed to remove rule: %v", err)
	}

	// Remove again - should fail
	err := config.RemoveRule("lease-001")
	if err == nil {
		t.Fatal("Expected error when removing non-existent rule, got nil")
	}

	// Remove with empty ID - should fail
	err = config.RemoveRule("")
	if err == nil {
		t.Fatal("Expected error when removing with empty ID, got nil")
	}
}

// TestGetRule tests retrieving ACL rules
func TestGetRule(t *testing.T) {
	config := NewACLConfig()

	// Add exact match rule
	exactRule := &ACLRule{
		LeaseID:       "lease-001",
		AllowedKeyIDs: []string{"key1"},
	}
	if err := config.AddRule(exactRule); err != nil {
		t.Fatalf("Failed to add exact rule: %v", err)
	}

	// Add wildcard rule
	wildcardRule := &ACLRule{
		LeaseID:       "mcp-*",
		AllowedKeyIDs: []string{"key2"},
	}
	if err := config.AddRule(wildcardRule); err != nil {
		t.Fatalf("Failed to add wildcard rule: %v", err)
	}

	tests := []struct {
		name       string
		leaseID    string
		wantRule   bool
		wantKeyIDs []string
	}{
		{
			name:       "exact match",
			leaseID:    "lease-001",
			wantRule:   true,
			wantKeyIDs: []string{"key1"},
		},
		{
			name:       "wildcard match",
			leaseID:    "mcp-server-1",
			wantRule:   true,
			wantKeyIDs: []string{"key2"},
		},
		{
			name:     "no match",
			leaseID:  "nonexistent",
			wantRule: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule := config.GetRule(tt.leaseID)

			if tt.wantRule && rule == nil {
				t.Fatal("Expected rule, got nil")
			}

			if !tt.wantRule && rule != nil {
				t.Errorf("Expected nil rule, got %v", rule)
			}

			if tt.wantRule {
				if len(rule.AllowedKeyIDs) != len(tt.wantKeyIDs) {
					t.Errorf("Expected %d allowed key IDs, got %d", len(tt.wantKeyIDs), len(rule.AllowedKeyIDs))
				}
			}
		})
	}
}

// TestCheckAccess tests access control checks
func TestCheckAccess(t *testing.T) {
	config := NewACLConfig()

	// Add rule without IP restrictions
	rule1 := &ACLRule{
		LeaseID:       "lease-001",
		AllowedKeyIDs: []string{"key1", "key2"},
	}
	if err := config.AddRule(rule1); err != nil {
		t.Fatalf("Failed to add rule1: %v", err)
	}

	// Add rule with IP restrictions
	rule2 := &ACLRule{
		LeaseID:         "lease-002",
		AllowedKeyIDs:   []string{"key1"},
		AllowedIPRanges: []*net.IPNet{mustParseCIDR("192.168.1.0/24")},
	}
	if err := config.AddRule(rule2); err != nil {
		t.Fatalf("Failed to add rule2: %v", err)
	}

	tests := []struct {
		name    string
		leaseID string
		keyID   string
		ip      net.IP
		wantErr error
	}{
		{
			name:    "valid access without IP restriction",
			leaseID: "lease-001",
			keyID:   "key1",
			ip:      net.ParseIP("10.0.0.1"),
			wantErr: nil,
		},
		{
			name:    "valid access with matching IP",
			leaseID: "lease-002",
			keyID:   "key1",
			ip:      net.ParseIP("192.168.1.100"),
			wantErr: nil,
		},
		{
			name:    "access denied - key not allowed",
			leaseID: "lease-001",
			keyID:   "key3",
			ip:      net.ParseIP("10.0.0.1"),
			wantErr: ErrAccessDenied,
		},
		{
			name:    "access denied - IP not whitelisted",
			leaseID: "lease-002",
			keyID:   "key1",
			ip:      net.ParseIP("10.0.0.1"),
			wantErr: ErrIPNotWhitelisted,
		},
		{
			name:    "lease not found",
			leaseID: "nonexistent",
			keyID:   "key1",
			ip:      net.ParseIP("10.0.0.1"),
			wantErr: ErrLeaseNotFound,
		},
		{
			name:    "invalid lease ID",
			leaseID: "",
			keyID:   "key1",
			ip:      net.ParseIP("10.0.0.1"),
			wantErr: ErrInvalidLeaseID,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := config.CheckAccess(tt.leaseID, tt.keyID, tt.ip)

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
		})
	}
}

// TestExtractLeaseID tests lease ID extraction from URL paths
func TestExtractLeaseID(t *testing.T) {
	tests := []struct {
		path    string
		wantID  string
	}{
		{"/peer/lease-001", "lease-001"},
		{"/peer/lease-001/data", "lease-001"},
		{"/peer/mcp-server-1", "mcp-server-1"},
		{"/peer/", ""},
		{"/peer", ""},
		{"/other/path", ""},
		{"/", ""},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			leaseID := extractLeaseID(tt.path)
			if leaseID != tt.wantID {
				t.Errorf("extractLeaseID(%q) = %q, want %q", tt.path, leaseID, tt.wantID)
			}
		})
	}
}

// TestGetClientIP tests client IP extraction
func TestGetClientIP(t *testing.T) {
	tests := []struct {
		name       string
		headers    map[string]string
		remoteAddr string
		wantIP     string
	}{
		{
			name: "X-Forwarded-For header",
			headers: map[string]string{
				"X-Forwarded-For": "203.0.113.1, 198.51.100.1",
			},
			remoteAddr: "192.168.1.1:12345",
			wantIP:     "203.0.113.1",
		},
		{
			name: "X-Real-IP header",
			headers: map[string]string{
				"X-Real-IP": "203.0.113.2",
			},
			remoteAddr: "192.168.1.1:12345",
			wantIP:     "203.0.113.2",
		},
		{
			name:       "RemoteAddr only",
			headers:    map[string]string{},
			remoteAddr: "203.0.113.3:12345",
			wantIP:     "203.0.113.3",
		},
		{
			name: "X-Forwarded-For takes precedence",
			headers: map[string]string{
				"X-Forwarded-For": "203.0.113.4",
				"X-Real-IP":       "203.0.113.5",
			},
			remoteAddr: "192.168.1.1:12345",
			wantIP:     "203.0.113.4",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/test", nil)
			for key, value := range tt.headers {
				req.Header.Set(key, value)
			}
			req.RemoteAddr = tt.remoteAddr

			ip := getClientIP(req)
			if ip == nil {
				t.Fatal("Expected IP, got nil")
			}

			if ip.String() != tt.wantIP {
				t.Errorf("Expected IP %s, got %s", tt.wantIP, ip.String())
			}
		})
	}
}

// TestIsIPAllowed tests IP whitelist checking
func TestIsIPAllowed(t *testing.T) {
	allowedRanges := []*net.IPNet{
		mustParseCIDR("192.168.1.0/24"),
		mustParseCIDR("10.0.0.0/8"),
	}

	tests := []struct {
		ip      string
		allowed bool
	}{
		{"192.168.1.100", true},
		{"192.168.1.1", true},
		{"10.5.10.20", true},
		{"172.16.0.1", false},
		{"8.8.8.8", false},
	}

	for _, tt := range tests {
		t.Run(tt.ip, func(t *testing.T) {
			ip := net.ParseIP(tt.ip)
			result := isIPAllowed(ip, allowedRanges)
			if result != tt.allowed {
				t.Errorf("isIPAllowed(%s) = %v, want %v", tt.ip, result, tt.allowed)
			}
		})
	}

	// Test nil IP
	if isIPAllowed(nil, allowedRanges) {
		t.Error("nil IP should not be allowed")
	}
}

// TestValidateWildcardPattern tests wildcard pattern validation
func TestValidateWildcardPattern(t *testing.T) {
	tests := []struct {
		pattern string
		wantErr bool
	}{
		{"mcp-*", false},
		{"lease-*", false},
		{"exact-match", false},
		{"*", true},  // Too short
		{"mcp-*-*", true},  // Multiple wildcards
		{"*-mcp", true},  // Wildcard not at end
		{"", true},  // Empty pattern
	}

	for _, tt := range tests {
		t.Run(tt.pattern, func(t *testing.T) {
			err := validateWildcardPattern(tt.pattern)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateWildcardPattern(%q) error = %v, wantErr %v", tt.pattern, err, tt.wantErr)
			}
		})
	}
}

// TestMatchWildcard tests wildcard matching
func TestMatchWildcard(t *testing.T) {
	tests := []struct {
		pattern string
		str     string
		want    bool
	}{
		{"mcp-*", "mcp-server-1", true},
		{"mcp-*", "mcp-", true},
		{"mcp-*", "n8n-server-1", false},
		{"exact", "exact", true},
		{"exact", "exactish", false},
		{"lease-*", "lease-001", true},
	}

	for _, tt := range tests {
		t.Run(tt.pattern+"_"+tt.str, func(t *testing.T) {
			result := matchWildcard(tt.pattern, tt.str)
			if result != tt.want {
				t.Errorf("matchWildcard(%q, %q) = %v, want %v", tt.pattern, tt.str, result, tt.want)
			}
		})
	}
}

// TestParseCIDR tests CIDR parsing
func TestParseCIDR(t *testing.T) {
	tests := []struct {
		cidr    string
		wantErr bool
	}{
		{"192.168.1.0/24", false},
		{"10.0.0.0/8", false},
		{"2001:db8::/32", false},
		{"invalid", true},
		{"192.168.1.0", true},  // Missing /prefix
	}

	for _, tt := range tests {
		t.Run(tt.cidr, func(t *testing.T) {
			_, err := ParseCIDR(tt.cidr)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseCIDR(%q) error = %v, wantErr %v", tt.cidr, err, tt.wantErr)
			}
		})
	}
}

// TestACLMiddleware tests the ACL middleware
func TestACLMiddleware(t *testing.T) {
	config := NewACLConfig()

	// Add test rule
	rule := &ACLRule{
		LeaseID:       "lease-001",
		AllowedKeyIDs: []string{"test_key"},
	}
	if err := config.AddRule(rule); err != nil {
		t.Fatalf("Failed to add rule: %v", err)
	}

	middleware := NewACLMiddleware(config)

	// Handler that checks if ACL passed
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaseID := GetLeaseID(r.Context())
		if leaseID == "" {
			t.Error("Lease ID not found in context")
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	})

	tests := []struct {
		name           string
		path           string
		setupContext   func() context.Context
		wantStatusCode int
		wantBody       string
	}{
		{
			name: "valid access",
			path: "/peer/lease-001",
			setupContext: func() context.Context {
				return context.WithValue(context.Background(), ContextKeyAPIKey, &APIKeyInfo{
					KeyID: "test_key",
				})
			},
			wantStatusCode: http.StatusOK,
			wantBody:       "success",
		},
		{
			name: "access denied - key not allowed",
			path: "/peer/lease-001",
			setupContext: func() context.Context {
				return context.WithValue(context.Background(), ContextKeyAPIKey, &APIKeyInfo{
					KeyID: "other_key",
				})
			},
			wantStatusCode: http.StatusForbidden,
			wantBody:       "access_denied",
		},
		{
			name: "lease not found",
			path: "/peer/nonexistent",
			setupContext: func() context.Context {
				return context.WithValue(context.Background(), ContextKeyAPIKey, &APIKeyInfo{
					KeyID: "test_key",
				})
			},
			wantStatusCode: http.StatusNotFound,
			wantBody:       "lease_not_found",
		},
		{
			name:           "no authentication",
			path:           "/peer/lease-001",
			setupContext:   func() context.Context { return context.Background() },
			wantStatusCode: http.StatusForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tt.path, nil)
			req = req.WithContext(tt.setupContext())

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

// Helper function to parse CIDR (panics on error, for test data)
func mustParseCIDR(cidr string) *net.IPNet {
	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		panic(err)
	}
	return ipNet
}
