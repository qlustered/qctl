package catalog_test

import (
	"testing"

	"github.com/qlustered/qctl/internal/catalog"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

// Every positional-argument rejection in the tree must exit 2. cobra's stock
// validators return a bare fmt.Errorf, so cmdutil.TypeArgsErrors decorates
// them once at tree-build time from root's init(). This is what keeps a
// command that installs its Args after that walk -- or a refactor that drops
// the call -- from silently regressing 86 validators to exit 1.
func TestEveryArgsValidatorExitsBadUsage(t *testing.T) {
	nodes := catalog.Walk(realRoot(t))
	if len(nodes) < 130 {
		t.Fatalf("walked only %d nodes; the tree is not fully constructed", len(nodes))
	}

	// Zero and nine arguments exercise the count validators; an empty
	// argument also exercises decorated ArbitraryArgs validators.
	probes := [][]string{
		{},
		{"a", "b", "c", "d", "e", "f", "g", "h", "i"},
		{""},
	}

	rejections := 0
	for _, n := range nodes {
		if n.Cmd.Args == nil {
			continue
		}
		for _, probe := range probes {
			err := n.Cmd.Args(n.Cmd, probe)
			if err == nil {
				continue
			}
			rejections++
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("%s: Args with %d argument(s) exits %d, want %d (ExitBadUsage)\n"+
					"    error: %v\n"+
					"    Fix: internal/commands/root.init() must call cmdutil.TypeArgsErrors(rootCmd)\n"+
					"    after every AddCommand, and nothing may assign Args after that point.",
					n.Path, len(probe), got, qerrors.ExitBadUsage, err)
			}
		}
	}
	if rejections == 0 {
		t.Fatal("no Args validator rejected any probe; the probes no longer exercise anything")
	}
}

func TestEveryArgsValidatorRejectsEmptyArguments(t *testing.T) {
	for _, n := range catalog.Walk(realRoot(t)) {
		if n.Cmd.Args == nil {
			continue
		}
		for _, args := range [][]string{{""}, {"orders", ""}} {
			if err := n.Cmd.Args(n.Cmd, args); qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
				t.Errorf("%s accepted empty arguments %q or returned the wrong exit code: %v", n.Path, args, err)
			}
		}
	}
}
