package alert

import (
	"net/http"
	"os"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"golang.org/x/term"
)

// protectCurrentContext marks the stored current context protected and
// returns its name.
func protectCurrentContext(t *testing.T) string {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	ctx, err := cfg.GetCurrentContext()
	if err != nil {
		t.Fatalf("GetCurrentContext: %v", err)
	}
	ctx.Protected = testutil.BoolPtr(true)
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	t.Cleanup(func() { config.SetContextOverride("") })
	return cfg.CurrentContext
}

func TestResolveDestructiveActionOnAProtectedContextNeedsTheContextNamed(t *testing.T) {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		t.Skip("test requires non-TTY stdin (as under 'go test' / CI)")
	}
	mock := newAlertTestEnv(t)
	registerResolvingGetAlert(mock, migrationAlert())
	registerEmptySuggestions(mock)
	var posts int
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9",
		func(w http.ResponseWriter, r *http.Request) {
			posts++
			testutil.RespondJSON(w, http.StatusOK, api.AlertResolveResponseSchema{})
		})
	name := protectCurrentContext(t)

	_, err := runAlertCommand(t, "alert", "resolve", "9", "--action", "discard_migration", "--yes")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Fatalf("exit code = %d, want %d (err = %v)", code, qerrors.ExitBadUsage, err)
	}
	if posts != 0 {
		t.Fatalf("refused resolve still sent %d POSTs", posts)
	}

	config.SetContextOverride(name)
	if out, err := runAlertCommand(t, "alert", "resolve", "9", "--action", "discard_migration", "--yes"); err != nil {
		t.Fatalf("with --context %s: %v\n%s", name, err, out)
	}
	if posts != 1 {
		t.Errorf("POSTs = %d with the context named, want 1", posts)
	}
}

func TestResolveNonDestructiveActionOnAProtectedContextRuns(t *testing.T) {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		t.Skip("test requires non-TTY stdin (as under 'go test' / CI)")
	}
	mock := newAlertTestEnv(t)
	registerResolvingGetAlert(mock, migrationAlert())
	registerEmptySuggestions(mock)
	var posts int
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9",
		func(w http.ResponseWriter, r *http.Request) {
			posts++
			testutil.RespondJSON(w, http.StatusOK, api.AlertResolveResponseSchema{})
		})
	protectCurrentContext(t)

	if out, err := runAlertCommand(t, "alert", "resolve", "9", "--action", "apply_migration", "--yes"); err != nil {
		t.Fatalf("apply_migration on a protected context: %v\n%s", err, out)
	}
	if posts != 1 {
		t.Errorf("POSTs = %d, want 1", posts)
	}
}
