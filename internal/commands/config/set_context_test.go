package config

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestSetContextCommand(t *testing.T) {
	t.Setenv("QCTL_INSECURE_HTTP", "1")

	// Mock the spec caching function to always succeed for these tests
	originalEnsureSpecCached := EnsureSpecCachedFunc
	EnsureSpecCachedFunc = func(ctx context.Context, serverURL string) (string, error) {
		return "test-version", nil
	}
	defer func() { EnsureSpecCachedFunc = originalEnsureSpecCached }()

	tests := []struct {
		name               string
		setupContexts      map[string]*config.Context
		currentCtx         string
		contextToSet       string
		flags              []string
		mockAPIResponse    int // HTTP status code to return from mock server
		mockAPIError       bool
		shouldErr          bool
		errContains        string
		expectedCtxServer  string
		expectedCurrentCtx string
		expectedOrg        string
		expectedOrgName    string
	}{
		{
			name:               "create new context with valid server",
			setupContexts:      map[string]*config.Context{},
			currentCtx:         "",
			contextToSet:       "test1",
			flags:              []string{"--server"},
			mockAPIResponse:    http.StatusOK,
			shouldErr:          false,
			expectedCurrentCtx: "test1",
		},
		{
			name: "create new context switches to it automatically",
			setupContexts: map[string]*config.Context{
				"existing": {Server: "https://api.example.com/api"},
			},
			currentCtx:         "existing",
			contextToSet:       "newcontext",
			flags:              []string{"--server"},
			mockAPIResponse:    http.StatusOK,
			shouldErr:          false,
			expectedCurrentCtx: "newcontext",
		},
		{
			name: "update existing context switches to it",
			setupContexts: map[string]*config.Context{
				"ctx1": {Server: "https://api1.example.com/api"},
				"ctx2": {Server: "https://api2.example.com/api"},
			},
			currentCtx:         "ctx1",
			contextToSet:       "ctx2",
			flags:              []string{"--output", "json"},
			mockAPIResponse:    http.StatusOK,
			shouldErr:          false,
			expectedCurrentCtx: "ctx2",
		},
		{
			name:          "fail when API endpoint is unreachable",
			setupContexts: map[string]*config.Context{},
			currentCtx:    "",
			contextToSet:  "test1",
			flags:         []string{"--server"},
			mockAPIError:  true,
			shouldErr:     true,
			errContains:   "failed to validate API endpoint",
		},
		{
			name:            "fail when API returns 500",
			setupContexts:   map[string]*config.Context{},
			currentCtx:      "",
			contextToSet:    "test1",
			flags:           []string{"--server"},
			mockAPIResponse: http.StatusInternalServerError,
			shouldErr:       true,
			errContains:     "server error",
		},
		{
			name:               "accept 401 response (server exists but requires auth)",
			setupContexts:      map[string]*config.Context{},
			currentCtx:         "",
			contextToSet:       "test1",
			flags:              []string{"--server"},
			mockAPIResponse:    http.StatusUnauthorized,
			shouldErr:          false,
			expectedCurrentCtx: "test1",
		},
		{
			name:               "accept 404 response (server exists)",
			setupContexts:      map[string]*config.Context{},
			currentCtx:         "",
			contextToSet:       "test1",
			flags:              []string{"--server"},
			mockAPIResponse:    http.StatusNotFound,
			shouldErr:          false,
			expectedCurrentCtx: "test1",
		},
		{
			name:          "new context requires server flag",
			setupContexts: map[string]*config.Context{},
			currentCtx:    "",
			contextToSet:  "test1",
			flags:         []string{},
			shouldErr:     true,
			errContains:   "--server is required",
		},
		{
			name: "update current context with --current flag",
			setupContexts: map[string]*config.Context{
				"existing": {Server: "https://api.example.com/api"},
			},
			currentCtx:         "existing",
			contextToSet:       "", // No explicit name, using --current
			flags:              []string{"--current", "--output", "json"},
			shouldErr:          false,
			expectedCurrentCtx: "existing",
		},
		{
			name:          "fail --current when no current context set",
			setupContexts: map[string]*config.Context{},
			currentCtx:    "",
			contextToSet:  "",
			flags:         []string{"--current", "--output", "json"},
			shouldErr:     true,
			errContains:   "no current context set",
		},
		{
			name: "set org with UUID directly",
			setupContexts: map[string]*config.Context{
				"existing": {Server: "https://api.example.com/api"},
			},
			currentCtx:         "existing",
			contextToSet:       "", // Using --current
			flags:              []string{"--current", "--org", "550e8400-e29b-41d4-a716-446655440000"},
			shouldErr:          false,
			expectedCurrentCtx: "existing",
			expectedOrg:        "550e8400-e29b-41d4-a716-446655440000",
		},
		{
			name: "set org resolves name from cache",
			setupContexts: map[string]*config.Context{
				"existing": {
					Server: "https://api.example.com/api",
					Organizations: []config.OrganizationRef{
						{ID: "550e8400-e29b-41d4-a716-446655440000", Name: "Acme Corporation"},
						{ID: "660e8400-e29b-41d4-a716-446655440001", Name: "Beta Inc"},
					},
				},
			},
			currentCtx:         "existing",
			contextToSet:       "", // Using --current
			flags:              []string{"--current", "--org", "Acme Corporation"},
			shouldErr:          false,
			expectedCurrentCtx: "existing",
			expectedOrg:        "550e8400-e29b-41d4-a716-446655440000",
			expectedOrgName:    "Acme Corporation",
		},
		{
			name: "set org fails without cached orgs when using name",
			setupContexts: map[string]*config.Context{
				"existing": {Server: "https://api.example.com/api"},
			},
			currentCtx:   "existing",
			contextToSet: "",
			flags:        []string{"--current", "--org", "Unknown Org"},
			shouldErr:    true,
			errContains:  "no cached organizations",
		},
		{
			name:          "context name required without --current",
			setupContexts: map[string]*config.Context{},
			currentCtx:    "",
			contextToSet:  "",
			flags:         []string{},
			shouldErr:     true,
			errContains:   "context name required",
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

			// Setup mock API server if needed
			var serverURL string
			if tt.mockAPIError {
				// Use a URL that will fail to connect
				serverURL = "http://unreachable.invalid"
			} else if len(tt.flags) > 0 && tt.flags[0] == "--server" {
				mock := testutil.NewMockAPIServer()
				t.Cleanup(mock.Close)
				mock.RegisterHandler(http.MethodGet, "/api/version", func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(tt.mockAPIResponse)
				})
				serverURL = mock.URL()
			}

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
			cmd := newSetContextCommand()
			var args []string
			// Only add context name if provided (not using --current)
			if tt.contextToSet != "" {
				args = append(args, tt.contextToSet)
			}
			// Process flags
			for i := 0; i < len(tt.flags); i++ {
				flag := tt.flags[i]
				switch flag {
				case "--server":
					if serverURL != "" {
						args = append(args, flag, serverURL)
					}
				case "--output", "--org":
					// These flags expect a value
					if i+1 < len(tt.flags) {
						args = append(args, flag, tt.flags[i+1])
						i++ // Skip the value in next iteration
					}
				case "--current":
					args = append(args, flag)
				default:
					// Skip values that were already processed
				}
			}
			cmd.SetArgs(args)

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

			// Verify context was created/updated and is now current
			cfg2, err := config.Load()
			if err != nil {
				t.Fatalf("Failed to load config after set: %v", err)
			}

			if tt.expectedCurrentCtx != "" && cfg2.CurrentContext != tt.expectedCurrentCtx {
				t.Errorf("Expected current context to be %q, got %q", tt.expectedCurrentCtx, cfg2.CurrentContext)
			}

			// Determine which context to check
			ctxToCheck := tt.contextToSet
			if ctxToCheck == "" {
				ctxToCheck = tt.expectedCurrentCtx
			}

			if ctxToCheck != "" {
				ctx2, ok := cfg2.Contexts[ctxToCheck]
				if !ok {
					t.Errorf("Context %q should exist", ctxToCheck)
				} else {
					// Check org settings if expected
					if tt.expectedOrg != "" && ctx2.Organization != tt.expectedOrg {
						t.Errorf("Expected organization %q, got %q", tt.expectedOrg, ctx2.Organization)
					}
					if tt.expectedOrgName != "" && ctx2.OrganizationName != tt.expectedOrgName {
						t.Errorf("Expected organization name %q, got %q", tt.expectedOrgName, ctx2.OrganizationName)
					}
				}
			}
		})
	}
}

func TestSetContextCommandNoArgs(t *testing.T) {
	cmd := newSetContextCommand()
	cmd.SetArgs([]string{})

	err := cmd.Execute()
	if err == nil {
		t.Error("Expected error when no context name provided")
	}
}

func TestSetContextCommandTooManyArgs(t *testing.T) {
	cmd := newSetContextCommand()
	cmd.SetArgs([]string{"ctx1", "ctx2"})

	err := cmd.Execute()
	if err == nil {
		t.Error("Expected error when too many arguments provided")
	}
}

func TestValidateAPIEndpoint(t *testing.T) {
	t.Setenv("QCTL_INSECURE_HTTP", "1")
	tests := []struct {
		name        string
		statusCode  int
		shouldErr   bool
		errContains string
	}{
		{
			name:       "200 OK - valid",
			statusCode: http.StatusOK,
			shouldErr:  false,
		},
		{
			name:       "401 Unauthorized - valid (server exists)",
			statusCode: http.StatusUnauthorized,
			shouldErr:  false,
		},
		{
			name:       "403 Forbidden - valid (server exists)",
			statusCode: http.StatusForbidden,
			shouldErr:  false,
		},
		{
			name:       "404 Not Found - valid (server exists)",
			statusCode: http.StatusNotFound,
			shouldErr:  false,
		},
		{
			name:        "500 Internal Server Error - invalid",
			statusCode:  http.StatusInternalServerError,
			shouldErr:   true,
			errContains: "server error",
		},
		{
			name:        "502 Bad Gateway - invalid",
			statusCode:  http.StatusBadGateway,
			shouldErr:   true,
			errContains: "server error",
		},
		{
			name:        "503 Service Unavailable - invalid",
			statusCode:  http.StatusServiceUnavailable,
			shouldErr:   true,
			errContains: "server error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()
			mock.RegisterHandler(http.MethodGet, "/api/version", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
			})

			err := validateAPIEndpointDefault(mock.URL())

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
				t.Errorf("Unexpected error: %v", err)
			}
		})
	}
}

func TestValidateAPIEndpointUnreachable(t *testing.T) {
	err := validateAPIEndpointDefault("http://unreachable.invalid")
	if err == nil {
		t.Error("Expected error for unreachable endpoint")
	}
	if !contains(err.Error(), "cannot reach") {
		t.Errorf("Expected error to contain 'cannot reach', got %q", err.Error())
	}
}

func TestValidateAPIEndpointFuncOverride(t *testing.T) {
	// Test that the function can be overridden for testing
	originalFunc := ValidateAPIEndpointFunc
	defer func() { ValidateAPIEndpointFunc = originalFunc }()

	// Override with a function that always succeeds
	ValidateAPIEndpointFunc = func(serverURL string) error {
		return nil
	}

	err := validateAPIEndpoint("http://any-url.com")
	if err != nil {
		t.Errorf("Expected no error with overridden function, got %v", err)
	}

	// Override with a function that always fails
	ValidateAPIEndpointFunc = func(serverURL string) error {
		return errors.New("mock error")
	}

	err = validateAPIEndpoint("http://any-url.com")
	if err == nil {
		t.Error("Expected error with overridden function")
	}
}

func TestSetContextHelp(t *testing.T) {
	cmd := newSetContextCommand()

	// Check that example is set
	if cmd.Example == "" {
		t.Error("Expected command to have examples")
	}

	// Check that example contains key content
	if !contains(cmd.Example, "qctl config set-context") {
		t.Error("Expected example to contain 'qctl config set-context'")
	}

	if !contains(cmd.Example, "--server") {
		t.Error("Expected example to contain '--server'")
	}

	// Check long description mentions auto-switch
	if !contains(cmd.Long, "automatically become the current context") {
		t.Error("Expected long description to mention auto-switch behavior")
	}

	// Check long description mentions validation
	if !contains(cmd.Long, "validated") {
		t.Error("Expected long description to mention API validation")
	}
}

func TestEnsureSpecCachedFuncOverride(t *testing.T) {
	// Test that the function can be overridden for testing
	originalFunc := EnsureSpecCachedFunc
	defer func() { EnsureSpecCachedFunc = originalFunc }()

	// Override with a function that always succeeds with a specific version
	EnsureSpecCachedFunc = func(ctx context.Context, serverURL string) (string, error) {
		return "v1.2.3", nil
	}

	version, err := ensureSpecCached(context.Background(), "http://any-url.com")
	if err != nil {
		t.Errorf("Expected no error with overridden function, got %v", err)
	}
	if version != "v1.2.3" {
		t.Errorf("Expected version 'v1.2.3', got %q", version)
	}

	// Override with a function that always fails
	EnsureSpecCachedFunc = func(ctx context.Context, serverURL string) (string, error) {
		return "", errors.New("mock caching error")
	}

	_, err = ensureSpecCached(context.Background(), "http://any-url.com")
	if err == nil {
		t.Error("Expected error with overridden function")
	}
}

func TestSetContextCommand_SpecCachingFailure(t *testing.T) {
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

	// Mock API validation to succeed
	originalValidate := ValidateAPIEndpointFunc
	ValidateAPIEndpointFunc = func(serverURL string) error {
		return nil
	}
	defer func() { ValidateAPIEndpointFunc = originalValidate }()

	// Mock spec caching to fail
	originalEnsureSpecCached := EnsureSpecCachedFunc
	EnsureSpecCachedFunc = func(ctx context.Context, serverURL string) (string, error) {
		return "", errors.New("failed to fetch API version")
	}
	defer func() { EnsureSpecCachedFunc = originalEnsureSpecCached }()

	// Setup empty config
	cfg := &config.Config{
		APIVersion: config.APIVersion,
		Contexts:   map[string]*config.Context{},
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Failed to save config: %v", err)
	}

	// Create and execute command
	cmd := newSetContextCommand()
	cmd.SetArgs([]string{"test-context", "--server", "http://localhost:8000"})

	err := cmd.Execute()
	if err == nil {
		t.Error("Expected error when spec caching fails")
	}
	if !contains(err.Error(), "failed to cache API schema") {
		t.Errorf("Expected error to contain 'failed to cache API schema', got %q", err.Error())
	}
}

func TestFetchAuthConfig(t *testing.T) {
	t.Setenv("QCTL_INSECURE_HTTP", "1")

	tests := []struct {
		name              string
		statusCode        int
		responseBody      string
		shouldErr         bool
		errContains       string
		expectedHost      string
		expectedClientID  string
	}{
		{
			name:       "successful fetch",
			statusCode: http.StatusOK,
			responseBody: `{"kinde_host": "https://test.kinde.com", "kinde_cli_client_id": "test-client-123"}`,
			shouldErr:  false,
			expectedHost:     "https://test.kinde.com",
			expectedClientID: "test-client-123",
		},
		{
			name:        "404 returns error",
			statusCode:  http.StatusNotFound,
			shouldErr:   true,
			errContains: "returned status",
		},
		{
			name:        "500 returns error",
			statusCode:  http.StatusInternalServerError,
			shouldErr:   true,
			errContains: "returned status",
		},
		{
			name:        "invalid JSON returns error",
			statusCode:  http.StatusOK,
			responseBody: `invalid json`,
			shouldErr:   true,
			errContains: "failed to parse",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()
			mock.RegisterHandler(http.MethodGet, "/api/auth/config", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
				if tt.responseBody != "" {
					w.Write([]byte(tt.responseBody))
				}
			})

			authConfig, err := fetchAuthConfigDefault(mock.URL())

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
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if authConfig.KindeHost != tt.expectedHost {
				t.Errorf("Expected KindeHost %q, got %q", tt.expectedHost, authConfig.KindeHost)
			}
			if authConfig.KindeClientID != tt.expectedClientID {
				t.Errorf("Expected KindeClientID %q, got %q", tt.expectedClientID, authConfig.KindeClientID)
			}
		})
	}
}

func TestFetchAuthConfigFuncOverride(t *testing.T) {
	// Test that the function can be overridden for testing
	originalFunc := FetchAuthConfigFunc
	defer func() { FetchAuthConfigFunc = originalFunc }()

	// Override with a function that always succeeds
	FetchAuthConfigFunc = func(serverURL string) (*AuthConfig, error) {
		return &AuthConfig{
			KindeHost:     "https://override.kinde.com",
			KindeClientID: "override-client-id",
		}, nil
	}

	config, err := fetchAuthConfig("http://any-url.com")
	if err != nil {
		t.Errorf("Expected no error with overridden function, got %v", err)
	}
	if config.KindeHost != "https://override.kinde.com" {
		t.Errorf("Expected KindeHost 'https://override.kinde.com', got %q", config.KindeHost)
	}

	// Override with a function that always fails
	FetchAuthConfigFunc = func(serverURL string) (*AuthConfig, error) {
		return nil, errors.New("mock error")
	}

	_, err = fetchAuthConfig("http://any-url.com")
	if err == nil {
		t.Error("Expected error with overridden function")
	}
}

func TestSetContextCommand_AuthConfigFetch(t *testing.T) {
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

	// Mock API validation to succeed
	originalValidate := ValidateAPIEndpointFunc
	ValidateAPIEndpointFunc = func(serverURL string) error {
		return nil
	}
	defer func() { ValidateAPIEndpointFunc = originalValidate }()

	// Mock spec caching to succeed
	originalEnsureSpecCached := EnsureSpecCachedFunc
	EnsureSpecCachedFunc = func(ctx context.Context, serverURL string) (string, error) {
		return "test-version", nil
	}
	defer func() { EnsureSpecCachedFunc = originalEnsureSpecCached }()

	// Mock auth config fetch to succeed
	originalFetchAuthConfig := FetchAuthConfigFunc
	FetchAuthConfigFunc = func(serverURL string) (*AuthConfig, error) {
		return &AuthConfig{
			KindeHost:     "https://test.kinde.com",
			KindeClientID: "test-client-123",
		}, nil
	}
	defer func() { FetchAuthConfigFunc = originalFetchAuthConfig }()

	// Setup empty config
	cfg := &config.Config{
		APIVersion: config.APIVersion,
		Contexts:   map[string]*config.Context{},
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Failed to save config: %v", err)
	}

	// Create and execute command
	cmd := newSetContextCommand()
	cmd.SetArgs([]string{"test-context", "--server", "http://localhost:8000"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Verify auth config was stored in context
	cfg2, err := config.Load()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	ctx2, ok := cfg2.Contexts["test-context"]
	if !ok {
		t.Fatal("Context 'test-context' should exist")
	}

	if ctx2.KindeHost != "https://test.kinde.com" {
		t.Errorf("Expected KindeHost 'https://test.kinde.com', got %q", ctx2.KindeHost)
	}
	if ctx2.KindeClientID != "test-client-123" {
		t.Errorf("Expected KindeClientID 'test-client-123', got %q", ctx2.KindeClientID)
	}
}

func TestSetContextCommand_AuthConfigFetchFailure(t *testing.T) {
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

	// Mock API validation to succeed
	originalValidate := ValidateAPIEndpointFunc
	ValidateAPIEndpointFunc = func(serverURL string) error {
		return nil
	}
	defer func() { ValidateAPIEndpointFunc = originalValidate }()

	// Mock spec caching to succeed
	originalEnsureSpecCached := EnsureSpecCachedFunc
	EnsureSpecCachedFunc = func(ctx context.Context, serverURL string) (string, error) {
		return "test-version", nil
	}
	defer func() { EnsureSpecCachedFunc = originalEnsureSpecCached }()

	// Mock auth config fetch to fail
	originalFetchAuthConfig := FetchAuthConfigFunc
	FetchAuthConfigFunc = func(serverURL string) (*AuthConfig, error) {
		return nil, errors.New("auth config endpoint not available")
	}
	defer func() { FetchAuthConfigFunc = originalFetchAuthConfig }()

	// Setup empty config
	cfg := &config.Config{
		APIVersion: config.APIVersion,
		Contexts:   map[string]*config.Context{},
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Failed to save config: %v", err)
	}

	// Create and execute command
	cmd := newSetContextCommand()
	cmd.SetArgs([]string{"test-context", "--server", "http://localhost:8000"})

	// Should succeed even though auth config fetch fails (non-fatal warning)
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Expected no error (auth config failure should be non-fatal), got: %v", err)
	}

	// Verify context was still created
	cfg2, err := config.Load()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	ctx2, ok := cfg2.Contexts["test-context"]
	if !ok {
		t.Fatal("Context 'test-context' should exist")
	}

	// Auth config should be empty since fetch failed
	if ctx2.KindeHost != "" {
		t.Errorf("Expected KindeHost to be empty, got %q", ctx2.KindeHost)
	}
	if ctx2.KindeClientID != "" {
		t.Errorf("Expected KindeClientID to be empty, got %q", ctx2.KindeClientID)
	}
}
