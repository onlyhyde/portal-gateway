package quota

import (
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// contextKey is a custom type for context keys to avoid collisions
type contextKey string

const (
	// ContextKeyAPIKey is the context key for API key info
	ContextKeyAPIKey = contextKey("api_key_info")
)

// APIKeyInfo represents API key information in context
type APIKeyInfo struct {
	KeyID string
}

// QuotaMiddleware provides quota enforcement middleware
type QuotaMiddleware struct {
	manager *Manager
}

// NewQuotaMiddleware creates a new quota middleware
func NewQuotaMiddleware(manager *Manager) *QuotaMiddleware {
	if manager == nil {
		panic("quota manager cannot be nil")
	}

	return &QuotaMiddleware{
		manager: manager,
	}
}

// Middleware returns an http.Handler that enforces quota limits
func (m *QuotaMiddleware) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Get API key from context
		apiKeyInfo := getAPIKeyInfo(r.Context())
		if apiKeyInfo == nil {
			// No API key in context, skip quota check
			next.ServeHTTP(w, r)
			return
		}

		keyID := apiKeyInfo.KeyID

		// Estimate request size (content-length if available, otherwise use default)
		estimatedBytes := int64(1024) // Default: 1 KB
		if r.ContentLength > 0 {
			estimatedBytes = r.ContentLength
		}

		// Check quota before processing request
		if err := m.manager.CheckQuota(keyID, estimatedBytes); err != nil {
			m.handleQuotaExceeded(w, keyID, err)
			return
		}

		// Wrap response writer to capture response size
		wrapped := &responseWriter{
			ResponseWriter: w,
			statusCode:     http.StatusOK,
			bytesWritten:   0,
		}

		// Process request
		next.ServeHTTP(wrapped, r)

		// Record request after successful completion
		totalBytes := r.ContentLength + int64(wrapped.bytesWritten)
		if totalBytes < 0 {
			totalBytes = int64(wrapped.bytesWritten)
		}

		if err := m.manager.RecordRequest(keyID, totalBytes); err != nil {
			// Log error but don't fail the request
			// TODO: Add proper logging
		}

		// Add quota headers to response
		m.addQuotaHeaders(wrapped, keyID)
	})
}

// handleQuotaExceeded handles quota exceeded responses
func (m *QuotaMiddleware) handleQuotaExceeded(w http.ResponseWriter, keyID string, err error) {
	status, _ := m.manager.GetStatus(keyID)

	// Calculate retry-after (seconds until period end)
	retryAfter := 0
	if status != nil {
		retryAfter = int(time.Until(status.PeriodEnd).Seconds())
		if retryAfter < 0 {
			retryAfter = 0
		}
	}

	// Add headers
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Retry-After", strconv.Itoa(retryAfter))

	if status != nil {
		w.Header().Set("X-Quota-Limit-Requests", strconv.FormatInt(status.RequestLimit, 10))
		w.Header().Set("X-Quota-Remaining-Requests", "0")
		w.Header().Set("X-Quota-Limit-Bytes", strconv.FormatInt(status.BytesLimit, 10))
		w.Header().Set("X-Quota-Remaining-Bytes", "0")
		w.Header().Set("X-Quota-Reset", strconv.FormatInt(status.PeriodEnd.Unix(), 10))
	}

	w.WriteHeader(http.StatusTooManyRequests)

	// Determine error message
	errorType := "quota_exceeded"
	errorMessage := err.Error()

	if status != nil && status.QuotaExceededReason != "" {
		errorMessage = status.QuotaExceededReason
	}

	fmt.Fprintf(w, `{"error":"%s","message":"%s","retry_after":%d}`, errorType, errorMessage, retryAfter)
}

// addQuotaHeaders adds quota information to response headers
func (m *QuotaMiddleware) addQuotaHeaders(w http.ResponseWriter, keyID string) {
	status, err := m.manager.GetStatus(keyID)
	if err != nil {
		return
	}

	w.Header().Set("X-Quota-Limit-Requests", strconv.FormatInt(status.RequestLimit, 10))
	w.Header().Set("X-Quota-Remaining-Requests", strconv.FormatInt(status.RequestRemaining, 10))
	w.Header().Set("X-Quota-Limit-Bytes", strconv.FormatInt(status.BytesLimit, 10))
	w.Header().Set("X-Quota-Remaining-Bytes", strconv.FormatInt(status.BytesRemaining, 10))
	w.Header().Set("X-Quota-Reset", strconv.FormatInt(status.PeriodEnd.Unix(), 10))
}

// getAPIKeyInfo retrieves API key info from context
func getAPIKeyInfo(ctx interface{}) *APIKeyInfo {
	if ctx == nil {
		return nil
	}

	// Type assert to get the actual context
	type contextGetter interface {
		Value(key interface{}) interface{}
	}

	getter, ok := ctx.(contextGetter)
	if !ok {
		return nil
	}

	value := getter.Value(ContextKeyAPIKey)
	if value == nil {
		return nil
	}

	// Try to type assert to our APIKeyInfo
	if apiKeyInfo, ok := value.(*APIKeyInfo); ok {
		return apiKeyInfo
	}

	// Try to type assert to middleware.APIKeyInfo
	type middlewareAPIKeyInfo interface {
		GetKeyID() string
	}

	if info, ok := value.(middlewareAPIKeyInfo); ok {
		return &APIKeyInfo{
			KeyID: info.GetKeyID(),
		}
	}

	// Try struct with KeyID field
	type keyIDGetter struct {
		KeyID string
	}

	if v, ok := value.(*keyIDGetter); ok {
		return &APIKeyInfo{
			KeyID: v.KeyID,
		}
	}

	return nil
}

// responseWriter wraps http.ResponseWriter to capture response size
type responseWriter struct {
	http.ResponseWriter
	statusCode   int
	bytesWritten int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	n, err := rw.ResponseWriter.Write(b)
	rw.bytesWritten += n
	return n, err
}
