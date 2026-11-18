package alert

import (
	"fmt"
	"io"

	"github.com/qlustered/qctl/internal/alerts"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/user_submitted"
	"github.com/spf13/cobra"
)

func newSubmitCommand() *cobra.Command {
	var (
		rows    []int
		fields  []string
		comment string
		suggest []string
	)

	cmd := &cobra.Command{
		Use:   "submit",
		Short: "Raise an ad-hoc alert on cells of quarantined rows",
		Long: `Raise an ad-hoc, hand-written alert on named cells of named rows.

This HOLDS each flagged row in quarantine until the flag is retracted or the
flagged value changes. It is a blocking action on production data.

The rows must ALREADY be quarantined — find them with
'qctl get data <table> --is-bad-data'. A row that is not a live quarantined
row of this table, whose flagged values changed under you, or that is being
reprocessed, comes back as row_not_found and is reported on stderr while the
rest are flagged. The backend answers all three with that one status, so qctl
re-reads each row_not_found row, up to ten, and names what it found: a row you
just moved with 'qctl rows send-to-quarantine' is quarantined but awaiting
processing, and the submit succeeds once the queued reprocess settles.

To flag a cell on a CLEAN row, move it first — that is the two-step the web UI
enforces, and the row keeps its id across the move:

  qctl rows send-to-quarantine orders --row-id 1042
  qctl alert submit --table orders --row 1042 --field amount --comment "..."

Targets are the cross product of --row and --field: every named field on every
named row. Unlike a comment thread there is no whole-row form — an alert
always names at least one cell.

The --comment also becomes the first comment of a new comment thread on the
flagged cells: one thread per submit, covering every row it newly flagged. A
[@label](mention:<uuid>) link in the comment notifies that user, as in any
comment; a bare @name notifies nobody, and the submit notifies no one else.
The output names the thread and the command that reads it:

  alert/8817 flagged rows 1042, 1043
  discussion: comment-thread/<uuid> (your --comment is its first comment)
    Next: qctl describe comment-thread <uuid> --table-id <id>

A row that already carried this flag answers already_existed and is listed
with who flagged it first and that flag's thread:

  row 1050 already carried this flag (same comment, same values) — nothing changed there.
    row 1050: flagged by Dana Ray <dana@example.com> — comment-thread/<uuid>

The --comment is part of the alert's identity and is therefore IMMUTABLE.
'qctl comment-thread edit-comment' lets you reword the thread's first comment,
but that changes the discussion only, never the alert's message. To reword
the flag itself, retract and submit again: the new submit opens a new thread,
and retracting leaves the old one open.

One --comment can produce more than one alert. The identity folds in the cells'
observed values, so rows holding different values become separate alerts; each
one is named on its own output line.

-o json|yaml prints the server's response, {issue_item_ids, results}, which
also carries each row's own record id, submitted_by and thread_id, and moves
those lines to stderr; -o name prints alert/<id> for each alert.

For an objection that must not hold the row, use 'qctl warning submit' — but
read its --help first: a warning can go stale.

Examples:
  qctl alert submit --table orders --row 1042 --field amount --comment "wrong FX rate"
  qctl alert submit --table orders --row 1042 --row 1043 --field amount --field currency \
      --comment "FX mismatch" --yes
  qctl alert submit --table-id 42 --row 1042 --field amount \
      --comment "wrong FX rate" --suggest 1420.00`,
		Annotations: cmdutil.WithFormats(catalog.For("alert"), "table", "json", "yaml", "name"),
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Before Bootstrap and the table lookup, both of which cost a
			// request: an over-cap or malformed target set is decidable
			// locally, and a typo must cost nothing.
			body, err := user_submitted.SubmitRequest{
				Rows: rows, Fields: fields, Comment: comment, Suggest: suggest,
			}.Build()
			if err != nil {
				return err
			}
			if err := cmdutil.ValidateTableSelectorFromFlags(cmd); err != nil {
				return err
			}

			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}
			datasetID, err := cmdutil.ResolveTableIDFromFlags(ctx, cmd)
			if err != nil {
				return err
			}

			yes, _ := cmd.Flags().GetBool("yes")
			if err := confirmSubmit(cmd.ErrOrStderr(), datasetID, len(rows), len(fields), yes); err != nil {
				return err
			}

			client := alerts.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			result, err := client.SubmitUserIssues(ctx.Credential.AccessToken, datasetID, body)
			if err != nil {
				return err
			}
			// The prober is passed on every submit, not behind -v: a row
			// refused because `rows send-to-quarantine` has not finished
			// reprocessing it reads exactly like a mistyped row id, and the
			// caller who most needs the difference is the one who will never
			// re-run the command with a verbosity flag to find out.
			out := cmdutil.NewResultWriter(cmd)
			if err := user_submitted.ReportSubmit(out.Human, cmd.ErrOrStderr(),
				user_submitted.KindAlert, datasetID, result, cmdutil.NewQuarantineRowProber(ctx, datasetID)); err != nil {
				return err
			}
			return out.Write(result, user_submitted.IssueNames(user_submitted.KindAlert, result)...)
		},
	}

	cmdutil.AddTableFlags(cmd, "Table (dataset)")
	cmd.Flags().IntSliceVar(&rows, "row", nil,
		"Quarantined row id to flag (repeatable; required; max 500; must be unique)")
	cmd.Flags().StringArrayVar(&fields, "field", nil,
		"Field to flag on every --row (repeatable; required — there is no whole-row form)")
	cmd.Flags().StringVar(&comment, "comment", "",
		"What is wrong with these cells (required; 1-2000 chars; immutable once submitted)")
	cmd.Flags().StringArrayVar(&suggest, "suggest", nil,
		"Value you think the cell should hold (repeatable; optional)")
	cmd.Flags().Bool("yes", false, "Skip the confirmation prompt (for scripted/non-interactive use)")
	return cmd
}

// confirmSubmit gets agreement before holding production rows in quarantine.
//
// The cell count is len(rows)*len(fields) because every --field applies to
// every --row and a fieldless request is already refused — there is no
// whole-row target to exclude from the product, which is what makes this
// differ from comment-thread create's cap arithmetic.
func confirmSubmit(out io.Writer, datasetID, rowCount, fieldCount int, yes bool) error {
	return cmdutil.Confirm(out, yes,
		fmt.Sprintf(
			"confirmation required but stdin is not an interactive terminal — pass --yes to flag %d row(s) on table %d",
			rowCount, datasetID),
		fmt.Sprintf(
			"Raise an alert on %d cell(s) across %d row(s) of table %d? This holds those rows in quarantine.",
			rowCount*fieldCount, rowCount, datasetID))
}
