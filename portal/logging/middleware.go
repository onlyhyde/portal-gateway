package logging

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"time"
)

// LoggingMiddleware provides request logging
type LoggingMiddleware struct {
	logger *Logger
}

// NewLoggingMiddleware creates a new logging middleware
func NewLoggingMiddleware(logger *Logger) *LoggingMiddleware {
	if logger == nil {
		logger = Default()
	}

	return &LoggingMiddleware{
		logger: logger,
	}
}

// Middleware returns an http.Handler that logs requests
func (m *LoggingMiddleware) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Generate request ID
		requestID := generateRequestID()

		// Add request ID to context
		ctx := ContextWithRequestID(r.Context(), requestID)
		r = r.WithContext(ctx)

		// Wrap response writer to capture status code
		wrapped := &loggingResponseWriter{
			ResponseWriter: w,
			statusCode:     http.StatusOK,
		}

		// Start timing
		start := time.Now()

		// Log request start
		m.logger.WithContext(ctx).Info("Request started",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.String("remote_addr", r.RemoteAddr),
			slog.String("user_agent", r.UserAgent()),
		)

		// Process request
		next.ServeHTTP(wrapped, r)

		// Calculate duration
		duration := time.Since(start)

		// Log request completion
		m.logger.WithContext(ctx).Info("Request completed",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", wrapped.statusCode),
			slog.Duration("duration", duration),
			slog.Int("bytes_written", wrapped.bytesWritten),
		)
	})
}

// loggingResponseWriter wraps http.ResponseWriter to capture status code
type loggingResponseWriter struct {
	http.ResponseWriter
	statusCode   int
	bytesWritten int
}

func (rw *loggingResponseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *loggingResponseWriter) Write(b []byte) (int, error) {
	n, err := rw.ResponseWriter.Write(b)
	rw.bytesWritten += n
	return n, err
}

// generateRequestID generates a unique request ID
func generateRequestID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Fallback to timestamp-based ID if random fails
		return hex.EncodeToString([]byte(time.Now().Format("20060102150405.000000")))
	}
	return hex.EncodeToString(b)
}

// GetRequestID retrieves the request ID from context
func GetRequestID(ctx interface{}) string {
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

	value := getter.Value(requestIDKey)
	if value == nil {
		return ""
	}

	if requestID, ok := value.(string); ok {
		return requestID
	}

	return ""
}
