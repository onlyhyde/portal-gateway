package middleware

import (
	"errors"
	"fmt"
	"net/http"
	"sync"
)

// LeaseRateLimitRule defines rate limit for a specific lease
type LeaseRateLimitRule struct {
	LeaseID           string  // Lease ID (supports wildcards like "mcp-*")
	RequestsPerSecond float64 // Rate limit for this lease
	BurstSize         int     // Burst capacity
}

// LeaseRateLimitConfig manages per-lease rate limiting
type LeaseRateLimitConfig struct {
	Rules        map[string]*LeaseRateLimitRule // leaseID -> rule
	DefaultRate  float64                        // Default rate for unconfigured leases
	DefaultBurst int                            // Default burst for unconfigured leases
	mu           sync.RWMutex
}

// LeaseRateLimitMiddleware provides lease-specific rate limiting
type LeaseRateLimitMiddleware struct {
	config            *LeaseRateLimitConfig
	rateLimitConfig   *RateLimitConfig // Underlying rate limiter
	rateLimitMiddleware *RateLimitMiddleware
}

// Common errors
var (
	ErrLeaseRuleDuplicate = errors.New("lease rate limit rule already exists")
	ErrLeaseRuleNotFound  = errors.New("lease rate limit rule not found")
)

// NewLeaseRateLimitConfig creates a new lease-specific rate limit configuration
func NewLeaseRateLimitConfig(defaultRate float64, defaultBurst int) *LeaseRateLimitConfig {
	if defaultRate <= 0 {
		defaultRate = 50.0 // Default: 50 req/s per lease
	}
	if defaultBurst <= 0 {
		defaultBurst = int(defaultRate * 2)
	}

	return &LeaseRateLimitConfig{
		Rules:        make(map[string]*LeaseRateLimitRule),
		DefaultRate:  defaultRate,
		DefaultBurst: defaultBurst,
	}
}

// AddRule adds a rate limit rule for a lease
func (c *LeaseRateLimitConfig) AddRule(rule *LeaseRateLimitRule) error {
	if rule == nil {
		return errors.New("lease rate limit rule cannot be nil")
	}

	if rule.LeaseID == "" {
		return errors.New("lease ID cannot be empty")
	}

	if rule.RequestsPerSecond <= 0 {
		return errors.New("requests per second must be positive")
	}

	if rule.BurstSize <= 0 {
		rule.BurstSize = int(rule.RequestsPerSecond * 2)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.Rules[rule.LeaseID]; exists {
		return fmt.Errorf("%w: %s", ErrLeaseRuleDuplicate, rule.LeaseID)
	}

	c.Rules[rule.LeaseID] = rule
	return nil
}

// UpdateRule updates a rate limit rule for a lease
func (c *LeaseRateLimitConfig) UpdateRule(rule *LeaseRateLimitRule) error {
	if rule == nil {
		return errors.New("lease rate limit rule cannot be nil")
	}

	if rule.LeaseID == "" {
		return errors.New("lease ID cannot be empty")
	}

	if rule.RequestsPerSecond <= 0 {
		return errors.New("requests per second must be positive")
	}

	if rule.BurstSize <= 0 {
		rule.BurstSize = int(rule.RequestsPerSecond * 2)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.Rules[rule.LeaseID] = rule
	return nil
}

// RemoveRule removes a rate limit rule for a lease
func (c *LeaseRateLimitConfig) RemoveRule(leaseID string) error {
	if leaseID == "" {
		return errors.New("lease ID cannot be empty")
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.Rules[leaseID]; !exists {
		return fmt.Errorf("%w: %s", ErrLeaseRuleNotFound, leaseID)
	}

	delete(c.Rules, leaseID)
	return nil
}

// GetRule retrieves a rate limit rule for a lease
// Returns nil if no specific rule exists (will use default)
func (c *LeaseRateLimitConfig) GetRule(leaseID string) *LeaseRateLimitRule {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// Try exact match first
	if rule, exists := c.Rules[leaseID]; exists {
		return rule
	}

	// Try wildcard match
	for pattern, rule := range c.Rules {
		if matchWildcard(pattern, leaseID) {
			return rule
		}
	}

	return nil
}

// GetRateLimit returns the rate limit for a lease (considering defaults)
func (c *LeaseRateLimitConfig) GetRateLimit(leaseID string) (rate float64, burst int) {
	rule := c.GetRule(leaseID)
	if rule != nil {
		return rule.RequestsPerSecond, rule.BurstSize
	}
	return c.DefaultRate, c.DefaultBurst
}

// ListRules returns all configured rules
func (c *LeaseRateLimitConfig) ListRules() []*LeaseRateLimitRule {
	c.mu.RLock()
	defer c.mu.RUnlock()

	rules := make([]*LeaseRateLimitRule, 0, len(c.Rules))
	for _, rule := range c.Rules {
		rules = append(rules, rule)
	}
	return rules
}

// NewLeaseRateLimitMiddleware creates a new lease-specific rate limit middleware
func NewLeaseRateLimitMiddleware(config *LeaseRateLimitConfig, baseRateLimitConfig *RateLimitConfig) *LeaseRateLimitMiddleware {
	if config == nil {
		config = NewLeaseRateLimitConfig(50, 100)
	}

	if baseRateLimitConfig == nil {
		baseRateLimitConfig = NewRateLimitConfig(100, 200)
	}

	return &LeaseRateLimitMiddleware{
		config:              config,
		rateLimitConfig:     baseRateLimitConfig,
		rateLimitMiddleware: NewRateLimitMiddleware(baseRateLimitConfig),
	}
}

// Stop stops the underlying rate limit middleware
func (m *LeaseRateLimitMiddleware) Stop() {
	if m.rateLimitMiddleware != nil {
		m.rateLimitMiddleware.Stop()
	}
}

// Middleware returns an http.Handler that performs lease-specific rate limiting
func (m *LeaseRateLimitMiddleware) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Get lease ID from context (set by ACL middleware)
		leaseID := GetLeaseID(r.Context())
		if leaseID == "" {
			// No lease ID, use default rate limiting
			m.rateLimitMiddleware.Middleware(next).ServeHTTP(w, r)
			return
		}

		// Get rate limit for this lease
		rate, burst := m.config.GetRateLimit(leaseID)

		// Determine limiter key
		var limiterKey string
		apiKeyInfo := GetAPIKeyInfo(r.Context())
		if apiKeyInfo != nil {
			limiterKey = fmt.Sprintf("lease:%s:key:%s", leaseID, apiKeyInfo.KeyID)
		} else {
			clientIP := getClientIP(r)
			if clientIP != nil {
				limiterKey = fmt.Sprintf("lease:%s:ip:%s", leaseID, clientIP.String())
			} else {
				limiterKey = fmt.Sprintf("lease:%s:ip:unknown", leaseID)
			}
		}

		// Get or create rate limiter for this lease
		limiter := m.rateLimitConfig.GetLimiter(limiterKey, rate, burst)

		// Check if request is allowed
		if !limiter.Allow() {
			m.rateLimitMiddleware.handleRateLimitExceeded(w, limiter, burst)
			return
		}

		// Add rate limit headers
		m.rateLimitMiddleware.addRateLimitHeaders(w, limiter, burst)

		// Call next handler
		next.ServeHTTP(w, r)
	})
}
