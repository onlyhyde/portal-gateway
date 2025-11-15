package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestNewRateLimiter tests creating a new rate limiter
func TestNewRateLimiter(t *testing.T) {
	limiter := NewRateLimiter(10.0, 20)

	if limiter.rate != 10.0 {
		t.Errorf("Expected rate 10.0, got %f", limiter.rate)
	}

	if limiter.burst != 20 {
		t.Errorf("Expected burst 20, got %d", limiter.burst)
	}

	if limiter.tokens != 20.0 {
		t.Errorf("Expected initial tokens 20.0, got %f", limiter.tokens)
	}
}

// TestNewRateLimiterDefaults tests default values
func TestNewRateLimiterDefaults(t *testing.T) {
	limiter := NewRateLimiter(0, 0)

	if limiter.rate <= 0 {
		t.Error("Rate should have default value")
	}

	if limiter.burst <= 0 {
		t.Error("Burst should have default value")
	}
}

// TestRateLimiterAllow tests the Allow method
func TestRateLimiterAllow(t *testing.T) {
	// Create limiter with 10 req/s, burst of 10
	limiter := NewRateLimiter(10.0, 10)

	// Should allow first 10 requests (burst)
	for i := 0; i < 10; i++ {
		if !limiter.Allow() {
			t.Errorf("Request %d should be allowed (within burst)", i+1)
		}
	}

	// 11th request should be denied (burst exhausted)
	if limiter.Allow() {
		t.Error("Request 11 should be denied (burst exhausted)")
	}

	// Wait for tokens to refill
	time.Sleep(200 * time.Millisecond) // Should refill ~2 tokens at 10/s

	// Should allow 1-2 more requests
	allowed := 0
	for i := 0; i < 3; i++ {
		if limiter.Allow() {
			allowed++
		}
	}

	if allowed < 1 {
		t.Error("At least 1 request should be allowed after token refill")
	}
}

// TestRateLimiterRemaining tests the Remaining method
func TestRateLimiterRemaining(t *testing.T) {
	limiter := NewRateLimiter(10.0, 10)

	// Initial remaining should be burst size
	remaining := limiter.Remaining()
	if remaining != 10 {
		t.Errorf("Expected 10 remaining, got %d", remaining)
	}

	// Use 5 tokens
	for i := 0; i < 5; i++ {
		limiter.Allow()
	}

	remaining = limiter.Remaining()
	if remaining != 5 {
		t.Errorf("Expected 5 remaining after using 5 tokens, got %d", remaining)
	}
}

// TestRateLimiterReset tests the Reset method
func TestRateLimiterReset(t *testing.T) {
	limiter := NewRateLimiter(10.0, 10)

	// Exhaust all tokens
	for i := 0; i < 10; i++ {
		limiter.Allow()
	}

	// Get reset time
	reset := limiter.Reset()
	now := time.Now()

	// Reset should be in the future
	if !reset.After(now) {
		t.Error("Reset time should be in the future when tokens are exhausted")
	}

	// Reset should be within reasonable time (< 1 second for 10 req/s)
	if reset.Sub(now) > time.Second {
		t.Errorf("Reset time too far in future: %v", reset.Sub(now))
	}
}

// TestNewRateLimitConfig tests creating a new rate limit configuration
func TestNewRateLimitConfig(t *testing.T) {
	config := NewRateLimitConfig(100, 200)

	if config.RequestsPerSecond != 100 {
		t.Errorf("Expected RequestsPerSecond 100, got %f", config.RequestsPerSecond)
	}

	if config.BurstSize != 200 {
		t.Errorf("Expected BurstSize 200, got %d", config.BurstSize)
	}

	if config.limiters == nil {
		t.Error("Limiters map should be initialized")
	}

	if config.lastUsed == nil {
		t.Error("LastUsed map should be initialized")
	}
}

// TestGetLimiter tests getting or creating rate limiters
func TestGetLimiter(t *testing.T) {
	config := NewRateLimitConfig(100, 200)

	// Get limiter for key1
	limiter1 := config.GetLimiter("key1", 10, 20)
	if limiter1 == nil {
		t.Fatal("Limiter should not be nil")
	}

	// Get same limiter again
	limiter2 := config.GetLimiter("key1", 10, 20)
	if limiter1 != limiter2 {
		t.Error("Should return same limiter instance for same key")
	}

	// Get different limiter for key2
	limiter3 := config.GetLimiter("key2", 10, 20)
	if limiter1 == limiter3 {
		t.Error("Should return different limiter for different key")
	}
}

// TestCleanupExpiredLimiters tests limiter cleanup
func TestCleanupExpiredLimiters(t *testing.T) {
	config := NewRateLimitConfig(100, 200)
	config.LimiterTTL = 100 * time.Millisecond

	// Create some limiters
	config.GetLimiter("key1", 10, 20)
	config.GetLimiter("key2", 10, 20)

	// Wait for TTL to expire
	time.Sleep(150 * time.Millisecond)

	// Create one more recent limiter
	config.GetLimiter("key3", 10, 20)

	// Cleanup
	config.CleanupExpiredLimiters()

	// Check stats
	active, total := config.GetStats()

	// key1 and key2 should be cleaned up, key3 should remain
	if active != 1 {
		t.Errorf("Expected 1 active limiter after cleanup, got %d", active)
	}

	if total != 1 {
		t.Errorf("Expected 1 total key after cleanup, got %d", total)
	}
}

// TestGetStats tests getting rate limiter statistics
func TestGetStats(t *testing.T) {
	config := NewRateLimitConfig(100, 200)

	// Initially should be empty
	active, total := config.GetStats()
	if active != 0 || total != 0 {
		t.Errorf("Expected 0 limiters initially, got active=%d, total=%d", active, total)
	}

	// Add some limiters
	config.GetLimiter("key1", 10, 20)
	config.GetLimiter("key2", 10, 20)
	config.GetLimiter("key3", 10, 20)

	active, total = config.GetStats()
	if active != 3 || total != 3 {
		t.Errorf("Expected 3 limiters, got active=%d, total=%d", active, total)
	}
}

// TestRateLimitMiddleware tests the rate limit middleware
func TestRateLimitMiddleware(t *testing.T) {
	config := NewRateLimitConfig(10, 10)
	config.PerKeyRequestsPerSecond = 5
	config.PerKeyBurstSize = 5

	middleware := NewRateLimitMiddleware(config)
	defer middleware.Stop()

	// Create test handler
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	})

	// Wrap with rate limit middleware
	wrappedHandler := middleware.Middleware(handler)

	// Create request with API key context
	req := httptest.NewRequest("GET", "/test", nil)
	ctx := context.WithValue(req.Context(), ContextKeyAPIKey, &APIKeyInfo{
		KeyID: "test_key",
	})
	req = req.WithContext(ctx)

	// First 5 requests should succeed (burst)
	for i := 0; i < 5; i++ {
		rr := httptest.NewRecorder()
		wrappedHandler.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("Request %d: expected status 200, got %d", i+1, rr.Code)
		}

		// Check rate limit headers
		limit := rr.Header().Get("X-RateLimit-Limit")
		if limit != "5" {
			t.Errorf("Request %d: expected X-RateLimit-Limit 5, got %s", i+1, limit)
		}
	}

	// 6th request should be rate limited
	rr := httptest.NewRecorder()
	wrappedHandler.ServeHTTP(rr, req)

	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("Request 6: expected status 429, got %d", rr.Code)
	}

	// Check rate limit headers
	remaining := rr.Header().Get("X-RateLimit-Remaining")
	if remaining != "0" {
		t.Errorf("Expected X-RateLimit-Remaining 0, got %s", remaining)
	}

	retryAfter := rr.Header().Get("Retry-After")
	if retryAfter == "" {
		t.Error("Retry-After header should be set")
	}

	// Check error response
	if !strings.Contains(rr.Body.String(), "rate_limit_exceeded") {
		t.Error("Response should contain rate_limit_exceeded error")
	}
}

// TestRateLimitMiddlewareIPFallback tests IP-based rate limiting
func TestRateLimitMiddlewareIPFallback(t *testing.T) {
	config := NewRateLimitConfig(10, 10)
	config.PerIPRequestsPerSecond = 2
	config.PerIPBurstSize = 2

	middleware := NewRateLimitMiddleware(config)
	defer middleware.Stop()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	wrappedHandler := middleware.Middleware(handler)

	// Create request without API key (will use IP-based limiting)
	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "203.0.113.1:12345"

	// First 2 requests should succeed
	for i := 0; i < 2; i++ {
		rr := httptest.NewRecorder()
		wrappedHandler.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("Request %d: expected status 200, got %d", i+1, rr.Code)
		}
	}

	// 3rd request should be rate limited
	rr := httptest.NewRecorder()
	wrappedHandler.ServeHTTP(rr, req)

	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("Request 3: expected status 429, got %d", rr.Code)
	}
}

// TestRateLimitMiddlewareConcurrent tests concurrent requests
func TestRateLimitMiddlewareConcurrent(t *testing.T) {
	config := NewRateLimitConfig(100, 100)
	middleware := NewRateLimitMiddleware(config)
	defer middleware.Stop()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	wrappedHandler := middleware.Middleware(handler)

	// Run concurrent requests
	var wg sync.WaitGroup
	successCount := 0
	rateLimitedCount := 0
	var mu sync.Mutex

	numRequests := 150
	for i := 0; i < numRequests; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			req := httptest.NewRequest("GET", "/test", nil)
			ctx := context.WithValue(req.Context(), ContextKeyAPIKey, &APIKeyInfo{
				KeyID: "test_key",
			})
			req = req.WithContext(ctx)

			rr := httptest.NewRecorder()
			wrappedHandler.ServeHTTP(rr, req)

			mu.Lock()
			if rr.Code == http.StatusOK {
				successCount++
			} else if rr.Code == http.StatusTooManyRequests {
				rateLimitedCount++
			}
			mu.Unlock()
		}(i)
	}

	wg.Wait()

	// Some requests should succeed (within burst + refill)
	if successCount == 0 {
		t.Error("No requests succeeded")
	}

	// Some requests should be rate limited
	if rateLimitedCount == 0 {
		t.Error("No requests were rate limited")
	}

	total := successCount + rateLimitedCount
	if total != numRequests {
		t.Errorf("Expected %d total requests, got %d", numRequests, total)
	}
}

// TestRateLimitHeadersFormat tests rate limit header format
func TestRateLimitHeadersFormat(t *testing.T) {
	config := NewRateLimitConfig(10, 10)
	middleware := NewRateLimitMiddleware(config)
	defer middleware.Stop()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	wrappedHandler := middleware.Middleware(handler)

	req := httptest.NewRequest("GET", "/test", nil)
	ctx := context.WithValue(req.Context(), ContextKeyAPIKey, &APIKeyInfo{
		KeyID: "test_key",
	})
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	wrappedHandler.ServeHTTP(rr, req)

	// Verify header formats
	limit := rr.Header().Get("X-RateLimit-Limit")
	if _, err := strconv.Atoi(limit); err != nil {
		t.Errorf("X-RateLimit-Limit should be numeric, got %s", limit)
	}

	remaining := rr.Header().Get("X-RateLimit-Remaining")
	if _, err := strconv.Atoi(remaining); err != nil {
		t.Errorf("X-RateLimit-Remaining should be numeric, got %s", remaining)
	}

	reset := rr.Header().Get("X-RateLimit-Reset")
	if _, err := strconv.ParseInt(reset, 10, 64); err != nil {
		t.Errorf("X-RateLimit-Reset should be unix timestamp, got %s", reset)
	}
}

// TestRateLimitMiddlewareStop tests stopping the middleware
func TestRateLimitMiddlewareStop(t *testing.T) {
	config := NewRateLimitConfig(10, 10)
	middleware := NewRateLimitMiddleware(config)

	// Stop should not panic
	middleware.Stop()

	// Calling Stop again should not panic
	middleware.Stop()
}

// TestTokenRefill tests that tokens refill over time
func TestTokenRefill(t *testing.T) {
	limiter := NewRateLimiter(10.0, 5)

	// Exhaust all tokens
	for i := 0; i < 5; i++ {
		if !limiter.Allow() {
			t.Fatalf("Initial request %d should be allowed", i+1)
		}
	}

	// Should be denied immediately
	if limiter.Allow() {
		t.Error("Request should be denied when tokens exhausted")
	}

	// Wait for token refill (100ms = 1 token at 10/s)
	time.Sleep(150 * time.Millisecond)

	// Should allow 1 request now
	if !limiter.Allow() {
		t.Error("Request should be allowed after token refill")
	}
}
