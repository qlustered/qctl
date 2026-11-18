package warning

import (
	"fmt"
	"io"
	"strconv"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/user_submitted"
	"github.com/qlustered/qctl/internal/warnings"
	"github.com/spf13/cobra"
)

func newRetractCommand() *cobra.Command {
	var rows []int

	cmd := &cobra.Command{
		Use:   "retract <warning-id>",
		Short: "Withdraw a user-submitted warning from rows you flagged in error",
		Long: `Withdraw a user-submitted warning from the rows you name.

Retract is NOT resolve. It means "I flagged the wrong cell", not "this is
fixed" — 'qctl warning resolve' is the other thing, and it is what a warning a
rule raised needs. Only a warning a human submitted can be retracted; anything
else is refused before the call.

The warning id is the id 'qctl get warnings' prints. The row ids are the rows
carrying it; both are also in the quarantined-row payload:

  qctl get data orders --is-bad-data --warning-item-id 8817
  qctl get data orders --is-bad-data -o json      # warning_item_ids_ per row

Each raw entry is "<warning-id>-<field>", so a warning on row 1042's amount
shows as "8817-amount" — which the rendered page spells "[W8817]" on that cell.

A retraction that empties the aggregate auto-resolves it, which is reported.

CAVEAT: "retracted" describes the RECORD, not the row. The token removal only
reaches rows still in quarantine, so a warning whose row has already gone
clean keeps its token and STAYS ON DISPLAY even though this command succeeded.
The record is retracted and will not survive the next reprocess, but nothing
revisits clean rows to take it off in the meantime.

This is attributed but permissive: the alert:resolve permission lets any
resolver erase someone else's objection. Hence the confirmation prompt.

There is no way to edit the comment: it is part of the warning's identity. To
reword it, retract and 'qctl warning submit' again.

Examples:
  qctl warning retract 8817 --row 1042
  qctl warning retract 8817 --row 1042 --row 1043 --yes

-o json|yaml prints the server's {issue_resolved, results}, one result per
row, and moves the prose to stderr; -o name prints warning/<id>.`,
		Annotations: cmdutil.WithFormats(catalog.For("warning"), "table", "json", "yaml", "name"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Decided locally, so it runs before Bootstrap and the warning
			// read: a typo must cost nothing.
			warningID, err := strconv.Atoi(args[0])
			if err != nil {
				return qerrors.NewBadUsagef("invalid warning id %q: expected a number", args[0])
			}
			body, err := user_submitted.RetractRequest{IssueItemID: warningID, Rows: rows}.Build()
			if err != nil {
				return err
			}

			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}
			client := warnings.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			token := ctx.Credential.AccessToken

			// The read is what makes the positional form work: the warning
			// names its own table, so there is no --table to get wrong. It
			// also catches the one mistake the wire cannot report — a rule
			// warning comes back as every row not_found, which reads as
			// "wrong row" rather than "wrong verb".
			warning, err := client.GetWarning(token, warningID)
			if err != nil {
				return fmt.Errorf("failed to get warning %d: %w", warningID, err)
			}
			if warning.IssueType != api.UserSubmitted {
				return qerrors.NewBadUsagef(
					"warning %d is a %s warning raised by a table rule, not one a human submitted — "+
						"there is nothing to retract.\n"+
						"Act on it with: qctl warning resolve %d --action <action>\n"+
						"List the actions it offers with: qctl describe warning %d\n"+
						"Reclassify or silence FUTURE findings by patching the table rule: qctl explain table-rule",
					warningID, warning.IssueType, warningID, warningID)
			}

			yes, _ := cmd.Flags().GetBool("yes")
			if err := confirmRetract(cmd.ErrOrStderr(), warning, len(rows), yes); err != nil {
				return err
			}

			result, err := client.RetractUserIssues(token, warning.DatasetID, body)
			if err != nil {
				return err
			}
			w := cmdutil.NewResultWriter(cmd)
			if err := user_submitted.ReportRetract(w.Human, cmd.ErrOrStderr(),
				user_submitted.KindWarning, warningID, result); err != nil {
				return err
			}
			return w.Write(result, warningName(warningID))
		},
	}

	cmd.Flags().IntSliceVar(&rows, "row", nil,
		"Row id to withdraw the flag from (repeatable; required; max 500; must be unique)")
	cmd.Flags().Bool("yes", false, "Skip the confirmation prompt (for scripted/non-interactive use)")
	return cmd
}

// confirmRetract shows the objection about to be erased and gets agreement.
// The message is quoted because the caller may not be the person who wrote it.
//
// 'warning submit' deliberately has no prompt — raising an advisory note holds
// nothing — but retract erases someone else's objection, so it does.
func confirmRetract(out io.Writer, warning *warnings.WarningFull, rowCount int, yes bool) error {
	return cmdutil.Confirm(out, yes,
		fmt.Sprintf(
			"confirmation required but stdin is not an interactive terminal — pass --yes to retract warning %d from %d row(s)",
			warning.ID, rowCount),
		fmt.Sprintf(
			"Retract warning %d (%q) from %d row(s) of table %s?",
			warning.ID, warning.Msg, rowCount, warning.DatasetName))
}
