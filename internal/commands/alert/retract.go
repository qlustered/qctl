package alert

import (
	"fmt"
	"io"
	"strconv"

	"github.com/qlustered/qctl/internal/alerts"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/user_submitted"
	"github.com/spf13/cobra"
)

func newRetractCommand() *cobra.Command {
	var rows []int

	cmd := &cobra.Command{
		Use:   "retract <alert-id>",
		Short: "Withdraw a user-submitted alert from rows you flagged in error",
		Long: `Withdraw a user-submitted alert from the rows you name.

Retract is NOT resolve. It means "I flagged the wrong cell", not "this is
fixed" — 'qctl alert resolve' is the other thing, and it is what an alert
someone else's rule raised needs. Only an alert a human submitted can be
retracted; anything else is refused before the call.

The alert id is the id 'qctl get alerts' prints. The row ids are the rows the
alert holds; both are also in the quarantined-row payload:

  qctl get data orders --is-bad-data             # [A<alert-id>] on each cell
  qctl get data orders --is-bad-data -o json     # alert_item_ids_ per row

Each raw entry is "<alert-id>-<field>", so an alert holding row 1042 on amount
shows as "8817-amount" — which the rendered page spells "[A8817]" on that cell.

Retracting the last row an alert holds auto-resolves it, which is reported.

This is attributed but permissive: the alert:resolve permission lets any
resolver — including an external data-source-scoped principal — erase someone
else's objection. Hence the confirmation prompt.

There is no way to edit the comment: it is part of the alert's identity. To
reword it, retract and 'qctl alert submit' again.

Examples:
  qctl alert retract 8817 --row 1042
  qctl alert retract 8817 --row 1042 --row 1043 --yes

-o json|yaml prints the server's {issue_resolved, results}, one result per
row, and moves the prose to stderr; -o name prints alert/<id>.`,
		Annotations: cmdutil.WithFormats(catalog.For("alert"), "table", "json", "yaml", "name"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Decided locally, so it runs before Bootstrap and the alert read:
			// a typo must cost nothing.
			alertID, err := strconv.Atoi(args[0])
			if err != nil {
				return qerrors.NewBadUsagef("invalid alert id %q: expected a number", args[0])
			}
			body, err := user_submitted.RetractRequest{IssueItemID: alertID, Rows: rows}.Build()
			if err != nil {
				return err
			}

			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}
			client := alerts.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			token := ctx.Credential.AccessToken

			// The read is what makes the positional form work: the alert names
			// its own table, so there is no --table to get wrong. It also
			// catches the one mistake the wire cannot report — a rule or
			// ingestion alert comes back as every row not_found, which reads
			// as "wrong row" rather than "wrong verb".
			alert, err := client.GetAlert(token, alertID)
			if err != nil {
				return fmt.Errorf("failed to get alert %d: %w", alertID, err)
			}
			if alert.IssueType == api.AlertTypePersonMarkedDuplicate {
				if group := alerts.HoldGroupID(alert); group != "" {
					return qerrors.NewBadUsagef(
						"alert %d holds the rows of person-marked duplicate group %s; retract the group's mark instead:\n"+
							"  qctl duplicate-group retract %s --table-id %d",
						alertID, group, group, alert.DatasetID)
				}
				return qerrors.NewBadUsagef(
					"alert %d holds the rows of a person-marked duplicate group; retract the group's mark instead.\n"+
						"Find the group with: qctl get duplicate-groups --table-id %d --row-id <a held row's id>\n"+
						"Then: qctl duplicate-group retract <group-id> --table-id %d",
					alertID, alert.DatasetID, alert.DatasetID)
			}
			if alert.IssueType != api.AlertTypeUserSubmitted {
				return qerrors.NewBadUsagef(
					"alert %d is a %s alert, not one a human submitted — there is nothing to retract.\n"+
						"Act on it with: qctl alert resolve %d --action <action>\n"+
						"List the actions it offers with: qctl describe alert %d",
					alertID, alert.IssueType, alertID, alertID)
			}

			yes, _ := cmd.Flags().GetBool("yes")
			if err := confirmRetract(cmd.ErrOrStderr(), alert, len(rows), yes); err != nil {
				return err
			}

			result, err := client.RetractUserIssues(token, alert.DatasetID, body)
			if err != nil {
				return err
			}
			w := cmdutil.NewResultWriter(cmd)
			if err := user_submitted.ReportRetract(w.Human, cmd.ErrOrStderr(),
				user_submitted.KindAlert, alertID, result); err != nil {
				return err
			}
			return w.Write(result, alertName(alertID))
		},
	}

	cmd.Flags().IntSliceVar(&rows, "row", nil,
		"Row id to withdraw the flag from (repeatable; required; max 500; must be unique)")
	cmd.Flags().Bool("yes", false, "Skip the confirmation prompt (for scripted/non-interactive use)")
	return cmd
}

// confirmRetract shows the objection about to be erased and gets agreement.
// The message is quoted because the caller may not be the person who wrote it.
func confirmRetract(out io.Writer, alert *alerts.AlertFull, rowCount int, yes bool) error {
	return cmdutil.Confirm(out, yes,
		fmt.Sprintf(
			"confirmation required but stdin is not an interactive terminal — pass --yes to retract alert %d from %d row(s)",
			alert.ID, rowCount),
		fmt.Sprintf(
			"Retract alert %d (%q) from %d row(s) of table %s?",
			alert.ID, alert.Msg, rowCount, alert.DatasetName))
}
