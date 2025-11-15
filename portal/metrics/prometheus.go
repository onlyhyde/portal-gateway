package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metrics holds all Prometheus metrics for the portal gateway
type Metrics struct {
	// Core metrics
	RequestsTotal          *prometheus.CounterVec
	RequestDuration        *prometheus.HistogramVec
	ActiveConnections      prometheus.Gauge
	ActiveLeases           prometheus.Gauge
	BytesTransferredTotal  *prometheus.CounterVec

	// AI Agent metrics
	AIAgentRequestsTotal   *prometheus.CounterVec
	AIAgentLatency         *prometheus.HistogramVec
	AIAgentErrorsTotal     *prometheus.CounterVec

	// Rate limiting metrics
	RateLimitExceeded      *prometheus.CounterVec
	QuotaExceeded          *prometheus.CounterVec

	// ACL metrics
	ACLDenied              *prometheus.CounterVec
}

// NewMetrics creates and registers all Prometheus metrics
func NewMetrics() *Metrics {
	return &Metrics{
		// Core metrics
		RequestsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "portal_requests_total",
				Help: "Total number of HTTP requests",
			},
			[]string{"method", "endpoint", "status", "lease_id"},
		),

		RequestDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "portal_request_duration_seconds",
				Help:    "HTTP request duration in seconds",
				Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0},
			},
			[]string{"method", "endpoint", "lease_id"},
		),

		ActiveConnections: promauto.NewGauge(
			prometheus.GaugeOpts{
				Name: "portal_active_connections",
				Help: "Number of active HTTP connections",
			},
		),

		ActiveLeases: promauto.NewGauge(
			prometheus.GaugeOpts{
				Name: "portal_active_leases",
				Help: "Number of active leases with connections",
			},
		),

		BytesTransferredTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "portal_bytes_transferred_total",
				Help: "Total bytes transferred (request + response)",
			},
			[]string{"direction", "lease_id"}, // direction: "in" or "out"
		),

		// AI Agent metrics
		AIAgentRequestsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "portal_ai_agent_requests_total",
				Help: "Total number of AI agent requests",
			},
			[]string{"agent_type", "lease_id", "status"},
		),

		AIAgentLatency: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "portal_ai_agent_latency_seconds",
				Help:    "AI agent request latency in seconds",
				Buckets: []float64{0.1, 0.5, 1.0, 2.0, 5.0, 10.0, 30.0, 60.0},
			},
			[]string{"agent_type", "lease_id"},
		),

		AIAgentErrorsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "portal_ai_agent_errors_total",
				Help: "Total number of AI agent errors",
			},
			[]string{"agent_type", "lease_id", "error_type"},
		),

		// Rate limiting metrics
		RateLimitExceeded: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "portal_rate_limit_exceeded_total",
				Help: "Total number of rate limit exceeded errors",
			},
			[]string{"lease_id", "limit_type"}, // limit_type: "global", "lease", "ip"
		),

		QuotaExceeded: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "portal_quota_exceeded_total",
				Help: "Total number of quota exceeded errors",
			},
			[]string{"key_id", "quota_type"}, // quota_type: "requests", "bytes", "connections"
		),

		// ACL metrics
		ACLDenied: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "portal_acl_denied_total",
				Help: "Total number of ACL denied requests",
			},
			[]string{"lease_id", "reason"}, // reason: "lease_not_found", "key_not_allowed", "ip_not_allowed"
		),
	}
}

// Default global metrics instance
var DefaultMetrics = NewMetrics()

// GetDefaultMetrics returns the default global metrics instance
func GetDefaultMetrics() *Metrics {
	return DefaultMetrics
}
