package config

import (
	"path/filepath"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
)

// withOverrideConfig saves a config with contexts "dev" (current, allowlisted)
// and "prod" to a temporary path and points Load and Save at it.
func withOverrideConfig(t *testing.T) {
	t.Helper()
	SetPathOverride(filepath.Join(t.TempDir(), "config"))
	t.Cleanup(func() {
		SetPathOverride("")
		SetContextOverride("")
	})
	cfg := &Config{
		APIVersion:     APIVersion,
		CurrentContext: "dev",
		Contexts: map[string]*Context{
			"dev":  {Server: "https://dev.example.com", Protected: &allowlisted},
			"prod": {Server: "https://prod.example.com"},
		},
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
}

var allowlisted = false

// storedCurrentContext reads the config as it is on disk.
func storedCurrentContext(t *testing.T) string {
	t.Helper()
	saved := contextOverride
	SetContextOverride("")
	defer SetContextOverride(saved)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return cfg.CurrentContext
}

func TestContextOverrideSelectsTheContextInMemoryOnly(t *testing.T) {
	withOverrideConfig(t)
	SetContextOverride("prod")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	ctx, err := cfg.GetCurrentContext()
	if err != nil {
		t.Fatalf("GetCurrentContext: %v", err)
	}
	if cfg.CurrentContext != "prod" || !ctx.IsProtected() {
		t.Fatalf("current = %q (protected %v), want prod (protected)", cfg.CurrentContext, ctx.IsProtected())
	}

	ctx.OrganizationName = "Acme"
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if got := storedCurrentContext(t); got != "dev" {
		t.Errorf("stored current context = %q after a save under --context prod, want dev", got)
	}
	SetContextOverride("")
	reloaded, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := reloaded.Contexts["prod"].OrganizationName; got != "Acme" {
		t.Errorf("prod's organizationName = %q, want the edit made under --context prod", got)
	}
}

func TestUseContextPersistsUnderAContextOverride(t *testing.T) {
	for _, target := range []string{"prod", "dev"} {
		t.Run(target, func(t *testing.T) {
			withOverrideConfig(t)
			SetContextOverride("prod")
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if err := cfg.UseContext(target); err != nil {
				t.Fatalf("UseContext: %v", err)
			}
			if err := cfg.Save(); err != nil {
				t.Fatalf("Save: %v", err)
			}
			if got := storedCurrentContext(t); got != target {
				t.Errorf("stored current context = %q, want %q", got, target)
			}
		})
	}
}

func TestDeleteContextRefusesTheStoredContextUnderAnOverride(t *testing.T) {
	withOverrideConfig(t)
	SetContextOverride("prod")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.DeleteContext("dev"); err == nil {
		t.Fatal("DeleteContext(dev) succeeded while dev is the stored current context")
	}
	if err := cfg.DeleteContext("prod"); err == nil {
		t.Fatal("DeleteContext(prod) succeeded while --context selects prod")
	}
}

func TestUnknownContextOverrideIsBadUsage(t *testing.T) {
	withOverrideConfig(t)
	SetContextOverride("staging")
	_, err := Load()
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Fatalf("exit code = %d, want %d (err = %v)", code, qerrors.ExitBadUsage, err)
	}
}

func TestEveryContextIsProtectedUnlessAllowlisted(t *testing.T) {
	protected, allowed := true, false
	for _, tc := range []struct {
		name string
		key  *bool
		want bool
	}{
		{"absent", nil, true},
		{"true", &protected, true},
		{"false", &allowed, false},
	} {
		if got := (&Context{Protected: tc.key}).IsProtected(); got != tc.want {
			t.Errorf("protected %s: IsProtected() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestAnAllowlistSurvivesASave(t *testing.T) {
	withOverrideConfig(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Contexts["dev"].IsProtected() {
		t.Error("dev lost its protected: false through a save and load")
	}
	if !cfg.Contexts["prod"].IsProtected() || cfg.Contexts["prod"].Protected != nil {
		t.Errorf("prod = %v, want protected with no key written", cfg.Contexts["prod"].Protected)
	}
}
