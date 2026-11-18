package alerts

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
)

// pendingDetail is a pending suggestion staged against draft migration
// migrationModelID — the shape the pre-flight now classifies.
func pendingDetail(id string, action api.SuggestionAction, name string, migrationModelID int) api.DatasetRuleSuggestionSchema {
	m := migrationModelID
	return api.DatasetRuleSuggestionSchema{
		ID:               uuid.MustParse(id),
		Action:           action,
		State:            api.SuggestionStatePending,
		Source:           api.User,
		InstanceName:     name,
		MigrationModelID: &m,
		CreatedAt:        time.Date(2026, 7, 30, 9, 0, 0, 0, time.UTC),
	}
}

func migrationID(n int) *int { return &n }

func TestBuildSuggestionGatePartitionsByAction(t *testing.T) {
	g := BuildSuggestionGate([]api.DatasetRuleSuggestionSchema{
		pendingDetail("11111111-1111-1111-1111-111111111111", api.SuggestionActionCreate, "Range Rule (1)", 3),
		pendingDetail("22222222-2222-2222-2222-222222222222", api.SuggestionActionUpdate, "Keyword Rule (3)", 3),
		pendingDetail("33333333-3333-3333-3333-333333333333", api.SuggestionActionDelete, "Old Rule", 3),
	}, migrationID(3))

	if len(g.AutoAccept) != 1 || g.AutoAccept[0].Name != "Range Rule (1)" {
		t.Errorf("AutoAccept = %+v, want the one create", g.AutoAccept)
	}
	if len(g.Blockers) != 2 {
		t.Fatalf("Blockers = %+v, want the update and the delete", g.Blockers)
	}
	if g.Blockers[0].Action != "update" || g.Blockers[1].Action != "delete" {
		t.Errorf("blocker actions = %q/%q, want update/delete", g.Blockers[0].Action, g.Blockers[1].Action)
	}
}

func TestBuildSuggestionGateIgnoresNonPending(t *testing.T) {
	applied := pendingDetail("44444444-4444-4444-4444-444444444444", api.SuggestionActionUpdate, "Already Done", 3)
	applied.State = api.SuggestionStateApplied

	g := BuildSuggestionGate([]api.DatasetRuleSuggestionSchema{applied}, migrationID(3))

	if !g.Empty() {
		t.Errorf("non-pending suggestion must be ignored, got %+v", g)
	}
}

// The backend's gate query filters on migration_model_id
// (needs_review_migration, migration_handlers.py), so a suggestion staged
// against another draft migration — or against none — does not block the
// request, but the migration job attempts to apply it.
func TestBuildSuggestionGateShowsSuggestionsOffThisMigration(t *testing.T) {
	other := pendingDetail("55555555-5555-5555-5555-555555555555", api.SuggestionActionUpdate, "Other Migration", 99)
	unattached := pendingDetail("66666666-6666-6666-6666-666666666666", api.SuggestionActionUpdate, "Unattached", 3)
	unattached.MigrationModelID = nil
	strayCreate := pendingDetail("77777777-7777-7777-7777-777777777777", api.SuggestionActionCreate, "Profiling Idea", 3)
	strayCreate.MigrationModelID = nil

	g := BuildSuggestionGate([]api.DatasetRuleSuggestionSchema{other, unattached, strayCreate}, migrationID(3))

	if len(g.Blockers) != 0 || len(g.AutoAccept) != 0 || len(g.JobApply) != 3 {
		t.Errorf("off-migration suggestions are job work, not request blockers: %+v", g)
	}
}

func TestBuildSuggestionGateShowsProfilingButSkipsPeriodicAndCandidates(t *testing.T) {
	profiling := pendingDetail("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", api.SuggestionActionCreate, "Profiled Rule", 3)
	profiling.Source = api.Profiling
	profiling.MigrationModelID = nil
	periodic := pendingDetail("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", api.SuggestionActionCreate, "Periodic Rule", 3)
	periodic.Source = api.Periodic
	periodic.MigrationModelID = nil
	candidate := validatedCandidate("cccccccc-cccc-cccc-cccc-cccccccccccc", "Other Candidate", 99)

	g := BuildSuggestionGate([]api.DatasetRuleSuggestionSchema{profiling, periodic, candidate}, migrationID(3))
	if len(g.JobApply) != 1 || g.JobApply[0].Name != "Profiled Rule" {
		t.Errorf("JobApply = %+v, want only the profiling suggestion", g.JobApply)
	}
	out := g.RenderPreflight(42)
	if !strings.Contains(out, "Profiled Rule") || !strings.Contains(out, "may remain pending") {
		t.Errorf("job work must be previewed with its possible outcome:\n%s", out)
	}
}

// An alert with no draft migration behind it has no gate to model. The
// backend 404s before it ever reaches the suggestion query
// (needs_review_migration, migration_handlers.py).
func TestBuildSuggestionGateWithoutAMigrationIsEmpty(t *testing.T) {
	g := BuildSuggestionGate([]api.DatasetRuleSuggestionSchema{
		pendingDetail("22222222-2222-2222-2222-222222222222", api.SuggestionActionUpdate, "Keyword Rule (3)", 3),
	}, nil)

	if !g.Empty() {
		t.Errorf("no draft migration means no gate, got %+v", g)
	}
}

func TestRenderBlockersPrintsRunnableCommands(t *testing.T) {
	g := BuildSuggestionGate([]api.DatasetRuleSuggestionSchema{
		pendingDetail("22222222-2222-2222-2222-222222222222", api.SuggestionActionUpdate, "Keyword Rule (3)", 3),
	}, migrationID(3))

	out := g.RenderBlockers(42)

	for _, want := range []string{
		"Keyword Rule (3)",
		"qctl suggestion accept 22222222-2222-2222-2222-222222222222 --table-id 42",
		"qctl suggestion dismiss 22222222-2222-2222-2222-222222222222 --table-id 42",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("RenderBlockers missing %q\n%s", want, out)
		}
	}
}

// The create set is now exact — only creates staged against THIS draft
// migration are in it — so the copy states the outcome plainly. This
// replaces TestRenderPreflightDoesNotPromiseEveryCreateIsAccepted, which
// pinned the hedge that was needed while the set was an upper bound.
func TestRenderPreflightNamesTheCreatesItWillAccept(t *testing.T) {
	g := BuildSuggestionGate([]api.DatasetRuleSuggestionSchema{
		pendingDetail("11111111-1111-1111-1111-111111111111", api.SuggestionActionCreate, "Range Rule (1)", 3),
	}, migrationID(3))

	out := g.RenderPreflight(42)

	if !strings.Contains(out, "Range Rule (1)") {
		t.Errorf("preflight must name the create\n%s", out)
	}
	if !strings.Contains(out, "will be accepted") {
		t.Errorf("preflight must state the outcome now that the set is exact\n%s", out)
	}
}

func TestRenderPreflightEmptyGateSaysNothingPending(t *testing.T) {
	g := BuildSuggestionGate(nil, migrationID(3))
	if out := g.RenderPreflight(42); out != "" {
		t.Errorf("empty gate must render nothing, got: %q", out)
	}
}

// validatedCandidate is the shape complete_revalidation_dry_run leaves behind
// on a passing run: pending, action=update, attached to the draft migration it
// just staged, with validated_at and validation_dry_run_job_id both stamped
// (cettings/cettings/controllers/rule_revalidation.py, in
// complete_revalidation_dry_run, stage_revalidation_dry_run and
// stage_candidate_migration).
func validatedCandidate(id, name string, migrationModelID int) api.DatasetRuleSuggestionSchema {
	s := pendingDetail(id, api.SuggestionActionUpdate, name, migrationModelID)
	yes := true
	at := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	job := 4242
	s.IsRevalidationCandidate = &yes
	s.ValidatedAt = &at
	s.ValidationDryRunJobID = &job
	return s
}

// The backend exempts it from the blocker filter
// (_is_validated_revalidation_candidate, migration_handlers.py). It is
// not auto-accepted either — only `create` reaches _apply_accept — so it lands
// in neither of the other two buckets.
func TestBuildSuggestionGateExemptsValidatedRevalidationCandidates(t *testing.T) {
	g := BuildSuggestionGate([]api.DatasetRuleSuggestionSchema{
		validatedCandidate("88888888-8888-8888-8888-888888888888", "Revalidated Rule", 3),
	}, migrationID(3))

	if len(g.Blockers) != 0 {
		t.Errorf("a validated revalidation candidate must not block, got %+v", g.Blockers)
	}
	if len(g.AutoAccept) != 0 {
		t.Errorf("it is not auto-accepted either, got %+v", g.AutoAccept)
	}
	if len(g.Exempt) != 1 || g.Exempt[0].Name != "Revalidated Rule" {
		t.Errorf("Exempt = %+v, want the one candidate", g.Exempt)
	}
	if g.Empty() {
		t.Error("a gate holding only an exempt candidate is not empty")
	}
}

// All three fields are required. A candidate mid-flight — dry run launched but
// not yet passed, or passed on a rule that is not a candidate — still blocks.
func TestBuildSuggestionGateStillBlocksAPartiallyValidatedCandidate(t *testing.T) {
	cases := map[string]func(*api.DatasetRuleSuggestionSchema){
		"not a revalidation candidate": func(s *api.DatasetRuleSuggestionSchema) { s.IsRevalidationCandidate = nil },
		"revalidation flag false":      func(s *api.DatasetRuleSuggestionSchema) { no := false; s.IsRevalidationCandidate = &no },
		"dry run not finished":         func(s *api.DatasetRuleSuggestionSchema) { s.ValidatedAt = nil },
		"no dry run job":               func(s *api.DatasetRuleSuggestionSchema) { s.ValidationDryRunJobID = nil },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			s := validatedCandidate("88888888-8888-8888-8888-888888888888", "Half Done", 3)
			mutate(&s)

			g := BuildSuggestionGate([]api.DatasetRuleSuggestionSchema{s}, migrationID(3))

			if len(g.Blockers) != 1 {
				t.Errorf("Blockers = %+v, want the candidate — the exemption needs all three fields", g.Blockers)
			}
			if len(g.Exempt) != 0 {
				t.Errorf("Exempt = %+v, want none", g.Exempt)
			}
		})
	}
}

// A `delete` can never be a revalidation candidate in practice — the candidate
// is created action=update (create_revalidation_candidate,
// rule_revalidation_view_controller.py) — but the
// backend's filter tests the exemption for both actions, so this one does too.
func TestBuildSuggestionGateExemptionIsNotActionSpecific(t *testing.T) {
	s := validatedCandidate("99999999-9999-9999-9999-999999999999", "Odd One", 3)
	s.Action = api.SuggestionActionDelete

	g := BuildSuggestionGate([]api.DatasetRuleSuggestionSchema{s}, migrationID(3))

	if len(g.Blockers) != 0 || len(g.Exempt) != 1 {
		t.Errorf("the exemption covers update and delete alike, got %+v", g)
	}
}

func TestRenderPreflightNamesTheExemptCandidates(t *testing.T) {
	g := BuildSuggestionGate([]api.DatasetRuleSuggestionSchema{
		validatedCandidate("88888888-8888-8888-8888-888888888888", "Revalidated Rule", 3),
	}, migrationID(3))

	out := g.RenderPreflight(42)

	for _, want := range []string{"Revalidated Rule", "attempt to promote", "leave a candidate pending"} {
		if !strings.Contains(out, want) {
			t.Errorf("preflight must explain the exempt candidate, missing %q\n%s", want, out)
		}
	}
}

// The exemption lives inside the update/delete branch on purpose. The backend
// auto-accepts every pending `create` on the migration with no exemption check
// at all (needs_review_migration in migration_handlers.py calls _apply_accept on
// action==create; _is_validated_revalidation_candidate appears only in the
// blockers comprehension). Hoisting our check above the switch would report
// "stays pending" for a row the backend silently accepts — the one direction
// worse than over-blocking, because we would promise the opposite of what
// happens. Unreachable today (candidates are created action=update,
// create_revalidation_candidate in rule_revalidation_view_controller.py) and
// undefended until now.
func TestBuildSuggestionGateAcceptsAValidatedCreate(t *testing.T) {
	s := validatedCandidate("77777777-7777-7777-7777-777777777777", "Validated Create", 3)
	s.Action = api.SuggestionActionCreate

	g := BuildSuggestionGate([]api.DatasetRuleSuggestionSchema{s}, migrationID(3))

	if len(g.AutoAccept) != 1 || len(g.Exempt) != 0 || len(g.Blockers) != 0 {
		t.Errorf("a validated create is auto-accepted by the backend, not exempt, got %+v", g)
	}
}

// The migration job skips candidates on other migrations, so they are neither
// exempt from this request nor in the job's ordinary apply set.
func TestBuildSuggestionGateDropsAValidatedCandidateOnAnotherMigration(t *testing.T) {
	g := BuildSuggestionGate([]api.DatasetRuleSuggestionSchema{
		validatedCandidate("66666666-6666-6666-6666-666666666666", "Elsewhere", 99),
	}, migrationID(3))

	if !g.Empty() {
		t.Errorf("a candidate on migration 99 is not part of migration 3's gate, got %+v", g)
	}
}

// The request's three buckets had no committed test rendering them together, so their
// order and their headers were only ever verified one at a time.
func TestRenderPreflightRendersAllThreeBuckets(t *testing.T) {
	g := BuildSuggestionGate([]api.DatasetRuleSuggestionSchema{
		pendingDetail("11111111-1111-1111-1111-111111111111", api.SuggestionActionCreate, "New Rule", 3),
		pendingDetail("22222222-2222-2222-2222-222222222222", api.SuggestionActionUpdate, "Blocking Rule", 3),
		validatedCandidate("33333333-3333-3333-3333-333333333333", "Validated Rule", 3),
	}, migrationID(3))

	out := g.RenderPreflight(42)

	if len(g.AutoAccept) != 1 || len(g.Blockers) != 1 || len(g.Exempt) != 1 {
		t.Fatalf("fixture must populate all three buckets, got %+v", g)
	}
	blockers := strings.Index(out, "Blocking Rule")
	accepts := strings.Index(out, "New Rule")
	exempt := strings.Index(out, "Validated Rule")
	if blockers < 0 || accepts < 0 || exempt < 0 {
		t.Fatalf("every bucket must be named, got:\n%s", out)
	}
	// Blockers first: they are the only bucket that stops the command.
	if blockers >= accepts || accepts >= exempt {
		t.Errorf("buckets must render blockers, then accepts, then exempt, got:\n%s", out)
	}
}

// An unvalidated revalidation candidate IS a blocker (atlas
// migration_handlers.py, the blockers comprehension in needs_review_migration)
// and the accept endpoint refuses it with a 409
// (_raise_if_revalidation_candidate, capi/capi/views/suggestion_view.py,
// called from _apply_accept). Telling the user to accept it is advice
// that cannot be followed.
func TestRenderBlockersOffersDismissOnlyForACandidate(t *testing.T) {
	s := validatedCandidate("88888888-8888-8888-8888-888888888888", "Mid Flight", 3)
	s.ValidatedAt = nil // unvalidated, so it blocks

	gate := BuildSuggestionGate([]api.DatasetRuleSuggestionSchema{s}, migrationID(3))
	if len(gate.Blockers) != 1 {
		t.Fatalf("an unvalidated candidate must block, got %+v", gate)
	}

	out := gate.RenderBlockers(42)
	if strings.Contains(out, "suggestion accept") {
		t.Errorf("accept is refused for a candidate; must not be offered:\n%s", out)
	}
	// dismiss really does clear one: _apply_dismiss branches into
	// dismiss_revalidation_candidate for a candidate (suggestion_view.py)
	// and the view, dismiss_suggestion, gates only on state == pending.
	if !strings.Contains(out, "suggestion dismiss") {
		t.Errorf("dismiss must still be offered:\n%s", out)
	}
}

// An ordinary blocker keeps both commands.
func TestRenderBlockersKeepsAcceptForAnOrdinaryBlocker(t *testing.T) {
	gate := BuildSuggestionGate([]api.DatasetRuleSuggestionSchema{
		pendingDetail("22222222-2222-2222-2222-222222222222", api.SuggestionActionUpdate, "Keyword Rule (3)", 3),
	}, migrationID(3))

	out := gate.RenderBlockers(42)

	for _, want := range []string{"suggestion accept", "suggestion dismiss"} {
		if !strings.Contains(out, want) {
			t.Errorf("an ordinary blocker keeps %q:\n%s", want, out)
		}
	}
}

// GateEntry.Name is DatasetRuleSuggestionSchema.instance_name, which the
// backend builds by interpolating the rule's own subject — a column name, or a
// customer-uploaded cell value. internal/commands/alert/resolve.go prints this
// report straight to stdout as the last thing the user reads before confirming
// a schema migration, so the whole document is made control-free at the
// boundary.
//
// RenderBlockers is deliberately left raw: its other caller returns it inside a
// qerrors.NewBadUsagef, and qerrors.Exit sanitizes every error it prints.
func TestRenderPreflightSanitizesTheSuggestionNames(t *testing.T) {
	g := BuildSuggestionGate([]api.DatasetRuleSuggestionSchema{
		pendingDetail("11111111-1111-1111-1111-111111111111", api.SuggestionActionCreate,
			"Range Rule \x1b[31m(1)", 3),
		pendingDetail("22222222-2222-2222-2222-222222222222", api.SuggestionActionUpdate,
			"Keyword Rule \x1b]0;pwned\x07(3)", 3),
	}, migrationID(3))

	out := g.RenderPreflight(42)

	if strings.ContainsRune(out, '\x1b') || strings.ContainsRune(out, '\x07') {
		t.Fatalf("the pre-flight reached the terminal with a live control byte: %q", out)
	}
	for _, want := range []string{`Range Rule \x1b[31m(1)`, `Keyword Rule \x1b]0;pwned\x07(3)`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing the visible escape %q\n%s", want, out)
		}
	}
	// Sanitize is not applied twice: the blocker text folded in from
	// RenderBlockers must carry one backslash, not two.
	if strings.Contains(out, `\\x1b`) {
		t.Errorf("the blocker block was sanitized twice\n%s", out)
	}
}

// decide_suggestions (atlas suggestion_decisions.py) decides the requested ids
// before it looks for blockers, so a named update no longer blocks and a
// dismissed create is no longer auto-accepted.
func TestWithDecisionsTakesTheRequestedIDsOutOfTheGate(t *testing.T) {
	create := "11111111-1111-1111-1111-111111111111"
	update := "22222222-2222-2222-2222-222222222222"
	del := "33333333-3333-3333-3333-333333333333"
	g := BuildSuggestionGate([]api.DatasetRuleSuggestionSchema{
		pendingDetail(create, api.SuggestionActionCreate, "Range Rule (1)", 3),
		pendingDetail(update, api.SuggestionActionUpdate, "Keyword Rule (3)", 3),
		pendingDetail(del, api.SuggestionActionDelete, "Old Rule", 3),
	}, migrationID(3)).WithDecisions(
		[]uuid.UUID{uuid.MustParse(update)},
		[]uuid.UUID{uuid.MustParse(create)},
	)

	if len(g.AutoAccept) != 0 {
		t.Errorf("AutoAccept = %+v, want none: the create is dismissed", g.AutoAccept)
	}
	if len(g.Blockers) != 1 || g.Blockers[0].ID != del {
		t.Errorf("Blockers = %+v, want only the undecided delete", g.Blockers)
	}
	if len(g.Accept) != 1 || g.Accept[0].ID != update {
		t.Errorf("Accept = %+v, want the update", g.Accept)
	}
	if len(g.Dismiss) != 1 || g.Dismiss[0].ID != create {
		t.Errorf("Dismiss = %+v, want the create", g.Dismiss)
	}
	out := g.RenderPreflight(7)
	for _, want := range []string{"1 will be dismissed by this request", "1 will be accepted by this request", "1 block this migration"} {
		if !strings.Contains(out, want) {
			t.Errorf("preflight lacks %q:\n%s", want, out)
		}
	}
}

func TestWithDecisionsWithoutIDsIsTheSameGate(t *testing.T) {
	g := BuildSuggestionGate([]api.DatasetRuleSuggestionSchema{
		pendingDetail("22222222-2222-2222-2222-222222222222", api.SuggestionActionUpdate, "Keyword Rule (3)", 3),
	}, migrationID(3))
	if got := g.WithDecisions(nil, nil); len(got.Blockers) != 1 || len(got.Accept)+len(got.Dismiss) != 0 {
		t.Errorf("WithDecisions(nil, nil) = %+v, want the gate unchanged", got)
	}
}
