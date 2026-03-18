package testutil

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/auth"
	"github.com/qlustered/qctl/internal/config"
)

// TestEnv sets up a test environment with temp config and credentials
type TestEnv struct {
	t            *testing.T
	TempDir      string
	ConfigPath   string
	OriginalHome string
	// originalPlaintextTokens stores the incoming QCTL_ALLOW_PLAINTEXT_TOKENS value
	// so we can restore it after tests that force plaintext token storage.
	originalPlaintextTokens string
}

// NewTestEnv creates a new test environment
// It creates a temporary directory and sets HOME to it
// This isolates the test from the user's actual config
func NewTestEnv(t *testing.T) *TestEnv {
	tmpDir, err := os.MkdirTemp("", "qctl-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	originalHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	originalPlaintextTokens := os.Getenv("QCTL_ALLOW_PLAINTEXT_TOKENS")
	// Force plaintext credentials during tests to avoid hitting the system keyring.
	os.Setenv("QCTL_ALLOW_PLAINTEXT_TOKENS", "1")

	return &TestEnv{
		TempDir:                 tmpDir,
		ConfigPath:              filepath.Join(tmpDir, config.ConfigDirName, config.ConfigFileName),
		OriginalHome:            originalHome,
		originalPlaintextTokens: originalPlaintextTokens,
		t:                       t,
	}
}

// SetupConfig creates a config file with the given server
func (e *TestEnv) SetupConfig(server, _ string) *config.Config {
	// The second parameter was "user" but is no longer used with OAuth
	cfg := &config.Config{
		APIVersion:     config.APIVersion,
		CurrentContext: "default",
		Contexts: map[string]*config.Context{
			"default": {
				Server: server,
			},
		},
	}

	if err := cfg.Save(); err != nil {
		e.t.Fatalf("Failed to save config: %v", err)
	}

	return cfg
}

// SetupConfigWithOrg creates a config file with the given server and organization
func (e *TestEnv) SetupConfigWithOrg(server, _, orgID string) *config.Config {
	// The second parameter was "user" but is no longer used with OAuth
	cfg := &config.Config{
		APIVersion:     config.APIVersion,
		CurrentContext: "default",
		Contexts: map[string]*config.Context{
			"default": {
				Server:       server,
				Organization: orgID,
			},
		},
	}

	if err := cfg.Save(); err != nil {
		e.t.Fatalf("Failed to save config: %v", err)
	}

	return cfg
}

// SetupConfigWithContext creates a config file with a custom context name
func (e *TestEnv) SetupConfigWithContext(contextName, server, _ string) *config.Config {
	// The third parameter was "user" but is no longer used with OAuth
	cfg := &config.Config{
		APIVersion:     config.APIVersion,
		CurrentContext: contextName,
		Contexts: map[string]*config.Context{
			contextName: {
				Server: server,
			},
		},
	}

	if err := cfg.Save(); err != nil {
		e.t.Fatalf("Failed to save config: %v", err)
	}

	return cfg
}

// SetupMultipleContexts creates a config file with multiple contexts
func (e *TestEnv) SetupMultipleContexts(contexts map[string]*config.Context, currentContext string) *config.Config {
	cfg := &config.Config{
		APIVersion:     config.APIVersion,
		CurrentContext: currentContext,
		Contexts:       contexts,
	}

	if err := cfg.Save(); err != nil {
		e.t.Fatalf("Failed to save config: %v", err)
	}

	return cfg
}

// SetupCredential stores a credential for the given endpoint and organization
func (e *TestEnv) SetupCredential(endpointKey, orgID, token string) {
	// Enable plaintext token storage for tests (required for CI where keyring is unavailable)
	os.Setenv("QCTL_ALLOW_PLAINTEXT_TOKENS", "1")

	credStore := auth.NewCredentialStore(true) // allow plaintext for testing
	cred := &auth.Credential{
		CreatedAt:      time.Now(),
		AccessToken:    token,
		ExpiresAt:      time.Now().Add(24 * time.Hour), // Valid for 24 hours
		OrganizationID: orgID,
	}
	err := credStore.Store(endpointKey, cred)
	if err != nil {
		e.t.Fatalf("Failed to store credential: %v", err)
	}
}

// GetCredential retrieves a credential for the given endpoint and organization
func (e *TestEnv) GetCredential(endpointKey, orgID string) *auth.Credential {
	credStore := auth.NewCredentialStore(true) // allow plaintext for testing
	cred, err := credStore.Retrieve(endpointKey, orgID)
	if err != nil {
		e.t.Fatalf("Failed to retrieve credential: %v", err)
	}
	return cred
}

// DeleteCredential removes a credential for the given endpoint and organization
func (e *TestEnv) DeleteCredential(endpointKey, orgID string) {
	credStore := auth.NewCredentialStore(true) // allow plaintext for testing
	err := credStore.Delete(endpointKey, orgID)
	if err != nil {
		e.t.Fatalf("Failed to delete credential: %v", err)
	}
}

// SetEnv sets an environment variable and returns a cleanup function
func (e *TestEnv) SetEnv(key, value string) func() {
	original := os.Getenv(key)
	os.Setenv(key, value)
	return func() {
		if original == "" {
			os.Unsetenv(key)
		} else {
			os.Setenv(key, original)
		}
	}
}

// Cleanup cleans up the test environment
// Call this with defer in your test
func (e *TestEnv) Cleanup() {
	os.Setenv("HOME", e.OriginalHome)
	if e.originalPlaintextTokens == "" {
		os.Unsetenv("QCTL_ALLOW_PLAINTEXT_TOKENS")
	} else {
		os.Setenv("QCTL_ALLOW_PLAINTEXT_TOKENS", e.originalPlaintextTokens)
	}
	os.RemoveAll(e.TempDir)
}

// ConfigPath returns the path to the config file
func (e *TestEnv) GetConfigPath() string {
	return e.ConfigPath
}

// LoadConfig loads the config from the test environment
func (e *TestEnv) LoadConfig() *config.Config {
	cfg, err := config.Load()
	if err != nil {
		e.t.Fatalf("Failed to load config: %v", err)
	}
	return cfg
}

// CreateFile creates a file with the given content in the test environment
func (e *TestEnv) CreateFile(relativePath, content string) string {
	fullPath := filepath.Join(e.TempDir, relativePath)
	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		e.t.Fatalf("Failed to create directory %s: %v", dir, err)
	}
	if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
		e.t.Fatalf("Failed to create file %s: %v", fullPath, err)
	}
	return fullPath
}

// ReadFile reads a file from the test environment
func (e *TestEnv) ReadFile(relativePath string) string {
	fullPath := filepath.Join(e.TempDir, relativePath)
	content, err := os.ReadFile(fullPath)
	if err != nil {
		e.t.Fatalf("Failed to read file %s: %v", fullPath, err)
	}
	return string(content)
}

// FileExists checks if a file exists in the test environment
func (e *TestEnv) FileExists(relativePath string) bool {
	fullPath := filepath.Join(e.TempDir, relativePath)
	_, err := os.Stat(fullPath)
	return err == nil
}
