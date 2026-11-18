package root

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"golang.org/x/term"
)

// runRootWithContextFlag executes the real tree and resets every persistent
// flag these tests set, --context included, plus the override it feeds.
func runRootWithContextFlag(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := GetRootCmd()
	reset := func() {
		for _, f := range []struct{ name, def string }{
			{"context", ""}, {"org", ""}, {"output", "table"}, {"config", ""}, {"server", ""},
		} {
			resetPersistentFlag(t, cmd, f.name, f.def)
		}
		config.SetContextOverride("")
		config.SetPathOverride("")
		apierror.SetOrgScope("", "")
	}
	reset()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(args)
	t.Cleanup(func() {
		cmd.SetArgs(nil)
		cmd.SetOut(nil)
		cmd.SetErr(nil)
		reset()
	})
	err := cmd.Execute()
	return out.String(), err
}

// setupStoredProd stores contexts dev and prod (served by prodServer), with
// prod current. Neither has a protected key, so both are protected.
func setupStoredProd(t *testing.T, prodServer string) {
	t.Helper()
	env := testutil.NewTestEnv(t)
	env.SetupMultipleContexts(map[string]*config.Context{
		"dev":  {Server: "https://dev.example.com", Organization: scopeTestOrgID},
		"prod": {Server: prodServer, Organization: scopeTestOrgID},
	}, "prod")
}

func TestDestructiveCommandOnAProtectedContextIsRefused(t *testing.T) {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		t.Skip("test requires non-TTY stdin (as under 'go test' / CI)")
	}
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	setupStoredProd(t, mock.URL())

	_, err := runRootWithContextFlag(t, "delete", "rule", "old-rule", "--yes")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Fatalf("exit code = %d, want %d (err = %v)", code, qerrors.ExitBadUsage, err)
	}
	if !strings.Contains(err.Error(), "--context prod") {
		t.Errorf("refusal does not name --context prod: %v", err)
	}
	if reqs := mock.Requests(); len(reqs) != 0 {
		t.Errorf("refused command still sent %v", reqs)
	}
}

func TestNamingTheProtectedContextLetsTheCommandRun(t *testing.T) {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		t.Skip("test requires non-TTY stdin (as under 'go test' / CI)")
	}
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	env := testutil.NewTestEnv(t)
	env.SetupMultipleContexts(map[string]*config.Context{
		"prod": {Server: mock.URL(), Organization: scopeTestOrgID},
	}, "prod")
	key, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(key, scopeTestOrgID, "tok")

	_, _ = runRootWithContextFlag(t, "delete", "rule", "old-rule", "--yes", "--context", "prod")
	if len(mock.Requests()) == 0 {
		t.Fatal("delete rule --context prod never reached the server")
	}
}

func TestContextFlagSelectsAContextWithoutSwitching(t *testing.T) {
	setupStoredProd(t, "https://prod.example.com")

	out, err := runRootWithContextFlag(t, "config", "current-context", "--context", "dev", "-o", "json")
	if err != nil {
		t.Fatalf("current-context --context dev: %v", err)
	}
	var doc struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	if doc.Name != "dev" {
		t.Errorf("name = %q, want dev", doc.Name)
	}
	config.SetContextOverride("") // read what is on disk
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.CurrentContext != "prod" {
		t.Errorf("stored current context = %q after --context dev, want prod", cfg.CurrentContext)
	}
}
