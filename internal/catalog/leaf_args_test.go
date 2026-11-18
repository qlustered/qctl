package catalog_test

import (
	"testing"

	"github.com/qlustered/qctl/internal/catalog"
)

// TestEveryRunnableLeafDeclaresArgs fails for any runnable command with no
// subcommands whose Args is nil. cobra treats a nil Args on such a command as
// ArbitraryArgs, so `qctl get tables orders` would list every table and drop
// "orders" without a word.
//
// help is the one exclusion: it transcribes cobra's stock help command, which
// sets no Args, and its arguments are a command path of any length.
func TestEveryRunnableLeafDeclaresArgs(t *testing.T) {
	nodes := catalog.Walk(realRoot(t))
	if len(nodes) < 130 {
		t.Fatalf("walked only %d nodes; the tree is not fully constructed", len(nodes))
	}

	leaves := 0
	for _, n := range nodes {
		c := n.Cmd
		if !c.Runnable() || c.HasSubCommands() {
			continue
		}
		leaves++
		if n.Path == "qctl help" {
			continue
		}
		if c.Args == nil {
			t.Errorf("%s declares no Args, so cobra accepts and ignores any stray "+
				"positional argument.\n"+
				"    Fix: set Args on the command to the validator matching what RunE "+
				"reads -- cobra.NoArgs when it never reads args, cobra.ExactArgs(n) "+
				"or cobra.MaximumNArgs(n) for a fixed count, cobra.MinimumNArgs(n) or "+
				"cobra.ArbitraryArgs for a variadic list.", n.Path)
		}
	}
	if leaves == 0 {
		t.Fatal("found no runnable leaf command; the walk no longer exercises anything")
	}
}
