package middleware

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// RateLimiter implements token bucket rate limiting
type RateLimiter struct {
	rate       float64       // Tokens per second
	burst      int           // Maximum burst size
	tokens     float64       // Current token count
	lastUpdate time.Time     // Last token refill time
	mu         sync.Mutex
}

// RateLimitConfig holds rate limiting configuration
type RateLimitConfig struct {
	// Global rate limiting
	RequestsPerSecond float64
	BurstSize         int

	// Per-API-key rate limiting
	PerKeyRequestsPerSecond float64
	PerKeyBurstSize         int

	// Per-IP rate limiting (fallback)
	PerIPRequestsPerSecond float64
	PerIPBurstSize         int

	// Limiter cache settings
	LimiterTTL      time.Duration // How long to keep inactive limiters
	CleanupInterval time.Duration // How often to clean up expired limiters

	mu       sync.RWMutex
	limiters map[string]*RateLimiter // key -> limiter
	lastUsed map[string]time.Time    // key -> last use time
}

// RateLimitMiddleware provides rate limiting
type RateLimitMiddleware struct {
	config  *RateLimitConfig
	stopCh  chan struct{}
	stopMu  sync.Mutex
	stopped bool
}

// Common errors
var (
	ErrRateLimitExceeded = errors.New("rate limit exceeded")
	ErrInvalidRateLimit  = errors.New("invalid rate limit configuration")
)

// NewRateLimiter creates a new token bucket rate limiter
func NewRateLimiter(rate float64, burst int) *RateLimiter {
	if rate <= 0 {
		rate = 10 // Default: 10 requests per second
	}
	if burst <= 0 {
		burst = int(rate * 2) // Default: 2x the rate
	}

	return &RateLimiter{
		rate:       rate,
		burst:      burst,
		tokens:     float64(burst), // Start with full bucket
		lastUpdate: time.Now(),
	}
}

// Allow checks if a request is allowed under the rate limit
// Returns true if allowed, false if rate limit exceeded
func (rl *RateLimiter) Allow() bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(rl.lastUpdate).Seconds()

	// Refill tokens based on elapsed time
	rl.tokens += elapsed * rl.rate
	if rl.tokens > float64(rl.burst) {
		rl.tokens = float64(rl.burst)
	}

	rl.lastUpdate = now

	// Check if we have tokens available
	if rl.tokens >= 1.0 {
		rl.tokens--
		return true
	}

	return false
}

// Remaining returns the number of tokens remaining
func (rl *RateLimiter) Remaining() int {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(rl.lastUpdate).Seconds()

	// Calculate current tokens
	tokens := rl.tokens + elapsed*rl.rate
	if tokens > float64(rl.burst) {
		tokens = float64(rl.burst)
	}

	return int(tokens)
}

// Reset returns when the rate limiter will have tokens available
func (rl *RateLimiter) Reset() time.Time {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	if rl.tokens >= 1.0 {
		return time.Now()
	}

	// Calculate when we'll have 1 token
	tokensNeeded := 1.0 - rl.tokens
	secondsNeeded := tokensNeeded / rl.rate

	return time.Now().Add(time.Duration(secondsNeeded * float64(time.Second)))
}

// NewRateLimitConfig creates a new rate limit configuration
func NewRateLimitConfig(requestsPerSecond float64, burstSize int) *RateLimitConfig {
	if requestsPerSecond <= 0 {
		requestsPerSecond = 100 // Default: 100 req/s
	}
	if burstSize <= 0 {
		burstSize = int(requestsPerSecond * 2) // Default: 2x the rate
	}

	config := &RateLimitConfig{
		RequestsPerSecond:       requestsPerSecond,
		BurstSize:               burstSize,
		PerKeyRequestsPerSecond: requestsPerSecond,
		PerKeyBurstSize:         burstSize,
		PerIPRequestsPerSecond:  requestsPerSecond / 10, // More restrictive for IPs
		PerIPBurstSize:          burstSize / 10,
		LimiterTTL:              10 * time.Minute,
		CleanupInterval:         5 * time.Minute,
		limiters:                make(map[string]*RateLimiter),
		lastUsed:                make(map[string]time.Time),
	}

	return config
}

// GetLimiter returns or creates a rate limiter for the given key
func (c *RateLimitConfig) GetLimiter(key string, rate float64, burst int) *RateLimiter {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Update last used time
	c.lastUsed[key] = time.Now()

	// Return existing limiter if present
	if limiter, exists := c.limiters[key]; exists {
		return limiter
	}

	// Create new limiter
	limiter := NewRateLimiter(rate, burst)
	c.limiters[key] = limiter

	return limiter
}

// CleanupExpiredLimiters removes limiters that haven't been used recently
func (c *RateLimitConfig) CleanupExpiredLimiters() {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	for key, lastUsed := range c.lastUsed {
		if now.Sub(lastUsed) > c.LimiterTTL {
			delete(c.limiters, key)
			delete(c.lastUsed, key)
		}
	}
}

// GetStats returns statistics about rate limiters
func (c *RateLimitConfig) GetStats() (activeLimiters, totalKeys int) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return len(c.limiters), len(c.lastUsed)
}

// NewRateLimitMiddleware creates a new rate limit middleware
func NewRateLimitMiddleware(config *RateLimitConfig) *RateLimitMiddleware {
	if config == nil {
		config = NewRateLimitConfig(100, 200)
	}

	m := &RateLimitMiddleware{
		config: config,
		stopCh: make(chan struct{}),
	}

	// Start cleanup goroutine
	go m.cleanupLoop()

	return m
}

// cleanupLoop periodically cleans up expired rate limiters
func (m *RateLimitMiddleware) cleanupLoop() {
	ticker := time.NewTicker(m.config.CleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			m.config.CleanupExpiredLimiters()
		case <-m.stopCh:
			return
		}
	}
}

// Stop stops the cleanup goroutine
func (m *RateLimitMiddleware) Stop() {
	m.stopMu.Lock()
	defer m.stopMu.Unlock()

	if !m.stopped {
		close(m.stopCh)
		m.stopped = true
	}
}

// Middleware returns an http.Handler that performs rate limiting
func (m *RateLimitMiddleware) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Determine rate limit key (prefer API key, fallback to IP)
		var limiterKey string
		var rate float64
		var burst int

		// Try to get API key info from context
		apiKeyInfo := GetAPIKeyInfo(r.Context())
		if apiKeyInfo != nil {
			limiterKey = "key:" + apiKeyInfo.KeyID
			rate = m.config.PerKeyRequestsPerSecond
			burst = m.config.PerKeyBurstSize
		} else {
			// Fallback to IP-based rate limiting
			clientIP := getClientIP(r)
			if clientIP != nil {
				limiterKey = "ip:" + clientIP.String()
			} else {
				limiterKey = "ip:unknown"
			}
			rate = m.config.PerIPRequestsPerSecond
			burst = m.config.PerIPBurstSize
		}

		// Get rate limiter for this key
		limiter := m.config.GetLimiter(limiterKey, rate, burst)

		// Check if request is allowed
		if !limiter.Allow() {
			m.handleRateLimitExceeded(w, limiter, burst)
			return
		}

		// Add rate limit headers
		m.addRateLimitHeaders(w, limiter, burst)

		// Call next handler
		next.ServeHTTP(w, r)
	})
}

// addRateLimitHeaders adds rate limit headers to the response
func (m *RateLimitMiddleware) addRateLimitHeaders(w http.ResponseWriter, limiter *RateLimiter, limit int) {
	remaining := limiter.Remaining()
	if remaining < 0 {
		remaining = 0
	}

	reset := limiter.Reset()

	w.Header().Set("X-RateLimit-Limit", strconv.Itoa(limit))
	w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
	w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(reset.Unix(), 10))
}

// handleRateLimitExceeded handles rate limit exceeded responses
func (m *RateLimitMiddleware) handleRateLimitExceeded(w http.ResponseWriter, limiter *RateLimiter, limit int) {
	reset := limiter.Reset()
	retryAfter := int(time.Until(reset).Seconds()) + 1
	if retryAfter < 0 {
		retryAfter = 1
	}

	// Add headers
	w.Header().Set("X-RateLimit-Limit", strconv.Itoa(limit))
	w.Header().Set("X-RateLimit-Remaining", "0")
	w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(reset.Unix(), 10))
	w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
	w.Header().Set("Content-Type", "application/json")

	w.WriteHeader(http.StatusTooManyRequests)
	fmt.Fprintf(w, `{"error":"rate_limit_exceeded","message":"Rate limit exceeded. Retry after %d seconds.","retry_after":%d}`, retryAfter, retryAfter)
}
