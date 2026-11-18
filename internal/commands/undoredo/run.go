package undoredo

import (
	"fmt"
	"io"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/history"
	"github.com/spf13/cobra"
)

// runFlow is the whole command body for all six leaves. Only the resolver
// above it differs.
func runFlow(cmd *cobra.Command, ctx *cmdutil.CommandContext, dir history.Direction, target history.Target) error {
	yes, _ := cmd.Flags().GetBool("yes")

	format := ""
	if cmd.Flags().Changed("output") {
		format, _ = cmd.Flags().GetString("output")
	}

	client := history.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	return client.RunFlow(ctx.Credential.AccessToken, dir, target, history.FlowOptions{
		Out:     cmd.OutOrStdout(),
		ErrOut:  cmd.ErrOrStderr(),
		Confirm: confirmer(cmd.ErrOrStderr(), yes, dir, target),
		Format:  format,
	})
}

// confirmer returns nil when --yes was passed, which is how RunFlow learns the
// action is already authorized without importing cmdutil itself.
//
// The two failure exits differ deliberately and cmdutil.Confirm owns that: a
// non-interactive stdin is BAD USAGE (exit 2) because the only fix is to change
// the command line, while declining at the prompt is a choice, not a usage
// error (exit 1).
func confirmer(out io.Writer, yes bool, dir history.Direction, target history.Target) func(string) error {
	if yes {
		return nil
	}
	return func(prompt string) error {
		return cmdutil.Confirm(out, false,
			fmt.Sprintf("confirmation required but stdin is not an interactive terminal — "+
				"pass --yes to %s the last change on %s", dir, target.Label),
			prompt)
	}
}

// addFlags registers what every leaf carries.
//
// --yes only. There is no --dry-run: the step call is not an opt-in preview,
// it is the only way the command can work safely, so it always runs. What of
// its message reaches the terminal is RunFlow's rule to state, not this one's
// -- see internal/history/flow.go. What the missing flag costs the caller is
// the separation: with no --dry-run there is no way to look without acting, so
// `qctl get history --table <t> --undo-status active --limit 1` is the
// near-substitute for "what would I be undoing?", and the resource page says
// why it is NEAR and not equal.
func addFlags(cmd *cobra.Command) {
	cmd.Flags().Bool("yes", false, "Skip the confirmation prompt")
}
