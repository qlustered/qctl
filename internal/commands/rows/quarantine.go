package rows

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/datasets"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/spf13/cobra"
)

// sendToQuarantineVerb is the name cobra registers and the name the result
// lines print, declared once so those two cannot drift.
const sendToQuarantineVerb = "send-to-quarantine"

// moveSupport is BOTH move verbs' row: they share every fact in it and differ
// only in the quarantine_hold value runMove sends, which reaches the wire at
// either selector.
//
// Table-only is the backend's matrix and not a qctl preference: both
// MoveRowsToQuarantineView and MoveAllRowsToQuarantineView exist at dataset
// scope alone. isBadData is left at its zero value because neither route has
// an is_bad_data parameter at all — see support.isBadData.
var moveSupport = support{
	table: []selectorKind{selRowIDs, selAll},
	// move_rows_to_quarantine reads `SELECT ... FROM {dataset_table} WHERE
	// id IN %s`, so these are CLEAN-table ids — the ones `qctl get data`
	// lists by default.
	rowIDs: rowIDsClean,
	// The move-all route reads the CLEAN table too
	// (`SELECT id FROM {dataset_table} WHERE is_deleted_ IS DISTINCT FROM
	// TRUE`, atlas cettings .../data_controller/quarantine.py
	// move_all_rows_to_quarantine).
	// It is the only --all in this group that does, which is the whole
	// reason support.all exists.
	all: allClean,
}

func newSendToQuarantineCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   sendToQuarantineVerb + " [table-id-or-name]",
		Short: "Park clean rows in quarantine for manual review",
		Long: `Move clean rows into quarantine and hold them for manual review.

  qctl rows send-to-quarantine orders --row-id 1042 --row-id 1043
  qctl rows send-to-quarantine orders --all

This and 'qctl rows reprocess-clean' are the same backend routes separated by
intent: this one PARKS a row for a human to look at (quarantine_hold: hold),
reprocess-clean hands the decision back to the table (quarantine_hold:
undecided). That is the same split the web UI makes between "move to
quarantine" and the clean tab's "Reprocess rows".

--all is a SECOND route, not a wildcard over the first: it takes no ids and
empties the table's clean side in one call, parking every live row.

On a large table this can outlive qctl's request deadline: --all is the one
selector in this group whose route walks the table in batches inside a single
request, and the default 30s can expire while the server is still working.
Raise it with --request-timeout. The move commits once at the end, so a
client-side timeout leaves no partial progress -- re-read 'qctl describe
table' before retrying.

The result reports rows moved, already in quarantine (skipped without changing
their hold decision), and not found. A wholly missing --row-id request exits 3;
other successful responses, including zero-move no-ops, exit 0.

Release a parked row with 'qctl rows release'. When the table requires
approval (its data-review policy requires review or it holds rows until
approved), that records your approval and releases the row only once it has
enough approvals.

To flag a specific wrong cell on a row you parked here, use 'qctl alert submit'
(or 'qctl warning submit'). The row keeps its id across the move, so the id you
read from 'qctl get data' is the id you flag — but the move publishes a
reprocess and the flag is refused until that settles. Full walkthrough:
qctl guide triage-alerts` + moveCommentDoc + moveOutputDoc,
		Annotations: cmdutil.WithArgKinds(cmdutil.WithFormats(catalog.For("data"), "table", "json", "yaml"), "table"),
		Args:        cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMove(cmd, firstArg(args), api.QuarantineHoldHold, moveSupport, action{
				verb:   sendToQuarantineVerb,
				gerund: "Move",
				extra:  []string{"Moved rows leave the clean table and stay on hold for manual review until they are released or their hold is reset."},
			})
		},
	}
	addScopeFlags(cmd, moveSupport)
	addSelectorFlags(cmd, moveSupport, selRowIDs, selAll)
	addMoveCommentFlags(cmd)
	addYesFlag(cmd)
	return cmd
}

func newReprocessCleanCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reprocess-clean [table-id-or-name]",
		Short: "Send clean rows back through validation",
		Long: `Send clean rows back through validation.

  qctl rows reprocess-clean orders --row-id 1042
  qctl rows reprocess-clean orders --all

The rows leave the clean table immediately. They return to it only if they pass
validation AND the table lets them: a reprocessed row carries no hold decision
of its own, so a table that quarantines rows until approved, or whose
data-review policy requires review, keeps it in quarantine even once it is
valid. Nothing in the response says which did — check the table afterwards.

The moved count confirms movement for reprocessing, not completed validation.
Already-quarantined rows are skipped and were not reprocessed by this request.
A wholly missing --row-id request exits 3; other successful responses, including
zero-move no-ops, exit 0. Missing IDs in mixed outcomes warn on stderr.

For rows that are ALREADY quarantined use 'qctl rows reprocess-quarantined';
that is a different endpoint with different selectors.

--all is a SECOND route, not a wildcard over the first: it takes no ids and
sends every live row on the table's clean side back through validation in one
call. On an older server, whose route ignored the hold, it parks them for
manual review instead. Like 'qctl rows send-to-quarantine --all' it can outlive
qctl's request deadline on a large table: raise --request-timeout, and re-read
'qctl describe table' before retrying.` + moveCommentDoc + moveOutputDoc,
		Annotations: cmdutil.WithArgKinds(cmdutil.WithFormats(catalog.For("data"), "table", "json", "yaml"), "table"),
		Args:        cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// undecided, not release: the two are not interchangeable on the
			// clean side either. Both leave the row unheld, but a released row
			// leaves quarantine as soon as it has no issues while an undecided
			// one defers to the table — _should_hold_valid_row_in_quarantine
			// keeps a NULL row in quarantine when it carries
			// suspected_duplicate_, the dataset quarantines rows until
			// approved, or its data-review policy requires review (atlas
			// cmapper/cmapper/cleaner.py). "Reprocess it, and let the
			// table decide" is this verb, and it is exactly what the retired
			// JSON null meant.
			return runMove(cmd, firstArg(args), api.QuarantineHoldUndecided, moveSupport, action{
				verb:   "reprocess-clean",
				gerund: "Reprocess",
				extra:  []string{"These rows leave the clean table now and come back only if they pass validation."},
			})
		},
	}
	addScopeFlags(cmd, moveSupport)
	addSelectorFlags(cmd, moveSupport, selRowIDs, selAll)
	addMoveCommentFlags(cmd)
	addYesFlag(cmd)
	return cmd
}

// runMove is both move verbs. hold is the whole difference: QuarantineHoldHold
// parks the rows in quarantine, QuarantineHoldUndecided sends them back through
// validation with no hold decision on them.
//
// hold is also the SINGLE place each verb declares which population it leaves
// the rows in, which is why the follow-up line reads it rather than deriving
// the population a second time. Whatever went on the wire is what the printed
// 'qctl get data' line names, so the two cannot drift: a parked row is read
// back from the quarantine table, a reprocessed one from the clean table it
// returns to if it passes and the table does not hold it anyway.
func runMove(cmd *cobra.Command, tableArg string, hold api.QuarantineHold, sup support, act action) error {
	var comment datasets.MoveComment
	ctx, sc, sel, err := resolveTarget(cmd, tableArg, sup, act.verb, func(c *cobra.Command) error {
		var cerr error
		comment, cerr = moveCommentFlags(c)
		return cerr
	})
	if err != nil {
		return err
	}
	if err := confirmAction(cmd, ctx, sc, sel, sup, act); err != nil {
		return err
	}

	res, err := moveRows(ctx, sc, sel, hold, act.verb, comment)
	if err != nil {
		return err
	}
	return reportMoveResult(cmd, sc, sel, sup, act, res, hold, comment.Text != nil)
}

// maxMoveCommentLength is the backend's limit on a move comment, in
// characters, counted after surrounding whitespace is stripped.
const maxMoveCommentLength = 10000

// maxMoveCommentRows is the most --row-id values a move may carry with a
// comment.
const maxMoveCommentRows = 500

const moveCommentDoc = `

--comment is optional. With --row-id it becomes a discussion on the requested
rows, moved or already in quarantine, and the result names the thread. With
--all no thread is made: the table's internal followers are notified instead,
and the result says how many. It must not be blank, is at most 10000
characters, and covers at most 500 --row-id values. An internal user's comment
is hidden from the sender by default; --visible-to-sender shows it, and needs
--comment, --row-id and rows that all come from one cloud source. On a --shared
table the comment is always internal and cannot mention anyone.`

func addMoveCommentFlags(cmd *cobra.Command) {
	cmd.Flags().String("comment", "", "Optional note on why the rows are moved: a discussion on the rows with --row-id, a notification to the table's followers with --all")
	cmd.Flags().Bool("visible-to-sender", false, "Show the --comment discussion to the sender (needs --comment and --row-id; not with --shared)")
}

// moveCommentFlags returns the move's comment from --comment and
// --visible-to-sender, refusing a combination the server would refuse with
// exit 2. Without --comment the result is the zero MoveComment.
func moveCommentFlags(cmd *cobra.Command) (datasets.MoveComment, error) {
	visible, _ := cmd.Flags().GetBool("visible-to-sender")
	if !cmd.Flags().Changed("comment") {
		if visible {
			return datasets.MoveComment{}, qerrors.NewBadUsage("--visible-to-sender needs --comment")
		}
		return datasets.MoveComment{}, nil
	}
	text, _ := cmd.Flags().GetString("comment")
	stripped := strings.TrimSpace(text)
	if stripped == "" {
		return datasets.MoveComment{}, qerrors.NewBadUsage("--comment cannot be blank")
	}
	if n := utf8.RuneCountInString(stripped); n > maxMoveCommentLength {
		return datasets.MoveComment{}, qerrors.NewBadUsage(fmt.Sprintf("--comment is %d characters; the limit is %d", n, maxMoveCommentLength))
	}
	if ids, _ := cmd.Flags().GetIntSlice("row-id"); len(dedupe(ids)) > maxMoveCommentRows {
		return datasets.MoveComment{}, qerrors.NewBadUsage(fmt.Sprintf(
			"--comment covers at most %d rows; %d --row-id values were given", maxMoveCommentRows, len(dedupe(ids))))
	}
	comment := datasets.MoveComment{Text: &text}
	if !visible {
		return comment, nil
	}
	if all, _ := cmd.Flags().GetBool("all"); all {
		return datasets.MoveComment{}, qerrors.NewBadUsage("--visible-to-sender applies to --row-id only: an --all comment opens no discussion")
	}
	if shared, _ := cmd.Flags().GetBool("shared"); shared {
		return datasets.MoveComment{}, qerrors.NewBadUsage("--visible-to-sender is not supported with --shared: a comment on a shared table is always internal")
	}
	internal := false
	comment.IsInternal = &internal
	return comment, nil
}

// moveRows picks the route, and is named arms plus refusing defaults for the
// same reason deleteRows, reprocessRows and updateKeepInQuarantine are. It is
// extracted from runMove rather than written inline so the defaults are
// REACHABLE from a test: runMove starts at resolveTarget, which refuses a scope
// or selector the verb's support{} does not list, so an inline guard could
// never be driven.
//
// Both routes exist at dataset scope alone and carry no narrowing parameter, so
// a support{} edit that gave either verb a `cloud` or `file` row would send the
// TABLE-scoped request while the prompt named a cloud source, and move rows the
// user never scoped to. That is the refuseAllBadRows shape — the route ignores
// the value the arm was chosen on — so the scope switch refuses instead of
// falling through.
func moveRows(ctx *cmdutil.CommandContext, sc scope, sel selector, hold api.QuarantineHold, verb string, comment datasets.MoveComment) (*datasets.QuarantineMoveResult, error) {
	switch sc.kind {
	case scopeTable:
		return moveRowsInTable(ctx, sc, sel, hold, verb, comment)
	default:
		return nil, unroutableSelector(verb, sel, sc)
	}
}

// moveRowsInTable is the selector switch: two routes, and they are NOT the same
// request with a different selector. hold and comment.Text reach the wire on
// both; comment.IsInternal only on --row-id.
func moveRowsInTable(ctx *cmdutil.CommandContext, sc scope, sel selector, hold api.QuarantineHold, verb string, comment datasets.MoveComment) (*datasets.QuarantineMoveResult, error) {
	dsClient := datasets.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity).WithThirdParty(sc.thirdParty)
	token := ctx.Credential.AccessToken
	var r *datasets.QuarantineMoveResult
	var err error
	switch sel.kind {
	case selAll:
		r, err = dsClient.MoveAllRowsToQuarantine(token, sc.tableID, hold, comment.Text)
	case selRowIDs:
		r, err = dsClient.MoveRowsToQuarantine(token, sc.tableID, sel.rowIDs, hold, comment)
	default:
		return nil, unroutableSelector(verb, sel, sc)
	}
	if err != nil {
		return nil, err
	}
	return r, nil
}
