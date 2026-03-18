// Package cache provides OpenAPI schema caching functionality.
// It fetches the OpenAPI spec from the API server and caches it locally
// based on the API version to avoid repeated network requests.
package cache

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/getkin/kin-openapi/openapi3"
)

var (
	// cacheDir is the directory where cached specs are stored
	cacheDir     string
	cacheDirOnce sync.Once
)

// CacheDir returns the cache directory path (~/.qctl/cache).
// Creates the directory if it doesn't exist.
func CacheDir() (string, error) {
	var err error
	cacheDirOnce.Do(func() {
		home, e := os.UserHomeDir()
		if e != nil {
			err = fmt.Errorf("failed to get home directory: %w", e)
			return
		}
		cacheDir = filepath.Join(home, ".qctl", "cache")
		if e := os.MkdirAll(cacheDir, 0o755); e != nil {
			err = fmt.Errorf("failed to create cache directory: %w", e)
			return
		}
	})
	return cacheDir, err
}

// SpecPath returns the path to the cached spec file for a given version.
func SpecPath(version string) (string, error) {
	dir, err := CacheDir()
	if err != nil {
		return "", err
	}
	// Sanitize version string to be safe for filenames
	safeVersion := sanitizeFilename(version)
	return filepath.Join(dir, fmt.Sprintf("openapi-%s.json", safeVersion)), nil
}

// ReadSpec reads the cached OpenAPI spec for a given version.
// Returns nil if the cache doesn't exist or is invalid.
func ReadSpec(version string) (*openapi3.T, error) {
	path, err := SpecPath(version)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // Cache miss, not an error
		}
		return nil, fmt.Errorf("failed to read cached spec: %w", err)
	}

	loader := openapi3.NewLoader()
	spec, err := loader.LoadFromData(data)
	if err != nil {
		// Invalid cache, remove it
		_ = os.Remove(path)
		return nil, nil
	}

	return spec, nil
}

// WriteSpec writes the OpenAPI spec to the cache for a given version.
func WriteSpec(version string, data []byte) error {
	path, err := SpecPath(version)
	if err != nil {
		return err
	}

	// Write atomically by writing to temp file first
	tempPath := path + ".tmp"
	if err := os.WriteFile(tempPath, data, 0o644); err != nil {
		return fmt.Errorf("failed to write cache file: %w", err)
	}

	if err := os.Rename(tempPath, path); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("failed to rename cache file: %w", err)
	}

	return nil
}

// WriteSpecJSON marshals and writes the OpenAPI spec to the cache.
func WriteSpecJSON(version string, spec *openapi3.T) error {
	data, err := json.Marshal(spec)
	if err != nil {
		return fmt.Errorf("failed to marshal spec: %w", err)
	}
	return WriteSpec(version, data)
}

// ClearCache removes all cached spec files.
func ClearCache() error {
	dir, err := CacheDir()
	if err != nil {
		return err
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read cache directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("failed to remove cache file %s: %w", entry.Name(), err)
		}
	}

	return nil
}

// sanitizeFilename makes a version string safe for use in filenames.
func sanitizeFilename(s string) string {
	result := make([]byte, 0, len(s))
	for i := range len(s) {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' {
			result = append(result, c)
		} else {
			result = append(result, '_')
		}
	}
	return string(result)
}
