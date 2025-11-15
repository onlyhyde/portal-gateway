package shutdown

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metrics holds shutdown metrics
type Metrics struct {
	ShutdownsTotal       prometheus.Counter
	ShutdownDuration     prometheus.Histogram
	ActiveConnections    prometheus.Gauge
	DrainedConnections   prometheus.Counter
	ShutdownTimeoutsTotal prometheus.Counter
}

// NewMetrics creates new shutdown metrics
func NewMetrics() *Metrics {
	return NewMetricsWithRegistry(prometheus.DefaultRegisterer)
}

// NewMetricsWithRegistry creates new shutdown metrics with a custom registry
func NewMetricsWithRegistry(reg prometheus.Registerer) *Metrics {
	if reg == nil {
		reg = prometheus.NewRegistry()
	}

	factory := promauto.With(reg)

	return &Metrics{
		ShutdownsTotal: factory.NewCounter(
			prometheus.CounterOpts{
				Name: "portal_shutdowns_total",
				Help: "Total number of graceful shutdowns",
			},
		),
		ShutdownDuration: factory.NewHistogram(
			prometheus.HistogramOpts{
				Name:    "portal_shutdown_duration_seconds",
				Help:    "Shutdown duration in seconds",
				Buckets: []float64{0.1, 0.5, 1, 5, 10, 30},
			},
		),
		ActiveConnections: factory.NewGauge(
			prometheus.GaugeOpts{
				Name: "portal_active_connections",
				Help: "Number of active connections",
			},
		),
		DrainedConnections: factory.NewCounter(
			prometheus.CounterOpts{
				Name: "portal_drained_connections_total",
				Help: "Total number of connections drained during shutdown",
			},
		),
		ShutdownTimeoutsTotal: factory.NewCounter(
			prometheus.CounterOpts{
				Name: "portal_shutdown_timeouts_total",
				Help: "Total number of shutdown timeouts",
			},
		),
	}
}

// Manager manages graceful shutdown
type Manager struct {
	// shuttingDown indicates if shutdown is in progress
	shuttingDown atomic.Bool

	// drainTimeout is the maximum time to wait for connections to drain
	drainTimeout time.Duration

	// metrics is the metrics collector
	metrics *Metrics

	// servers is a list of HTTP servers to shutdown
	servers []*http.Server

	// cleanupFuncs are functions to call during shutdown
	cleanupFuncs []func() error

	// mutex protects the servers and cleanupFuncs slices
	mutex sync.RWMutex
}

// Config holds shutdown manager configuration
type Config struct {
	// DrainTimeout is the maximum time to wait for connections to drain (default: 30s)
	DrainTimeout time.Duration

	// Metrics is the metrics collector
	Metrics *Metrics
}

// DefaultConfig returns default configuration
func DefaultConfig() *Config {
	return &Config{
		DrainTimeout: 30 * time.Second,
		Metrics:      nil, // Will be created by NewManager
	}
}

// NewManager creates a new shutdown manager
func NewManager(config *Config) *Manager {
	if config == nil {
		config = DefaultConfig()
	}

	if config.DrainTimeout == 0 {
		config.DrainTimeout = 30 * time.Second
	}

	if config.Metrics == nil {
		config.Metrics = NewMetrics()
	}

	return &Manager{
		drainTimeout: config.DrainTimeout,
		metrics:      config.Metrics,
		servers:      make([]*http.Server, 0),
		cleanupFuncs: make([]func() error, 0),
	}
}

// IsShuttingDown returns true if shutdown is in progress
func (m *Manager) IsShuttingDown() bool {
	return m.shuttingDown.Load()
}

// RegisterServer registers an HTTP server for graceful shutdown
func (m *Manager) RegisterServer(server *http.Server) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	m.servers = append(m.servers, server)
}

// RegisterCleanup registers a cleanup function to be called during shutdown
func (m *Manager) RegisterCleanup(fn func() error) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	m.cleanupFuncs = append(m.cleanupFuncs, fn)
}

// Shutdown performs graceful shutdown
func (m *Manager) Shutdown(ctx context.Context) error {
	// Mark as shutting down
	if !m.shuttingDown.CompareAndSwap(false, true) {
		return fmt.Errorf("shutdown already in progress")
	}

	// Track shutdown metrics
	m.metrics.ShutdownsTotal.Inc()
	startTime := time.Now()
	defer func() {
		duration := time.Since(startTime).Seconds()
		m.metrics.ShutdownDuration.Observe(duration)
	}()

	// Create shutdown context with timeout
	shutdownCtx, cancel := context.WithTimeout(ctx, m.drainTimeout)
	defer cancel()

	// Shutdown all registered servers
	m.mutex.RLock()
	servers := make([]*http.Server, len(m.servers))
	copy(servers, m.servers)
	m.mutex.RUnlock()

	// Shutdown servers concurrently
	var wg sync.WaitGroup
	errChan := make(chan error, len(servers))

	for _, server := range servers {
		wg.Add(1)
		go func(srv *http.Server) {
			defer wg.Done()

			if err := srv.Shutdown(shutdownCtx); err != nil {
				errChan <- fmt.Errorf("server %s shutdown error: %w", srv.Addr, err)
			} else {
				// Count drained connections (approximation)
				m.metrics.DrainedConnections.Inc()
			}
		}(server)
	}

	// Wait for all servers to shutdown
	wg.Wait()
	close(errChan)

	// Check for errors
	var shutdownErrors []error
	for err := range errChan {
		shutdownErrors = append(shutdownErrors, err)
	}

	// Check if shutdown timed out
	if shutdownCtx.Err() == context.DeadlineExceeded {
		m.metrics.ShutdownTimeoutsTotal.Inc()
		return fmt.Errorf("shutdown timed out after %v", m.drainTimeout)
	}

	// Run cleanup functions
	m.mutex.RLock()
	cleanupFuncs := make([]func() error, len(m.cleanupFuncs))
	copy(cleanupFuncs, m.cleanupFuncs)
	m.mutex.RUnlock()

	for _, fn := range cleanupFuncs {
		if err := fn(); err != nil {
			shutdownErrors = append(shutdownErrors, err)
		}
	}

	// Return combined errors if any
	if len(shutdownErrors) > 0 {
		return fmt.Errorf("shutdown errors: %v", shutdownErrors)
	}

	return nil
}

// GetMetrics returns the metrics collector
func (m *Manager) GetMetrics() *Metrics {
	return m.metrics
}
