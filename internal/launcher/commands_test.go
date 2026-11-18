package launcher_test

import (
	"testing"

	"github.com/qlustered/qctl/internal/commands/root"
	"github.com/qlustered/qctl/internal/launcher"
	"github.com/spf13/cobra"
)

// TestEveryTopLevelCommandIsClassified fails when a top-level command or alias
// is added without an entry in the launcher's table, or an entry outlives its
// command. A new command must be classified deliberately: local commands run
// whatever qctl the user invoked, whatever the server's version.
func TestEveryTopLevelCommandIsClassified(t *testing.T) {
	table := launcher.LocalCommands()
	inTree := map[string]bool{
		// cobra adds these during Execute, so the tree walked here lacks them.
		cobra.ShellCompRequestCmd:       true,
		cobra.ShellCompNoDescRequestCmd: true,
	}
	rootCmd := root.GetRootCmd()
	for _, c := range rootCmd.Commands() {
		names := append([]string{c.Name()}, c.Aliases...)
		local, ok := table[c.Name()]
		for _, name := range names {
			inTree[name] = true
			got, found := table[name]
			switch {
			case !found:
				t.Errorf("top-level command %q is not in the launcher's table: classify it as local "+
					"(never talks to atlas) or checked", name)
			case ok && got != local:
				t.Errorf("%q is classified local=%v, but %q, the same command, local=%v", name, got, c.Name(), local)
			}
		}
	}
	for name := range table {
		if !inTree[name] {
			t.Errorf("the launcher's table classifies %q, which is not a top-level command", name)
		}
	}
}
