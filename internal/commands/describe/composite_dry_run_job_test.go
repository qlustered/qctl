package describe

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const compositeDryRunJobDetailPath = "/composite-dry-runs/5512"

// finishedCompositeDryRun is a detail body for a finished dry run: a Markdown
// summary with a value marker, a structured summary, and metrics, which only
// the raw dump shows.
const finishedCompositeDryRun = `{
  "id": 5512,
  "composite_spec_revision_id": "5d0c2a4e-8b1f-4e3a-a6c9-7f2d1b0e4c33",
  "composite_spec_group_id": "0b6f4a36-2f7e-4c61-9d7a-3c1f1e2a9b10",
  "downstream_dataset_id": 12,
  "organization_id": "b2c3d4e5-f6a7-8901-bcde-f23456789012",
  "state": "finished",
  "max_spine_rows": 1000,
  "seed": 7,
  "summary": "**40 of 50 spine rows matched** ~|\"~*claims*~\"|~",
  "sampling": {"strategy": "random"},
  "metrics": {"join": {"matched": 40}},
  "created_at": "2026-09-22T10:00:00Z",
  "finished_at": "2026-09-22T10:02:00Z",
  "ingestion_job_id": 9921,
  "revision_state": "draft",
  "auto_enable_state": "withheld",
  "user_message": null,
  "wizard_step": null,
  "structured_summary": {"mode": "composite", "activation_blocker_count": 2}
}`

// runningCompositeDryRun has no metrics yet, so the route sends
// structured_summary null.
const runningCompositeDryRun = `{
  "id": 5512,
  "composite_spec_revision_id": "5d0c2a4e-8b1f-4e3a-a6c9-7f2d1b0e4c33",
  "composite_spec_group_id": "0b6f4a36-2f7e-4c61-9d7a-3c1f1e2a9b10",
  "downstream_dataset_id": 12,
  "organization_id": "b2c3d4e5-f6a7-8901-bcde-f23456789012",
  "state": "running",
  "max_spine_rows": 1000,
  "created_at": "2026-09-22T10:00:00Z",
  "structured_summary": null
}`

func setupDescribeCompositeDryRunJobTestCommand() *cobra.Command {
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
	describeCmd.AddCommand(NewCompositeDryRunJobCommand())
	rootCmd.AddCommand(describeCmd)

	return rootCmd
}

// runDescribeCompositeDryRunJob drives the command against a mock server that
// answers the detail route with status and body, and returns stdout+stderr.
func runDescribeCompositeDryRunJob(t *testing.T, status int, body string, args ...string) (string, error) {
	t.Helper()

	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+compositeDryRunJobDetailPath,
		func(w http.ResponseWriter, r *http.Request) {
			if status != http.StatusOK {
				testutil.RespondError(w, status, body)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(body))
		})

	cmd := setupDescribeCompositeDryRunJobTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(append([]string{"describe", "composite-dry-run-job", "5512"}, args...))

	err := cmd.Execute()
	return buf.String(), err
}

func TestDescribeCompositeDryRunJobDefaultYAML(t *testing.T) {
	out, err := runDescribeCompositeDryRunJob(t, http.StatusOK, finishedCompositeDryRun)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, w := range []string{
		"kind: CompositeDryRunJob",
		"name: composite-dry-run-job-5512",
		"spec:",
		"composite_spec_revision_id: 5d0c2a4e-8b1f-4e3a-a6c9-7f2d1b0e4c33",
		"downstream_dataset_id: 12",
		"seed: 7",
		"status:",
		"state: finished",
		"revision_state: draft",
		"auto_enable_state: withheld",
		"structured_summary:",
		"activation_blocker_count: 2",
		"ingestion_job_id: 9921",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("YAML output should contain %q, got:\n%s", w, out)
		}
	}
	if strings.Contains(out, "metrics:") {
		t.Errorf("the manifest carries metrics, which belong to -vv only:\n%s", out)
	}
}

// TestDescribeCompositeDryRunJobRendersTheSummary: summary is Markdown with
// value markers, so no view may print the raw markup, and -o table prints the
// complete text under a Summary heading.
func TestDescribeCompositeDryRunJobRendersTheSummary(t *testing.T) {
	for _, tc := range []struct {
		format string
		args   []string
	}{
		{format: "yaml", args: []string{"-o", "yaml"}},
		{format: "json", args: []string{"-o", "json"}},
		{format: "table", args: []string{"-o", "table", "--max-column-width", "2000"}},
	} {
		out, err := runDescribeCompositeDryRunJob(t, http.StatusOK, finishedCompositeDryRun, tc.args...)
		if err != nil {
			t.Fatalf("-o %s: unexpected error: %v", tc.format, err)
		}
		if strings.Contains(out, "~*") || strings.Contains(out, "~|") || strings.Contains(out, "**40") {
			t.Errorf("-o %s printed raw markup:\n%s", tc.format, out)
		}
		if !strings.Contains(out, "40 of 50 spine rows matched") {
			t.Errorf("-o %s lost the summary text:\n%s", tc.format, out)
		}
		if tc.format == "table" && !strings.Contains(out, "\nSummary:\n") {
			t.Errorf("-o table did not print the complete summary under a Summary heading:\n%s", out)
		}
	}
}

// erroredCompositeDryRun failed on its spec: user_message is the wizard copy
// atlas's build_user_message gives this error_loc.
const erroredCompositeDryRun = `{
  "id": 5512,
  "composite_spec_revision_id": "5d0c2a4e-8b1f-4e3a-a6c9-7f2d1b0e4c33",
  "composite_spec_group_id": "0b6f4a36-2f7e-4c61-9d7a-3c1f1e2a9b10",
  "downstream_dataset_id": 12,
  "organization_id": "b2c3d4e5-f6a7-8901-bcde-f23456789012",
  "state": "error",
  "max_spine_rows": 1000,
  "created_at": "2026-09-22T10:00:00Z",
  "error_type": "CompositeRunnerSpecError",
  "error_message": "field policy_ref is not on claims",
  "error_loc": ["revision", "output", "policy_ref", "field"],
  "user_message": "Something in the Output columns step needs attention before you can continue.",
  "wizard_step": "output_columns",
  "structured_summary": null
}`

// TestDescribeCompositeDryRunJobTableLeadsWithTheErrorMessage: -o table's
// Error Message section prints error_message first and user_message after
// it, in full.
func TestDescribeCompositeDryRunJobTableLeadsWithTheErrorMessage(t *testing.T) {
	out, err := runDescribeCompositeDryRunJob(t, http.StatusOK, erroredCompositeDryRun, "-o", "table")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	heading := strings.Index(out, "\nError Message:\n")
	if heading < 0 {
		t.Fatalf("-o table printed no Error Message section:\n%s", out)
	}
	section := out[heading:]
	detail := strings.Index(section, "field policy_ref is not on claims")
	wizardCopy := strings.Index(section, "Something in the Output columns step needs attention before you can continue.")
	if detail < 0 || wizardCopy < 0 {
		t.Fatalf("Error Message section is missing a message:\n%s", section)
	}
	if detail > wizardCopy {
		t.Errorf("error_message should precede user_message in the Error Message section:\n%s", section)
	}
}

// TestDescribeCompositeDryRunJobNullSummaryIsAbsent: a dry run that has not
// finished has a null structured_summary, and every view leaves the key out
// rather than printing a summary of zeros.
func TestDescribeCompositeDryRunJobNullSummaryIsAbsent(t *testing.T) {
	for _, format := range []string{"yaml", "json"} {
		out, err := runDescribeCompositeDryRunJob(t, http.StatusOK, runningCompositeDryRun, "-o", format)
		if err != nil {
			t.Fatalf("-o %s: unexpected error: %v", format, err)
		}
		if !strings.Contains(out, "running") {
			t.Fatalf("-o %s did not print the job at all:\n%s", format, out)
		}
		for _, absent := range []string{"structured_summary", "activation_blocker_count"} {
			if strings.Contains(out, absent) {
				t.Errorf("-o %s printed %q for a null summary:\n%s", format, absent, out)
			}
		}
	}
}

// TestDescribeCompositeDryRunJobRawDump: -vv is the response as received,
// metrics included, and the default view is not.
func TestDescribeCompositeDryRunJobRawDump(t *testing.T) {
	out, err := runDescribeCompositeDryRunJob(t, http.StatusOK, finishedCompositeDryRun, "-vv")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, w := range []string{"raw_response:", "metrics:", "matched: 40", "organization_id:"} {
		if !strings.Contains(out, w) {
			t.Errorf("-vv output should contain %q, got:\n%s", w, out)
		}
	}

	plain, err := runDescribeCompositeDryRunJob(t, http.StatusOK, finishedCompositeDryRun)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(plain, "raw_response:") {
		t.Error("the default view rendered the raw dump; the -vv gate is not doing anything")
	}
}

// largeCountsCompositeDryRun is finishedCompositeDryRun with a 7-digit count
// and one past 2^53 in structured_summary and in metrics.
var largeCountsCompositeDryRun = strings.NewReplacer(
	`"activation_blocker_count": 2}`, `"activation_blocker_count": 1234567, "spine_rows": 9007199254740993}`,
	`{"matched": 40}`, `{"matched": 1234567, "scanned": 9007199254740993}`,
).Replace(finishedCompositeDryRun)

// TestDescribeCompositeDryRunJobKeepsIntegers: the default view and the -vv
// raw dump print a 7-digit count as an integer in YAML, and one past 2^53
// unrounded in JSON.
func TestDescribeCompositeDryRunJobKeepsIntegers(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"default yaml", nil, "activation_blocker_count: 1234567\n"},
		{"default json", []string{"-o", "json"}, `"spine_rows": 9007199254740993`},
		{"-vv yaml", []string{"-vv"}, "matched: 1234567\n"},
		{"-vv json", []string{"-vv", "-o", "json"}, `"scanned": 9007199254740993`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runDescribeCompositeDryRunJob(t, http.StatusOK, largeCountsCompositeDryRun, tc.args...)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("output should contain %q, got:\n%s", tc.want, out)
			}
		})
	}
}

// TestDescribeCompositeDryRunJobNotFoundExitsThree: a missing id and another
// organization's id are the same 404 (get_composite_dry_run,
// capi/capi/views/composite_dry_run_view.py).
func TestDescribeCompositeDryRunJobNotFoundExitsThree(t *testing.T) {
	_, err := runDescribeCompositeDryRunJob(t, http.StatusNotFound,
		"composite dry run job 5512 not found in this organization.")
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitNotFound {
		t.Errorf("exit code = %d, want %d; err = %v", got, qerrors.ExitNotFound, err)
	}
}

// TestDescribeCompositeDryRunJobNonNumericIDExitsTwo: a typed id that is not a
// number is bad usage, and nothing is sent.
func TestDescribeCompositeDryRunJobNonNumericIDExitsTwo(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	cmd := setupDescribeCompositeDryRunJobTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"describe", "composite-dry-run-job", "latest"})

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
