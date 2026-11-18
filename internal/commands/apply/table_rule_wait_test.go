package apply

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/dataset_rules"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"golang.org/x/term"
)

const (
	waitRulePath    = "/api/orgs/" + testOrgID + "/dataset-rules/" + testTableRuleID
	waitDryRunPath  = "/api/orgs/" + testOrgID + "/dry-runs/77"
	waitInstantiate = "/api/orgs/" + testOrgID + "/rule-revisions/" + testRuleRevID + "/instantiate"
	waitNotifyPath  = "/api/orgs/" + testOrgID + "/notifications"
	waitAlertsPath  = "/api/orgs/" + testOrgID + "/alerts"
	waitTablePath   = "/api/orgs/" + testOrgID + "/datasets/1"
	waitSeriesID    = "0b8c3f9e-1d2a-4e5f-9a6b-7c8d9e0f1a2b"
)

// waitDetail is the rule apply creates, in state, with validation_state
// validation (omitted when "") and validation dry run jobID (omitted when 0).
func waitDetail(state api.DatasetRuleState, validation api.ValidationEvidenceKind, jobID int) dataset_rules.DatasetRuleDetail {
	d := sampleDatasetRuleDetailForApply()
	d.State = state
	if validation != "" {
		v := validation
		d.ValidationState = &v
	}
	if jobID != 0 {
		id := jobID
		d.ValidationDryRunJobID = &id
	}
	return d
}

func pendingDetail(validation api.ValidationEvidenceKind, jobID int) dataset_rules.DatasetRuleDetail {
	return waitDetail(api.DatasetRuleStatePendingValidation, validation, jobID)
}

// waitServer is a mock server for one TableRule create under --wait.
type waitServer struct {
	mock *testutil.MockAPIServer
	path string
}

// serveTableRuleWait answers the instantiate with created and each poll of the
// rule with the next of polls, repeating the last. It also serves the table's
// rule list empty, the rule-revision lookup, the table in no series, and an
// inbox and an alert list with nothing in them. The wait's clock is stubbed so
// polling costs no time.
func serveTableRuleWait(t *testing.T, created dataset_rules.DatasetRuleDetail, polls ...dataset_rules.DatasetRuleDetail) *waitServer {
	t.Helper()
	now := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	dataset_rules.SetWaitClock(func() time.Time { return now }, func(d time.Duration) { now = now.Add(d) })
	t.Cleanup(dataset_rules.ResetWaitClock)

	env, mock := setupTableRuleApplyEnv(t)
	t.Cleanup(env.Cleanup)
	t.Cleanup(mock.Close)
	registerRuleRevisionDetails(mock)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1/dataset-rules", func(w http.ResponseWriter, _ *http.Request) {
		total, page := 0, 1
		testutil.RespondJSON(w, http.StatusOK, dataset_rules.DatasetRuleList{Results: []dataset_rules.DatasetRuleTiny{}, TotalRows: &total, Page: &page})
	})
	mock.RegisterHandler("POST", waitInstantiate, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusCreated, created)
	})
	served := 0
	mock.RegisterHandler("GET", waitRulePath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, polls[min(served, len(polls)-1)])
		served++
	})
	serveNotifications(mock)
	serveAlerts(mock, nil)
	serveTable(mock, nil)
	return &waitServer{mock: mock, path: env.CreateFile("table-rule.yaml", tableRuleInstantiateDoc("email_check"))}
}

func (s *waitServer) run(t *testing.T, extra ...string) (stdout, stderr string, err error) {
	t.Helper()
	return execApplyRoot(t, append([]string{"apply", "-f", s.path, "--yes"}, extra...)...)
}

// serveDryRun answers GET dry run 77 with job.
func serveDryRun(mock *testutil.MockAPIServer, job map[string]any) {
	mock.RegisterHandler("GET", waitDryRunPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, job)
	})
}

func dryRunJob(state string, extra map[string]any) map[string]any {
	job := map[string]any{"id": 77, "dataset_id": 1, "data_source_model_id": 3, "state": state, "rule_run_specs": []any{}, "sampling": map[string]any{}}
	for k, v := range extra {
		job[k] = v
	}
	return job
}

// serveNotifications answers the inbox listing with one validation_failed
// notification per entry of byRule, keyed by dataset rule id.
func serveNotifications(mock *testutil.MockAPIServer, byRule ...[2]string) {
	mock.RegisterHandler("GET", waitNotifyPath, func(w http.ResponseWriter, r *http.Request) {
		results := make([]api.NotificationSchema, 0, len(byRule))
		for _, entry := range byRule {
			ruleID := uuid.MustParse(entry[0])
			msg := entry[1]
			results = append(results, api.NotificationSchema{
				ID:               uuid.New(),
				NotificationType: api.ValidationFailed,
				DatasetRuleID:    &ruleID,
				Msg:              &msg,
				OrganizationID:   uuid.MustParse(testOrgID),
			})
		}
		testutil.RespondJSON(w, http.StatusOK, api.NotificationListSchema{Results: results})
	})
}

// serveTable answers GET table 1, in no series when period is nil and in
// series waitSeriesID with period otherwise.
func serveTable(mock *testutil.MockAPIServer, period map[string]any) {
	mock.RegisterHandler("GET", waitTablePath, func(w http.ResponseWriter, _ *http.Request) {
		table := map[string]any{"id": 1, "name": "orders"}
		if period != nil {
			table["series"] = map[string]any{"id": waitSeriesID, "name": "Monthly bordereau"}
			table["period"] = period
		}
		testutil.RespondJSON(w, http.StatusOK, table)
	})
}

func tablePeriod(locked bool) map[string]any {
	return map[string]any{"period_key": "2026-09", "period_start": "2026-09-01", "period_end": "2026-09-30", "is_locked": locked}
}

// serveAlerts answers the alert listing with one alert per key of
// migrationByAlert and each alert's detail with that migration_model_id.
func serveAlerts(mock *testutil.MockAPIServer, migrationByAlert map[int]int) {
	mock.RegisterHandler("GET", waitAlertsPath, func(w http.ResponseWriter, _ *http.Request) {
		results := []map[string]any{}
		for id := range migrationByAlert {
			results = append(results, map[string]any{"id": id, "dataset_id": 1, "issue_type": "needs_review_migration"})
		}
		testutil.RespondJSON(w, http.StatusOK, map[string]any{"results": results, "total_rows": len(results)})
	})
	for id, migration := range migrationByAlert {
		mock.RegisterHandler("GET", waitAlertsPath+"/"+strconv.Itoa(id), func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, map[string]any{
				"id": id, "dataset_id": 1, "issue_type": "needs_review_migration", "migration_model_id": migration,
			})
		})
	}
}

func requireExit(t *testing.T, err error, want int) {
	t.Helper()
	if got := qerrors.GetExitCode(err); got != want {
		t.Fatalf("exit code = %d, want %d (err: %v)", got, want, err)
	}
}

const waitRuleLabel = "email_check (" + testTableRuleID + ")"

// pwTableRuleBanner is the line a table rule's --wait prints to stderr before
// its first poll, for a wait of budget.
func pwTableRuleBanner(budget string) string {
	return "waiting for table rule email_check to finish validation, for up to " + budget +
		"; check with: qctl get table-rules 1 --columns instance_name,state,severity\n"
}

// A passing wait prints each change once on stderr -- two identical polls in a
// row print nothing new -- and the outcome on stdout, after the 201 block.
func TestApplyTableRuleWait_PassedPrintsTheOutcomeOnStdoutAndChangesOnStderr(t *testing.T) {
	s := serveTableRuleWait(t, pendingDetail(api.ValidationEvidenceKindQueued, 0),
		pendingDetail(api.ValidationEvidenceKindQueued, 0),
		pendingDetail(api.ValidationEvidenceKindQueued, 0),
		pendingDetail(api.ValidationEvidenceKindInFlight, 77),
		pendingDetail(api.ValidationEvidenceKindInFlight, 77),
		waitDetail(api.DatasetRuleStateEnabled, api.ValidationEvidenceKindPassed, 77),
	)
	stdout, stderr, err := s.run(t, "--wait")
	requireExit(t, err, qerrors.ExitSuccess)

	wantErr := pwTableRuleBanner("15m0s") +
		"table rule email_check: pending_validation (validation: queued)\n" +
		"table rule email_check: pending_validation (validation: in_flight, dry run 77)\n"
	if stderr != wantErr {
		t.Errorf("stderr:\n--- got ---\n%s--- want ---\n%s", stderr, wantErr)
	}
	created := strings.Index(stdout, "Table rule created: "+waitRuleLabel)
	outcome := strings.Index(stdout, "Validation passed: "+waitRuleLabel+" is enabled (validation: passed, dry run 77)\n")
	if created < 0 || outcome < created {
		t.Errorf("stdout lacks the 201 block followed by the outcome:\n%s", stdout)
	}
	if strings.Contains(stdout, "table rule email_check: pending_validation") {
		t.Errorf("progress leaked onto stdout:\n%s", stdout)
	}
	if n := countRequests(s.mock, "/dry-runs/77"); n != 0 {
		t.Errorf("dry run read %d times, want 0: in_flight does not need it", n)
	}
}

// Without --wait a 201 in pending_validation is reported and left: no poll,
// nothing on stderr.
func TestApplyTableRuleWithoutWaitDoesNotPoll(t *testing.T) {
	s := serveTableRuleWait(t, pendingDetail(api.ValidationEvidenceKindQueued, 0),
		waitDetail(api.DatasetRuleStateEnabled, api.ValidationEvidenceKindPassed, 77))
	_, stderr, err := s.run(t)
	requireExit(t, err, qerrors.ExitSuccess)
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
	if n := countRequests(s.mock, waitRulePath); n != 0 {
		t.Errorf("rule polled %d times without --wait", n)
	}
}

func TestApplyTableRuleWait_FailedExitsOneWithTheReason(t *testing.T) {
	s := serveTableRuleWait(t, pendingDetail(api.ValidationEvidenceKindQueued, 0),
		pendingDetail(api.ValidationEvidenceKindInFlight, 77),
		waitDetail(api.DatasetRuleStateValidationFailed, api.ValidationEvidenceKindFailed, 77),
	)
	serveNotifications(s.mock,
		[2]string{"11111111-2222-3333-4444-555555555555", "another rule's failure"},
		[2]string{testTableRuleID, "Validation dry run did not evaluate any eligible rows."},
	)
	serveDryRun(s.mock, dryRunJob("finished", map[string]any{
		"rule_load_errors": []map[string]any{{"position": 0, "error_type": "ImportError", "message": "No module named 'pandas'", "rule_revision_id": testRuleRevID}},
		"summary":          "**0** eligible rows",
		"metrics": map[string]any{"mode": "single", "exceptions": map[string]any{"count": 2, "top_messages": []map[string]any{
			{"type": "KeyError", "msg": "'amount'", "count": 2, "rule_indices": []int{0}, "sample_signatures": []string{}},
		}}},
	}))

	stdout, _, err := s.run(t, "--wait")
	requireExit(t, err, qerrors.ExitGenericError)
	if !errors.Is(err, dataset_rules.ErrValidationFailed) {
		t.Fatalf("err = %v, want ErrValidationFailed", err)
	}
	for _, want := range []string{
		"reason: Validation dry run did not evaluate any eligible rows.",
		"position 0 (ImportError): No module named 'pandas'",
		"summary:\n  0 eligible rows",
		"KeyError: 'amount' (2 times)",
		"Next: qctl describe dry-run-job 77",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q:\n%s", want, err.Error())
		}
	}
	if strings.Contains(err.Error(), "another rule's failure") {
		t.Errorf("error quotes another rule's notification:\n%s", err.Error())
	}
	if !strings.Contains(stdout, "Validation failed: "+waitRuleLabel+" is validation_failed") {
		t.Errorf("stdout lacks the final state:\n%s", stdout)
	}
}

// A run killed while the rule is pending ends the wait at once, saying the
// backend can still re-run the validation. No notification is consulted,
// because a kill writes none.
func TestApplyTableRuleWait_KilledRunIsStopped(t *testing.T) {
	s := serveTableRuleWait(t, pendingDetail(api.ValidationEvidenceKindQueued, 0),
		pendingDetail(api.ValidationEvidenceKindInFlight, 77),
		pendingDetail(api.ValidationEvidenceKindFailed, 77),
	)
	serveDryRun(s.mock, dryRunJob("killed", map[string]any{"error_message": "Killed by user"}))

	stdout, _, err := s.run(t, "--wait")
	requireExit(t, err, qerrors.ExitGenericError)
	if !errors.Is(err, dataset_rules.ErrValidationStopped) {
		t.Fatalf("err = %v, want ErrValidationStopped", err)
	}
	if !strings.Contains(err.Error(), "error message: Killed by user") {
		t.Errorf("error lacks the job's error_message:\n%s", err.Error())
	}
	if !strings.Contains(err.Error(), "only the wait ended: the backend can re-run the validation about 5 minutes after the run, at most twice, before it sets the rule validation_failed") {
		t.Errorf("error lacks the automatic re-run:\n%s", err.Error())
	}
	if !strings.Contains(stdout, "Validation stopped: "+waitRuleLabel+" is pending_validation (validation: failed, dry run 77 killed)") {
		t.Errorf("stdout lacks the final state:\n%s", stdout)
	}
	if n := countRequests(s.mock, "/notifications"); n != 0 {
		t.Errorf("inbox read %d times for a stopped run", n)
	}
}

func TestApplyTableRuleWait_InconclusiveSummarySettles(t *testing.T) {
	restarts := map[string]string{
		"error":   "the backend can re-run the validation about 5 minutes after the run, at most twice, before it sets the rule validation_failed",
		"blocked": "the stuck-job healer and the automatic re-run both skip a blocked run that wrote its summary",
	}
	for state, restart := range restarts {
		t.Run(state, func(t *testing.T) {
			s := serveTableRuleWait(t, pendingDetail(api.ValidationEvidenceKindQueued, 0),
				pendingDetail(api.ValidationEvidenceKindFailed, 77))
			serveDryRun(s.mock, dryRunJob(state, map[string]any{"summary": "**No verdict:** run could not evaluate rows"}))

			stdout, stderr, err := s.run(t, "--wait")
			requireExit(t, err, qerrors.ExitGenericError)
			if !errors.Is(err, dataset_rules.ErrValidationInconclusive) {
				t.Fatalf("err = %v, want ErrValidationInconclusive", err)
			}
			for _, want := range []string{"summary:", "run could not evaluate rows", "so validation is inconclusive; " + restart, "Next: qctl describe dry-run-job 77"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error lacks %q:\n%s", want, err)
				}
			}
			if !strings.Contains(stdout, "Validation inconclusive: "+waitRuleLabel) {
				t.Errorf("stdout lacks the inconclusive outcome:\n%s", stdout)
			}
			if strings.Contains(stderr, "may restart") {
				t.Errorf("stderr claims a restart:\n%s", stderr)
			}
			if n := countRequests(s.mock, waitRulePath); n != 1 {
				t.Errorf("rule polls = %d, want 1", n)
			}
			if n := countRequests(s.mock, "/notifications"); n != 0 {
				t.Errorf("inbox reads = %d, want 0 for an inconclusive run", n)
			}
		})
	}
}

// A finished run that decided nothing, or one the healer may restart, keeps
// the wait going.
func TestApplyTableRuleWait_InconclusiveRunKeepsWaiting(t *testing.T) {
	const rerunNote = "the backend can re-run the validation about 5 minutes after the run, at most twice, before it sets the rule validation_failed"
	notes := map[string]string{
		"finished":            "the run finished without a verdict on the rule; " + rerunNote,
		"error":               "the backend's stuck-job healer may restart the run; " + rerunNote,
		"blocked":             "the backend's stuck-job healer may restart the run",
		"version_id_mismatch": "the backend's stuck-job healer may restart the run",
	}
	for state, note := range notes {
		t.Run(state, func(t *testing.T) {
			s := serveTableRuleWait(t, pendingDetail(api.ValidationEvidenceKindQueued, 0),
				pendingDetail(api.ValidationEvidenceKindFailed, 77),
				pendingDetail(api.ValidationEvidenceKindFailed, 77),
				waitDetail(api.DatasetRuleStateEnabled, api.ValidationEvidenceKindPassed, 78),
			)
			serveDryRun(s.mock, dryRunJob(state, nil))
			_, stderr, err := s.run(t, "--wait")
			requireExit(t, err, qerrors.ExitSuccess)
			if want := pwTableRuleBanner("15m0s") + "table rule email_check: pending_validation (validation: failed, dry run 77 " + state + "); " + note + "\n"; stderr != want {
				t.Errorf("stderr:\n--- got ---\n%s--- want ---\n%s", stderr, want)
			}
		})
	}
}

func TestApplyTableRuleWait_ValidationNoneMidWaitExitsOne(t *testing.T) {
	s := serveTableRuleWait(t, pendingDetail(api.ValidationEvidenceKindQueued, 0),
		pendingDetail(api.ValidationEvidenceKindNone, 0))
	serveNotifications(s.mock, [2]string{testTableRuleID, "Validation of rule \"email_check\" could not start: this table has no active data source."})

	stdout, _, err := s.run(t, "--wait")
	requireExit(t, err, qerrors.ExitGenericError)
	if !errors.Is(err, dataset_rules.ErrValidationNotQueued) {
		t.Fatalf("err = %v, want ErrValidationNotQueued", err)
	}
	if !strings.Contains(err.Error(), "could not start: this table has no active data source") {
		t.Errorf("error lacks the notification:\n%s", err.Error())
	}
	if !strings.Contains(stdout, "Validation not queued: "+waitRuleLabel) {
		t.Errorf("stdout lacks the final state:\n%s", stdout)
	}
}

func TestApplyTableRuleWait_UnexpectedStateExitsOne(t *testing.T) {
	for _, state := range []api.DatasetRuleState{api.DatasetRuleStateDisabled, api.DatasetRuleStateDeleted, api.DatasetRuleStateError} {
		t.Run(string(state), func(t *testing.T) {
			s := serveTableRuleWait(t, pendingDetail(api.ValidationEvidenceKindQueued, 0),
				waitDetail(state, api.ValidationEvidenceKindQueued, 0))
			stdout, _, err := s.run(t, "--wait")
			requireExit(t, err, qerrors.ExitGenericError)
			if !errors.Is(err, dataset_rules.ErrValidationUnexpected) {
				t.Fatalf("err = %v, want ErrValidationUnexpected", err)
			}
			if !strings.Contains(stdout, "Validation ended: "+waitRuleLabel+" is "+string(state)) {
				t.Errorf("stdout lacks the final state:\n%s", stdout)
			}
		})
	}
}

func TestApplyTableRuleWait_TimeoutNamesTheStateAndExits124(t *testing.T) {
	s := serveTableRuleWait(t, pendingDetail(api.ValidationEvidenceKindQueued, 0),
		pendingDetail(api.ValidationEvidenceKindQueued, 0))
	stdout, stderr, err := s.run(t, "--wait", "--wait-timeout", "10")
	requireExit(t, err, qerrors.ExitTimeout)
	if !errors.Is(err, dataset_rules.ErrValidationWaitTimeout) {
		t.Fatalf("err = %v, want ErrValidationWaitTimeout", err)
	}
	if !strings.Contains(err.Error(), "after 10s: table rule "+waitRuleLabel+" is still pending_validation (validation: queued)") {
		t.Errorf("error does not name the state:\n%s", err.Error())
	}
	if strings.Contains(stdout, "Validation ") {
		t.Errorf("a timeout printed an outcome on stdout:\n%s", stdout)
	}
	if want := pwTableRuleBanner("10s") + "table rule email_check: pending_validation (validation: queued)\n"; stderr != want {
		t.Errorf("stderr = %q, want the banner and one progress line", stderr)
	}
}

// Under -o json stdout is the rule as the wait last read it, and everything
// else -- the 201 block, the banner, the progress and the outcome -- goes to
// stderr in that order.
func TestApplyTableRuleWait_JSONStdoutIsTheRuleAsLastRead(t *testing.T) {
	s := serveTableRuleWait(t, pendingDetail(api.ValidationEvidenceKindQueued, 0),
		pendingDetail(api.ValidationEvidenceKindQueued, 0),
		waitDetail(api.DatasetRuleStateEnabled, api.ValidationEvidenceKindPassed, 77))
	stdout, stderr, err := s.run(t, "--wait", "-o", "json")
	requireExit(t, err, qerrors.ExitSuccess)
	var rule map[string]any
	if err := json.Unmarshal([]byte(stdout), &rule); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
	}
	if rule["state"] != "enabled" || rule["validation_dry_run_job_id"] != float64(77) {
		t.Errorf("stdout is not the rule as last read:\n%s", stdout)
	}
	created := strings.Index(stderr, "Table rule created: "+waitRuleLabel)
	banner := strings.Index(stderr, pwTableRuleBanner("15m0s")+"table rule email_check: pending_validation (validation: queued)\n")
	outcome := strings.Index(stderr, "Validation passed: ")
	if created < 0 || banner < created || outcome < banner {
		t.Errorf("stderr is not the 201 block, banner and progress, then the outcome:\n%s", stderr)
	}
}

// pendingMigration is a validated rule with produced columns in draft migration 12.
func pendingMigration() dataset_rules.DatasetRuleDetail {
	d := pendingDetail(api.ValidationEvidenceKindPassed, 77)
	migration := 12
	d.PendingMigrationModelID = &migration
	return d
}

func draftRule(validation api.ValidationEvidenceKind, jobID int) dataset_rules.DatasetRuleDetail {
	d := pendingDetail(validation, jobID)
	migration := 12
	d.PendingMigrationModelID = &migration
	d.ValidationBlocksEnable = validation != api.ValidationEvidenceKindPassed
	return d
}

func TestApplyTableRuleWait_DraftExistsBeforeValidationPasses(t *testing.T) {
	created := draftRule(api.ValidationEvidenceKindQueued, 0)
	s := serveTableRuleWait(t, created, created,
		draftRule(api.ValidationEvidenceKindInFlight, 77), pendingMigration())

	stdout, stderr, err := s.run(t, "--wait")
	requireExit(t, err, qerrors.ExitSuccess)
	if n := countRequests(s.mock, waitRulePath); n != 3 {
		t.Errorf("rule polls = %d, want 3 before reporting passed", n)
	}
	for _, want := range []string{"validation: queued", "validation: in_flight, dry run 77"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("progress lacks %q:\n%s", want, stderr)
		}
	}
	if !strings.Contains(stdout, "Validation passed: "+waitRuleLabel+" stays pending_validation until draft migration 12 is applied") {
		t.Errorf("stdout lacks the validated migration outcome:\n%s", stdout)
	}
}

func TestApplyTableRuleWait_DraftDoesNotProveValidationPassed(t *testing.T) {
	for _, tc := range []struct {
		name       string
		validation api.ValidationEvidenceKind
		job        string
		summary    string
		blocked    bool
		wantExit   int
		wantError  error
	}{
		{"queued", api.ValidationEvidenceKindQueued, "", "", true, 124, dataset_rules.ErrValidationWaitTimeout},
		{"queued with recorded pass", api.ValidationEvidenceKindQueued, "", "", false, 124, dataset_rules.ErrValidationWaitTimeout},
		{"in flight", api.ValidationEvidenceKindInFlight, "", "", true, 124, dataset_rules.ErrValidationWaitTimeout},
		{"stale", api.ValidationEvidenceKindStale, "", "", true, 124, dataset_rules.ErrValidationWaitTimeout},
		{"absent evidence", "", "", "", false, 124, dataset_rules.ErrValidationWaitTimeout},
		{"passed but blocked", api.ValidationEvidenceKindPassed, "", "", true, 124, dataset_rules.ErrValidationWaitTimeout},
		{"none", api.ValidationEvidenceKindNone, "", "", true, 1, dataset_rules.ErrValidationNotQueued},
		{"killed", api.ValidationEvidenceKindFailed, "killed", "", true, 1, dataset_rules.ErrValidationStopped},
		{"stopped", api.ValidationEvidenceKindFailed, "stopped", "", true, 1, dataset_rules.ErrValidationStopped},
		{"error with summary", api.ValidationEvidenceKindFailed, "error", "No verdict", true, 1, dataset_rules.ErrValidationInconclusive},
		{"blocked with summary", api.ValidationEvidenceKindFailed, "blocked", "No verdict", true, 1, dataset_rules.ErrValidationInconclusive},
		{"healable error", api.ValidationEvidenceKindFailed, "error", "", true, 124, dataset_rules.ErrValidationWaitTimeout},
		{"finished without verdict", api.ValidationEvidenceKindFailed, "finished", "", true, 124, dataset_rules.ErrValidationWaitTimeout},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rule := draftRule(tc.validation, 77)
			rule.ValidationBlocksEnable = tc.blocked
			s := serveTableRuleWait(t, draftRule(api.ValidationEvidenceKindQueued, 0), rule)
			if tc.job != "" {
				serveDryRun(s.mock, dryRunJob(tc.job, map[string]any{"summary": tc.summary}))
			}
			stdout, stderr, err := s.run(t, "--wait", "--wait-timeout", "10")
			requireExit(t, err, tc.wantExit)
			if !errors.Is(err, tc.wantError) {
				t.Errorf("err = %v, want %v", err, tc.wantError)
			}
			if strings.Contains(stdout+stderr, "Validation passed:") {
				t.Errorf("reported passing validation:\n%s\n%s", stdout, stderr)
			}
			if n := countRequests(s.mock, waitAlertsPath); n != 0 {
				t.Errorf("migration alert reads = %d, want 0 before validation passes", n)
			}
			if tc.job != "" && countRequests(s.mock, waitDryRunPath) == 0 {
				t.Error("failed validation dry run was not read")
			}
			if (tc.job == "finished" || tc.job == "killed" || tc.job == "stopped" || tc.name == "error with summary") &&
				!strings.Contains(err.Error(), "its draft migration prevents automatic re-queueing") {
				t.Errorf("error lacks the draft's re-queue restriction: %v", err)
			}
		})
	}
}

// The alert is matched on migration_model_id, which only the alert's detail
// carries: alert 400 belongs to another draft.
func TestApplyTableRuleWait_PendingMigrationNamesTheMatchingAlert(t *testing.T) {
	cases := []struct {
		name     string
		terminal bool
		wantLine string
	}{
		{"terminal", true, "qctl alert resolve 401 --action apply_migration   # apply the draft; that enables the rule"},
		{"no terminal", false, "qctl alert resolve 401 --action apply_migration --yes   # apply the draft; that enables the rule"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.terminal {
				cmdutil.SetReader(strings.NewReader(""))
				t.Cleanup(cmdutil.ResetReader)
			} else if term.IsTerminal(int(os.Stdin.Fd())) {
				t.Skip("stdin is a terminal; CanPrompt() cannot be made false here")
			}
			s := serveTableRuleWait(t, pendingDetail(api.ValidationEvidenceKindQueued, 0), pendingMigration())
			serveAlerts(s.mock, map[int]int{400: 11, 401: 12})

			stdout, _, err := s.run(t, "--wait")
			requireExit(t, err, qerrors.ExitSuccess)
			for _, want := range []string{
				"Validation passed: " + waitRuleLabel + " stays pending_validation until draft migration 12 is applied\n",
				"  alert       : 401 (needs_review_migration for draft migration 12)\n",
				"  note        : apply_migration applies the table's WHOLE draft migration, not only this rule's columns\n",
				tc.wantLine,
			} {
				if !strings.Contains(stdout, want) {
					t.Errorf("stdout lacks %q:\n%s", want, stdout)
				}
			}
			if strings.Contains(stdout, "alert resolve 400") {
				t.Errorf("stdout names the other draft's alert:\n%s", stdout)
			}
			if n := countRequests(s.mock, waitTablePath); n != 1 {
				t.Errorf("table reads = %d, want only the validation poll's read when an alert matches", n)
			}
		})
	}
}

func TestApplyTableRuleWait_PendingMigrationFallsBackToTheAlertListing(t *testing.T) {
	for _, state := range []string{"no series", "unlocked", "read failed"} {
		t.Run(state, func(t *testing.T) {
			s := serveTableRuleWait(t, pendingDetail(api.ValidationEvidenceKindQueued, 0), pendingMigration())
			serveAlerts(s.mock, map[int]int{400: 11})
			switch state {
			case "unlocked":
				serveTable(s.mock, tablePeriod(false))
			case "read failed":
				reads := 0
				s.mock.RegisterHandler("GET", waitTablePath, func(w http.ResponseWriter, _ *http.Request) {
					reads++
					if reads > 1 {
						testutil.RespondJSON(w, http.StatusForbidden, map[string]any{"detail": "permission denied"})
						return
					}
					testutil.RespondJSON(w, http.StatusOK, map[string]any{"id": 1, "name": "orders"})
				})
			}

			stdout, _, err := s.run(t, "--wait")
			requireExit(t, err, qerrors.ExitSuccess)
			for _, want := range []string{
				"  alert       : no unresolved needs_review_migration alert on table 1 names draft migration 12\n",
				"qctl get alerts --table-id 1 --issue-type needs_review_migration   # find the review alert",
			} {
				if !strings.Contains(stdout, want) {
					t.Errorf("stdout lacks %q:\n%s", want, stdout)
				}
			}
			if strings.Contains(stdout, "alert resolve") || strings.Contains(stdout, "table-series unlock") {
				t.Errorf("stdout suggests resolving an unmatched alert or unlocking an unknown lock:\n%s", stdout)
			}
			if n := countRequests(s.mock, waitTablePath); n != 2 {
				t.Errorf("table reads = %d, want the validation poll and the missing-alert check", n)
			}
		})
	}
}

func TestApplyTableRuleWait_PeriodLocksBeforeMigrationAlertLookup(t *testing.T) {
	for _, canPrompt := range []bool{true, false} {
		for _, format := range []string{"table", "json"} {
			t.Run(strconv.FormatBool(canPrompt)+"/"+format, func(t *testing.T) {
				if canPrompt {
					cmdutil.SetReader(strings.NewReader(""))
					t.Cleanup(cmdutil.ResetReader)
				} else if term.IsTerminal(int(os.Stdin.Fd())) {
					t.Skip("stdin is a terminal; CanPrompt() cannot be made false here")
				}
				s := serveTableRuleWait(t, pendingDetail(api.ValidationEvidenceKindQueued, 0), pendingMigration())
				reads := 0
				s.mock.RegisterHandler("GET", waitTablePath, func(w http.ResponseWriter, _ *http.Request) {
					reads++
					testutil.RespondJSON(w, http.StatusOK, map[string]any{
						"id": 1, "name": "orders",
						"series": map[string]any{"id": waitSeriesID, "name": "Monthly bordereau"},
						"period": tablePeriod(reads > 1),
					})
				})

				stdout, stderr, err := s.run(t, "--wait", "-o", format)
				requireExit(t, err, qerrors.ExitSuccess)
				human := stdout
				if format == "json" {
					var rule dataset_rules.DatasetRuleDetail
					if err := json.Unmarshal([]byte(stdout), &rule); err != nil || rule.PendingMigrationModelID == nil {
						t.Fatalf("stdout is not the validated rule: %v\n%s", err, stdout)
					}
					human = stderr
				}
				unlock := "qctl table-series unlock " + waitSeriesID + " --period 2026-09"
				if !canPrompt {
					unlock += " --yes"
				}
				for _, want := range []string{
					`  alert       : period "2026-09" of series "Monthly bordereau" is locked; unresolved migration alerts are hidden`,
					unlock + "   # unlock the period (requires deliverable:admin)",
					"qctl get alerts --table-id 1 --issue-type needs_review_migration",
					"# find the review alert after unlocking",
					"qctl describe table-schema --table-id 1",
				} {
					if !strings.Contains(human, want) {
						t.Errorf("output lacks %q:\n%s", want, human)
					}
				}
				if strings.Contains(human, "no unresolved needs_review_migration alert") || strings.Contains(human, "qctl alert resolve") {
					t.Errorf("output suggests a missing or resolvable alert on the locked period:\n%s", human)
				}
			})
		}
	}
}

// A 201 that starts no wait keeps exit 0 and says why on stderr, without
// polling the rule.
func TestApplyTableRuleWait_CreatesThatStartNoWait(t *testing.T) {
	cases := []struct {
		name    string
		created dataset_rules.DatasetRuleDetail
		want    string
	}{
		{"already enabled", waitDetail(api.DatasetRuleStateEnabled, api.ValidationEvidenceKindNotNeeded, 0),
			"--wait: nothing to wait for: table rule " + waitRuleLabel + " was created enabled\n"},
		{"no validation queued", pendingDetail(api.ValidationEvidenceKindNone, 0),
			"--wait: nothing to wait for: table rule " + waitRuleLabel + " has no validation dry run queued (validation: none)\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := serveTableRuleWait(t, tc.created, tc.created)
			_, stderr, err := s.run(t, "--wait")
			requireExit(t, err, qerrors.ExitSuccess)
			if stderr != tc.want {
				t.Errorf("stderr = %q, want %q", stderr, tc.want)
			}
			if n := countRequests(s.mock, waitRulePath); n != 0 {
				t.Errorf("rule polled %d times", n)
			}
		})
	}
}

func TestApplyTableRuleWait_StagedCreateStartsNoWait(t *testing.T) {
	s := serveTableRuleWait(t, pendingDetail(api.ValidationEvidenceKindQueued, 0))
	suggestion := sampleSuggestion(api.SuggestionStatePending)
	s.mock.RegisterHandler("POST", waitInstantiate, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusAccepted, suggestion)
	})
	_, stderr, err := s.run(t, "--wait")
	requireExit(t, err, qerrors.ExitSuccess)
	want := "--wait: nothing to wait for: the instantiation was staged for review as suggestion " + suggestion.ID.String() + ", so no rule exists yet\n"
	if stderr != want {
		t.Errorf("stderr = %q, want %q", stderr, want)
	}
}

func TestApplyTableRuleWait_PatchStartsNoWait(t *testing.T) {
	s := serveTableRuleWait(t, pendingDetail(api.ValidationEvidenceKindQueued, 0))
	live := sampleDatasetRuleDetailForApply()
	registerRuleDetailGet(s.mock, live)
	patched := live
	patched.InstanceName = "renamed"
	s.mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/datasets/1/dataset-rules/"+testTableRuleID, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, patched)
	})
	path := strings.TrimSuffix(s.path, "table-rule.yaml") + "patch.yaml"
	if err := os.WriteFile(path, []byte(tableRulePatchDoc("renamed")), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err := execApplyRoot(t, "apply", "-f", path, "--yes", "--wait")
	requireExit(t, err, qerrors.ExitSuccess)
	want := "--wait: nothing to wait for: " + path + " patches table rule " + testTableRuleID + "; --wait waits only for the validation of a rule a document creates\n"
	if stderr != want {
		t.Errorf("stderr = %q, want %q", stderr, want)
	}
	if !strings.Contains(stdout, "Table rule updated: renamed") {
		t.Errorf("stdout lacks the patch result:\n%s", stdout)
	}
}

// Each created rule's wait finishes before the next document is applied.
func TestApplyTableRuleWait_WaitsRunPerDocumentInOrder(t *testing.T) {
	const secondID = "550e8400-e29b-41d4-a716-446655440001"
	s := serveTableRuleWait(t, pendingDetail(api.ValidationEvidenceKindQueued, 0),
		pendingDetail(api.ValidationEvidenceKindInFlight, 77),
		waitDetail(api.DatasetRuleStateEnabled, api.ValidationEvidenceKindPassed, 77))
	second := pendingDetail(api.ValidationEvidenceKindQueued, 0)
	second.ID = uuid.MustParse(secondID)
	second.InstanceName = "second_check"
	posts := 0
	s.mock.RegisterHandler("POST", waitInstantiate, func(w http.ResponseWriter, _ *http.Request) {
		posts++
		if posts == 1 {
			testutil.RespondJSON(w, http.StatusCreated, pendingDetail(api.ValidationEvidenceKindQueued, 0))
			return
		}
		testutil.RespondJSON(w, http.StatusCreated, second)
	})
	failed := second
	failed.State = api.DatasetRuleStateValidationFailed
	s.mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dataset-rules/"+secondID, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, failed)
	})
	path := strings.TrimSuffix(s.path, "table-rule.yaml") + "two.yaml"
	body := tableRuleInstantiateDoc("email_check") + "---\n" + tableRuleInstantiateDoc("second_check")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, err := execApplyRoot(t, "apply", "-f", path, "--yes", "--wait")
	requireExit(t, err, qerrors.ExitGenericError)
	if !errors.Is(err, dataset_rules.ErrValidationFailed) {
		t.Fatalf("err = %v, want the second rule's ErrValidationFailed", err)
	}

	lastFirstPoll, secondPost, seenPosts := -1, -1, 0
	for i, r := range s.mock.Requests() {
		switch r {
		case "GET " + waitRulePath:
			lastFirstPoll = i
		case "POST " + waitInstantiate:
			if seenPosts++; seenPosts == 2 {
				secondPost = i
			}
		}
	}
	if lastFirstPoll < 0 || secondPost < lastFirstPoll {
		t.Errorf("second create (request %d) did not wait for the first rule's last poll (request %d):\n%s",
			secondPost, lastFirstPoll, strings.Join(s.mock.Requests(), "\n"))
	}
}

func TestApplyTableRuleWait_RefusedBesideValidateOnly(t *testing.T) {
	s := serveTableRuleWait(t, pendingDetail(api.ValidationEvidenceKindQueued, 0))
	_, _, err := s.run(t, "--wait", "--validate-only")
	requireExit(t, err, qerrors.ExitBadUsage)
	if n := len(s.mock.Requests()); n != 0 {
		t.Errorf("%d requests sent, want none", n)
	}
}

func TestTableRuleWaitCancelsBothPollingRequests(t *testing.T) {
	for _, path := range []string{waitRulePath, waitDryRunPath} {
		t.Run(path, func(t *testing.T) {
			created := pendingDetail(api.ValidationEvidenceKindInFlight, 77)
			s := serveTableRuleWait(t, created, pendingDetail(api.ValidationEvidenceKindFailed, 77))
			s.mock.RegisterHandler("GET", path, func(w http.ResponseWriter, r *http.Request) {
				testutil.WaitForRequestCancellation(t, r)
				// A cancelled request may still return a successful response.
				if path == waitRulePath {
					testutil.RespondJSON(w, http.StatusOK, waitDetail(api.DatasetRuleStateEnabled, api.ValidationEvidenceKindPassed, 77))
				} else {
					testutil.RespondJSON(w, http.StatusOK, dryRunJob("finished", nil))
				}
			})
			_, _, err := s.run(t, "--wait", "--wait-timeout", "1")
			requireExit(t, err, qerrors.ExitTimeout)
			if !errors.Is(err, dataset_rules.ErrValidationWaitTimeout) {
				t.Errorf("err = %v, want validation timeout", err)
			}
		})
	}
}

const lockedValidationMsg = "The dry run passed, but this table's period is locked, so the rule was not enabled and no schema change was staged."

// A rule whose validation passed on a locked period settles at once, draft or
// not: it exits 1 naming the period and the unlock, carries the backend's
// notification, and never looks for a migration alert.
func TestApplyTableRuleWait_LockedPeriodHoldsAPassedRule(t *testing.T) {
	for name, poll := range map[string]dataset_rules.DatasetRuleDetail{
		"no draft": pendingDetail(api.ValidationEvidenceKindPassed, 77),
		"draft":    pendingMigration(),
	} {
		t.Run(name, func(t *testing.T) {
			s := serveTableRuleWait(t, pendingDetail(api.ValidationEvidenceKindQueued, 0), poll)
			serveTable(s.mock, tablePeriod(true))
			serveNotifications(s.mock, [2]string{testTableRuleID, lockedValidationMsg})

			stdout, _, err := s.run(t, "--wait", "--wait-timeout", "10")
			requireExit(t, err, qerrors.ExitGenericError)
			if !errors.Is(err, dataset_rules.ErrValidationHeldLocked) {
				t.Fatalf("err = %v, want ErrValidationHeldLocked", err)
			}
			if !strings.Contains(stdout, "Validation held: "+waitRuleLabel+" is pending_validation") ||
				!strings.Contains(stdout, `period "2026-09" of series "Monthly bordereau" is locked`) {
				t.Errorf("stdout lacks the held outcome:\n%s", stdout)
			}
			for _, want := range []string{
				"reason: " + lockedValidationMsg,
				"Next: qctl table-series unlock " + waitSeriesID + " --period 2026-09 (requires deliverable:admin)",
			} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error lacks %q:\n%s", want, err.Error())
				}
			}
			if n := countRequests(s.mock, waitAlertsPath); n != 0 {
				t.Errorf("migration alert reads = %d, want 0 on a locked period", n)
			}
		})
	}
}

// On an unlocked period a passed rule with no draft is still settling, so the
// wait keeps polling to its budget.
func TestApplyTableRuleWait_UnlockedPeriodKeepsWaitingForThePassedRule(t *testing.T) {
	s := serveTableRuleWait(t, pendingDetail(api.ValidationEvidenceKindQueued, 0), pendingDetail(api.ValidationEvidenceKindPassed, 77))
	serveTable(s.mock, tablePeriod(false))

	_, _, err := s.run(t, "--wait", "--wait-timeout", "10")
	requireExit(t, err, 124)
	if countRequests(s.mock, waitTablePath) == 0 {
		t.Error("the table's period was not read")
	}
}
