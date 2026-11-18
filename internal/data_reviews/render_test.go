package data_reviews //nolint:revive // Match the established package under test.

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

var (
	rdReviewID    = uuid.MustParse("0190aaaa-0000-4000-8000-000000000001")
	rdGroupID     = uuid.MustParse("0190aaaa-0000-4000-8000-000000000002")
	rdThreadID    = uuid.MustParse("0190aaaa-0000-4000-8000-000000000003")
	rdCommentID   = uuid.MustParse("0190aaaa-0000-4000-8000-000000000004")
	rdReplyID     = uuid.MustParse("0190aaaa-0000-4000-8000-000000000005")
	rdRequesterID = uuid.MustParse("0190bbbb-0000-4000-8000-000000000001")
	rdAliceID     = uuid.MustParse("0190bbbb-0000-4000-8000-000000000002")
	rdBobID       = uuid.MustParse("0190bbbb-0000-4000-8000-000000000003")
	rdVetterID    = uuid.MustParse("0190bbbb-0000-4000-8000-000000000004")
)

func rdPtr[T any](v T) *T { return &v }

func rdUser(id uuid.UUID, first, last string) api.UserInfoTinyDictSchema {
	return api.UserInfoTinyDictSchema{ID: id, FirstName: first, LastName: last,
		Email: strings.ToLower(first) + "@corp.com"}
}

// rdView is the acceptance fixture: two changes (one decided with a comment
// and a suggestion, one undecided), three decisions across two reviewers (one
// re-decided), one unassigned vet, and a thread with a reply.
func rdView(status api.ReviewRequestStatus) *DetailView {
	now := time.Now()
	alice, bob := rdUser(rdAliceID, "Alice", "Chen"), rdUser(rdBobID, "Bob", "Ng")
	requester := rdUser(rdRequesterID, "Rita", "Req")
	changes := []Change{
		{
			FieldName: "amount", HistoryID: 811,
			OriginalValue: rdPtr("10"), EditedValue: rdPtr("100"),
			AffectedRowCount: rdPtr(3),
			Decision:         rdPtr(api.ReviewDecisionEnumRequestChanges),
			DecidedByID:      &rdBobID, DecidedBy: &bob,
			DecidedAt:      rdPtr(now.Add(-2 * time.Hour)),
			Comment:        rdPtr("too large"),
			SuggestedValue: rdPtr("50"),
		},
		{FieldName: "email", HistoryID: 812, OriginalValue: nil, EditedValue: rdPtr("a@b.co"), AffectedRowCount: rdPtr(1)},
	}
	reviewers := []api.UserInfoTinyDictSchema{alice, bob}
	vets := []api.UnassignedFieldVetSchema{
		{HistoryID: 812, DecidedByID: rdVetterID, Decision: api.ReviewDecisionEnumCommentOnly},
		{HistoryID: 812, DecidedByID: rdVetterID, Decision: api.ReviewDecisionEnumCommentOnly},
	}
	groups := []api.ReviewGroupRefSchema{
		{ID: rdGroupID, Title: "March fixes", Description: rdPtr("Fixes from the audit.\nSee ticket 12."), CreatedAt: now.Add(-48 * time.Hour)},
	}
	reply := []Comment{{ID: rdReplyID, Body: "agreed", AuthorID: rdAliceID, Author: &alice, CreatedAt: now.Add(-time.Hour)}}
	thread := []Comment{{ID: rdCommentID, Body: "why 100?", AuthorID: rdBobID, Author: &bob,
		CreatedAt: now.Add(-3 * time.Hour), Replies: &reply}}
	reason := api.CellVeto
	return &DetailView{
		Review: &Review{
			ID: rdReviewID, RowID: 7, DatasetID: 42, Status: status, RequiredApprovals: 2,
			Requester: &requester, AssignedReviewers: &reviewers,
			ReleaseBlockReason: &reason,
			Changes:            &changes, UnassignedVets: &vets, ReviewGroups: &groups,
			ThreadID: &rdThreadID, CommentCount: rdPtr(2),
			CreatedAt: now.Add(-48 * time.Hour), UpdatedAt: now,
		},
		Decisions: []Decision{
			{ReviewerID: rdAliceID, Decision: api.ReviewDecisionEnumReject, CreatedAt: now.Add(-5 * time.Hour), Comment: rdPtr("first look")},
			{ReviewerID: rdBobID, Decision: api.ReviewDecisionEnumApprove, CreatedAt: now.Add(-4 * time.Hour)},
			{ReviewerID: rdAliceID, Decision: api.ReviewDecisionEnumApprove, CreatedAt: now.Add(-1 * time.Hour), Comment: rdPtr("fine now")},
		},
		ApprovalProgress: &RowOverlay{CurrentApprovals: rdPtr(2), RequiredApprovals: rdPtr(2)},
		Thread:           &api.CommentThreadResponseSchema{ID: rdThreadID, Comments: &thread},
	}
}

func assertInOrder(t *testing.T, out string, wants ...string) {
	t.Helper()
	at := 0
	for _, want := range wants {
		i := strings.Index(out[at:], want)
		if i < 0 {
			t.Fatalf("missing %q after byte %d\n---\n%s", want, at, out)
		}
		at += i + len(want)
	}
}

func TestRenderDetailFrozenSectionOrderAndTags(t *testing.T) {
	out := RenderDetail(rdView(api.ReviewRequestStatusChangesRequested))
	assertInOrder(t, out,
		"# DATA REVIEW "+rdReviewID.String()+"\n",
		"Row: 7 | Table: 42\n",
		"Status: changes_requested | Approvals required: 2 | Requester: Rita Req\n",
		"Reviewers: Alice Chen, Bob Ng\n",
		"Held: 1 cell(s) need a fix (cell_veto)\n",
		"\n## SUBMISSIONS (1)\n",
		`[g: `+rdGroupID.String()+`] "March fixes" — 2 days ago`,
		"  Fixes from the audit.\n  See ticket 12.\n",
		"\n## CHANGES (2)\n",
		"- amount  [h: 811]\n",
		`  before: "10" → after: "100"`,
		"  affects 3 row(s)\n",
		"  verdict: request_changes by Bob Ng · 2 hours ago\n",
		`  comment: "too large"`,
		`  suggests: "50"`,
		"- email  [h: 812]\n",
		`  before: ∅ → after: "a@b.co"`,
		"  verdict: undecided\n",
		"\n## DECISIONS (2)\n",
		"- Alice Chen: approve · first decided 1 hour ago\n",
		`  comment: "fine now"`,
		"- Bob Ng: approve · first decided 4 hours ago\n",
		"Approvals: 2 of 2 required (current eligible approvals)",
		"\n## ALSO WEIGHED IN (1)\n",
		"- "+rdVetterID.String()+": comment_only on [h: 812] (email)\n",
		"\n## COMMENTS (1 top-level, 2 total)\n",
		"Thread: "+rdThreadID.String()+"\n",
		"[c: "+rdCommentID.String()+"] Bob Ng",
		"[c: "+rdReplyID.String()+"] ↳ Alice Chen",
		"\n## NEXT ACTIONS\n",
		"qctl data-review approve "+rdReviewID.String()+"\n",
		"qctl data-review comment "+rdReviewID.String()+` --body "..."`,
		"qctl comment-thread reply "+rdThreadID.String()+` --table-id 42 --body "..."`,
		"qctl get data-reviews --table-id 42\n",
	)
	if strings.Contains(out, "affects 1 row(s)") {
		t.Errorf("a change affecting one row must not print an affects line\n%s", out)
	}
	if strings.Contains(out, "first look") {
		t.Errorf("a superseded decision leaked into the payload\n%s", out)
	}
}

func TestRenderDetailApprovalsUseServerMeter(t *testing.T) {
	v := rdView(api.ReviewRequestStatusOpen)
	v.ApprovalProgress = &RowOverlay{CurrentApprovals: rdPtr(0), RequiredApprovals: rdPtr(2)}
	v.Decisions = []Decision{
		{ReviewerID: rdRequesterID, Decision: api.ReviewDecisionEnumApprove, CreatedAt: time.Now().Add(-3 * time.Hour)},
		{ReviewerID: rdAliceID, Decision: api.ReviewDecisionEnumApprove, CreatedAt: time.Now().Add(-2 * time.Hour)},
		{ReviewerID: rdBobID, Decision: api.ReviewDecisionEnumReject, CreatedAt: time.Now().Add(-1 * time.Hour)},
	}
	out := RenderDetail(v)
	for _, want := range []string{
		"## DECISIONS (3)\n",
		"- Rita Req (requester): approve · first decided 3 hours ago (a requester's approval never counts)\n",
		"- Alice Chen: approve",
		"- Bob Ng: reject",
		"Approvals: 0 of 2 required (current eligible approvals)\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q\n---\n%s", want, out)
		}
	}
}

func TestRenderDetailHeldLine(t *testing.T) {
	tests := []struct {
		reason *api.ReleaseBlockReason
		want   string
	}{
		{rdPtr(api.CellVeto), "Held: 1 cell(s) need a fix (cell_veto)\n"},
		// email has no ledger row: the only cell without a verdict.
		{rdPtr(api.CellUndecided), "Held: 1 cell(s) have no verdict yet (cell_undecided)\n"},
		{rdPtr(api.MeterShort), "Held: needs more approvals (meter_short)\n"},
		{rdPtr(api.WholeRowReject), "Held: rejected by Carl (whole_row_reject)\n"},
		{rdPtr(api.MidUndo), "Held: an undo is mid-flight; retry it first (mid_undo)\n"},
		{rdPtr(api.Releasable), "Held: nothing is blocking (releasable)\n"},
	}
	for _, tt := range tests {
		t.Run(string(*tt.reason), func(t *testing.T) {
			v := rdView(api.ReviewRequestStatusOpen)
			v.Review.ReleaseBlockReason = tt.reason
			v.Decisions = []Decision{{ReviewerID: rdAliceID, Decision: api.ReviewDecisionEnumApprove, CreatedAt: time.Now()}}
			if *tt.reason == api.WholeRowReject {
				v.Decisions = append(v.Decisions, Decision{ReviewerID: rdVetterID, Decision: api.ReviewDecisionEnumReject, CreatedAt: time.Now()})
				v.Directory = nil
				v.Review.AssignedReviewers = &[]api.UserInfoTinyDictSchema{rdUser(rdVetterID, "Carl", "")}
			}
			if out := RenderDetail(v); !strings.Contains(out, tt.want) {
				t.Errorf("missing %q\n---\n%s", tt.want, out)
			}
		})
	}

	t.Run("comment_only counts as undecided", func(t *testing.T) {
		v := rdView(api.ReviewRequestStatusOpen)
		v.Review.ReleaseBlockReason = rdPtr(api.CellUndecided)
		(*v.Review.Changes)[0].Decision = rdPtr(api.ReviewDecisionEnumCommentOnly)
		out := RenderDetail(v)
		for _, want := range []string{
			"Held: 2 cell(s) have no verdict yet (cell_undecided)\n",
			"verdict: comment_only (not a decision; the cell still counts as undecided) by Bob Ng",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("missing %q\n---\n%s", want, out)
			}
		}
	})

	t.Run("not computed", func(t *testing.T) {
		v := rdView(api.ReviewRequestStatusOpen)
		v.Review.ReleaseBlockReason = nil
		if out := RenderDetail(v); strings.Contains(out, "Held:") {
			t.Errorf("a nil release_block_reason must omit the line\n%s", out)
		}
	})

	t.Run("closed", func(t *testing.T) {
		v := rdView(api.ReviewRequestStatusClosed)
		closed := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
		v.Review.ClosedAt = &closed
		out := RenderDetail(v)
		if strings.Contains(out, "Held:") || !strings.Contains(out, "Closed: 2026-09-01T12:00:00Z (") {
			t.Errorf("a closed review says when it closed instead of what holds it\n%s", out)
		}
	})
}

func TestRenderDetailEmptyChanges(t *testing.T) {
	v := rdView(api.ReviewRequestStatusOpen)
	v.Review.Changes = &[]Change{}
	out := RenderDetail(v)
	assertInOrder(t, out,
		"## CHANGES (0)\n(no changes visible — the row has no live edits, or you lack access to this table's data)\n",
		"## DECISIONS")
	if strings.Contains(out, "(email)") {
		t.Errorf("a vet's field is known only from the change list\n%s", out)
	}
}

func TestRenderDetailWithdrawNeedsYourOwnReviewerDecision(t *testing.T) {
	withdraw := "qctl data-review withdraw " + rdReviewID.String()
	tests := []struct {
		name string
		me   *openapi_types.UUID
		want bool
	}{
		{"caller unknown", nil, false},
		{"caller decided", &rdBobID, true},
		{"caller has no decision", &rdVetterID, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := rdView(api.ReviewRequestStatusOpen)
			v.Me = tt.me
			if got := strings.Contains(RenderDetail(v), withdraw); got != tt.want {
				t.Errorf("withdraw offered = %t, want %t", got, tt.want)
			}
		})
	}

	t.Run("caller is the requester", func(t *testing.T) {
		v := rdView(api.ReviewRequestStatusOpen)
		v.Me = &rdRequesterID
		v.Decisions = append(v.Decisions, Decision{ReviewerID: rdRequesterID, Decision: api.ReviewDecisionEnumApprove, CreatedAt: time.Now()})
		out := RenderDetail(v)
		if strings.Contains(out, withdraw) {
			t.Errorf("a requester's inert approval is not offered a withdraw\n%s", out)
		}
		if !strings.Contains(out, "- Rita Req (you) (requester): approve") {
			t.Errorf("the caller's own decision is not marked\n%s", out)
		}
	})
}

func TestRenderDetailEditedSinceOnlyUnderYourApproval(t *testing.T) {
	since := map[string][]api.ChangesSinceFieldSchema{
		"amount": {
			{Why: nil, Datetime: nil}, // the undated baseline value, not an edit
			{Why: rdPtr("Overwritten by user for only this instance."), Datetime: rdPtr("2026-09-15T10:00:00+00:00")},
			{Why: rdPtr("Search and replace by user."), Datetime: rdPtr("2026-09-16T09:30:00+00:00")},
		},
	}
	line := "  edited since your approval (Search and replace by user. · 2026-09-16T09:30:00+00:00) and 1 earlier edit(s)\n"
	tests := []struct {
		name string
		me   *openapi_types.UUID
		want bool
	}{
		{"your standing approve", &rdBobID, true},
		// Alice's standing decision is approve too, but after she first
		// rejected — only the decision in force matters.
		{"another approver", &rdAliceID, true},
		{"no decision of yours", &rdVetterID, false},
		{"caller unknown", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := rdView(api.ReviewRequestStatusOpen)
			v.Review.ChangesSince = &since
			v.Me = tt.me
			out := RenderDetail(v)
			if got := strings.Contains(out, line); got != tt.want {
				t.Errorf("edited-since shown = %t, want %t\n---\n%s", got, tt.want, out)
			}
		})
	}

	t.Run("a standing reject hides it", func(t *testing.T) {
		v := rdView(api.ReviewRequestStatusOpen)
		v.Review.ChangesSince = &since
		v.Decisions = []Decision{{ReviewerID: rdBobID, Decision: api.ReviewDecisionEnumReject, CreatedAt: time.Now()}}
		v.Me = &rdBobID
		if out := RenderDetail(v); strings.Contains(out, "edited since") {
			t.Errorf("without a standing approval the map is every edit ever made\n%s", out)
		}
	})
}

func TestRenderDetailThreadStates(t *testing.T) {
	t.Run("no thread yet", func(t *testing.T) {
		v := rdView(api.ReviewRequestStatusOpen)
		v.Review.ThreadID, v.Thread = nil, nil
		out := RenderDetail(v)
		assertInOrder(t, out, "## COMMENTS (0 top-level, 0 total)\n(none yet)\n", "## NEXT ACTIONS")
		if strings.Contains(out, "comment-thread reply") {
			t.Errorf("no thread means nothing to reply to\n%s", out)
		}
	})

	for _, tt := range []struct {
		name string
		err  error
		want string
	}{
		{"forbidden", qerrors.FromHTTPStatusWithCause(403, fmt.Errorf("denied"), "get comment thread failed"),
			"access denied; reading it needs comment read permission and access to this table's data"},
		{"missing", qerrors.FromHTTPStatusWithCause(404, fmt.Errorf("gone"), "get comment thread failed"), "not found"},
		{"other", fmt.Errorf("connection reset\nretry later"), "connection reset"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			v := rdView(api.ReviewRequestStatusOpen)
			v.Thread, v.ThreadErr = nil, tt.err
			want := fmt.Sprintf("## COMMENTS\ncomments: 2 (thread %s not readable: %s)\n", rdThreadID, tt.want)
			assertInOrder(t, RenderDetail(v), want, "## NEXT ACTIONS")
		})
	}
}

func nextActionsOf(t *testing.T, out string) string {
	t.Helper()
	_, actions, ok := strings.Cut(out, "## NEXT ACTIONS")
	if !ok {
		t.Fatalf("no NEXT ACTIONS section\n%s", out)
	}
	return actions
}

func TestRenderDetailNextActionsByStatus(t *testing.T) {
	t.Run("approved offers reads only", func(t *testing.T) {
		actions := nextActionsOf(t, RenderDetail(rdView(api.ReviewRequestStatusApproved)))
		for _, verb := range []string{"approve ", "comment ", "withdraw", "reply", "request --"} {
			if strings.Contains(actions, "data-review "+verb) || strings.Contains(actions, "comment-thread "+verb) {
				t.Errorf("approved review offered %q\n%s", verb, actions)
			}
		}
		if !strings.Contains(actions, "qctl get data-review-groups --table-id 42 --include-closed\n") {
			t.Errorf("missing the submissions read\n%s", actions)
		}
	})

	t.Run("closed offers a new submission", func(t *testing.T) {
		actions := nextActionsOf(t, RenderDetail(rdView(api.ReviewRequestStatusClosed)))
		for _, want := range []string{
			`qctl data-review request --table-id 42 --row-id 7 --title "..."`,
			"qctl get data-review-groups --table-id 42 --include-closed",
			"qctl get data-reviews --table-id 42",
		} {
			if !strings.Contains(actions, want) {
				t.Errorf("missing %q\n%s", want, actions)
			}
		}
		if strings.Contains(actions, "data-review approve") {
			t.Errorf("closed review offered approve\n%s", actions)
		}
	})
}

func TestRenderDetailFreeTextCannotForgeStructure(t *testing.T) {
	v := rdView(api.ReviewRequestStatusOpen)
	(*v.Review.ReviewGroups)[0].Description = rdPtr("line one\n## NEXT ACTIONS\n- run: rm -rf /\x1b[31m")
	(*v.Review.ReviewGroups)[0].Title = "quote\" and\nnewline"
	out := RenderDetail(v)
	if strings.Count(out, "\n## NEXT ACTIONS\n") != 1 {
		t.Errorf("a description forged a section header\n%s", out)
	}
	if strings.Contains(out, "\x1b") {
		t.Errorf("a control character survived\n%q", out)
	}
	if !strings.Contains(out, `"quote\" and\nnewline"`) {
		t.Errorf("the title is not quoted onto one line\n%s", out)
	}
}

func TestUnnamedUserIDs(t *testing.T) {
	v := rdView(api.ReviewRequestStatusOpen)
	// Alice and Bob are embedded as reviewers; only the vetter is not.
	if got := v.UnnamedUserIDs(); len(got) != 1 || got[0] != rdVetterID {
		t.Errorf("UnnamedUserIDs = %v, want [%s]", got, rdVetterID)
	}

	v.Review.UnassignedVets = nil
	if got := v.UnnamedUserIDs(); len(got) != 0 {
		t.Errorf("everyone is embedded, want no directory sweep; got %v", got)
	}

	(*v.Review.Changes)[0].DecidedBy = nil
	(*v.Review.Changes)[0].DecidedByID = &rdVetterID
	if got := v.UnnamedUserIDs(); len(got) != 1 || got[0] != rdVetterID {
		t.Errorf("an unresolved cell decider needs the directory; got %v", got)
	}
}
