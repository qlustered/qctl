package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"golang.org/x/term"
)

func TestDeleteContextCommand(t *testing.T) {
	tests := []struct {
		name          string
		setupContexts map[string]*config.Context
		currentCtx    string
		deleteCtx     string
		useYesFlag    bool
		shouldErr     bool
		errContains   string
		exitCode      int
	}{
		{
			name: "delete non-current context with --yes flag",
			setupContexts: map[string]*config.Context{
				"test1": {Server: "https://api1.example.com/api"},
				"test2": {Server: "https://api2.example.com/api"},
			},
			currentCtx: "test1",
			deleteCtx:  "test2",
			useYesFlag: true,
			shouldErr:  false,
		},
		{
			name: "cannot delete current context",
			setupContexts: map[string]*config.Context{
				"test1": {Server: "https://api1.example.com/api"},
				"test2": {Server: "https://api2.example.com/api"},
			},
			currentCtx:  "test1",
			deleteCtx:   "test1",
			useYesFlag:  true,
			shouldErr:   true,
			errContains: "cannot delete current context",
			exitCode:    qerrors.ExitBadUsage,
		},
		{
			name: "cannot delete non-existent context",
			setupContexts: map[string]*config.Context{
				"test1": {Server: "https://api1.example.com/api"},
			},
			currentCtx:  "test1",
			deleteCtx:   "nonexistent",
			useYesFlag:  true,
			shouldErr:   true,
			errContains: "not found",
			exitCode:    qerrors.ExitNotFound,
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
			cmd := newDeleteContextCommand()
			args := []string{tt.deleteCtx}
			if tt.useYesFlag {
				args = append(args, "--yes")
			}
			cmd.SetArgs(args)

			err := cmd.Execute()
			if code := qerrors.GetExitCode(err); code != tt.exitCode {
				t.Errorf("exit code = %d, want %d; error: %v", code, tt.exitCode, err)
			}

			if tt.shouldErr {
				if err == nil {
					t.Error("Expected error but got none")
					return
				}
				if tt.errContains != "" {
					errMsg := err.Error()
					if !contains(errMsg, tt.errContains) {
						t.Errorf("Expected error to contain %q, got %q", tt.errContains, errMsg)
					}
				}
				return
			}

			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			// Verify context was deleted
			cfg2, err := config.Load()
			if err != nil {
				t.Fatalf("Failed to load config after delete: %v", err)
			}

			if _, ok := cfg2.Contexts[tt.deleteCtx]; ok {
				t.Errorf("Context %q should have been deleted", tt.deleteCtx)
			}
		})
	}
}

func TestDeleteContextCommandConfirmation(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		shouldDelete   bool
		expectedOutput string
	}{
		{
			name:           "confirm with yes",
			input:          "yes\n",
			shouldDelete:   true,
			expectedOutput: "Deleted context",
		},
		{
			name:           "confirm with y",
			input:          "y\n",
			shouldDelete:   true,
			expectedOutput: "Deleted context",
		},
		{
			name:           "cancel with no",
			input:          "no\n",
			shouldDelete:   false,
			expectedOutput: "Delete cancelled",
		},
		{
			name:           "cancel with empty",
			input:          "\n",
			shouldDelete:   false,
			expectedOutput: "Delete cancelled",
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

			// Setup config with two contexts
			cfg := &config.Config{
				APIVersion:     config.APIVersion,
				CurrentContext: "test1",
				Contexts: map[string]*config.Context{
					"test1": {Server: "https://api1.example.com/api"},
					"test2": {Server: "https://api2.example.com/api"},
				},
			}
			if err := cfg.Save(); err != nil {
				t.Fatalf("Failed to save config: %v", err)
			}

			// Set up mock input
			cmdutil.SetReader(strings.NewReader(tt.input))
			defer cmdutil.ResetReader()

			// Capture output
			var out bytes.Buffer

			// Create and execute command
			cmd := newDeleteContextCommand()
			cmd.SetArgs([]string{"test2"})
			cmd.SetOut(&out)

			err := cmd.Execute()
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			// Verify context deletion status
			cfg2, err := config.Load()
			if err != nil {
				t.Fatalf("Failed to load config after delete: %v", err)
			}

			_, exists := cfg2.Contexts["test2"]
			if tt.shouldDelete && exists {
				t.Error("Context should have been deleted but still exists")
			}
			if !tt.shouldDelete && !exists {
				t.Error("Context should not have been deleted but was")
			}
		})
	}
}

func TestDeleteContextCommandNoArgs(t *testing.T) {
	cmd := newDeleteContextCommand()
	cmd.SetArgs([]string{})

	err := cmd.Execute()
	if err == nil {
		t.Error("Expected error when no context name provided")
	}
}

func TestDeleteContextCommandTooManyArgs(t *testing.T) {
	cmd := newDeleteContextCommand()
	cmd.SetArgs([]string{"ctx1", "ctx2"})

	err := cmd.Execute()
	if err == nil {
		t.Error("Expected error when too many arguments provided")
	}
}

// Helper function to check if a string contains a substring
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

// The chokepoint proof at the command layer: cmdutil.ErrNonInteractive is what
// all eighteen ConfirmYesNo sites return, and this is the cheapest of the
// fourteen commands to drive (no API server). If this exits 1, the sentinel
// regressed and the other thirteen went with it.
func TestDeleteContextNonInteractiveIsBadUsage(t *testing.T) {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		t.Skip("test requires non-TTY stdin (as under 'go test' / CI)")
	}

	tmpDir := t.TempDir()
	tmpConfigPath := filepath.Join(tmpDir, config.ConfigFileName)
	originalGetConfigPath := config.GetConfigPath
	config.GetConfigPath = func() (string, error) { return tmpConfigPath, nil }
	defer func() { config.GetConfigPath = originalGetConfigPath }()

	cfg := &config.Config{
		APIVersion:     config.APIVersion,
		CurrentContext: "test1",
		Contexts: map[string]*config.Context{
			"test1": {Server: "https://api1.example.com/api"},
			"test2": {Server: "https://api2.example.com/api"},
		},
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Failed to save config: %v", err)
	}

	cmdutil.ResetReader() // reader = os.Stdin, not a terminal under `go test`

	cmd := newDeleteContextCommand()
	cmd.SetArgs([]string{"test2"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected a non-interactive confirmation error")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}

	// A refused confirmation must change nothing on disk.
	cfg2, err := config.Load()
	if err != nil {
		t.Fatalf("Failed to load config after the refusal: %v", err)
	}
	if _, ok := cfg2.Contexts["test2"]; !ok {
		t.Error("context must survive a confirmation that could not be answered")
	}
}
