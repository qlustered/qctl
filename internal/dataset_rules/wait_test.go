package dataset_rules //nolint:revive // Public package name matches the established resource domain.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/datasets"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/pkg/pollwait"
)

const waitRuleID = "6f1c2a3b-4d5e-4f60-8a7b-9c0d1e2f3a4b"

// waitRule is a rule on table 8 in state, with validation_state validation
// (omitted when "") and validation dry run jobID (omitted when 0).
func waitRule(state api.DatasetRuleState, validation api.ValidationEvidenceKind, jobID int) *DatasetRuleDetail {
	r := &DatasetRuleDetail{
		ID:           uuid.MustParse(waitRuleID),
		InstanceName: "Not Null on amount",
		DatasetID:    8,
		State:        state,
	}
	if validation != "" {
		v := validation
		r.ValidationState = &v
	}
	if jobID != 0 {
		id := jobID
		r.ValidationDryRunJobID = &id
	}
	return r
}

func withMigration(r *DatasetRuleDetail) *DatasetRuleDetail {
	migrationID := 12
	r.PendingMigrationModelID = &migrationID
	return r
}

// waitJob decodes a dry-run job body, so metrics go through the generated
// union exactly as a response does.
func waitJob(t *testing.T, body string) *api.DryRunJobFullSchema {
	t.Helper()
	var job api.DryRunJobFullSchema
	if err := json.Unmarshal([]byte(body), &job); err != nil {
		t.Fatalf("decode job: %v", err)
	}
	return &job
}

func jobInState(t *testing.T, state string) *api.DryRunJobFullSchema {
	t.Helper()
	return waitJob(t, `{"id": 77, "dataset_id": 8, "data_source_model_id": 3, "state": "`+state+`",
		"rule_run_specs": [], "sampling": {}}`)
}

func TestClassifyValidation(t *testing.T) {
	pending := api.DatasetRuleStatePendingValidation
	failed := api.ValidationEvidenceKindFailed
	cases := []struct {
		name string
		rule *DatasetRuleDetail
		job  string
		want ValidationOutcome
	}{
		{"enabled", waitRule(api.DatasetRuleStateEnabled, api.ValidationEvidenceKindPassed, 77), "", ValidationPassed},
		{"validation_failed", waitRule(api.DatasetRuleStateValidationFailed, failed, 77), "", ValidationFailed},
		{"pending with a draft migration", withMigration(waitRule(pending, api.ValidationEvidenceKindPassed, 77)), "", ValidationPassedPendingMigration},
		{"pending with nothing queued", waitRule(pending, api.ValidationEvidenceKindNone, 0), "", ValidationNotQueued},
		{"pending and queued", waitRule(pending, api.ValidationEvidenceKindQueued, 0), "", ValidationRunning},
		{"pending and in flight", waitRule(pending, api.ValidationEvidenceKindInFlight, 77), "", ValidationRunning},
		{"pending with no validation_state", waitRule(pending, "", 0), "", ValidationRunning},
		{"pending, run killed", waitRule(pending, failed, 77), "killed", ValidationStopped},
		{"pending, run stopped", waitRule(pending, failed, 77), "stopped", ValidationStopped},
		{"pending, run finished without a verdict", waitRule(pending, failed, 77), "finished", ValidationRunning},
		{"pending, run error", waitRule(pending, failed, 77), "error", ValidationRunning},
		{"pending, run blocked", waitRule(pending, failed, 77), "blocked", ValidationRunning},
		{"pending, run version_id_mismatch", waitRule(pending, failed, 77), "version_id_mismatch", ValidationRunning},
		{"pending, failed, run not read", waitRule(pending, failed, 77), "", ValidationRunning},
		{"pending and stale", waitRule(pending, api.ValidationEvidenceKindStale, 77), "", ValidationRunning},
		{"pending and not needed", waitRule(pending, api.ValidationEvidenceKindNotNeeded, 0), "", ValidationRunning},
		{"disabled", waitRule(api.DatasetRuleStateDisabled, failed, 77), "", ValidationUnexpected},
		{"deleted", waitRule(api.DatasetRuleStateDeleted, failed, 77), "", ValidationUnexpected},
		{"error", waitRule(api.DatasetRuleStateError, api.ValidationEvidenceKindPassed, 77), "", ValidationUnexpected},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, draft := range []bool{false, true} {
				rule := *tc.rule
				if draft {
					withMigration(&rule)
				}
				s := &ValidationSnapshot{Rule: &rule}
				if tc.job != "" {
					s.Job = jobInState(t, tc.job)
				}
				if got := ClassifyValidation(s); got != tc.want {
					t.Errorf("draft %t: ClassifyValidation = %d, want %d", draft, got, tc.want)
				}
			}
		})
	}
}

func TestClassifyValidationPassedNeedsDraftAndClearBlocker(t *testing.T) {
	for _, tc := range []struct {
		draft   bool
		blocked bool
		want    ValidationOutcome
	}{
		{false, false, ValidationRunning},
		{true, false, ValidationPassedPendingMigration},
		{true, true, ValidationRunning},
	} {
		rule := waitRule(api.DatasetRuleStatePendingValidation, api.ValidationEvidenceKindPassed, 77)
		if tc.draft {
			withMigration(rule)
		}
		rule.ValidationBlocksEnable = tc.blocked
		if got := ClassifyValidation(&ValidationSnapshot{Rule: rule}); got != tc.want {
			t.Errorf("draft %t, blocked %t: ClassifyValidation = %d, want %d", tc.draft, tc.blocked, got, tc.want)
		}
	}
}

// waitLockedPeriod is a locked period on table 8's series.
func waitLockedPeriod() *datasets.LockedPeriod {
	return &datasets.LockedPeriod{
		SeriesID:   uuid.MustParse("0b8c3f9e-1d2a-4e5f-9a6b-7c8d9e0f1a2b"),
		SeriesName: "Monthly bordereau",
		PeriodKey:  "2026-09",
	}
}

// A locked period holds a passed rule whether or not it carries a draft, and
// says nothing about a rule whose validation has not passed.
func TestClassifyValidationLockedPeriodHoldsAPassedRule(t *testing.T) {
	pending := api.DatasetRuleStatePendingValidation
	for _, tc := range []struct {
		name string
		rule *DatasetRuleDetail
		want ValidationOutcome
	}{
		{"passed, no draft", waitRule(pending, api.ValidationEvidenceKindPassed, 77), ValidationHeldLocked},
		{"passed, draft", withMigration(waitRule(pending, api.ValidationEvidenceKindPassed, 77)), ValidationHeldLocked},
		{"in flight", waitRule(pending, api.ValidationEvidenceKindInFlight, 77), ValidationRunning},
		{"none", waitRule(pending, api.ValidationEvidenceKindNone, 0), ValidationNotQueued},
		{"enabled", waitRule(api.DatasetRuleStateEnabled, api.ValidationEvidenceKindPassed, 77), ValidationPassed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &ValidationSnapshot{Rule: tc.rule, LockedPeriod: waitLockedPeriod()}
			if got := ClassifyValidation(s); got != tc.want {
				t.Errorf("ClassifyValidation = %d, want %d", got, tc.want)
			}
		})
	}
}

// The period is read only for a pending rule whose validation passed, and a
// locked one ends the wait.
func TestWaitForValidationReadsThePeriodOnlyOncePassed(t *testing.T) {
	stubWaitClock(t)
	pending := api.DatasetRuleStatePendingValidation
	getRule, _ := pollRules(
		waitRule(pending, api.ValidationEvidenceKindQueued, 0),
		waitRule(pending, api.ValidationEvidenceKindInFlight, 77),
		waitRule(pending, api.ValidationEvidenceKindPassed, 77),
		waitRule(pending, api.ValidationEvidenceKindPassed, 77),
	)
	var periodReads []int
	locked := []*datasets.LockedPeriod{nil, waitLockedPeriod()}
	opts := ValidationWaitOptions{
		Timeout: time.Minute,
		LockedPeriod: func(_ context.Context, datasetID int) (*datasets.LockedPeriod, error) {
			periodReads = append(periodReads, datasetID)
			return locked[min(len(periodReads), len(locked))-1], nil
		},
	}
	noJob := func(context.Context, int) (*api.DryRunJobFullSchema, error) {
		t.Fatal("job read")
		return nil, nil
	}
	s, outcome, err := WaitForValidation(context.Background(), waitRule(pending, api.ValidationEvidenceKindQueued, 0), getRule, noJob, opts)
	if err != nil {
		t.Fatalf("WaitForValidation: %v", err)
	}
	if outcome != ValidationHeldLocked || s.LockedPeriod == nil {
		t.Errorf("outcome = %d with period %v, want held by the locked period", outcome, s.LockedPeriod)
	}
	if len(periodReads) != 2 || periodReads[0] != 8 || periodReads[1] != 8 {
		t.Errorf("period reads = %v, want two reads of table 8, made only once validation_state is passed", periodReads)
	}
}

func TestClassifyValidationWrittenSummary(t *testing.T) {
	for _, tc := range []struct {
		state   string
		summary *string
		want    ValidationOutcome
	}{
		{"error", nil, ValidationRunning},
		{"error", strPtr(""), ValidationRunning},
		{"error", strPtr("No verdict"), ValidationInconclusive},
		{"error", strPtr(" "), ValidationInconclusive},
		{"blocked", nil, ValidationRunning},
		{"blocked", strPtr(""), ValidationRunning},
		{"blocked", strPtr("No verdict"), ValidationInconclusive},
		{"finished", strPtr("No verdict"), ValidationRunning},
		{"version_id_mismatch", strPtr("No verdict"), ValidationRunning},
	} {
		job := jobInState(t, tc.state)
		job.Summary = tc.summary
		s := &ValidationSnapshot{
			Rule: withMigration(waitRule(api.DatasetRuleStatePendingValidation, api.ValidationEvidenceKindFailed, 77)),
			Job:  job,
		}
		if got := ClassifyValidation(s); got != tc.want {
			t.Errorf("state %s, summary %q: outcome = %d, want %d", tc.state, deref(tc.summary), got, tc.want)
		}
	}
}

// stubWaitClock replaces WaitForValidation's clock with one that only moves
// when the wait sleeps.
func stubWaitClock(t *testing.T) {
	t.Helper()
	now := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	SetWaitClock(func() time.Time { return now }, func(d time.Duration) { now = now.Add(d) })
	t.Cleanup(ResetWaitClock)
}

// pollRules answers each rule poll with the next of answers, repeating the
// last; an answer that is an error is returned as the poll's error.
func pollRules(answers ...any) (func(context.Context) (*DatasetRuleDetail, error), *int) {
	polls := 0
	return func(context.Context) (*DatasetRuleDetail, error) {
		a := answers[min(polls, len(answers)-1)]
		polls++
		if err, ok := a.(error); ok {
			return nil, err
		}
		return a.(*DatasetRuleDetail), nil
	}, &polls
}

func TestWaitForValidationReadsTheJobOnlyWhenItDecides(t *testing.T) {
	stubWaitClock(t)
	pending := api.DatasetRuleStatePendingValidation
	getRule, _ := pollRules(
		withMigration(waitRule(pending, api.ValidationEvidenceKindQueued, 0)),
		withMigration(waitRule(pending, api.ValidationEvidenceKindInFlight, 77)),
		withMigration(waitRule(pending, api.ValidationEvidenceKindFailed, 77)),
	)
	var jobReads []int
	getJob := func(_ context.Context, id int) (*api.DryRunJobFullSchema, error) {
		jobReads = append(jobReads, id)
		return jobInState(t, "killed"), nil
	}
	s, outcome, err := WaitForValidation(context.Background(), waitRule(pending, api.ValidationEvidenceKindQueued, 0), getRule, getJob, ValidationWaitOptions{Timeout: time.Minute})
	if err != nil {
		t.Fatalf("WaitForValidation: %v", err)
	}
	if outcome != ValidationStopped || s.Job == nil || s.Job.State != api.IngestionJobStateKilled {
		t.Errorf("outcome = %d with job %+v, want stopped with the killed job", outcome, s.Job)
	}
	if len(jobReads) != 1 || jobReads[0] != 77 {
		t.Errorf("job reads = %v, want one read of 77, made only once validation_state is failed", jobReads)
	}
}

func TestWaitForValidationReportsProgressOnlyWhenItChanges(t *testing.T) {
	stubWaitClock(t)
	pending := api.DatasetRuleStatePendingValidation
	getRule, polls := pollRules(
		waitRule(pending, api.ValidationEvidenceKindQueued, 0),
		waitRule(pending, api.ValidationEvidenceKindQueued, 0),
		waitRule(pending, api.ValidationEvidenceKindInFlight, 77),
		waitRule(pending, api.ValidationEvidenceKindInFlight, 77),
		waitRule(api.DatasetRuleStateEnabled, api.ValidationEvidenceKindPassed, 77),
	)
	var progress bytes.Buffer
	_, outcome, err := WaitForValidation(context.Background(), waitRule(pending, api.ValidationEvidenceKindQueued, 0), getRule, nil, ValidationWaitOptions{
		Timeout:  time.Minute,
		Progress: func(s *ValidationSnapshot) { WriteValidationProgress(&progress, s) },
	})
	if err != nil || outcome != ValidationPassed {
		t.Fatalf("WaitForValidation = (%d, %v), want passed", outcome, err)
	}
	if *polls != 5 {
		t.Errorf("polls = %d, want 5", *polls)
	}
	want := "table rule Not Null on amount: pending_validation (validation: queued)\n" +
		"table rule Not Null on amount: pending_validation (validation: in_flight, dry run 77)\n"
	if progress.String() != want {
		t.Errorf("progress:\n--- got ---\n%s--- want ---\n%s", progress.String(), want)
	}
}

func TestWaitForValidationTimeoutNamesTheStateItWasStillIn(t *testing.T) {
	stubWaitClock(t)
	pending := api.DatasetRuleStatePendingValidation
	cases := []struct {
		name       string
		validation api.ValidationEvidenceKind
		job        string
		want       []string
	}{
		{"in flight", api.ValidationEvidenceKindInFlight, "", []string{
			"is still pending_validation (validation: in_flight, dry run 77); only the wait stopped",
			"Next: qctl describe dry-run-job 77",
		}},
		{"finished without a verdict", api.ValidationEvidenceKindFailed, "finished", []string{
			"is still pending_validation (validation: failed, dry run 77 finished); the run finished without a verdict",
			"the backend can re-run the validation about 5 minutes after the run, at most twice, before it sets the rule validation_failed",
		}},
		{"healable error", api.ValidationEvidenceKindFailed, "error", []string{
			"is still pending_validation (validation: failed, dry run 77 error); the backend's stuck-job healer may restart the run; the backend can re-run the validation about 5 minutes after the run, at most twice, before it sets the rule validation_failed",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rule := waitRule(pending, tc.validation, 77)
			getRule, _ := pollRules(rule)
			getJob := func(context.Context, int) (*api.DryRunJobFullSchema, error) { return jobInState(t, tc.job), nil }
			_, _, err := WaitForValidation(context.Background(), rule, getRule, getJob, ValidationWaitOptions{Timeout: 30 * time.Second})
			if !errors.Is(err, ErrValidationWaitTimeout) {
				t.Fatalf("err = %v, want ErrValidationWaitTimeout", err)
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitTimeout {
				t.Errorf("exit code = %d, want %d", got, qerrors.ExitTimeout)
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error lacks %q:\n%s", want, err.Error())
				}
			}
		})
	}
}

func TestWaitForValidationTimeoutWithNoSuccessfulPoll(t *testing.T) {
	stubWaitClock(t)
	created := waitRule(api.DatasetRuleStatePendingValidation, api.ValidationEvidenceKindQueued, 0)
	transient := errors.New("connection reset")
	getRule, _ := pollRules(transient)
	_, _, err := WaitForValidation(context.Background(), created, getRule, nil, ValidationWaitOptions{Timeout: 10 * time.Second})
	if !errors.Is(err, ErrValidationWaitTimeout) || !errors.Is(err, transient) {
		t.Fatalf("err = %v, want the timeout wrapping the last poll error", err)
	}
	for _, want := range []string{
		"no poll of table rule Not Null on amount (" + waitRuleID + ") succeeded",
		"Next: qctl get table-rules 8 --columns instance_name,state,severity",
		"last poll error: connection reset",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q:\n%s", want, err.Error())
		}
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitTimeout {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitTimeout)
	}
}

// A closed Done ends the wait with pollwait.ErrStopped, unwrapped, before the
// next poll.
func TestWaitForValidationDoneStopsTheWait(t *testing.T) {
	stubWaitClock(t)
	created := waitRule(api.DatasetRuleStatePendingValidation, api.ValidationEvidenceKindQueued, 0)
	getRule, polls := pollRules(created)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cancel()
	s, _, err := WaitForValidation(ctx, created, getRule, nil, ValidationWaitOptions{Timeout: time.Minute})
	if s != nil || err != pollwait.ErrStopped {
		t.Fatalf("WaitForValidation = (%v, %v), want pollwait.ErrStopped", s, err)
	}
	if *polls != 0 {
		t.Errorf("polls = %d, want 0", *polls)
	}
}

func TestValidationCheckCommand(t *testing.T) {
	queued := waitRule(api.DatasetRuleStatePendingValidation, api.ValidationEvidenceKindQueued, 0)
	if got, want := ValidationCheckCommand(queued), "qctl get table-rules 8 --columns instance_name,state,severity"; got != want {
		t.Errorf("no dry run: got %q, want %q", got, want)
	}
	inFlight := waitRule(api.DatasetRuleStatePendingValidation, api.ValidationEvidenceKindInFlight, 77)
	if got, want := ValidationCheckCommand(inFlight), "qctl describe dry-run-job 77"; got != want {
		t.Errorf("dry run 77: got %q, want %q", got, want)
	}
}

func TestWaitForValidationPermanentPollErrorKeepsItsExitCode(t *testing.T) {
	stubWaitClock(t)
	created := waitRule(api.DatasetRuleStatePendingValidation, api.ValidationEvidenceKindQueued, 0)
	getRule, polls := pollRules(qerrors.NewNotFound("Table Rule not found"))
	_, _, err := WaitForValidation(context.Background(), created, getRule, nil, ValidationWaitOptions{Timeout: time.Minute})
	if got := qerrors.GetExitCode(err); got != qerrors.ExitNotFound {
		t.Errorf("exit code = %d, want %d (err: %v)", got, qerrors.ExitNotFound, err)
	}
	if *polls != 1 {
		t.Errorf("polls = %d, want 1", *polls)
	}
	if !strings.Contains(err.Error(), "its validation was not stopped with it") {
		t.Errorf("error does not say the validation goes on:\n%s", err.Error())
	}
}

func TestValidationVerdict(t *testing.T) {
	pending := api.DatasetRuleStatePendingValidation
	failedJob := `{"id": 77, "dataset_id": 8, "data_source_model_id": 3, "state": "finished",
		"rule_run_specs": [], "sampling": {},
		"rule_load_errors": [{"position": 0, "error_type": "ImportError", "message": "No module named 'pandas'", "rule_revision_id": "r"}],
		"summary": "**Validation** failed\nsecond line",
		"metrics": {"mode": "single", "exceptions": {"count": 4, "top_messages": [
			{"type": "ValueError", "msg": "bad amount", "count": 3, "rule_indices": [0], "sample_signatures": []}]}}}`
	cases := []struct {
		name     string
		snapshot func(t *testing.T) *ValidationSnapshot
		outcome  ValidationOutcome
		reason   func(t *testing.T) ValidationReason
		sentinel error
		want     []string
	}{
		{
			name: "failed, every source",
			snapshot: func(*testing.T) *ValidationSnapshot {
				return &ValidationSnapshot{Rule: waitRule(api.DatasetRuleStateValidationFailed, api.ValidationEvidenceKindFailed, 77)}
			},
			outcome: ValidationFailed,
			reason: func(t *testing.T) ValidationReason {
				return ValidationReason{Notification: "Validation dry run did not evaluate any eligible rows.", Job: waitJob(t, failedJob)}
			},
			sentinel: ErrValidationFailed,
			want: []string{
				"table rule failed validation: Not Null on amount (" + waitRuleID + ") is validation_failed (validation: failed, dry run 77)",
				"reason: Validation dry run did not evaluate any eligible rows.",
				"rule load errors:\n  position 0 (ImportError): No module named 'pandas'",
				"summary:\n  Validation failed\n  second line",
				"top exceptions:\n  ValueError: bad amount (3 times)",
				"Next: qctl describe dry-run-job 77",
			},
		},
		{
			name: "stopped",
			snapshot: func(t *testing.T) *ValidationSnapshot {
				return &ValidationSnapshot{Rule: waitRule(pending, api.ValidationEvidenceKindFailed, 77), Job: waitJob(t,
					`{"id": 77, "dataset_id": 8, "data_source_model_id": 3, "state": "killed", "rule_run_specs": [], "sampling": {},
					"error_message": "Killed by the user"}`)}
			},
			outcome:  ValidationStopped,
			reason:   func(*testing.T) ValidationReason { return ValidationReason{} },
			sentinel: ErrValidationStopped,
			want: []string{
				"is pending_validation (validation: failed, dry run 77 killed); only the wait ended: the backend can re-run the validation about 5 minutes after the run, at most twice, before it sets the rule validation_failed",
				"Next: qctl describe dry-run-job 77",
			},
		},
		{
			name: "not queued",
			snapshot: func(*testing.T) *ValidationSnapshot {
				return &ValidationSnapshot{Rule: waitRule(pending, api.ValidationEvidenceKindNone, 0)}
			},
			outcome: ValidationNotQueued,
			reason: func(*testing.T) ValidationReason {
				return ValidationReason{Notification: "Validation of rule \"x\" could not start: this table has no active data source."}
			},
			sentinel: ErrValidationNotQueued,
			want: []string{
				"is pending_validation (validation: none)",
				"reason: Validation of rule \"x\" could not start: this table has no active data source.",
				"Next: qctl describe table-rule " + waitRuleID + " --table-id 8",
			},
		},
		{
			name: "held by a locked period",
			snapshot: func(*testing.T) *ValidationSnapshot {
				return &ValidationSnapshot{Rule: waitRule(pending, api.ValidationEvidenceKindPassed, 77), LockedPeriod: waitLockedPeriod()}
			},
			outcome: ValidationHeldLocked,
			reason: func(*testing.T) ValidationReason {
				return ValidationReason{Notification: "The dry run passed, but this table's period is locked."}
			},
			sentinel: ErrValidationHeldLocked,
			want: []string{
				"is pending_validation (validation: passed, dry run 77); its validation dry run passed, but period \"2026-09\" of series \"Monthly bordereau\" is locked",
				"reason: The dry run passed, but this table's period is locked.",
				"Next: qctl table-series unlock 0b8c3f9e-1d2a-4e5f-9a6b-7c8d9e0f1a2b --period 2026-09 (requires deliverable:admin)",
			},
		},
		{
			name: "unexpected state",
			snapshot: func(*testing.T) *ValidationSnapshot {
				return &ValidationSnapshot{Rule: waitRule(api.DatasetRuleStateDisabled, "", 0)}
			},
			outcome:  ValidationUnexpected,
			reason:   func(*testing.T) ValidationReason { return ValidationReason{} },
			sentinel: ErrValidationUnexpected,
			want:     []string{"table rule left pending_validation: Not Null on amount (" + waitRuleID + ") is disabled"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidationVerdict(tc.snapshot(t), tc.outcome, tc.reason(t))
			if !errors.Is(err, tc.sentinel) {
				t.Fatalf("err = %v, want it to wrap %v", err, tc.sentinel)
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitGenericError {
				t.Errorf("exit code = %d, want 1", got)
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error lacks %q:\n%s", want, err.Error())
				}
			}
		})
	}
}

func TestValidationVerdictIsNilWhenValidationPassed(t *testing.T) {
	for _, outcome := range []ValidationOutcome{ValidationPassed, ValidationPassedPendingMigration} {
		s := &ValidationSnapshot{Rule: waitRule(api.DatasetRuleStateEnabled, api.ValidationEvidenceKindPassed, 77)}
		if err := ValidationVerdict(s, outcome, ValidationReason{Notification: "stale"}); err != nil {
			t.Errorf("outcome %d: err = %v, want nil", outcome, err)
		}
	}
}

func TestValidationVerdictSanitizesServerText(t *testing.T) {
	rule := waitRule(api.DatasetRuleStateValidationFailed, api.ValidationEvidenceKindFailed, 0)
	rule.InstanceName = "evil\x1b[2Jname"
	err := ValidationVerdict(&ValidationSnapshot{Rule: rule}, ValidationFailed, ValidationReason{Notification: "bad\x1b]0;title\x07msg"})
	if strings.ContainsRune(err.Error(), '\x1b') || strings.ContainsRune(err.Error(), '\x07') {
		t.Errorf("error carries raw control bytes: %q", err.Error())
	}
}
