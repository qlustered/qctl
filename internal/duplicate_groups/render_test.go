package duplicate_groups //nolint:revive // Matches the backend resource name.

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
)

var (
	testGroupID = uuid.MustParse("0190aaaa-1111-4222-8333-444444444444")
	testPending = uuid.MustParse("0190bbbb-1111-4222-8333-444444444444")
	testThread  = uuid.MustParse("0190cccc-1111-4222-8333-444444444444")
	testUser    = uuid.MustParse("0190eeee-1111-4222-8333-444444444444")
	testTime    = time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC)
)

func ptr[T any](v T) *T { return &v }

func testGroup(origin Origin) *Group {
	outcome := api.DuplicateMemberStatus("held")
	return &Group{
		ID:        testGroupID,
		Origin:    origin,
		State:     "needs_decision",
		HeldCount: 2,
		CreatedAt: testTime,
		Caller:    CallerPermissions{CanDecide: true},
		Members: []GroupMember{
			{ID: 812, RowID: 1042, DataSourceModelID: 5, Location: "quarantine", Rank: ptr(1), Outcome: &outcome},
			{ID: 813, RowID: 1043, DataSourceModelID: 5, Location: "quarantine", Rank: ptr(2), Outcome: &outcome,
				IdenticalAtLoadTo: []int{812}},
		},
	}
}

func pending(kind DecisionKind, count *int) *Decision {
	return &Decision{
		ID: testPending, Kind: kind, AllowedCount: count, State: StatePending, Scope: "group",
		ProposedByID: testUser, CreatedAt: testTime, RequiredApprovals: 2,
		Approvals: []api.DuplicateDecisionApprovalSchema{{ReviewerID: testUser, ApprovedAt: testTime, Counted: true}},
	}
}

func assertContains(t *testing.T, out string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func assertLacks(t *testing.T, out string, nots ...string) {
	t.Helper()
	for _, not := range nots {
		if strings.Contains(out, not) {
			t.Errorf("output has %q:\n%s", not, out)
		}
	}
}

func TestRenderDetailShowsEverySection(t *testing.T) {
	g := testGroup(OriginRepeatedKey)
	g.KeyValues = &map[string]interface{}{"policy_id": "P-10", "email": "a***@x"}
	g.MaskedFields = &[]string{"email"}
	g.StoredItemID = ptr(311)
	g.AwaitingApproval = true
	g.PendingDecision = pending(KindAllow, ptr(3))
	g.Caller.CanApprove = true
	g.CarriedDecision = &Decision{ID: uuid.New(), Kind: KindAllow, State: StateEffective, Scope: ScopeSeries,
		PeriodKey: ptr("2026-09"), Carried: true, ProposedByID: testUser, CreatedAt: testTime, RequiredApprovals: 1}
	g.DifferingCells = []DifferingCell{{Column: "amount", Values: []api.DuplicateCellValueSchema{
		{MemberID: 812, Value: 10.5}, {MemberID: 813, Value: nil}}}}
	g.Responses = []SenderResponse{{Kind: "separate_rows", Comment: "two orders", AuthorName: "Sam",
		AuthorIsExternal: true, DataSourceModelID: 5, CreatedAt: testTime}}
	g.CommentThreadID = &testThread
	g.CommentThreadIsInternal = ptr(true)

	out := RenderDetail(42, g)
	assertContains(t, out,
		"duplicate-group/0190aaaa-1111-4222-8333-444444444444 (table/42)",
		"State:     needs_decision (awaiting approval)",
		"File:      file/311",
		"KEY\n  email=a***@x, policy_id=P-10",
		"masked for you: email",
		"PENDING DECISION\n  allow 3 [d: 0190bbbb-1111-4222-8333-444444444444] pending, approvals 1/2",
		"CARRIED DECISION\n  allow [d: ",
		"scope series (from period 2026-09)",
		"approved by 0190eeee-1111-4222-8333-444444444444",
		"MEMBERS (2)", "IDENTICAL-TO",
		"DIFFERING CELLS (1)\n  amount: member 812=10.5; member 813=null",
		"RESPONSES (1)\n  separate_rows from cloud-source/5 by Sam (external)",
		"thread 0190cccc-1111-4222-8333-444444444444, internal",
		"qctl describe comment-thread 0190cccc-1111-4222-8333-444444444444 --table-id 42",
		"can decide: yes",
		"can approve the pending decision: yes",
		"approved it: no",
		"can propose a change: no",
		"NEXT ACTIONS",
		"qctl duplicate-group approve 0190aaaa-1111-4222-8333-444444444444 --table-id 42   # approve the pending allow 3 (1/2)",
		"qctl duplicate-group share 0190aaaa-1111-4222-8333-444444444444 --table-id 42",
	)
	// Nothing effective of the group's own, and the caller did not propose the pending one.
	assertLacks(t, out, "\nDECISION\n", "duplicate-group withdraw", "duplicate-group allow")
}

func TestRenderUnappliedPeriods(t *testing.T) {
	g := testGroup(OriginRepeatedKey)
	g.Unapplied = true
	g.UnappliedPeriods = []api.DuplicateUnappliedPeriodSchema{
		{DatasetID: 43, Outcome: "skipped_locked", Reason: ptr("Period locked")},
		{DatasetID: 44, Outcome: "failed", Reason: ptr("Apply failed\x1b[31m")},
		{DatasetID: 45, Outcome: "failed"},
	}
	out := RenderDetail(42, g)
	assertContains(t, out,
		"Unapplied: yes — the decision has not been fully applied",
		"UNAPPLIED PERIODS\n  table/43 skipped_locked: \"Period locked\"",
		`table/44 failed: "Apply failed\\x1b[31m"`, "table/45 failed\n",
		"qctl duplicate-group reapply --table-id 42   # retry unapplied decisions",
	)
	assertLacks(t, out, "rows disagree", "\x1b")

	g.UnappliedPeriods = nil
	out = RenderDetail(42, g)
	assertContains(t, out, "Unapplied: yes", "# retry unapplied decisions")
	assertLacks(t, out, "UNAPPLIED PERIODS", "rows disagree")

	g.Unapplied = false
	out = RenderDetail(42, g)
	assertLacks(t, out, "Unapplied:", "UNAPPLIED PERIODS", "duplicate-group reapply")
}

func TestWriteDecisionShowsVotesAndHowItEnded(t *testing.T) {
	replaced := uuid.MustParse("0190ffff-1111-4222-8333-444444444444")
	other := uuid.MustParse("0190dddd-1111-4222-8333-444444444444")
	d := pending(KindStopSeries, nil)
	d.ReplacesDecisionID = &replaced
	d.Approvals[0].Comment = ptr("fine by me")
	d.Disagreements = []api.DuplicateDecisionDisagreementSchema{
		{ReviewerID: other, DisagreedAt: testTime, Counted: true, Reason: ptr("still repeats")},
		{ReviewerID: testUser, DisagreedAt: testTime, Reason: ptr("excluded")},
	}
	d.SupersededAt, d.SupersededByDecisionID, d.SupersededByID = &testTime, &other, &testUser
	d.WithdrawnAt, d.WithdrawReason = &testTime, ptr(api.DuplicateDecisionWithdrawReason("group_ended"))
	d.DismissedAt, d.DismissedByID, d.DismissReason = &testTime, &other, ptr("wrong key")
	var b strings.Builder
	writeDecision(&b, d)
	out := b.String()
	assertContains(t, out,
		"stop-series [d: 0190bbbb",
		"stops the series allow [d: 0190ffff-1111-4222-8333-444444444444]",
		"(counted): \"fine by me\"",
		"disagreed by 0190dddd-1111-4222-8333-444444444444 at 2026-11-10T10:00:00Z",
		"(counted): \"still repeats\"", "(not counted): \"excluded\"",
		"superseded at 2026-11-10T10:00:00Z", "by [d: 0190dddd-1111-4222-8333-444444444444] (proposed by 0190eeee",
		"by the system (group_ended)",
		"dismissed at 2026-11-10T10:00:00Z", "by 0190dddd-1111-4222-8333-444444444444: \"wrong key\"",
	)
	d.Kind = KindAllow
	b.Reset()
	writeDecision(&b, d)
	assertContains(t, b.String(), "replaces [d: 0190ffff")
}

func TestRenderCallerAbilities(t *testing.T) {
	series := uuid.MustParse("0190ffff-1111-4222-8333-444444444444")
	g := testGroup(OriginPersonMarked)
	g.PendingDecision = pending(KindKeepOne, nil)
	g.Decision = &Decision{Kind: KindAllow, State: StateEffective}
	g.Caller = CallerPermissions{
		CanDecide: true, CanApprove: true, CallerApproved: true,
		DisagreeReason: ptr(DecideRefusal("proposer")), CanWithdraw: true,
		DismissReason: ptr(DecideRefusal("not_admin")), ChangeReason: ptr(DecideRefusal("pending_exists")),
		StopForSeriesDecisionID: &series, StopForSeriesReason: ptr(DecideRefusal("pending_exists")),
		RetractReason: ptr(api.RetractRefusal("decided")),
	}
	var b strings.Builder
	renderCaller(&b, 42, g)
	assertContains(t, b.String(),
		"can decide: yes\n",
		"can approve the pending decision: yes\n",
		"approved it: yes\n",
		"can disagree: no (you proposed it; withdraw it instead)\n",
		"can withdraw it: yes\n",
		"can dismiss it: no (only an admin or ops user may dismiss)\n",
		"can propose a change: no (another reviewer's decision is pending; approve or disagree with it)\n",
		"can stop the series allow [d: 0190ffff-1111-4222-8333-444444444444]: no (the group has a pending decision;",
		"can retract the mark: no (its own decision is effective)\n",
	)

	g = testGroup(OriginRepeatedKey)
	g.Caller = CallerPermissions{Reason: ptr(DecideRefusal("not_designated"))}
	b.Reset()
	renderCaller(&b, 42, g)
	out := b.String()
	assertContains(t, out, "can decide: no (the data review policy names who may; see qctl get data-review-policy --table-id 42)")
	assertLacks(t, out, "approve", "propose a change", "series", "retract")
}

func TestNextActionsFollowStateAndCaller(t *testing.T) {
	sel := "0190aaaa-1111-4222-8333-444444444444 --table-id 42"
	for _, tc := range []struct {
		name  string
		setup func(g *Group)
		want  []string
		not   []string
	}{
		{
			name:  "undecided person_marked offers the three kinds",
			setup: func(g *Group) { g.Origin = OriginPersonMarked },
			want: []string{"allow " + sel + "   # decide", "keep-one " + sel + " --member <member-id>",
				"not-duplicates " + sel + " --reason <text>"},
			not: []string{"approve", "retract"},
		},
		{
			name: "the marker may retract but not decide",
			setup: func(g *Group) {
				g.Origin = OriginPersonMarked
				g.Caller.CanDecide = false
				reason := DecideRefusal("excluded")
				g.Caller.Reason = &reason
				g.Caller.CanRetract = true
			},
			want: []string{"marked it; another reviewer must", "retract " + sel + "   # take the mark back"},
			not:  []string{"duplicate-group allow"},
		},
		{
			name: "can_retract on a load-formed group offers no retract",
			setup: func(g *Group) {
				g.Caller.CanRetract = true
			},
			not: []string{"retract"},
		},
		{
			name:  "identical_rows allow needs a count, keep-one no member",
			setup: func(g *Group) { g.Origin = OriginIdenticalRows },
			want:  []string{"allow " + sel + " --count N", "keep-one " + sel + "\n"},
			not:   []string{"--member"},
		},
		{
			name: "caller already approved; a counted disagreement holds it",
			setup: func(g *Group) {
				g.PendingDecision = pending(KindKeepOne, nil)
				g.PendingDecision.Disagreements = []api.DuplicateDecisionDisagreementSchema{{Counted: true}}
				g.Caller.CanApprove, g.Caller.CallerApproved = true, true
			},
			want: []string{"you approved the pending keep-one; waiting for 1 more approval(s); held by 1 disagreement(s)"},
			not:  []string{"duplicate-group approve", "duplicate-group allow", "withdraw", "dismiss"},
		},
		{
			name: "another reviewer's pending decision: approve, disagree or dismiss it",
			setup: func(g *Group) {
				g.PendingDecision = pending(KindAllow, ptr(3))
				g.Caller.CanApprove, g.Caller.CanDisagree, g.Caller.CanDismiss = true, true, true
				g.Caller.WithdrawReason = ptr(DecideRefusal("not_proposer"))
			},
			want: []string{"approve " + sel + "   # approve the pending allow 3 (1/2)",
				"disagree " + sel + " --reason <text>   # hold the pending allow 3",
				"dismiss " + sel + " --reason <text>   # admin/ops"},
			not: []string{"withdraw", "duplicate-group allow", "keep-one"},
		},
		{
			name: "your own pending decision: withdraw or replace it",
			setup: func(g *Group) {
				g.PendingDecision = pending(KindAllow, nil)
				g.Caller.CanApprove, g.Caller.CallerApproved, g.Caller.CanWithdraw = true, true, true
			},
			want: []string{"withdraw " + sel + "   # take back your pending allow",
				"allow " + sel + " [--count N]   # or replace it: allow the copies"},
			not: []string{"disagree", "dismiss", "duplicate-group approve"},
		},
		{
			name: "a pending decision the caller may not approve",
			setup: func(g *Group) {
				g.PendingDecision = pending(KindAllow, nil)
				g.Caller.ApproveReason = ptr(DecideRefusal("excluded"))
			},
			want: []string{"you may not approve the pending allow: you edited or requested review of its rows"},
			not:  []string{"duplicate-group approve", "duplicate-group allow"},
		},
		{
			name: "an effective decision: propose a change",
			setup: func(g *Group) {
				g.Decision = &Decision{Kind: KindAllow, State: StateEffective}
				g.State = "decided"
				g.Caller.CanChange = true
			},
			want: []string{"allow " + sel + " [--count N]   # propose a change: allow the copies"},
			not:  []string{"withdraw", "approve"},
		},
		{
			name: "a carried decision the caller may not change",
			setup: func(g *Group) {
				g.CarriedDecision = &Decision{Kind: KindAllow, State: StateEffective, Carried: true}
				g.Caller.ChangeReason = ptr(DecideRefusal("excluded"))
			},
			want: []string{"you may not propose a change to the effective carried allow: you edited"},
			not:  []string{"withdraw", "duplicate-group allow"},
		},
		{
			name: "a series allow the caller may stop",
			setup: func(g *Group) {
				series := uuid.MustParse("0190ffff-1111-4222-8333-444444444444")
				g.CarriedDecision = &Decision{ID: series, Kind: KindAllow, State: StateEffective, Scope: ScopeSeries}
				g.Caller.CanChange, g.Caller.CanStopForSeries = true, true
				g.Caller.StopForSeriesDecisionID = &series
			},
			want: []string{"stop-series " + sel + "   # stop the series allow [d: 0190ffff-1111-4222-8333-444444444444] in every period",
				"# propose a change"},
		},
		{
			name: "not designated",
			setup: func(g *Group) {
				g.Caller = CallerPermissions{Reason: ptr(DecideRefusal("not_designated"))}
				g.CommentThreadID = &testThread
			},
			want: []string{"qctl get data-review-policy --table-id 42", "comment " + sel},
			not:  []string{"duplicate-group allow", "share"},
		},
		{
			name:  "unapplied offers reapply",
			setup: func(g *Group) { g.Unapplied = true },
			want:  []string{"qctl duplicate-group reapply --table-id 42"},
		},
		{
			name: "ended group without a thread has no next actions",
			setup: func(g *Group) {
				g.State = GroupStateEnded
				g.Caller = CallerPermissions{Reason: ptr(DecideRefusal("ended"))}
			},
			not: []string{"NEXT ACTIONS"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := testGroup(OriginRepeatedKey)
			tc.setup(g)
			out := NextActions(42, g)
			assertContains(t, out, tc.want...)
			assertLacks(t, out, tc.not...)
		})
	}
}

func TestDecisionColumn(t *testing.T) {
	carried := &Decision{Kind: KindAllow, AllowedCount: ptr(2), PeriodKey: ptr("2026-09")}
	own := &Decision{Kind: KindKeepOne, KeptMemberID: ptr(812), State: StateEffective}
	for _, tc := range []struct {
		own, carried *Decision
		want         string
	}{
		{own, carried, "keep-one member 812"},
		{nil, carried, "carried allow 2 (2026-09)"},
		{nil, nil, "-"},
	} {
		if got := DecisionColumn(tc.own, tc.carried); got != tc.want {
			t.Errorf("DecisionColumn = %q, want %q", got, tc.want)
		}
	}
}

func TestPendingColumn(t *testing.T) {
	replaced := uuid.New()
	change := pending(KindAllow, ptr(3))
	change.ReplacesDecisionID = &replaced
	change.Disagreements = []api.DuplicateDecisionDisagreementSchema{{Counted: true}, {Counted: true}, {}}
	stop := pending(KindStopSeries, nil)
	stop.ReplacesDecisionID = &replaced
	for _, tc := range []struct {
		p    *Decision
		want string
	}{
		{pending(KindAllow, ptr(3)), "allow 3 1/2"},
		{change, "allow 3 1/2 change 2x disagree"},
		{stop, "stop-series 1/2"},
		{nil, "-"},
	} {
		if got := PendingColumn(tc.p); got != tc.want {
			t.Errorf("PendingColumn = %q, want %q", got, tc.want)
		}
	}
}

func TestRenderDetailMemberValues(t *testing.T) {
	g := testGroup(OriginIdenticalRows)
	g.MaskedFields = &[]string{"email"}
	g.Members[0].Values = &map[string]interface{}{
		"email": "a***@x", "amount": 10.5, "empty": nil, "paid": false,
		"notes\nfield": "first\nsecond\x1b[31m", "items": []interface{}{1, "two"},
	}
	g.Members[1].Location = "missing"
	g.Members = append(g.Members,
		GroupMember{ID: 814, RowID: 1044, DeletedAt: &testTime},
		GroupMember{ID: 815, RowID: 1045, Values: &map[string]interface{}{}},
	)
	out := RenderDetail(42, g)
	assertContains(t, out,
		"MEMBER VALUES\n  member 812 (row 1042):\n    amount: 10.5\n    email: a***@x\n    empty: null\n",
		"    items: [1,\"two\"]\n    notes field: first second",
		"    paid: false\n",
		"  member 813 (row 1043):\n    (unavailable)\n",
		"  member 814 (row 1044):\n    (unavailable)\n",
		"  member 815 (row 1045):\n    (no values)\n",
		"masked for you: email",
	)
	assertLacks(t, out, "\x1b", "notes\nfield")
	g.Members = nil
	assertLacks(t, RenderDetail(42, g), "MEMBER VALUES")
}
