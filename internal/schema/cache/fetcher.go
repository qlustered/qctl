package cache

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/qlustered/qctl/internal/api"
)

var (
	// cachedSpec holds the in-memory cached spec to avoid re-reading from disk
	cachedSpec     *openapi3.T
	cachedVersion  string
	cachedSpecLock sync.RWMutex
)

// Fetcher handles fetching and caching OpenAPI specs from the API server.
type Fetcher struct {
	baseURL string
	timeout time.Duration
}

// NewFetcher creates a new Fetcher for the given API base URL.
func NewFetcher(baseURL string) *Fetcher {
	return &Fetcher{
		baseURL: baseURL,
		timeout: 30 * time.Second,
	}
}

// GetOrFetch returns the cached spec if available, or fetches and caches it.
// This is the main entry point for getting the OpenAPI spec.
func (f *Fetcher) GetOrFetch(ctx context.Context) (*openapi3.T, string, error) {
	// First, get the current API version
	version, err := f.fetchVersion(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("failed to fetch API version: %w", err)
	}

	// Check in-memory cache first
	cachedSpecLock.RLock()
	if cachedSpec != nil && cachedVersion == version {
		spec := cachedSpec
		cachedSpecLock.RUnlock()
		return spec, version, nil
	}
	cachedSpecLock.RUnlock()

	// Check disk cache
	spec, err := ReadSpec(version)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read cached spec: %w", err)
	}

	if spec != nil {
		// Update in-memory cache
		cachedSpecLock.Lock()
		cachedSpec = spec
		cachedVersion = version
		cachedSpecLock.Unlock()
		return spec, version, nil
	}

	// Cache miss - fetch from API
	spec, err = f.fetchSpec(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("failed to fetch OpenAPI spec: %w", err)
	}

	// Write to disk cache
	if err := WriteSpecJSON(version, spec); err != nil {
		// Log warning but don't fail - we have the spec in memory
		// fmt.Fprintf(os.Stderr, "warning: failed to cache spec: %v\n", err)
	}

	// Update in-memory cache
	cachedSpecLock.Lock()
	cachedSpec = spec
	cachedVersion = version
	cachedSpecLock.Unlock()

	return spec, version, nil
}

// fetchVersion fetches the current API version from /api/version.
func (f *Fetcher) fetchVersion(ctx context.Context) (string, error) {
	client, err := api.NewClientWithResponses(f.baseURL)
	if err != nil {
		return "", fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := client.GetVersionWithResponse(ctx)
	if err != nil {
		return "", fmt.Errorf("version request failed: %w", err)
	}

	if resp.StatusCode() != http.StatusOK {
		return "", fmt.Errorf("version request returned status %d", resp.StatusCode())
	}

	if resp.JSON200 == nil {
		return "", fmt.Errorf("version response is empty")
	}

	return resp.JSON200.Version, nil
}

// fetchSpec fetches the OpenAPI spec from /api/docs/openapi.json.
func (f *Fetcher) fetchSpec(ctx context.Context) (*openapi3.T, error) {
	specURL := f.baseURL + "/api/docs/openapi.json"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, specURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpClient := &http.Client{Timeout: f.timeout}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("request returned status %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	loader := openapi3.NewLoader()
	spec, err := loader.LoadFromData(data)
	if err != nil {
		return nil, fmt.Errorf("failed to parse OpenAPI spec: %w", err)
	}

	return spec, nil
}

// InvalidateCache clears both in-memory and disk cache.
func InvalidateCache() error {
	cachedSpecLock.Lock()
	cachedSpec = nil
	cachedVersion = ""
	cachedSpecLock.Unlock()

	return ClearCache()
}

// GetCachedVersion returns the currently cached version, if any.
func GetCachedVersion() string {
	cachedSpecLock.RLock()
	defer cachedSpecLock.RUnlock()
	return cachedVersion
}

// EnsureSpecCached fetches the API version and ensures the OpenAPI spec is cached.
// This is meant to be called during context switching to pre-cache the spec.
// Returns the API version string on success.
func EnsureSpecCached(ctx context.Context, serverURL string) (string, error) {
	fetcher := NewFetcher(serverURL)

	// Fetch the API version first
	version, err := fetcher.fetchVersion(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to fetch API version: %w", err)
	}

	// Check if we already have this version cached on disk
	spec, err := ReadSpec(version)
	if err != nil {
		return "", fmt.Errorf("failed to check spec cache: %w", err)
	}

	if spec != nil {
		// Already cached, update in-memory cache
		cachedSpecLock.Lock()
		cachedSpec = spec
		cachedVersion = version
		cachedSpecLock.Unlock()
		return version, nil
	}

	// Cache miss - fetch and cache the spec
	spec, err = fetcher.fetchSpec(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to fetch OpenAPI spec: %w", err)
	}

	// Write to disk cache
	if err := WriteSpecJSON(version, spec); err != nil {
		return "", fmt.Errorf("failed to cache OpenAPI spec: %w", err)
	}

	// Update in-memory cache
	cachedSpecLock.Lock()
	cachedSpec = spec
	cachedVersion = version
	cachedSpecLock.Unlock()

	return version, nil
}
