package dataset_rules //nolint:revive // Public package name matches the established resource domain.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/datasets"
	"github.com/qlustered/qctl/internal/dry_runs"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/markdown"
	"github.com/qlustered/qctl/internal/pkg/pollwait"
	"github.com/qlustered/qctl/internal/terminal"
)

// ErrValidationWaitTimeout is wrapped by the error WaitForValidation returns
// when the rule's validation has not settled inside
// ValidationWaitOptions.Timeout; that error exits 124.
var ErrValidationWaitTimeout = errors.New("timed out waiting for the table rule's validation")

// The errors ValidationVerdict wraps, one per outcome that did not pass.
var (
	ErrValidationFailed       = errors.New("table rule failed validation")
	ErrValidationStopped      = errors.New("table rule's validation dry run stopped")
	ErrValidationInconclusive = errors.New("table rule's validation is inconclusive")
	ErrValidationNotQueued    = errors.New("no validation dry run is queued for the table rule")
	ErrValidationHeldLocked   = errors.New("table rule's validation is held by a locked period")
	ErrValidationUnexpected   = errors.New("table rule left pending_validation")
)

// waitNow and waitSleep are WaitForValidation's clock.
var (
	waitNow   = time.Now
	waitSleep = time.Sleep
)

// SetWaitClock replaces WaitForValidation's clock (tests only). sleep must
// advance what now returns, or a wait never reaches its timeout.
func SetWaitClock(now func() time.Time, sleep func(time.Duration)) {
	waitNow, waitSleep = now, sleep
}

// ResetWaitClock restores the real clock.
func ResetWaitClock() { waitNow, waitSleep = time.Now, time.Sleep }

// ValidationOutcome is where one poll leaves a created rule's validation.
type ValidationOutcome int

const (
	// ValidationRunning has not settled.
	ValidationRunning ValidationOutcome = iota
	// ValidationPassed is a rule in enabled.
	ValidationPassed
	// ValidationPassedPendingMigration has passing validation evidence and a draft migration.
	ValidationPassedPendingMigration
	// ValidationFailed is a rule in validation_failed.
	ValidationFailed
	// ValidationStopped is a rule in pending_validation whose validation dry
	// run was killed or stopped.
	ValidationStopped
	// ValidationInconclusive is an error or blocked run with a nonempty summary.
	ValidationInconclusive
	// ValidationNotQueued is a rule in pending_validation with
	// validation_state none.
	ValidationNotQueued
	// ValidationHeldLocked is a rule in pending_validation with
	// validation_state passed on a table whose series period is locked.
	ValidationHeldLocked
	// ValidationUnexpected is a rule in any other state.
	ValidationUnexpected
)

// Passed reports whether o is ValidationPassed or
// ValidationPassedPendingMigration.
func (o ValidationOutcome) Passed() bool {
	return o == ValidationPassed || o == ValidationPassedPendingMigration
}

// ValidationSnapshot is one poll: the rule, its validation dry run when the
// poll read it, and its table's period when the poll read it and found it
// locked.
type ValidationSnapshot struct {
	Rule         *DatasetRuleDetail
	Job          *api.DryRunJobFullSchema
	LockedPeriod *datasets.LockedPeriod
}

func validationStateOf(r *DatasetRuleDetail) api.ValidationEvidenceKind {
	if r.ValidationState == nil {
		return ""
	}
	return *r.ValidationState
}

// needsValidationJob reports whether r needs its failed validation dry run read.
func needsValidationJob(r *DatasetRuleDetail) bool {
	return r.State == api.DatasetRuleStatePendingValidation &&
		validationStateOf(r) == api.ValidationEvidenceKindFailed &&
		r.ValidationDryRunJobID != nil
}

// needsLockedPeriod reports whether r needs its table's period read.
func needsLockedPeriod(r *DatasetRuleDetail) bool {
	return r.State == api.DatasetRuleStatePendingValidation &&
		validationStateOf(r) == api.ValidationEvidenceKindPassed
}

// ClassifyValidation returns where s leaves the rule's validation.
func ClassifyValidation(s *ValidationSnapshot) ValidationOutcome {
	r := s.Rule
	switch r.State {
	case api.DatasetRuleStateEnabled:
		return ValidationPassed
	case api.DatasetRuleStateValidationFailed:
		return ValidationFailed
	case api.DatasetRuleStatePendingValidation:
	default:
		return ValidationUnexpected
	}
	switch validationStateOf(r) {
	case api.ValidationEvidenceKindPassed:
		if s.LockedPeriod != nil {
			return ValidationHeldLocked
		}
		if r.PendingMigrationModelID != nil && !r.ValidationBlocksEnable {
			return ValidationPassedPendingMigration
		}
	case api.ValidationEvidenceKindNone:
		return ValidationNotQueued
	case api.ValidationEvidenceKindFailed:
		if s.Job != nil {
			switch s.Job.State {
			case api.IngestionJobStateKilled, api.IngestionJobStateStopped:
				return ValidationStopped
			case api.IngestionJobStateError, api.IngestionJobStateBlocked:
				if s.Job.Summary != nil && *s.Job.Summary != "" {
					return ValidationInconclusive
				}
			}
		}
	}
	return ValidationRunning
}

// ValidationWaitOptions configures WaitForValidation.
type ValidationWaitOptions struct {
	// Timeout bounds the whole wait.
	Timeout time.Duration
	// Progress, when set, receives the first poll that has not settled, and
	// each later one whose state, validation_state, dry run id or dry run
	// state differs from the previous one it received.
	Progress func(*ValidationSnapshot)
	// LockedPeriod, when set, reads the locked period of the table whose id
	// it is passed, returning nil for a table whose period is not locked. A
	// poll calls it only when needsLockedPeriod says the rule cannot be
	// classified without it.
	LockedPeriod func(ctx context.Context, datasetID int) (*datasets.LockedPeriod, error)
}

// WaitForValidation polls the rule created describes until ClassifyValidation
// settles, and returns that poll and its outcome. getRule reads the rule;
// getJob reads a dry run by id, and is called only when needsValidationJob
// says the rule cannot be classified without it.
//
// A poll that fails with a permanent error ends the wait with that error,
// wrapped. A cancelled ctx ends it with pollwait.ErrStopped. A wait that
// reaches opts.Timeout returns an error wrapping ErrValidationWaitTimeout.
func WaitForValidation(ctx context.Context, created *DatasetRuleDetail, getRule func(context.Context) (*DatasetRuleDetail, error), getJob func(context.Context, int) (*api.DryRunJobFullSchema, error), opts ValidationWaitOptions) (*ValidationSnapshot, ValidationOutcome, error) {
	poll := func(ctx context.Context) (*ValidationSnapshot, error) {
		rule, err := getRule(ctx)
		if err != nil {
			return nil, err
		}
		s := &ValidationSnapshot{Rule: rule}
		if needsValidationJob(rule) {
			if s.Job, err = getJob(ctx, *rule.ValidationDryRunJobID); err != nil {
				return nil, err
			}
		}
		if opts.LockedPeriod != nil && needsLockedPeriod(rule) {
			if s.LockedPeriod, err = opts.LockedPeriod(ctx, rule.DatasetID); err != nil {
				return nil, err
			}
		}
		return s, nil
	}
	s, expired, err := pollwait.Run(ctx, poll, pollwait.Options[*ValidationSnapshot]{
		Timeout:  opts.Timeout,
		Now:      waitNow,
		Sleep:    waitSleep,
		Settled:  func(s *ValidationSnapshot) bool { return ClassifyValidation(s) != ValidationRunning },
		Changed:  validationProgressChanged,
		Progress: opts.Progress,
	})
	switch {
	case errors.Is(err, pollwait.ErrStopped):
		return nil, ValidationRunning, err
	case err != nil:
		return nil, ValidationRunning, fmt.Errorf("%w\n\nthe wait for table rule %s stopped; its validation was not stopped with it",
			err, ruleLabel(created))
	case expired != nil:
		return nil, ValidationRunning, validationTimeoutError(created, opts.Timeout, expired)
	}
	return s, ClassifyValidation(s), nil
}

// validationProgressKey is what a progress line reports.
type validationProgressKey struct {
	state      api.DatasetRuleState
	validation api.ValidationEvidenceKind
	jobID      int
	jobState   api.IngestionJobState
}

func progressKey(s *ValidationSnapshot) validationProgressKey {
	k := validationProgressKey{state: s.Rule.State, validation: validationStateOf(s.Rule)}
	if s.Rule.ValidationDryRunJobID != nil {
		k.jobID = *s.Rule.ValidationDryRunJobID
	}
	if s.Job != nil {
		k.jobState = s.Job.State
	}
	return k
}

func validationProgressChanged(prev, next *ValidationSnapshot) bool {
	return progressKey(prev) != progressKey(next)
}

// ruleLabel is "<instance name> (<id>)", sanitized.
func ruleLabel(r *DatasetRuleDetail) string {
	return fmt.Sprintf("%s (%s)", terminal.Sanitize(r.InstanceName), r.ID)
}

// validationStateText is "<state> (validation: <validation_state>[, dry run
// <id>[ <job state>]])", sanitized.
func validationStateText(s *ValidationSnapshot) string {
	r := s.Rule
	text := string(r.State)
	var parts []string
	if v := validationStateOf(r); v != "" {
		parts = append(parts, "validation: "+string(v))
	}
	if r.ValidationDryRunJobID != nil {
		run := fmt.Sprintf("dry run %d", *r.ValidationDryRunJobID)
		if s.Job != nil {
			run += " " + string(s.Job.State)
		}
		parts = append(parts, run)
	}
	if len(parts) > 0 {
		text += " (" + strings.Join(parts, ", ") + ")"
	}
	return terminal.Sanitize(text)
}

// WriteValidationProgress prints one line for a poll of a rule whose
// validation has not settled, saying why a run that did not pass leaves it
// unsettled.
func WriteValidationProgress(w io.Writer, s *ValidationSnapshot) {
	line := fmt.Sprintf("table rule %s: %s", terminal.Sanitize(s.Rule.InstanceName), validationStateText(s))
	if note := stillRunningNote(s); note != "" {
		line += "; " + note
	}
	fmt.Fprintln(w, line)
}

// ValidationCheckCommand is the command that shows where r's validation
// stands: "qctl describe dry-run-job <id>" when r names its validation dry
// run, and otherwise the listing of r's table's rules.
func ValidationCheckCommand(r *DatasetRuleDetail) string {
	if id := r.ValidationDryRunJobID; id != nil {
		return fmt.Sprintf("qctl describe dry-run-job %d", *id)
	}
	return tableRulesListCommand(r.DatasetID)
}

func tableRulesListCommand(datasetID int) string {
	return fmt.Sprintf("qctl get table-rules %d --columns instance_name,state,severity", datasetID)
}

// validationTimeoutError is the qerrors.NewTimedOut error naming what the wait
// was still waiting for.
func validationTimeoutError(created *DatasetRuleDetail, timeout time.Duration, expired *pollwait.Expired[*ValidationSnapshot]) error {
	last := expired.Last
	var reason string
	next := tableRulesListCommand(created.DatasetID)
	if last == nil {
		reason = fmt.Sprintf("no poll of table rule %s succeeded", ruleLabel(created))
	} else {
		reason = fmt.Sprintf("table rule %s is still %s", ruleLabel(last.Rule), validationStateText(last))
		if note := stillRunningNote(last); note != "" {
			reason += "; " + note
		}
		reason += "; only the wait stopped"
		next = ValidationCheckCommand(last.Rule)
	}
	err := fmt.Errorf("%w after %s: %s\n\nNext: %s", ErrValidationWaitTimeout, timeout, reason, next)
	if expired.LastErr != nil {
		err = fmt.Errorf("%w\n\nlast poll error: %w", err, expired.LastErr)
	}
	return qerrors.NewTimedOut(err)
}

// stillRunningNote says why a pending_validation rule whose last dry run did
// not pass can still settle; "" when there is nothing to add.
func stillRunningNote(s *ValidationSnapshot) string {
	if s.Job == nil {
		return ""
	}
	switch s.Job.State {
	case api.IngestionJobStateFinished:
		return "the run finished without a verdict on the rule; " + rerunNote(s.Rule)
	case api.IngestionJobStateError:
		return healerNote + "; " + rerunNote(s.Rule)
	case api.IngestionJobStateVersionIDMismatch, api.IngestionJobStateBlocked:
		return healerNote
	}
	return ""
}

const healerNote = "the backend's stuck-job healer may restart the run"

// rerunNote says whether the backend's automatic re-run can pick up the
// validation of r once its run has ended.
func rerunNote(r *DatasetRuleDetail) string {
	if r.PendingMigrationModelID != nil {
		return "its draft migration prevents automatic re-queueing"
	}
	return "the backend can re-run the validation about 5 minutes after the run, at most twice, before it sets the rule validation_failed"
}

// WriteValidationOutcome prints the line a settled wait ends with.
func WriteValidationOutcome(w io.Writer, s *ValidationSnapshot, outcome ValidationOutcome) {
	r := s.Rule
	label := ruleLabel(r)
	switch outcome {
	case ValidationPassed:
		fmt.Fprintf(w, "Validation passed: %s is %s\n", label, validationStateText(s))
	case ValidationPassedPendingMigration:
		fmt.Fprintf(w, "Validation passed: %s stays pending_validation until draft migration %d is applied\n", label, *r.PendingMigrationModelID)
	case ValidationFailed:
		fmt.Fprintf(w, "Validation failed: %s is %s\n", label, validationStateText(s))
	case ValidationStopped:
		fmt.Fprintf(w, "Validation stopped: %s is %s\n", label, validationStateText(s))
	case ValidationInconclusive:
		fmt.Fprintf(w, "Validation inconclusive: %s is %s\n", label, validationStateText(s))
	case ValidationNotQueued:
		fmt.Fprintf(w, "Validation not queued: %s is %s\n", label, validationStateText(s))
	case ValidationHeldLocked:
		fmt.Fprintf(w, "Validation held: %s is %s; %s is locked\n", label, validationStateText(s), s.LockedPeriod)
	default:
		fmt.Fprintf(w, "Validation ended: %s is %s\n", label, validationStateText(s))
	}
}

// ValidationReason is what ValidationVerdict reports about why a validation
// did not pass.
type ValidationReason struct {
	// Notification is the msg of the caller's validation_failed notification
	// for the rule; "" when there is none.
	Notification string
	// Job is the rule's validation dry run; nil when it was not read.
	Job *api.DryRunJobFullSchema
}

// ValidationVerdict returns nil for an outcome that Passed. Otherwise it
// returns an error wrapping that outcome's Err* sentinel, naming the rule and
// its state, followed by reason's notification as its reason, the dry run's
// rule_load_errors, error_message, summary and top exception messages, and a
// Next: line.
func ValidationVerdict(s *ValidationSnapshot, outcome ValidationOutcome, reason ValidationReason) error {
	if outcome.Passed() {
		return nil
	}
	r := s.Rule
	label := ruleLabel(r)
	var sentinel error
	var head string
	switch outcome {
	case ValidationFailed:
		sentinel, head = ErrValidationFailed, fmt.Sprintf("%s is %s", label, validationStateText(s))
	case ValidationStopped:
		sentinel, head = ErrValidationStopped, fmt.Sprintf("%s is %s; only the wait ended: %s",
			label, validationStateText(s), rerunNote(r))
	case ValidationInconclusive:
		restart := rerunNote(r)
		if s.Job != nil && s.Job.State == api.IngestionJobStateBlocked {
			restart = "the stuck-job healer and the automatic re-run both skip a blocked run that wrote its summary"
		}
		sentinel, head = ErrValidationInconclusive, fmt.Sprintf(
			"%s is %s; the run wrote its summary, so validation is inconclusive; %s",
			label, validationStateText(s), restart)
	case ValidationNotQueued:
		sentinel, head = ErrValidationNotQueued, fmt.Sprintf("%s is %s", label, validationStateText(s))
	case ValidationHeldLocked:
		sentinel, head = ErrValidationHeldLocked, fmt.Sprintf(
			"%s is %s; its validation dry run passed, but %s is locked, so the rule stays pending_validation while the period is locked",
			label, validationStateText(s), s.LockedPeriod)
	case ValidationUnexpected:
		sentinel, head = ErrValidationUnexpected, fmt.Sprintf("%s is %s", label, validationStateText(s))
	default:
		return fmt.Errorf("table rule %s has not settled: %s", label, validationStateText(s))
	}

	var b strings.Builder
	if reason.Notification != "" {
		b.WriteString("\n\nreason: " + indentContinuation(markdown.PlainText(reason.Notification)))
	}
	writeJobReason(&b, reason.Job)

	next := fmt.Sprintf("qctl describe table-rule %s --table-id %d", r.ID, r.DatasetID)
	switch {
	case outcome == ValidationHeldLocked:
		next = s.LockedPeriod.UnlockCommand() + " (requires deliverable:admin)"
	case reason.Job != nil:
		next = fmt.Sprintf("qctl describe dry-run-job %d", reason.Job.ID)
	case r.ValidationDryRunJobID != nil:
		next = fmt.Sprintf("qctl describe dry-run-job %d", *r.ValidationDryRunJobID)
	}
	return fmt.Errorf("%w: %s%s\n\nNext: %s", sentinel, head, b.String(), next)
}

// maxReasonExceptions caps the exception messages a verdict lists.
const maxReasonExceptions = 5

// writeJobReason appends the parts of job that say why it did not pass.
func writeJobReason(b *strings.Builder, job *api.DryRunJobFullSchema) {
	if job == nil {
		return
	}
	dry_runs.WriteFailureDetail(b, job)
	if job.Summary != nil && strings.TrimSpace(*job.Summary) != "" {
		b.WriteString("\n\nsummary:\n  " + indentContinuation(strings.TrimSpace(markdown.PlainText(*job.Summary))))
	}
	if exceptions := topExceptions(job); len(exceptions) > 0 {
		b.WriteString("\n\ntop exceptions:")
		for i, e := range exceptions {
			if i == maxReasonExceptions {
				break
			}
			fmt.Fprintf(b, "\n  %s: %s (%d times)", terminal.Sanitize(e.Type),
				indentContinuation(terminal.Sanitize(e.Msg)), e.Count)
		}
	}
}

// topExceptions is the dry run's metrics.exceptions.top_messages, nil when
// the metrics carry none.
func topExceptions(job *api.DryRunJobFullSchema) []api.ExceptionDetail {
	if job.Metrics == nil {
		return nil
	}
	raw, err := json.Marshal(job.Metrics)
	if err != nil {
		return nil
	}
	var metrics struct {
		Exceptions *api.Exceptions `json:"exceptions"`
	}
	if json.Unmarshal(raw, &metrics) != nil || metrics.Exceptions == nil {
		return nil
	}
	return metrics.Exceptions.TopMessages
}

// indentContinuation indents every line of s after the first by two spaces.
func indentContinuation(s string) string {
	return strings.ReplaceAll(s, "\n", "\n  ")
}
