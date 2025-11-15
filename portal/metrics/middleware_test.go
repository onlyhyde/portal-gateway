package metrics

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// TestNewMetricsMiddleware tests creating a new metrics middleware
func TestNewMetricsMiddleware(t *testing.T) {
	middleware := NewMetricsMiddleware(nil)
	if middleware == nil {
		t.Fatal("Expected middleware to be created, got nil")
	}

	if middleware.metrics == nil {
		t.Fatal("Expected metrics to be initialized, got nil")
	}
}

// TestMetricsMiddleware tests the metrics middleware
func TestMetricsMiddleware(t *testing.T) {
	// Create custom metrics registry to avoid conflicts
	reg := prometheus.NewRegistry()

	requestsTotal := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "test_requests_total",
			Help: "Total requests",
		},
		[]string{"method", "endpoint", "status", "lease_id"},
	)

	requestDuration := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name: "test_request_duration_seconds",
			Help: "Request duration",
		},
		[]string{"method", "endpoint", "lease_id"},
	)

	activeConnections := prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "test_active_connections",
			Help: "Active connections",
		},
	)

	reg.MustRegister(requestsTotal, requestDuration, activeConnections)

	metrics := &Metrics{
		RequestsTotal:     requestsTotal,
		RequestDuration:   requestDuration,
		ActiveConnections: activeConnections,
		ActiveLeases:      prometheus.NewGauge(prometheus.GaugeOpts{Name: "test_active_leases"}),
		BytesTransferredTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{Name: "test_bytes_transferred"},
			[]string{"direction", "lease_id"},
		),
	}

	middleware := NewMetricsMiddleware(metrics)

	// Create test handler
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	wrappedHandler := middleware.Middleware(handler)

	// Make request
	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	wrappedHandler.ServeHTTP(rr, req)

	// Verify response
	if rr.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rr.Code)
	}

	// Verify metrics were recorded (basic check)
	// Note: In a real test, we'd inspect the metric values
}

// TestMetricsResponseWriter tests the response writer wrapper
func TestMetricsResponseWriter(t *testing.T) {
	rr := httptest.NewRecorder()
	wrapped := &metricsResponseWriter{
		ResponseWriter: rr,
		statusCode:     http.StatusOK,
	}

	// Write header
	wrapped.WriteHeader(http.StatusCreated)
	if wrapped.statusCode != http.StatusCreated {
		t.Errorf("Expected status code 201, got %d", wrapped.statusCode)
	}

	// Write body
	data := []byte("test data")
	n, err := wrapped.Write(data)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	if n != len(data) {
		t.Errorf("Expected %d bytes written, got %d", len(data), n)
	}

	if wrapped.bytesWritten != len(data) {
		t.Errorf("Expected bytesWritten %d, got %d", len(data), wrapped.bytesWritten)
	}
}

// TestSanitizeEndpoint tests endpoint path sanitization
func TestSanitizeEndpoint(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		expected string
	}{
		{
			name:     "peer endpoint",
			path:     "/peer/lease-123",
			expected: "/peer/{lease_id}",
		},
		{
			name:     "peer endpoint with trailing slash",
			path:     "/peer/lease-123/",
			expected: "/peer/{lease_id}",
		},
		{
			name:     "admin acl endpoint",
			path:     "/admin/acl/lease-456",
			expected: "/admin/acl/{lease_id}",
		},
		{
			name:     "admin quota endpoint",
			path:     "/admin/quota/sk_test_123",
			expected: "/admin/quota/{key_id}",
		},
		{
			name:     "admin quota reset",
			path:     "/admin/quota/sk_test_123/reset",
			expected: "/admin/quota/{key_id}/reset",
		},
		{
			name:     "health endpoint",
			path:     "/health",
			expected: "/health",
		},
		{
			name:     "metrics endpoint",
			path:     "/metrics",
			expected: "/metrics",
		},
		{
			name:     "root",
			path:     "/",
			expected: "/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeEndpoint(tt.path)
			if result != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, result)
			}
		})
	}
}

// TestGetLeaseIDFromContext tests lease ID extraction from context
func TestGetLeaseIDFromContext(t *testing.T) {
	// Test with nil context
	leaseID := getLeaseIDFromContext(nil)
	if leaseID != "" {
		t.Errorf("Expected empty string for nil context, got %q", leaseID)
	}

	// Test with context without lease ID
	ctx := context.Background()
	leaseID = getLeaseIDFromContext(ctx)
	if leaseID != "" {
		t.Errorf("Expected empty string for context without lease ID, got %q", leaseID)
	}

	// Note: Testing with lease ID requires the exact context key type used in middleware
	// which may be defined in another package. Skipping positive test case.
}

// TestRecordRateLimitExceeded tests rate limit recording
func TestRecordRateLimitExceeded(t *testing.T) {
	reg := prometheus.NewRegistry()

	rateLimitExceeded := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "test_rate_limit_exceeded",
			Help: "Rate limits exceeded",
		},
		[]string{"lease_id", "limit_type"},
	)

	reg.MustRegister(rateLimitExceeded)

	metrics := &Metrics{
		RateLimitExceeded: rateLimitExceeded,
	}

	middleware := NewMetricsMiddleware(metrics)

	// Record rate limit exceeded
	middleware.RecordRateLimitExceeded("test-lease", "global")

	// Verify metric was incremented
	counter, err := rateLimitExceeded.GetMetricWithLabelValues("test-lease", "global")
	if err != nil {
		t.Fatalf("Failed to get metric: %v", err)
	}

	metric := &dto.Metric{}
	counter.Write(metric)

	if metric.Counter.GetValue() != 1 {
		t.Errorf("Expected counter value 1, got %f", metric.Counter.GetValue())
	}
}

// TestRecordQuotaExceeded tests quota exceeded recording
func TestRecordQuotaExceeded(t *testing.T) {
	reg := prometheus.NewRegistry()

	quotaExceeded := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "test_quota_exceeded",
			Help: "Quota exceeded",
		},
		[]string{"key_id", "quota_type"},
	)

	reg.MustRegister(quotaExceeded)

	metrics := &Metrics{
		QuotaExceeded: quotaExceeded,
	}

	middleware := NewMetricsMiddleware(metrics)

	// Record quota exceeded
	middleware.RecordQuotaExceeded("test-key", "requests")

	// Verify metric was incremented
	counter, err := quotaExceeded.GetMetricWithLabelValues("test-key", "requests")
	if err != nil {
		t.Fatalf("Failed to get metric: %v", err)
	}

	metric := &dto.Metric{}
	counter.Write(metric)

	if metric.Counter.GetValue() != 1 {
		t.Errorf("Expected counter value 1, got %f", metric.Counter.GetValue())
	}
}

// TestRecordAIAgentRequest tests AI agent request recording
func TestRecordAIAgentRequest(t *testing.T) {
	reg := prometheus.NewRegistry()

	aiAgentRequests := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "test_ai_agent_requests",
			Help: "AI agent requests",
		},
		[]string{"agent_type", "lease_id", "status"},
	)

	aiAgentLatency := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name: "test_ai_agent_latency",
			Help: "AI agent latency",
		},
		[]string{"agent_type", "lease_id"},
	)

	reg.MustRegister(aiAgentRequests, aiAgentLatency)

	metrics := &Metrics{
		AIAgentRequestsTotal: aiAgentRequests,
		AIAgentLatency:       aiAgentLatency,
	}

	middleware := NewMetricsMiddleware(metrics)

	// Record AI agent request
	middleware.RecordAIAgentRequest("mcp", "test-lease", "200", 1*time.Second)

	// Verify counter was incremented
	counter, err := aiAgentRequests.GetMetricWithLabelValues("mcp", "test-lease", "200")
	if err != nil {
		t.Fatalf("Failed to get metric: %v", err)
	}

	metric := &dto.Metric{}
	counter.Write(metric)

	if metric.Counter.GetValue() != 1 {
		t.Errorf("Expected counter value 1, got %f", metric.Counter.GetValue())
	}
}

// TestActiveConnectionsTracking tests active connection tracking
func TestActiveConnectionsTracking(t *testing.T) {
	reg := prometheus.NewRegistry()

	activeConnections := prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "test_active_connections",
			Help: "Active connections",
		},
	)

	reg.MustRegister(activeConnections)

	metrics := &Metrics{
		ActiveConnections: activeConnections,
		ActiveLeases:      prometheus.NewGauge(prometheus.GaugeOpts{Name: "test_active_leases"}),
		RequestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{Name: "test_requests_total"},
			[]string{"method", "endpoint", "status", "lease_id"},
		),
		RequestDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{Name: "test_request_duration"},
			[]string{"method", "endpoint", "lease_id"},
		),
		BytesTransferredTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{Name: "test_bytes_transferred"},
			[]string{"direction", "lease_id"},
		),
	}

	middleware := NewMetricsMiddleware(metrics)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check that active connections is incremented during request
		metric := &dto.Metric{}
		activeConnections.Write(metric)

		if metric.Gauge.GetValue() != 1 {
			t.Errorf("Expected 1 active connection during request, got %f", metric.Gauge.GetValue())
		}

		w.WriteHeader(http.StatusOK)
	})

	wrappedHandler := middleware.Middleware(handler)

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	wrappedHandler.ServeHTTP(rr, req)

	// After request, active connections should be back to 0
	metric := &dto.Metric{}
	activeConnections.Write(metric)

	if metric.Gauge.GetValue() != 0 {
		t.Errorf("Expected 0 active connections after request, got %f", metric.Gauge.GetValue())
	}
}
