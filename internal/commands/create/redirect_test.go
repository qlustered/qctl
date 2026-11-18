// Package create_test is external because the redirect now needs the real
// command tree, and internal/commands/root imports the create package.
package create_test

import (
	"bytes"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/commands/create"
	"github.com/qlustered/qctl/internal/commands/root"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

// run executes qctl in-process and returns stdout+stderr plus the execution
// error. The root command is a package singleton, so every flag it sets must
// be reset afterwards.
func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	r := root.GetRootCmd()
	var buf bytes.Buffer
	r.SetOut(&buf)
	r.SetErr(&buf)
	r.SetArgs(args)
	t.Cleanup(func() {
		r.SetArgs(nil)
		r.SetOut(nil)
		r.SetErr(nil)
		_ = r.PersistentFlags().Set("output", "table")
	})
	err := r.Execute()
	return buf.String(), err
}

// The redirect must cover the four apply-managed resources that have no
// `apply <name>` subcommand at all — the registry-based version could only
// name resources it happened to list.
func TestCreateRedirectCoversFOnlyResources(t *testing.T) {
	for _, name := range []string{"rule", "table-rule", "rule-review-policy", "data-review-policy"} {
		out, err := run(t, "create", name)
		if err == nil {
			t.Fatalf("qctl create %s should fail with a redirect", name)
		}
		msg := err.Error() + out
		if !strings.Contains(msg, "qctl apply -f") {
			t.Errorf("create %s redirect does not mention apply -f: %s", name, msg)
		}
	}
	// A typed subcommand exists for table, so the redirect names it.
	_, err := run(t, "create", "tables") // plural guess, as the old map tolerated
	if err == nil || !strings.Contains(err.Error(), "qctl apply table -f") {
		t.Errorf("create tables redirect = %v", err)
	}
}

// managedEntry pulls "name" and "name (alias, alias)" out of the generated
// paragraph. Parsing the rendered Long rather than regenerating the list is
// the point: regenerating would compare the helper against itself.
var managedEntry = regexp.MustCompile(`([a-z0-9-]+)(?: \(([^)]+)\))?`)

// parseManagedList returns the canonical names and the aliases the Long
// advertises.
func parseManagedList(t *testing.T, long string) (names, aliases []string) {
	t.Helper()
	lines := strings.Split(long, "\n")
	body := ""
	for i, ln := range lines {
		if !strings.HasPrefix(ln, "Managed this way: ") {
			continue
		}
		body = strings.TrimPrefix(ln, "Managed this way: ")
		for _, cont := range lines[i+1:] {
			if !strings.HasPrefix(cont, "  ") {
				break
			}
			body += " " + strings.TrimSpace(cont)
		}
		break
	}
	if body == "" {
		t.Fatalf("create Long has no 'Managed this way:' paragraph; got:\n%s", long)
	}
	for _, m := range managedEntry.FindAllStringSubmatch(body, -1) {
		names = append(names, m[1])
		if m[2] != "" {
			aliases = append(aliases, strings.Split(m[2], ", ")...)
		}
	}
	return names, aliases
}

// The Long must advertise exactly the catalog's apply-managed set. Compared as
// an exact set, not by substring containment, for the reason
// TestApplyLongListsValidKinds spells out: `rule` is a substring of
// `rule-review-policy` and `table` of `table-rule`, so a dropped row would be
// masked by a containment check.
func TestCreateLongListsApplyManagedResources(t *testing.T) {
	all, err := catalog.Build(root.GetRootCmd())
	if err != nil {
		t.Fatalf("catalog.Build: %v", err)
	}
	var want []string
	for _, r := range all {
		if r.Class == catalog.ClassApply {
			want = append(want, r.Name)
		}
	}
	sort.Strings(want)

	got, _ := parseManagedList(t, create.NewCommand().Long)
	sort.Strings(got)

	if !slices.Equal(got, want) {
		t.Errorf("create Long lists %v, catalog says %v", got, want)
	}
}

// Every name and alias the Long advertises must actually redirect, and the
// resources it does not advertise must not. This is the contract a user
// exercises; TestCreateLongListsApplyManagedResources never calls the
// redirect and so cannot see the two predicates diverge.
func TestCreateLongMatchesRedirect(t *testing.T) {
	names, aliases := parseManagedList(t, create.NewCommand().Long)
	for _, name := range append(append([]string{}, names...), aliases...) {
		out, err := run(t, "create", name)
		if err == nil {
			t.Errorf("qctl create %s should fail with a redirect", name)
			continue
		}
		if msg := err.Error() + out; !strings.Contains(msg, "qctl apply") {
			t.Errorf("create %s advertised in --help but does not redirect to apply: %s", name, msg)
		}
	}
	// Control: read-only and create-class resources must not claim to be
	// apply-managed, or the redirect would be answering for everything.
	for _, name := range []string{"alert", "warning", "dry-run-job"} {
		out, err := run(t, "create", name)
		if err != nil && strings.Contains(err.Error()+out, "managed declaratively") {
			t.Errorf("create %s should not redirect to apply: %s", name, err.Error()+out)
		}
	}
}

// The redirect is a rejection of what the user typed, so it exits 2 like every
// other malformed-input path -- including the SubcommandRequired branch two
// lines below it in the same RunE.
func TestCreateRedirectExitsBadUsage(t *testing.T) {
	_, err := run(t, "create", "table")
	if err == nil {
		t.Fatal("qctl create table exited 0")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (ExitBadUsage)", got, qerrors.ExitBadUsage)
	}
	if !strings.Contains(err.Error(), "qctl apply table -f") {
		t.Errorf("redirect no longer names the apply path; got: %v", err)
	}
}

// The other branch of the same RunE: a name that is not apply-managed falls
// through to SubcommandRequired, which Task 1 typed.
func TestCreateUnknownSubcommandExitsBadUsage(t *testing.T) {
	_, err := run(t, "create", "bogusresource")
	if err == nil {
		t.Fatal("qctl create bogusresource exited 0")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (ExitBadUsage)", got, qerrors.ExitBadUsage)
	}
}

// `qctl create table --help` answers with the apply redirect on stdout and
// exit 0, where the group help would otherwise list only the imperative types.
func TestCreateHelpIsRedirected(t *testing.T) {
	for _, tc := range []struct {
		args       []string
		want       string
		wantAbsent string
	}{
		{[]string{"create", "table", "--help"}, "qctl apply table -f", "Available Commands:"},
		{[]string{"create", "tables", "-h"}, "qctl apply table -f", "Available Commands:"},
		{[]string{"create", "--help"}, "Available Commands:", "is managed declaratively"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			t.Cleanup(func() { resetHelpFlag(t, "create") })
			out, err := run(t, tc.args...)
			if err != nil {
				t.Fatalf("--help must not error: %v", err)
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("output lacks %q:\n%s", tc.want, out)
			}
			if strings.Contains(out, tc.wantAbsent) {
				t.Errorf("output carries %q:\n%s", tc.wantAbsent, out)
			}
		})
	}
}

// resetHelpFlag clears --help on the named child of the shared root, which a
// --help run leaves set for the next test in the process.
func resetHelpFlag(t *testing.T, name string) {
	t.Helper()
	cmd, _, err := root.GetRootCmd().Find([]string{name})
	if err != nil {
		t.Fatal(err)
	}
	if f := cmd.Flags().Lookup("help"); f != nil {
		_ = f.Value.Set("false")
		f.Changed = false
	}
}

// With no word to redirect, bare `qctl create` names its subcommands and
// points at apply.
func TestCreateBareNamesItsSubcommands(t *testing.T) {
	out, err := run(t, "create")
	if err == nil {
		t.Fatal("bare qctl create exited 0")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (ExitBadUsage)", got, qerrors.ExitBadUsage)
	}
	if want := "qctl create needs a subcommand: composite-dry-run-job, deliverable-run, dry-run-job\n\n" +
		"Most resources are created from a manifest: qctl apply -f <file.yaml> --yes"; err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
	if out != "" {
		t.Errorf("bare qctl create printed:\n%s", out)
	}
}
