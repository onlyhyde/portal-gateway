package webhook

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// RetryMetrics holds retry metrics
type RetryMetrics struct {
	RetriesTotal     *prometheus.CounterVec
	RetrySuccessTotal prometheus.Counter
	RetryFailureTotal prometheus.Counter
	RetryDuration    prometheus.Histogram
}

// NewRetryMetrics creates new retry metrics
func NewRetryMetrics() *RetryMetrics {
	return NewRetryMetricsWithRegistry(prometheus.DefaultRegisterer)
}

// NewRetryMetricsWithRegistry creates new retry metrics with a custom registry
func NewRetryMetricsWithRegistry(reg prometheus.Registerer) *RetryMetrics {
	if reg == nil {
		reg = prometheus.NewRegistry()
	}

	factory := promauto.With(reg)

	return &RetryMetrics{
		RetriesTotal: factory.NewCounterVec(
			prometheus.CounterOpts{
				Name: "portal_webhook_retries_total",
				Help: "Total number of webhook retries by attempt",
			},
			[]string{"attempt"},
		),
		RetrySuccessTotal: factory.NewCounter(
			prometheus.CounterOpts{
				Name: "portal_webhook_retry_success_total",
				Help: "Total number of successful retries",
			},
		),
		RetryFailureTotal: factory.NewCounter(
			prometheus.CounterOpts{
				Name: "portal_webhook_retry_failure_total",
				Help: "Total number of failed retries",
			},
		),
		RetryDuration: factory.NewHistogram(
			prometheus.HistogramOpts{
				Name:    "portal_webhook_retry_duration_seconds",
				Help:    "Time spent retrying webhooks in seconds",
				Buckets: []float64{0.1, 0.5, 1, 2, 5, 10, 30},
			},
		),
	}
}

// RetryConfig holds retry configuration
type RetryConfig struct {
	// MaxRetries is the maximum number of retry attempts
	MaxRetries int

	// InitialBackoff is the initial backoff duration
	InitialBackoff time.Duration

	// MaxBackoff is the maximum backoff duration
	MaxBackoff time.Duration

	// BackoffMultiplier is the multiplier for exponential backoff
	BackoffMultiplier float64

	// RetryOn5xxOnly retries only on 5xx status codes
	RetryOn5xxOnly bool

	// Metrics is the metrics collector
	Metrics *RetryMetrics

	// DLQ is the dead letter queue for failed requests
	DLQ *DLQ
}

// DefaultRetryConfig returns default retry configuration
func DefaultRetryConfig() *RetryConfig {
	return &RetryConfig{
		MaxRetries:        3,
		InitialBackoff:    1 * time.Second,
		MaxBackoff:        30 * time.Second,
		BackoffMultiplier: 2.0,
		RetryOn5xxOnly:    true,
		Metrics:           nil, // Will be created by NewRetryHandler
		DLQ:               nil, // Will be set separately
	}
}

// RetryHandler handles webhook requests with retry logic
type RetryHandler struct {
	config *RetryConfig
	client *http.Client
}

// NewRetryHandler creates a new retry handler
func NewRetryHandler(config *RetryConfig) *RetryHandler {
	if config == nil {
		config = DefaultRetryConfig()
	}

	if config.Metrics == nil {
		config.Metrics = NewRetryMetrics()
	}

	if config.MaxRetries == 0 {
		config.MaxRetries = 3
	}

	if config.InitialBackoff == 0 {
		config.InitialBackoff = 1 * time.Second
	}

	if config.MaxBackoff == 0 {
		config.MaxBackoff = 30 * time.Second
	}

	if config.BackoffMultiplier == 0 {
		config.BackoffMultiplier = 2.0
	}

	return &RetryHandler{
		config: config,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Do executes an HTTP request with retry logic
func (h *RetryHandler) Do(req *http.Request) (*http.Response, error) {
	startTime := time.Now()
	defer func() {
		duration := time.Since(startTime).Seconds()
		h.config.Metrics.RetryDuration.Observe(duration)
	}()

	// Save the request body for retries
	var bodyBytes []byte
	if req.Body != nil {
		var err error
		bodyBytes, err = io.ReadAll(req.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read request body: %w", err)
		}
		req.Body.Close()
	}

	var lastErr error
	var lastResp *http.Response

	for attempt := 0; attempt <= h.config.MaxRetries; attempt++ {
		// Restore request body for each attempt
		if bodyBytes != nil {
			req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		}

		// Track retry attempt
		if attempt > 0 {
			h.config.Metrics.RetriesTotal.WithLabelValues(fmt.Sprintf("%d", attempt)).Inc()
		}

		// Execute request
		resp, err := h.client.Do(req)
		if err != nil {
			lastErr = err
			if attempt < h.config.MaxRetries {
				h.sleep(attempt)
				continue
			}
			break
		}

		lastResp = resp

		// Check if we should retry based on status code
		if h.shouldRetry(resp.StatusCode) {
			// Drain and close response body
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()

			lastErr = fmt.Errorf("request failed with status %d", resp.StatusCode)
			if attempt < h.config.MaxRetries {
				h.sleep(attempt)
				continue
			}
			break
		}

		// Success
		h.config.Metrics.RetrySuccessTotal.Inc()
		return resp, nil
	}

	// All retries failed
	h.config.Metrics.RetryFailureTotal.Inc()

	// Store in DLQ if configured
	if h.config.DLQ != nil {
		entry := &DLQEntry{
			Method:     req.Method,
			URL:        req.URL.String(),
			Headers:    req.Header,
			Body:       bodyBytes,
			Retries:    h.config.MaxRetries,
			LastError:  lastErr.Error(),
			CreatedAt:  time.Now(),
			LastAttempt: time.Now(),
		}

		if lastResp != nil {
			entry.StatusCode = lastResp.StatusCode
		}

		if err := h.config.DLQ.Add(entry); err != nil {
			// Log DLQ error but don't fail the request
			fmt.Printf("Failed to add to DLQ: %v\n", err)
		}
	}

	if lastErr != nil {
		return nil, fmt.Errorf("request failed after %d retries: %w", h.config.MaxRetries, lastErr)
	}

	return lastResp, nil
}

// shouldRetry checks if a request should be retried based on status code
func (h *RetryHandler) shouldRetry(statusCode int) bool {
	if h.config.RetryOn5xxOnly {
		// Retry only on 5xx errors
		return statusCode >= 500 && statusCode < 600
	}

	// Retry on 4xx and 5xx errors
	return statusCode >= 400
}

// sleep sleeps for the backoff duration based on the attempt number
func (h *RetryHandler) sleep(attempt int) {
	backoff := h.calculateBackoff(attempt)
	time.Sleep(backoff)
}

// calculateBackoff calculates the backoff duration for a given attempt
func (h *RetryHandler) calculateBackoff(attempt int) time.Duration {
	// Exponential backoff: initialBackoff * (multiplier ^ attempt)
	backoff := float64(h.config.InitialBackoff) * math.Pow(h.config.BackoffMultiplier, float64(attempt))

	// Cap at max backoff
	if backoff > float64(h.config.MaxBackoff) {
		backoff = float64(h.config.MaxBackoff)
	}

	return time.Duration(backoff)
}

// GetMetrics returns the metrics collector
func (h *RetryHandler) GetMetrics() *RetryMetrics {
	return h.config.Metrics
}
