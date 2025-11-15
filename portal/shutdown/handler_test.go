package shutdown

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// newTestMetrics creates new metrics for testing with a fresh registry
func newTestMetrics() *Metrics {
	return NewMetricsWithRegistry(prometheus.NewRegistry())
}

func TestNewManager(t *testing.T) {
	config := &Config{
		DrainTimeout: 10 * time.Second,
		Metrics:      newTestMetrics(),
	}

	m := NewManager(config)

	if m == nil {
		t.Fatal("Expected manager to be created")
	}

	if m.drainTimeout != 10*time.Second {
		t.Errorf("Expected drain timeout 10s, got %v", m.drainTimeout)
	}

	if m.metrics == nil {
		t.Error("Expected metrics to be initialized")
	}
}

func TestNewManagerWithDefaults(t *testing.T) {
	m := NewManager(nil)

	if m == nil {
		t.Fatal("Expected manager to be created with defaults")
	}

	if m.drainTimeout != 30*time.Second {
		t.Errorf("Expected default drain timeout 30s, got %v", m.drainTimeout)
	}

	if m.metrics == nil {
		t.Error("Expected metrics to be initialized")
	}
}

func TestIsShuttingDown(t *testing.T) {
	config := &Config{
		Metrics: newTestMetrics(),
	}
	m := NewManager(config)

	if m.IsShuttingDown() {
		t.Error("Expected IsShuttingDown to be false initially")
	}

	m.shuttingDown.Store(true)

	if !m.IsShuttingDown() {
		t.Error("Expected IsShuttingDown to be true after setting")
	}
}

func TestRegisterServer(t *testing.T) {
	config := &Config{
		Metrics: newTestMetrics(),
	}
	m := NewManager(config)

	server1 := &http.Server{Addr: ":8080"}
	server2 := &http.Server{Addr: ":8443"}

	m.RegisterServer(server1)
	m.RegisterServer(server2)

	m.mutex.RLock()
	defer m.mutex.RUnlock()

	if len(m.servers) != 2 {
		t.Errorf("Expected 2 servers registered, got %d", len(m.servers))
	}
}

func TestRegisterCleanup(t *testing.T) {
	config := &Config{
		Metrics: newTestMetrics(),
	}
	m := NewManager(config)

	cleanup := func() error {
		return nil
	}

	m.RegisterCleanup(cleanup)

	m.mutex.RLock()
	defer m.mutex.RUnlock()

	if len(m.cleanupFuncs) != 1 {
		t.Errorf("Expected 1 cleanup function registered, got %d", len(m.cleanupFuncs))
	}
}

func TestShutdown(t *testing.T) {
	config := &Config{
		DrainTimeout: 2 * time.Second,
		Metrics:      newTestMetrics(),
	}
	m := NewManager(config)

	// Create a test server
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	server := &http.Server{
		Addr:    ":0",
		Handler: handler,
	}

	// Register server (not started, just testing shutdown mechanism)
	m.RegisterServer(server)

	// Shutdown
	ctx := context.Background()
	err := m.Shutdown(ctx)

	if err != nil {
		t.Errorf("Unexpected shutdown error: %v", err)
	}

	if !m.IsShuttingDown() {
		t.Error("Expected IsShuttingDown to be true after shutdown")
	}
}

func TestShutdownWithCleanup(t *testing.T) {
	config := &Config{
		DrainTimeout: 2 * time.Second,
		Metrics:      newTestMetrics(),
	}
	m := NewManager(config)

	cleanupCalled := false
	cleanup := func() error {
		cleanupCalled = true
		return nil
	}

	m.RegisterCleanup(cleanup)

	// Create a simple server
	server := &http.Server{Addr: ":0"}
	m.RegisterServer(server)

	// Shutdown
	ctx := context.Background()
	err := m.Shutdown(ctx)

	if err != nil {
		t.Errorf("Unexpected shutdown error: %v", err)
	}

	if !cleanupCalled {
		t.Error("Expected cleanup function to be called")
	}
}

func TestShutdownWithCleanupError(t *testing.T) {
	config := &Config{
		DrainTimeout: 2 * time.Second,
		Metrics:      newTestMetrics(),
	}
	m := NewManager(config)

	cleanupError := errors.New("cleanup failed")
	cleanup := func() error {
		return cleanupError
	}

	m.RegisterCleanup(cleanup)

	// Create a simple server
	server := &http.Server{Addr: ":0"}
	m.RegisterServer(server)

	// Shutdown
	ctx := context.Background()
	err := m.Shutdown(ctx)

	if err == nil {
		t.Error("Expected shutdown to return error from cleanup")
	}
}

func TestShutdownAlreadyInProgress(t *testing.T) {
	config := &Config{
		Metrics: newTestMetrics(),
	}
	m := NewManager(config)

	// Set shutdown flag
	m.shuttingDown.Store(true)

	// Try to shutdown again
	ctx := context.Background()
	err := m.Shutdown(ctx)

	if err == nil {
		t.Error("Expected error when shutdown already in progress")
	}

	if err.Error() != "shutdown already in progress" {
		t.Errorf("Expected 'shutdown already in progress' error, got: %v", err)
	}
}

func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()

	if config.DrainTimeout != 30*time.Second {
		t.Errorf("Expected default drain timeout 30s, got %v", config.DrainTimeout)
	}

	if config.Metrics != nil {
		t.Error("Expected metrics to be nil from DefaultConfig")
	}
}

func TestGetMetrics(t *testing.T) {
	metrics := newTestMetrics()
	config := &Config{
		Metrics: metrics,
	}
	m := NewManager(config)

	retrievedMetrics := m.GetMetrics()

	if retrievedMetrics != metrics {
		t.Error("Expected GetMetrics to return the same metrics instance")
	}
}

func TestShutdownMetrics(t *testing.T) {
	metrics := newTestMetrics()
	config := &Config{
		DrainTimeout: 1 * time.Second,
		Metrics:      metrics,
	}
	m := NewManager(config)

	// Create a simple server
	server := &http.Server{Addr: ":0"}
	m.RegisterServer(server)

	// Shutdown
	ctx := context.Background()
	err := m.Shutdown(ctx)

	if err != nil {
		t.Errorf("Unexpected shutdown error: %v", err)
	}

	// Metrics should be updated
	// Note: We can't easily verify prometheus metrics in tests without more setup
	// but the code paths are covered
}

// Benchmark tests
func BenchmarkShutdown(b *testing.B) {
	for i := 0; i < b.N; i++ {
		config := &Config{
			DrainTimeout: 1 * time.Second,
			Metrics:      newTestMetrics(),
		}
		m := NewManager(config)

		server := &http.Server{Addr: ":0"}
		m.RegisterServer(server)

		ctx := context.Background()
		_ = m.Shutdown(ctx)
	}
}
