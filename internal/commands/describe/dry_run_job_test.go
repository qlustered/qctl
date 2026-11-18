package describe

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

func setupDryRunJobTestCommand() *cobra.Command {
	rootCmd := &cobra.Command{Use: "qctl"}
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format")
	rootCmd.PersistentFlags().Bool("no-headers", false, "")
	rootCmd.PersistentFlags().String("columns", "", "")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "")
	rootCmd.PersistentFlags().CountP("verbose", "v", "Verbosity level")

	describeCmd := &cobra.Command{Use: "describe", Short: "Describe resource"}
	describeCmd.AddCommand(NewDryRunJobCommand())
	rootCmd.AddCommand(describeCmd)
	return rootCmd
}

func sampleDryRunJobFull() api.DryRunJobFullSchema {
	snapshotAt := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
	summary := "5 rows changed state"
	return api.DryRunJobFullSchema{
		ID:         42,
		DatasetID:  10,
		State:      "finished",
		Summary:    &summary,
		SnapshotAt: &snapshotAt,
		RuleRunSpecs: []api.RuleRunSpec{
			{Position: 0},
		},
		Sampling: &api.SamplingMetadata{
			Cap:       1000,
			AlphaPref: 0.05,
		},
	}
}

func TestDescribeDryRunJob_DefaultOutput(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dry-runs/42", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleDryRunJobFull())
	})

	cmd := setupDryRunJobTestCommand()
	cmd.SetArgs([]string{"describe", "dry-run-job", "42"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()

	// Should contain essential fields
	essentialFields := []string{
		"apiVersion: qluster.ai/v1",
		"kind: DryRunJob",
		"state: finished",
	}
	for _, field := range essentialFields {
		if !strings.Contains(output, field) {
			t.Errorf("output should contain %q, got:\n%s", field, output)
		}
	}
}

func TestDescribeDryRunJob_JSONOutput(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dry-runs/42", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleDryRunJobFull())
	})

	cmd := setupDryRunJobTestCommand()
	cmd.SetArgs([]string{"describe", "dry-run-job", "42", "-o", "json"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, `"kind": "DryRunJob"`) {
		t.Errorf("JSON output should contain kind DryRunJob, got:\n%s", output)
	}
}

func TestDescribeDryRunJob_TableRendersNestedSummary(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	job := sampleDryRunJobFull()
	summary := `Changed ~|"~*Hyundai Sonata Hybrid Limited Edition*~"|~<br>review **model**`
	job.Summary = &summary
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dry-runs/42", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, job)
	})

	cmd := setupDryRunJobTestCommand()
	cmd.SetArgs([]string{"describe", "dry-run-job", "42", "-o", "table", "--max-column-width", "2000"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "\nSummary:\n"+`Changed "Hyundai Sonata Hybrid Limited Edition"`+"\nreview model\n") {
		t.Errorf("table output did not inline-render the nested summary:\n%s", output)
	}
	for _, marker := range []string{"~*", "~|", "<br>", "**"} {
		if strings.Contains(output, marker) {
			t.Errorf("table output retained backend message syntax %q:\n%s", marker, output)
		}
	}
}

func TestDescribeDryRunJob_VeryVerboseRawDump(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dry-runs/42", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleDryRunJobFull())
	})

	cmd := setupDryRunJobTestCommand()
	cmd.SetArgs([]string{"describe", "dry-run-job", "42", "-vvv"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()

	if !strings.Contains(output, "raw_response") {
		t.Errorf("-vvv output should contain raw_response, got:\n%s", output)
	}
	if !strings.Contains(output, "apiVersion") {
		t.Errorf("-vvv output should contain apiVersion, got:\n%s", output)
	}
}

func TestDescribeDryRunJob_Verbose(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dry-runs/42", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleDryRunJobFull())
	})

	cmd := setupDryRunJobTestCommand()
	cmd.SetArgs([]string{"describe", "dry-run-job", "42", "-v"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()
	// -v should include sampling data
	if !strings.Contains(output, "sampling") {
		t.Errorf("-v output should contain sampling, got:\n%s", output)
	}
}

func TestDescribeDryRunJob_InvalidID(t *testing.T) {
	cmd := setupDryRunJobTestCommand()
	cmd.SetArgs([]string{"describe", "dry-run-job", "abc"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for invalid ID")
	}
}

func TestDescribeDryRunJob_NoArgs(t *testing.T) {
	cmd := setupDryRunJobTestCommand()
	cmd.SetArgs([]string{"describe", "dry-run-job"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for no args")
	}
}

func TestDescribeDryRunJob_NotFound(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dry-runs/999", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondError(w, http.StatusNotFound, "Not found")
	})

	cmd := setupDryRunJobTestCommand()
	cmd.SetArgs([]string{"describe", "dry-run-job", "999"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for not found")
	}
}

// describe echoes the run's own severity policy and invents nothing. The
// server returns rule_run_specs as the launch stored them, and an absent
// severity is resolved to as_declared only when the backend loads the rule
// (RuleController._get_dry_run_rules_and_versions,
// cettings/.../rule_controller_core.py), so rendering a default here would
// report a value the job record does not carry.
func TestDescribeDryRunJob_EchoesSeverity(t *testing.T) {
	blocker := api.RuleSeverityBlocker
	for _, tc := range []struct {
		name     string
		severity *api.RuleSeverity
		wantLine bool
	}{
		{"set", &blocker, true},
		{"unset", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()

			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
			env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
			env.SetupCredential(endpointKey, testOrgID, "test-token")

			job := sampleDryRunJobFull()
			job.RuleRunSpecs = []api.RuleRunSpec{{Position: 0, Severity: tc.severity}}
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dry-runs/42", func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, job)
			})

			cmd := setupDryRunJobTestCommand()
			cmd.SetArgs([]string{"describe", "dry-run-job", "42"})
			var buf bytes.Buffer
			cmd.SetOut(&buf)
			cmd.SetErr(&buf)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}

			output := buf.String()
			if got := strings.Contains(output, "severity: blocker"); got != tc.wantLine {
				t.Errorf("contains %q = %t, want %t; output:\n%s", "severity: blocker", got, tc.wantLine, output)
			}
			if !tc.wantLine && strings.Contains(output, "severity:") {
				t.Errorf("output names a severity for a spec that carries none:\n%s", output)
			}
		})
	}
}

// -v and -vvv print structured_summary as the server sent it: the validity
// keys it set survive, and the ones it omitted (a valid stat's rate_valid and
// not_estimable_reason) stay omitted rather than being filled in.
func TestDescribeDryRunJob_StructuredSummaryIsTheWireObject(t *testing.T) {
	summaries := []struct{ name, wire string }{
		{"single all valid", testutil.DryRunSummarySingleAllValid},
		{"single all invalid", testutil.DryRunSummarySingleAllInvalid},
		{"pair independent all invalid", testutil.DryRunSummaryPairIndependentAllInvalid},
	}
	views := []struct {
		args   []string
		format string
		path   []string
	}{
		{[]string{"-v", "-o", "json"}, "json", []string{"status", "structured_summary"}},
		{[]string{"-v", "-o", "yaml"}, "yaml", []string{"status", "structured_summary"}},
		{[]string{"-vvv", "-o", "json"}, "json", []string{"raw_response", "structured_summary"}},
	}
	for _, summary := range summaries {
		for _, view := range views {
			t.Run(summary.name+"/"+strings.Join(view.args, " "), func(t *testing.T) {
				env := testutil.NewTestEnv(t)
				defer env.Cleanup()

				mock := testutil.NewMockAPIServer()
				defer mock.Close()

				endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
				env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
				env.SetupCredential(endpointKey, testOrgID, "test-token")

				job := sampleDryRunJobFull()
				var structured api.DryRunJobFullSchema_StructuredSummary
				if err := structured.UnmarshalJSON([]byte(summary.wire)); err != nil {
					t.Fatal(err)
				}
				job.StructuredSummary = &structured
				mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dry-runs/42", func(w http.ResponseWriter, _ *http.Request) {
					testutil.RespondJSON(w, http.StatusOK, job)
				})

				cmd := setupDryRunJobTestCommand()
				cmd.SetArgs(append([]string{"describe", "dry-run-job", "42"}, view.args...))
				var stdout, stderr bytes.Buffer
				cmd.SetOut(&stdout)
				cmd.SetErr(&stderr)
				if err := cmd.Execute(); err != nil {
					t.Fatalf("Execute() error = %v\n%s", err, stderr.String())
				}

				got := testutil.DecodedValueAt(t, stdout.Bytes(), view.format, view.path...)
				testutil.RequireSameJSONTree(t, got, summary.wire)
			})
		}
	}
}

// -vvv prints a rule param and a produced field's schema holding an integer
// past 2^53 as the server sent it.
func TestDescribeDryRunJob_RawDumpKeepsRuleRunSpecDigits(t *testing.T) {
	job := sampleDryRunJobFull()
	big := json.Number("12345678901234567")
	job.RuleRunSpecs[0].Params = &map[string]interface{}{"threshold": big}
	job.RuleRunSpecs[0].ProducedFields = &[]api.ProducedFieldSpec{{
		RuleFieldName:    "score",
		DatasetFieldName: "score",
		FieldSchema:      map[string]interface{}{"maximum": big},
	}}

	for _, tc := range []struct {
		args []string
		want []string
	}{
		{[]string{"-vvv"}, []string{"threshold: 12345678901234567\n", "maximum: 12345678901234567\n"}},
		{[]string{"-vvv", "-o", "json"}, []string{`"threshold": 12345678901234567`, `"maximum": 12345678901234567`}},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()

			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
			env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
			env.SetupCredential(endpointKey, testOrgID, "test-token")

			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dry-runs/42", func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, job)
			})

			cmd := setupDryRunJobTestCommand()
			cmd.SetArgs(append([]string{"describe", "dry-run-job", "42"}, tc.args...))
			var stdout, stderr bytes.Buffer
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("Execute() error = %v\n%s", err, stderr.String())
			}
			for _, want := range tc.want {
				if !strings.Contains(stdout.String(), want) {
					t.Errorf("output should contain %q, got:\n%s", want, stdout.String())
				}
			}
		})
	}
}

// -v and -vvv print a 7-digit count in metrics and in structured_summary as an
// integer in YAML, and one past 2^53 unrounded in JSON.
func TestDescribeDryRunJob_KeepsIntegers(t *testing.T) {
	job := sampleDryRunJobFull()
	var metrics api.DryRunJobFullSchema_Metrics
	if err := metrics.UnmarshalJSON([]byte(`{"rows_scanned": 1234567, "big_metric": 9007199254740993}`)); err != nil {
		t.Fatal(err)
	}
	var structured api.DryRunJobFullSchema_StructuredSummary
	if err := structured.UnmarshalJSON([]byte(`{"rows_flagged": 1234567, "big_summary": 9007199254740993}`)); err != nil {
		t.Fatal(err)
	}
	job.Metrics = &metrics
	job.StructuredSummary = &structured

	for _, tc := range []struct {
		args []string
		want []string
	}{
		{[]string{"-v"}, []string{"rows_scanned: 1234567\n", "rows_flagged: 1234567\n"}},
		{[]string{"-v", "-o", "json"}, []string{`"big_metric": 9007199254740993`, `"big_summary": 9007199254740993`}},
		{[]string{"-vvv"}, []string{"rows_scanned: 1234567\n", "rows_flagged: 1234567\n"}},
		{[]string{"-vvv", "-o", "json"}, []string{`"big_metric": 9007199254740993`, `"big_summary": 9007199254740993`}},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()

			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
			env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
			env.SetupCredential(endpointKey, testOrgID, "test-token")

			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dry-runs/42", func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, job)
			})

			cmd := setupDryRunJobTestCommand()
			cmd.SetArgs(append([]string{"describe", "dry-run-job", "42"}, tc.args...))
			var stdout, stderr bytes.Buffer
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("Execute() error = %v\n%s", err, stderr.String())
			}
			for _, want := range tc.want {
				if !strings.Contains(stdout.String(), want) {
					t.Errorf("output should contain %q, got:\n%s", want, stdout.String())
				}
			}
		})
	}
}

const (
	dryRunLoadErrorTrace = "Traceback (most recent call last):\n  File \"rule.py\", line 3, in <module>\nNameError: name 'foo' is not defined"
	dryRunLoadErrorJSON  = `[{"position": 0, "rule_revision_id": "0e9d8c7b-6a5f-4e3d-2c1b-a09f8e7d6c5b", "error_type": "NameError", "message": "name 'foo' is not defined"}]`
)

// failedDryRunJobs are the two failure shapes: a hard whole-job failure,
// which carries error_message (backend Markdown, value markers included),
// and a run whose rule failed to load, which carries rule_load_errors.
func failedDryRunJobs() map[string]api.DryRunJobFullSchema {
	crashed := sampleDryRunJobFull()
	crashed.State = "error"
	crashed.Summary = nil
	errorMessage := `Dry run crashed on **orders**: value ~|"~*N/A*~"|~ is not a number`
	crashed.ErrorMessage = &errorMessage

	loadFailed := sampleDryRunJobFull()
	loadFailed.State = "error"
	trace := dryRunLoadErrorTrace
	loadFailed.RuleLoadErrors = &[]api.RuleLoadError{{
		Position:       0,
		RuleRevisionID: "0e9d8c7b-6a5f-4e3d-2c1b-a09f8e7d6c5b",
		ErrorType:      "NameError",
		Message:        "name 'foo' is not defined",
		StackTrace:     &trace,
	}}

	return map[string]api.DryRunJobFullSchema{"error_message": crashed, "rule_load_errors": loadFailed}
}

// runDescribeDryRunJob describes job 42, served as job, with args appended.
func runDescribeDryRunJob(t *testing.T, job api.DryRunJobFullSchema, args ...string) []byte {
	t.Helper()
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dry-runs/42", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, job)
	})

	cmd := setupDryRunJobTestCommand()
	cmd.SetArgs(append([]string{"describe", "dry-run-job", "42"}, args...))
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v\n%s", err, stderr.String())
	}
	return stdout.Bytes()
}

// A failed run says why at the default verbosity, in the default YAML view
// and under -o yaml and -o json: error_message rendered plain like summary,
// and rule_load_errors without their stack traces.
func TestDescribeDryRunJob_FailedRunShowsWhy(t *testing.T) {
	views := []struct {
		name   string
		args   []string
		format string
	}{
		{"default", nil, "yaml"},
		{"yaml", []string{"-o", "yaml"}, "yaml"},
		{"json", []string{"-o", "json"}, "json"},
	}
	jobs := failedDryRunJobs()
	for _, view := range views {
		t.Run("error_message/"+view.name, func(t *testing.T) {
			out := runDescribeDryRunJob(t, jobs["error_message"], view.args...)
			got := testutil.DecodedValueAt(t, out, view.format, "status", "error_message")
			if want := `Dry run crashed on orders: value "N/A" is not a number`; got != want {
				t.Errorf("status.error_message = %q, want %q\n%s", got, want, out)
			}
			status := testutil.DecodedValueAt(t, out, view.format, "status").(map[string]interface{})
			if _, ok := status["rule_load_errors"]; ok {
				t.Errorf("status carries rule_load_errors the job does not have:\n%s", out)
			}
		})
		t.Run("rule_load_errors/"+view.name, func(t *testing.T) {
			out := runDescribeDryRunJob(t, jobs["rule_load_errors"], view.args...)
			testutil.RequireSameJSONTree(t, testutil.DecodedValueAt(t, out, view.format, "status", "rule_load_errors"), dryRunLoadErrorJSON)
			status := testutil.DecodedValueAt(t, out, view.format, "status").(map[string]interface{})
			if _, ok := status["error_message"]; ok {
				t.Errorf("status carries an error_message the job does not have:\n%s", out)
			}
		})
	}
}

// status.snapshot_at is RFC3339 in UTC in every structured view. The fixture's
// +02:00 offset makes a builder that skips the UTC conversion fail too.
func TestDescribeDryRunJob_SnapshotAtIsRFC3339InUTC(t *testing.T) {
	job := sampleDryRunJobFull()
	snapshotAt := time.Date(2025, 6, 1, 14, 0, 0, 0, time.FixedZone("UTC+2", 2*60*60))
	job.SnapshotAt = &snapshotAt

	views := []struct {
		name   string
		format string
		args   []string
	}{
		{"default", "yaml", nil},
		{"yaml", "yaml", []string{"-o", "yaml"}},
		{"json", "json", []string{"-o", "json"}},
	}
	for _, view := range views {
		t.Run(view.name, func(t *testing.T) {
			out := runDescribeDryRunJob(t, job, view.args...)
			if got := testutil.DecodedValueAt(t, out, view.format, "status", "snapshot_at"); got != "2025-06-01T12:00:00Z" {
				t.Errorf("status.snapshot_at = %#v, want 2025-06-01T12:00:00Z\n%s", got, out)
			}
		})
	}
}

// -v adds each rule load error's stack trace.
func TestDescribeDryRunJob_VerboseAddsLoadErrorStackTrace(t *testing.T) {
	for _, format := range []string{"json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			out := runDescribeDryRunJob(t, failedDryRunJobs()["rule_load_errors"], "-v", "-o", format)
			loadErrors := testutil.DecodedValueAt(t, out, format, "status", "rule_load_errors").([]interface{})
			if len(loadErrors) != 1 {
				t.Fatalf("rule_load_errors has %d entries, want 1:\n%s", len(loadErrors), out)
			}
			if got := loadErrors[0].(map[string]interface{})["stack_trace"]; got != dryRunLoadErrorTrace {
				t.Errorf("stack_trace = %q, want %q", got, dryRunLoadErrorTrace)
			}
		})
	}
}

// -o table carries both fields in the status cell, error_message rendered
// inline, and prints the whole error_message as plain text under its own
// heading.
func TestDescribeDryRunJob_TableShowsWhy(t *testing.T) {
	jobs := failedDryRunJobs()

	out := string(runDescribeDryRunJob(t, jobs["error_message"], "-o", "table", "--max-column-width", "2000"))
	if !strings.Contains(out, "\nError Message:\nDry run crashed on orders: value \"N/A\" is not a number\n") {
		t.Errorf("table output has no plain Error Message section:\n%s", out)
	}
	if !strings.Contains(out, `"error_message":`) {
		t.Errorf("table status cell has no error_message:\n%s", out)
	}
	for _, marker := range []string{"~*", "~|", "**"} {
		if strings.Contains(out, marker) {
			t.Errorf("table output retained backend message syntax %q:\n%s", marker, out)
		}
	}

	out = string(runDescribeDryRunJob(t, jobs["rule_load_errors"], "-o", "table", "--max-column-width", "2000"))
	for _, want := range []string{`"rule_load_errors":`, `"error_type":"NameError"`, `"message":"name 'foo' is not defined"`} {
		if !strings.Contains(out, want) {
			t.Errorf("table status cell lacks %s:\n%s", want, out)
		}
	}
	if strings.Contains(out, "stack_trace") || strings.Contains(out, "Error Message:") {
		t.Errorf("table output shows a stack trace or an Error Message section at the default verbosity:\n%s", out)
	}
}

// A run with no error_message and no rule_load_errors, or empty ones, shows
// neither key in any view.
func TestDescribeDryRunJob_HealthyRunOmitsFailureFields(t *testing.T) {
	empty := sampleDryRunJobFull()
	blank := ""
	empty.ErrorMessage = &blank
	empty.RuleLoadErrors = &[]api.RuleLoadError{}
	jobs := map[string]api.DryRunJobFullSchema{"nil": sampleDryRunJobFull(), "empty": empty}

	for name, job := range jobs {
		for _, args := range [][]string{nil, {"-o", "json"}, {"-o", "table"}, {"-v"}} {
			t.Run(name+"/"+strings.Join(args, " "), func(t *testing.T) {
				out := string(runDescribeDryRunJob(t, job, args...))
				for _, absent := range []string{"error_message", "rule_load_errors", "Error Message"} {
					if strings.Contains(out, absent) {
						t.Errorf("output names %q for a run that carries none:\n%s", absent, out)
					}
				}
			})
		}
	}
}
