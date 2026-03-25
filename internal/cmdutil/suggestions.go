package cmdutil

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// SubcommandRequired returns a RunE function for parent commands (commands that
// only group subcommands). When the user types an unknown subcommand, it suggests
// the closest match using Levenshtein distance (like kubectl).
//
// Usage:
//
//	cmd := &cobra.Command{
//	    Use:   "get",
//	    Short: "Display one or many resources",
//	    RunE:  cmdutil.SubcommandRequired,
//	}
func SubcommandRequired(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return cmd.Help()
	}

	unknown := args[0]
	suggestions := cmd.SuggestionsFor(unknown)

	var msg strings.Builder
	fmt.Fprintf(&msg, "unknown command %q for %q", unknown, cmd.CommandPath())
	if len(suggestions) > 0 {
		msg.WriteString("\n\nDid you mean this?\n")
		for _, s := range suggestions {
			fmt.Fprintf(&msg, "\t%s\n", s)
		}
	}
	msg.WriteString(fmt.Sprintf("\nRun '%s --help' for usage.", cmd.CommandPath()))

	return fmt.Errorf("%s", msg.String())
}
