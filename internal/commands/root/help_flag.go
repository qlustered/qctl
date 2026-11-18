package root

import (
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/spf13/cobra"
)

// helpFlagErr is the rejection rejectUnknownUnderHelp recorded during the
// current executeC, or nil.
var helpFlagErr error

// rejectUnknownUnderHelp wraps help. When unknownUnderHelpFlag holds, it
// records SubcommandRequired's error for executeC and prints nothing;
// otherwise it calls help.
func rejectUnknownUnderHelp(help func(*cobra.Command, []string)) func(*cobra.Command, []string) {
	return func(cmd *cobra.Command, args []string) {
		if unknownUnderHelpFlag(cmd, args) {
			helpFlagErr = cmdutil.SubcommandRequired(cmd, cmd.Flags().Args())
			return
		}
		help(cmd, args)
	}
}

// unknownUnderHelpFlag reports whether cmd is a dispatch parent whose help
// flag is set and that was left a positional tail.
//
// args must be non-empty too. ExecuteC passes HelpFunc the command line, and
// Help() passes an empty slice, so `qctl help get` and a bare parent's
// SubcommandRequired never qualify, even when an earlier in-process run left
// the help flag and a tail on the command.
func unknownUnderHelpFlag(cmd *cobra.Command, args []string) bool {
	if len(args) == 0 || cmd.Annotations[catalog.AnnotationResource] != catalog.Dispatch {
		return false
	}
	set, err := cmd.Flags().GetBool("help")
	if err != nil || !set || !cmd.Flags().Changed("help") {
		return false
	}
	return len(cmd.Flags().Args()) > 0
}

// executeC runs the tree. It returns the error rejectUnknownUnderHelp
// recorded when cobra itself returned nil.
func executeC() (*cobra.Command, error) {
	helpFlagErr = nil
	cmd, err := rootCmd.ExecuteC()
	if err == nil {
		err = helpFlagErr
	}
	helpFlagErr = nil
	return cmd, err
}
