package streaming

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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
		EnableKeepAlive:   true,
		KeepAliveInterval: 30 * time.Second,
		Metrics:           newTestMetrics(),
	}
	m := NewMiddleware(config)

	if m == nil {
		t.Fatal("Expected middleware to be created")
	}

	if m.config.KeepAliveInterval != 30*time.Second {
		t.Errorf("Expected keep-alive interval 30s, got %v", m.config.KeepAliveInterval)
	}

	if m.config.Metrics == nil {
		t.Error("Expected metrics to be initialized")
	}
}

func TestNewMiddlewareWithDefaults(t *testing.T) {
	m := NewMiddleware(nil)

	if m == nil {
		t.Fatal("Expected middleware to be created with defaults")
	}

	if m.config.KeepAliveInterval != 30*time.Second {
		t.Errorf("Expected default keep-alive interval 30s, got %v", m.config.KeepAliveInterval)
	}

	if !m.config.EnableKeepAlive {
		t.Error("Expected keep-alive to be enabled by default")
	}

	if m.config.Metrics == nil {
		t.Error("Expected metrics to be initialized")
	}
}

func TestMiddlewareNonStreaming(t *testing.T) {
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

	if rr.Body.String() != "OK" {
		t.Errorf("Expected body 'OK', got %q", rr.Body.String())
	}
}

func TestMiddlewareSSE(t *testing.T) {
	config := &MiddlewareConfig{
		Metrics: newTestMetrics(),
	}
	m := NewMiddleware(config)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("data: test event\n\n"))
	})

	wrapped := m.Middleware(handler)

	req := httptest.NewRequest("GET", "/events", nil)
	req.Header.Set("Accept", "text/event-stream")
	rr := httptest.NewRecorder()

	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rr.Code)
	}

	// Check SSE headers
	if rr.Header().Get("Content-Type") != "text/event-stream" {
		t.Errorf("Expected Content-Type text/event-stream, got %q", rr.Header().Get("Content-Type"))
	}

	if rr.Header().Get("Cache-Control") != "no-cache" {
		t.Errorf("Expected Cache-Control no-cache, got %q", rr.Header().Get("Cache-Control"))
	}

	if rr.Header().Get("Connection") != "keep-alive" {
		t.Errorf("Expected Connection keep-alive, got %q", rr.Header().Get("Connection"))
	}

	if rr.Header().Get("X-Accel-Buffering") != "no" {
		t.Errorf("Expected X-Accel-Buffering no, got %q", rr.Header().Get("X-Accel-Buffering"))
	}
}

func TestMiddlewareStreaming(t *testing.T) {
	config := &MiddlewareConfig{
		Metrics: newTestMetrics(),
	}
	m := NewMiddleware(config)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for i := 0; i < 3; i++ {
			fmt.Fprintf(w, "chunk %d\n", i)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	})

	wrapped := m.Middleware(handler)

	req := httptest.NewRequest("GET", "/stream", nil)
	req.Header.Set("X-Stream", "true")
	rr := httptest.NewRecorder()

	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rr.Code)
	}

	body := rr.Body.String()
	if !strings.Contains(body, "chunk 0") {
		t.Errorf("Expected body to contain 'chunk 0', got %q", body)
	}
}

func TestIsSSERequest(t *testing.T) {
	config := &MiddlewareConfig{
		Metrics: newTestMetrics(),
	}
	m := NewMiddleware(config)

	tests := []struct {
		name     string
		accept   string
		expected bool
	}{
		{
			name:     "SSE Accept header",
			accept:   "text/event-stream",
			expected: true,
		},
		{
			name:     "SSE with multiple Accept",
			accept:   "text/html,text/event-stream,*/*",
			expected: true,
		},
		{
			name:     "Non-SSE Accept",
			accept:   "application/json",
			expected: false,
		},
		{
			name:     "Empty Accept",
			accept:   "",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/test", nil)
			req.Header.Set("Accept", tt.accept)

			result := m.isSSERequest(req)
			if result != tt.expected {
				t.Errorf("isSSERequest() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestIsStreamingRequest(t *testing.T) {
	config := &MiddlewareConfig{
		Metrics: newTestMetrics(),
	}
	m := NewMiddleware(config)

	tests := []struct {
		name     string
		headers  map[string]string
		expected bool
	}{
		{
			name: "X-Stream header",
			headers: map[string]string{
				"X-Stream": "true",
			},
			expected: true,
		},
		{
			name: "Chunked Transfer-Encoding",
			headers: map[string]string{
				"Transfer-Encoding": "chunked",
			},
			expected: true,
		},
		{
			name:     "No streaming headers",
			headers:  map[string]string{},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/test", nil)
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}

			result := m.isStreamingRequest(req)
			if result != tt.expected {
				t.Errorf("isStreamingRequest() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestStreamingResponseWriter(t *testing.T) {
	rr := httptest.NewRecorder()
	sw := &streamingResponseWriter{
		ResponseWriter: rr,
		metrics:        newTestMetrics(),
	}

	// Test Write
	data := []byte("test data")
	n, err := sw.Write(data)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	if n != len(data) {
		t.Errorf("Expected %d bytes written, got %d", len(data), n)
	}

	if sw.bytesWritten != int64(len(data)) {
		t.Errorf("Expected bytesWritten %d, got %d", len(data), sw.bytesWritten)
	}

	if !sw.headerWritten {
		t.Error("Expected headerWritten to be true after Write")
	}

	if rr.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rr.Code)
	}
}

func TestStreamingResponseWriterWriteHeader(t *testing.T) {
	rr := httptest.NewRecorder()
	sw := &streamingResponseWriter{
		ResponseWriter: rr,
		metrics:        newTestMetrics(),
	}

	sw.WriteHeader(http.StatusAccepted)

	if rr.Code != http.StatusAccepted {
		t.Errorf("Expected status 202, got %d", rr.Code)
	}

	if !sw.headerWritten {
		t.Error("Expected headerWritten to be true")
	}

	// Writing header again should not change it
	sw.WriteHeader(http.StatusNotFound)

	if rr.Code != http.StatusAccepted {
		t.Errorf("Expected status to remain 202, got %d", rr.Code)
	}
}

func TestStreamingResponseWriterFlush(t *testing.T) {
	rr := httptest.NewRecorder()
	sw := &streamingResponseWriter{
		ResponseWriter: rr,
		metrics:        newTestMetrics(),
	}

	// Flush should not panic
	sw.Flush()

	// Write some data
	sw.Write([]byte("test"))

	// Flush again
	sw.Flush()
}

func TestStreamingMetrics(t *testing.T) {
	metrics := newTestMetrics()
	config := &MiddlewareConfig{
		Metrics: metrics,
	}
	m := NewMiddleware(config)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("data"))
	})

	wrapped := m.Middleware(handler)

	req := httptest.NewRequest("GET", "/events", nil)
	req.Header.Set("Accept", "text/event-stream")
	rr := httptest.NewRecorder()

	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rr.Code)
	}

	// Metrics should be updated
	// Note: We can't easily verify prometheus metrics in tests without more setup
	// but the code paths are covered
}

func TestGetMetrics(t *testing.T) {
	metrics := newTestMetrics()
	config := &MiddlewareConfig{
		Metrics: metrics,
	}
	m := NewMiddleware(config)

	retrievedMetrics := m.GetMetrics()

	if retrievedMetrics != metrics {
		t.Error("Expected GetMetrics to return the same metrics instance")
	}
}

func TestDefaultMiddlewareConfig(t *testing.T) {
	config := DefaultMiddlewareConfig()

	if !config.EnableKeepAlive {
		t.Error("Expected EnableKeepAlive to be true")
	}

	if config.KeepAliveInterval != 30*time.Second {
		t.Errorf("Expected KeepAliveInterval 30s, got %v", config.KeepAliveInterval)
	}

	if config.Metrics != nil {
		t.Error("Expected metrics to be nil from DefaultMiddlewareConfig")
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
		w.Write([]byte("OK"))
	})

	wrapped := m.Middleware(handler)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest("GET", "/test", nil)
		rr := httptest.NewRecorder()
		wrapped.ServeHTTP(rr, req)
	}
}

func BenchmarkMiddlewareSSE(b *testing.B) {
	config := &MiddlewareConfig{
		Metrics: newTestMetrics(),
	}
	m := NewMiddleware(config)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("data: test\n\n"))
	})

	wrapped := m.Middleware(handler)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest("GET", "/events", nil)
		req.Header.Set("Accept", "text/event-stream")
		rr := httptest.NewRecorder()
		wrapped.ServeHTTP(rr, req)
	}
}
