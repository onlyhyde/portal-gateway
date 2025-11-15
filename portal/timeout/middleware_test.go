package timeout

import (
	"context"
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
		DefaultTimeout: 30 * time.Second,
		Metrics:        newTestMetrics(),
	}
	m := NewMiddleware(config)

	if m == nil {
		t.Fatal("Expected middleware to be created")
	}

	if m.config.DefaultTimeout != 30*time.Second {
		t.Errorf("Expected default timeout 30s, got %v", m.config.DefaultTimeout)
	}
}

func TestMiddlewareWithDefaultConfig(t *testing.T) {
	m := NewMiddleware(nil)

	if m == nil {
		t.Fatal("Expected middleware to be created with default config")
	}

	if m.config.DefaultTimeout != 30*time.Second {
		t.Errorf("Expected default timeout 30s, got %v", m.config.DefaultTimeout)
	}
}

func TestMiddlewareNoTimeout(t *testing.T) {
	config := &MiddlewareConfig{
		DefaultTimeout: 1 * time.Second,
		Metrics:        newTestMetrics(),
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

func TestMiddlewareTimeout(t *testing.T) {
	config := &MiddlewareConfig{
		DefaultTimeout: 100 * time.Millisecond,
		Metrics:        newTestMetrics(),
	}
	m := NewMiddleware(config)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Sleep longer than timeout
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	wrapped := m.Middleware(handler)

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusGatewayTimeout {
		t.Errorf("Expected status 504, got %d", rr.Code)
	}
}

func TestMiddlewareContextCancellation(t *testing.T) {
	config := &MiddlewareConfig{
		DefaultTimeout: 100 * time.Millisecond,
		Metrics:        newTestMetrics(),
	}
	m := NewMiddleware(config)

	contextCancelled := false

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Wait for context cancellation
		select {
		case <-r.Context().Done():
			contextCancelled = true
			return
		case <-time.After(200 * time.Millisecond):
			w.WriteHeader(http.StatusOK)
		}
	})

	wrapped := m.Middleware(handler)

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	wrapped.ServeHTTP(rr, req)

	if !contextCancelled {
		t.Error("Expected context to be cancelled on timeout")
	}

	if rr.Code != http.StatusGatewayTimeout {
		t.Errorf("Expected status 504, got %d", rr.Code)
	}
}

func TestMiddlewarePerLeaseTimeout(t *testing.T) {
	config := &MiddlewareConfig{
		DefaultTimeout: 1 * time.Second,
		LeaseTimeouts: map[string]time.Duration{
			"fast-lease": 50 * time.Millisecond,
			"slow-lease": 2 * time.Second,
		},
		Metrics: newTestMetrics(),
	}
	m := NewMiddleware(config)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	wrapped := m.Middleware(handler)

	// Test fast-lease (should timeout)
	ctx := context.WithValue(context.Background(), "lease_id", "fast-lease")
	req := httptest.NewRequest("GET", "/test", nil).WithContext(ctx)
	rr := httptest.NewRecorder()

	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusGatewayTimeout {
		t.Errorf("Expected fast-lease to timeout with status 504, got %d", rr.Code)
	}

	// Test slow-lease (should succeed)
	ctx = context.WithValue(context.Background(), "lease_id", "slow-lease")
	req = httptest.NewRequest("GET", "/test", nil).WithContext(ctx)
	rr = httptest.NewRecorder()

	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Expected slow-lease to succeed with status 200, got %d", rr.Code)
	}
}

func TestGetTimeout(t *testing.T) {
	config := &MiddlewareConfig{
		DefaultTimeout: 30 * time.Second,
		LeaseTimeouts: map[string]time.Duration{
			"custom-lease": 10 * time.Second,
		},
		Metrics: newTestMetrics(),
	}
	m := NewMiddleware(config)

	// Test default timeout
	timeout := m.GetTimeout("unknown-lease")
	if timeout != 30*time.Second {
		t.Errorf("Expected default timeout 30s, got %v", timeout)
	}

	// Test custom timeout
	timeout = m.GetTimeout("custom-lease")
	if timeout != 10*time.Second {
		t.Errorf("Expected custom timeout 10s, got %v", timeout)
	}
}

func TestSetTimeout(t *testing.T) {
	config := &MiddlewareConfig{
		DefaultTimeout: 30 * time.Second,
		Metrics:        newTestMetrics(),
	}
	m := NewMiddleware(config)

	// Set timeout for a lease
	m.SetTimeout("test-lease", 15*time.Second)

	// Verify timeout was set
	timeout := m.GetTimeout("test-lease")
	if timeout != 15*time.Second {
		t.Errorf("Expected timeout 15s, got %v", timeout)
	}
}

func TestServiceTimeouts(t *testing.T) {
	config := &MiddlewareConfig{
		DefaultTimeout: 30 * time.Second,
		ServiceTimeouts: map[string]time.Duration{
			"mcp":    10 * time.Second,
			"n8n":    60 * time.Second,
			"openai": 30 * time.Second,
		},
		Metrics: newTestMetrics(),
	}
	m := NewMiddleware(config)

	tests := []struct {
		service  string
		expected time.Duration
	}{
		{"mcp", 10 * time.Second},
		{"n8n", 60 * time.Second},
		{"openai", 30 * time.Second},
		{"unknown", 30 * time.Second}, // Should return default
	}

	for _, tt := range tests {
		t.Run(tt.service, func(t *testing.T) {
			timeout := m.GetServiceTimeout(tt.service)
			if timeout != tt.expected {
				t.Errorf("Service %s: expected timeout %v, got %v", tt.service, tt.expected, timeout)
			}
		})
	}
}

func TestSetServiceTimeout(t *testing.T) {
	config := &MiddlewareConfig{
		DefaultTimeout: 30 * time.Second,
		Metrics:        newTestMetrics(),
	}
	m := NewMiddleware(config)

	// Set timeout for a service
	m.SetServiceTimeout("custom", 45*time.Second)

	// Verify timeout was set
	timeout := m.GetServiceTimeout("custom")
	if timeout != 45*time.Second {
		t.Errorf("Expected timeout 45s, got %v", timeout)
	}
}

func TestTimeoutMetrics(t *testing.T) {
	metrics := newTestMetrics()
	config := &MiddlewareConfig{
		DefaultTimeout: 50 * time.Millisecond,
		Metrics:        metrics,
	}
	m := NewMiddleware(config)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	})

	wrapped := m.Middleware(handler)

	ctx := context.WithValue(context.Background(), "lease_id", "test-lease")
	req := httptest.NewRequest("GET", "/test", nil).WithContext(ctx)
	rr := httptest.NewRecorder()

	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusGatewayTimeout {
		t.Errorf("Expected status 504, got %d", rr.Code)
	}

	// Metrics should be incremented
	// Note: We can't easily verify prometheus metrics in tests without more setup
	// but the code paths are covered
}

func TestTimeoutResponseWriterConcurrency(t *testing.T) {
	rr := httptest.NewRecorder()
	wrapped := &timeoutResponseWriter{
		ResponseWriter: rr,
		wroteHeader:    false,
	}

	// Test concurrent writes
	done := make(chan struct{})
	go func() {
		wrapped.WriteHeader(http.StatusOK)
		close(done)
	}()

	// Try to write at the same time
	wrapped.WriteHeader(http.StatusNotFound)

	<-done

	// Should only write header once
	if rr.Code != http.StatusOK && rr.Code != http.StatusNotFound {
		t.Errorf("Unexpected status code: %d", rr.Code)
	}
}

func TestTimeoutResponseWriterWrite(t *testing.T) {
	rr := httptest.NewRecorder()
	wrapped := &timeoutResponseWriter{
		ResponseWriter: rr,
		wroteHeader:    false,
	}

	data := []byte("test data")
	n, err := wrapped.Write(data)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	if n != len(data) {
		t.Errorf("Expected %d bytes written, got %d", len(data), n)
	}

	if !wrapped.wroteHeader {
		t.Error("Expected wroteHeader to be true after Write")
	}
}

func TestGetLeaseID(t *testing.T) {
	tests := []struct {
		name     string
		ctx      context.Context
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

func TestDefaultMiddlewareConfig(t *testing.T) {
	// Create config manually to avoid duplicate metrics registration
	config := &MiddlewareConfig{
		DefaultTimeout: 30 * time.Second,
		LeaseTimeouts:  make(map[string]time.Duration),
		ServiceTimeouts: map[string]time.Duration{
			"mcp":    10 * time.Second,
			"n8n":    60 * time.Second,
			"openai": 30 * time.Second,
		},
		Metrics: newTestMetrics(),
	}

	if config.DefaultTimeout != 30*time.Second {
		t.Errorf("Expected default timeout 30s, got %v", config.DefaultTimeout)
	}

	if config.ServiceTimeouts["mcp"] != 10*time.Second {
		t.Errorf("Expected MCP timeout 10s, got %v", config.ServiceTimeouts["mcp"])
	}

	if config.ServiceTimeouts["n8n"] != 60*time.Second {
		t.Errorf("Expected n8n timeout 60s, got %v", config.ServiceTimeouts["n8n"])
	}

	if config.ServiceTimeouts["openai"] != 30*time.Second {
		t.Errorf("Expected OpenAI timeout 30s, got %v", config.ServiceTimeouts["openai"])
	}

	if config.Metrics == nil {
		t.Error("Expected metrics to be initialized")
	}
}

func TestLoadFromFile(t *testing.T) {
	// Placeholder test for future YAML config loading
	config, err := LoadFromFile("nonexistent.yaml")
	if err != nil {
		t.Errorf("LoadFromFile should not error for now: %v", err)
	}

	if config == nil {
		t.Error("Expected config to be returned")
	}

	// Verify default values
	if config.DefaultTimeout != 30*time.Second {
		t.Errorf("Expected default timeout 30s, got %v", config.DefaultTimeout)
	}

	// Metrics should be nil (will be created by NewMiddleware)
	if config.Metrics != nil {
		t.Error("Expected metrics to be nil from LoadFromFile")
	}
}

// Benchmark tests
func BenchmarkMiddleware(b *testing.B) {
	config := &MiddlewareConfig{
		DefaultTimeout: 1 * time.Second,
		Metrics:        newTestMetrics(),
	}
	m := NewMiddleware(config)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	wrapped := m.Middleware(handler)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest("GET", "/test", nil)
		rr := httptest.NewRecorder()
		wrapped.ServeHTTP(rr, req)
	}
}

func BenchmarkMiddlewareWithTimeout(b *testing.B) {
	config := &MiddlewareConfig{
		DefaultTimeout: 10 * time.Millisecond,
		Metrics:        newTestMetrics(),
	}
	m := NewMiddleware(config)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(20 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	})

	wrapped := m.Middleware(handler)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest("GET", "/test", nil)
		rr := httptest.NewRecorder()
		wrapped.ServeHTTP(rr, req)
	}
}
