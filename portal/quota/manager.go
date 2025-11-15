package quota

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// QuotaLimit defines quota limits for an API key
type QuotaLimit struct {
	KeyID                 string `json:"key_id"`
	MonthlyRequestLimit   int64  `json:"monthly_request_limit"`   // 0 = unlimited
	MonthlyBytesLimit     int64  `json:"monthly_bytes_limit"`     // 0 = unlimited (in bytes)
	ConcurrentConnections int    `json:"concurrent_connections"`  // 0 = unlimited
}

// QuotaStatus represents the current quota status for an API key
type QuotaStatus struct {
	KeyID                  string    `json:"key_id"`
	RequestCount           int64     `json:"request_count"`
	RequestLimit           int64     `json:"request_limit"`
	RequestRemaining       int64     `json:"request_remaining"`
	BytesTransferred       int64     `json:"bytes_transferred"`
	BytesLimit             int64     `json:"bytes_limit"`
	BytesRemaining         int64     `json:"bytes_remaining"`
	ActiveConnections      int       `json:"active_connections"`
	ConcurrentConnLimit    int       `json:"concurrent_conn_limit"`
	PeriodStart            time.Time `json:"period_start"`
	PeriodEnd              time.Time `json:"period_end"`
	QuotaExceeded          bool      `json:"quota_exceeded"`
	QuotaExceededReason    string    `json:"quota_exceeded_reason,omitempty"`
}

// Manager manages quota limits and enforcement
type Manager struct {
	storage             Storage
	limits              map[string]*QuotaLimit // keyID -> limit
	activeConnections   map[string]int         // keyID -> count
	defaultRequestLimit int64
	defaultBytesLimit   int64
	defaultConnLimit    int
	mu                  sync.RWMutex
	connMu              sync.Mutex
}

// Common errors
var (
	ErrQuotaExceeded        = errors.New("quota exceeded")
	ErrRequestQuotaExceeded = errors.New("monthly request quota exceeded")
	ErrBytesQuotaExceeded   = errors.New("monthly data transfer quota exceeded")
	ErrConnectionLimit      = errors.New("concurrent connection limit exceeded")
	ErrInvalidLimit         = errors.New("invalid quota limit")
)

// NewManager creates a new quota manager
func NewManager(storage Storage, defaultRequestLimit int64, defaultBytesLimit int64, defaultConnLimit int) *Manager {
	if defaultRequestLimit <= 0 {
		defaultRequestLimit = 1000000 // Default: 1M requests/month
	}
	if defaultBytesLimit <= 0 {
		defaultBytesLimit = 100 * 1024 * 1024 * 1024 // Default: 100 GB/month
	}
	if defaultConnLimit <= 0 {
		defaultConnLimit = 100 // Default: 100 concurrent connections
	}

	return &Manager{
		storage:             storage,
		limits:              make(map[string]*QuotaLimit),
		activeConnections:   make(map[string]int),
		defaultRequestLimit: defaultRequestLimit,
		defaultBytesLimit:   defaultBytesLimit,
		defaultConnLimit:    defaultConnLimit,
	}
}

// SetLimit sets quota limit for an API key
func (m *Manager) SetLimit(limit *QuotaLimit) error {
	if limit == nil {
		return errors.New("quota limit cannot be nil")
	}

	if limit.KeyID == "" {
		return errors.New("key ID cannot be empty")
	}

	if limit.MonthlyRequestLimit < 0 || limit.MonthlyBytesLimit < 0 || limit.ConcurrentConnections < 0 {
		return ErrInvalidLimit
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.limits[limit.KeyID] = limit
	return nil
}

// GetLimit retrieves quota limit for an API key
func (m *Manager) GetLimit(keyID string) *QuotaLimit {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if limit, exists := m.limits[keyID]; exists {
		return limit
	}

	// Return default limits
	return &QuotaLimit{
		KeyID:                 keyID,
		MonthlyRequestLimit:   m.defaultRequestLimit,
		MonthlyBytesLimit:     m.defaultBytesLimit,
		ConcurrentConnections: m.defaultConnLimit,
	}
}

// RemoveLimit removes quota limit for an API key (reverts to default)
func (m *Manager) RemoveLimit(keyID string) error {
	if keyID == "" {
		return errors.New("key ID cannot be empty")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.limits, keyID)
	return nil
}

// ListLimits returns all configured quota limits
func (m *Manager) ListLimits() []*QuotaLimit {
	m.mu.RLock()
	defer m.mu.RUnlock()

	limits := make([]*QuotaLimit, 0, len(m.limits))
	for _, limit := range m.limits {
		limits = append(limits, limit)
	}
	return limits
}

// CheckQuota checks if a request is allowed under quota limits
func (m *Manager) CheckQuota(keyID string, estimatedBytes int64) error {
	if keyID == "" {
		return errors.New("key ID cannot be empty")
	}

	// Get current usage
	usage, err := m.storage.GetUsage(keyID)
	if err != nil {
		return fmt.Errorf("failed to get usage: %w", err)
	}

	// Get quota limit
	limit := m.GetLimit(keyID)

	// Check request quota
	if limit.MonthlyRequestLimit > 0 && usage.RequestCount >= limit.MonthlyRequestLimit {
		return fmt.Errorf("%w: %d/%d requests used", ErrRequestQuotaExceeded, usage.RequestCount, limit.MonthlyRequestLimit)
	}

	// Check bytes quota
	if limit.MonthlyBytesLimit > 0 {
		projectedBytes := usage.BytesTransferred + estimatedBytes
		if projectedBytes > limit.MonthlyBytesLimit {
			return fmt.Errorf("%w: %d/%d bytes used", ErrBytesQuotaExceeded, usage.BytesTransferred, limit.MonthlyBytesLimit)
		}
	}

	// Check concurrent connections
	m.connMu.Lock()
	activeConns := m.activeConnections[keyID]
	m.connMu.Unlock()

	if limit.ConcurrentConnections > 0 && activeConns >= limit.ConcurrentConnections {
		return fmt.Errorf("%w: %d/%d connections", ErrConnectionLimit, activeConns, limit.ConcurrentConnections)
	}

	return nil
}

// RecordRequest records a request and updates usage
func (m *Manager) RecordRequest(keyID string, bytesTransferred int64) error {
	if keyID == "" {
		return errors.New("key ID cannot be empty")
	}

	return m.storage.UpdateUsage(keyID, 1, bytesTransferred)
}

// AcquireConnection increments the active connection count
func (m *Manager) AcquireConnection(keyID string) error {
	if keyID == "" {
		return errors.New("key ID cannot be empty")
	}

	limit := m.GetLimit(keyID)

	m.connMu.Lock()
	defer m.connMu.Unlock()

	currentCount := m.activeConnections[keyID]
	if limit.ConcurrentConnections > 0 && currentCount >= limit.ConcurrentConnections {
		return fmt.Errorf("%w: %d/%d connections", ErrConnectionLimit, currentCount, limit.ConcurrentConnections)
	}

	m.activeConnections[keyID]++
	return nil
}

// ReleaseConnection decrements the active connection count
func (m *Manager) ReleaseConnection(keyID string) {
	if keyID == "" {
		return
	}

	m.connMu.Lock()
	defer m.connMu.Unlock()

	if count, exists := m.activeConnections[keyID]; exists && count > 0 {
		m.activeConnections[keyID]--
		if m.activeConnections[keyID] == 0 {
			delete(m.activeConnections, keyID)
		}
	}
}

// GetStatus returns the current quota status for an API key
func (m *Manager) GetStatus(keyID string) (*QuotaStatus, error) {
	if keyID == "" {
		return nil, errors.New("key ID cannot be empty")
	}

	// Get current usage
	usage, err := m.storage.GetUsage(keyID)
	if err != nil {
		return nil, fmt.Errorf("failed to get usage: %w", err)
	}

	// Get quota limit
	limit := m.GetLimit(keyID)

	// Calculate remaining quota
	requestRemaining := int64(0)
	if limit.MonthlyRequestLimit > 0 {
		requestRemaining = limit.MonthlyRequestLimit - usage.RequestCount
		if requestRemaining < 0 {
			requestRemaining = 0
		}
	}

	bytesRemaining := int64(0)
	if limit.MonthlyBytesLimit > 0 {
		bytesRemaining = limit.MonthlyBytesLimit - usage.BytesTransferred
		if bytesRemaining < 0 {
			bytesRemaining = 0
		}
	}

	// Get active connections
	m.connMu.Lock()
	activeConns := m.activeConnections[keyID]
	m.connMu.Unlock()

	// Calculate period end
	periodEnd := getMonthEnd(usage.PeriodStart)

	// Check if quota is exceeded
	quotaExceeded := false
	quotaExceededReason := ""

	if limit.MonthlyRequestLimit > 0 && usage.RequestCount >= limit.MonthlyRequestLimit {
		quotaExceeded = true
		quotaExceededReason = "Monthly request quota exceeded"
	} else if limit.MonthlyBytesLimit > 0 && usage.BytesTransferred >= limit.MonthlyBytesLimit {
		quotaExceeded = true
		quotaExceededReason = "Monthly data transfer quota exceeded"
	}

	return &QuotaStatus{
		KeyID:                keyID,
		RequestCount:         usage.RequestCount,
		RequestLimit:         limit.MonthlyRequestLimit,
		RequestRemaining:     requestRemaining,
		BytesTransferred:     usage.BytesTransferred,
		BytesLimit:           limit.MonthlyBytesLimit,
		BytesRemaining:       bytesRemaining,
		ActiveConnections:    activeConns,
		ConcurrentConnLimit:  limit.ConcurrentConnections,
		PeriodStart:          usage.PeriodStart,
		PeriodEnd:            periodEnd,
		QuotaExceeded:        quotaExceeded,
		QuotaExceededReason:  quotaExceededReason,
	}, nil
}

// ResetQuota resets quota for an API key
func (m *Manager) ResetQuota(keyID string) error {
	if keyID == "" {
		return errors.New("key ID cannot be empty")
	}

	return m.storage.ResetUsage(keyID)
}

// Close closes the quota manager
func (m *Manager) Close() error {
	if m.storage != nil {
		return m.storage.Close()
	}
	return nil
}

// getMonthEnd returns the end of the month
func getMonthEnd(periodStart time.Time) time.Time {
	return periodStart.AddDate(0, 1, 0).Add(-time.Second)
}
