package config

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/qlustered/qctl/internal/config"
)

func TestUseContextCommand(t *testing.T) {
	t.Setenv("QCTL_INSECURE_HTTP", "1")

	// Mock the spec caching function to always succeed
	originalEnsureSpecCached := UseContextEnsureSpecCachedFunc
	UseContextEnsureSpecCachedFunc = func(ctx context.Context, serverURL string) (string, error) {
		return "test-version", nil
	}
	defer func() { UseContextEnsureSpecCachedFunc = originalEnsureSpecCached }()

	tests := []struct {
		name               string
		setupContexts      map[string]*config.Context
		currentCtx         string
		contextToUse       string
		shouldErr          bool
		errContains        string
		expectedCurrentCtx string
	}{
		{
			name: "switch to existing context",
			setupContexts: map[string]*config.Context{
				"ctx1": {Server: "http://localhost:8000"},
				"ctx2": {Server: "http://localhost:9000"},
			},
			currentCtx:         "ctx1",
			contextToUse:       "ctx2",
			shouldErr:          false,
			expectedCurrentCtx: "ctx2",
		},
		{
			name: "switch to same context",
			setupContexts: map[string]*config.Context{
				"ctx1": {Server: "http://localhost:8000"},
			},
			currentCtx:         "ctx1",
			contextToUse:       "ctx1",
			shouldErr:          false,
			expectedCurrentCtx: "ctx1",
		},
		{
			name: "fail when context does not exist",
			setupContexts: map[string]*config.Context{
				"ctx1": {Server: "http://localhost:8000"},
			},
			currentCtx:   "ctx1",
			contextToUse: "nonexistent",
			shouldErr:    true,
			errContains:  "context \"nonexistent\" does not exist",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a temporary directory for testing
			tmpDir := t.TempDir()
			tmpConfigPath := filepath.Join(tmpDir, config.ConfigFileName)

			// Override the config path for testing
			originalGetConfigPath := config.GetConfigPath
			config.GetConfigPath = func() (string, error) {
				return tmpConfigPath, nil
			}
			defer func() { config.GetConfigPath = originalGetConfigPath }()

			// Setup config
			cfg := &config.Config{
				APIVersion:     config.APIVersion,
				CurrentContext: tt.currentCtx,
				Contexts:       tt.setupContexts,
			}
			if err := cfg.Save(); err != nil {
				t.Fatalf("Failed to save config: %v", err)
			}

			// Create and execute command
			cmd := newUseContextCommand()
			cmd.SetArgs([]string{tt.contextToUse})

			err := cmd.Execute()

			if tt.shouldErr {
				if err == nil {
					t.Error("Expected error but got none")
					return
				}
				if tt.errContains != "" && !contains(err.Error(), tt.errContains) {
					t.Errorf("Expected error to contain %q, got %q", tt.errContains, err.Error())
				}
				return
			}

			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			// Verify context was switched
			cfg2, err := config.Load()
			if err != nil {
				t.Fatalf("Failed to load config after use: %v", err)
			}

			if tt.expectedCurrentCtx != "" && cfg2.CurrentContext != tt.expectedCurrentCtx {
				t.Errorf("Expected current context to be %q, got %q", tt.expectedCurrentCtx, cfg2.CurrentContext)
			}
		})
	}
}

func TestUseContextCommandNoArgs(t *testing.T) {
	cmd := newUseContextCommand()
	cmd.SetArgs([]string{})

	err := cmd.Execute()
	if err == nil {
		t.Error("Expected error when no context name provided")
	}
}

func TestUseContextCommandTooManyArgs(t *testing.T) {
	cmd := newUseContextCommand()
	cmd.SetArgs([]string{"ctx1", "ctx2"})

	err := cmd.Execute()
	if err == nil {
		t.Error("Expected error when too many arguments provided")
	}
}

func TestUseContextEnsureSpecCachedFuncOverride(t *testing.T) {
	// Test that the function can be overridden for testing
	originalFunc := UseContextEnsureSpecCachedFunc
	defer func() { UseContextEnsureSpecCachedFunc = originalFunc }()

	// Override with a function that always succeeds with a specific version
	UseContextEnsureSpecCachedFunc = func(ctx context.Context, serverURL string) (string, error) {
		return "v2.0.0", nil
	}

	version, err := useContextEnsureSpecCached(context.Background(), "http://any-url.com")
	if err != nil {
		t.Errorf("Expected no error with overridden function, got %v", err)
	}
	if version != "v2.0.0" {
		t.Errorf("Expected version 'v2.0.0', got %q", version)
	}

	// Override with a function that always fails
	UseContextEnsureSpecCachedFunc = func(ctx context.Context, serverURL string) (string, error) {
		return "", errors.New("mock caching error")
	}

	_, err = useContextEnsureSpecCached(context.Background(), "http://any-url.com")
	if err == nil {
		t.Error("Expected error with overridden function")
	}
}

func TestUseContextCommand_SpecCachingFailure(t *testing.T) {
	t.Setenv("QCTL_INSECURE_HTTP", "1")

	// Create a temporary directory for testing
	tmpDir := t.TempDir()
	tmpConfigPath := filepath.Join(tmpDir, config.ConfigFileName)

	// Override the config path for testing
	originalGetConfigPath := config.GetConfigPath
	config.GetConfigPath = func() (string, error) {
		return tmpConfigPath, nil
	}
	defer func() { config.GetConfigPath = originalGetConfigPath }()

	// Mock spec caching to fail
	originalEnsureSpecCached := UseContextEnsureSpecCachedFunc
	UseContextEnsureSpecCachedFunc = func(ctx context.Context, serverURL string) (string, error) {
		return "", errors.New("failed to fetch API version")
	}
	defer func() { UseContextEnsureSpecCachedFunc = originalEnsureSpecCached }()

	// Setup config with a context
	cfg := &config.Config{
		APIVersion:     config.APIVersion,
		CurrentContext: "other",
		Contexts: map[string]*config.Context{
			"test-context": {Server: "http://localhost:8000"},
			"other":        {Server: "http://localhost:9000"},
		},
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Failed to save config: %v", err)
	}

	// Create and execute command
	cmd := newUseContextCommand()
	cmd.SetArgs([]string{"test-context"})

	err := cmd.Execute()
	if err == nil {
		t.Error("Expected error when spec caching fails")
	}
	if !contains(err.Error(), "failed to cache API schema") {
		t.Errorf("Expected error to contain 'failed to cache API schema', got %q", err.Error())
	}
}
