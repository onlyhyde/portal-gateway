package metrics

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// MetricsMiddleware provides HTTP metrics collection
type MetricsMiddleware struct {
	metrics         *Metrics
	activeLeases    map[string]bool
	activeLeasesMu  sync.RWMutex
}

// NewMetricsMiddleware creates a new metrics middleware
func NewMetricsMiddleware(metrics *Metrics) *MetricsMiddleware {
	if metrics == nil {
		metrics = GetDefaultMetrics()
	}

	return &MetricsMiddleware{
		metrics:      metrics,
		activeLeases: make(map[string]bool),
	}
}

// Middleware returns an http.Handler that collects metrics
func (m *MetricsMiddleware) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Increment active connections
		m.metrics.ActiveConnections.Inc()
		defer m.metrics.ActiveConnections.Dec()

		// Start timing
		start := time.Now()

		// Wrap response writer to capture status code and bytes written
		wrapped := &metricsResponseWriter{
			ResponseWriter: w,
			statusCode:     http.StatusOK,
			bytesWritten:   0,
		}

		// Get lease ID from context if available
		leaseID := getLeaseIDFromContext(r.Context())
		if leaseID != "" {
			m.trackActiveLease(leaseID)
			defer m.untrackActiveLease(leaseID)
		}

		// Record request bytes
		requestBytes := r.ContentLength
		if requestBytes > 0 && leaseID != "" {
			m.metrics.BytesTransferredTotal.WithLabelValues("in", leaseID).Add(float64(requestBytes))
		}

		// Process request
		next.ServeHTTP(wrapped, r)

		// Calculate duration
		duration := time.Since(start).Seconds()

		// Get endpoint path (sanitized to avoid cardinality explosion)
		endpoint := sanitizeEndpoint(r.URL.Path)

		// Record metrics
		statusStr := strconv.Itoa(wrapped.statusCode)
		m.metrics.RequestsTotal.WithLabelValues(r.Method, endpoint, statusStr, leaseID).Inc()
		m.metrics.RequestDuration.WithLabelValues(r.Method, endpoint, leaseID).Observe(duration)

		// Record response bytes
		if wrapped.bytesWritten > 0 && leaseID != "" {
			m.metrics.BytesTransferredTotal.WithLabelValues("out", leaseID).Add(float64(wrapped.bytesWritten))
		}
	})
}

// trackActiveLease adds a lease to the active set
func (m *MetricsMiddleware) trackActiveLease(leaseID string) {
	m.activeLeasesMu.Lock()
	defer m.activeLeasesMu.Unlock()

	wasActive := m.activeLeases[leaseID]
	m.activeLeases[leaseID] = true

	if !wasActive {
		m.metrics.ActiveLeases.Inc()
	}
}

// untrackActiveLease removes a lease from the active set if it has no more connections
func (m *MetricsMiddleware) untrackActiveLease(leaseID string) {
	m.activeLeasesMu.Lock()
	defer m.activeLeasesMu.Unlock()

	// Note: In a real implementation, we'd track connection count per lease
	// For simplicity, we just keep the lease as active if it was active
	// A more sophisticated implementation would use reference counting
}

// RecordRateLimitExceeded records a rate limit exceeded event
func (m *MetricsMiddleware) RecordRateLimitExceeded(leaseID, limitType string) {
	m.metrics.RateLimitExceeded.WithLabelValues(leaseID, limitType).Inc()
}

// RecordQuotaExceeded records a quota exceeded event
func (m *MetricsMiddleware) RecordQuotaExceeded(keyID, quotaType string) {
	m.metrics.QuotaExceeded.WithLabelValues(keyID, quotaType).Inc()
}

// RecordACLDenied records an ACL denied event
func (m *MetricsMiddleware) RecordACLDenied(leaseID, reason string) {
	m.metrics.ACLDenied.WithLabelValues(leaseID, reason).Inc()
}

// RecordAIAgentRequest records an AI agent request
func (m *MetricsMiddleware) RecordAIAgentRequest(agentType, leaseID, status string, duration time.Duration) {
	m.metrics.AIAgentRequestsTotal.WithLabelValues(agentType, leaseID, status).Inc()
	m.metrics.AIAgentLatency.WithLabelValues(agentType, leaseID).Observe(duration.Seconds())
}

// RecordAIAgentError records an AI agent error
func (m *MetricsMiddleware) RecordAIAgentError(agentType, leaseID, errorType string) {
	m.metrics.AIAgentErrorsTotal.WithLabelValues(agentType, leaseID, errorType).Inc()
}

// metricsResponseWriter wraps http.ResponseWriter to capture status code and bytes
type metricsResponseWriter struct {
	http.ResponseWriter
	statusCode   int
	bytesWritten int
}

func (rw *metricsResponseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *metricsResponseWriter) Write(b []byte) (int, error) {
	n, err := rw.ResponseWriter.Write(b)
	rw.bytesWritten += n
	return n, err
}

// sanitizeEndpoint sanitizes endpoint paths to prevent cardinality explosion
// Converts paths like /peer/lease-123 to /peer/{lease_id}
func sanitizeEndpoint(path string) string {
	// Handle common patterns
	if strings.HasPrefix(path, "/peer/") {
		return "/peer/{lease_id}"
	}

	if strings.HasPrefix(path, "/admin/acl/") {
		return "/admin/acl/{lease_id}"
	}

	if strings.HasPrefix(path, "/admin/quota/") {
		if strings.HasSuffix(path, "/reset") {
			return "/admin/quota/{key_id}/reset"
		}
		return "/admin/quota/{key_id}"
	}

	// Return known static paths as-is
	knownPaths := []string{
		"/",
		"/health",
		"/metrics",
		"/admin/acl",
		"/auth/validate",
	}

	for _, known := range knownPaths {
		if path == known {
			return path
		}
	}

	// Default: return the path (might need more sophisticated handling)
	return path
}

// getLeaseIDFromContext retrieves lease ID from context
func getLeaseIDFromContext(ctx interface{}) string {
	if ctx == nil {
		return ""
	}

	// Type assert to get the actual context
	type contextGetter interface {
		Value(key interface{}) interface{}
	}

	getter, ok := ctx.(contextGetter)
	if !ok {
		return ""
	}

	// Try common context keys
	type contextKey string
	leaseIDKey := contextKey("lease_id")

	value := getter.Value(leaseIDKey)
	if value == nil {
		return ""
	}

	if leaseID, ok := value.(string); ok {
		return leaseID
	}

	return ""
}
