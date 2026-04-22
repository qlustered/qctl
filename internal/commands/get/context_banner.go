package get

import (
	"fmt"
	"os"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// printContextBanner writes "Context: X | Org: Y" to stderr when format is
// table AND stdout is a TTY. No-op otherwise (piped output stays clean).
func printContextBanner(cmd *cobra.Command, ctx *cmdutil.CommandContext) {
	if !isTableFormat(cmd) || !stdoutIsTTY(cmd) {
		return
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "Context: %s | Org: %s\n\n",
		contextLabel(ctx), orgLabel(ctx))
}

// printEmptyResult writes the "No X found..." message to stderr when format
// is table. Always visible; stderr never pollutes a pipe.
// Returns true if the caller should skip printing (i.e., empty + table).
func printEmptyResult(cmd *cobra.Command, ctx *cmdutil.CommandContext, resource string) bool {
	if !isTableFormat(cmd) {
		return false
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "No %s found in org %q (context: %q).\n",
		resource, orgLabel(ctx), contextLabel(ctx))
	return true
}

func isTableFormat(cmd *cobra.Command) bool {
	f, _ := cmd.Flags().GetString("output")
	return f == "" || f == "table"
}

func stdoutIsTTY(cmd *cobra.Command) bool {
	f, ok := cmd.OutOrStdout().(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

func contextLabel(ctx *cmdutil.CommandContext) string {
	if ctx.Config != nil && ctx.Config.CurrentContext != "" {
		return ctx.Config.CurrentContext
	}
	return "(none)"
}

func orgLabel(ctx *cmdutil.CommandContext) string {
	if ctx.OrganizationName != "" {
		return ctx.OrganizationName
	}
	return ctx.OrganizationID
}
