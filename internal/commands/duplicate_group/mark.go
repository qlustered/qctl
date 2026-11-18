package duplicate_group //nolint:revive // Package name mirrors the hyphenated command.

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/duplicate_groups"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// The error codes only the mark and retract routes answer with.
const (
	codeRowAlreadyMarked     = "DUPLICATE_ROW_ALREADY_MARKED"
	codeRowInUndecidedGroup  = "DUPLICATE_ROW_IN_UNDECIDED_GROUP"
	codeGroupNotPersonMarked = "DUPLICATE_GROUP_NOT_PERSON_MARKED"
	codeGroupNotMarker       = "DUPLICATE_GROUP_NOT_MARKER"
	codeValidation           = "ERR_VALIDATION"
	markPermissionNote       = "(marking needs the SUBMIT_ALERT permission, an internal role and access to the table; " +
		"--mode hold also needs SOURCE_DATA_WRITE)"
	retractPermissionNote = "(retracting needs the SUBMIT_ALERT permission, an internal role and access to the " +
		"table; only the group's marker may retract it)"
	// maxListedRows caps the row ids a result line names.
	maxListedRows = 10
)

func newMarkCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mark",
		Short: "Mark rows of a table as one duplicate group",
		Long: `Mark rows of a mirror or append table as duplicates of one another: they
become one person_marked group until a reviewer decides it.

  --mode hold  holds the rows in quarantine, moving clean ones there
  --mode flag  only flags them; they stay where they are

--row-id names 2 to 500 distinct row ids, repeated or comma-separated, and
--comment says why (required, 1-10000 characters). The comment opens the
group's internal discussion. You cannot decide a group you marked; another
reviewer allows it, keeps one member or says the rows are not duplicates.
'qctl duplicate-group retract' takes the mark back.

The server refuses a row already in another live marked group
(DUPLICATE_ROW_ALREADY_MARKED) or held by a loaded group awaiting a decision
(DUPLICATE_ROW_IN_UNDECIDED_GROUP), and a row that is not a row of the table.

Needs the SUBMIT_ALERT permission and an internal role; --mode hold also
needs SOURCE_DATA_WRITE. Prompts unless --yes. -o json|yaml prints the
server's {group_id, comment_thread_id, hold_mode, members, apply} and moves
the result lines to stderr; -o name prints duplicate-group/<uuid>.`,
		Example: `  qctl duplicate-group mark --table orders --row-id 1001,1002 --mode hold --comment "same order keyed twice" --yes
  qctl duplicate-group mark --table-id 42 --row-id 1001 --row-id 1007 --mode flag --comment "check with the sender"`,
		Annotations: decisionAnnotations(),
		Args:        cobra.NoArgs,
		RunE:        runMark,
	}
	addCommonFlags(cmd, "Why the rows are duplicates (required, 1-10000 chars)")
	cmd.Flags().IntSlice("row-id", nil, "Row to mark, by id (repeatable or comma-separated; 2-500 distinct ids)")
	cmd.Flags().String("mode", "", "hold (keep the rows in quarantine) or flag (only flag them); required")
	return cmd
}

// markRowIDs returns --row-id, checked as the server's schema checks it.
func markRowIDs(cmd *cobra.Command) ([]int, error) {
	ids, _ := cmd.Flags().GetIntSlice("row-id")
	seen := make(map[int]bool, len(ids))
	for _, id := range ids {
		if id < 1 {
			return nil, qerrors.NewBadUsagef("--row-id must be a positive integer, got %d", id)
		}
		if seen[id] {
			return nil, qerrors.NewBadUsagef("--row-id %d is given more than once", id)
		}
		seen[id] = true
	}
	if len(ids) < duplicate_groups.MinMarkRows || len(ids) > duplicate_groups.MaxMarkRows {
		return nil, qerrors.NewBadUsagef("--row-id names %d row(s); a mark takes %d to %d",
			len(ids), duplicate_groups.MinMarkRows, duplicate_groups.MaxMarkRows)
	}
	return ids, nil
}

func markMode(cmd *cobra.Command) (duplicate_groups.HoldMode, error) {
	raw, _ := cmd.Flags().GetString("mode")
	switch mode := duplicate_groups.HoldMode(strings.ToLower(strings.TrimSpace(raw))); mode {
	case duplicate_groups.HoldModeHold, duplicate_groups.HoldModeFlag:
		return mode, nil
	case "":
		return "", qerrors.NewBadUsage("--mode is required: hold or flag")
	}
	return "", qerrors.NewBadUsagef("invalid --mode %q: expected hold or flag", raw)
}

func runMark(cmd *cobra.Command, args []string) error {
	if _, err := preflight(cmd, args); err != nil {
		return err
	}
	rowIDs, err := markRowIDs(cmd)
	if err != nil {
		return err
	}
	mode, err := markMode(cmd)
	if err != nil {
		return err
	}
	comment, err := commentFlag(cmd)
	if err != nil {
		return err
	}
	if comment == nil {
		return qerrors.NewBadUsage("--comment is required: say why the rows are duplicates")
	}
	t, err := resolveTarget(cmd)
	if err != nil {
		return err
	}
	question := fmt.Sprintf("Mark %d row(s) of table/%d as one duplicate group (%s)?%s",
		len(rowIDs), t.tableID, mode, markNote(mode))
	if err := confirm(cmd, "marking a duplicate group", question); err != nil {
		return err
	}
	body := duplicate_groups.MarkRequest{RowIds: rowIDs, Mode: mode, Comment: *comment}
	result, err := t.client.Mark(t.token(), t.tableID, body)
	if err != nil {
		return mapMarkError(err, t.tableID)
	}
	out := cmdutil.NewResultWriter(cmd)
	writeMarkResult(out.Human, t.tableID, result)
	if result.Apply != nil {
		warnUnapplied(cmd, result.Apply)
	}
	return out.Write(result, groupRef(result.GroupID))
}

func markNote(mode duplicate_groups.HoldMode) string {
	if mode == duplicate_groups.HoldModeHold {
		return " The rows are held in quarantine, clean ones moved there, until a reviewer decides the group."
	}
	return " The rows are flagged and stay where they are until a reviewer decides the group."
}

func writeMarkResult(out io.Writer, tableID int, r *duplicate_groups.MarkResult) {
	fmt.Fprintf(out, "%s marked %d row(s) (mode: %s, thread: %s)\n", groupRef(r.GroupID), len(r.Members),
		terminal.Sanitize(string(r.HoldMode)), r.CommentThreadID)
	for _, m := range r.Members {
		fmt.Fprintf(out, "  member %d: row %d (cloud-source/%d)\n", m.ID, m.RowID, m.DataSourceModelID)
	}
	if r.Apply != nil {
		writeApply(out, r.Apply)
	}
	fmt.Fprintf(out, "Another reviewer decides it: you marked it, so you cannot. "+
		"'qctl duplicate-group retract %s --table-id %d' takes the mark back.\n", r.GroupID, tableID)
	writeNext(out, tableID, r.GroupID)
}

// mapMarkError explains the mark route's refusals. %w keeps each exit code.
func mapMarkError(err error, tableID int) error {
	var apiErr *duplicate_groups.APIError
	if !errors.As(err, &apiErr) {
		return err
	}
	switch apiErr.Code {
	case codeRowAlreadyMarked:
		return fmt.Errorf("%w\n(rows %s are in marked group(s) %s; retract that mark first: %s)",
			err, contextList(apiErr, "row_ids"), contextList(apiErr, "group_ids"),
			groupCommand(apiErr, tableID, "qctl duplicate-group retract"))
	case codeRowInUndecidedGroup:
		return fmt.Errorf("%w\n(rows %s are held by group(s) %s awaiting a decision; decide those first: %s)",
			err, contextList(apiErr, "row_ids"), contextList(apiErr, "group_ids"),
			groupCommand(apiErr, tableID, "qctl describe duplicate-group"))
	case codeValidation:
		if _, ok := apiErr.Context["row_ids"]; ok {
			return fmt.Errorf("%w\n(rows %s are not rows of table/%d; 'qctl get data --table-id %d', with "+
				"--is-bad-data for quarantined rows, lists its rows)", err, contextList(apiErr, "row_ids"), tableID, tableID)
		}
	case apierror.CodeChangeProposalRequired, apierror.CodeChangeReviewPolicyDenied:
		return err
	}
	if apiErr.StatusCode == http.StatusForbidden && apiErr.Code == "" {
		return fmt.Errorf("%w\n%s", err, markPermissionNote)
	}
	return err
}

// groupCommand is command run on the one group context.group_ids names, or
// on <group-id> when it names several.
func groupCommand(apiErr *duplicate_groups.APIError, tableID int, command string) string {
	raw, _ := apiErr.Context["group_ids"].([]interface{})
	group := "<group-id>"
	if len(raw) == 1 {
		group = terminal.Sanitize(fmt.Sprint(raw[0]))
	}
	return fmt.Sprintf("'%s %s --table-id %d'", command, group, tableID)
}

// contextList renders the list context[key].
func contextList(apiErr *duplicate_groups.APIError, key string) string {
	raw, _ := apiErr.Context[key].([]interface{})
	items := make([]string, 0, len(raw))
	for _, v := range raw {
		items = append(items, terminal.Sanitize(fmt.Sprint(v)))
	}
	if len(items) == 0 {
		return "not named"
	}
	return strings.Join(items, ", ")
}

func newRetractCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "retract <group-id>",
		Short: "Retract the mark on a person-marked duplicate group",
		Long: `Retract a person_marked group: it ends, and its rows still in quarantine
are queued for a reprocess, which releases them unless something else holds
them. --comment, when given, is added to the group's discussion.

qctl reads the group first and exits 2 without a request when it was not
marked by a person, has ended, or has an effective decision of its own (a
decided mark cannot be retracted). A pending decision does not block a
retract: it is withdrawn as the group ends.

Only the user who marked the group may retract it; describe duplicate-group
shows whether you may ("can retract the mark").

Prompts unless --yes. -o json|yaml prints the server's {group_id, ended_at,
end_reason, comment_id, enqueued_row_ids} and moves the result lines to
stderr; -o name prints duplicate-group/<uuid>.`,
		Example:     `  qctl duplicate-group retract 0190aaaa-1111-4222-8333-444444444444 --table orders --comment "they are two orders" --yes`,
		Annotations: decisionAnnotations(),
		Args:        cobra.ExactArgs(1),
		RunE:        runRetract,
	}
	addCommonFlags(cmd, "Comment added to the group's discussion")
	return cmd
}

func runRetract(cmd *cobra.Command, args []string) error {
	groupID, err := preflight(cmd, args)
	if err != nil {
		return err
	}
	comment, err := commentFlag(cmd)
	if err != nil {
		return err
	}
	t, err := resolveTarget(cmd)
	if err != nil {
		return err
	}
	group, err := t.client.Get(t.token(), t.tableID, groupID)
	if err != nil {
		return mapRetractError(err, t.tableID, groupID)
	}
	if err := checkRetractable(group, t.tableID); err != nil {
		return err
	}
	if err := confirm(cmd, "retracting a duplicate group", retractQuestion(group, t.tableID)); err != nil {
		return err
	}
	result, err := t.client.Retract(t.token(), t.tableID, groupID, comment)
	if err != nil {
		return mapRetractError(err, t.tableID, groupID)
	}
	out := cmdutil.NewResultWriter(cmd)
	writeRetractResult(out.Human, t.tableID, result)
	return out.Write(result, groupRef(groupID))
}

// checkRetractable refuses, at exit 2, a group the server refuses to
// retract whoever asks.
func checkRetractable(group *duplicate_groups.Group, tableID int) error {
	switch {
	case group.Origin != duplicate_groups.OriginPersonMarked:
		return qerrors.NewBadUsagef("%s is a %s group; only a person_marked group can be retracted "+
			"(decide it instead: %s)", groupRef(group.ID), terminal.Sanitize(string(group.Origin)),
			describeCmd(tableID, group.ID))
	case group.State == duplicate_groups.GroupStateEnded:
		return qerrors.NewBadUsagef("%s has ended; there is no mark to retract", groupRef(group.ID))
	case group.Decision != nil:
		return qerrors.NewBadUsage(decidedMarkNote(fmt.Sprintf("%s's own %s", groupRef(group.ID), labelOf(group.Decision))))
	}
	return nil
}

// decidedMarkNote says that subject, a group's own decision, being effective
// keeps its mark from being retracted.
func decidedMarkNote(subject string) string {
	return subject + " is effective, so its mark can no longer be retracted; another reviewer may propose " +
		"a different decision (allow, keep-one, not-duplicates)"
}

// retractQuestion is retract's prompt.
func retractQuestion(group *duplicate_groups.Group, tableID int) string {
	question := fmt.Sprintf("Retract the mark on %s (%d member(s)) on table/%d? The group ends and its rows "+
		"still in quarantine are queued for a reprocess, which releases them unless something else holds them.",
		groupRef(group.ID), activeMembers(group), tableID)
	if p := group.PendingDecision; p != nil {
		question += fmt.Sprintf(" Its pending %s [d: %s] is withdrawn as the group ends.", labelOf(p), p.ID)
	}
	return question
}

// activeMembers counts the group's members with an outcome.
func activeMembers(group *duplicate_groups.Group) int {
	n := 0
	for _, m := range group.Members {
		if m.Outcome != nil && m.DeletedAt == nil {
			n++
		}
	}
	return n
}

func writeRetractResult(out io.Writer, tableID int, r *duplicate_groups.RetractResult) {
	fmt.Fprintf(out, "%s retracted (ended %s, reason: %s)\n", groupRef(r.GroupID),
		r.EndedAt.Format("2006-01-02 15:04 MST"), terminal.Sanitize(string(r.EndReason)))
	if r.CommentID != nil {
		fmt.Fprintf(out, "  comment added [c: %s]\n", *r.CommentID)
	}
	if n := len(r.EnqueuedRowIds); n > 0 {
		fmt.Fprintf(out, "  %d quarantined row(s) queued for a reprocess: %s\n", n, rowList(r.EnqueuedRowIds))
	} else {
		fmt.Fprintln(out, "  no quarantined row queued for a reprocess")
	}
	writeNext(out, tableID, r.GroupID)
}

// rowList names at most maxListedRows row ids.
func rowList(ids []int) string {
	parts := make([]string, 0, maxListedRows+1)
	for i, id := range ids {
		if i == maxListedRows {
			parts = append(parts, fmt.Sprintf("and %d more", len(ids)-maxListedRows))
			break
		}
		parts = append(parts, fmt.Sprint(id))
	}
	return strings.Join(parts, ", ")
}

// mapRetractError explains the retract route's refusals, then defers to
// mapError. %w keeps each exit code.
func mapRetractError(err error, tableID int, groupID openapi_types.UUID) error {
	var apiErr *duplicate_groups.APIError
	if !errors.As(err, &apiErr) {
		return err
	}
	switch {
	case apiErr.Code == codeGroupNotPersonMarked:
		return fmt.Errorf("%w\n(only a person_marked group can be retracted; %s shows how to decide it)",
			err, describeCmd(tableID, groupID))
	case apiErr.Code == codeGroupNotMarker:
		return fmt.Errorf("%w\n(only the user who marked the group may retract it)", err)
	case apiErr.Code == codeDecisionExists:
		return fmt.Errorf("%w\n(%s; %s shows it)", err, decidedMarkNote("the group's own decision"),
			describeCmd(tableID, groupID))
	case apiErr.StatusCode == http.StatusForbidden && apiErr.Code == "":
		return fmt.Errorf("%w\n%s", err, retractPermissionNote)
	}
	return mapError(err, tableID, groupID)
}
