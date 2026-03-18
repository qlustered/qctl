package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAndSave(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir := t.TempDir()
	tmpConfigPath := filepath.Join(tmpDir, ConfigFileName)

	// Override the config path for testing
	originalGetConfigPath := GetConfigPath
	GetConfigPath = func() (string, error) {
		return tmpConfigPath, nil
	}
	defer func() { GetConfigPath = originalGetConfigPath }()

	// Test loading non-existent config (should return default)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.APIVersion != APIVersion {
		t.Errorf("Expected APIVersion %q, got %q", APIVersion, cfg.APIVersion)
	}

	if cfg.CurrentContext != "" {
		t.Errorf("Expected empty CurrentContext, got %q", cfg.CurrentContext)
	}

	if len(cfg.Contexts) != 0 {
		t.Errorf("Expected no contexts, got %d", len(cfg.Contexts))
	}

	// Add a context and save
	ctx := &Context{
		Server: "https://api.example.com/api",
		Output: "json",
	}
	cfg.SetContext("test", ctx)
	cfg.CurrentContext = "test"

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Load again and verify
	cfg2, err := Load()
	if err != nil {
		t.Fatalf("Load after save failed: %v", err)
	}

	if cfg2.CurrentContext != "test" {
		t.Errorf("Expected CurrentContext 'test', got %q", cfg2.CurrentContext)
	}

	testCtx, ok := cfg2.Contexts["test"]
	if !ok {
		t.Fatal("Context 'test' not found after load")
	}

	if testCtx.Server != ctx.Server {
		t.Errorf("Expected Server %q, got %q", ctx.Server, testCtx.Server)
	}

	if testCtx.Output != ctx.Output {
		t.Errorf("Expected Output %q, got %q", ctx.Output, testCtx.Output)
	}
}

func TestGetCurrentContext(t *testing.T) {
	cfg := &Config{
		APIVersion:     APIVersion,
		CurrentContext: "test",
		Contexts: map[string]*Context{
			"test": {
				Server: "https://api.example.com/api",
			},
		},
	}

	ctx, err := cfg.GetCurrentContext()
	if err != nil {
		t.Fatalf("GetCurrentContext failed: %v", err)
	}

	if ctx.Server != "https://api.example.com/api" {
		t.Errorf("Expected Server 'https://api.example.com/api', got %q", ctx.Server)
	}

	// Test with no current context
	cfg.CurrentContext = ""
	_, err = cfg.GetCurrentContext()
	if err == nil {
		t.Error("Expected error when CurrentContext is empty")
	}

	// Test with non-existent context
	cfg.CurrentContext = "nonexistent"
	_, err = cfg.GetCurrentContext()
	if err == nil {
		t.Error("Expected error when context does not exist")
	}
}

func TestUseContext(t *testing.T) {
	cfg := &Config{
		APIVersion:     APIVersion,
		CurrentContext: "test1",
		Contexts: map[string]*Context{
			"test1": {Server: "https://api1.example.com/api"},
			"test2": {Server: "https://api2.example.com/api"},
		},
	}

	// Switch to test2
	if err := cfg.UseContext("test2"); err != nil {
		t.Fatalf("UseContext failed: %v", err)
	}

	if cfg.CurrentContext != "test2" {
		t.Errorf("Expected CurrentContext 'test2', got %q", cfg.CurrentContext)
	}

	// Try to switch to non-existent context
	if err := cfg.UseContext("nonexistent"); err == nil {
		t.Error("Expected error when switching to non-existent context")
	}
}

func TestDeleteContext(t *testing.T) {
	cfg := &Config{
		APIVersion:     APIVersion,
		CurrentContext: "test1",
		Contexts: map[string]*Context{
			"test1": {Server: "https://api1.example.com/api"},
			"test2": {Server: "https://api2.example.com/api"},
		},
	}

	// Try to delete current context (should fail)
	if err := cfg.DeleteContext("test1"); err == nil {
		t.Error("Expected error when deleting current context")
	}

	// Delete non-current context
	if err := cfg.DeleteContext("test2"); err != nil {
		t.Fatalf("DeleteContext failed: %v", err)
	}

	if _, ok := cfg.Contexts["test2"]; ok {
		t.Error("Context 'test2' should have been deleted")
	}

	// Try to delete non-existent context
	if err := cfg.DeleteContext("nonexistent"); err == nil {
		t.Error("Expected error when deleting non-existent context")
	}
}

func TestResolveServer(t *testing.T) {
	cfg := &Config{
		APIVersion:     APIVersion,
		CurrentContext: "test",
		Contexts: map[string]*Context{
			"test": {
				Server: "https://api.example.com/api",
			},
		},
	}

	tests := []struct {
		name       string
		serverFlag string
		envVar     string
		expected   string
		shouldErr  bool
	}{
		{
			name:       "flag takes priority",
			serverFlag: "https://flag.example.com/api",
			envVar:     "https://env.example.com/api",
			expected:   "https://flag.example.com/api",
		},
		{
			name:       "env var when no flag",
			serverFlag: "",
			envVar:     "https://env.example.com/api",
			expected:   "https://env.example.com/api",
		},
		{
			name:       "context when no flag or env",
			serverFlag: "",
			envVar:     "",
			expected:   "https://api.example.com/api",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set/unset env var
			if tt.envVar != "" {
				os.Setenv("QCTL_SERVER", tt.envVar)
				defer os.Unsetenv("QCTL_SERVER")
			} else {
				os.Unsetenv("QCTL_SERVER")
			}

			result, err := ResolveServer(cfg, tt.serverFlag)
			if tt.shouldErr {
				if err == nil {
					t.Error("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if result != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, result)
			}
		})
	}

	// Test with no current context
	cfg.CurrentContext = ""
	_, err := ResolveServer(cfg, "")
	if err == nil {
		t.Error("Expected error when no server specified and no current context")
	}
}

func TestValidateServer(t *testing.T) {
	tests := []struct {
		name          string
		serverURL     string
		allowInsecure bool
		shouldErr     bool
		errContains   string
	}{
		{
			name:          "https is always valid",
			serverURL:     "https://api.example.com/api",
			allowInsecure: false,
			shouldErr:     false,
		},
		{
			name:          "localhost http is valid",
			serverURL:     "http://localhost:8000/api",
			allowInsecure: false,
			shouldErr:     false,
		},
		{
			name:          "127.0.0.1 http is valid",
			serverURL:     "http://127.0.0.1:8000/api",
			allowInsecure: false,
			shouldErr:     false,
		},
		{
			name:          "non-localhost http without flag is invalid",
			serverURL:     "http://api.example.com/api",
			allowInsecure: false,
			shouldErr:     true,
			errContains:   "require --allow-insecure-http",
		},
		{
			name:          "non-localhost http with flag is valid",
			serverURL:     "http://api.example.com/api",
			allowInsecure: true,
			shouldErr:     false,
		},
		{
			name:          "invalid scheme",
			serverURL:     "ftp://api.example.com/api",
			allowInsecure: false,
			shouldErr:     true,
			errContains:   "must use http or https",
		},
		{
			name:          "invalid url",
			serverURL:     "://invalid",
			allowInsecure: false,
			shouldErr:     true,
			errContains:   "invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateServer(tt.serverURL, tt.allowInsecure)
			if tt.shouldErr {
				if err == nil {
					t.Error("Expected error but got none")
					return
				}
				if tt.errContains != "" && !contains(err.Error(), tt.errContains) {
					t.Errorf("Expected error to contain %q, got %q", tt.errContains, err.Error())
				}
			} else if err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
		})
	}
}

func TestNormalizeEndpointKey(t *testing.T) {
	tests := []struct {
		name      string
		serverURL string
		expected  string
		shouldErr bool
	}{
		{
			name:      "https with default port",
			serverURL: "https://api.example.com/api",
			expected:  "https://api.example.com:443",
		},
		{
			name:      "http with default port",
			serverURL: "http://api.example.com/api",
			expected:  "http://api.example.com:80",
		},
		{
			name:      "https with custom port",
			serverURL: "https://api.example.com:8443/api",
			expected:  "https://api.example.com:8443",
		},
		{
			name:      "http with custom port",
			serverURL: "http://localhost:8000/api",
			expected:  "http://localhost:8000",
		},
		{
			name:      "uppercase scheme and host",
			serverURL: "HTTPS://API.EXAMPLE.COM/api",
			expected:  "https://api.example.com:443",
		},
		{
			name:      "mixed case",
			serverURL: "HtTpS://Api.Example.Com:8443/api",
			expected:  "https://api.example.com:8443",
		},
		{
			name:      "invalid url",
			serverURL: "://invalid",
			shouldErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := NormalizeEndpointKey(tt.serverURL)
			if tt.shouldErr {
				if err == nil {
					t.Error("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if result != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestIsInsecureHTTPAllowed(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		expected bool
	}{
		{
			name:     "not set",
			envValue: "",
			expected: false,
		},
		{
			name:     "set to 1",
			envValue: "1",
			expected: true,
		},
		{
			name:     "set to true",
			envValue: "true",
			expected: true,
		},
		{
			name:     "set to false",
			envValue: "false",
			expected: false,
		},
		{
			name:     "set to other value",
			envValue: "yes",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envValue != "" {
				os.Setenv("QCTL_INSECURE_HTTP", tt.envValue)
				defer os.Unsetenv("QCTL_INSECURE_HTTP")
			} else {
				os.Unsetenv("QCTL_INSECURE_HTTP")
			}

			result := IsInsecureHTTPAllowed()
			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
