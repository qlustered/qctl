package describe

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/composite_run_jobs"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const compositeRunJobDetailPath = "/composite-run-jobs/123"

// setupDescribeCompositeRunJobTestCommand builds `qctl describe
// composite-run-job` on a root carrying the real persistent flags.
func setupDescribeCompositeRunJobTestCommand() *cobra.Command {
	rootCmd := &cobra.Command{Use: "qctl"}
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "", "output format")
	rootCmd.PersistentFlags().Bool("no-headers", false, "Don't print column headers")
	rootCmd.PersistentFlags().String("columns", "", "Columns to display")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "Max column width")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "Allow plaintext secrets")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "Allow insecure http")
	rootCmd.PersistentFlags().CountP("verbose", "v", "Verbosity")

	describeCmd := &cobra.Command{Use: "describe", Short: "Describe resources"}
	describeCmd.AddCommand(NewCompositeRunJobCommand())
	rootCmd.AddCommand(describeCmd)

	return rootCmd
}

func sampleCompositeRunJobFull() composite_run_jobs.CompositeRunJobFull {
	createdAt := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2026, 9, 10, 10, 5, 0, 0, time.UTC)
	startedAt := time.Date(2026, 9, 10, 10, 1, 0, 0, time.UTC)
	finishedAt := time.Date(2026, 9, 10, 10, 6, 0, 0, time.UTC)
	changed := 5
	downstream := 9
	publisher := "ingestion_controller"
	groupID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	ruleRunIDs := []int{41, 42}
	targetResults := []map[string]interface{}{{"downstream_dataset_id": 9, "rows_written": 120}}
	trigger := map[string]interface{}{"ingestion_job_id": 900}

	return composite_run_jobs.CompositeRunJobFull{
		ID:                         123,
		State:                      api.IngestionJobStateFinished,
		Reason:                     "upstream_ingestion_finished",
		Publisher:                  &publisher,
		ChangedDatasetID:           &changed,
		AffectedTargetCount:        1,
		CompletedTargetCount:       1,
		AttemptID:                  2,
		Msg:                        "composite recompute finished",
		TouchedGroupIds:            &[]openapi_types.UUID{groupID},
		CurrentDownstreamDatasetID: &downstream,
		TargetResults:              &targetResults,
		TriggerContext:             &trigger,
		RuleRunIds:                 &ruleRunIDs,
		CreatedAt:                  createdAt,
		UpdatedAt:                  updatedAt,
		StartedAt:                  &startedAt,
		FinishedAt:                 &finishedAt,
	}
}

// runDescribeCompositeRunJob drives the command against a mock server that
// serves one job, and returns everything written to stdout+stderr.
func runDescribeCompositeRunJob(t *testing.T, args ...string) (string, error) {
	t.Helper()
	return runDescribeCompositeRunJobServing(t, sampleCompositeRunJobFull(), args...)
}

// runDescribeCompositeRunJobServing is runDescribeCompositeRunJob serving job.
func runDescribeCompositeRunJobServing(t *testing.T, job composite_run_jobs.CompositeRunJobFull, args ...string) (string, error) {
	t.Helper()

	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+compositeRunJobDetailPath,
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, job)
		})

	cmd := setupDescribeCompositeRunJobTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(append([]string{"describe", "composite-run-job", "123"}, args...))

	err := cmd.Execute()
	return buf.String(), err
}

func TestDescribeCompositeRunJobDefaultYAML(t *testing.T) {
	out, err := runDescribeCompositeRunJob(t)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []string{
		"apiVersion: qluster.ai/v1",
		"kind: CompositeRunJob",
		"name: composite-run-job-123",
		"spec:",
		"reason: upstream_ingestion_finished",
		"changed_dataset_id: 5",
		"status:",
		"state: finished",
		"attempt_id: 2",
		"completed_target_count: 1",
		"rule_run_ids:",
		"current_downstream_dataset_id: 9",
	}
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("YAML output should contain %q, got:\n%s", w, out)
		}
	}
}

// An infrastructure failure stores the same text in msg and error_message
// (atlas composite_run_job_controller.py record_composite_run_infra_failure),
// and an alert's text is its Markdown message. Both fields render it: neither
// shows a value marker or bold-red delimiters while the other shows the value.
func TestDescribeCompositeRunJobRendersBothFailureFields(t *testing.T) {
	failure := `~|"~*Hyundai Sonata Hybrid Limited Edition*~"|~ is not a valid ***Car model***`
	job := sampleCompositeRunJobFull()
	job.State = api.IngestionJobStateError
	job.Msg = failure
	job.ErrorMessage = &failure
	for _, tc := range []struct {
		format string
		args   []string
	}{
		{format: "yaml", args: []string{"-o", "yaml"}},
		{format: "json", args: []string{"-o", "json"}},
		{format: "table", args: []string{"-o", "table", "--max-column-width", "2000"}},
	} {
		out, err := runDescribeCompositeRunJobServing(t, job, tc.args...)
		if err != nil {
			t.Fatalf("-o %s: unexpected error: %v", tc.format, err)
		}
		if strings.Contains(out, "~*") || strings.Contains(out, "~|") {
			t.Errorf("-o %s printed raw markup:\n%s", tc.format, out)
		}
		wantCount := 2
		if tc.format == "table" {
			wantCount = 4 // the two status fields plus their complete detail sections
		}
		if n := strings.Count(out, `Hyundai Sonata Hybrid Limited Edition`); n != wantCount {
			t.Errorf("-o %s: the value appears %d times, want %d:\n%s", tc.format, n, wantCount, out)
		}
		if tc.format == "table" {
			for _, want := range []string{"\nMessage:\n", "\nError Message:\n"} {
				if !strings.Contains(out, want) {
					t.Errorf("-o table did not print the complete failure under %q:\n%s", want, out)
				}
			}
		}
	}
}

func TestDescribeCompositeRunJobJSON(t *testing.T) {
	out, err := runDescribeCompositeRunJob(t, "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, w := range []string{`"kind": "CompositeRunJob"`, `"reason": "upstream_ingestion_finished"`, `"state": "finished"`} {
		if !strings.Contains(out, w) {
			t.Errorf("JSON output should contain %q, got:\n%s", w, out)
		}
	}
}

// TestDescribeCompositeRunJobRawDump: -vv is the untouched response, so it must
// carry the wire field names -- including the ones the manifest never renders.
func TestDescribeCompositeRunJobRawDump(t *testing.T) {
	out, err := runDescribeCompositeRunJob(t, "-vv")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, w := range []string{"raw_response:", "affected_target_count: 1", "trigger_context:", "worker_job_id:"} {
		if !strings.Contains(out, w) {
			t.Errorf("-vv output should contain %q, got:\n%s", w, out)
		}
	}

	// The default view is a manifest, not the raw response: prove the two
	// differ, so a broken verbosity gate cannot pass both assertions.
	plain, err := runDescribeCompositeRunJob(t)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(plain, "raw_response:") {
		t.Error("the default view rendered the raw dump; the -vv gate is not doing anything")
	}
}

// TestDescribeCompositeRunJobRawDumpKeepsIntegers: -vv prints a 7-digit count
// as an integer in YAML, and one past 2^53 unrounded in JSON.
func TestDescribeCompositeRunJobRawDumpKeepsIntegers(t *testing.T) {
	job := sampleCompositeRunJobFull()
	job.AffectedTargetCount = 1234567
	job.CompletedTargetCount = 9007199254740993

	out, err := runDescribeCompositeRunJobServing(t, job, "-vv")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "affected_target_count: 1234567\n") {
		t.Errorf("-vv YAML should print affected_target_count: 1234567, got:\n%s", out)
	}

	out, err = runDescribeCompositeRunJobServing(t, job, "-vv", "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `"completed_target_count": 9007199254740993,`) {
		t.Errorf("-vv JSON should print completed_target_count: 9007199254740993, got:\n%s", out)
	}
}

// TestDescribeCompositeRunJobServerErrors covers the two the backend documents
// on this route: 404 for a missing id or one anchored in another organization
// (verify_composite_run_job_organization,
// capi/capi/controllers/composite_run_job_controller.py), and 403 when the
// caller cannot read every dataset the response names
// (check_composite_run_job_datasets_for_user).
func TestDescribeCompositeRunJobServerErrors(t *testing.T) {
	cases := map[string]struct {
		status   int
		body     string
		wantExit int
	}{
		"missing or other org": {http.StatusNotFound, "Composite run job 123 can not be found.", qerrors.ExitNotFound},
		"undisclosable table":  {http.StatusForbidden, "User is not authorized to access table 9", qerrors.ExitUnauthorized},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()

			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
			env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
			env.SetupCredential(endpointKey, testOrgID, "test-token")

			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+compositeRunJobDetailPath,
				func(w http.ResponseWriter, r *http.Request) {
					testutil.RespondError(w, tc.status, tc.body)
				})

			cmd := setupDescribeCompositeRunJobTestCommand()
			var buf bytes.Buffer
			cmd.SetOut(&buf)
			cmd.SetErr(&buf)
			cmd.SetArgs([]string{"describe", "composite-run-job", "123"})

			err := cmd.Execute()
			if err == nil {
				t.Fatal("expected an error")
			}
			if got := qerrors.GetExitCode(err); got != tc.wantExit {
				t.Errorf("exit code = %d, want %d; err = %v", got, tc.wantExit, err)
			}
		})
	}
}

// TestDescribeCompositeRunJobNonNumericIDExitsTwo: the id is typed by the user,
// so a typo is bad usage (exit 2), not a generic failure, and nothing is sent.
func TestDescribeCompositeRunJobNonNumericIDExitsTwo(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	cmd := setupDescribeCompositeRunJobTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"describe", "composite-run-job", "not-a-number"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (ExitBadUsage); err = %v", got, qerrors.ExitBadUsage, err)
	}
	if len(mock.Requests()) != 0 {
		t.Errorf("a request escaped argument validation: %v", mock.Requests())
	}
}
