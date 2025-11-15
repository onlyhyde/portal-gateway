package quota

import (
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Storage defines the interface for quota persistence
type Storage interface {
	// GetUsage retrieves current usage for an API key
	GetUsage(keyID string) (*Usage, error)

	// UpdateUsage updates usage counters for an API key
	UpdateUsage(keyID string, requestsIncrement int64, bytesIncrement int64) error

	// ResetUsage resets usage counters for an API key
	ResetUsage(keyID string) error

	// ListAllUsage lists usage for all API keys
	ListAllUsage() ([]*Usage, error)

	// Close closes the storage connection
	Close() error
}

// Usage represents quota usage for an API key
type Usage struct {
	KeyID            string    `json:"key_id"`
	RequestCount     int64     `json:"request_count"`
	BytesTransferred int64     `json:"bytes_transferred"`
	LastRequestTime  time.Time `json:"last_request_time"`
	PeriodStart      time.Time `json:"period_start"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// SQLiteStorage implements Storage using SQLite
type SQLiteStorage struct {
	db *sql.DB
	mu sync.RWMutex
}

// Common errors
var (
	ErrStorageNotFound   = errors.New("quota usage not found")
	ErrStorageFailed     = errors.New("storage operation failed")
	ErrStorageInvalidKey = errors.New("invalid API key ID")
)

// NewSQLiteStorage creates a new SQLite-based quota storage
func NewSQLiteStorage(dbPath string) (*SQLiteStorage, error) {
	if dbPath == "" {
		return nil, errors.New("database path cannot be empty")
	}

	// Open database connection
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Configure connection pool
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	storage := &SQLiteStorage{
		db: db,
	}

	// Initialize schema
	if err := storage.initSchema(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to initialize schema: %w", err)
	}

	return storage, nil
}

// initSchema creates the quota_usage table if it doesn't exist
func (s *SQLiteStorage) initSchema() error {
	query := `
	CREATE TABLE IF NOT EXISTS quota_usage (
		key_id TEXT PRIMARY KEY,
		request_count INTEGER NOT NULL DEFAULT 0,
		bytes_transferred INTEGER NOT NULL DEFAULT 0,
		last_request_time DATETIME,
		period_start DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_period_start ON quota_usage(period_start);
	CREATE INDEX IF NOT EXISTS idx_updated_at ON quota_usage(updated_at);
	`

	_, err := s.db.Exec(query)
	return err
}

// GetUsage retrieves current usage for an API key
func (s *SQLiteStorage) GetUsage(keyID string) (*Usage, error) {
	if keyID == "" {
		return nil, ErrStorageInvalidKey
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `
	SELECT key_id, request_count, bytes_transferred, last_request_time, period_start, updated_at
	FROM quota_usage
	WHERE key_id = ?
	`

	usage := &Usage{}
	var lastRequestTime sql.NullTime

	err := s.db.QueryRow(query, keyID).Scan(
		&usage.KeyID,
		&usage.RequestCount,
		&usage.BytesTransferred,
		&lastRequestTime,
		&usage.PeriodStart,
		&usage.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		// Return zero usage for new keys
		return &Usage{
			KeyID:            keyID,
			RequestCount:     0,
			BytesTransferred: 0,
			PeriodStart:      getMonthStart(time.Now()),
			UpdatedAt:        time.Now(),
		}, nil
	}

	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrStorageFailed, err)
	}

	if lastRequestTime.Valid {
		usage.LastRequestTime = lastRequestTime.Time
	}

	return usage, nil
}

// UpdateUsage updates usage counters for an API key
func (s *SQLiteStorage) UpdateUsage(keyID string, requestsIncrement int64, bytesIncrement int64) error {
	if keyID == "" {
		return ErrStorageInvalidKey
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	periodStart := getMonthStart(now)

	query := `
	INSERT INTO quota_usage (key_id, request_count, bytes_transferred, last_request_time, period_start, updated_at)
	VALUES (?, ?, ?, ?, ?, ?)
	ON CONFLICT(key_id) DO UPDATE SET
		request_count = request_count + ?,
		bytes_transferred = bytes_transferred + ?,
		last_request_time = ?,
		updated_at = ?
	WHERE period_start = ?
	`

	result, err := s.db.Exec(query,
		keyID, requestsIncrement, bytesIncrement, now, periodStart, now,
		requestsIncrement, bytesIncrement, now, now, periodStart,
	)

	if err != nil {
		return fmt.Errorf("%w: %v", ErrStorageFailed, err)
	}

	// Check if period has changed (new month)
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		// Period changed, reset and insert new record
		return s.resetAndUpdate(keyID, requestsIncrement, bytesIncrement, now, periodStart)
	}

	return nil
}

// resetAndUpdate resets usage and updates with new values (for period rollover)
func (s *SQLiteStorage) resetAndUpdate(keyID string, requests int64, bytes int64, now time.Time, periodStart time.Time) error {
	// Delete old record
	_, err := s.db.Exec("DELETE FROM quota_usage WHERE key_id = ?", keyID)
	if err != nil {
		return fmt.Errorf("%w: failed to delete old record: %v", ErrStorageFailed, err)
	}

	// Insert new record
	_, err = s.db.Exec(`
		INSERT INTO quota_usage (key_id, request_count, bytes_transferred, last_request_time, period_start, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, keyID, requests, bytes, now, periodStart, now)

	if err != nil {
		return fmt.Errorf("%w: failed to insert new record: %v", ErrStorageFailed, err)
	}

	return nil
}

// ResetUsage resets usage counters for an API key
func (s *SQLiteStorage) ResetUsage(keyID string) error {
	if keyID == "" {
		return ErrStorageInvalidKey
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	periodStart := getMonthStart(now)

	query := `
	UPDATE quota_usage
	SET request_count = 0,
	    bytes_transferred = 0,
	    period_start = ?,
	    updated_at = ?
	WHERE key_id = ?
	`

	result, err := s.db.Exec(query, periodStart, now, keyID)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrStorageFailed, err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("%w: key %s", ErrStorageNotFound, keyID)
	}

	return nil
}

// ListAllUsage lists usage for all API keys
func (s *SQLiteStorage) ListAllUsage() ([]*Usage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `
	SELECT key_id, request_count, bytes_transferred, last_request_time, period_start, updated_at
	FROM quota_usage
	ORDER BY updated_at DESC
	`

	rows, err := s.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrStorageFailed, err)
	}
	defer rows.Close()

	var usages []*Usage
	for rows.Next() {
		usage := &Usage{}
		var lastRequestTime sql.NullTime

		err := rows.Scan(
			&usage.KeyID,
			&usage.RequestCount,
			&usage.BytesTransferred,
			&lastRequestTime,
			&usage.PeriodStart,
			&usage.UpdatedAt,
		)

		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrStorageFailed, err)
		}

		if lastRequestTime.Valid {
			usage.LastRequestTime = lastRequestTime.Time
		}

		usages = append(usages, usage)
	}

	return usages, nil
}

// Close closes the database connection
func (s *SQLiteStorage) Close() error {
	return s.db.Close()
}

// getMonthStart returns the start of the current month
func getMonthStart(t time.Time) time.Time {
	year, month, _ := t.Date()
	return time.Date(year, month, 1, 0, 0, 0, 0, t.Location())
}
