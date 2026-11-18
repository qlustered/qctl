package create

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/composite_dry_runs"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const (
	cdrLatestRevision = "5d0c2a4e-8b1f-4e3a-a6c9-7f2d1b0e4c33"
	cdrOlderRevision  = "7a1e9c02-3b4d-4f5e-8a6b-9c0d1e2f3a4b"
	cdrGroup          = "0b6f4a36-2f7e-4c61-9d7a-3c1f1e2a9b10"
	cdrOtherGroup     = "e3f1a2b4-c5d6-4e7f-8091-a2b3c4d5e6f7"
	cdrLaunchBody     = `{"composite_dry_run_job_id":5512,"ingestion_job_id":9921}`
)

// cdrLatestPath and cdrLaunchPath are the two routes every launch reaches.
var (
	cdrLatestPath = "/api/orgs/" + testOrgID + "/datasets/12/latest-composite-spec-revision"
	cdrLaunchPath = func(rev string) string {
		return "/api/orgs/" + testOrgID + "/composite-spec-revisions/" + rev + "/dry-runs"
	}
)

// setupCompositeLaunchEnv serves table 12's latest revision (v3, draft).
// The caller registers the launch handler.
func setupCompositeLaunchEnv(t *testing.T) *testutil.MockAPIServer {
	t.Helper()
	env, mock := setupLaunchEnv(t)
	t.Cleanup(env.Cleanup)
	t.Cleanup(mock.Close)
	mock.RegisterHandler("GET", cdrLatestPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"composite_spec_revision_id": cdrLatestRevision,
			"composite_spec_group_id":    cdrGroup,
			"state":                      "draft",
			"release":                    "v3",
			"description":                nil,
			"spec_sha256":                "ab12",
			"created_at":                 "2026-09-22T10:00:00Z",
			"updated_at":                 "2026-09-22T10:00:00Z",
		})
	})
	return mock
}

// registerCompositeLaunch answers every launch of rev with cdrLaunchBody and
// records each request body.
func registerCompositeLaunch(t *testing.T, mock *testutil.MockAPIServer, rev string) *[]map[string]interface{} {
	t.Helper()
	var bodies []map[string]interface{}
	mock.RegisterHandler("PUT", cdrLaunchPath(rev), func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode launch body: %v", err)
		}
		bodies = append(bodies, body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(cdrLaunchBody))
	})
	return &bodies
}

// registerRevisionDetail serves GET /composite-spec-revisions/{rev} for a
// revision of group.
func registerRevisionDetail(mock *testutil.MockAPIServer, rev, group, release, state string) {
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/composite-spec-revisions/"+rev, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"composite_spec_revision_id": rev,
			"composite_spec_group_id":    group,
			"state":                      state,
			"spec_sha256":                "cd34",
			"created_at":                 "2026-09-20T10:00:00Z",
			"created_by_user_id":         nil,
			"is_rerunnable":              true,
			"revision": map[string]interface{}{
				"release": release, "description": nil,
				"sources": map[string]interface{}{"orders": map[string]interface{}{"dataset_id": 11}},
				"output":  map[string]interface{}{"order_id": map[string]interface{}{"source": "orders", "field": "order_id"}},
			},
			"group": map[string]interface{}{
				"name": "orders_enriched", "slug": "orders-enriched", "spine_dataset_id": 11,
				"natural_key_columns": []string{"order_id"}, "natural_key_normalization": map[string]interface{}{},
			},
		})
	})
}

// The default launch: the latest revision, nothing but its id in the body, so
// the server applies its own sampling defaults.
func TestCreateCompositeDryRunJob_LaunchesTheLatestRevision(t *testing.T) {
	mock := setupCompositeLaunchEnv(t)
	bodies := registerCompositeLaunch(t, mock, cdrLatestRevision)

	stdout, stderr, err := execCreate(t, "create", "composite-dry-run-job", "12", "-o", "table")
	if err != nil {
		t.Fatalf("Execute() error = %v\nstderr:\n%s", err, stderr)
	}

	want := `composite-dry-run-job/5512 created
  revision : v3 (draft)
Next: qctl describe composite-dry-run-job 5512
`
	if stdout != want {
		t.Errorf("stdout mismatch:\n--- got ---\n%s--- want ---\n%s", stdout, want)
	}
	if len(*bodies) != 1 {
		t.Fatalf("expected 1 launch, got %d", len(*bodies))
	}
	body := (*bodies)[0]
	if body["composite_spec_revision_id"] != cdrLatestRevision {
		t.Errorf("composite_spec_revision_id = %v, want the path's %s", body["composite_spec_revision_id"], cdrLatestRevision)
	}
	for _, key := range []string{"max_spine_rows", "pinned_spine_row_ids"} {
		if _, sent := body[key]; sent {
			t.Errorf("%s was sent although its flag was not: %v", key, body)
		}
	}
	if seed, sent := body["seed"]; sent && seed != nil {
		t.Errorf("seed = %v, want null when --seed is unset", seed)
	}
}

// Every sampling flag reaches the body, and --seed 0 is a seed, not "unset".
func TestCreateCompositeDryRunJob_SendsTheSamplingFlags(t *testing.T) {
	mock := setupCompositeLaunchEnv(t)
	bodies := registerCompositeLaunch(t, mock, cdrLatestRevision)

	_, stderr, err := execCreate(t, "create", "composite-dry-run-job", "--table-id", "12", "-o", "table",
		"--max-spine-rows", "200", "--seed", "0", "--pin-spine-row", "1024", "--pin-spine-row", "2048")
	if err != nil {
		t.Fatalf("Execute() error = %v\nstderr:\n%s", err, stderr)
	}
	if len(*bodies) != 1 {
		t.Fatalf("expected 1 launch, got %d", len(*bodies))
	}
	body := (*bodies)[0]
	if body["max_spine_rows"] != float64(200) {
		t.Errorf("max_spine_rows = %v, want 200", body["max_spine_rows"])
	}
	if seed, sent := body["seed"]; !sent || seed != float64(0) {
		t.Errorf("seed = %v (sent: %v), want 0", seed, sent)
	}
	pins, _ := json.Marshal(body["pinned_spine_row_ids"])
	if string(pins) != "[1024,2048]" {
		t.Errorf("pinned_spine_row_ids = %s, want [1024,2048]", pins)
	}
}

// A table named by --table resolves through the name lookup.
func TestCreateCompositeDryRunJob_ResolvesTheTableByName(t *testing.T) {
	mock := setupCompositeLaunchEnv(t)
	bodies := registerCompositeLaunch(t, mock, cdrLatestRevision)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(
			`{"total_rows":1,"page":1,"results":[{"id":12,"name":"orders_enriched","state":"active"}]}`))
	})

	_, stderr, err := execCreate(t, "create", "composite-dry-run-job", "--table", "orders_enriched", "-o", "table")
	if err != nil {
		t.Fatalf("Execute() error = %v\nstderr:\n%s", err, stderr)
	}
	if len(*bodies) != 1 {
		t.Fatalf("expected 1 launch, got %d", len(*bodies))
	}
}

// --revision of the same linked table is launched in place of the latest,
// and the output names it.
func TestCreateCompositeDryRunJob_LaunchesTheNamedRevision(t *testing.T) {
	mock := setupCompositeLaunchEnv(t)
	registerRevisionDetail(mock, cdrOlderRevision, cdrGroup, "v2", "enabled")
	older := registerCompositeLaunch(t, mock, cdrOlderRevision)
	latest := registerCompositeLaunch(t, mock, cdrLatestRevision)

	stdout, stderr, err := execCreate(t, "create", "composite-dry-run-job", "12", "--revision", cdrOlderRevision, "-o", "table")
	if err != nil {
		t.Fatalf("Execute() error = %v\nstderr:\n%s", err, stderr)
	}
	if len(*older) != 1 || len(*latest) != 0 {
		t.Fatalf("launches: named revision %d, latest %d; want 1 and 0", len(*older), len(*latest))
	}
	if !strings.Contains(stdout, "  revision : v2 (enabled)\n") {
		t.Errorf("the output should name the launched revision, got:\n%s", stdout)
	}
}

// A --revision of another linked table is refused before anything launches:
// the dry run would preview a different table than the one named.
func TestCreateCompositeDryRunJob_RefusesARevisionOfAnotherTable(t *testing.T) {
	mock := setupCompositeLaunchEnv(t)
	registerRevisionDetail(mock, cdrOlderRevision, cdrOtherGroup, "v1", "enabled")
	bodies := registerCompositeLaunch(t, mock, cdrOlderRevision)

	_, _, err := execCreate(t, "create", "composite-dry-run-job", "12", "--revision", cdrOlderRevision, "-o", "table")
	if err == nil {
		t.Fatal("expected a revision of another group to be refused")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d; err = %v", code, qerrors.ExitBadUsage, err)
	}
	if len(*bodies) != 0 {
		t.Errorf("launched %d dry run(s) of another table's revision", len(*bodies))
	}
}

// A table with no composite spec revision is a 404 from the latest-revision
// route, and nothing launches.
func TestCreateCompositeDryRunJob_TableWithoutARevisionIsNotFound(t *testing.T) {
	env, mock := setupLaunchEnv(t)
	t.Cleanup(env.Cleanup)
	t.Cleanup(mock.Close)
	mock.RegisterHandler("GET", cdrLatestPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusNotFound, map[string]interface{}{
			"detail": "latest composite spec revision for dataset 12 can not be found.",
		})
	})

	_, _, err := execCreate(t, "create", "composite-dry-run-job", "12", "-o", "table")
	if err == nil {
		t.Fatal("expected an error for a table with no spec revision")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
		t.Errorf("exit code = %d, want %d; err = %v", code, qerrors.ExitNotFound, err)
	}
	for _, req := range mock.Requests() {
		if strings.HasPrefix(req, "PUT ") {
			t.Errorf("launched although the table has no revision: %v", mock.Requests())
		}
	}
}

// Malformed flags exit 2 before any request.
func TestCreateCompositeDryRunJob_RejectsBadFlagsBeforeAnyRequest(t *testing.T) {
	cases := map[string][]string{
		"no table":                {},
		"table twice":             {"12", "--table-id", "12"},
		"zero max-spine-rows":     {"12", "--max-spine-rows", "0"},
		"negative max-spine-rows": {"12", "--max-spine-rows", "-5"},
		"malformed revision":      {"12", "--revision", "5d0c2a4e"},
		"empty revision":          {"12", "--revision", ""},
		"zero pinned row":         {"12", "--pin-spine-row", "0"},
		"negative pinned row":     {"12", "--pin-spine-row", "7", "--pin-spine-row", "-1"},
		"wait-timeout alone":      {"12", "--wait-timeout", "60"},
		"zero wait-timeout":       {"12", "--wait", "--wait-timeout", "0"},
	}
	for name, flags := range cases {
		t.Run(name, func(t *testing.T) {
			mock := setupCompositeLaunchEnv(t)
			registerCompositeLaunch(t, mock, cdrLatestRevision)

			args := append([]string{"create", "composite-dry-run-job", "-o", "table"}, flags...)
			_, _, err := execCreate(t, args...)
			if err == nil {
				t.Fatal("expected an error")
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d; err = %v", code, qerrors.ExitBadUsage, err)
			}
			if reqs := mock.Requests(); len(reqs) != 0 {
				t.Errorf("expected no request, got %v", reqs)
			}
		})
	}
}

// -o json and -o yaml are the server's answer; -o name is the resource name.
func TestCreateCompositeDryRunJob_OutputFormats(t *testing.T) {
	cases := map[string]string{
		"json": "{\n  \"composite_dry_run_job_id\": 5512,\n  \"ingestion_job_id\": 9921\n}\n",
		"yaml": "composite_dry_run_job_id: 5512\ningestion_job_id: 9921\n",
		"name": "composite-dry-run-job/5512\n",
	}
	for format, want := range cases {
		t.Run(format, func(t *testing.T) {
			mock := setupCompositeLaunchEnv(t)
			registerCompositeLaunch(t, mock, cdrLatestRevision)

			stdout, stderr, err := execCreate(t, "create", "composite-dry-run-job", "12", "-o", format)
			if err != nil {
				t.Fatalf("Execute() error = %v\nstderr:\n%s", err, stderr)
			}
			if stdout != want {
				t.Errorf("stdout mismatch:\n--- got ---\n%s--- want ---\n%s", stdout, want)
			}
		})
	}
}

// A full dry-run budget is waited out: the server's Retry-After is honoured,
// the wait is explained on stderr, and stdout stays the JSON answer.
func TestCreateCompositeDryRunJob_RetriesAThrottledLaunch(t *testing.T) {
	mock := setupCompositeLaunchEnv(t)
	slept := stubThrottleSleep(t)

	calls := 0
	mock.RegisterHandler("PUT", cdrLaunchPath(cdrLatestRevision), func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "5")
			testutil.RespondJSON(w, http.StatusTooManyRequests, map[string]interface{}{
				"detail": map[string]interface{}{
					"msg":        "You have 3 dry run(s) in progress (limit: 3).",
					"error_code": "DRY_RUN_RATE_LIMIT",
					"context": map[string]interface{}{
						"current_count": 3, "max_allowed": 3,
						"limit_kind": "concurrent_execution", "retry_after_seconds": 15,
					},
				},
			})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(cdrLaunchBody))
	})

	stdout, stderr, err := execCreate(t, "create", "composite-dry-run-job", "12", "-o", "json")
	if err != nil {
		t.Fatalf("expected the retry to succeed, got: %v\nstderr:\n%s", err, stderr)
	}
	if calls != 2 {
		t.Fatalf("expected 2 launch attempts, got %d", calls)
	}
	if len(*slept) != 1 || (*slept)[0] != 5*time.Second {
		t.Fatalf("waits = %v, want [5s] from Retry-After", *slept)
	}
	if !strings.Contains(stderr, "throttling") {
		t.Errorf("expected the wait to be explained on stderr, got:\n%s", stderr)
	}
	var payload map[string]int
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("stdout is not a single JSON object (%v):\n%s", err, stdout)
	}
	if payload["composite_dry_run_job_id"] != 5512 {
		t.Errorf("composite_dry_run_job_id = %d, want 5512", payload["composite_dry_run_job_id"])
	}
}

// A refusal that is not a throttle is not retried and keeps its exit code.
func TestCreateCompositeDryRunJob_DoesNotRetryAPermanentFailure(t *testing.T) {
	mock := setupCompositeLaunchEnv(t)
	stubThrottleSleep(t)

	calls := 0
	mock.RegisterHandler("PUT", cdrLaunchPath(cdrLatestRevision), func(w http.ResponseWriter, _ *http.Request) {
		calls++
		testutil.RespondJSON(w, http.StatusBadRequest, map[string]interface{}{
			"detail": "composite spec revision " + cdrLatestRevision + ": no active composite DataSourceModel is linked to its group",
		})
	})

	_, _, err := execCreate(t, "create", "composite-dry-run-job", "12", "-o", "table")
	if err == nil {
		t.Fatal("expected an error for a 400")
	}
	if calls != 1 {
		t.Fatalf("expected exactly 1 attempt for a permanent failure, got %d", calls)
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
}

// cdrDetail is the detail of dry run 5512 of the latest revision, in state,
// with overrides on top.
func cdrDetail(state string, overrides map[string]interface{}) map[string]interface{} {
	d := map[string]interface{}{
		"id":                         5512,
		"composite_spec_revision_id": cdrLatestRevision,
		"composite_spec_group_id":    cdrGroup,
		"downstream_dataset_id":      12,
		"organization_id":            testOrgID,
		"state":                      state,
		"max_spine_rows":             1000,
		"created_at":                 "2026-09-22T10:00:00Z",
		"ingestion_job_id":           9921,
		"structured_summary":         nil,
		"revision_state":             "draft",
	}
	for k, v := range overrides {
		d[k] = v
	}
	return d
}

// cdrFinishedDetail is dry run 5512 finished with a structured summary.
func cdrFinishedDetail() map[string]interface{} {
	return cdrDetail("finished", map[string]interface{}{
		"structured_summary": map[string]interface{}{
			"mode":                     "composite",
			"activation_blocker_count": 1,
			"sample":                   map[string]interface{}{"sampled_spine_rows": 200},
		},
	})
}

// registerDryRunPolls answers each poll of dry run 5512 with the next body,
// repeating the last, and stubs the wait's clock so polling costs no time.
func registerDryRunPolls(t *testing.T, mock *testutil.MockAPIServer, bodies ...map[string]interface{}) {
	t.Helper()
	now := time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)
	composite_dry_runs.SetWaitClock(func() time.Time { return now }, func(d time.Duration) { now = now.Add(d) })
	t.Cleanup(composite_dry_runs.ResetWaitClock)

	polls := 0
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/composite-dry-runs/5512", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, bodies[min(polls, len(bodies)-1)])
		polls++
	})
}

// pwCompositeBanner is the line --wait prints to stderr before its first poll
// of dry run 5512, for a wait of budget.
func pwCompositeBanner(budget string) string {
	return "waiting for composite dry run 5512 to settle, for up to " + budget +
		"; check with: qctl describe composite-dry-run-job 5512\n"
}

// --wait replaces the Next: line with how the dry run ended, and each change
// of state it saw along the way goes to stderr.
func TestCreateCompositeDryRunJob_WaitsForTheDryRun(t *testing.T) {
	mock := setupCompositeLaunchEnv(t)
	registerCompositeLaunch(t, mock, cdrLatestRevision)
	registerDryRunPolls(t, mock, cdrDetail("created", nil), cdrDetail("running", nil), cdrFinishedDetail())

	stdout, stderr, err := execCreate(t, "create", "composite-dry-run-job", "12", "-o", "table", "--wait")
	if err != nil {
		t.Fatalf("Execute() error = %v\nstderr:\n%s", err, stderr)
	}

	want := `composite-dry-run-job/5512 created
  revision : v3 (draft)
composite-dry-run-job/5512 finished
  revision : v3 (draft)
  summary  : sampled 200 spine rows; activation blockers: 1
Next: qctl describe composite-dry-run-job 5512
`
	if stdout != want {
		t.Errorf("stdout mismatch:\n--- got ---\n%s--- want ---\n%s", stdout, want)
	}
	wantErr := pwCompositeBanner("15m0s") + "composite dry run 5512: created\ncomposite dry run 5512: running\n"
	if stderr != wantErr {
		t.Errorf("stderr mismatch:\n--- got ---\n%s--- want ---\n%s", stderr, wantErr)
	}
}

// Under -o json, yaml and name stdout is what it is without --wait, and how
// the dry run ended goes to stderr.
func TestCreateCompositeDryRunJob_WaitKeepsStructuredStdout(t *testing.T) {
	cases := map[string]string{
		"json": "{\n  \"composite_dry_run_job_id\": 5512,\n  \"ingestion_job_id\": 9921\n}\n",
		"yaml": "composite_dry_run_job_id: 5512\ningestion_job_id: 9921\n",
		"name": "composite-dry-run-job/5512\n",
	}
	for format, want := range cases {
		t.Run(format, func(t *testing.T) {
			mock := setupCompositeLaunchEnv(t)
			registerCompositeLaunch(t, mock, cdrLatestRevision)
			registerDryRunPolls(t, mock, cdrFinishedDetail())

			stdout, stderr, err := execCreate(t, "create", "composite-dry-run-job", "12", "-o", format, "--wait")
			if err != nil {
				t.Fatalf("Execute() error = %v\nstderr:\n%s", err, stderr)
			}
			if stdout != want {
				t.Errorf("stdout mismatch:\n--- got ---\n%s--- want ---\n%s", stdout, want)
			}
			if !strings.Contains(stderr, "composite-dry-run-job/5512 finished\n") {
				t.Errorf("stderr lacks the outcome:\n%s", stderr)
			}
			if !strings.HasPrefix(stderr, pwCompositeBanner("15m0s")) {
				t.Errorf("stderr does not open with the wait's banner:\n%s", stderr)
			}
		})
	}
}

// Running out of --wait-timeout exits 124, naming the state the dry run was
// still in; stdout keeps only the launch lines.
func TestCreateCompositeDryRunJob_WaitTimeoutExits124(t *testing.T) {
	mock := setupCompositeLaunchEnv(t)
	registerCompositeLaunch(t, mock, cdrLatestRevision)
	registerDryRunPolls(t, mock, cdrDetail("running", nil))

	stdout, stderr, err := execCreate(t, "create", "composite-dry-run-job", "12", "-o", "table",
		"--wait", "--wait-timeout", "10")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitTimeout {
		t.Fatalf("exit code = %d, want %d (err: %v)", code, qerrors.ExitTimeout, err)
	}
	if !errors.Is(err, composite_dry_runs.ErrWaitTimeout) {
		t.Errorf("err = %v, want ErrWaitTimeout", err)
	}
	for _, want := range []string{
		"after 10s: composite dry run 5512 is still running; only the wait stopped",
		"Next: qctl describe composite-dry-run-job 5512",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q:\n%s", want, err)
		}
	}
	if want := "composite-dry-run-job/5512 created\n  revision : v3 (draft)\n"; stdout != want {
		t.Errorf("stdout mismatch:\n--- got ---\n%s--- want ---\n%s", stdout, want)
	}
	if want := pwCompositeBanner("10s") + "composite dry run 5512: running\n"; stderr != want {
		t.Errorf("stderr mismatch:\n--- got ---\n%s--- want ---\n%s", stderr, want)
	}
}

// A dry run that ends in error exits 1 with its error, after the launch lines.
func TestCreateCompositeDryRunJob_WaitExitsOneWhenTheDryRunFails(t *testing.T) {
	mock := setupCompositeLaunchEnv(t)
	registerCompositeLaunch(t, mock, cdrLatestRevision)
	registerDryRunPolls(t, mock, cdrDetail("error", map[string]interface{}{
		"error_type":    "CompositeRunnerSpecError",
		"error_message": "field policy_ref is not on claims",
	}))

	stdout, _, err := execCreate(t, "create", "composite-dry-run-job", "12", "-o", "table", "--wait")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitGenericError {
		t.Fatalf("exit code = %d, want %d (err: %v)", code, qerrors.ExitGenericError, err)
	}
	if !strings.Contains(err.Error(), "field policy_ref is not on claims") {
		t.Errorf("error lacks the dry run's error_message:\n%s", err)
	}
	want := "composite-dry-run-job/5512 created\n  revision : v3 (draft)\n"
	if stdout != want {
		t.Errorf("stdout mismatch:\n--- got ---\n%s--- want ---\n%s", stdout, want)
	}
}
