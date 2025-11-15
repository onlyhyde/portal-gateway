package webhook

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// DLQMetrics holds DLQ metrics
type DLQMetrics struct {
	EntriesTotal   prometheus.Counter
	EntriesActive  prometheus.Gauge
	ReplayTotal    prometheus.Counter
	ReplaySuccess  prometheus.Counter
	ReplayFailure  prometheus.Counter
	DeletedTotal   prometheus.Counter
}

// NewDLQMetrics creates new DLQ metrics
func NewDLQMetrics() *DLQMetrics {
	return NewDLQMetricsWithRegistry(prometheus.DefaultRegisterer)
}

// NewDLQMetricsWithRegistry creates new DLQ metrics with a custom registry
func NewDLQMetricsWithRegistry(reg prometheus.Registerer) *DLQMetrics {
	if reg == nil {
		reg = prometheus.NewRegistry()
	}

	factory := promauto.With(reg)

	return &DLQMetrics{
		EntriesTotal: factory.NewCounter(
			prometheus.CounterOpts{
				Name: "portal_dlq_entries_total",
				Help: "Total number of DLQ entries added",
			},
		),
		EntriesActive: factory.NewGauge(
			prometheus.GaugeOpts{
				Name: "portal_dlq_entries_active",
				Help: "Number of active DLQ entries",
			},
		),
		ReplayTotal: factory.NewCounter(
			prometheus.CounterOpts{
				Name: "portal_dlq_replay_total",
				Help: "Total number of DLQ replay attempts",
			},
		),
		ReplaySuccess: factory.NewCounter(
			prometheus.CounterOpts{
				Name: "portal_dlq_replay_success_total",
				Help: "Total number of successful DLQ replays",
			},
		),
		ReplayFailure: factory.NewCounter(
			prometheus.CounterOpts{
				Name: "portal_dlq_replay_failure_total",
				Help: "Total number of failed DLQ replays",
			},
		),
		DeletedTotal: factory.NewCounter(
			prometheus.CounterOpts{
				Name: "portal_dlq_deleted_total",
				Help: "Total number of DLQ entries deleted",
			},
		),
	}
}

// DLQEntry represents a failed request in the DLQ
type DLQEntry struct {
	ID          int64             `json:"id"`
	Method      string            `json:"method"`
	URL         string            `json:"url"`
	Headers     http.Header       `json:"headers"`
	Body        []byte            `json:"body"`
	StatusCode  int               `json:"status_code"`
	Retries     int               `json:"retries"`
	LastError   string            `json:"last_error"`
	CreatedAt   time.Time         `json:"created_at"`
	LastAttempt time.Time         `json:"last_attempt"`
}

// DLQ represents a dead letter queue for failed webhook requests
type DLQ struct {
	db      *sql.DB
	metrics *DLQMetrics
	mutex   sync.RWMutex
}

// NewDLQ creates a new DLQ with SQLite backend
func NewDLQ(dbPath string) (*DLQ, error) {
	return NewDLQWithMetrics(dbPath, nil)
}

// NewDLQWithMetrics creates a new DLQ with custom metrics
func NewDLQWithMetrics(dbPath string, metrics *DLQMetrics) (*DLQ, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Create table
	schema := `
	CREATE TABLE IF NOT EXISTS dlq_entries (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		method TEXT NOT NULL,
		url TEXT NOT NULL,
		headers TEXT NOT NULL,
		body BLOB,
		status_code INTEGER,
		retries INTEGER NOT NULL,
		last_error TEXT NOT NULL,
		created_at DATETIME NOT NULL,
		last_attempt DATETIME NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_created_at ON dlq_entries(created_at);
	`

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to create schema: %w", err)
	}

	if metrics == nil {
		metrics = NewDLQMetrics()
	}

	dlq := &DLQ{
		db:      db,
		metrics: metrics,
	}

	// Update active entries metric
	dlq.updateActiveEntriesMetric()

	return dlq, nil
}

// Add adds a failed request to the DLQ
func (d *DLQ) Add(entry *DLQEntry) error {
	d.mutex.Lock()
	defer d.mutex.Unlock()

	// Serialize headers
	headersJSON, err := json.Marshal(entry.Headers)
	if err != nil {
		return fmt.Errorf("failed to marshal headers: %w", err)
	}

	query := `
		INSERT INTO dlq_entries (method, url, headers, body, status_code, retries, last_error, created_at, last_attempt)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	result, err := d.db.Exec(query,
		entry.Method,
		entry.URL,
		string(headersJSON),
		entry.Body,
		entry.StatusCode,
		entry.Retries,
		entry.LastError,
		entry.CreatedAt,
		entry.LastAttempt,
	)
	if err != nil {
		return fmt.Errorf("failed to insert entry: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("failed to get last insert ID: %w", err)
	}

	entry.ID = id

	// Update metrics
	d.metrics.EntriesTotal.Inc()
	d.metrics.EntriesActive.Inc()

	return nil
}

// List returns all entries in the DLQ
func (d *DLQ) List(limit, offset int) ([]*DLQEntry, error) {
	d.mutex.RLock()
	defer d.mutex.RUnlock()

	query := `
		SELECT id, method, url, headers, body, status_code, retries, last_error, created_at, last_attempt
		FROM dlq_entries
		ORDER BY created_at DESC
		LIMIT ? OFFSET ?
	`

	rows, err := d.db.Query(query, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to query entries: %w", err)
	}
	defer rows.Close()

	var entries []*DLQEntry
	for rows.Next() {
		entry := &DLQEntry{}
		var headersJSON string

		err := rows.Scan(
			&entry.ID,
			&entry.Method,
			&entry.URL,
			&headersJSON,
			&entry.Body,
			&entry.StatusCode,
			&entry.Retries,
			&entry.LastError,
			&entry.CreatedAt,
			&entry.LastAttempt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan entry: %w", err)
		}

		// Deserialize headers
		if err := json.Unmarshal([]byte(headersJSON), &entry.Headers); err != nil {
			return nil, fmt.Errorf("failed to unmarshal headers: %w", err)
		}

		entries = append(entries, entry)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating entries: %w", err)
	}

	return entries, nil
}

// Get returns a specific entry from the DLQ
func (d *DLQ) Get(id int64) (*DLQEntry, error) {
	d.mutex.RLock()
	defer d.mutex.RUnlock()

	query := `
		SELECT id, method, url, headers, body, status_code, retries, last_error, created_at, last_attempt
		FROM dlq_entries
		WHERE id = ?
	`

	entry := &DLQEntry{}
	var headersJSON string

	err := d.db.QueryRow(query, id).Scan(
		&entry.ID,
		&entry.Method,
		&entry.URL,
		&headersJSON,
		&entry.Body,
		&entry.StatusCode,
		&entry.Retries,
		&entry.LastError,
		&entry.CreatedAt,
		&entry.LastAttempt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("entry not found: %d", id)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get entry: %w", err)
	}

	// Deserialize headers
	if err := json.Unmarshal([]byte(headersJSON), &entry.Headers); err != nil {
		return nil, fmt.Errorf("failed to unmarshal headers: %w", err)
	}

	return entry, nil
}

// Delete removes an entry from the DLQ
func (d *DLQ) Delete(id int64) error {
	d.mutex.Lock()
	defer d.mutex.Unlock()

	query := `DELETE FROM dlq_entries WHERE id = ?`

	result, err := d.db.Exec(query, id)
	if err != nil {
		return fmt.Errorf("failed to delete entry: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("entry not found: %d", id)
	}

	// Update metrics
	d.metrics.DeletedTotal.Inc()
	d.metrics.EntriesActive.Dec()

	return nil
}

// Count returns the total number of entries in the DLQ
func (d *DLQ) Count() (int, error) {
	d.mutex.RLock()
	defer d.mutex.RUnlock()

	query := `SELECT COUNT(*) FROM dlq_entries`

	var count int
	err := d.db.QueryRow(query).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count entries: %w", err)
	}

	return count, nil
}

// updateActiveEntriesMetric updates the active entries metric
func (d *DLQ) updateActiveEntriesMetric() {
	count, err := d.Count()
	if err != nil {
		return
	}

	d.metrics.EntriesActive.Set(float64(count))
}

// Close closes the DLQ database
func (d *DLQ) Close() error {
	if d.db != nil {
		return d.db.Close()
	}
	return nil
}

// GetMetrics returns the metrics collector
func (d *DLQ) GetMetrics() *DLQMetrics {
	return d.metrics
}
