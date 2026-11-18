package cmdutil

import "github.com/spf13/cobra"

// InheritedHelp returns the HelpFunc group would have without its own: its
// parent's at the time of the call, or cobra's default when it has no parent.
func InheritedHelp(group *cobra.Command) func(*cobra.Command, []string) {
	if parent := group.Parent(); parent != nil {
		return parent.HelpFunc()
	}
	return (&cobra.Command{}).HelpFunc()
}
