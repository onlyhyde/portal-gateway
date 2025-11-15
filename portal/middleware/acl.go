package middleware

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
)

// ACLRule represents an access control rule for a lease
type ACLRule struct {
	LeaseID        string   // Lease ID (supports wildcards like "mcp-*")
	AllowedKeyIDs  []string // List of allowed API key IDs
	AllowedIPRanges []*net.IPNet // CIDR ranges for IP whitelist
}

// ACLConfig holds the access control configuration
type ACLConfig struct {
	Rules map[string]*ACLRule // leaseID -> ACLRule
	mu    sync.RWMutex
}

// ACLMiddleware provides lease-based access control
type ACLMiddleware struct {
	config *ACLConfig
}

// Common errors
var (
	ErrLeaseNotFound       = errors.New("lease not found")
	ErrAccessDenied        = errors.New("access denied to this lease")
	ErrInvalidLeaseID      = errors.New("invalid lease ID")
	ErrInvalidIPRange      = errors.New("invalid IP range")
	ErrIPNotWhitelisted    = errors.New("IP address not whitelisted")
)

// NewACLConfig creates a new ACL configuration
func NewACLConfig() *ACLConfig {
	return &ACLConfig{
		Rules: make(map[string]*ACLRule),
	}
}

// AddRule adds a new ACL rule
// Returns an error if validation fails
func (c *ACLConfig) AddRule(rule *ACLRule) error {
	if rule == nil {
		return errors.New("ACL rule cannot be nil")
	}

	if rule.LeaseID == "" {
		return ErrInvalidLeaseID
	}

	// Validate wildcard pattern if present
	if strings.Contains(rule.LeaseID, "*") {
		if err := validateWildcardPattern(rule.LeaseID); err != nil {
			return fmt.Errorf("invalid wildcard pattern: %w", err)
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.Rules[rule.LeaseID] = rule
	return nil
}

// RemoveRule removes an ACL rule for a lease
func (c *ACLConfig) RemoveRule(leaseID string) error {
	if leaseID == "" {
		return ErrInvalidLeaseID
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.Rules[leaseID]; !exists {
		return fmt.Errorf("ACL rule for lease %s not found", leaseID)
	}

	delete(c.Rules, leaseID)
	return nil
}

// GetRule retrieves an ACL rule for a lease
// Returns nil if no rule exists
func (c *ACLConfig) GetRule(leaseID string) *ACLRule {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// First, try exact match
	if rule, exists := c.Rules[leaseID]; exists {
		return rule
	}

	// Then, try wildcard matches
	for pattern, rule := range c.Rules {
		if matchWildcard(pattern, leaseID) {
			return rule
		}
	}

	return nil
}

// ListRules returns all ACL rules
func (c *ACLConfig) ListRules() []*ACLRule {
	c.mu.RLock()
	defer c.mu.RUnlock()

	rules := make([]*ACLRule, 0, len(c.Rules))
	for _, rule := range c.Rules {
		rules = append(rules, rule)
	}
	return rules
}

// CheckAccess checks if an API key has access to a lease from a given IP
func (c *ACLConfig) CheckAccess(leaseID, keyID string, ip net.IP) error {
	if leaseID == "" {
		return ErrInvalidLeaseID
	}

	rule := c.GetRule(leaseID)

	// If no rule exists, deny access by default (fail-closed)
	if rule == nil {
		return ErrLeaseNotFound
	}

	// Check API key whitelist
	if !contains(rule.AllowedKeyIDs, keyID) {
		return ErrAccessDenied
	}

	// Check IP whitelist if configured
	if len(rule.AllowedIPRanges) > 0 {
		if !isIPAllowed(ip, rule.AllowedIPRanges) {
			return ErrIPNotWhitelisted
		}
	}

	return nil
}

// NewACLMiddleware creates a new ACL middleware
func NewACLMiddleware(config *ACLConfig) *ACLMiddleware {
	if config == nil {
		config = NewACLConfig()
	}
	return &ACLMiddleware{
		config: config,
	}
}

// Middleware returns an http.Handler that performs ACL checks
func (m *ACLMiddleware) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Get API key info from context (set by auth middleware)
		apiKeyInfo := GetAPIKeyInfo(r.Context())
		if apiKeyInfo == nil {
			m.handleACLError(w, errors.New("authentication required"))
			return
		}

		// Extract lease ID from URL path
		// Expected format: /peer/{leaseID}/...
		leaseID := extractLeaseID(r.URL.Path)
		if leaseID == "" {
			m.handleACLError(w, ErrInvalidLeaseID)
			return
		}

		// Get client IP
		clientIP := getClientIP(r)

		// Check access
		if err := m.config.CheckAccess(leaseID, apiKeyInfo.KeyID, clientIP); err != nil {
			m.handleACLError(w, err)
			return
		}

		// Add lease ID to context for downstream handlers
		ctx := context.WithValue(r.Context(), contextKey("lease_id"), leaseID)

		// Call next handler
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// handleACLError writes an appropriate error response
func (m *ACLMiddleware) handleACLError(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json")

	switch {
	case errors.Is(err, ErrLeaseNotFound):
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, `{"error":"lease_not_found","message":"Lease not found or no ACL rule configured"}`)
	case errors.Is(err, ErrAccessDenied):
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprintf(w, `{"error":"access_denied","message":"Access denied to this lease"}`)
	case errors.Is(err, ErrInvalidLeaseID):
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, `{"error":"invalid_lease_id","message":"Invalid or missing lease ID"}`)
	case errors.Is(err, ErrIPNotWhitelisted):
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprintf(w, `{"error":"ip_not_whitelisted","message":"Your IP address is not whitelisted for this lease"}`)
	default:
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprintf(w, `{"error":"access_denied","message":"Access denied"}`)
	}
}

// extractLeaseID extracts the lease ID from the URL path
// Expected format: /peer/{leaseID}/... or /peer/{leaseID}
func extractLeaseID(urlPath string) string {
	// Remove leading slash
	urlPath = strings.TrimPrefix(urlPath, "/")

	// Split by slash
	parts := strings.Split(urlPath, "/")

	// Expected: ["peer", "{leaseID}", ...]
	if len(parts) < 2 || parts[0] != "peer" {
		return ""
	}

	return parts[1]
}

// getClientIP extracts the client IP address from the request
// Checks X-Forwarded-For and X-Real-IP headers first, then falls back to RemoteAddr
func getClientIP(r *http.Request) net.IP {
	// Check X-Forwarded-For header
	xff := r.Header.Get("X-Forwarded-For")
	if xff != "" {
		// Take the first IP in the list
		ips := strings.Split(xff, ",")
		if len(ips) > 0 {
			ip := net.ParseIP(strings.TrimSpace(ips[0]))
			if ip != nil {
				return ip
			}
		}
	}

	// Check X-Real-IP header
	xri := r.Header.Get("X-Real-IP")
	if xri != "" {
		ip := net.ParseIP(xri)
		if ip != nil {
			return ip
		}
	}

	// Fall back to RemoteAddr
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// Try parsing as IP directly
		ip := net.ParseIP(r.RemoteAddr)
		if ip != nil {
			return ip
		}
		return nil
	}

	return net.ParseIP(host)
}

// isIPAllowed checks if an IP is in any of the allowed ranges
func isIPAllowed(ip net.IP, allowedRanges []*net.IPNet) bool {
	if ip == nil {
		return false
	}

	for _, ipNet := range allowedRanges {
		if ipNet.Contains(ip) {
			return true
		}
	}

	return false
}

// contains checks if a string is in a slice
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// validateWildcardPattern validates a wildcard pattern
// Only allows * at the end of the pattern (e.g., "mcp-*")
func validateWildcardPattern(pattern string) error {
	if pattern == "" {
		return errors.New("pattern cannot be empty")
	}

	// Count asterisks
	count := strings.Count(pattern, "*")
	if count == 0 {
		return nil // No wildcard, valid
	}

	if count > 1 {
		return errors.New("only one wildcard (*) is allowed")
	}

	// Asterisk must be at the end
	if !strings.HasSuffix(pattern, "*") {
		return errors.New("wildcard (*) must be at the end of the pattern")
	}

	// Pattern must have at least one character before the asterisk
	if len(pattern) < 2 {
		return errors.New("pattern must have at least one character before wildcard")
	}

	return nil
}

// matchWildcard checks if a string matches a wildcard pattern
func matchWildcard(pattern, str string) bool {
	if !strings.Contains(pattern, "*") {
		return pattern == str
	}

	// Simple wildcard matching (only supports * at the end)
	prefix := strings.TrimSuffix(pattern, "*")
	return strings.HasPrefix(str, prefix)
}

// ParseCIDR parses a CIDR notation string into an IPNet
func ParseCIDR(cidr string) (*net.IPNet, error) {
	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrInvalidIPRange, err.Error())
	}
	return ipNet, nil
}

// ParseCIDRList parses a list of CIDR notation strings
func ParseCIDRList(cidrs []string) ([]*net.IPNet, error) {
	ipNets := make([]*net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		ipNet, err := ParseCIDR(cidr)
		if err != nil {
			return nil, err
		}
		ipNets = append(ipNets, ipNet)
	}
	return ipNets, nil
}

// GetLeaseID retrieves the lease ID from the request context
func GetLeaseID(ctx context.Context) string {
	leaseID, ok := ctx.Value(contextKey("lease_id")).(string)
	if !ok {
		return ""
	}
	return leaseID
}
