package releases_test

import (
	"testing"

	"github.com/qlustered/qctl/internal/commands/root"
	"github.com/qlustered/qctl/internal/releases"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// TestParseArgsKnowsRootsFlags fails when a root persistent flag is added,
// removed or changes whether it takes a value, or when any command declares a
// flag with a root flag's name or shorthand that disagrees about taking a
// value: ParseArgs would then misread the command words or --server.
func TestParseArgsKnowsRootsFlags(t *testing.T) {
	long, short := releases.RootFlagKinds()
	rootCmd := root.GetRootCmd()
	rootCmd.InitDefaultHelpFlag()
	rootCmd.InitDefaultVersionFlag()

	seen := map[string]bool{}
	visit := func(f *pflag.Flag) {
		seen[f.Name] = true
		takes := f.NoOptDefVal == ""
		want, ok := long[f.Name]
		if !ok {
			t.Errorf("root flag --%s is missing from ParseArgs' table (takes a value: %v)", f.Name, takes)
		} else if want != takes {
			t.Errorf("--%s takes a value: %v, ParseArgs' table says %v", f.Name, takes, want)
		}
		if f.Shorthand != "" {
			if want, ok := short[f.Shorthand[0]]; !ok || want != takes {
				t.Errorf("-%s: table entry %v (present %v), flag takes a value: %v", f.Shorthand, want, ok, takes)
			}
		}
	}
	rootCmd.PersistentFlags().VisitAll(visit)
	rootCmd.Flags().VisitAll(visit)
	for name := range long {
		if !seen[name] {
			t.Errorf("ParseArgs' table lists --%s, which root does not have", name)
		}
	}

	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		c.LocalFlags().VisitAll(func(f *pflag.Flag) {
			takes := f.NoOptDefVal == ""
			if want, ok := long[f.Name]; ok && want != takes {
				t.Errorf("%s --%s takes a value: %v, unlike root's", c.CommandPath(), f.Name, takes)
			}
			if f.Shorthand != "" {
				if want, ok := short[f.Shorthand[0]]; ok && want != takes {
					t.Errorf("%s -%s takes a value: %v, unlike root's", c.CommandPath(), f.Shorthand, takes)
				}
			}
		})
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(rootCmd)
}
