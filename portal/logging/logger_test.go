package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

// TestNewLogger tests creating a new logger
func TestNewLogger(t *testing.T) {
	logger := NewLogger(nil)
	if logger == nil {
		t.Fatal("Expected logger to be created, got nil")
	}

	if logger.Logger == nil {
		t.Fatal("Expected slog.Logger to be initialized, got nil")
	}
}

// TestNewLoggerWithConfig tests creating a logger with custom config
func TestNewLoggerWithConfig(t *testing.T) {
	cfg := &Config{
		Level:     slog.LevelDebug,
		Format:    FormatText,
		Output:    &bytes.Buffer{},
		AddSource: true,
	}

	logger := NewLogger(cfg)
	if logger == nil {
		t.Fatal("Expected logger to be created, got nil")
	}
}

// TestLoggerSetGetLevel tests setting and getting log level
func TestLoggerSetGetLevel(t *testing.T) {
	logger := NewLogger(DefaultConfig())

	// Set level to Debug
	logger.SetLevel(slog.LevelDebug)
	if logger.GetLevel() != slog.LevelDebug {
		t.Errorf("Expected level Debug, got %v", logger.GetLevel())
	}

	// Set level to Error
	logger.SetLevel(slog.LevelError)
	if logger.GetLevel() != slog.LevelError {
		t.Errorf("Expected level Error, got %v", logger.GetLevel())
	}
}

// TestLoggerJSONFormat tests JSON output format
func TestLoggerJSONFormat(t *testing.T) {
	buf := &bytes.Buffer{}
	cfg := &Config{
		Level:  slog.LevelInfo,
		Format: FormatJSON,
		Output: buf,
	}

	logger := NewLogger(cfg)
	logger.Info("test message", "key", "value")

	output := buf.String()
	if output == "" {
		t.Fatal("Expected log output, got empty string")
	}

	// Verify it's valid JSON
	var logEntry map[string]interface{}
	if err := json.Unmarshal([]byte(output), &logEntry); err != nil {
		t.Fatalf("Expected valid JSON, got error: %v", err)
	}

	// Verify message
	if msg, ok := logEntry["msg"].(string); !ok || msg != "test message" {
		t.Errorf("Expected message 'test message', got %v", logEntry["msg"])
	}

	// Verify key-value pair
	if val, ok := logEntry["key"].(string); !ok || val != "value" {
		t.Errorf("Expected key 'value', got %v", logEntry["key"])
	}
}

// TestLoggerTextFormat tests text output format
func TestLoggerTextFormat(t *testing.T) {
	buf := &bytes.Buffer{}
	cfg := &Config{
		Level:  slog.LevelInfo,
		Format: FormatText,
		Output: buf,
	}

	logger := NewLogger(cfg)
	logger.Info("test message", "key", "value")

	output := buf.String()
	if output == "" {
		t.Fatal("Expected log output, got empty string")
	}

	// Verify message is in output
	if !strings.Contains(output, "test message") {
		t.Errorf("Expected output to contain 'test message', got: %s", output)
	}

	// Verify key-value pair is in output
	if !strings.Contains(output, "key=value") {
		t.Errorf("Expected output to contain 'key=value', got: %s", output)
	}
}

// TestLoggerLevels tests different log levels
func TestLoggerLevels(t *testing.T) {
	buf := &bytes.Buffer{}
	cfg := &Config{
		Level:  slog.LevelInfo,
		Format: FormatJSON,
		Output: buf,
	}

	logger := NewLogger(cfg)

	// Debug should not be logged (level is Info)
	logger.Debug("debug message")
	if buf.Len() > 0 {
		t.Error("Debug message should not be logged when level is Info")
	}

	// Info should be logged
	buf.Reset()
	logger.Info("info message")
	if buf.Len() == 0 {
		t.Error("Info message should be logged when level is Info")
	}

	// Warn should be logged
	buf.Reset()
	logger.Warn("warn message")
	if buf.Len() == 0 {
		t.Error("Warn message should be logged when level is Info")
	}

	// Error should be logged
	buf.Reset()
	logger.Error("error message")
	if buf.Len() == 0 {
		t.Error("Error message should be logged when level is Info")
	}
}

// TestLoggerWithRequestID tests adding request ID to logger
func TestLoggerWithRequestID(t *testing.T) {
	buf := &bytes.Buffer{}
	cfg := &Config{
		Level:  slog.LevelInfo,
		Format: FormatJSON,
		Output: buf,
	}

	logger := NewLogger(cfg)
	loggerWithID := logger.WithRequestID("test-request-123")

	loggerWithID.Info("test message")

	output := buf.String()
	var logEntry map[string]interface{}
	if err := json.Unmarshal([]byte(output), &logEntry); err != nil {
		t.Fatalf("Expected valid JSON, got error: %v", err)
	}

	if requestID, ok := logEntry["request_id"].(string); !ok || requestID != "test-request-123" {
		t.Errorf("Expected request_id 'test-request-123', got %v", logEntry["request_id"])
	}
}

// TestLoggerWithContext tests adding context fields
func TestLoggerWithContext(t *testing.T) {
	buf := &bytes.Buffer{}
	cfg := &Config{
		Level:  slog.LevelInfo,
		Format: FormatJSON,
		Output: buf,
	}

	logger := NewLogger(cfg)

	// Create context with request ID, lease ID, and key ID
	ctx := ContextWithRequestID(context.Background(), "req-123")
	ctx = ContextWithLeaseID(ctx, "lease-456")
	ctx = ContextWithKeyID(ctx, "key-789")

	loggerWithContext := logger.WithContext(ctx)
	loggerWithContext.Info("test message")

	output := buf.String()
	var logEntry map[string]interface{}
	if err := json.Unmarshal([]byte(output), &logEntry); err != nil {
		t.Fatalf("Expected valid JSON, got error: %v", err)
	}

	// Verify all context fields
	if requestID, ok := logEntry["request_id"].(string); !ok || requestID != "req-123" {
		t.Errorf("Expected request_id 'req-123', got %v", logEntry["request_id"])
	}

	if leaseID, ok := logEntry["lease_id"].(string); !ok || leaseID != "lease-456" {
		t.Errorf("Expected lease_id 'lease-456', got %v", logEntry["lease_id"])
	}

	if keyID, ok := logEntry["key_id"].(string); !ok || keyID != "key-789" {
		t.Errorf("Expected key_id 'key-789', got %v", logEntry["key_id"])
	}
}

// TestContextFunctions tests context helper functions
func TestContextFunctions(t *testing.T) {
	ctx := context.Background()

	// Test request ID
	ctx = ContextWithRequestID(ctx, "req-123")
	if requestID := getRequestID(ctx); requestID != "req-123" {
		t.Errorf("Expected request ID 'req-123', got %q", requestID)
	}

	// Test lease ID
	ctx = ContextWithLeaseID(ctx, "lease-456")
	if leaseID := getLeaseID(ctx); leaseID != "lease-456" {
		t.Errorf("Expected lease ID 'lease-456', got %q", leaseID)
	}

	// Test key ID
	ctx = ContextWithKeyID(ctx, "key-789")
	if keyID := getKeyID(ctx); keyID != "key-789" {
		t.Errorf("Expected key ID 'key-789', got %q", keyID)
	}

	// Test nil context
	if requestID := getRequestID(nil); requestID != "" {
		t.Errorf("Expected empty string for nil context, got %q", requestID)
	}
}

// TestDefaultLogger tests the global default logger
func TestDefaultLogger(t *testing.T) {
	logger := Default()
	if logger == nil {
		t.Fatal("Expected default logger to exist, got nil")
	}

	// Test setting a new default
	newLogger := NewLogger(DefaultConfig())
	SetDefault(newLogger)

	if Default() != newLogger {
		t.Error("Expected default logger to be updated")
	}
}

// TestConvenienceFunctions tests global convenience functions
func TestConvenienceFunctions(t *testing.T) {
	buf := &bytes.Buffer{}
	cfg := &Config{
		Level:  slog.LevelDebug,
		Format: FormatJSON,
		Output: buf,
	}

	logger := NewLogger(cfg)
	SetDefault(logger)

	// Test Debug
	buf.Reset()
	Debug("debug message")
	if !strings.Contains(buf.String(), "debug message") {
		t.Error("Debug function should log message")
	}

	// Test Info
	buf.Reset()
	Info("info message")
	if !strings.Contains(buf.String(), "info message") {
		t.Error("Info function should log message")
	}

	// Test Warn
	buf.Reset()
	Warn("warn message")
	if !strings.Contains(buf.String(), "warn message") {
		t.Error("Warn function should log message")
	}

	// Test Error
	buf.Reset()
	Error("error message")
	if !strings.Contains(buf.String(), "error message") {
		t.Error("Error function should log message")
	}
}

// TestContextConvenienceFunctions tests global context convenience functions
func TestContextConvenienceFunctions(t *testing.T) {
	buf := &bytes.Buffer{}
	cfg := &Config{
		Level:  slog.LevelInfo,
		Format: FormatJSON,
		Output: buf,
	}

	logger := NewLogger(cfg)
	SetDefault(logger)

	ctx := ContextWithRequestID(context.Background(), "test-req")

	// Test InfoContext
	buf.Reset()
	InfoContext(ctx, "info message")
	output := buf.String()

	if !strings.Contains(output, "info message") {
		t.Error("InfoContext should log message")
	}

	if !strings.Contains(output, "test-req") {
		t.Error("InfoContext should include request ID from context")
	}
}
