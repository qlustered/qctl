package releases

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"gopkg.in/yaml.v3"
)

// writeConfig writes a config file at path with the given contexts.
func writeConfig(t *testing.T, path, current string, contexts map[string]*config.Context) {
	t.Helper()
	data, err := yaml.Marshal(&config.Config{APIVersion: config.APIVersion, CurrentContext: current, Contexts: contexts})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestResolveTargetPrecedence(t *testing.T) {
	env := testutil.NewTestEnv(t)
	t.Setenv("QCTL_SERVER", "")
	t.Setenv(SkipEnv, "")
	env.SetupMultipleContexts(map[string]*config.Context{
		"cloud": {Server: "https://cloud.example.com"},
		"local": {Server: "http://localhost:8000", SkipReleaseCheck: true},
	}, "cloud")
	other := filepath.Join(t.TempDir(), "other")
	writeConfig(t, other, "staging", map[string]*config.Context{
		"staging": {Server: "https://staging.example.com"},
		"dev":     {Server: "http://localhost:9000", SkipReleaseCheck: true},
	})

	for _, tt := range []struct {
		name        string
		args        []string
		envServer   string
		envSkip     string
		wantServer  string
		wantSource  config.ServerSource
		wantContext string
		wantSkip    bool
	}{
		{"current context", []string{"get", "tables"}, "", "", "https://cloud.example.com", config.ServerSourceContext, "cloud", false},
		{"--context", []string{"get", "--context", "local"}, "", "", "http://localhost:8000", config.ServerSourceContext, "local", true},
		{"QCTL_SERVER beats the context", []string{"--context=local", "get"}, "https://env.example.com", "", "https://env.example.com", config.ServerSourceEnv, "local", false},
		{"--server beats QCTL_SERVER", []string{"get", "--server", "https://flag.example.com"}, "https://env.example.com", "", "https://flag.example.com", config.ServerSourceFlag, "cloud", false},
		{"--config", []string{"--config", other, "get"}, "", "", "https://staging.example.com", config.ServerSourceContext, "staging", false},
		{"--config with --context", []string{"--config=" + other, "--context", "dev", "get"}, "", "", "http://localhost:9000", config.ServerSourceContext, "dev", true},
		{"env skip", []string{"get"}, "", "1", "https://cloud.example.com", config.ServerSourceContext, "cloud", true},
		{"env skip false", []string{"--context", "local", "get"}, "", "false", "http://localhost:8000", config.ServerSourceContext, "local", true},
		{"env skip unparseable", []string{"get"}, "", "nope", "https://cloud.example.com", config.ServerSourceContext, "cloud", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("QCTL_SERVER", tt.envServer)
			t.Setenv(SkipEnv, tt.envSkip)
			got, err := ResolveTarget(ParseArgs(tt.args))
			if err != nil {
				t.Fatal(err)
			}
			if got.Server != tt.wantServer || got.Source != tt.wantSource || got.Context != tt.wantContext || got.Skip != tt.wantSkip {
				t.Fatalf("got %+v, want server %q source %q context %q skip %v",
					got, tt.wantServer, tt.wantSource, tt.wantContext, tt.wantSkip)
			}
		})
	}
}

func TestResolveTargetWithoutAServerIsNoTarget(t *testing.T) {
	testutil.NewTestEnv(t)
	t.Setenv("QCTL_SERVER", "")
	t.Setenv(SkipEnv, "")

	got, err := ResolveTarget(ParseArgs([]string{"get", "tables"}))
	if err != nil || got.Server != "" || got.Context != "" {
		t.Fatalf("no config file: got %+v, %v; want no target", got, err)
	}

	empty := filepath.Join(t.TempDir(), "config")
	writeConfig(t, empty, "blank", map[string]*config.Context{"blank": {}})
	got, err = ResolveTarget(ParseArgs([]string{"--config", empty, "get"}))
	if err != nil || got.Server != "" || got.Context != "blank" {
		t.Fatalf("context without a server: got %+v, %v; want no target", got, err)
	}

	t.Setenv("QCTL_SERVER", "https://env.example.com")
	got, err = ResolveTarget(ParseArgs([]string{"get"}))
	if err != nil || got.Server != "https://env.example.com" || got.Source != config.ServerSourceEnv {
		t.Fatalf("QCTL_SERVER without a config: got %+v, %v", got, err)
	}
}

func TestResolveTargetConfigErrors(t *testing.T) {
	env := testutil.NewTestEnv(t)
	t.Setenv("QCTL_SERVER", "")
	env.SetupConfig("https://cloud.example.com", "")

	_, err := ResolveTarget(ParseArgs([]string{"--config", filepath.Join(t.TempDir(), "missing"), "get"}))
	if err == nil {
		t.Error("a missing --config file is not an error")
	}
	_, err = ResolveTarget(ParseArgs([]string{"--context", "nope", "get"}))
	if code := qerrors.GetExitCode(err); err == nil || code != 2 {
		t.Errorf("unknown --context: err %v, exit %d; want exit 2", err, code)
	}
}

func TestResolveTargetRestoresTheConfigOverrides(t *testing.T) {
	testutil.NewTestEnv(t)
	t.Cleanup(func() {
		config.SetPathOverride("")
		config.SetContextOverride("")
	})
	config.SetPathOverride("/before")
	config.SetContextOverride("before")

	_, _ = ResolveTarget(ParseArgs([]string{"--config", "/after", "--context", "after"}))
	if config.PathOverride() != "/before" || config.ContextOverride() != "before" {
		t.Fatalf("overrides now %q, %q", config.PathOverride(), config.ContextOverride())
	}
}
