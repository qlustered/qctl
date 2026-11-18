// Package reveal implements the qctl reveal command group: the unmasked value
// of one personal-data cell, or of one alert's or warning's message.
package reveal

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/pii_reveals"
	"github.com/spf13/cobra"
)

// NewCommand creates the reveal parent command.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reveal",
		Short: "Show one masked personal-data value unmasked",
		Long: `Show the unmasked value of one masked personal-data value: a table cell, or
an alert's or warning's message.

  cell      one cell of a personal-data column
  alert     one alert's message
  warning   one warning's message

Every reveal is recorded with who asked, what they revealed and when. The
server allows 20 reveals per user per table in a rolling hour, counted across
qctl and the web app; past that it refuses (exit 1) and says when to try
again. An admin's temporary grant (qctl pii-grant create) lifts the masking
on the whole table instead.

Revealing needs the pii:reveal permission, which the admin, ops and read_only
roles hold. The member, editor and external roles already see personal data
unmasked; the agent role cannot reveal. Anyone without it gets a 403 (exit 4).`,
		Annotations: catalog.Parent(),
		RunE:        cmdutil.SubcommandRequired,
	}
	cmd.AddCommand(newCellCommand(), newIssueCommand(issueAlert), newIssueCommand(issueWarning))
	return cmd
}

// revealFormats are the -o values every reveal verb honors.
var revealFormats = []string{"table", "json", "yaml"}

// annotations is the reveal verbs' catalog annotation, with argKinds as the
// kind of each positional slot.
func annotations(argKinds ...string) map[string]string {
	return cmdutil.WithArgKinds(cmdutil.WithFormats(catalog.For("pii-reveal"), revealFormats...), argKinds...)
}

// send reveals target on tableID and prints it: -o json|yaml the server's
// answer, otherwise render(value) on stdout and the audit line on stderr.
func send(cmd *cobra.Command, ctx *cmdutil.CommandContext, tableID int, target any, what string, render func(string) string) error {
	result, err := pii_reveals.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity).
		Reveal(ctx.Credential.AccessToken, tableID, target)
	if err != nil {
		return explainLimit(err, tableID)
	}
	out := cmd.OutOrStdout()
	if format, _ := cmd.Flags().GetString("output"); format == "json" || format == "yaml" {
		return output.WriteRaw(out, format, result)
	}
	if result.Value == nil {
		if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "%s is null; reveal recorded as event %d\n", what, result.EventID); err != nil {
			return err
		}
		return nil
	}
	if _, err := fmt.Fprintln(out, render(*result.Value)); err != nil {
		return err
	}
	_, err = fmt.Fprintf(cmd.ErrOrStderr(), "Reveal recorded as event %d\n", result.EventID)
	return err
}

// explainLimit adds, to the reveal limit's 429, when the next reveal frees up
// and how an admin lifts the limit. Any other error is returned unchanged.
func explainLimit(err error, tableID int) error {
	var throttle *apierror.ThrottleError
	if !errors.As(err, &throttle) {
		return err
	}
	when := "Try again later"
	if throttle.RetryAfter > 0 {
		minutes := int(math.Ceil(throttle.RetryAfter.Minutes()))
		when = fmt.Sprintf("Try again in %d minute%s", minutes, plural(minutes))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s, or ask an admin for temporary unmasked access to the table:\n", when)
	fmt.Fprintf(&b, "  qctl pii-grant create --table-id %d --to <your-email> --reason <why>", tableID)
	return fmt.Errorf("%w\n\n%s", err, b.String())
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
