package cmdutil

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// protectedTree builds qctl -> group -> leaf for each path and returns the
// leaves by path.
func protectedTree(paths ...string) map[string]*cobra.Command {
	root := &cobra.Command{Use: "qctl"}
	leaves := map[string]*cobra.Command{}
	for _, p := range paths {
		parent := root
		for _, name := range strings.Fields(p) {
			var next *cobra.Command
			for _, c := range parent.Commands() {
				if c.Name() == name {
					next = c
				}
			}
			if next == nil {
				next = &cobra.Command{Use: name}
				parent.AddCommand(next)
			}
			parent = next
		}
		leaves[p] = parent
	}
	return leaves
}

func TestIsDestructive(t *testing.T) {
	want := map[string]bool{
		"delete rule":                    true,
		"delete":                         true,
		"kill ingestion-job":             true,
		"rows delete-quarantined":        true,
		"review delete-comment":          true,
		"comment-thread delete-comment":  true,
		"queue-message retire":           true,
		"deliverable-run unwaive":        true,
		"deliverable-run sign-off":       true,
		"deliverable-run cancel":         true,
		"deliverable-run mark-delivered": true,
		"data-review-group cancel":       true,
		"data-review-group assign":       false,
		"data-review close":              true,
		"data-review assign":             false,
		"review close":                   true,
		"review reopen":                  false,
		"deliverable-run waive":          false,
		"submission-status waive":        false,
		"submission-status unwaive":      false,
		"rows set-cell":                  false,
		"undelete table":                 false,
		"queue-message retry":            false,
		"get rules":                      false,
		"config delete-context":          false,
	}
	paths := make([]string, 0, len(want))
	for p := range want {
		paths = append(paths, p)
	}
	leaves := protectedTree(paths...)
	for p, w := range want {
		if got := IsDestructive(leaves[p]); got != w {
			t.Errorf("IsDestructive(qctl %s) = %v, want %v", p, got, w)
		}
	}
}

func TestIsDestructiveAction(t *testing.T) {
	for action, want := range map[string]bool{
		"discard_migration":            true,
		"discard_cell_override":        true,
		"trash_file":                   true,
		"trash_alert_associated_files": true,
		"reject_held_file":             true,
		"release_held_file":            false,
		"reject_held_email_file":       true,
		"use_held_email_file":          false,
		"apply_migration":              false,
		"mark_file_unavailable":        false,
		"ignore_issue_for_value":       false,
	} {
		if got := IsDestructiveAction(action); got != want {
			t.Errorf("IsDestructiveAction(%q) = %v, want %v", action, got, want)
		}
	}
}

// An action whose name says it removes something, or that ignores files, has
// to be classified: a spec regen that adds one fails here until it is.
func TestEveryActionThatLooksDestructiveIsClassified(t *testing.T) {
	looksDestructive := func(a string) bool {
		for _, word := range []string{"delete", "discard", "drop", "purge", "remove", "retire", "kill", "reject"} {
			if strings.Contains(a, word) {
				return true
			}
		}
		return strings.HasPrefix(a, "ignore_") && strings.Contains(a, "file")
	}
	for _, enum := range []string{"AlertAction", "WarningAction"} {
		actions := api.StringEnumValues[enum]
		if len(actions) == 0 {
			t.Fatalf("api.StringEnumValues[%q] is empty; the enum moved", enum)
		}
		for _, a := range actions {
			if looksDestructive(a) && !IsDestructiveAction(a) {
				t.Errorf("%s %s looks destructive but IsDestructiveAction does not classify it", enum, a)
			}
		}
	}
}

// setupProtectedContexts writes contexts dev (allowlisted) and prod (no
// protected key), with current as the stored current context.
func setupProtectedContexts(t *testing.T, current string) {
	t.Helper()
	env := testutil.NewTestEnv(t)
	env.SetupMultipleContexts(map[string]*config.Context{
		"dev":  {Server: "https://dev.example.com", Protected: testutil.BoolPtr(false)},
		"prod": {Server: "https://prod.example.com"},
	}, current)
	t.Cleanup(func() { config.SetContextOverride("") })
}

func deleteRuleCommand() *cobra.Command {
	cmd := protectedTree("delete rule")["delete rule"]
	cmd.SetErr(&bytes.Buffer{})
	return cmd
}

func requireNonTTYStdin(t *testing.T) {
	t.Helper()
	if term.IsTerminal(int(os.Stdin.Fd())) {
		t.Skip("test requires non-TTY stdin (as under 'go test' / CI)")
	}
}

// prod carries no protected key: every context is protected by default.
func TestGuardRefusesADestructiveCommandOnAProtectedContext(t *testing.T) {
	requireNonTTYStdin(t)
	setupProtectedContexts(t, "prod")

	err := GuardProtectedContext(deleteRuleCommand())
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Fatalf("exit code = %d, want %d (err = %v)", code, qerrors.ExitBadUsage, err)
	}
	if !strings.Contains(err.Error(), "--context prod") {
		t.Errorf("refusal does not name the flag that satisfies it: %v", err)
	}
	if !strings.Contains(err.Error(), "qctl config set-context prod --protected=false") {
		t.Errorf("refusal does not name the allowlist command: %v", err)
	}
}

func TestGuardPassesWhenContextIsNamed(t *testing.T) {
	requireNonTTYStdin(t)
	setupProtectedContexts(t, "prod")
	config.SetContextOverride("prod")

	if err := GuardProtectedContext(deleteRuleCommand()); err != nil {
		t.Fatalf("GuardProtectedContext with --context prod = %v, want nil", err)
	}
}

func TestGuardFollowsTheNamedContext(t *testing.T) {
	requireNonTTYStdin(t)
	// --context dev from a stored prod: the command runs against dev, which is
	// allowlisted.
	setupProtectedContexts(t, "prod")
	config.SetContextOverride("dev")
	if err := GuardProtectedContext(deleteRuleCommand()); err != nil {
		t.Fatalf("GuardProtectedContext with --context dev = %v, want nil", err)
	}
}

func TestGuardIgnoresUnprotectedContextsAndReads(t *testing.T) {
	requireNonTTYStdin(t)
	setupProtectedContexts(t, "dev")
	if err := GuardProtectedContext(deleteRuleCommand()); err != nil {
		t.Errorf("delete on allowlisted dev = %v, want nil", err)
	}

	setupProtectedContexts(t, "prod")
	get := protectedTree("get rules")["get rules"]
	if err := GuardProtectedContext(get); err != nil {
		t.Errorf("get on protected prod = %v, want nil", err)
	}
}

// An allowlist covers its context's own server only: --server or QCTL_SERVER
// pointing elsewhere is a destructive command against some other server.
func TestGuardRefusesAServerRedirectFromAnAllowlistedContext(t *testing.T) {
	requireNonTTYStdin(t)
	setupProtectedContexts(t, "dev")

	t.Setenv("QCTL_SERVER", "https://prod.example.com")
	err := GuardProtectedContext(deleteRuleCommand())
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("QCTL_SERVER redirect: exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
	if err != nil && strings.Contains(err.Error(), "--protected=false") {
		t.Errorf("a redirect refusal offers the allowlist, which cannot satisfy it: %v", err)
	}

	t.Setenv("QCTL_SERVER", "")
	cmd := deleteRuleCommand()
	cmd.Flags().String("server", "", "")
	if err := cmd.Flags().Set("server", "https://prod.example.com"); err != nil {
		t.Fatal(err)
	}
	if code := qerrors.GetExitCode(GuardProtectedContext(cmd)); code != qerrors.ExitBadUsage {
		t.Errorf("--server redirect: exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
}

// --context is the confirmation only for the server it selects: naming dev
// while QCTL_SERVER still sends the request to prod does not satisfy it.
func TestGuardNamedContextDoesNotCoverARedirect(t *testing.T) {
	requireNonTTYStdin(t)
	setupProtectedContexts(t, "prod")
	t.Setenv("QCTL_SERVER", "https://prod.example.com")

	config.SetContextOverride("dev")
	if code := qerrors.GetExitCode(GuardProtectedContext(deleteRuleCommand())); code != qerrors.ExitBadUsage {
		t.Errorf("--context dev with prod's server: exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}

	config.SetContextOverride("prod")
	if err := GuardProtectedContext(deleteRuleCommand()); err != nil {
		t.Errorf("--context prod with prod's server = %v, want nil", err)
	}
}

func TestGuardTreatsAnEquivalentServerAsTheContextsOwn(t *testing.T) {
	requireNonTTYStdin(t)
	setupProtectedContexts(t, "dev")
	t.Setenv("QCTL_SERVER", "HTTPS://dev.example.com:443/")
	if err := GuardProtectedContext(deleteRuleCommand()); err != nil {
		t.Errorf("QCTL_SERVER naming dev's own server = %v, want nil", err)
	}
}

func TestGuardRefusesAServerNoContextSelects(t *testing.T) {
	requireNonTTYStdin(t)
	setupProtectedContexts(t, "")
	t.Setenv("QCTL_SERVER", "https://dev.example.com")
	if code := qerrors.GetExitCode(GuardProtectedContext(deleteRuleCommand())); code != qerrors.ExitBadUsage {
		t.Errorf("no current context: exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
}

func TestGuardYesDoesNotSatisfyIt(t *testing.T) {
	requireNonTTYStdin(t)
	setupProtectedContexts(t, "prod")
	cmd := deleteRuleCommand()
	cmd.Flags().Bool("yes", false, "")
	if err := cmd.Flags().Set("yes", "true"); err != nil {
		t.Fatal(err)
	}
	if code := qerrors.GetExitCode(GuardProtectedContext(cmd)); code != qerrors.ExitBadUsage {
		t.Fatalf("exit code with --yes = %d, want %d", code, qerrors.ExitBadUsage)
	}
}

func TestGuardAsksOnAnInteractiveStdin(t *testing.T) {
	setupProtectedContexts(t, "prod")
	t.Cleanup(ResetReader)

	SetReader(strings.NewReader("y\n"))
	cmd := deleteRuleCommand()
	var prompt bytes.Buffer
	cmd.SetErr(&prompt)
	if err := GuardProtectedContext(cmd); err != nil {
		t.Errorf("answered y: %v, want nil", err)
	}
	if !strings.Contains(prompt.String(), "qctl config set-context prod --protected=false") {
		t.Errorf("prompt does not name the allowlist command:\n%s", prompt.String())
	}

	SetReader(strings.NewReader("n\n"))
	err := GuardProtectedContext(deleteRuleCommand())
	if !errors.Is(err, ErrDeclined) {
		t.Errorf("answered n: %v, want ErrDeclined", err)
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitGenericError {
		t.Errorf("declined exit code = %d, want %d", code, qerrors.ExitGenericError)
	}
}
