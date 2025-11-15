package circuitbreaker

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// newTestMetrics creates new metrics for testing with a fresh registry
func newTestMetrics() *Metrics {
	return NewMetricsWithRegistry(prometheus.NewRegistry())
}

func TestNewMiddleware(t *testing.T) {
	config := &MiddlewareConfig{
		MaxRequests:      3,
		Timeout:          30 * time.Second,
		FailureThreshold: 5,
		Metrics:          newTestMetrics(),
	}
	m := NewMiddleware(config)
	if m == nil {
		t.Fatal("Expected middleware to be created")
	}

	if m.config == nil {
		t.Fatal("Expected config to be set")
	}

	if m.config.Metrics == nil {
		t.Fatal("Expected metrics to be initialized")
	}
}

func TestMiddlewareWithoutLeaseID(t *testing.T) {
	config := &MiddlewareConfig{
		Metrics: newTestMetrics(),
	}
	m := NewMiddleware(config)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	wrapped := m.Middleware(handler)

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rr.Code)
	}
}

func TestMiddlewareWithLeaseID(t *testing.T) {
	config := &MiddlewareConfig{
		MaxRequests:      2,
		Timeout:          50 * time.Millisecond,
		FailureThreshold: 3,
		Metrics:          newTestMetrics(),
	}

	m := NewMiddleware(config)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	wrapped := m.Middleware(handler)

	ctx := context.WithValue(context.Background(), "lease_id", "test-lease")
	req := httptest.NewRequest("GET", "/test", nil).WithContext(ctx)
	rr := httptest.NewRecorder()

	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rr.Code)
	}

	// Check that breaker was created
	breakers := m.ListBreakers()
	if len(breakers) != 1 {
		t.Errorf("Expected 1 breaker, got %d", len(breakers))
	}

	if _, ok := breakers["test-lease"]; !ok {
		t.Error("Expected breaker for test-lease to exist")
	}
}

func TestMiddlewareCircuitTrips(t *testing.T) {
	config := &MiddlewareConfig{
		MaxRequests:      2,
		Timeout:          100 * time.Millisecond,
		FailureThreshold: 3,
		Metrics:          newTestMetrics(),
	}

	m := NewMiddleware(config)

	// Handler that returns 500 errors
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Error"))
	})

	wrapped := m.Middleware(handler)

	ctx := context.WithValue(context.Background(), "lease_id", "test-lease")

	// Send 3 failing requests to trip the breaker
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest("GET", "/test", nil).WithContext(ctx)
		rr := httptest.NewRecorder()
		wrapped.ServeHTTP(rr, req)

		if rr.Code != http.StatusInternalServerError {
			t.Errorf("Request %d: Expected status 500, got %d", i, rr.Code)
		}
	}

	// Verify breaker is open
	breaker := m.GetBreaker("test-lease")
	if breaker.State() != StateOpen {
		t.Errorf("Expected breaker state to be Open, got %v", breaker.State())
	}

	// Next request should be rejected by circuit breaker
	req := httptest.NewRequest("GET", "/test", nil).WithContext(ctx)
	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("Expected status 503, got %d", rr.Code)
	}
}

func TestMiddlewareCircuitRecovery(t *testing.T) {
	config := &MiddlewareConfig{
		MaxRequests:      2,
		Timeout:          50 * time.Millisecond,
		FailureThreshold: 3,
		Metrics:          newTestMetrics(),
	}

	m := NewMiddleware(config)

	failCount := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failCount < 3 {
			failCount++
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("Error"))
		} else {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("OK"))
		}
	})

	wrapped := m.Middleware(handler)

	ctx := context.WithValue(context.Background(), "lease_id", "test-lease")

	// Trip the breaker
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest("GET", "/test", nil).WithContext(ctx)
		rr := httptest.NewRecorder()
		wrapped.ServeHTTP(rr, req)
	}

	// Wait for timeout
	time.Sleep(60 * time.Millisecond)

	// Send successful requests to recover
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("GET", "/test", nil).WithContext(ctx)
		rr := httptest.NewRecorder()
		wrapped.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("Request %d: Expected status 200, got %d", i, rr.Code)
		}
	}

	// Verify breaker is closed
	breaker := m.GetBreaker("test-lease")
	if breaker.State() != StateClosed {
		t.Errorf("Expected breaker state to be Closed, got %v", breaker.State())
	}
}

func TestMiddlewarePerLeaseBreakers(t *testing.T) {
	config := &MiddlewareConfig{
		MaxRequests:      2,
		Timeout:          50 * time.Millisecond,
		FailureThreshold: 3,
		Metrics:          newTestMetrics(),
	}

	m := NewMiddleware(config)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	wrapped := m.Middleware(handler)

	// Create requests for different leases
	leases := []string{"lease-1", "lease-2", "lease-3"}

	for _, leaseID := range leases {
		ctx := context.WithValue(context.Background(), "lease_id", leaseID)
		req := httptest.NewRequest("GET", "/test", nil).WithContext(ctx)
		rr := httptest.NewRecorder()
		wrapped.ServeHTTP(rr, req)
	}

	// Check that separate breakers were created
	breakers := m.ListBreakers()
	if len(breakers) != 3 {
		t.Errorf("Expected 3 breakers, got %d", len(breakers))
	}

	for _, leaseID := range leases {
		if _, ok := breakers[leaseID]; !ok {
			t.Errorf("Expected breaker for %s to exist", leaseID)
		}
	}
}

func TestMiddlewareWithFallback(t *testing.T) {
	fallbackCalled := false
	fallbackHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackCalled = true
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Fallback"))
	})

	config := &MiddlewareConfig{
		MaxRequests:      2,
		Timeout:          50 * time.Millisecond,
		FailureThreshold: 3,
		Metrics:          newTestMetrics(),
		FallbackHandler:  fallbackHandler,
	}

	m := NewMiddleware(config)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Error"))
	})

	wrapped := m.Middleware(handler)

	ctx := context.WithValue(context.Background(), "lease_id", "test-lease")

	// Trip the breaker
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest("GET", "/test", nil).WithContext(ctx)
		rr := httptest.NewRecorder()
		wrapped.ServeHTTP(rr, req)
	}

	// Next request should use fallback
	req := httptest.NewRequest("GET", "/test", nil).WithContext(ctx)
	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	if !fallbackCalled {
		t.Error("Expected fallback handler to be called")
	}

	if rr.Code != http.StatusOK {
		t.Errorf("Expected status 200 from fallback, got %d", rr.Code)
	}
}

func TestMiddlewareResetBreaker(t *testing.T) {
	config := &MiddlewareConfig{
		MaxRequests:      2,
		Timeout:          50 * time.Millisecond,
		FailureThreshold: 3,
		Metrics:          newTestMetrics(),
	}

	m := NewMiddleware(config)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Error"))
	})

	wrapped := m.Middleware(handler)

	ctx := context.WithValue(context.Background(), "lease_id", "test-lease")

	// Trip the breaker
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest("GET", "/test", nil).WithContext(ctx)
		rr := httptest.NewRecorder()
		wrapped.ServeHTTP(rr, req)
	}

	breaker := m.GetBreaker("test-lease")
	if breaker.State() != StateOpen {
		t.Errorf("Expected breaker state to be Open, got %v", breaker.State())
	}

	// Reset the breaker
	reset := m.ResetBreaker("test-lease")
	if !reset {
		t.Error("Expected ResetBreaker to return true")
	}

	if breaker.State() != StateClosed {
		t.Errorf("Expected breaker state to be Closed after reset, got %v", breaker.State())
	}
}

func TestMiddlewareResetNonexistentBreaker(t *testing.T) {
	m := NewMiddleware(nil)

	reset := m.ResetBreaker("nonexistent")
	if reset {
		t.Error("Expected ResetBreaker to return false for nonexistent breaker")
	}
}

func TestMiddlewareResetAll(t *testing.T) {
	config := &MiddlewareConfig{
		MaxRequests:      2,
		Timeout:          50 * time.Millisecond,
		FailureThreshold: 3,
		Metrics:          newTestMetrics(),
	}

	m := NewMiddleware(config)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Error"))
	})

	wrapped := m.Middleware(handler)

	// Create and trip multiple breakers
	leases := []string{"lease-1", "lease-2"}
	for _, leaseID := range leases {
		ctx := context.WithValue(context.Background(), "lease_id", leaseID)
		for i := 0; i < 3; i++ {
			req := httptest.NewRequest("GET", "/test", nil).WithContext(ctx)
			rr := httptest.NewRecorder()
			wrapped.ServeHTTP(rr, req)
		}
	}

	// Verify all breakers are open
	for _, leaseID := range leases {
		breaker := m.GetBreaker(leaseID)
		if breaker.State() != StateOpen {
			t.Errorf("Expected breaker %s to be Open, got %v", leaseID, breaker.State())
		}
	}

	// Reset all breakers
	m.ResetAll()

	// Verify all breakers are closed
	for _, leaseID := range leases {
		breaker := m.GetBreaker(leaseID)
		if breaker.State() != StateClosed {
			t.Errorf("Expected breaker %s to be Closed after reset, got %v", leaseID, breaker.State())
		}
	}
}

func TestMiddleware4xxNotFailure(t *testing.T) {
	config := &MiddlewareConfig{
		MaxRequests:      2,
		Timeout:          50 * time.Millisecond,
		FailureThreshold: 3,
		Metrics:          newTestMetrics(),
	}

	m := NewMiddleware(config)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("Bad Request"))
	})

	wrapped := m.Middleware(handler)

	ctx := context.WithValue(context.Background(), "lease_id", "test-lease")

	// Send multiple 4xx requests
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest("GET", "/test", nil).WithContext(ctx)
		rr := httptest.NewRecorder()
		wrapped.ServeHTTP(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Errorf("Request %d: Expected status 400, got %d", i, rr.Code)
		}
	}

	// Breaker should still be closed (4xx is not a failure)
	breaker := m.GetBreaker("test-lease")
	if breaker.State() != StateClosed {
		t.Errorf("Expected breaker state to be Closed, got %v", breaker.State())
	}
}

func TestGetLeaseID(t *testing.T) {
	tests := []struct {
		name     string
		ctx      interface{}
		expected string
	}{
		{
			name:     "nil context",
			ctx:      nil,
			expected: "",
		},
		{
			name:     "context with lease ID",
			ctx:      context.WithValue(context.Background(), "lease_id", "test-lease"),
			expected: "test-lease",
		},
		{
			name:     "context without lease ID",
			ctx:      context.Background(),
			expected: "",
		},
		{
			name:     "context with wrong type",
			ctx:      context.WithValue(context.Background(), "lease_id", 123),
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getLeaseID(tt.ctx)
			if result != tt.expected {
				t.Errorf("getLeaseID() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestResponseWriterCapture(t *testing.T) {
	rr := httptest.NewRecorder()
	wrapped := &responseWriter{
		ResponseWriter: rr,
		statusCode:     http.StatusOK,
	}

	wrapped.WriteHeader(http.StatusNotFound)
	if wrapped.statusCode != http.StatusNotFound {
		t.Errorf("Expected status code 404, got %d", wrapped.statusCode)
	}

	data := []byte("test data")
	n, err := wrapped.Write(data)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	if n != len(data) {
		t.Errorf("Expected %d bytes written, got %d", len(data), n)
	}
}

func TestMiddlewareMetrics(t *testing.T) {
	metrics := newTestMetrics()
	config := &MiddlewareConfig{
		MaxRequests:      2,
		Timeout:          50 * time.Millisecond,
		FailureThreshold: 3,
		Metrics:          metrics,
	}

	m := NewMiddleware(config)

	if m.config.Metrics != metrics {
		t.Error("Expected metrics to be set correctly")
	}

	// Verify metrics are not nil
	if metrics.StateGauge == nil {
		t.Error("Expected StateGauge to be initialized")
	}
	if metrics.RequestsTotal == nil {
		t.Error("Expected RequestsTotal to be initialized")
	}
	if metrics.FailuresTotal == nil {
		t.Error("Expected FailuresTotal to be initialized")
	}
	if metrics.StateChangesTotal == nil {
		t.Error("Expected StateChangesTotal to be initialized")
	}
	if metrics.RejectedTotal == nil {
		t.Error("Expected RejectedTotal to be initialized")
	}
}

// Benchmark tests
func BenchmarkMiddleware(b *testing.B) {
	config := &MiddlewareConfig{
		Metrics: newTestMetrics(),
	}
	m := NewMiddleware(config)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "OK")
	})

	wrapped := m.Middleware(handler)

	ctx := context.WithValue(context.Background(), "lease_id", "test-lease")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest("GET", "/test", nil).WithContext(ctx)
		rr := httptest.NewRecorder()
		wrapped.ServeHTTP(rr, req)
	}
}
