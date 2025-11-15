package webhook

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// newTestRetryMetrics creates new retry metrics for testing with a fresh registry
func newTestRetryMetrics() *RetryMetrics {
	return NewRetryMetricsWithRegistry(prometheus.NewRegistry())
}

func TestNewRetryHandler(t *testing.T) {
	config := &RetryConfig{
		MaxRetries:        3,
		InitialBackoff:    1 * time.Second,
		MaxBackoff:        30 * time.Second,
		BackoffMultiplier: 2.0,
		Metrics:           newTestRetryMetrics(),
	}

	handler := NewRetryHandler(config)

	if handler == nil {
		t.Fatal("Expected retry handler to be created")
	}

	if handler.config.MaxRetries != 3 {
		t.Errorf("Expected MaxRetries 3, got %d", handler.config.MaxRetries)
	}

	if handler.config.Metrics == nil {
		t.Error("Expected metrics to be initialized")
	}
}

func TestNewRetryHandlerWithDefaults(t *testing.T) {
	handler := NewRetryHandler(nil)

	if handler == nil {
		t.Fatal("Expected retry handler to be created with defaults")
	}

	if handler.config.MaxRetries != 3 {
		t.Errorf("Expected default MaxRetries 3, got %d", handler.config.MaxRetries)
	}

	if handler.config.InitialBackoff != 1*time.Second {
		t.Errorf("Expected default InitialBackoff 1s, got %v", handler.config.InitialBackoff)
	}

	if handler.config.Metrics == nil {
		t.Error("Expected metrics to be initialized")
	}
}

func TestRetryHandlerSuccess(t *testing.T) {
	config := &RetryConfig{
		MaxRetries: 3,
		Metrics:    newTestRetryMetrics(),
	}
	handler := NewRetryHandler(config)

	// Create test server that always succeeds
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	}))
	defer server.Close()

	req, err := http.NewRequest("GET", server.URL, nil)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}

	resp, err := handler.Do(req)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}
}

func TestRetryHandlerRetryOn5xx(t *testing.T) {
	config := &RetryConfig{
		MaxRetries:     2,
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff:     100 * time.Millisecond,
		Metrics:        newTestRetryMetrics(),
	}
	handler := NewRetryHandler(config)

	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("error"))
		} else {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("success"))
		}
	}))
	defer server.Close()

	req, err := http.NewRequest("GET", server.URL, nil)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}

	resp, err := handler.Do(req)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	if attempts != 3 {
		t.Errorf("Expected 3 attempts, got %d", attempts)
	}
}

func TestRetryHandlerMaxRetriesExceeded(t *testing.T) {
	config := &RetryConfig{
		MaxRetries:     2,
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff:     100 * time.Millisecond,
		Metrics:        newTestRetryMetrics(),
	}
	handler := NewRetryHandler(config)

	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("error"))
	}))
	defer server.Close()

	req, err := http.NewRequest("GET", server.URL, nil)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}

	_, err = handler.Do(req)
	if err == nil {
		t.Error("Expected error after max retries exceeded")
	}

	expectedAttempts := config.MaxRetries + 1 // initial + retries
	if attempts != expectedAttempts {
		t.Errorf("Expected %d attempts, got %d", expectedAttempts, attempts)
	}
}

func TestRetryHandlerNoRetryOn4xx(t *testing.T) {
	config := &RetryConfig{
		MaxRetries:     2,
		RetryOn5xxOnly: true,
		Metrics:        newTestRetryMetrics(),
	}
	handler := NewRetryHandler(config)

	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("bad request"))
	}))
	defer server.Close()

	req, err := http.NewRequest("GET", server.URL, nil)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}

	resp, err := handler.Do(req)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", resp.StatusCode)
	}

	// Should not retry on 4xx when RetryOn5xxOnly is true
	if attempts != 1 {
		t.Errorf("Expected 1 attempt, got %d", attempts)
	}
}

func TestRetryHandlerWithBody(t *testing.T) {
	config := &RetryConfig{
		MaxRetries:     2,
		InitialBackoff: 10 * time.Millisecond,
		Metrics:        newTestRetryMetrics(),
	}
	handler := NewRetryHandler(config)

	requestBody := "test body"
	receivedBodies := []string{}

	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		body := make([]byte, len(requestBody))
		r.Body.Read(body)
		receivedBodies = append(receivedBodies, string(body))

		if attempts < 3 {
			w.WriteHeader(http.StatusInternalServerError)
		} else {
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	req, err := http.NewRequest("POST", server.URL, strings.NewReader(requestBody))
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}

	resp, err := handler.Do(req)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	// Verify body was sent in all attempts
	for i, body := range receivedBodies {
		if body != requestBody {
			t.Errorf("Attempt %d: expected body %q, got %q", i+1, requestBody, body)
		}
	}
}

func TestCalculateBackoff(t *testing.T) {
	config := &RetryConfig{
		InitialBackoff:    1 * time.Second,
		MaxBackoff:        10 * time.Second,
		BackoffMultiplier: 2.0,
		Metrics:           newTestRetryMetrics(),
	}
	handler := NewRetryHandler(config)

	tests := []struct {
		attempt  int
		expected time.Duration
	}{
		{0, 1 * time.Second},
		{1, 2 * time.Second},
		{2, 4 * time.Second},
		{3, 8 * time.Second},
		{4, 10 * time.Second}, // capped at MaxBackoff
		{5, 10 * time.Second}, // capped at MaxBackoff
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			backoff := handler.calculateBackoff(tt.attempt)
			if backoff != tt.expected {
				t.Errorf("Attempt %d: expected backoff %v, got %v", tt.attempt, tt.expected, backoff)
			}
		})
	}
}

func TestShouldRetry(t *testing.T) {
	tests := []struct {
		name           string
		statusCode     int
		retryOn5xxOnly bool
		expected       bool
	}{
		{"500 with 5xx only", 500, true, true},
		{"502 with 5xx only", 502, true, true},
		{"400 with 5xx only", 400, true, false},
		{"404 with 5xx only", 404, true, false},
		{"500 without 5xx only", 500, false, true},
		{"400 without 5xx only", 400, false, true},
		{"200 with 5xx only", 200, true, false},
		{"200 without 5xx only", 200, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &RetryConfig{
				RetryOn5xxOnly: tt.retryOn5xxOnly,
				Metrics:        newTestRetryMetrics(),
			}
			handler := NewRetryHandler(config)

			result := handler.shouldRetry(tt.statusCode)
			if result != tt.expected {
				t.Errorf("shouldRetry(%d) = %v, want %v", tt.statusCode, result, tt.expected)
			}
		})
	}
}

func TestDefaultRetryConfig(t *testing.T) {
	config := DefaultRetryConfig()

	if config.MaxRetries != 3 {
		t.Errorf("Expected default MaxRetries 3, got %d", config.MaxRetries)
	}

	if config.InitialBackoff != 1*time.Second {
		t.Errorf("Expected default InitialBackoff 1s, got %v", config.InitialBackoff)
	}

	if config.MaxBackoff != 30*time.Second {
		t.Errorf("Expected default MaxBackoff 30s, got %v", config.MaxBackoff)
	}

	if config.BackoffMultiplier != 2.0 {
		t.Errorf("Expected default BackoffMultiplier 2.0, got %v", config.BackoffMultiplier)
	}

	if !config.RetryOn5xxOnly {
		t.Error("Expected default RetryOn5xxOnly to be true")
	}

	if config.Metrics != nil {
		t.Error("Expected metrics to be nil from DefaultRetryConfig")
	}
}

func TestGetMetrics(t *testing.T) {
	metrics := newTestRetryMetrics()
	config := &RetryConfig{
		Metrics: metrics,
	}
	handler := NewRetryHandler(config)

	retrievedMetrics := handler.GetMetrics()

	if retrievedMetrics != metrics {
		t.Error("Expected GetMetrics to return the same metrics instance")
	}
}
