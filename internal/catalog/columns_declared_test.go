package catalog_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
)

// TestEveryLocalColumnsFlagIsDeclared fails for a command that registers its
// own --columns flag without declaring its columns with
// cmdutil.DeclareColumns. Root's PersistentPreRunE checks --columns only
// against a declaration, so such a command reports an unknown name after
// Bootstrap and the request, and never under -o json.
//
// It sees only commands with a local --columns flag. A command that hands
// root's persistent --columns to a printer without registering its own is
// invisible here.
func TestEveryLocalColumnsFlagIsDeclared(t *testing.T) {
	root := realRoot(t)
	persistent := root.PersistentFlags().Lookup("columns")

	var checked int
	var offenders []string
	for _, n := range catalog.Walk(root) {
		f := n.Cmd.LocalFlags().Lookup("columns")
		if f == nil || f == persistent {
			continue
		}
		checked++
		if len(cmdutil.DeclaredColumns(n.Cmd)) == 0 {
			offenders = append(offenders, n.Path)
		}
	}
	// get tables registers its own --columns; finding none means the lookup
	// above is broken, not that the tree is clean.
	if checked == 0 {
		t.Fatal("found no command with a local --columns flag")
	}
	sort.Strings(offenders)
	if len(offenders) > 0 {
		t.Errorf("these commands register --columns but declare no columns:\n  %s\n"+
			"Build the flag's Available list with cmdutil.DeclareColumns(cmd, <display samples>).",
			strings.Join(offenders, "\n  "))
	}
}
