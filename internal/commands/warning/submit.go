package warning

import (
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/user_submitted"
	"github.com/qlustered/qctl/internal/warnings"
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
		Short: "Raise an ad-hoc warning on cells of quarantined rows",
		Long: `Raise an ad-hoc, hand-written warning on named cells of named rows.

This does NOT hold the row: a warning is advisory only. Once the row's other
issues clear, the row goes clean CARRYING the warning, and from then on the
note can go stale — editing the flagged cell on a clean row does not update or
clear it, because there is no producer that revisits clean rows.

If you need the objection to survive that, raise an alert instead:
'qctl alert submit'. That holds the row until the flag is retracted or the
flagged value changes.

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
  qctl warning submit --table orders --row 1042 --field amount --comment "..."

Targets are the cross product of --row and --field: every named field on every
named row. There is no whole-row form — a warning always names at least one
cell.

The --comment also becomes the first comment of a new comment thread on the
flagged cells: one thread per submit, covering every row it newly flagged. A
[@label](mention:<uuid>) link in the comment notifies that user, as in any
comment; a bare @name notifies nobody, and the submit notifies no one else.
The output names the thread and the command that reads it:

  warning/8817 flagged rows 1042, 1043
  discussion: comment-thread/<uuid> (your --comment is its first comment)
    Next: qctl describe comment-thread <uuid> --table-id <id>

A row that already carried this flag answers already_existed and is listed
with who flagged it first and that flag's thread:

  row 1050 already carried this flag (same comment, same values) — nothing changed there.
    row 1050: flagged by Dana Ray <dana@example.com> — comment-thread/<uuid>

The --comment is part of the warning's identity and is therefore IMMUTABLE.
'qctl comment-thread edit-comment' lets you reword the thread's first comment,
but that changes the discussion only, never the warning's message. To reword
the flag itself, retract and submit again: the new submit opens a new thread,
and retracting leaves the old one open.

One --comment can produce more than one warning. The identity folds in the
cells' observed values, so rows holding different values become separate
warnings; each one is named on its own output line.

-o json|yaml prints the server's response, {issue_item_ids, results}, which
also carries each row's own record id, submitted_by and thread_id, and moves
those lines to stderr; -o name prints warning/<id> for each warning.

Examples:
  qctl warning submit --table orders --row 1042 --field amount --comment "looks off"
  qctl warning submit --table orders --row 1042 --row 1043 --field amount --field currency \
      --comment "FX pair looks wrong"
  qctl warning submit --table-id 42 --row 1042 --field amount \
      --comment "looks off" --suggest 1420.00`,
		Annotations: cmdutil.WithFormats(catalog.For("warning"), "table", "json", "yaml", "name"),
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

			client := warnings.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
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
				user_submitted.KindWarning, datasetID, result, cmdutil.NewQuarantineRowProber(ctx, datasetID)); err != nil {
				return err
			}
			return out.Write(result, user_submitted.IssueNames(user_submitted.KindWarning, result)...)
		},
	}

	cmdutil.AddTableFlags(cmd, "Table (dataset)")
	cmd.Flags().BoolP("yes", "y", false, "Accepted for compatibility; warning submit does not prompt")
	cmd.Flags().IntSliceVar(&rows, "row", nil,
		"Quarantined row id to flag (repeatable; required; max 500; must be unique)")
	cmd.Flags().StringArrayVar(&fields, "field", nil,
		"Field to flag on every --row (repeatable; required — there is no whole-row form)")
	cmd.Flags().StringVar(&comment, "comment", "",
		"What looks wrong about these cells (required; 1-2000 chars; immutable once submitted)")
	cmd.Flags().StringArrayVar(&suggest, "suggest", nil,
		"Value you think the cell should hold (repeatable; optional)")
	return cmd
}
