package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
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

func TestEnsureQctlDirRepairsExistingMode(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ConfigDirName)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}

	got, err := EnsureQctlDir()
	if err != nil {
		t.Fatal(err)
	}
	if got != dir {
		t.Fatalf("EnsureQctlDir() = %q, want %q", got, dir)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0700 {
		t.Errorf("qctl directory mode = %04o, want 0700", mode)
	}
}

func TestSaveRepairsDefaultQctlDirMode(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ConfigDirName)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{APIVersion: APIVersion, Contexts: map[string]*Context{}}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0700 {
		t.Errorf("qctl directory mode = %04o, want 0700", mode)
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

	// Test with no current context: the error must guide the user to fix it.
	cfg.CurrentContext = ""
	_, err = cfg.GetCurrentContext()
	if err == nil {
		t.Fatal("Expected error when CurrentContext is empty")
	}
	if !strings.Contains(err.Error(), "qctl config set-context") {
		t.Errorf("Expected error to guide user to set-context, got: %v", err)
	}

	// Test with non-existent context: also guides the user.
	cfg.CurrentContext = "nonexistent"
	_, err = cfg.GetCurrentContext()
	if err == nil {
		t.Fatal("Expected error when context does not exist")
	}
	if !strings.Contains(err.Error(), "qctl config set-context") {
		t.Errorf("Expected error to guide user to set-context, got: %v", err)
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
	if err := cfg.UseContext("nonexistent"); qerrors.GetExitCode(err) != qerrors.ExitNotFound {
		t.Errorf("exit code = %d, want %d; error: %v", qerrors.GetExitCode(err), qerrors.ExitNotFound, err)
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
	if err := cfg.DeleteContext("test1"); qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d; error: %v", qerrors.GetExitCode(err), qerrors.ExitBadUsage, err)
	}

	// Delete non-current context
	if err := cfg.DeleteContext("test2"); err != nil {
		t.Fatalf("DeleteContext failed: %v", err)
	}

	if _, ok := cfg.Contexts["test2"]; ok {
		t.Error("Context 'test2' should have been deleted")
	}

	// Try to delete non-existent context
	if err := cfg.DeleteContext("nonexistent"); qerrors.GetExitCode(err) != qerrors.ExitNotFound {
		t.Errorf("exit code = %d, want %d; error: %v", qerrors.GetExitCode(err), qerrors.ExitNotFound, err)
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

	// Test with no current context: the error must tell the user how to fix it.
	cfg.CurrentContext = ""
	_, err := ResolveServer(cfg, "")
	if err == nil {
		t.Fatal("Expected error when no server specified and no current context")
	}
	if !strings.Contains(err.Error(), "qctl config set-context") {
		t.Errorf("Expected error to guide user to set-context, got: %v", err)
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

// --config is the only way to point qctl at a config file that is not
// $HOME/.qctl/config, so the override has to reach both Load and Save.
func TestPathOverrideRedirectsLoadAndSave(t *testing.T) {
	overridePath := filepath.Join(t.TempDir(), "alternate-config")

	SetPathOverride(overridePath)
	defer SetPathOverride("")

	got, err := GetConfigPath()
	if err != nil {
		t.Fatalf("GetConfigPath failed: %v", err)
	}
	// Fatal, not Error: if the override is not in effect, the Save below would
	// write to the real $HOME/.qctl/config and destroy the developer's setup.
	if got != overridePath {
		t.Fatalf("GetConfigPath() = %q, want %q", got, overridePath)
	}

	cfg := &Config{
		APIVersion:     APIVersion,
		CurrentContext: "alt",
		Contexts:       map[string]*Context{"alt": {Server: "https://alt.example.com/api"}},
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	if _, err := os.Stat(overridePath); err != nil {
		t.Fatalf("Save did not write to the override path: %v", err)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if loaded.CurrentContext != "alt" {
		t.Errorf("Load read the wrong file: CurrentContext = %q, want %q", loaded.CurrentContext, "alt")
	}
}

// A missing default config is normal — a first run. A missing config the user
// named explicitly is a typo, and starting from an empty config would surface
// it much later as a confusing "no API server specified".
func TestLoadErrorsWhenOverriddenConfigIsMissing(t *testing.T) {
	missingPath := filepath.Join(t.TempDir(), "does-not-exist")

	SetPathOverride(missingPath)
	defer SetPathOverride("")

	_, err := Load()
	if err == nil {
		t.Fatal("Load succeeded on a --config path that does not exist; want an error")
	}
	if !strings.Contains(err.Error(), missingPath) {
		t.Errorf("error does not name the missing path %q: %v", missingPath, err)
	}
}

// Without --config, a missing file still means "first run", not an error.
func TestLoadWithoutOverrideTreatsMissingFileAsDefaults(t *testing.T) {
	originalGetConfigPath := GetConfigPath
	GetConfigPath = func() (string, error) {
		return filepath.Join(t.TempDir(), "does-not-exist"), nil
	}
	defer func() { GetConfigPath = originalGetConfigPath }()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load failed on a missing default config: %v", err)
	}
	if len(cfg.Contexts) != 0 {
		t.Errorf("Expected no contexts, got %d", len(cfg.Contexts))
	}
}

// `qctl --config /new/path config set-context` has to be able to create that
// file, the way `kubectl --kubeconfig=/new/path config set-context` does.
// Load refuses a missing override; LoadOrCreate is the write path's escape.
func TestLoadOrCreateAcceptsMissingOverriddenConfig(t *testing.T) {
	missingPath := filepath.Join(t.TempDir(), "brand-new-config")

	SetPathOverride(missingPath)
	defer SetPathOverride("")

	cfg, err := LoadOrCreate()
	if err != nil {
		t.Fatalf("LoadOrCreate failed on a config that does not exist yet: %v", err)
	}
	if cfg.APIVersion != APIVersion {
		t.Errorf("Expected APIVersion %q, got %q", APIVersion, cfg.APIVersion)
	}
	if cfg.Contexts == nil {
		t.Error("Contexts map is nil; callers add to it without checking")
	}
}

// LoadOrCreate must still read a config that does exist, not blank it.
func TestLoadOrCreateReadsExistingConfig(t *testing.T) {
	existingPath := filepath.Join(t.TempDir(), "existing-config")

	SetPathOverride(existingPath)
	defer SetPathOverride("")

	cfg := &Config{
		APIVersion:     APIVersion,
		CurrentContext: "already-here",
		Contexts:       map[string]*Context{"already-here": {Server: "https://existing.example.com"}},
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded, err := LoadOrCreate()
	if err != nil {
		t.Fatalf("LoadOrCreate failed: %v", err)
	}
	if loaded.CurrentContext != "already-here" {
		t.Errorf("LoadOrCreate blanked an existing config: CurrentContext = %q", loaded.CurrentContext)
	}
}

// A corrupt file is a real error either way — LoadOrCreate must not swallow it
// and hand back an empty config that Save would then overwrite the file with.
func TestLoadOrCreateStillFailsOnUnparseableConfig(t *testing.T) {
	badPath := filepath.Join(t.TempDir(), "corrupt-config")
	if err := os.WriteFile(badPath, []byte("\tnot: [valid: yaml"), 0600); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}

	SetPathOverride(badPath)
	defer SetPathOverride("")

	if _, err := LoadOrCreate(); err == nil {
		t.Fatal("LoadOrCreate succeeded on an unparseable config; want an error")
	}
}

// Clearing the override has to restore the default location, or one command
// run with --config would leak into everything after it.
func TestEmptyPathOverrideRestoresDefaultLocation(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory available: %v", err)
	}
	want := filepath.Join(home, ConfigDirName, ConfigFileName)

	SetPathOverride(filepath.Join(t.TempDir(), "alternate-config"))
	SetPathOverride("")

	got, err := GetConfigPath()
	if err != nil {
		t.Fatalf("GetConfigPath failed: %v", err)
	}
	if got != want {
		t.Errorf("GetConfigPath() = %q, want the default %q", got, want)
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

// The two resolvers used to share the name ResolveOrganization across packages
// with different arity. This pins the difference that made that dangerous: the
// config-level one hands a human-typed org NAME straight back, so a caller who
// mistook it for cmdutil.ResolveOrganization would put a name in a URL path.
// Name resolution lives in cmdutil.ResolveOrganization, not here.
func TestOrganizationFlagOrDefault_ReturnsTheFlagVerbatim(t *testing.T) {
	cfg := &Config{APIVersion: APIVersion}

	got, _, err := OrganizationFlagOrDefault(cfg, "Acme Corporation")
	if err != nil {
		t.Fatalf("OrganizationFlagOrDefault() error = %v", err)
	}
	if got != "Acme Corporation" {
		t.Errorf("got %q, want the flag verbatim -- this function does no name resolution", got)
	}
}

// The note appended to every 403 and 404 names the precedence step that
// supplied the organization. The phrase is chosen beside the branch that
// picks the value, so there is exactly one answer to "where did the org come
// from" -- re-deriving it in cmdutil or apierror would create a second.
func TestOrganizationFlagOrDefaultReportsItsSource(t *testing.T) {
	cfgWithContext := &Config{
		APIVersion:     APIVersion,
		CurrentContext: "default",
		Contexts: map[string]*Context{
			"default": {Server: "http://localhost:8000", Organization: "ctx-org"},
		},
	}

	tests := []struct {
		name       string
		orgFlag    string
		envOrg     string
		cfg        *Config
		wantValue  string
		wantSource OrgSource
		wantErr    bool
	}{
		{
			name:       "the --org flag wins",
			orgFlag:    "flag-org",
			envOrg:     "env-org",
			cfg:        cfgWithContext,
			wantValue:  "flag-org",
			wantSource: OrgSourceFlag,
		},
		{
			name:       "QCTL_ORG wins when the flag is unset",
			envOrg:     "env-org",
			cfg:        cfgWithContext,
			wantValue:  "env-org",
			wantSource: OrgSourceEnv,
		},
		{
			name:       "the current context is the last resort",
			cfg:        cfgWithContext,
			wantValue:  "ctx-org",
			wantSource: OrgSourceContext,
		},
		{
			name:    "no current context returns no source",
			cfg:     &Config{APIVersion: APIVersion},
			wantErr: true,
		},
		{
			name: "current context without an organization returns no source",
			cfg: &Config{
				APIVersion:     APIVersion,
				CurrentContext: "default",
				Contexts: map[string]*Context{
					"default": {Server: "http://localhost:8000"},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("QCTL_ORG", tt.envOrg)

			value, source, err := OrganizationFlagOrDefault(tt.cfg, tt.orgFlag)
			if tt.wantErr {
				if err == nil {
					t.Fatal("OrganizationFlagOrDefault() error = nil, want an error")
				}
				if value != "" || source != "" {
					t.Errorf("failure returned (%q, %q), want empty value and source", value, source)
				}
				return
			}
			if err != nil {
				t.Fatalf("OrganizationFlagOrDefault() error = %v", err)
			}
			if value != tt.wantValue {
				t.Errorf("value = %q, want %q", value, tt.wantValue)
			}
			if source != tt.wantSource {
				t.Errorf("source = %q, want %q", source, tt.wantSource)
			}
		})
	}
}

// The three phrases are user-visible: they are rendered verbatim inside the
// organization-scope note on every 403 and 404. Pinning them here is what
// stops a rename from silently changing what a reader is told to change.
func TestOrgSourcePhrasesAreTheOnesTheNoteRenders(t *testing.T) {
	for source, want := range map[OrgSource]string{
		OrgSourceFlag:    "from the --org flag",
		OrgSourceEnv:     "from QCTL_ORG",
		OrgSourceContext: "from the current context",
	} {
		if string(source) != want {
			t.Errorf("OrgSource = %q, want %q", source, want)
		}
	}
}

// TestSaveTightensAPreExistingLooseConfig: os.WriteFile applied 0600 only when
// it created the file, so a config left at 0644 by an older qctl or a restored
// backup kept that mode through every save. A different inode proves the data
// went into a fresh 0600 file rather than being tightened in place.
func TestSaveTightensAPreExistingLooseConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), ConfigFileName)
	SetPathOverride(path)
	defer SetPathOverride("")
	if err := os.WriteFile(path, []byte("apiVersion: qctl/v1\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	cfg := &Config{APIVersion: APIVersion, CurrentContext: "a", Contexts: map[string]*Context{"a": {Server: "https://a.example.com"}}}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := after.Mode().Perm(); got != 0600 {
		t.Errorf("config mode = %04o, want 0600", got)
	}
	if os.SameFile(before, after) {
		t.Error("the config was written in place at its old loose mode")
	}
}

// TestSaveWritesThroughASymlinkedConfig: a rename replaces a link rather than
// following it, so without resolving it a dotfiles-managed config would be
// silently swapped for a plain file and the managed copy left stale.
func TestSaveWritesThroughASymlinkedConfig(t *testing.T) {
	target := filepath.Join(t.TempDir(), "managed-config")
	if err := os.WriteFile(target, []byte("apiVersion: qctl/v1\n"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	link := filepath.Join(t.TempDir(), ConfigFileName)
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	SetPathOverride(link)
	defer SetPathOverride("")

	cfg := &Config{APIVersion: APIVersion, CurrentContext: "linked", Contexts: map[string]*Context{"linked": {Server: "https://l.example.com"}}}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	if fi, err := os.Lstat(link); err != nil {
		t.Fatalf("lstat: %v", err)
	} else if fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("%s was replaced by a plain file instead of written through", link)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(data), "currentContext: linked") {
		t.Errorf("the link's target was not updated:\n%s", data)
	}
}
