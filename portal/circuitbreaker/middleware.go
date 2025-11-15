package circuitbreaker

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metrics holds circuit breaker metrics
type Metrics struct {
	StateGauge         *prometheus.GaugeVec
	RequestsTotal      *prometheus.CounterVec
	FailuresTotal      *prometheus.CounterVec
	StateChangesTotal  *prometheus.CounterVec
	RejectedTotal      *prometheus.CounterVec
}

// NewMetrics creates new circuit breaker metrics using the default registry
func NewMetrics() *Metrics {
	return NewMetricsWithRegistry(prometheus.DefaultRegisterer)
}

// NewMetricsWithRegistry creates new circuit breaker metrics with a custom registry
func NewMetricsWithRegistry(reg prometheus.Registerer) *Metrics {
	if reg == nil {
		reg = prometheus.NewRegistry()
	}

	factory := promauto.With(reg)

	return &Metrics{
		StateGauge: factory.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "portal_circuit_breaker_state",
				Help: "Current state of circuit breakers (0=closed, 1=open, 2=half-open)",
			},
			[]string{"lease_id"},
		),
		RequestsTotal: factory.NewCounterVec(
			prometheus.CounterOpts{
				Name: "portal_circuit_breaker_requests_total",
				Help: "Total number of requests through circuit breaker",
			},
			[]string{"lease_id", "result"},
		),
		FailuresTotal: factory.NewCounterVec(
			prometheus.CounterOpts{
				Name: "portal_circuit_breaker_failures_total",
				Help: "Total number of failures tracked by circuit breaker",
			},
			[]string{"lease_id"},
		),
		StateChangesTotal: factory.NewCounterVec(
			prometheus.CounterOpts{
				Name: "portal_circuit_breaker_state_changes_total",
				Help: "Total number of circuit breaker state changes",
			},
			[]string{"lease_id", "from", "to"},
		),
		RejectedTotal: factory.NewCounterVec(
			prometheus.CounterOpts{
				Name: "portal_circuit_breaker_rejected_total",
				Help: "Total number of requests rejected by circuit breaker",
			},
			[]string{"lease_id", "reason"},
		),
	}
}

// MiddlewareConfig holds circuit breaker middleware configuration
type MiddlewareConfig struct {
	// MaxRequests is the maximum number of requests allowed in half-open state
	MaxRequests uint32
	// Interval is the cyclic period to clear internal counts
	Interval time.Duration
	// Timeout is the period after which the breaker moves from open to half-open
	Timeout time.Duration
	// FailureThreshold is the number of consecutive failures before tripping
	FailureThreshold uint32
	// Metrics is the metrics collector
	Metrics *Metrics
	// FallbackHandler is called when circuit is open (optional)
	FallbackHandler http.Handler
}

// DefaultMiddlewareConfig returns default configuration
func DefaultMiddlewareConfig() *MiddlewareConfig {
	return &MiddlewareConfig{
		MaxRequests:      3,
		Interval:         0,
		Timeout:          30 * time.Second,
		FailureThreshold: 5,
		Metrics:          NewMetrics(),
	}
}

// Middleware provides circuit breaker middleware with per-lease breakers
type Middleware struct {
	config   *MiddlewareConfig
	breakers map[string]*CircuitBreaker
	mutex    sync.RWMutex
}

// NewMiddleware creates a new circuit breaker middleware
func NewMiddleware(config *MiddlewareConfig) *Middleware {
	if config == nil {
		config = DefaultMiddlewareConfig()
	}

	if config.Metrics == nil {
		config.Metrics = NewMetrics()
	}

	return &Middleware{
		config:   config,
		breakers: make(map[string]*CircuitBreaker),
	}
}

// GetBreaker returns the circuit breaker for a given lease ID
func (m *Middleware) GetBreaker(leaseID string) *CircuitBreaker {
	m.mutex.RLock()
	breaker, ok := m.breakers[leaseID]
	m.mutex.RUnlock()

	if ok {
		return breaker
	}

	m.mutex.Lock()
	defer m.mutex.Unlock()

	// Double-check after acquiring write lock
	if breaker, ok := m.breakers[leaseID]; ok {
		return breaker
	}

	// Create new circuit breaker for this lease
	threshold := m.config.FailureThreshold
	breaker = NewCircuitBreaker(leaseID, Config{
		MaxRequests: m.config.MaxRequests,
		Interval:    m.config.Interval,
		Timeout:     m.config.Timeout,
		ReadyToTrip: func(counts Counts) bool {
			return counts.ConsecutiveFailures >= threshold
		},
		OnStateChange: func(name string, from State, to State) {
			m.onStateChange(name, from, to)
		},
	})

	m.breakers[leaseID] = breaker

	// Initialize state metric
	m.config.Metrics.StateGauge.WithLabelValues(leaseID).Set(float64(StateClosed))

	return breaker
}

// onStateChange is called when a circuit breaker changes state
func (m *Middleware) onStateChange(name string, from State, to State) {
	// Update metrics
	m.config.Metrics.StateGauge.WithLabelValues(name).Set(float64(to))
	m.config.Metrics.StateChangesTotal.WithLabelValues(name, from.String(), to.String()).Inc()
}

// Middleware returns an http.Handler that wraps the next handler with circuit breaker
func (m *Middleware) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Get lease ID from context (set by ACL middleware)
		leaseID := getLeaseID(r.Context())
		if leaseID == "" {
			// No lease ID, skip circuit breaker
			next.ServeHTTP(w, r)
			return
		}

		// Get or create circuit breaker for this lease
		breaker := m.GetBreaker(leaseID)

		// Execute request through circuit breaker
		err := breaker.Execute(func() error {
			// Create response writer wrapper to capture status code
			wrapped := &responseWriter{
				ResponseWriter: w,
				statusCode:     http.StatusOK,
			}

			next.ServeHTTP(wrapped, r)

			// Consider 5xx errors as failures
			if wrapped.statusCode >= 500 {
				m.config.Metrics.FailuresTotal.WithLabelValues(leaseID).Inc()
				m.config.Metrics.RequestsTotal.WithLabelValues(leaseID, "failure").Inc()
				return fmt.Errorf("server error: %d", wrapped.statusCode)
			}

			m.config.Metrics.RequestsTotal.WithLabelValues(leaseID, "success").Inc()
			return nil
		})

		if err != nil {
			// Circuit breaker rejected the request
			if err == ErrCircuitOpen {
				m.config.Metrics.RejectedTotal.WithLabelValues(leaseID, "open").Inc()

				// Use fallback handler if configured
				if m.config.FallbackHandler != nil {
					m.config.FallbackHandler.ServeHTTP(w, r)
					return
				}

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusServiceUnavailable)
				fmt.Fprintf(w, `{"error":"service_unavailable","message":"Circuit breaker is open for lease %s"}`, leaseID)
				return
			}

			if err == ErrTooManyRequests {
				m.config.Metrics.RejectedTotal.WithLabelValues(leaseID, "too_many_requests").Inc()
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				fmt.Fprintf(w, `{"error":"too_many_requests","message":"Circuit breaker is testing recovery for lease %s"}`, leaseID)
				return
			}

			// Other errors are already handled by the function
		}
	})
}

// responseWriter wraps http.ResponseWriter to capture status code
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// getLeaseID retrieves lease ID from context
func getLeaseID(ctx interface{}) string {
	if ctx == nil {
		return ""
	}

	type contextGetter interface {
		Value(key interface{}) interface{}
	}

	getter, ok := ctx.(contextGetter)
	if !ok {
		return ""
	}

	// Try both contextKey type and string type for compatibility
	type contextKey string
	const leaseIDKey contextKey = "lease_id"

	// Try contextKey type first
	if value := getter.Value(leaseIDKey); value != nil {
		if leaseID, ok := value.(string); ok {
			return leaseID
		}
	}

	// Try string type as fallback
	if value := getter.Value("lease_id"); value != nil {
		if leaseID, ok := value.(string); ok {
			return leaseID
		}
	}

	return ""
}

// ListBreakers returns all active circuit breakers
func (m *Middleware) ListBreakers() map[string]*CircuitBreaker {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	result := make(map[string]*CircuitBreaker, len(m.breakers))
	for k, v := range m.breakers {
		result[k] = v
	}
	return result
}

// ResetBreaker resets a specific circuit breaker
func (m *Middleware) ResetBreaker(leaseID string) bool {
	m.mutex.RLock()
	breaker, ok := m.breakers[leaseID]
	m.mutex.RUnlock()

	if !ok {
		return false
	}

	breaker.Reset()
	return true
}

// ResetAll resets all circuit breakers
func (m *Middleware) ResetAll() {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	for _, breaker := range m.breakers {
		breaker.Reset()
	}
}
