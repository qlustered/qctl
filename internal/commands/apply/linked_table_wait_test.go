package apply

import (
	"bytes"
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

const linkedTableDryRunPath = "/api/orgs/" + testOrgID + "/composite-dry-runs/812"

// linkedTableDryRun is the detail of the dry run linkedTableApplyAnswer
// launched, in state, with overrides on top.
func linkedTableDryRun(state, autoEnable string, overrides map[string]any) map[string]any {
	d := map[string]any{
		"id":                         812,
		"composite_spec_revision_id": "5d0c2a4e-8b1f-4e3a-a6c9-7f2d1b0e4c33",
		"composite_spec_group_id":    linkedTableGroupID,
		"downstream_dataset_id":      7,
		"organization_id":            testOrgID,
		"state":                      state,
		"max_spine_rows":             1000,
		"created_at":                 "2026-09-22T10:00:00Z",
		"ingestion_job_id":           9001,
		"structured_summary":         nil,
		"revision_state":             "draft",
	}
	if autoEnable != "" {
		d["auto_enable_state"] = autoEnable
	}
	for k, v := range overrides {
		d[k] = v
	}
	return d
}

// serveLinkedTableWait answers the apply with answer and each poll of dry run
// 812 with the next of polls, repeating the last. The wait's clock is stubbed
// so polling costs no time. It returns the manifest's path.
func serveLinkedTableWait(t *testing.T, answer map[string]any, polls ...map[string]any) (*testutil.MockAPIServer, string) {
	t.Helper()
	now := time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)
	composite_dry_runs.SetWaitClock(func() time.Time { return now }, func(d time.Duration) { now = now.Add(d) })
	t.Cleanup(composite_dry_runs.ResetWaitClock)

	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/composite-datasets/apply", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, answer)
	})
	served := 0
	mock.RegisterHandler("GET", linkedTableDryRunPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, polls[min(served, len(polls)-1)])
		served++
	})
	env := setupLinkedTableEnv(t, mock.URL())
	return mock, env.CreateFile("linked-table.yaml", linkedTableYAML())
}

// execApplyRoot runs `qctl <args>` with stdout and stderr captured apart.
func execApplyRoot(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	root := setupApplyRoot()
	root.SilenceUsage = true
	root.SilenceErrors = true
	var outBuf, errBuf bytes.Buffer
	root.SetOut(&outBuf)
	root.SetErr(&errBuf)
	root.SetArgs(args)
	err = root.Execute()
	return outBuf.String(), errBuf.String(), err
}

func countRequests(mock *testutil.MockAPIServer, path string) int {
	n := 0
	for _, r := range mock.Requests() {
		if strings.HasSuffix(r, path) {
			n++
		}
	}
	return n
}

// pwLinkedTableBanner is the line --wait prints to stderr before its first
// poll of dry run 812, at the default --wait-timeout.
const pwLinkedTableBanner = "waiting for composite dry run 812 to settle, for up to 15m0s; " +
	"check with: qctl describe composite-dry-run-job 812\n"

const linkedTableLaunchedLines = `linked-table/orders-enriched created
  revision : v1 (draft)
  dry run  : composite dry run 812 launched (ingestion job 9001); the revision is enabled if it succeeds and the enable gates pass
`

// --wait outlasts the dry run and then the auto-enable, which the backend
// records after the dry run is already finished; the subcommand and apply -f
// alike.
func TestApplyLinkedTable_WaitsForTheAutoEnable(t *testing.T) {
	for name, args := range map[string][]string{
		"subcommand": {"apply", "linked-table", "--wait", "-f"},
		"apply -f":   {"apply", "--wait", "-f"},
	} {
		t.Run(name, func(t *testing.T) {
			mock, path := serveLinkedTableWait(t, linkedTableApplyAnswer(nil),
				linkedTableDryRun("running", "pending", nil),
				linkedTableDryRun("finished", "pending", nil),
				linkedTableDryRun("finished", "enabled", map[string]any{"revision_state": "enabled"}),
			)

			stdout, stderr, err := execApplyRoot(t, append(args, path)...)
			if err != nil {
				t.Fatalf("execute: %v\nstderr:\n%s", err, stderr)
			}
			want := linkedTableLaunchedLines + `composite-dry-run-job/812 finished
  revision : v1 (enabled; auto-enable: enabled)
Next: qctl get composite-run-jobs --group-id ` + linkedTableGroupID + "\n"
			if stdout != want {
				t.Errorf("stdout mismatch:\n--- got ---\n%s--- want ---\n%s", stdout, want)
			}
			wantErr := pwLinkedTableBanner + "composite dry run 812: running\ncomposite dry run 812: finished; waiting for its auto-enable\n"
			if stderr != wantErr {
				t.Errorf("stderr mismatch:\n--- got ---\n%s--- want ---\n%s", stderr, wantErr)
			}
			if n := countRequests(mock, linkedTableDryRunPath); n != 3 {
				t.Errorf("polls = %d, want 3", n)
			}
		})
	}
}

// An auto-enable the backend withheld exits 1 with its reason.
func TestApplyLinkedTable_WaitExitsOneWhenTheEnableIsWithheld(t *testing.T) {
	_, path := serveLinkedTableWait(t, linkedTableApplyAnswer(nil),
		linkedTableDryRun("finished", "withheld", map[string]any{
			"auto_enable_detail": "activation blockers present: 3 spine rows matched nothing in claims",
		}),
	)

	stdout, _, err := execApplyLinkedTable(t, path, "--wait")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitGenericError {
		t.Fatalf("exit code = %d, want %d (err: %v)", code, qerrors.ExitGenericError, err)
	}
	for _, want := range []string{
		"composite dry run 812 finished, but did not enable the revision (auto_enable_state: withheld)",
		"activation blockers present: 3 spine rows matched nothing in claims",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q:\n%s", want, err)
		}
	}
	if stdout != linkedTableLaunchedLines {
		t.Errorf("stdout mismatch:\n--- got ---\n%s--- want ---\n%s", stdout, linkedTableLaunchedLines)
	}
}

// An apply that launched no dry run has nothing to wait for: the output is
// what it is without --wait, and nothing is polled.
func TestApplyLinkedTable_WaitWithNoDryRunReturnsAtOnce(t *testing.T) {
	answer := linkedTableApplyAnswer(map[string]any{
		"result": "unchanged", "revision_state": "enabled", "converged": true,
		"composite_dry_run_job_id": nil, "ingestion_job_id": nil,
	})
	mock, path := serveLinkedTableWait(t, answer, linkedTableDryRun("running", "", nil))

	stdout, _, err := execApplyLinkedTable(t, path, "--wait")
	if err != nil {
		t.Fatal(err)
	}
	want := "linked-table/orders-enriched unchanged\n  revision : v1 (enabled)\nNext: qctl get composite-run-jobs --group-id " + linkedTableGroupID + "\n"
	if stdout != want {
		t.Errorf("stdout mismatch:\n--- got ---\n%s--- want ---\n%s", stdout, want)
	}
	if n := countRequests(mock, linkedTableDryRunPath); n != 0 {
		t.Errorf("polls = %d, want 0", n)
	}
}

// Under -o json stdout is the server's answer alone, as without --wait, and
// the launched block, the banner and how the dry run ended go to stderr.
func TestApplyLinkedTable_WaitKeepsJSONStdout(t *testing.T) {
	_, path := serveLinkedTableWait(t, linkedTableApplyAnswer(nil),
		linkedTableDryRun("finished", "enabled", map[string]any{"revision_state": "enabled"}))

	stdout, stderr, err := execApplyLinkedTable(t, path, "--wait", "-o", "json")
	if err != nil {
		t.Fatalf("execute: %v\nstderr:\n%s", err, stderr)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
	}
	if got["composite_dry_run_job_id"] != float64(812) {
		t.Errorf("stdout is not the server's answer: %s", stdout)
	}
	if !strings.Contains(stderr, "composite-dry-run-job/812 finished\n") {
		t.Errorf("stderr lacks the outcome:\n%s", stderr)
	}
	if !strings.HasPrefix(stderr, linkedTableLaunchedLines+pwLinkedTableBanner) {
		t.Errorf("stderr does not open with the launched block and then the wait's banner:\n%s", stderr)
	}
}

// Running out of --wait-timeout exits 124, naming the state the dry run was
// still in, on both commands.
func TestApplyLinkedTable_WaitTimeoutExits124(t *testing.T) {
	for name, args := range map[string][]string{
		"subcommand": {"apply", "linked-table", "--wait", "--wait-timeout", "10", "-f"},
		"apply -f":   {"apply", "--wait", "--wait-timeout", "10", "-f"},
	} {
		t.Run(name, func(t *testing.T) {
			_, path := serveLinkedTableWait(t, linkedTableApplyAnswer(nil), linkedTableDryRun("running", "pending", nil))

			stdout, stderr, err := execApplyRoot(t, append(args, path)...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitTimeout {
				t.Fatalf("exit code = %d, want %d (err: %v)", code, qerrors.ExitTimeout, err)
			}
			if !errors.Is(err, composite_dry_runs.ErrWaitTimeout) {
				t.Errorf("err = %v, want ErrWaitTimeout", err)
			}
			if !strings.Contains(err.Error(), "after 10s: composite dry run 812 is still running; only the wait stopped") {
				t.Errorf("error does not name the state:\n%s", err)
			}
			if stdout != linkedTableLaunchedLines {
				t.Errorf("stdout mismatch:\n--- got ---\n%s--- want ---\n%s", stdout, linkedTableLaunchedLines)
			}
			want := "waiting for composite dry run 812 to settle, for up to 10s; check with: qctl describe composite-dry-run-job 812\n" +
				"composite dry run 812: running\n"
			if stderr != want {
				t.Errorf("stderr mismatch:\n--- got ---\n%s--- want ---\n%s", stderr, want)
			}
		})
	}
}

// A flag the apply would ignore is refused before anything is sent, on both
// commands and whatever kind the file holds.
func TestApplyLinkedTable_WaitFlagsRefusedBeforeAnyRequest(t *testing.T) {
	cases := map[string][]string{
		"--wait with --validate-only":   {"apply", "linked-table", "--wait", "--validate-only", "-f"},
		"--wait-timeout alone":          {"apply", "linked-table", "--wait-timeout", "60", "-f"},
		"apply -f, --validate-only":     {"apply", "--wait", "--validate-only", "-f"},
		"apply -f, --wait-timeout zero": {"apply", "--wait", "--wait-timeout", "0", "-f"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			mock, path := serveLinkedTableWait(t, linkedTableApplyAnswer(nil), linkedTableDryRun("finished", "", nil))

			_, _, err := execApplyRoot(t, append(args, path)...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d (err: %v)", code, qerrors.ExitBadUsage, err)
			}
			if reqs := mock.Requests(); len(reqs) != 0 {
				t.Errorf("expected no request, got %v", reqs)
			}
		})
	}
}

// apply -f refuses the flags before the first document, so a Table ahead of
// the linked table is not applied either.
func TestApplyLinkedTable_WaitFlagsRefusedBeforeTheFirstDocument(t *testing.T) {
	mock, _ := serveLinkedTableWait(t, linkedTableApplyAnswer(nil), linkedTableDryRun("finished", "", nil))
	env := setupLinkedTableEnv(t, mock.URL())
	path := env.CreateFile("mixed.yaml", `apiVersion: qluster.ai/v1
kind: Table
metadata:
  name: orders
spec:
  destination_id: 3
  database_name: analytics
  schema_name: public
  table_name: orders
  migration_policy: apply_asap
  data_loading_process: append
  backup_settings_id: 1
---`+linkedTableYAML())

	for name, flags := range map[string][]string{
		"--wait with --validate-only": {"--wait", "--validate-only"},
		"--wait-timeout alone":        {"--wait-timeout", "60"},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := execApplyRoot(t, append([]string{"apply", "-f", path}, flags...)...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d (err: %v)", code, qerrors.ExitBadUsage, err)
			}
			if reqs := mock.Requests(); len(reqs) != 0 {
				t.Errorf("expected no request, got %v", reqs)
			}
		})
	}
}

// The refusal reads the flags' values: an explicit --validate-only=false
// beside --wait is an ordinary apply that waits.
func TestApplyLinkedTable_ValidateOnlyFalseStillWaits(t *testing.T) {
	mock, path := serveLinkedTableWait(t, linkedTableApplyAnswer(nil), linkedTableDryRun("finished", "enabled", nil))

	if _, stderr, err := execApplyLinkedTable(t, path, "--wait", "--validate-only=false"); err != nil {
		t.Fatalf("execute: %v\nstderr:\n%s", err, stderr)
	}
	if n := countRequests(mock, linkedTableDryRunPath); n != 1 {
		t.Errorf("polls = %d, want 1", n)
	}
}
