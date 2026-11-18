package rule_suggestions

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/dataset_rules"
)

func renderable(mutate func(*Suggestion)) *Suggestion {
	s := &Suggestion{
		ID:           uuid.MustParse("3f2a0000-1111-2222-3333-444444444444"),
		InstanceName: "email_check",
		DatasetID:    42,
		Action:       api.SuggestionActionUpdate,
		Source:       api.User,
		State:        api.SuggestionStatePending,
		CreatedAt:    time.Now().Add(-2 * time.Hour),
	}
	if mutate != nil {
		mutate(s)
	}
	return s
}

// A pending candidate is decided by its dry run. Offering accept is offering
// a call the backend refuses with 409, and --family does not apply to one.
//
// The assertion names the full invocation "qctl suggestion accept" because
// that prefix appears only on the line that offers the command; the candidate
// footer's prose quotes the bare subcommand to say it is refused, which is the
// opposite of offering it.
func TestRenderDetailCandidateFooterOffersDismissOnly(t *testing.T) {
	yes := true
	jobID := 8814
	s := renderable(func(s *Suggestion) {
		s.IsRevalidationCandidate = &yes
		s.ValidationDryRunJobID = &jobID
	})

	out := RenderDetail(s, nil, 42, nil)

	if strings.Contains(out, "qctl suggestion accept") {
		t.Errorf("a candidate must not be offered accept:\n%s", out)
	}
	if strings.Contains(out, "--family") {
		t.Errorf("dismiss_family does not apply to a candidate:\n%s", out)
	}
	if !strings.Contains(out, "suggestion dismiss") {
		t.Errorf("a pending candidate must offer dismiss:\n%s", out)
	}
	if !strings.Contains(out, "patch the") || !strings.Contains(out, "queues a fresh") {
		t.Errorf("a validating candidate must explain how to replace its in-flight run:\n%s", out)
	}
}

// A queued candidate has no run yet. Its block must say it is waiting for the
// scheduler, not that a run is validating it, and its footer offers what a
// validating candidate's does: dismiss, and a rule patch to replace it.
func TestRenderDetailQueuedCandidate(t *testing.T) {
	yes := true
	s := renderable(func(s *Suggestion) {
		s.IsRevalidationCandidate = &yes
		s.ValidationQueued = &yes
	})

	out := RenderDetail(s, nil, 42, nil)

	if got := PhaseOf(s, nil); got != PhaseQueued {
		t.Fatalf("fixture is phase %v, want PhaseQueued", got)
	}
	for _, want := range []string{
		"Phase            : queued (dry run not staged yet)",
		"its validation is queued",
		"qctl suggestion dismiss " + s.ID.String() + " --table-id 42",
		"queues a fresh run",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("queued candidate output missing %q:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"validating", "Dry run job", "qctl suggestion accept", "--family"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("queued candidate output must not contain %q:\n%s", unwanted, out)
		}
	}
}

func TestRenderDetailCandidateBlockShowsRetryBudgetAndJob(t *testing.T) {
	yes := true
	jobID := 8814
	retries := 1
	pin := 37
	s := renderable(func(s *Suggestion) {
		s.IsRevalidationCandidate = &yes
		s.ValidationDryRunJobID = &jobID
		s.RevalidationRetryCount = &retries
		s.ExpectedDatasetRuleVersionID = &pin
	})

	out := RenderDetail(s, nil, 42, nil)

	for _, want := range []string{
		"REVALIDATION CANDIDATE",
		"validating",
		"1 of 2 used",
		"8814",
		"qctl describe dry-run-job 8814",
		// Not the bare "37": that substring survives deleting the
		// "(server-set; ...)" labelling the spec mandates.
		"Rule version pin",
		"37 (server-set; a passing run is promoted only while the rule is at this version)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("candidate block missing %q\n%s", want, out)
		}
	}
}

// An applied candidate was written onto the live rule by its own dry run.
// Saying "accepted" would name an act nobody performed.
func TestRenderDetailPromotedCandidateIsNotDescribedAsAccepted(t *testing.T) {
	yes := true
	at := time.Now().Add(-time.Hour)
	jobID := 8814
	s := renderable(func(s *Suggestion) {
		s.IsRevalidationCandidate = &yes
		s.ValidationDryRunJobID = &jobID
		s.ValidatedAt = &at
		s.State = api.SuggestionStateApplied
	})

	out := RenderDetail(s, nil, 42, nil)

	if !strings.Contains(out, "promoted onto the live rule") {
		t.Errorf("a promoted candidate must say so:\n%s", out)
	}
	if strings.Contains(out, "qctl suggestion accept") {
		t.Errorf("nothing to accept on a resolved candidate:\n%s", out)
	}
}

// resolution_msg is the only explanation a failed candidate carries, and
// `failed` is reachable by nothing else.
func TestRenderDetailFailedShowsResolutionMsg(t *testing.T) {
	yes := true
	msg := "the validation dry run crashed on 41% of rows"
	s := renderable(func(s *Suggestion) {
		s.IsRevalidationCandidate = &yes
		s.State = api.SuggestionStateFailed
		s.ResolutionMsg = &msg
	})

	out := RenderDetail(s, nil, 42, nil)

	if !strings.Contains(out, msg) {
		t.Errorf("a failed candidate must show resolution_msg:\n%s", out)
	}
}

// resolution_msg is backend Markdown: a failed repair's reason can embed an
// alert message, value markers and all. The Why line renders it on one line
// with the value whole.
func TestRenderDetailRendersAMarkedResolutionMsg(t *testing.T) {
	yes := true
	msg := "This rule's repair could not be applied: ~|\"~*Hyundai Sonata Hybrid Limited*~\"|~ is not a valid **model**<br>see the job"
	s := renderable(func(s *Suggestion) {
		s.IsRevalidationCandidate = &yes
		s.State = api.SuggestionStateFailed
		s.ResolutionMsg = &msg
	})

	out := RenderDetail(s, nil, 42, nil)

	want := `: This rule's repair could not be applied: "Hyundai Sonata Hybrid Limited" is not a valid model see the job` + "\n"
	if !strings.Contains(out, want) {
		t.Errorf("Why line not rendered; want %q in:\n%s", want, out)
	}
}

// The three-line accept/dismiss/--family footer is correct for a staged edit
// and must not regress while the candidate case is added.
func TestRenderDetailOrdinarySuggestionFooterUnchanged(t *testing.T) {
	out := RenderDetail(renderable(nil), nil, 42, nil)

	for _, want := range []string{"suggestion accept", "suggestion dismiss", "--family"} {
		if !strings.Contains(out, want) {
			t.Errorf("ordinary pending footer missing %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "REVALIDATION CANDIDATE") {
		t.Errorf("an ordinary suggestion has no candidate block:\n%s", out)
	}
}

// The footer must cover every Phase, not only the four the branch's first
// pass exercised. Deleting the PhaseDismissed arm and narrowing the
// PhaseValidating/PhaseValidatedPendingMigration grouping left the whole
// suite green, so `describe rule-suggestion` could render a bare NEXT
// ACTIONS heading with nothing under it and nothing would fail.
func TestRenderDetailDismissedCandidateFooter(t *testing.T) {
	yes := true
	jobID := 8814
	s := renderable(func(s *Suggestion) {
		s.IsRevalidationCandidate = &yes
		s.ValidationDryRunJobID = &jobID
		s.State = api.SuggestionStateDismissed
	})

	out := RenderDetail(s, nil, 42, nil)

	if got := PhaseOf(s, nil); got != PhaseDismissed {
		t.Fatalf("fixture is phase %v, want PhaseDismissed", got)
	}
	if !strings.Contains(out, "dismissed — nothing to resolve") {
		t.Errorf("a dismissed candidate must say so in NEXT ACTIONS:\n%s", out)
	}
	if strings.Contains(out, "qctl suggestion accept") {
		t.Errorf("a dismissed candidate must not be offered accept:\n%s", out)
	}
	if strings.Contains(out, "qctl suggestion dismiss") {
		t.Errorf("a dismissed candidate must not be offered dismiss again:\n%s", out)
	}
}

// PhaseValidatedPendingMigration is not a corner case: gate.go documents it
// as the ordinary happy path, the candidate and the migration alert being
// siblings raised by the same complete_revalidation_dry_run call.
func TestRenderDetailValidatedPendingMigrationFooterOffersDismissOnly(t *testing.T) {
	yes := true
	jobID := 8814
	validated := time.Now().Add(-time.Hour)
	s := renderable(func(s *Suggestion) {
		s.IsRevalidationCandidate = &yes
		s.ValidationDryRunJobID = &jobID
		s.ValidatedAt = &validated
	})

	out := RenderDetail(s, nil, 42, nil)

	if got := PhaseOf(s, nil); got != PhaseValidatedPendingMigration {
		t.Fatalf("fixture is phase %v, want PhaseValidatedPendingMigration", got)
	}
	if !strings.Contains(out, "qctl suggestion dismiss") {
		t.Errorf("a validated candidate still offers dismiss:\n%s", out)
	}
	if strings.Contains(out, "qctl suggestion accept") {
		t.Errorf("a candidate is never offered accept, validated or not:\n%s", out)
	}
	if strings.Contains(out, "--family") {
		t.Errorf("dismiss_family does not apply to a candidate:\n%s", out)
	}
	for _, want := range []string{
		"validation dry run passed",
		"qctl describe table-schema --table-id 42",
		"qctl get alerts --table-id 42 --issue-type needs_review_migration",
		"reviewed and applied",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("validated candidate footer missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "patch the rule again") || strings.Contains(out, "queues a fresh") {
		t.Errorf("a validated candidate waits on its migration, not another patch:\n%s", out)
	}
}

// amountFilter keeps the rows whose amount is at least minAmount.
func amountFilter(description, minAmount string) *api.RowFilter {
	conditions := []api.RowFilterCondition{{ColumnName: "amount", Operator: api.Gte, Value: &minAmount}}
	return &api.RowFilter{Description: &description, Conditions: &conditions}
}

// renderAgainstLive renders an update suggestion staging staged against a
// live rule carrying live, with every other staged field equal to the rule's.
func renderAgainstLive(staged, live *api.RowFilter) string {
	s := renderable(func(s *Suggestion) {
		s.Severity = api.RuleSeverityWarning
		s.RowFilter = staged
	})
	current := &dataset_rules.DatasetRuleDetail{Severity: api.RuleSeverityWarning, RowFilter: live}
	return RenderDetail(s, current, 42, nil)
}

// A suggestion staged under a draft migration snapshots the live rule's
// filter, so equal is the ordinary case: printing it would announce a change
// accepting does not make. With nothing else staged, the section still says
// so.
func TestRenderDetailRowFilterEqualToLiveRendersNothing(t *testing.T) {
	out := renderAgainstLive(amountFilter("Big orders", "10"), amountFilter("Big orders", "10"))

	if strings.Contains(out, "row_filter") {
		t.Errorf("an unchanged filter must not render:\n%s", out)
	}
	if !strings.Contains(out, "(none recorded on this suggestion)") {
		t.Errorf("with nothing differing, the section must say nothing is staged:\n%s", out)
	}
}

func TestRenderDetailRowFilterDiffersFromLive(t *testing.T) {
	for _, tc := range []struct {
		name         string
		staged, live *api.RowFilter
		want         string
	}{
		{
			name:   "the rows kept differ",
			staged: amountFilter("Big orders", "100"),
			live:   amountFilter("Big orders", "10"),
			want: "row_filter       : the rows kept differ\n" +
				"  live:\n" +
				"    description: Big orders\n" +
				"    conditions (a row must meet every one):\n" +
				"      - amount gte \"10\"\n" +
				"  staged:\n" +
				"    description: Big orders\n" +
				"    conditions (a row must meet every one):\n" +
				"      - amount gte \"100\"\n",
		},
		{
			// Accepting writes the description too, so a reworded one is a
			// change to the rule even though the rows kept are not.
			name:   "only the description differs",
			staged: amountFilter("Orders worth reviewing", "10"),
			live:   amountFilter("Big orders", "10"),
			want: "row_filter       : only the description differs; the rows kept are the same\n" +
				"  live:\n" +
				"    description: Big orders\n" +
				"    conditions (a row must meet every one):\n" +
				"      - amount gte \"10\"\n" +
				"  staged:\n" +
				"    description: Orders worth reviewing\n" +
				"    conditions (a row must meet every one):\n" +
				"      - amount gte \"10\"\n",
		},
		{
			// A null staged filter is not "not touched": accepting assigns it
			// and the rule loses its filter.
			name:   "a null staged filter removes the live one",
			staged: nil,
			live:   amountFilter("Big orders", "10"),
			want: "row_filter       : accepting removes the rule's filter\n" +
				"  live:\n" +
				"    description: Big orders\n" +
				"    conditions (a row must meet every one):\n" +
				"      - amount gte \"10\"\n" +
				"  staged:\n" +
				"    (no filter: keeps every row)\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := renderAgainstLive(tc.staged, tc.live)

			if !strings.Contains(out, tc.want) {
				t.Errorf("want block:\n%s\nin:\n%s", tc.want, out)
			}
			// The filter is the only staged difference, and it is one.
			if strings.Contains(out, "(none recorded on this suggestion)") {
				t.Errorf("a differing filter is a staged value:\n%s", out)
			}
		})
	}
}

// A create suggestion's filter is the one the new rule is created with, and
// there is no live rule to compare it against.
func TestRenderDetailCreateRowFilterIsOneSided(t *testing.T) {
	create := func(filter *api.RowFilter) string {
		return RenderDetail(renderable(func(s *Suggestion) {
			s.Action = api.SuggestionActionCreate
			s.Severity = api.RuleSeverityWarning
			s.RowFilter = filter
		}), nil, 42, nil)
	}

	out := create(amountFilter("Big orders", "100"))
	want := "severity         : warning\n" +
		"row_filter       : the rule's filter once accepted\n" +
		"  description: Big orders\n" +
		"  conditions (a row must meet every one):\n" +
		"    - amount gte \"100\"\n"
	if !strings.Contains(out, want) {
		t.Errorf("want block:\n%s\nin:\n%s", want, out)
	}
	if strings.Contains(out, "live:") {
		t.Errorf("a create has no live filter to show:\n%s", out)
	}

	if out := create(nil); strings.Contains(out, "row_filter") {
		t.Errorf("a create without a filter must not render one:\n%s", out)
	}
}

// Only a pending create or update that is not a revalidation candidate is
// accepted onto a rule's filter. Every other suggestion still shows how its
// staged filter compares, but never says what accepting does to the rule.
func TestRenderDetailRowFilterLabelClaimsAnAcceptEffectOnlyWhenAcceptWritesIt(t *testing.T) {
	yes := true
	live := amountFilter("Big orders", "10")
	for _, tc := range []struct {
		name   string
		mutate func(*Suggestion)
	}{
		// _apply_accept's delete branch only deletes the rule.
		{"pending delete", func(s *Suggestion) { s.Action = api.SuggestionActionDelete }},
		{"applied update", func(s *Suggestion) { s.State = api.SuggestionStateApplied }},
		{"dismissed update", func(s *Suggestion) { s.State = api.SuggestionStateDismissed }},
		// Accept refuses a candidate; its dry run decides it.
		{"pending candidate", func(s *Suggestion) { s.IsRevalidationCandidate = &yes }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := renderable(func(s *Suggestion) {
				s.Severity = api.RuleSeverityWarning
				tc.mutate(s)
			})
			current := &dataset_rules.DatasetRuleDetail{Severity: api.RuleSeverityWarning, RowFilter: live}
			out := RenderDetail(s, current, 42, nil)

			if !strings.Contains(out, "row_filter       : none staged; the live rule has one\n") {
				t.Errorf("want the neutral label:\n%s", out)
			}
			if strings.Contains(out, "accepting removes") {
				t.Errorf("accepting this suggestion does not touch the rule's filter:\n%s", out)
			}
		})
	}
}

func TestRenderDetailRowFilterWithoutALiveRule(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Suggestion)
		filter *api.RowFilter
		want   string // "" = no row_filter line at all
	}{
		{
			// Accepting assigns the null filter, so the rule loses any it has;
			// the failed fetch is why describe cannot say whether it has one.
			name:   "pending update, null filter",
			mutate: func(*Suggestion) {},
			want:   "row_filter       : none staged; accepting removes the rule's filter, if it has one\n",
		},
		{
			name:   "applied update, null filter",
			mutate: func(s *Suggestion) { s.State = api.SuggestionStateApplied },
		},
		{
			name:   "pending delete, null filter",
			mutate: func(s *Suggestion) { s.Action = api.SuggestionActionDelete },
		},
		{
			name: "applied create",
			mutate: func(s *Suggestion) {
				s.Action = api.SuggestionActionCreate
				s.State = api.SuggestionStateApplied
			},
			filter: amountFilter("Big orders", "100"),
			want:   "row_filter       : staged on this suggestion\n  description: Big orders\n",
		},
		{
			name:   "pending update, staged filter",
			mutate: func(*Suggestion) {},
			filter: amountFilter("Big orders", "100"),
			want:   "row_filter       : the rule's filter once accepted\n  description: Big orders\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := RenderDetail(renderable(func(s *Suggestion) {
				s.Severity = api.RuleSeverityWarning
				s.RowFilter = tc.filter
				tc.mutate(s)
			}), nil, 42, nil)

			if tc.want == "" {
				if strings.Contains(out, "row_filter") {
					t.Errorf("want no row_filter line:\n%s", out)
				}
				return
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("want:\n%s\nin:\n%s", tc.want, out)
			}
		})
	}
}

// A delete suggestion still records params, severity and a filter, and they
// diff against the live rule like any other suggestion's; accepting one
// applies none of them.
func TestRenderDetailDeleteSaysAcceptAppliesNoneOfItsValues(t *testing.T) {
	note := "(accepting a delete deletes the rule; it applies none of the values above)\n"
	render := func(severity api.RuleSeverity) string {
		s := renderable(func(s *Suggestion) {
			s.Action = api.SuggestionActionDelete
			s.Severity = severity
		})
		return RenderDetail(s, &dataset_rules.DatasetRuleDetail{Severity: api.RuleSeverityWarning}, 42, nil)
	}

	if out := render(api.RuleSeverityBlocker); !strings.Contains(out, note) {
		t.Errorf("a differing value on a delete needs the note:\n%s", out)
	}
	out := render(api.RuleSeverityWarning)
	if strings.Contains(out, note) {
		t.Errorf("with nothing differing there is nothing for the note to qualify:\n%s", out)
	}
	if !strings.Contains(out, "(none recorded on this suggestion)") {
		t.Errorf("with nothing differing, the section must say nothing is staged:\n%s", out)
	}
}

// A held candidate names the locked period, offers the unlock and dismiss,
// and does not claim a run is validating it.
func TestRenderDetailHeldCandidate(t *testing.T) {
	yes := true
	s := renderable(func(s *Suggestion) { s.IsRevalidationCandidate = &yes })

	out := RenderDetail(s, nil, 42, testLockedPeriod())

	for _, want := range []string{
		"Phase            : held (table period locked)",
		`Held by          : period "2026-09" of series "Monthly bordereau" is locked`,
		"- unlock the period (deliverable:admin): qctl table-series unlock 0b8c3f9e-1d2a-4e5f-9a6b-7c8d9e0f1a2b --period 2026-09 --yes",
		"qctl suggestion dismiss " + s.ID.String() + " --table-id 42",
		"passes on a locked period is not applied",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"qctl suggestion accept", "queues a fresh", "Dry run job"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("output carries %q:\n%s", unwanted, out)
		}
	}
}
