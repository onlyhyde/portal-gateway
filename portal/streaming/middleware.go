package streaming

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metrics holds streaming metrics
type Metrics struct {
	StreamingRequestsTotal prometheus.Counter
	StreamingBytesTotal    prometheus.Counter
	ActiveStreams          prometheus.Gauge
	StreamDuration         prometheus.Histogram
}

// NewMetrics creates new streaming metrics
func NewMetrics() *Metrics {
	return NewMetricsWithRegistry(prometheus.DefaultRegisterer)
}

// NewMetricsWithRegistry creates new streaming metrics with a custom registry
func NewMetricsWithRegistry(reg prometheus.Registerer) *Metrics {
	if reg == nil {
		reg = prometheus.NewRegistry()
	}

	factory := promauto.With(reg)

	return &Metrics{
		StreamingRequestsTotal: factory.NewCounter(
			prometheus.CounterOpts{
				Name: "portal_streaming_requests_total",
				Help: "Total number of streaming requests",
			},
		),
		StreamingBytesTotal: factory.NewCounter(
			prometheus.CounterOpts{
				Name: "portal_streaming_bytes_total",
				Help: "Total number of bytes streamed",
			},
		),
		ActiveStreams: factory.NewGauge(
			prometheus.GaugeOpts{
				Name: "portal_active_streams",
				Help: "Number of active streaming connections",
			},
		),
		StreamDuration: factory.NewHistogram(
			prometheus.HistogramOpts{
				Name:    "portal_stream_duration_seconds",
				Help:    "Streaming connection duration in seconds",
				Buckets: []float64{1, 5, 10, 30, 60, 300, 600},
			},
		),
	}
}

// MiddlewareConfig holds streaming middleware configuration
type MiddlewareConfig struct {
	// EnableKeepAlive enables keep-alive comments for SSE
	EnableKeepAlive bool

	// KeepAliveInterval is the interval for keep-alive comments
	KeepAliveInterval time.Duration

	// Metrics is the metrics collector
	Metrics *Metrics
}

// DefaultMiddlewareConfig returns default configuration
func DefaultMiddlewareConfig() *MiddlewareConfig {
	return &MiddlewareConfig{
		EnableKeepAlive:   true,
		KeepAliveInterval: 30 * time.Second,
		Metrics:           nil, // Will be created by NewMiddleware
	}
}

// Middleware provides SSE/streaming support
type Middleware struct {
	config *MiddlewareConfig
}

// NewMiddleware creates a new streaming middleware
func NewMiddleware(config *MiddlewareConfig) *Middleware {
	if config == nil {
		config = DefaultMiddlewareConfig()
	}

	if config.Metrics == nil {
		config.Metrics = NewMetrics()
	}

	if config.KeepAliveInterval == 0 {
		config.KeepAliveInterval = 30 * time.Second
	}

	return &Middleware{
		config: config,
	}
}

// Middleware returns an http.Handler that wraps the next handler with streaming support
func (m *Middleware) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check if this is a streaming request
		isSSE := m.isSSERequest(r)
		isStreaming := m.isStreamingRequest(r)

		if !isSSE && !isStreaming {
			// Not a streaming request, pass through
			next.ServeHTTP(w, r)
			return
		}

		// Track streaming request
		m.config.Metrics.StreamingRequestsTotal.Inc()
		m.config.Metrics.ActiveStreams.Inc()
		defer m.config.Metrics.ActiveStreams.Dec()

		startTime := time.Now()
		defer func() {
			duration := time.Since(startTime).Seconds()
			m.config.Metrics.StreamDuration.Observe(duration)
		}()

		// Wrap response writer for streaming
		sw := &streamingResponseWriter{
			ResponseWriter: w,
			metrics:        m.config.Metrics,
		}

		// Set headers for streaming
		if isSSE {
			sw.Header().Set("Content-Type", "text/event-stream")
			sw.Header().Set("Cache-Control", "no-cache")
			sw.Header().Set("Connection", "keep-alive")
			sw.Header().Set("X-Accel-Buffering", "no") // Disable nginx buffering
		}

		// Serve the request
		next.ServeHTTP(sw, r)
	})
}

// isSSERequest checks if the request is for SSE
func (m *Middleware) isSSERequest(r *http.Request) bool {
	accept := r.Header.Get("Accept")
	return strings.Contains(accept, "text/event-stream")
}

// isStreamingRequest checks if the request expects streaming response
func (m *Middleware) isStreamingRequest(r *http.Request) bool {
	// Check for streaming indicators
	if r.Header.Get("X-Stream") == "true" {
		return true
	}

	// Check for chunked transfer encoding in request
	if r.Header.Get("Transfer-Encoding") == "chunked" {
		return true
	}

	return false
}

// streamingResponseWriter wraps http.ResponseWriter to support streaming
type streamingResponseWriter struct {
	http.ResponseWriter
	metrics       *Metrics
	bytesWritten  int64
	headerWritten bool
}

// WriteHeader writes the status code and headers
func (w *streamingResponseWriter) WriteHeader(statusCode int) {
	if !w.headerWritten {
		w.headerWritten = true
		w.ResponseWriter.WriteHeader(statusCode)
	}
}

// Write writes data and flushes immediately for streaming
func (w *streamingResponseWriter) Write(b []byte) (int, error) {
	if !w.headerWritten {
		w.WriteHeader(http.StatusOK)
	}

	n, err := w.ResponseWriter.Write(b)
	if err != nil {
		return n, err
	}

	w.bytesWritten += int64(n)
	w.metrics.StreamingBytesTotal.Add(float64(n))

	// Flush if possible
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}

	return n, nil
}

// Flush flushes the response buffer
func (w *streamingResponseWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Hijack implements http.Hijacker for WebSocket support
func (w *streamingResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hijacker, ok := w.ResponseWriter.(http.Hijacker); ok {
		return hijacker.Hijack()
	}
	return nil, nil, fmt.Errorf("response writer does not support hijacking")
}

// GetMetrics returns the metrics collector
func (m *Middleware) GetMetrics() *Metrics {
	return m.config.Metrics
}
