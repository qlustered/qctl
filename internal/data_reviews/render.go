package data_reviews //nolint:revive // Mirrors the backend's data-review arc, like rule_reviews.

import (
	"fmt"
	"strings"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/comments"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/pkg/timeutil"
	"github.com/qlustered/qctl/internal/terminal"
)

// DetailView is everything `describe data-review` renders. Only Review is
// required; every other part is optional and its absence degrades a section
// rather than failing the payload.
type DetailView struct {
	Review    *Review
	Decisions []Decision
	// ApprovalProgress is the active review's server-side approval meter.
	ApprovalProgress *RowOverlay
	// Me is the caller, or nil when it was not looked up or could not be.
	// Nothing on the review's wire says who is asking, so without it the
	// payload cannot find the caller's own standing decision.
	Me *openapi_types.UUID
	// Directory names the users the review does not embed; nil renders them
	// as UUIDs.
	Directory *Directory
	// Thread is the review's discussion, read through the dataset-scoped
	// comment-thread route; ThreadErr is why that read failed.
	Thread    *api.CommentThreadResponseSchema
	ThreadErr error
}

// isBlocking reports whether a cell decision is a veto. comment_only is not:
// only approve, reject and request_changes decide a cell, and the release gate
// holds a row on any cell that is neither approved nor vetoed.
func isBlocking(d *api.ReviewDecisionEnum) bool {
	return d != nil && (*d == api.ReviewDecisionEnumReject || *d == api.ReviewDecisionEnumRequestChanges)
}

func isUndecided(d *api.ReviewDecisionEnum) bool {
	return d == nil || (*d != api.ReviewDecisionEnumApprove && !isBlocking(d))
}

// embeddedNames labels the users the review response carries whole. Only the
// ids missing from it cost a directory sweep.
func (v *DetailView) embeddedNames() map[openapi_types.UUID]string {
	names := map[openapi_types.UUID]string{}
	add := func(u *api.UserInfoTinyDictSchema) {
		if u != nil {
			names[u.ID] = comments.UserDisplay(u, u.ID)
		}
	}
	r := v.Review
	add(r.Requester)
	if r.AssignedReviewers != nil {
		for i := range *r.AssignedReviewers {
			add(&(*r.AssignedReviewers)[i])
		}
	}
	for _, c := range v.changes() {
		add(c.DecidedBy)
	}
	return names
}

// UnnamedUserIDs lists the users the payload names that the review response
// does not embed: whole-row deciders, unassigned vetters, and a cell decider
// whose user object the backend could not resolve. A caller fetches the
// directory only when this is non-empty.
func (v *DetailView) UnnamedUserIDs() []openapi_types.UUID {
	known := v.embeddedNames()
	var out []openapi_types.UUID
	want := func(id openapi_types.UUID) {
		if _, ok := known[id]; !ok {
			known[id] = ""
			out = append(out, id)
		}
	}
	for _, d := range v.Decisions {
		want(d.ReviewerID)
	}
	for _, vet := range v.vets() {
		want(vet.DecidedByID)
	}
	for _, c := range v.changes() {
		if c.DecidedBy == nil && c.DecidedByID != nil {
			want(*c.DecidedByID)
		}
	}
	return out
}

func (v *DetailView) changes() []Change {
	if v.Review.Changes == nil {
		return nil
	}
	return *v.Review.Changes
}

// vets returns unassigned_vets without exact repeats: the backend's join is
// not DISTINCT, so a row snapshotted in two submissions can list one vet twice.
func (v *DetailView) vets() []api.UnassignedFieldVetSchema {
	if v.Review.UnassignedVets == nil {
		return nil
	}
	seen := map[api.UnassignedFieldVetSchema]bool{}
	var out []api.UnassignedFieldVetSchema
	for _, vet := range *v.Review.UnassignedVets {
		if !seen[vet] {
			seen[vet] = true
			out = append(out, vet)
		}
	}
	return out
}

// standingDecisions folds the decisions list to one entry per reviewer, the
// latest by created_at, in served order. The backend already keeps one row per
// reviewer (a unique index, upserted in place), so this is defensive; note
// that the upsert never moves created_at, which is therefore when the reviewer
// FIRST decided, not when the verdict in force was recorded.
func (v *DetailView) standingDecisions() []Decision {
	latest := map[openapi_types.UUID]int{}
	var out []Decision
	for _, d := range v.Decisions {
		i, seen := latest[d.ReviewerID]
		switch {
		case !seen:
			latest[d.ReviewerID] = len(out)
			out = append(out, d)
		case !d.CreatedAt.Before(out[i].CreatedAt):
			out[i] = d
		}
	}
	return out
}

func (v *DetailView) myDecision() *Decision {
	if v.Me == nil {
		return nil
	}
	for _, d := range v.standingDecisions() {
		if d.ReviewerID == *v.Me {
			return &d
		}
	}
	return nil
}

func (v *DetailView) requesterID() *openapi_types.UUID {
	if v.Review.Requester == nil {
		return nil
	}
	return &v.Review.Requester.ID
}

type namer struct {
	known     map[openapi_types.UUID]string
	directory *Directory
	me        *openapi_types.UUID
}

func (n namer) name(id openapi_types.UUID) string {
	label, ok := n.known[id]
	if !ok {
		label = id.String()
		if n.directory != nil {
			label = n.directory.DisplayName(id)
		}
	}
	if n.me != nil && *n.me == id {
		label += " (you)"
	}
	return label
}

// RenderDetail renders the agent-facing payload for `describe data-review`,
// modelled on rule_reviews.RenderDetail. Section order is frozen — later work
// inserts sections, never reorders them.
func RenderDetail(v *DetailView) string {
	var b strings.Builder
	n := namer{known: v.embeddedNames(), directory: v.Directory, me: v.Me}
	r := v.Review

	renderHeader(&b, v, n)
	renderSubmissions(&b, r)
	renderChanges(&b, v, n)
	renderDecisions(&b, v, n)
	renderVets(&b, v, n)
	renderThread(&b, v)

	b.WriteString("\n## NEXT ACTIONS\n")
	renderNextActions(&b, v)

	return terminal.Sanitize(b.String())
}

func renderHeader(b *strings.Builder, v *DetailView, n namer) {
	r := v.Review
	fmt.Fprintf(b, "# DATA REVIEW %s\n", r.ID)
	fmt.Fprintf(b, "Row: %d | Table: %d\n", r.RowID, r.DatasetID)
	requester := "(unknown)"
	if r.Requester != nil {
		requester = n.name(r.Requester.ID)
	}
	fmt.Fprintf(b, "Status: %s | Approvals required: %d | Requester: %s\n", r.Status, r.RequiredApprovals, requester)

	if r.AssignedReviewers != nil && len(*r.AssignedReviewers) > 0 {
		names := make([]string, 0, len(*r.AssignedReviewers))
		for _, reviewer := range *r.AssignedReviewers {
			names = append(names, n.name(reviewer.ID))
		}
		fmt.Fprintf(b, "Reviewers: %s\n", strings.Join(names, ", "))
	} else {
		b.WriteString("Reviewers: (none assigned)\n")
	}

	// The backend suppresses release_block_reason on a closed review, so a
	// closed one says when it closed instead.
	if r.Status == api.ReviewRequestStatusClosed {
		if r.ClosedAt != nil {
			fmt.Fprintf(b, "Closed: %s (%s)\n", r.ClosedAt.UTC().Format(time.RFC3339), timeutil.FormatRelative(*r.ClosedAt))
		} else {
			b.WriteString("Closed: (time not recorded)\n")
		}
		return
	}
	if r.ReleaseBlockReason != nil {
		fmt.Fprintf(b, "Held: %s (%s)\n", heldCopy(v, *r.ReleaseBlockReason, n), *r.ReleaseBlockReason)
	}
}

// heldCopy explains release_block_reason in the order the backend checks it:
// a whole-row reject and a half-finished undo hide every cell verdict, so the
// cell counts are only ever quoted for the two cell reasons.
func heldCopy(v *DetailView, reason api.ReleaseBlockReason, n namer) string {
	switch reason {
	case api.CellVeto:
		count := 0
		for _, c := range v.changes() {
			if isBlocking(c.Decision) {
				count++
			}
		}
		return fmt.Sprintf("%d cell(s) need a fix", count)
	case api.CellUndecided:
		count := 0
		for _, c := range v.changes() {
			if isUndecided(c.Decision) {
				count++
			}
		}
		return fmt.Sprintf("%d cell(s) have no verdict yet", count)
	case api.MeterShort:
		return "needs more approvals"
	case api.WholeRowReject:
		// Every standing reject is a veto on its own; name them all.
		var names []string
		for _, d := range v.standingDecisions() {
			if d.Decision == api.ReviewDecisionEnumReject {
				names = append(names, n.name(d.ReviewerID))
			}
		}
		if len(names) == 0 {
			return "rejected by a reviewer"
		}
		return "rejected by " + strings.Join(names, ", ")
	case api.MidUndo:
		return "an undo is mid-flight; retry it first"
	case api.Releasable:
		return "nothing is blocking"
	default:
		return "the backend reports a reason this qctl does not know"
	}
}

func renderSubmissions(b *strings.Builder, r *Review) {
	var groups []api.ReviewGroupRefSchema
	if r.ReviewGroups != nil {
		groups = *r.ReviewGroups
	}
	fmt.Fprintf(b, "\n## SUBMISSIONS (%d)\n", len(groups))
	for _, g := range groups {
		fmt.Fprintf(b, "[g: %s] %q — %s\n", g.ID, g.Title, timeutil.FormatRelative(g.CreatedAt))
		// Plain text, indented line by line: a description is never rendered
		// as markdown, and indenting keeps a "## ..." line inside it from
		// reading as a section of this payload.
		if g.Description == nil || *g.Description == "" {
			b.WriteString("  (no description)\n")
			continue
		}
		for _, line := range strings.Split(*g.Description, "\n") {
			b.WriteString("  " + line + "\n")
		}
	}
}

func renderChanges(b *strings.Builder, v *DetailView, n namer) {
	changes := v.changes()
	fmt.Fprintf(b, "\n## CHANGES (%d)\n", len(changes))
	if len(changes) == 0 {
		b.WriteString("(no changes visible — the row has no live edits, or you lack access to this table's data)\n")
		return
	}
	masked := maskedFieldSet(v.Review.MaskedFields)
	writeChangeLegends(b, changes, masked, v.Review.MaskedFields)
	// changes_since=me is measured from the caller's latest APPROVAL; with no
	// approval standing it lists every edit the row ever had, which says
	// nothing about this caller. So it is quoted only under a standing approve.
	var since map[string][]api.ChangesSinceFieldSchema
	if mine := v.myDecision(); mine != nil && mine.Decision == api.ReviewDecisionEnumApprove && v.Review.ChangesSince != nil {
		since = *v.Review.ChangesSince
	}
	for _, c := range changes {
		oldTag, newTag := unitTags(c, masked)
		fmt.Fprintf(b, "- %s  [h: %d]\n", c.FieldName, c.HistoryID)
		fmt.Fprintf(b, "  before: %s%s → after: %s%s\n",
			quotedOrNull(c.OriginalValue), decimalMark(oldTag),
			quotedOrNull(c.EditedValue), decimalMark(newTag))
		if c.AffectedRowCount != nil && *c.AffectedRowCount > 1 {
			fmt.Fprintf(b, "  affects %d row(s)\n", *c.AffectedRowCount)
		}
		b.WriteString("  verdict: " + cellVerdict(c, n) + "\n")
		if c.Comment != nil && *c.Comment != "" {
			fmt.Fprintf(b, "  comment: %q\n", comments.DeRenderMentions(*c.Comment))
		}
		if c.SuggestedValue != nil && *c.SuggestedValue != "" {
			fmt.Fprintf(b, "  suggests: %q\n", *c.SuggestedValue)
		}
		if line := editedSince(since[c.FieldName]); line != "" {
			b.WriteString("  " + line + "\n")
		}
	}
}

// decimalLegend explains the marker decimalMark adds, once per payload.
//
// It claims nothing about an unmarked side on purpose: `str` does not prove
// source text (a composite table's cell override is tagged `str` whatever it
// holds, and an edit recorded before the server began tagging is `str` too),
// and old_value_type is absent altogether on an item that records no old
// value.
const decimalLegend = "([decimal] = the server recorded this side as a number; an unmarked side may still be one.)\n"

// decimalMark marks an operand the server recorded as a number rather than as
// text, which is what tells a quarantine cell's raw source text from a value in
// the column's own numeric form: `"150" → "0.13" [decimal]` is one cell that
// never converted being overwritten with a number, not a hundredfold cut.
//
// Only `decimal` is marked. It is the single tag that carries a claim here --
// the backend documents it as "'decimal' marks a stored number" and, on a cell
// change, tags the other side `str` whether it is source text or not.
func decimalMark(tag *api.HistoryValueTypes) string {
	if tag != nil && *tag == api.HistoryValueTypesDecimal {
		return " [decimal]"
	}
	return ""
}

// maskedLegend names the review's masked_fields, once per payload. A masked
// change carries no [decimal] mark: its values are text.
const maskedLegend = "(Masked fields: %s. Their values are •••••, or ••••• and the last 4 characters, printed as sent and not as the column's type.)\n"

// writeChangeLegends writes the [decimal] legend when a side carries the
// mark, then the masked legend when the review names masked fields.
func writeChangeLegends(b *strings.Builder, changes []Change, masked map[string]bool, maskedFields *[]string) {
	if anyDecimalSide(changes, masked) {
		b.WriteString(decimalLegend)
	}
	if len(masked) > 0 {
		fmt.Fprintf(b, maskedLegend, strings.Join(*maskedFields, ", "))
	}
}

// unitTags returns a change's old and new unit tags, or nil for both when its
// field is masked.
func unitTags(c Change, masked map[string]bool) (oldTag, newTag *api.HistoryValueTypes) {
	if masked[c.FieldName] {
		return nil, nil
	}
	return c.OldValueType, c.ValueType
}

func maskedFieldSet(names *[]string) map[string]bool {
	if names == nil {
		return nil
	}
	set := make(map[string]bool, len(*names))
	for _, name := range *names {
		set[name] = true
	}
	return set
}

func anyDecimalSide(changes []Change, masked map[string]bool) bool {
	for _, c := range changes {
		oldTag, newTag := unitTags(c, masked)
		if decimalMark(oldTag) != "" || decimalMark(newTag) != "" {
			return true
		}
	}
	return false
}

// UnitTag renders a change's value_type or old_value_type verbatim for an
// optional table column, blank when the change carries none. Unlike
// decimalMark it interprets nothing: a column is raw data.
func UnitTag(tag *api.HistoryValueTypes) string {
	if tag == nil {
		return ""
	}
	return string(*tag)
}

func quotedOrNull(s *string) string {
	if s == nil {
		return "∅"
	}
	return fmt.Sprintf("%q", *s)
}

func cellVerdict(c Change, n namer) string {
	if c.Decision == nil {
		return "undecided"
	}
	verdict := string(*c.Decision)
	if *c.Decision == api.ReviewDecisionEnumCommentOnly {
		verdict += " (not a decision; the cell still counts as undecided)"
	}
	if c.DecidedByID != nil {
		verdict += " by " + n.name(*c.DecidedByID)
		if c.DecidedAt != nil {
			verdict += " · " + timeutil.FormatRelative(*c.DecidedAt)
		}
	}
	return verdict
}

// editedSince quotes the newest dated edit. The entries are the row's log in
// append order; an undated one is the baseline value, not an edit.
func editedSince(entries []api.ChangesSinceFieldSchema) string {
	var dated []api.ChangesSinceFieldSchema
	for _, e := range entries {
		if e.Datetime != nil && *e.Datetime != "" {
			dated = append(dated, e)
		}
	}
	if len(dated) == 0 {
		return ""
	}
	last := dated[len(dated)-1]
	why := "edited"
	if last.Why != nil && *last.Why != "" {
		why = *last.Why
	}
	line := fmt.Sprintf("edited since your approval (%s · %s)", why, *last.Datetime)
	if more := len(dated) - 1; more > 0 {
		line += fmt.Sprintf(" and %d earlier edit(s)", more)
	}
	return line
}

func renderDecisions(b *strings.Builder, v *DetailView, n namer) {
	decisions := v.standingDecisions()
	requester := v.requesterID()
	fmt.Fprintf(b, "\n## DECISIONS (%d)\n", len(decisions))
	for _, d := range decisions {
		label := n.name(d.ReviewerID)
		isRequester := requester != nil && *requester == d.ReviewerID
		if isRequester {
			label += " (requester)"
		}
		fmt.Fprintf(b, "- %s: %s · first decided %s", label, d.Decision, timeutil.FormatRelative(d.CreatedAt))
		if d.Decision == api.ReviewDecisionEnumApprove && isRequester {
			b.WriteString(" (a requester's approval never counts)")
		}
		b.WriteString("\n")
		if d.Comment != nil && *d.Comment != "" {
			fmt.Fprintf(b, "  comment: %q\n", comments.DeRenderMentions(*d.Comment))
		}
	}
	if p := v.ApprovalProgress; p != nil && p.CurrentApprovals != nil && p.RequiredApprovals != nil {
		fmt.Fprintf(b, "Approvals: %d of %d required (current eligible approvals)\n", *p.CurrentApprovals, *p.RequiredApprovals)
	} else {
		fmt.Fprintf(b, "Approvals: unavailable (%d required)\n", v.Review.RequiredApprovals)
	}
}

func renderVets(b *strings.Builder, v *DetailView, n namer) {
	vets := v.vets()
	if len(vets) == 0 {
		return
	}
	fields := map[int]string{}
	for _, c := range v.changes() {
		fields[c.HistoryID] = c.FieldName
	}
	fmt.Fprintf(b, "\n## ALSO WEIGHED IN (%d)\n", len(vets))
	for _, vet := range vets {
		fmt.Fprintf(b, "- %s: %s on [h: %d]", n.name(vet.DecidedByID), vet.Decision, vet.HistoryID)
		if field, ok := fields[vet.HistoryID]; ok {
			fmt.Fprintf(b, " (%s)", field)
		}
		b.WriteString("\n")
	}
}

func renderThread(b *strings.Builder, v *DetailView) {
	r := v.Review
	switch {
	case r.ThreadID == nil:
		b.WriteString("\n## COMMENTS (0 top-level, 0 total)\n(none yet)\n")
	case v.Thread == nil:
		count := 0
		if r.CommentCount != nil {
			count = *r.CommentCount
		}
		fmt.Fprintf(b, "\n## COMMENTS\ncomments: %d (thread %s not readable: %s)\n",
			count, *r.ThreadID, threadFailure(v.ThreadErr))
	default:
		var thread []Comment
		if v.Thread.Comments != nil {
			thread = *v.Thread.Comments
		}
		fmt.Fprintf(b, "\n## COMMENTS (%d top-level, %d total)\n", len(thread), comments.CountComments(thread))
		fmt.Fprintf(b, "Thread: %s\n", *r.ThreadID)
		for i := range thread {
			comments.RenderComment(b, &thread[i], 0)
		}
	}
}

func threadFailure(err error) string {
	if err == nil {
		return "not fetched"
	}
	switch qerrors.GetExitCode(err) {
	case qerrors.ExitUnauthorized:
		return "access denied; reading it needs comment read permission and access to this table's data"
	case qerrors.ExitNotFound:
		return "not found"
	}
	msg, _, _ := strings.Cut(err.Error(), "\n")
	return msg
}

// renderNextActions writes status-aware, copyable commands with full ids.
func renderNextActions(b *strings.Builder, v *DetailView) {
	r := v.Review
	id := r.ID.String()
	switch r.Status {
	case api.ReviewRequestStatusOpen, api.ReviewRequestStatusChangesRequested:
		nextAction(b, "approve the row", "qctl data-review approve "+id)
		nextAction(b, "comment", fmt.Sprintf("qctl data-review comment %s --body \"...\"", id))
		// Offer withdrawal to a non-requester with a standing decision.
		if mine := v.myDecision(); mine != nil {
			if req := v.requesterID(); req == nil || *req != mine.ReviewerID {
				nextAction(b, "withdraw your decision", "qctl data-review withdraw "+id)
			}
		}
		if r.ThreadID != nil {
			nextAction(b, "reply in the thread", fmt.Sprintf(
				"qctl comment-thread reply %s --table-id %d --body \"...\"", *r.ThreadID, r.DatasetID))
		}
	case api.ReviewRequestStatusClosed:
		nextAction(b, "submit the row again", fmt.Sprintf(
			"qctl data-review request --table-id %d --row-id %d --title \"...\"", r.DatasetID, r.RowID))
		nextAction(b, "the table's submissions", fmt.Sprintf(
			"qctl get data-review-groups --table-id %d --include-closed", r.DatasetID))
	default: // approved, and any status newer than this qctl: reads only
		// The default group listing hides a submission with no open or
		// changes_requested member, which is what an approved review's is.
		nextAction(b, "the table's submissions", fmt.Sprintf(
			"qctl get data-review-groups --table-id %d --include-closed", r.DatasetID))
	}
	nextAction(b, "the table's queue", fmt.Sprintf("qctl get data-reviews --table-id %d", r.DatasetID))
}

func nextAction(b *strings.Builder, label, command string) {
	fmt.Fprintf(b, "- %-24s %s\n", label+":", command)
}
