package logging

import (
	"context"
	"io"
	"log/slog"
	"os"
)

// LogFormat represents the log output format
type LogFormat string

const (
	// FormatJSON outputs logs in JSON format (production)
	FormatJSON LogFormat = "json"
	// FormatText outputs logs in human-readable format (development)
	FormatText LogFormat = "text"
)

// Logger wraps slog.Logger with additional functionality
type Logger struct {
	*slog.Logger
	levelVar *slog.LevelVar // For runtime level adjustment
}

// Config holds logger configuration
type Config struct {
	Level  slog.Level // Minimum log level
	Format LogFormat  // Output format (json or text)
	Output io.Writer  // Output destination (default: os.Stdout)
	// AddSource adds source file and line number to logs
	AddSource bool
}

// DefaultConfig returns default logger configuration
func DefaultConfig() *Config {
	return &Config{
		Level:     slog.LevelInfo,
		Format:    FormatJSON,
		Output:    os.Stdout,
		AddSource: false,
	}
}

// NewLogger creates a new structured logger
func NewLogger(cfg *Config) *Logger {
	if cfg == nil {
		cfg = DefaultConfig()
	}

	if cfg.Output == nil {
		cfg.Output = os.Stdout
	}

	// Create level var for runtime adjustment
	levelVar := new(slog.LevelVar)
	levelVar.Set(cfg.Level)

	var handler slog.Handler
	handlerOpts := &slog.HandlerOptions{
		Level:     levelVar,
		AddSource: cfg.AddSource,
	}

	switch cfg.Format {
	case FormatText:
		handler = slog.NewTextHandler(cfg.Output, handlerOpts)
	case FormatJSON:
		handler = slog.NewJSONHandler(cfg.Output, handlerOpts)
	default:
		handler = slog.NewJSONHandler(cfg.Output, handlerOpts)
	}

	logger := slog.New(handler)

	return &Logger{
		Logger:   logger,
		levelVar: levelVar,
	}
}

// SetLevel changes the minimum log level at runtime
func (l *Logger) SetLevel(level slog.Level) {
	l.levelVar.Set(level)
}

// GetLevel returns the current log level
func (l *Logger) GetLevel() slog.Level {
	return l.levelVar.Level()
}

// WithRequestID adds request ID to the logger context
func (l *Logger) WithRequestID(requestID string) *slog.Logger {
	return l.With("request_id", requestID)
}

// WithLeaseID adds lease ID to the logger context
func (l *Logger) WithLeaseID(leaseID string) *slog.Logger {
	return l.With("lease_id", leaseID)
}

// WithKeyID adds API key ID to the logger context (NOT the key itself)
func (l *Logger) WithKeyID(keyID string) *slog.Logger {
	return l.With("key_id", keyID)
}

// WithContext adds context fields to the logger
func (l *Logger) WithContext(ctx context.Context) *slog.Logger {
	logger := l.Logger

	// Add request ID if available
	if requestID := getRequestID(ctx); requestID != "" {
		logger = logger.With("request_id", requestID)
	}

	// Add lease ID if available
	if leaseID := getLeaseID(ctx); leaseID != "" {
		logger = logger.With("lease_id", leaseID)
	}

	// Add key ID if available
	if keyID := getKeyID(ctx); keyID != "" {
		logger = logger.With("key_id", keyID)
	}

	return logger
}

// Context keys for structured logging
type contextKey string

const (
	requestIDKey contextKey = "request_id"
	leaseIDKey   contextKey = "lease_id"
	keyIDKey     contextKey = "key_id"
)

// ContextWithRequestID adds request ID to context
func ContextWithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, requestIDKey, requestID)
}

// ContextWithLeaseID adds lease ID to context
func ContextWithLeaseID(ctx context.Context, leaseID string) context.Context {
	return context.WithValue(ctx, leaseIDKey, leaseID)
}

// ContextWithKeyID adds key ID to context
func ContextWithKeyID(ctx context.Context, keyID string) context.Context {
	return context.WithValue(ctx, keyIDKey, keyID)
}

// getRequestID retrieves request ID from context
func getRequestID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if value := ctx.Value(requestIDKey); value != nil {
		if requestID, ok := value.(string); ok {
			return requestID
		}
	}
	return ""
}

// getLeaseID retrieves lease ID from context
func getLeaseID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	// Try both context keys (for compatibility)
	if value := ctx.Value(leaseIDKey); value != nil {
		if leaseID, ok := value.(string); ok {
			return leaseID
		}
	}
	if value := ctx.Value(contextKey("lease_id")); value != nil {
		if leaseID, ok := value.(string); ok {
			return leaseID
		}
	}
	return ""
}

// getKeyID retrieves key ID from context
func getKeyID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if value := ctx.Value(keyIDKey); value != nil {
		if keyID, ok := value.(string); ok {
			return keyID
		}
	}
	return ""
}

// Global default logger
var defaultLogger *Logger

func init() {
	defaultLogger = NewLogger(DefaultConfig())
}

// Default returns the global default logger
func Default() *Logger {
	return defaultLogger
}

// SetDefault sets the global default logger
func SetDefault(logger *Logger) {
	defaultLogger = logger
}

// Convenience functions using the default logger

// Debug logs a debug message
func Debug(msg string, args ...any) {
	defaultLogger.Debug(msg, args...)
}

// Info logs an info message
func Info(msg string, args ...any) {
	defaultLogger.Info(msg, args...)
}

// Warn logs a warning message
func Warn(msg string, args ...any) {
	defaultLogger.Warn(msg, args...)
}

// Error logs an error message
func Error(msg string, args ...any) {
	defaultLogger.Error(msg, args...)
}

// DebugContext logs a debug message with context
func DebugContext(ctx context.Context, msg string, args ...any) {
	defaultLogger.WithContext(ctx).Debug(msg, args...)
}

// InfoContext logs an info message with context
func InfoContext(ctx context.Context, msg string, args ...any) {
	defaultLogger.WithContext(ctx).Info(msg, args...)
}

// WarnContext logs a warning message with context
func WarnContext(ctx context.Context, msg string, args ...any) {
	defaultLogger.WithContext(ctx).Warn(msg, args...)
}

// ErrorContext logs an error message with context
func ErrorContext(ctx context.Context, msg string, args ...any) {
	defaultLogger.WithContext(ctx).Error(msg, args...)
}
