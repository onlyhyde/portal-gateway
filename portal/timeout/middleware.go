package timeout

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metrics holds timeout metrics
type Metrics struct {
	TimeoutsTotal *prometheus.CounterVec
	TimeoutsByLease *prometheus.CounterVec
}

// NewMetrics creates new timeout metrics
func NewMetrics() *Metrics {
	return NewMetricsWithRegistry(prometheus.DefaultRegisterer)
}

// NewMetricsWithRegistry creates new timeout metrics with a custom registry
func NewMetricsWithRegistry(reg prometheus.Registerer) *Metrics {
	if reg == nil {
		reg = prometheus.NewRegistry()
	}

	factory := promauto.With(reg)

	return &Metrics{
		TimeoutsTotal: factory.NewCounterVec(
			prometheus.CounterOpts{
				Name: "portal_request_timeouts_total",
				Help: "Total number of request timeouts",
			},
			[]string{"endpoint"},
		),
		TimeoutsByLease: factory.NewCounterVec(
			prometheus.CounterOpts{
				Name: "portal_request_timeouts_by_lease_total",
				Help: "Total number of request timeouts by lease",
			},
			[]string{"lease_id"},
		),
	}
}

// Config holds timeout configuration for a specific lease or service
type Config struct {
	// Timeout is the request timeout duration
	Timeout time.Duration
	// ServiceType is the type of service (e.g., "mcp", "n8n", "openai")
	ServiceType string
}

// MiddlewareConfig holds timeout middleware configuration
type MiddlewareConfig struct {
	// DefaultTimeout is the default timeout for all requests
	DefaultTimeout time.Duration
	// LeaseTimeouts maps lease IDs to their specific timeouts
	LeaseTimeouts map[string]time.Duration
	// ServiceTimeouts maps service types to their specific timeouts
	ServiceTimeouts map[string]time.Duration
	// Metrics is the metrics collector
	Metrics *Metrics
}

// DefaultMiddlewareConfig returns default configuration
func DefaultMiddlewareConfig() *MiddlewareConfig {
	return &MiddlewareConfig{
		DefaultTimeout: 30 * time.Second,
		LeaseTimeouts:  make(map[string]time.Duration),
		ServiceTimeouts: map[string]time.Duration{
			"mcp":    10 * time.Second,
			"n8n":    60 * time.Second,
			"openai": 30 * time.Second,
		},
		Metrics: NewMetrics(),
	}
}

// Middleware provides request timeout functionality
type Middleware struct {
	config *MiddlewareConfig
	mutex  sync.RWMutex
}

// NewMiddleware creates a new timeout middleware
func NewMiddleware(config *MiddlewareConfig) *Middleware {
	if config == nil {
		config = DefaultMiddlewareConfig()
	}

	if config.Metrics == nil {
		config.Metrics = NewMetrics()
	}

	if config.LeaseTimeouts == nil {
		config.LeaseTimeouts = make(map[string]time.Duration)
	}

	if config.ServiceTimeouts == nil {
		config.ServiceTimeouts = make(map[string]time.Duration)
	}

	return &Middleware{
		config: config,
	}
}

// GetTimeout returns the timeout for a given lease ID
func (m *Middleware) GetTimeout(leaseID string) time.Duration {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	// Check for lease-specific timeout
	if timeout, ok := m.config.LeaseTimeouts[leaseID]; ok {
		return timeout
	}

	// Use default timeout
	return m.config.DefaultTimeout
}

// SetTimeout sets the timeout for a specific lease ID
func (m *Middleware) SetTimeout(leaseID string, timeout time.Duration) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	m.config.LeaseTimeouts[leaseID] = timeout
}

// SetServiceTimeout sets the timeout for a specific service type
func (m *Middleware) SetServiceTimeout(serviceType string, timeout time.Duration) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	m.config.ServiceTimeouts[serviceType] = timeout
}

// GetServiceTimeout returns the timeout for a service type
func (m *Middleware) GetServiceTimeout(serviceType string) time.Duration {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	if timeout, ok := m.config.ServiceTimeouts[serviceType]; ok {
		return timeout
	}

	return m.config.DefaultTimeout
}

// Middleware returns an http.Handler that wraps the next handler with timeout
func (m *Middleware) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Get lease ID from context (set by ACL middleware)
		leaseID := getLeaseID(r.Context())

		// Determine timeout
		timeout := m.GetTimeout(leaseID)

		// Create context with timeout
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()

		// Create a channel to signal completion
		done := make(chan struct{})

		// Wrap response writer to track if response was sent
		wrapped := &timeoutResponseWriter{
			ResponseWriter: w,
			wroteHeader:    false,
		}

		// Execute request in goroutine
		go func() {
			defer close(done)
			next.ServeHTTP(wrapped, r.WithContext(ctx))
		}()

		// Wait for completion or timeout
		select {
		case <-done:
			// Request completed successfully
			return
		case <-ctx.Done():
			// Request timed out
			wrapped.mu.Lock()
			alreadyWrote := wrapped.wroteHeader
			wrapped.mu.Unlock()

			if !alreadyWrote {
				// Track timeout metric
				m.config.Metrics.TimeoutsTotal.WithLabelValues(r.URL.Path).Inc()
				if leaseID != "" {
					m.config.Metrics.TimeoutsByLease.WithLabelValues(leaseID).Inc()
				}

				// Send 504 Gateway Timeout
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusGatewayTimeout)
				if leaseID != "" {
					fmt.Fprintf(w, `{"error":"gateway_timeout","message":"Request timed out after %v for lease %s"}`, timeout, leaseID)
				} else {
					fmt.Fprintf(w, `{"error":"gateway_timeout","message":"Request timed out after %v"}`, timeout)
				}
			}
			return
		}
	})
}

// timeoutResponseWriter wraps http.ResponseWriter to track if header was written
type timeoutResponseWriter struct {
	http.ResponseWriter
	wroteHeader bool
	mu          sync.Mutex
}

func (w *timeoutResponseWriter) WriteHeader(code int) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.wroteHeader {
		w.wroteHeader = true
		w.ResponseWriter.WriteHeader(code)
	}
}

func (w *timeoutResponseWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.wroteHeader {
		w.wroteHeader = true
	}
	return w.ResponseWriter.Write(b)
}

// getLeaseID retrieves lease ID from context
func getLeaseID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}

	// Try string type for lease_id
	if value := ctx.Value("lease_id"); value != nil {
		if leaseID, ok := value.(string); ok {
			return leaseID
		}
	}

	return ""
}

// LoadFromFile loads timeout configuration from a YAML file
func LoadFromFile(path string) (*MiddlewareConfig, error) {
	// This is a placeholder for future YAML config loading
	// For now, return default config without metrics (will be created by NewMiddleware)
	return &MiddlewareConfig{
		DefaultTimeout: 30 * time.Second,
		LeaseTimeouts:  make(map[string]time.Duration),
		ServiceTimeouts: map[string]time.Duration{
			"mcp":    10 * time.Second,
			"n8n":    60 * time.Second,
			"openai": 30 * time.Second,
		},
		Metrics: nil, // Will be created by NewMiddleware
	}, nil
}
