package comment_threads //nolint:revive // Match the established package under test.

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
)

func ptr[T any](v T) *T { return &v }

// whole_row and field_names are INDEPENDENT: a thread may target the row as a
// whole AND some of its cells. Neither may be inferred from the other.
func TestRenderTargetsKeepsWholeRowAndFieldsIndependent(t *testing.T) {
	thread := &Thread{Targets: &[]api.CommentThreadTargetResponseSchema{
		{RowID: 1042, FieldName: nil},           // whole row
		{RowID: 1042, FieldName: ptr("amount")}, // and one of its cells
	}}
	got := RenderTargets(thread)
	if !strings.Contains(got, "whole-row") {
		t.Errorf("whole-row target was dropped: %q", got)
	}
	if !strings.Contains(got, "amount") {
		t.Errorf("cell target was dropped: %q", got)
	}
}

// "(none)" is the difference between "we asked and there are none" and "we
// never looked": Targets is an omitempty pointer, so both shapes reach here.
func TestRenderTargetsWithNoTargets(t *testing.T) {
	if got := RenderTargets(&Thread{}); got != "(none)" {
		t.Errorf("RenderTargets(nil targets) = %q, want %q", got, "(none)")
	}
	empty := &Thread{Targets: &[]api.CommentThreadTargetResponseSchema{}}
	if got := RenderTargets(empty); got != "(none)" {
		t.Errorf("RenderTargets(empty targets) = %q, want %q", got, "(none)")
	}
}

// Targets are grouped by row and ordered by row id, so two renders of the same
// thread are byte-identical — map iteration order must not reach the output.
func TestRenderTargetsGroupsByRowInAscendingOrder(t *testing.T) {
	thread := &Thread{Targets: &[]api.CommentThreadTargetResponseSchema{
		{RowID: 1043, FieldName: ptr("currency")},
		{RowID: 1042, FieldName: ptr("amount")},
		{RowID: 1042, FieldName: ptr("total")},
	}}
	want := "1042=amount,total 1043=currency"
	if got := RenderTargets(thread); got != want {
		t.Errorf("RenderTargets = %q, want %q", got, want)
	}
}

func sampleRenderThread() *Thread {
	created := time.Now().Add(-3 * time.Hour)
	return &Thread{
		ID:          uuid.MustParse("0190bbbb-1111-2222-3333-444444444444"),
		CreatedAt:   created,
		CreatedByID: uuid.MustParse("0190dddd-1111-2222-3333-444444444444"),
		CreatedBy:   &api.UserInfoTinyDictSchema{FirstName: "Ada", LastName: "Lovelace"},
		Title:       ptr("suspect totals"),
		Targets: &[]api.CommentThreadTargetResponseSchema{
			{RowID: 1042, FieldName: ptr("amount")},
		},
		Comments: &[]Comment{{
			ID:        uuid.MustParse("0190cccc-1111-2222-3333-444444444444"),
			AuthorID:  uuid.MustParse("0190dddd-1111-2222-3333-444444444444"),
			Author:    &api.UserInfoTinyDictSchema{FirstName: "Ada", LastName: "Lovelace"},
			CreatedAt: created,
			Body:      "this total looks wrong",
			Replies: &[]Comment{{
				ID:        uuid.MustParse("0190cccc-5555-6666-7777-888888888888"),
				AuthorID:  uuid.MustParse("0190dddd-1111-2222-3333-444444444444"),
				CreatedAt: created,
				Body:      "agreed",
			}},
		}},
	}
}

// The transcript must carry the [c: <uuid>] tags the reply/edit/delete verbs
// take verbatim, and a total that counts nested replies rather than only the
// top-level comments.
func TestRenderThreadCarriesCommentTagsAndTotals(t *testing.T) {
	got := RenderThread(sampleRenderThread())
	for _, want := range []string{
		"# COMMENT THREAD 0190bbbb-1111-2222-3333-444444444444",
		"Title: suspect totals",
		"Targets: 1042=amount",
		"Opened by: Ada Lovelace",
		"Status: open",
		"## COMMENTS (1 top-level, 2 total)",
		"[c: 0190cccc-1111-2222-3333-444444444444]",
		"[c: 0190cccc-5555-6666-7777-888888888888]",
		"this total looks wrong",
		"agreed",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("RenderThread output missing %q\n%s", want, got)
		}
	}
}

// The backend resolves threads on its own, stamping resolved_reason
// "row_gone" once every target row is gone. A status line that said only
// "resolved" would send the reader hunting for a teammate who never touched it.
func TestRenderThreadReportsWhyAThreadIsResolved(t *testing.T) {
	thread := sampleRenderThread()
	thread.ResolvedAt = ptr(time.Now().Add(-time.Hour))
	thread.ResolvedReason = ptr("row_gone")
	got := RenderThread(thread)
	if !strings.Contains(got, "Status: resolved") {
		t.Errorf("status line missing:\n%s", got)
	}
	if !strings.Contains(got, "row_gone") {
		t.Errorf("resolved reason missing from status line:\n%s", got)
	}
}

// An untitled thread must not print an empty "Title:" line.
func TestRenderThreadOmitsAnEmptyTitle(t *testing.T) {
	thread := sampleRenderThread()
	thread.Title = nil
	if got := RenderThread(thread); strings.Contains(got, "Title:") {
		t.Errorf("nil title still printed a Title line:\n%s", got)
	}
	thread.Title = ptr("")
	if got := RenderThread(thread); strings.Contains(got, "Title:") {
		t.Errorf("empty title still printed a Title line:\n%s", got)
	}
}

func TestThreadDuplicateGroup(t *testing.T) {
	thread := sampleRenderThread()
	if got := RenderThread(thread); strings.Contains(got, "Duplicate group:") {
		t.Errorf("ordinary thread names a group:\n%s", got)
	}
	if got := SummaryRows([]Thread{*thread})[0].DuplicateGroup; got != "-" {
		t.Errorf("ordinary thread group = %q, want -", got)
	}
	groupID := uuid.MustParse("0190aaaa-1111-4222-8333-444444444444")
	thread.DuplicateGroupID = &groupID
	if got := RenderThread(thread); !strings.Contains(got, "Duplicate group: duplicate-group/"+groupID.String()+"\n") {
		t.Errorf("group reference missing:\n%s", got)
	}
	if got := SummaryRows([]Thread{*thread})[0].DuplicateGroup; got != groupID.String() {
		t.Errorf("group = %q, want %s", got, groupID)
	}
}

func TestSummaryRowsProjectsEveryColumn(t *testing.T) {
	thread := sampleRenderThread()
	thread.Comments = nil
	thread.CommentCount = ptr(2)
	thread.Targets = &[]api.CommentThreadTargetResponseSchema{
		{RowID: 1042, FieldName: nil},
		{RowID: 1042, FieldName: ptr("amount")},
		{RowID: 1043, FieldName: ptr("amount")},
	}

	rows := SummaryRows([]Thread{*thread})
	if len(rows) != 1 {
		t.Fatalf("SummaryRows returned %d rows, want 1", len(rows))
	}
	row := rows[0]
	if row.ID != "0190bbbb-1111-2222-3333-444444444444" {
		t.Errorf("ID = %q", row.ID)
	}
	if row.Rows != "1042,1043" {
		t.Errorf("Rows = %q, want %q", row.Rows, "1042,1043")
	}
	// whole-row is a target in its own right, not the absence of fields.
	if row.Fields != "whole-row,amount" {
		t.Errorf("Fields = %q, want %q", row.Fields, "whole-row,amount")
	}
	if row.Comments != 2 {
		t.Errorf("Comments = %d, want 2", row.Comments)
	}
	if row.Resolved != "no" {
		t.Errorf("Resolved = %q, want %q", row.Resolved, "no")
	}
	if row.Title != "suspect totals" {
		t.Errorf("Title = %q", row.Title)
	}
}

// comment_count is omitempty on the wire, so a response that omits it must
// still report a total — counted from the transcript, replies included.
func TestSummaryRowsFallsBackToCountingTheTranscript(t *testing.T) {
	thread := sampleRenderThread()
	thread.CommentCount = nil
	rows := SummaryRows([]Thread{*thread})
	if rows[0].Comments != 2 {
		t.Errorf("Comments = %d, want 2 (one top-level plus its reply)", rows[0].Comments)
	}
}

func TestSummaryRowsMarksResolvedThreads(t *testing.T) {
	thread := sampleRenderThread()
	thread.ResolvedAt = ptr(time.Now())
	rows := SummaryRows([]Thread{*thread})
	if rows[0].Resolved != "yes" {
		t.Errorf("Resolved = %q, want %q", rows[0].Resolved, "yes")
	}
}

func TestRenderThreadAppendsTheCreatorAndAuthorRoles(t *testing.T) {
	role := api.RoleEnum("external")
	thread := sampleRenderThread()
	thread.CreatedBy.Role = &role
	(*thread.Comments)[0].Author.Role = &role

	got := RenderThread(thread)
	for _, want := range []string{
		"Opened by: Ada Lovelace (external) (3 hours ago)\n",
		"[c: 0190cccc-1111-2222-3333-444444444444] Ada Lovelace (external) (3 hours ago):",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("RenderThread output missing %q\n%s", want, got)
		}
	}

	got = RenderThread(sampleRenderThread())
	for _, want := range []string{
		"Opened by: Ada Lovelace (3 hours ago)\n",
		"[c: 0190cccc-1111-2222-3333-444444444444] Ada Lovelace (3 hours ago):",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("RenderThread output without roles missing %q\n%s", want, got)
		}
	}
}
