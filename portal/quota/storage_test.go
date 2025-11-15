package quota

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestNewSQLiteStorage tests creating a new SQLite storage
func TestNewSQLiteStorage(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	storage, err := NewSQLiteStorage(dbPath)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer storage.Close()

	// Verify database file was created
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Error("Database file was not created")
	}
}

// TestNewSQLiteStorageEmptyPath tests error handling for empty path
func TestNewSQLiteStorageEmptyPath(t *testing.T) {
	_, err := NewSQLiteStorage("")
	if err == nil {
		t.Fatal("Expected error for empty path, got nil")
	}
}

// TestGetUsageNewKey tests retrieving usage for a new key
func TestGetUsageNewKey(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	storage, err := NewSQLiteStorage(dbPath)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer storage.Close()

	usage, err := storage.GetUsage("test-key")
	if err != nil {
		t.Fatalf("Failed to get usage: %v", err)
	}

	if usage.KeyID != "test-key" {
		t.Errorf("Expected KeyID 'test-key', got '%s'", usage.KeyID)
	}

	if usage.RequestCount != 0 {
		t.Errorf("Expected RequestCount 0, got %d", usage.RequestCount)
	}

	if usage.BytesTransferred != 0 {
		t.Errorf("Expected BytesTransferred 0, got %d", usage.BytesTransferred)
	}
}

// TestGetUsageInvalidKey tests error handling for invalid key
func TestGetUsageInvalidKey(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	storage, err := NewSQLiteStorage(dbPath)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer storage.Close()

	_, err = storage.GetUsage("")
	if err != ErrStorageInvalidKey {
		t.Errorf("Expected ErrStorageInvalidKey, got %v", err)
	}
}

// TestUpdateUsage tests updating usage
func TestUpdateUsage(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	storage, err := NewSQLiteStorage(dbPath)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer storage.Close()

	// Update usage
	err = storage.UpdateUsage("test-key", 10, 1024)
	if err != nil {
		t.Fatalf("Failed to update usage: %v", err)
	}

	// Verify usage
	usage, err := storage.GetUsage("test-key")
	if err != nil {
		t.Fatalf("Failed to get usage: %v", err)
	}

	if usage.RequestCount != 10 {
		t.Errorf("Expected RequestCount 10, got %d", usage.RequestCount)
	}

	if usage.BytesTransferred != 1024 {
		t.Errorf("Expected BytesTransferred 1024, got %d", usage.BytesTransferred)
	}
}

// TestUpdateUsageMultipleTimes tests incremental updates
func TestUpdateUsageMultipleTimes(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	storage, err := NewSQLiteStorage(dbPath)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer storage.Close()

	// First update
	err = storage.UpdateUsage("test-key", 5, 512)
	if err != nil {
		t.Fatalf("Failed to update usage (1): %v", err)
	}

	// Second update
	err = storage.UpdateUsage("test-key", 3, 256)
	if err != nil {
		t.Fatalf("Failed to update usage (2): %v", err)
	}

	// Verify cumulative usage
	usage, err := storage.GetUsage("test-key")
	if err != nil {
		t.Fatalf("Failed to get usage: %v", err)
	}

	if usage.RequestCount != 8 {
		t.Errorf("Expected RequestCount 8, got %d", usage.RequestCount)
	}

	if usage.BytesTransferred != 768 {
		t.Errorf("Expected BytesTransferred 768, got %d", usage.BytesTransferred)
	}
}

// TestUpdateUsageInvalidKey tests error handling for invalid key
func TestUpdateUsageInvalidKey(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	storage, err := NewSQLiteStorage(dbPath)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer storage.Close()

	err = storage.UpdateUsage("", 10, 1024)
	if err != ErrStorageInvalidKey {
		t.Errorf("Expected ErrStorageInvalidKey, got %v", err)
	}
}

// TestResetUsage tests resetting usage
func TestResetUsage(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	storage, err := NewSQLiteStorage(dbPath)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer storage.Close()

	// Add some usage
	err = storage.UpdateUsage("test-key", 100, 10240)
	if err != nil {
		t.Fatalf("Failed to update usage: %v", err)
	}

	// Reset usage
	err = storage.ResetUsage("test-key")
	if err != nil {
		t.Fatalf("Failed to reset usage: %v", err)
	}

	// Verify usage is reset
	usage, err := storage.GetUsage("test-key")
	if err != nil {
		t.Fatalf("Failed to get usage: %v", err)
	}

	if usage.RequestCount != 0 {
		t.Errorf("Expected RequestCount 0 after reset, got %d", usage.RequestCount)
	}

	if usage.BytesTransferred != 0 {
		t.Errorf("Expected BytesTransferred 0 after reset, got %d", usage.BytesTransferred)
	}
}

// TestResetUsageNonExistent tests resetting non-existent key
func TestResetUsageNonExistent(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	storage, err := NewSQLiteStorage(dbPath)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer storage.Close()

	err = storage.ResetUsage("nonexistent-key")
	if err == nil {
		t.Fatal("Expected error for non-existent key, got nil")
	}
}

// TestResetUsageInvalidKey tests error handling for invalid key
func TestResetUsageInvalidKey(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	storage, err := NewSQLiteStorage(dbPath)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer storage.Close()

	err = storage.ResetUsage("")
	if err != ErrStorageInvalidKey {
		t.Errorf("Expected ErrStorageInvalidKey, got %v", err)
	}
}

// TestListAllUsage tests listing all usage
func TestListAllUsage(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	storage, err := NewSQLiteStorage(dbPath)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer storage.Close()

	// Initially should be empty
	usages, err := storage.ListAllUsage()
	if err != nil {
		t.Fatalf("Failed to list usage: %v", err)
	}

	if len(usages) != 0 {
		t.Errorf("Expected 0 usages initially, got %d", len(usages))
	}

	// Add some usage records
	keys := []string{"key1", "key2", "key3"}
	for i, key := range keys {
		err = storage.UpdateUsage(key, int64(i+1)*10, int64(i+1)*1024)
		if err != nil {
			t.Fatalf("Failed to update usage for %s: %v", key, err)
		}
	}

	// List all usage
	usages, err = storage.ListAllUsage()
	if err != nil {
		t.Fatalf("Failed to list usage: %v", err)
	}

	if len(usages) != 3 {
		t.Errorf("Expected 3 usages, got %d", len(usages))
	}

	// Verify all keys are present
	foundKeys := make(map[string]bool)
	for _, usage := range usages {
		foundKeys[usage.KeyID] = true
	}

	for _, key := range keys {
		if !foundKeys[key] {
			t.Errorf("Expected to find key %s in list, but it was missing", key)
		}
	}
}

// TestGetMonthStart tests the getMonthStart function
func TestGetMonthStart(t *testing.T) {
	tests := []struct {
		name     string
		input    time.Time
		expected time.Time
	}{
		{
			name:     "mid-month",
			input:    time.Date(2024, 6, 15, 14, 30, 45, 0, time.UTC),
			expected: time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name:     "first day of month",
			input:    time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			expected: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name:     "last day of month",
			input:    time.Date(2024, 12, 31, 23, 59, 59, 0, time.UTC),
			expected: time.Date(2024, 12, 1, 0, 0, 0, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getMonthStart(tt.input)
			if !result.Equal(tt.expected) {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}

// TestConcurrentAccess tests concurrent access to storage
func TestConcurrentAccess(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	storage, err := NewSQLiteStorage(dbPath)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer storage.Close()

	// Run concurrent updates
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				storage.UpdateUsage("concurrent-key", 1, 100)
			}
			done <- true
		}()
	}

	// Wait for all goroutines to complete
	for i := 0; i < 10; i++ {
		<-done
	}

	// Verify final count
	usage, err := storage.GetUsage("concurrent-key")
	if err != nil {
		t.Fatalf("Failed to get usage: %v", err)
	}

	expected := int64(1000)
	if usage.RequestCount != expected {
		t.Errorf("Expected RequestCount %d, got %d", expected, usage.RequestCount)
	}
}
