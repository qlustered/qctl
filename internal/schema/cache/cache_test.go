package cache

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"1.2.3", "1.2.3"},
		{"v1.2.3-beta", "v1.2.3-beta"},
		{"v1.2.3+build", "v1.2.3_build"},
		{"version/with/slashes", "version_with_slashes"},
		{"with spaces", "with_spaces"},
		{"with:colons", "with_colons"},
		{"normal_version_123", "normal_version_123"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := sanitizeFilename(tt.input)
			if got != tt.expected {
				t.Errorf("sanitizeFilename(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestCacheReadWrite(t *testing.T) {
	// Use a temp directory for testing
	tempDir := t.TempDir()

	// Override the cache directory for testing
	origCacheDir := cacheDir
	cacheDir = tempDir
	defer func() { cacheDir = origCacheDir }()

	version := "test-v1.0.0"
	testData := []byte(`{"openapi": "3.0.0", "info": {"title": "Test API", "version": "1.0.0"}}`)

	// Write spec
	if err := WriteSpec(version, testData); err != nil {
		t.Fatalf("WriteSpec() error = %v", err)
	}

	// Verify file exists
	specPath, _ := SpecPath(version)
	if _, err := os.Stat(specPath); os.IsNotExist(err) {
		t.Errorf("Spec file was not created at %s", specPath)
	}

	// Read spec
	spec, err := ReadSpec(version)
	if err != nil {
		t.Fatalf("ReadSpec() error = %v", err)
	}
	if spec == nil {
		t.Error("ReadSpec() returned nil for existing spec")
	}
}

func TestCacheReadNonExistent(t *testing.T) {
	// Use a temp directory for testing
	tempDir := t.TempDir()

	// Override the cache directory for testing
	origCacheDir := cacheDir
	cacheDir = tempDir
	defer func() { cacheDir = origCacheDir }()

	// Try to read a non-existent spec
	spec, err := ReadSpec("non-existent-version")
	if err != nil {
		t.Errorf("ReadSpec() for non-existent should return nil error, got %v", err)
	}
	if spec != nil {
		t.Error("ReadSpec() should return nil for non-existent spec")
	}
}

func TestClearCache(t *testing.T) {
	// Use a temp directory for testing
	tempDir := t.TempDir()

	// Override the cache directory for testing
	origCacheDir := cacheDir
	cacheDir = tempDir
	defer func() { cacheDir = origCacheDir }()

	// Create some test files
	testFiles := []string{"openapi-v1.json", "openapi-v2.json"}
	for _, f := range testFiles {
		path := filepath.Join(tempDir, f)
		if err := os.WriteFile(path, []byte("test"), 0o644); err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}
	}

	// Clear cache
	if err := ClearCache(); err != nil {
		t.Fatalf("ClearCache() error = %v", err)
	}

	// Verify files are gone
	entries, err := os.ReadDir(tempDir)
	if err != nil {
		t.Fatalf("Failed to read temp dir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("ClearCache() should remove all files, got %d remaining", len(entries))
	}
}

func TestSpecPath(t *testing.T) {
	// Use a temp directory for testing
	tempDir := t.TempDir()

	// Override the cache directory for testing
	origCacheDir := cacheDir
	cacheDir = tempDir
	defer func() { cacheDir = origCacheDir }()

	path, err := SpecPath("v1.2.3")
	if err != nil {
		t.Fatalf("SpecPath() error = %v", err)
	}

	expected := filepath.Join(tempDir, "openapi-v1.2.3.json")
	if path != expected {
		t.Errorf("SpecPath() = %q, want %q", path, expected)
	}
}

func TestEnsureSpecCached_AlreadyCached(t *testing.T) {
	// Use a temp directory for testing
	tempDir := t.TempDir()

	// Override the cache directory for testing
	origCacheDir := cacheDir
	cacheDir = tempDir
	defer func() { cacheDir = origCacheDir }()

	// Clear in-memory cache
	cachedSpecLock.Lock()
	cachedSpec = nil
	cachedVersion = ""
	cachedSpecLock.Unlock()

	// Pre-populate the cache with a spec
	version := "pre-cached-v1.0.0"
	testData := []byte(`{"openapi": "3.0.0", "info": {"title": "Test API", "version": "1.0.0"}}`)
	if err := WriteSpec(version, testData); err != nil {
		t.Fatalf("WriteSpec() error = %v", err)
	}

	// Verify the file exists
	specPath, _ := SpecPath(version)
	if _, err := os.Stat(specPath); os.IsNotExist(err) {
		t.Fatalf("Pre-cached spec file was not created at %s", specPath)
	}

	// Verify we can read it back
	spec, err := ReadSpec(version)
	if err != nil {
		t.Fatalf("ReadSpec() error = %v", err)
	}
	if spec == nil {
		t.Error("ReadSpec() returned nil for pre-cached spec")
	}
}

func TestGetCachedVersion(t *testing.T) {
	// Clear in-memory cache first
	cachedSpecLock.Lock()
	origSpec := cachedSpec
	origVersion := cachedVersion
	cachedSpec = nil
	cachedVersion = ""
	cachedSpecLock.Unlock()
	defer func() {
		cachedSpecLock.Lock()
		cachedSpec = origSpec
		cachedVersion = origVersion
		cachedSpecLock.Unlock()
	}()

	// Test empty cache
	if v := GetCachedVersion(); v != "" {
		t.Errorf("GetCachedVersion() = %q, want empty string", v)
	}

	// Set a version
	cachedSpecLock.Lock()
	cachedVersion = "test-v1.0.0"
	cachedSpecLock.Unlock()

	if v := GetCachedVersion(); v != "test-v1.0.0" {
		t.Errorf("GetCachedVersion() = %q, want 'test-v1.0.0'", v)
	}
}
