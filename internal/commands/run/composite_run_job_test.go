package run

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const (
	crjRevision = "5d0c2a4e-8b1f-4e3a-a6c9-7f2d1b0e4c33"
	crjGroup    = "0b6f4a36-2f7e-4c61-9d7a-3c1f1e2a9b10"
	// crjOutboxID is the outbox row id retry-build answers with. It is
	// distinctive so a test can prove it never reaches the default output.
	crjOutboxID = 90417
)

var (
	crjLatestPath = "/api/orgs/" + testOrgID + "/datasets/12/latest-composite-spec-revision"
	crjRetryPath  = "/api/orgs/" + testOrgID + "/composite-spec-groups/" + crjGroup + "/retry-build"
)

// loggedInEnv is a logged-in test environment against an empty mock server.
func loggedInEnv(t *testing.T) *testutil.MockAPIServer {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")
	return mock
}

// compositeRunEnv serves table 12's latest revision, of group crjGroup.
func compositeRunEnv(t *testing.T) *testutil.MockAPIServer {
	t.Helper()
	mock := loggedInEnv(t)
	mock.RegisterHandler(http.MethodGet, crjLatestPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"composite_spec_revision_id": crjRevision,
			"composite_spec_group_id":    crjGroup,
			"state":                      "enabled",
			"release":                    "v3",
			"description":                nil,
			"spec_sha256":                "ab12",
			"created_at":                 "2026-09-22T10:00:00Z",
			"updated_at":                 "2026-09-22T10:00:00Z",
		})
	})
	return mock
}

// registerRetry answers every retry-build of crjGroup with 200 and the given
// breaker state, and counts the calls.
func registerRetry(mock *testutil.MockAPIServer, breakerWasBlocked bool) *int {
	calls := 0
	mock.RegisterHandler(http.MethodPost, crjRetryPath, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"composite_spec_group_id": crjGroup,
			"breaker_was_blocked":     breakerWasBlocked,
			"run_job_id":              crjOutboxID,
		})
	})
	return &calls
}

// execRun runs the command with stdout and stderr kept apart.
func execRun(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := sensorJobRoot()
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return outBuf.String(), errBuf.String(), err
}

// The default: the rebuild of the table's group is queued, the output says
// whether the breaker had tripped and points at the listing the run will
// appear in -- and never prints the outbox row id as a job id.
func TestRunCompositeRunJob_QueuesTheRebuildOfTheTablesGroup(t *testing.T) {
	cases := map[string]struct {
		blocked bool
		breaker string
	}{
		"breaker tripped":     {true, "  breaker : was tripped, now reset\n"},
		"breaker not tripped": {false, "  breaker : was not tripped\n"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			mock := compositeRunEnv(t)
			calls := registerRetry(mock, tc.blocked)

			stdout, stderr, err := execRun(t, "run", "composite-run-job", "12")
			if err != nil {
				t.Fatalf("Execute() error = %v\nstderr:\n%s", err, stderr)
			}

			want := "linked-table/12 rebuild queued\n" + tc.breaker +
				"Next: qctl get composite-run-jobs --group-id " + crjGroup + " --reason operator_retry\n"
			if stdout != want {
				t.Errorf("stdout mismatch:\n--- got ---\n%s--- want ---\n%s", stdout, want)
			}
			if stderr != "" {
				t.Errorf("stderr should be empty when the rebuild was queued, got:\n%s", stderr)
			}
			if *calls != 1 {
				t.Errorf("retry-build POSTs = %d, want 1", *calls)
			}
		})
	}
}

// The table is named in the output as it was typed: a --table name stays a
// name, a --table-id stays a number.
func TestRunCompositeRunJob_NamesTheTableAsTyped(t *testing.T) {
	cases := map[string]struct {
		args []string
		want string
	}{
		"--table":    {[]string{"--table", "orders_enriched"}, "linked-table/orders_enriched rebuild queued\n"},
		"--table-id": {[]string{"--table-id", "12"}, "linked-table/12 rebuild queued\n"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			mock := compositeRunEnv(t)
			calls := registerRetry(mock, false)
			mock.RegisterHandler(http.MethodGet, "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
					"total_rows": 1, "page": 1,
					"results": []map[string]interface{}{{"id": 12, "name": "orders_enriched", "state": "active"}},
				})
			})

			stdout, stderr, err := execRun(t, append([]string{"run", "composite-run-job"}, tc.args...)...)
			if err != nil {
				t.Fatalf("Execute() error = %v\nstderr:\n%s", err, stderr)
			}
			if !strings.HasPrefix(stdout, tc.want) {
				t.Errorf("stdout should start %q, got:\n%s", tc.want, stdout)
			}
			if *calls != 1 {
				t.Errorf("retry-build POSTs = %d, want 1", *calls)
			}
		})
	}
}

// -o name prints the table's id however the table was named.
func TestRunCompositeRunJob_NameIsTheTableID(t *testing.T) {
	mock := compositeRunEnv(t)
	registerRetry(mock, true)
	mock.RegisterHandler(http.MethodGet, "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"total_rows": 1, "page": 1,
			"results": []map[string]interface{}{{"id": 12, "name": "orders_enriched", "state": "active"}},
		})
	})

	stdout, stderr, err := execRun(t, "run", "composite-run-job", "--table", "orders_enriched", "-o", "name")
	if err != nil {
		t.Fatalf("Execute() error = %v\nstderr:\n%s", err, stderr)
	}
	if stdout != "linked-table/12\n" {
		t.Errorf("stdout = %q, want %q", stdout, "linked-table/12\n")
	}
}

// -o json and -o yaml print the server's answer as sent; -o name the table.
func TestRunCompositeRunJob_OutputFormats(t *testing.T) {
	cases := map[string]string{
		"json": "{\n  \"breaker_was_blocked\": true,\n" +
			"  \"composite_spec_group_id\": \"" + crjGroup + "\",\n" +
			"  \"run_job_id\": 90417\n}\n",
		"yaml": "breaker_was_blocked: true\n" +
			"composite_spec_group_id: " + crjGroup + "\n" +
			"run_job_id: 90417\n",
		"name": "linked-table/12\n",
	}
	for format, want := range cases {
		t.Run(format, func(t *testing.T) {
			mock := compositeRunEnv(t)
			calls := registerRetry(mock, true)

			stdout, stderr, err := execRun(t, "run", "composite-run-job", "12", "-o", format)
			if err != nil {
				t.Fatalf("Execute() error = %v\nstderr:\n%s", err, stderr)
			}
			if stdout != want {
				t.Errorf("stdout mismatch:\n--- got ---\n%s--- want ---\n%s", stdout, want)
			}
			if *calls != 1 {
				t.Errorf("retry-build POSTs = %d, want 1", *calls)
			}
		})
	}
}

// retry-build answers 409 when the group has nothing to build; the command
// exits 2 and the server's reason reaches the user.
func TestRunCompositeRunJob_NothingToBuildExitsTwoWithTheServersReason(t *testing.T) {
	mock := compositeRunEnv(t)
	reason := "composite spec group " + crjGroup + " circuit breaker was reset, but the group has no " +
		"enabled revision / runnable target to rebuild."
	mock.RegisterHandler(http.MethodPost, crjRetryPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusConflict, map[string]interface{}{
			"detail": map[string]interface{}{"msg": reason, "error_id": "e-1"},
		})
	})

	stdout, _, err := execRun(t, "run", "composite-run-job", "12")
	if err == nil {
		t.Fatal("expected the 409 to fail the command")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d; err = %v", code, qerrors.ExitBadUsage, err)
	}
	if !strings.Contains(err.Error(), "no enabled revision / runnable target") {
		t.Errorf("the server's reason should reach the user, got: %v", err)
	}
	if stdout != "" {
		t.Errorf("a refused retry should print nothing on stdout, got:\n%s", stdout)
	}
}

// A table with no composite spec revision is a 404 from the latest-revision
// route, and nothing is sent.
func TestRunCompositeRunJob_TableWithoutARevisionIsNotFound(t *testing.T) {
	mock := loggedInEnv(t)
	mock.RegisterHandler(http.MethodGet, crjLatestPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusNotFound, map[string]interface{}{
			"detail": "latest composite spec revision for dataset 12 can not be found.",
		})
	})
	calls := registerRetry(mock, false)

	_, _, err := execRun(t, "run", "composite-run-job", "12")
	if err == nil {
		t.Fatal("expected an error for a table with no spec revision")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
		t.Errorf("exit code = %d, want %d; err = %v", code, qerrors.ExitNotFound, err)
	}
	if *calls != 0 {
		t.Errorf("sent a retry-build although the table has no revision")
	}
}

// Malformed table selectors exit 2 before any request.
func TestRunCompositeRunJob_RejectsBadFlagsBeforeAnyRequest(t *testing.T) {
	cases := map[string][]string{
		"no table":    {},
		"table twice": {"12", "--table-id", "12"},
		"bad id":      {"--table-id", "-3"},
	}
	for name, flags := range cases {
		t.Run(name, func(t *testing.T) {
			mock := compositeRunEnv(t)
			registerRetry(mock, false)

			_, _, err := execRun(t, append([]string{"run", "composite-run-job"}, flags...)...)
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

// A table name carrying terminal control sequences is printed escaped.
func TestRunCompositeRunJob_EscapesControlCharacters(t *testing.T) {
	mock := compositeRunEnv(t)
	mock.RegisterHandler(http.MethodGet, "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"total_rows": 1, "page": 1,
			"results": []map[string]interface{}{{"id": 12, "name": "orders\x1b[31m", "state": "active"}},
		})
	})
	registerRetry(mock, false)

	stdout, stderr, err := execRun(t, "run", "composite-run-job", "--table", "orders\x1b[31m")
	if err != nil {
		t.Fatalf("Execute() error = %v\nstderr:\n%s", err, stderr)
	}
	if strings.ContainsRune(stdout, '\x1b') {
		t.Errorf("stdout carries a raw escape sequence:\n%q", stdout)
	}
	if !strings.Contains(stdout, "linked-table/orders") {
		t.Errorf("stdout should name the table, got:\n%q", stdout)
	}
}
