package quota

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestNewManager tests creating a new quota manager
func TestNewManager(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	storage, err := NewSQLiteStorage(dbPath)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer storage.Close()

	manager := NewManager(storage, 1000000, 107374182400, 100)

	if manager == nil {
		t.Fatal("Expected manager to be created, got nil")
	}
}

// TestNewManagerDefaults tests default values
func TestNewManagerDefaults(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	storage, err := NewSQLiteStorage(dbPath)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer storage.Close()

	manager := NewManager(storage, 0, 0, 0)

	limit := manager.GetLimit("test-key")
	if limit.MonthlyRequestLimit <= 0 {
		t.Error("Default request limit should be positive")
	}

	if limit.MonthlyBytesLimit <= 0 {
		t.Error("Default bytes limit should be positive")
	}

	if limit.ConcurrentConnections <= 0 {
		t.Error("Default connection limit should be positive")
	}
}

// TestSetLimit tests setting quota limits
func TestSetLimit(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	storage, err := NewSQLiteStorage(dbPath)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer storage.Close()

	manager := NewManager(storage, 1000000, 107374182400, 100)

	limit := &QuotaLimit{
		KeyID:                 "test-key",
		MonthlyRequestLimit:   5000,
		MonthlyBytesLimit:     10485760,
		ConcurrentConnections: 50,
	}

	err = manager.SetLimit(limit)
	if err != nil {
		t.Fatalf("Failed to set limit: %v", err)
	}

	retrieved := manager.GetLimit("test-key")
	if retrieved.MonthlyRequestLimit != 5000 {
		t.Errorf("Expected request limit 5000, got %d", retrieved.MonthlyRequestLimit)
	}
}

// TestSetLimitInvalid tests error handling for invalid limits
func TestSetLimitInvalid(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	storage, err := NewSQLiteStorage(dbPath)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer storage.Close()

	manager := NewManager(storage, 1000000, 107374182400, 100)

	tests := []struct {
		name  string
		limit *QuotaLimit
	}{
		{
			name:  "nil limit",
			limit: nil,
		},
		{
			name: "empty key ID",
			limit: &QuotaLimit{
				KeyID:                 "",
				MonthlyRequestLimit:   1000,
				MonthlyBytesLimit:     10240,
				ConcurrentConnections: 10,
			},
		},
		{
			name: "negative request limit",
			limit: &QuotaLimit{
				KeyID:                 "test-key",
				MonthlyRequestLimit:   -1,
				MonthlyBytesLimit:     10240,
				ConcurrentConnections: 10,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := manager.SetLimit(tt.limit)
			if err == nil {
				t.Error("Expected error for invalid limit, got nil")
			}
		})
	}
}

// TestGetLimitDefault tests getting default limit for unconfigured key
func TestGetLimitDefault(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	storage, err := NewSQLiteStorage(dbPath)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer storage.Close()

	manager := NewManager(storage, 100000, 10485760, 50)

	limit := manager.GetLimit("unconfigured-key")

	if limit.KeyID != "unconfigured-key" {
		t.Errorf("Expected KeyID 'unconfigured-key', got '%s'", limit.KeyID)
	}

	if limit.MonthlyRequestLimit != 100000 {
		t.Errorf("Expected default request limit 100000, got %d", limit.MonthlyRequestLimit)
	}
}

// TestRemoveLimit tests removing a limit
func TestRemoveLimit(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	storage, err := NewSQLiteStorage(dbPath)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer storage.Close()

	manager := NewManager(storage, 1000000, 107374182400, 100)

	// Set a limit
	limit := &QuotaLimit{
		KeyID:                 "test-key",
		MonthlyRequestLimit:   5000,
		MonthlyBytesLimit:     10485760,
		ConcurrentConnections: 50,
	}
	manager.SetLimit(limit)

	// Remove the limit
	err = manager.RemoveLimit("test-key")
	if err != nil {
		t.Fatalf("Failed to remove limit: %v", err)
	}

	// Should revert to default
	retrieved := manager.GetLimit("test-key")
	if retrieved.MonthlyRequestLimit == 5000 {
		t.Error("Limit should revert to default after removal")
	}
}

// TestListLimits tests listing all limits
func TestListLimits(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	storage, err := NewSQLiteStorage(dbPath)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer storage.Close()

	manager := NewManager(storage, 1000000, 107374182400, 100)

	// Initially should be empty
	limits := manager.ListLimits()
	if len(limits) != 0 {
		t.Errorf("Expected 0 limits initially, got %d", len(limits))
	}

	// Add some limits
	for i := 1; i <= 3; i++ {
		limit := &QuotaLimit{
			KeyID:                 string(rune('a' + i - 1)),
			MonthlyRequestLimit:   int64(i * 1000),
			MonthlyBytesLimit:     int64(i * 10240),
			ConcurrentConnections: i * 10,
		}
		manager.SetLimit(limit)
	}

	// List limits
	limits = manager.ListLimits()
	if len(limits) != 3 {
		t.Errorf("Expected 3 limits, got %d", len(limits))
	}
}

// TestCheckQuota tests quota checking
func TestCheckQuota(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	storage, err := NewSQLiteStorage(dbPath)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer storage.Close()

	manager := NewManager(storage, 1000, 10240, 5)

	// First check should pass
	err = manager.CheckQuota("test-key", 1024)
	if err != nil {
		t.Fatalf("First check should pass: %v", err)
	}

	// Add usage close to limit
	storage.UpdateUsage("test-key", 999, 9000)

	// Should still pass
	err = manager.CheckQuota("test-key", 1024)
	if err != nil {
		t.Fatalf("Should still pass: %v", err)
	}

	// Add one more to exceed request quota
	storage.UpdateUsage("test-key", 1, 0)

	// Should fail
	err = manager.CheckQuota("test-key", 1024)
	if err == nil {
		t.Fatal("Expected error for exceeded quota, got nil")
	}
}

// TestCheckQuotaBytes tests bytes quota checking
func TestCheckQuotaBytes(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	storage, err := NewSQLiteStorage(dbPath)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer storage.Close()

	manager := NewManager(storage, 1000000, 10240, 5)

	// Add usage close to bytes limit
	storage.UpdateUsage("test-key", 100, 9000)

	// Check with bytes that would exceed limit
	err = manager.CheckQuota("test-key", 2000)
	if err == nil {
		t.Fatal("Expected error for exceeded bytes quota, got nil")
	}

	if !strings.Contains(err.Error(), "bytes") {
		t.Errorf("Expected bytes quota error, got: %v", err)
	}
}

// TestRecordRequest tests recording requests
func TestRecordRequest(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	storage, err := NewSQLiteStorage(dbPath)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer storage.Close()

	manager := NewManager(storage, 1000000, 107374182400, 100)

	// Record a request
	err = manager.RecordRequest("test-key", 1024)
	if err != nil {
		t.Fatalf("Failed to record request: %v", err)
	}

	// Verify usage
	usage, err := storage.GetUsage("test-key")
	if err != nil {
		t.Fatalf("Failed to get usage: %v", err)
	}

	if usage.RequestCount != 1 {
		t.Errorf("Expected RequestCount 1, got %d", usage.RequestCount)
	}

	if usage.BytesTransferred != 1024 {
		t.Errorf("Expected BytesTransferred 1024, got %d", usage.BytesTransferred)
	}
}

// TestAcquireReleaseConnection tests connection tracking
func TestAcquireReleaseConnection(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	storage, err := NewSQLiteStorage(dbPath)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer storage.Close()

	manager := NewManager(storage, 1000000, 107374182400, 3)

	// Acquire connections
	for i := 0; i < 3; i++ {
		err = manager.AcquireConnection("test-key")
		if err != nil {
			t.Fatalf("Failed to acquire connection %d: %v", i+1, err)
		}
	}

	// 4th connection should fail
	err = manager.AcquireConnection("test-key")
	if err == nil {
		t.Fatal("Expected error for connection limit, got nil")
	}

	// Release a connection
	manager.ReleaseConnection("test-key")

	// Should be able to acquire again
	err = manager.AcquireConnection("test-key")
	if err != nil {
		t.Fatalf("Should be able to acquire after release: %v", err)
	}
}

// TestGetStatus tests getting quota status
func TestGetStatus(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	storage, err := NewSQLiteStorage(dbPath)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer storage.Close()

	manager := NewManager(storage, 1000, 10240, 5)

	// Add some usage
	storage.UpdateUsage("test-key", 250, 2560)

	// Get status
	status, err := manager.GetStatus("test-key")
	if err != nil {
		t.Fatalf("Failed to get status: %v", err)
	}

	if status.KeyID != "test-key" {
		t.Errorf("Expected KeyID 'test-key', got '%s'", status.KeyID)
	}

	if status.RequestCount != 250 {
		t.Errorf("Expected RequestCount 250, got %d", status.RequestCount)
	}

	if status.RequestRemaining != 750 {
		t.Errorf("Expected RequestRemaining 750, got %d", status.RequestRemaining)
	}

	if status.QuotaExceeded {
		t.Error("Quota should not be exceeded")
	}
}

// TestGetStatusExceeded tests quota exceeded status
func TestGetStatusExceeded(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	storage, err := NewSQLiteStorage(dbPath)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer storage.Close()

	manager := NewManager(storage, 1000, 10240, 5)

	// Add usage exceeding limit
	storage.UpdateUsage("test-key", 1001, 2560)

	// Get status
	status, err := manager.GetStatus("test-key")
	if err != nil {
		t.Fatalf("Failed to get status: %v", err)
	}

	if !status.QuotaExceeded {
		t.Error("Quota should be exceeded")
	}

	if status.QuotaExceededReason == "" {
		t.Error("QuotaExceededReason should be set")
	}
}

// TestResetQuota tests resetting quota
func TestResetQuota(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	storage, err := NewSQLiteStorage(dbPath)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer storage.Close()

	manager := NewManager(storage, 1000000, 107374182400, 100)

	// Add some usage
	storage.UpdateUsage("test-key", 500, 5120)

	// Reset quota
	err = manager.ResetQuota("test-key")
	if err != nil {
		t.Fatalf("Failed to reset quota: %v", err)
	}

	// Verify usage is reset
	usage, err := storage.GetUsage("test-key")
	if err != nil {
		t.Fatalf("Failed to get usage: %v", err)
	}

	if usage.RequestCount != 0 {
		t.Errorf("Expected RequestCount 0 after reset, got %d", usage.RequestCount)
	}
}
