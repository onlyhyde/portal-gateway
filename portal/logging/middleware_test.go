package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestNewLoggingMiddleware tests creating a new logging middleware
func TestNewLoggingMiddleware(t *testing.T) {
	middleware := NewLoggingMiddleware(nil)
	if middleware == nil {
		t.Fatal("Expected middleware to be created, got nil")
	}

	if middleware.logger == nil {
		t.Fatal("Expected logger to be initialized, got nil")
	}
}

// TestLoggingMiddleware tests the logging middleware
func TestLoggingMiddleware(t *testing.T) {
	buf := &bytes.Buffer{}
	cfg := &Config{
		Level:  slog.LevelInfo,
		Format: FormatJSON,
		Output: buf,
	}

	logger := NewLogger(cfg)
	middleware := NewLoggingMiddleware(logger)

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

	// Verify logs were written
	output := buf.String()
	if output == "" {
		t.Fatal("Expected log output, got empty string")
	}

	// Should contain "Request started" and "Request completed"
	if !strings.Contains(output, "Request started") {
		t.Error("Expected log to contain 'Request started'")
	}

	if !strings.Contains(output, "Request completed") {
		t.Error("Expected log to contain 'Request completed'")
	}
}

// TestLoggingMiddlewareRequestID tests request ID generation
func TestLoggingMiddlewareRequestID(t *testing.T) {
	buf := &bytes.Buffer{}
	cfg := &Config{
		Level:  slog.LevelInfo,
		Format: FormatJSON,
		Output: buf,
	}

	logger := NewLogger(cfg)
	middleware := NewLoggingMiddleware(logger)

	var capturedRequestID string

	// Create test handler that captures request ID
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedRequestID = GetRequestID(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	wrappedHandler := middleware.Middleware(handler)

	// Make request
	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	wrappedHandler.ServeHTTP(rr, req)

	// Verify request ID was generated
	if capturedRequestID == "" {
		t.Error("Expected request ID to be generated")
	}

	// Verify request ID is in logs
	output := buf.String()
	if !strings.Contains(output, capturedRequestID) {
		t.Errorf("Expected log to contain request ID %q", capturedRequestID)
	}
}

// TestLoggingMiddlewareRequestDetails tests request details in logs
func TestLoggingMiddlewareRequestDetails(t *testing.T) {
	buf := &bytes.Buffer{}
	cfg := &Config{
		Level:  slog.LevelInfo,
		Format: FormatJSON,
		Output: buf,
	}

	logger := NewLogger(cfg)
	middleware := NewLoggingMiddleware(logger)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte("Created"))
	})

	wrappedHandler := middleware.Middleware(handler)

	// Make request with specific details
	req := httptest.NewRequest("POST", "/api/resource", nil)
	req.Header.Set("User-Agent", "TestAgent/1.0")
	rr := httptest.NewRecorder()

	wrappedHandler.ServeHTTP(rr, req)

	output := buf.String()

	// Parse log entries
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}

		var logEntry map[string]interface{}
		if err := json.Unmarshal([]byte(line), &logEntry); err != nil {
			continue
		}

		// Check for request details
		if method, ok := logEntry["method"].(string); ok {
			if method != "POST" {
				t.Errorf("Expected method POST, got %s", method)
			}
		}

		if path, ok := logEntry["path"].(string); ok {
			if path != "/api/resource" {
				t.Errorf("Expected path /api/resource, got %s", path)
			}
		}

		// Check completion log for status code
		if msg, ok := logEntry["msg"].(string); ok && msg == "Request completed" {
			if status, ok := logEntry["status"].(float64); ok {
				if int(status) != http.StatusCreated {
					t.Errorf("Expected status 201, got %d", int(status))
				}
			}
		}
	}
}

// TestLoggingResponseWriter tests the response writer wrapper
func TestLoggingResponseWriter(t *testing.T) {
	rr := httptest.NewRecorder()
	wrapped := &loggingResponseWriter{
		ResponseWriter: rr,
		statusCode:     http.StatusOK,
	}

	// Write header
	wrapped.WriteHeader(http.StatusNotFound)
	if wrapped.statusCode != http.StatusNotFound {
		t.Errorf("Expected status code 404, got %d", wrapped.statusCode)
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

// TestGenerateRequestID tests request ID generation
func TestGenerateRequestID(t *testing.T) {
	// Generate multiple IDs
	ids := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id := generateRequestID()

		// Verify ID is not empty
		if id == "" {
			t.Error("Expected non-empty request ID")
		}

		// Verify ID is unique
		if ids[id] {
			t.Errorf("Duplicate request ID generated: %s", id)
		}

		ids[id] = true

		// Verify ID is hexadecimal
		if len(id) != 32 { // 16 bytes = 32 hex characters
			t.Errorf("Expected ID length 32, got %d", len(id))
		}
	}
}

// TestGetRequestIDFromContext tests retrieving request ID from context
func TestGetRequestIDFromContext(t *testing.T) {
	// Test with nil context
	requestID := GetRequestID(nil)
	if requestID != "" {
		t.Errorf("Expected empty string for nil context, got %q", requestID)
	}

	// Test with context containing request ID
	ctx := ContextWithRequestID(context.Background(), "test-request-123")
	requestID = GetRequestID(ctx)
	if requestID != "test-request-123" {
		t.Errorf("Expected 'test-request-123', got %q", requestID)
	}

	// Test with context without request ID
	ctx = context.Background()
	requestID = GetRequestID(ctx)
	if requestID != "" {
		t.Errorf("Expected empty string for context without request ID, got %q", requestID)
	}
}

// TestLoggingMiddlewareWithContext tests logging with existing context values
func TestLoggingMiddlewareWithContext(t *testing.T) {
	buf := &bytes.Buffer{}
	cfg := &Config{
		Level:  slog.LevelInfo,
		Format: FormatJSON,
		Output: buf,
	}

	logger := NewLogger(cfg)
	middleware := NewLoggingMiddleware(logger)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Add lease ID to context during request processing
		ctx := ContextWithLeaseID(r.Context(), "lease-123")
		ctx = ContextWithKeyID(ctx, "key-456")

		// Log something with the enhanced context
		logger.WithContext(ctx).Info("Processing request")

		w.WriteHeader(http.StatusOK)
	})

	wrappedHandler := middleware.Middleware(handler)

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	wrappedHandler.ServeHTTP(rr, req)

	output := buf.String()

	// Verify lease_id and key_id are in the log for "Processing request"
	if strings.Contains(output, "Processing request") {
		// Parse the processing log entry
		lines := strings.Split(strings.TrimSpace(output), "\n")
		for _, line := range lines {
			if !strings.Contains(line, "Processing request") {
				continue
			}

			var logEntry map[string]interface{}
			if err := json.Unmarshal([]byte(line), &logEntry); err != nil {
				t.Fatalf("Failed to parse log entry: %v", err)
			}

			if leaseID, ok := logEntry["lease_id"].(string); !ok || leaseID != "lease-123" {
				t.Errorf("Expected lease_id 'lease-123', got %v", logEntry["lease_id"])
			}

			if keyID, ok := logEntry["key_id"].(string); !ok || keyID != "key-456" {
				t.Errorf("Expected key_id 'key-456', got %v", logEntry["key_id"])
			}
		}
	}
}
