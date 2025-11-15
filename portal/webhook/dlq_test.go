package webhook

import (
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// newTestDLQMetrics creates new DLQ metrics for testing with a fresh registry
func newTestDLQMetrics() *DLQMetrics {
	return NewDLQMetricsWithRegistry(prometheus.NewRegistry())
}

func TestNewDLQ(t *testing.T) {
	dbPath := "test_dlq.db"
	defer os.Remove(dbPath)

	dlq, err := NewDLQWithMetrics(dbPath, newTestDLQMetrics())
	if err != nil {
		t.Fatalf("Failed to create DLQ: %v", err)
	}
	defer dlq.Close()

	if dlq == nil {
		t.Fatal("Expected DLQ to be created")
	}

	if dlq.metrics == nil {
		t.Error("Expected metrics to be initialized")
	}
}

func TestDLQAdd(t *testing.T) {
	dbPath := "test_dlq_add.db"
	defer os.Remove(dbPath)

	dlq, err := NewDLQWithMetrics(dbPath, newTestDLQMetrics())
	if err != nil {
		t.Fatalf("Failed to create DLQ: %v", err)
	}
	defer dlq.Close()

	entry := &DLQEntry{
		Method:      "POST",
		URL:         "http://example.com/webhook",
		Headers:     http.Header{"Content-Type": []string{"application/json"}},
		Body:        []byte(`{"test": "data"}`),
		StatusCode:  500,
		Retries:     3,
		LastError:   "request failed with status 500",
		CreatedAt:   time.Now(),
		LastAttempt: time.Now(),
	}

	err = dlq.Add(entry)
	if err != nil {
		t.Errorf("Failed to add entry: %v", err)
	}

	if entry.ID == 0 {
		t.Error("Expected entry ID to be set")
	}

	// Verify entry was added
	count, err := dlq.Count()
	if err != nil {
		t.Errorf("Failed to count entries: %v", err)
	}

	if count != 1 {
		t.Errorf("Expected 1 entry, got %d", count)
	}
}

func TestDLQList(t *testing.T) {
	dbPath := "test_dlq_list.db"
	defer os.Remove(dbPath)

	dlq, err := NewDLQWithMetrics(dbPath, newTestDLQMetrics())
	if err != nil {
		t.Fatalf("Failed to create DLQ: %v", err)
	}
	defer dlq.Close()

	// Add multiple entries
	for i := 0; i < 5; i++ {
		entry := &DLQEntry{
			Method:      "POST",
			URL:         "http://example.com/webhook",
			Headers:     http.Header{},
			Body:        []byte(`{"test": "data"}`),
			StatusCode:  500,
			Retries:     3,
			LastError:   "error",
			CreatedAt:   time.Now(),
			LastAttempt: time.Now(),
		}
		dlq.Add(entry)
	}

	// List all entries
	entries, err := dlq.List(10, 0)
	if err != nil {
		t.Errorf("Failed to list entries: %v", err)
	}

	if len(entries) != 5 {
		t.Errorf("Expected 5 entries, got %d", len(entries))
	}

	// Test pagination
	entries, err = dlq.List(2, 0)
	if err != nil {
		t.Errorf("Failed to list entries: %v", err)
	}

	if len(entries) != 2 {
		t.Errorf("Expected 2 entries with limit, got %d", len(entries))
	}

	// Test offset
	entries, err = dlq.List(2, 2)
	if err != nil {
		t.Errorf("Failed to list entries: %v", err)
	}

	if len(entries) != 2 {
		t.Errorf("Expected 2 entries with offset, got %d", len(entries))
	}
}

func TestDLQGet(t *testing.T) {
	dbPath := "test_dlq_get.db"
	defer os.Remove(dbPath)

	dlq, err := NewDLQWithMetrics(dbPath, newTestDLQMetrics())
	if err != nil {
		t.Fatalf("Failed to create DLQ: %v", err)
	}
	defer dlq.Close()

	originalEntry := &DLQEntry{
		Method:      "POST",
		URL:         "http://example.com/webhook",
		Headers:     http.Header{"Content-Type": []string{"application/json"}},
		Body:        []byte(`{"test": "data"}`),
		StatusCode:  500,
		Retries:     3,
		LastError:   "error",
		CreatedAt:   time.Now(),
		LastAttempt: time.Now(),
	}

	err = dlq.Add(originalEntry)
	if err != nil {
		t.Fatalf("Failed to add entry: %v", err)
	}

	// Get the entry
	entry, err := dlq.Get(originalEntry.ID)
	if err != nil {
		t.Errorf("Failed to get entry: %v", err)
	}

	if entry.ID != originalEntry.ID {
		t.Errorf("Expected ID %d, got %d", originalEntry.ID, entry.ID)
	}

	if entry.Method != originalEntry.Method {
		t.Errorf("Expected method %s, got %s", originalEntry.Method, entry.Method)
	}

	if entry.URL != originalEntry.URL {
		t.Errorf("Expected URL %s, got %s", originalEntry.URL, entry.URL)
	}

	if string(entry.Body) != string(originalEntry.Body) {
		t.Errorf("Expected body %s, got %s", string(originalEntry.Body), string(entry.Body))
	}
}

func TestDLQGetNotFound(t *testing.T) {
	dbPath := "test_dlq_get_notfound.db"
	defer os.Remove(dbPath)

	dlq, err := NewDLQWithMetrics(dbPath, newTestDLQMetrics())
	if err != nil {
		t.Fatalf("Failed to create DLQ: %v", err)
	}
	defer dlq.Close()

	_, err = dlq.Get(999)
	if err == nil {
		t.Error("Expected error when getting non-existent entry")
	}
}

func TestDLQDelete(t *testing.T) {
	dbPath := "test_dlq_delete.db"
	defer os.Remove(dbPath)

	dlq, err := NewDLQWithMetrics(dbPath, newTestDLQMetrics())
	if err != nil {
		t.Fatalf("Failed to create DLQ: %v", err)
	}
	defer dlq.Close()

	entry := &DLQEntry{
		Method:      "POST",
		URL:         "http://example.com/webhook",
		Headers:     http.Header{},
		Body:        []byte(`{"test": "data"}`),
		StatusCode:  500,
		Retries:     3,
		LastError:   "error",
		CreatedAt:   time.Now(),
		LastAttempt: time.Now(),
	}

	err = dlq.Add(entry)
	if err != nil {
		t.Fatalf("Failed to add entry: %v", err)
	}

	// Delete the entry
	err = dlq.Delete(entry.ID)
	if err != nil {
		t.Errorf("Failed to delete entry: %v", err)
	}

	// Verify entry was deleted
	count, err := dlq.Count()
	if err != nil {
		t.Errorf("Failed to count entries: %v", err)
	}

	if count != 0 {
		t.Errorf("Expected 0 entries after delete, got %d", count)
	}

	// Try to get deleted entry
	_, err = dlq.Get(entry.ID)
	if err == nil {
		t.Error("Expected error when getting deleted entry")
	}
}

func TestDLQDeleteNotFound(t *testing.T) {
	dbPath := "test_dlq_delete_notfound.db"
	defer os.Remove(dbPath)

	dlq, err := NewDLQWithMetrics(dbPath, newTestDLQMetrics())
	if err != nil {
		t.Fatalf("Failed to create DLQ: %v", err)
	}
	defer dlq.Close()

	err = dlq.Delete(999)
	if err == nil {
		t.Error("Expected error when deleting non-existent entry")
	}
}

func TestDLQCount(t *testing.T) {
	dbPath := "test_dlq_count.db"
	defer os.Remove(dbPath)

	dlq, err := NewDLQWithMetrics(dbPath, newTestDLQMetrics())
	if err != nil {
		t.Fatalf("Failed to create DLQ: %v", err)
	}
	defer dlq.Close()

	// Initial count should be 0
	count, err := dlq.Count()
	if err != nil {
		t.Errorf("Failed to count entries: %v", err)
	}

	if count != 0 {
		t.Errorf("Expected 0 entries initially, got %d", count)
	}

	// Add entries
	for i := 0; i < 3; i++ {
		entry := &DLQEntry{
			Method:      "POST",
			URL:         "http://example.com/webhook",
			Headers:     http.Header{},
			Body:        []byte(`{"test": "data"}`),
			StatusCode:  500,
			Retries:     3,
			LastError:   "error",
			CreatedAt:   time.Now(),
			LastAttempt: time.Now(),
		}
		dlq.Add(entry)
	}

	count, err = dlq.Count()
	if err != nil {
		t.Errorf("Failed to count entries: %v", err)
	}

	if count != 3 {
		t.Errorf("Expected 3 entries, got %d", count)
	}
}

func TestDLQPersistence(t *testing.T) {
	dbPath := "test_dlq_persistence.db"
	defer os.Remove(dbPath)

	// Create DLQ and add entry
	dlq1, err := NewDLQWithMetrics(dbPath, newTestDLQMetrics())
	if err != nil {
		t.Fatalf("Failed to create DLQ: %v", err)
	}

	entry := &DLQEntry{
		Method:      "POST",
		URL:         "http://example.com/webhook",
		Headers:     http.Header{"X-Test": []string{"value"}},
		Body:        []byte(`{"test": "data"}`),
		StatusCode:  500,
		Retries:     3,
		LastError:   "error",
		CreatedAt:   time.Now(),
		LastAttempt: time.Now(),
	}

	err = dlq1.Add(entry)
	if err != nil {
		t.Fatalf("Failed to add entry: %v", err)
	}

	entryID := entry.ID
	dlq1.Close()

	// Reopen DLQ and verify entry persists
	dlq2, err := NewDLQWithMetrics(dbPath, newTestDLQMetrics())
	if err != nil {
		t.Fatalf("Failed to reopen DLQ: %v", err)
	}
	defer dlq2.Close()

	persistedEntry, err := dlq2.Get(entryID)
	if err != nil {
		t.Errorf("Failed to get persisted entry: %v", err)
	}

	if persistedEntry.URL != entry.URL {
		t.Errorf("Expected URL %s, got %s", entry.URL, persistedEntry.URL)
	}

	if persistedEntry.Headers.Get("X-Test") != "value" {
		t.Error("Expected headers to be persisted")
	}
}

func TestGetDLQMetrics(t *testing.T) {
	dbPath := "test_dlq_metrics.db"
	defer os.Remove(dbPath)

	metrics := newTestDLQMetrics()
	dlq, err := NewDLQWithMetrics(dbPath, metrics)
	if err != nil {
		t.Fatalf("Failed to create DLQ: %v", err)
	}
	defer dlq.Close()

	retrievedMetrics := dlq.GetMetrics()

	if retrievedMetrics != metrics {
		t.Error("Expected GetMetrics to return the same metrics instance")
	}
}

func TestDLQClose(t *testing.T) {
	dbPath := "test_dlq_close.db"
	defer os.Remove(dbPath)

	dlq, err := NewDLQWithMetrics(dbPath, newTestDLQMetrics())
	if err != nil {
		t.Fatalf("Failed to create DLQ: %v", err)
	}

	err = dlq.Close()
	if err != nil {
		t.Errorf("Failed to close DLQ: %v", err)
	}

	// Closing again should not error
	err = dlq.Close()
	if err != nil {
		t.Errorf("Second close should not error: %v", err)
	}
}
