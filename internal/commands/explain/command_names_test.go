package explain_test

import (
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/commands/root"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestExplainResolvesAPlural(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	out, _, err := runExplain(t, "explain", "tables")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitSuccess {
		t.Fatalf("qctl explain tables: exit %d, want 0: %v", code, err)
	}
	if !strings.HasPrefix(out, "RESOURCE: table\n") {
		t.Errorf("qctl explain tables did not print the table page:\n%s", out)
	}

	// The schema route resolves the same way. table-rule's schema ships in
	// the binary, so this needs no server.
	want, _, err := runExplain(t, "explain", "table-rule", "-o", "json")
	if err != nil {
		t.Fatalf("qctl explain table-rule -o json: %v", err)
	}
	got, _, err := runExplain(t, "explain", "table-rules", "-o", "json")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitSuccess {
		t.Fatalf("qctl explain table-rules -o json: exit %d, want 0: %v", code, err)
	}
	if got != want {
		t.Errorf("qctl explain table-rules -o json differs from table-rule's:\n%s", got)
	}
}

func TestExplainCommandPrintsItsHelp(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	want, _, err := runExplain(t, "apply", "--help")
	if err != nil {
		t.Fatalf("qctl apply --help: %v", err)
	}
	got, _, err := runExplain(t, "explain", "apply")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitSuccess {
		t.Fatalf("qctl explain apply: exit %d, want 0: %v", code, err)
	}
	if got != want {
		t.Errorf("qctl explain apply must print what qctl apply --help prints\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestExplainCommandHasNoSchema(t *testing.T) {
	for _, args := range [][]string{
		{"explain", "apply", "-o", "json"},
		{"explain", "apply", "-o", "yaml"},
		{"explain", "apply.foo"},
	} {
		t.Run(strings.Join(args[1:], " "), func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()

			out, _, err := runExplain(t, args...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("qctl %s: exit %d, want %d: %v", strings.Join(args, " "), code, qerrors.ExitBadUsage, err)
			}
			if !strings.Contains(err.Error(), "'qctl apply --help'") {
				t.Errorf("error should point at the command's help, got: %v", err)
			}
			if out != "" {
				t.Errorf("nothing belongs on stdout, got:\n%s", out)
			}
		})
	}
}

func TestExplainResourceWinsOverCommand(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	out, _, err := runExplain(t, "explain", "alert")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitSuccess {
		t.Fatalf("qctl explain alert: exit %d, want 0: %v", code, err)
	}
	if !strings.HasPrefix(out, "RESOURCE: alert\n") {
		t.Errorf("qctl explain alert must explain the alert resource, not the alert command:\n%s", out)
	}
}

// TestExplainEveryTopLevelCommand checks every child of root: one whose name
// is also a resource's explains that resource, and any other prints its help.
func TestExplainEveryTopLevelCommand(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	r := root.GetRootCmd()
	var asResource, asCommand int
	for _, c := range append(r.Commands()[:0:0], r.Commands()...) {
		name := c.Name()
		out, _, err := runExplain(t, "explain", name)
		if code := qerrors.GetExitCode(err); code != qerrors.ExitSuccess {
			t.Errorf("qctl explain %s: exit %d, want 0: %v", name, code, err)
			continue
		}
		if res, ok := catalog.ResourceNamed(r, name); ok {
			asResource++
			if !strings.HasPrefix(out, "RESOURCE: "+res.Name+"\n") {
				t.Errorf("qctl explain %s must explain the %s resource:\n%s", name, res.Name, out)
			}
			continue
		}
		asCommand++
		help, _, err := runExplain(t, name, "--help")
		if err != nil {
			t.Errorf("qctl %s --help: %v", name, err)
			continue
		}
		if out != help {
			t.Errorf("qctl explain %s must print what qctl %s --help prints\ngot:\n%s\nwant:\n%s", name, name, out, help)
		}
	}
	if asResource == 0 || asCommand == 0 {
		t.Fatalf("expected both kinds of top-level command; got %d resources and %d commands", asResource, asCommand)
	}
}

func TestExplainUnknownNameStillErrors(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	out, _, err := runExplain(t, "explain", "bogus")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Fatalf("qctl explain bogus: exit %d, want %d: %v", code, qerrors.ExitBadUsage, err)
	}
	if !strings.Contains(err.Error(), `unknown resource "bogus"`) || !strings.Contains(err.Error(), "api-resources") {
		t.Errorf("unknown name should keep the unknown-resource redirect, got: %v", err)
	}
	if out != "" {
		t.Errorf("nothing belongs on stdout, got:\n%s", out)
	}
}
