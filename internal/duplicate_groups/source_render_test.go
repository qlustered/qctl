package duplicate_groups //nolint:revive // Matches the backend resource name.

import (
	"testing"

	"github.com/qlustered/qctl/internal/api"
)

func testSourceGroup() *SourceGroup {
	held := SenderOutcome("held")
	loaded := SenderOutcome("loaded")
	return &SourceGroup{
		ID:           testGroupID,
		Origin:       OriginRepeatedKey,
		State:        SenderHeld,
		HeldReason:   ptr(HeldMoreThanAllowed),
		StoredItemID: ptr(311),
		CreatedAt:    testTime,
		KeyValues:    &map[string]interface{}{"policy_id": "P-10", "email": "a***@x"},
		MaskedFields: &[]string{"email"},
		Caller:       SourceCaller{CanRespond: true},
		Members: []SourceMember{
			{ID: 812, RowID: 1042, DataSourceModelID: 5, Location: "quarantine", StoredItemID: ptr(311),
				FileRowNumber: ptr(17), Outcome: &held},
			{ID: 813, RowID: 1043, DataSourceModelID: 5, Location: "clean", StoredItemID: ptr(311),
				FileRowNumber: ptr(18), Outcome: &loaded},
		},
	}
}

func TestRenderSourceDetailShowsEverySection(t *testing.T) {
	g := testSourceGroup()
	g.Decision = &SourceDecision{Kind: KindAllow, AllowedCount: ptr(1), Carried: true, PeriodKey: ptr("2026-09"),
		EffectiveAt: &testTime, Deciders: []string{"Ann", "Bob"}}
	g.DifferingCells = []DifferingCell{{Column: "amount", Values: []api.DuplicateCellValueSchema{
		{MemberID: 812, Value: 10.5}, {MemberID: 813, Value: 11.0}}}}
	g.Responses = []SourceResponse{testSourceResponse(ResponseCorrectedFileFollows)}
	g.AwaitingCorrectedFile = true
	ended := testSourceResponse(ResponseSeparateRows)
	ended.EndedAt = &testTime
	ended.EndReason = ptr(api.DuplicateGroupResponseEndReason("replaced"))
	g.EarlierResponses = []SourceResponse{ended}
	g.CommentThreadID = &testThread

	out := RenderSourceDetail(42, g)
	assertContains(t, out,
		"duplicate-group/0190aaaa-1111-4222-8333-444444444444 (table/42, as your cloud sources see it)",
		"State:     held (more_than_allowed) — there are more copies than the decision allows",
		"File:      file/311",
		"Awaiting a corrected file",
		"KEY\n  email=a***@x, policy_id=P-10",
		"masked for you: email",
		"DECISION\n  allow 1, effective at 2026-11-10T10:00:00Z",
		"carried from another group of the same key (from period 2026-09)",
		"approved by Ann, Bob",
		"YOUR COPIES (2)", "FILE-ROW", "17", "quarantine", "held",
		"DIFFERING CELLS (1)",
		"YOUR RESPONSES (1)\n  corrected_file_follows [r: "+testRespIDStr+"] from cloud-source/5 by Sam",
		"EARLIER RESPONSES (1)", "ended at 2026-11-10T10:00:00Z", "(replaced)",
		"thread 0190cccc-1111-4222-8333-444444444444, shared with your cloud source",
		"qctl describe comment-thread 0190cccc-1111-4222-8333-444444444444 --table-id 42",
		"can respond: yes",
		"NEXT ACTIONS",
		"qctl duplicate-group withdraw-response 0190aaaa-1111-4222-8333-444444444444 "+testRespIDStr+
			" --table-id 42   # withdraw the live corrected_file_follows",
	)
}

func TestSourceNextActionsFollowTheCaller(t *testing.T) {
	gid := "0190aaaa-1111-4222-8333-444444444444"
	for _, tc := range []struct {
		name  string
		setup func(g *SourceGroup)
		want  []string
		not   []string
	}{
		{
			name:  "one source: respond without a source",
			setup: func(*SourceGroup) {},
			want: []string{
				"qctl duplicate-group respond " + gid + " --kind separate-rows --comment <text> --table-id 42" +
					"   # ask that the copies load as separate rows: a request, not a decision; the table's reviewers decide",
				"qctl duplicate-group respond " + gid + " --kind corrected-file-follows --comment <text> --table-id 42",
			},
			not: []string{"--cloud-source-id", "withdraw-response", "replaces"},
		},
		{
			name:  "several sources: name one",
			setup: func(g *SourceGroup) { g.Members[1].DataSourceModelID = 7 },
			want: []string{"your copies are on cloud sources 5, 7",
				"respond " + gid + " --kind separate-rows --comment <text> --cloud-source-id <id> --table-id 42"},
		},
		{
			name: "live response: replace or withdraw",
			setup: func(g *SourceGroup) {
				g.Responses = []SourceResponse{testSourceResponse(ResponseSeparateRows)}
			},
			want: []string{"a new response replaces its cloud source's live one",
				"withdraw-response " + gid + " " + testRespIDStr + " --table-id 42"},
		},
		{
			name: "not permitted",
			setup: func(g *SourceGroup) {
				g.Caller = SourceCaller{Reason: ptr(RespondRefusal("not_permitted"))}
				g.Responses = []SourceResponse{testSourceResponse(ResponseSeparateRows)}
			},
			want: []string{"DATASET_INGEST_UPLOAD"},
			not:  []string{"duplicate-group respond", "withdraw-response"},
		},
		{
			name: "ended",
			setup: func(g *SourceGroup) {
				g.State = SenderEnded
				g.Caller = SourceCaller{Reason: ptr(RespondRefusal("ended"))}
			},
			want: []string{"the group has ended; it takes no responses"},
			not:  []string{"duplicate-group respond"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := testSourceGroup()
			tc.setup(g)
			out := SourceNextActions(42, g)
			assertContains(t, out, tc.want...)
			assertLacks(t, out, tc.not...)
		})
	}
}

func TestSourceColumns(t *testing.T) {
	g := testSourceGroup()
	if got := SenderStateColumn(g.State, g.HeldReason); got != "held (more_than_allowed)" {
		t.Errorf("SenderStateColumn = %q", got)
	}
	if got := HeldCopies(g.Members); got != 1 {
		t.Errorf("HeldCopies = %d, want 1", got)
	}
	for _, tc := range []struct {
		d    *SourceDecision
		want string
	}{
		{nil, "-"},
		{&SourceDecision{Kind: KindKeepOne, KeptMemberID: ptr(812)}, "keep-one member 812 (your copy)"},
		{&SourceDecision{Kind: KindKeepOne, KeptElsewhere: true}, "keep-one (a copy of another source)"},
		{&SourceDecision{Kind: KindAllow, Carried: true, PeriodKey: ptr("2026-09")}, "carried allow (2026-09)"},
	} {
		if got := SourceDecisionColumn(tc.d); got != tc.want {
			t.Errorf("SourceDecisionColumn = %q, want %q", got, tc.want)
		}
	}
	if got := CanRespondColumn(&SourceCaller{Reason: ptr(RespondRefusal("period_locked"))}); got != "no (period_locked)" {
		t.Errorf("CanRespondColumn = %q", got)
	}
}

func TestRenderSourceDetailStateAndDecisionText(t *testing.T) {
	loadedOutcome := SenderOutcome("loaded")
	for _, tc := range []struct {
		name  string
		setup func(g *SourceGroup)
		want  []string
		not   []string
	}{
		{
			name: "loaded needs no decision",
			setup: func(g *SourceGroup) {
				g.State, g.HeldReason = SenderLoaded, nil
				for i := range g.Members {
					g.Members[i].Outcome = &loadedOutcome
				}
			},
			want: []string{"State:     loaded — no copy is held and no decision is needed",
				"DECISION\n  none needed: no copy is held"},
			not: []string{"none in effect"},
		},
		{
			name:  "undecided held waits for the reviewers",
			setup: func(g *SourceGroup) { g.HeldReason = ptr(HeldUndecided) },
			want:  []string{"DECISION\n  none in effect: the table's reviewers decide"},
		},
		{
			name: "decided by a carried allow",
			setup: func(g *SourceGroup) {
				g.State, g.HeldReason = SenderDecided, nil
				g.Decision = &SourceDecision{Kind: KindAllow, Carried: true, PeriodKey: ptr("2026-09")}
			},
			want: []string{"State:     decided — a decision in effect governs it and no copy is held",
				"carried from another group of the same key (from period 2026-09)"},
			not: []string{"reviewers decided it"},
		},
		{
			name:  "ended",
			setup: func(g *SourceGroup) { g.State, g.HeldReason = SenderEnded, nil },
			want:  []string{"DECISION\n  none\n"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := testSourceGroup()
			tc.setup(g)
			out := RenderSourceDetail(42, g)
			assertContains(t, out, tc.want...)
			assertLacks(t, out, tc.not...)
		})
	}
}
