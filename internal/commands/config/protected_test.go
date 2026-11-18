package config

import (
	"os"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"golang.org/x/term"
)

// setupProtectedStaging adds a protected context "staging" next to setupAcme's
// current "dev".
func setupProtectedStaging(t *testing.T) {
	t.Helper()
	setupAcme(t, "")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Contexts["staging"] = &config.Context{Server: "https://staging.example.com", Protected: testutil.BoolPtr(true)}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
}

func loadContext(t *testing.T, name string) *config.Context {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	return cfg.Contexts[name]
}

func requireNonTTYStdin(t *testing.T) {
	t.Helper()
	if term.IsTerminal(int(os.Stdin.Fd())) {
		t.Skip("test requires non-TTY stdin (as under 'go test' / CI)")
	}
}

func TestSetContextProtectedMarksTheContext(t *testing.T) {
	setupAcme(t, "")
	if _, _, err := runContextCommand(t, "config", "set-context", "dev", "--protected"); err != nil {
		t.Fatalf("set-context --protected: %v", err)
	}
	if !loadContext(t, "dev").IsProtected() {
		t.Fatal("dev is not protected after set-context --protected")
	}

	stdout, _, err := runContextCommand(t, "config", "current-context")
	if err != nil {
		t.Fatalf("current-context: %v", err)
	}
	if !strings.Contains(stdout, "  Protected: yes") {
		t.Errorf("current-context does not show protection:\n%s", stdout)
	}
}

func TestRemovingProtectionNeedsATerminal(t *testing.T) {
	requireNonTTYStdin(t)
	setupProtectedStaging(t)

	_, _, err := runContextCommand(t, "config", "set-context", "staging", "--protected=false", "--yes")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Fatalf("exit code = %d, want %d (err = %v)", code, qerrors.ExitBadUsage, err)
	}
	if !loadContext(t, "staging").IsProtected() {
		t.Fatal("staging lost its protection without a terminal")
	}
}

func TestRemovingProtectionAtATerminal(t *testing.T) {
	setupProtectedStaging(t)
	t.Cleanup(cmdutil.ResetReader)

	cmdutil.SetReader(strings.NewReader("n\n"))
	_, _, err := runContextCommand(t, "config", "set-context", "staging", "--protected=false")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitGenericError {
		t.Fatalf("declined: exit code = %d, want %d (err = %v)", code, qerrors.ExitGenericError, err)
	}
	if !loadContext(t, "staging").IsProtected() {
		t.Fatal("staging lost its protection after a decline")
	}

	cmdutil.SetReader(strings.NewReader("y\n"))
	if _, _, err := runContextCommand(t, "config", "set-context", "staging", "--protected=false"); err != nil {
		t.Fatalf("confirmed: %v", err)
	}
	if loadContext(t, "staging").IsProtected() {
		t.Fatal("staging is still protected after a confirmed --protected=false")
	}
}

// Deleting a protected context needs no terminal: one recreated under the same
// name is protected again.
func TestDeletingAProtectedContextIsAnOrdinaryDelete(t *testing.T) {
	requireNonTTYStdin(t)
	setupProtectedStaging(t)

	if _, _, err := runContextCommand(t, "config", "delete-context", "staging", "--yes"); err != nil {
		t.Fatalf("delete-context --yes: %v", err)
	}
	if loadContext(t, "staging") != nil {
		t.Fatal("staging survived delete-context --yes")
	}
}

// Under --context, the stored current context is refused before any prompt.
func TestDeletingTheStoredCurrentContextUnderAnOverrideIsRefusedFirst(t *testing.T) {
	requireNonTTYStdin(t)
	setupProtectedStaging(t)
	config.SetContextOverride("staging")
	t.Cleanup(func() { config.SetContextOverride("") })

	_, _, err := runContextCommand(t, "config", "delete-context", "dev")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Fatalf("exit code = %d, want %d from the current-context refusal (err = %v)", code, qerrors.ExitBadUsage, err)
	}
	if err.Error() != `cannot delete current context "dev"` {
		t.Fatalf("expected the current-context refusal before prompting, got: %v", err)
	}
	config.SetContextOverride("")
	if loadContext(t, "dev") == nil {
		t.Fatal("the stored current context was deleted")
	}
}
