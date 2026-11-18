package get

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const testDryRunOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

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

	getCmd := &cobra.Command{Use: "get", Short: "Get resources"}
	getCmd.AddCommand(NewDryRunJobCommand())
	getCmd.AddCommand(NewDryRunJobsCommand())
	rootCmd.AddCommand(getCmd)
	return rootCmd
}

func TestGetDryRunJob_Success(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testDryRunOrgID)
	env.SetupCredential(endpointKey, testDryRunOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testDryRunOrgID+"/dry-runs/42", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"id": 42, "dataset_id": 10, "state": "finished",
			"rule_run_specs": []map[string]interface{}{{"position": 0, "dataset_rule_id": nil}},
			"sampling":       map[string]interface{}{"alpha_pref": 0, "cap": 0, "eligible_c": 0, "eligible_q": 0, "pins_inserted": 0, "pins_missing": 0, "pins_truncated": 0},
			"metrics":        nil,
		})
	})

	cmd := setupDryRunJobTestCommand()
	cmd.SetArgs([]string{"get", "dry-run-job", "42", "-o", "json"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, `"id": 42`) {
		t.Errorf("output should contain id 42, got: %s", output)
	}
	if !strings.Contains(output, `"state": "finished"`) {
		t.Errorf("output should contain state finished, got: %s", output)
	}
}

func TestGetDryRunJob_StructuredSummaryIsTheWireObject(t *testing.T) {
	summaries := []struct{ name, wire string }{
		{"single all valid", testutil.DryRunSummarySingleAllValid},
		{"single all invalid", testutil.DryRunSummarySingleAllInvalid},
		{"pair independent all invalid", testutil.DryRunSummaryPairIndependentAllInvalid},
	}
	for _, summary := range summaries {
		for _, format := range []string{"json", "yaml"} {
			t.Run(summary.name+"/"+format, func(t *testing.T) {
				stdout := runGetDryRunJob(t, format, map[string]interface{}{
					"id": 42, "dataset_id": 10, "state": "finished",
					"rule_run_specs":     []map[string]interface{}{{"position": 0, "dataset_rule_id": nil}},
					"sampling":           map[string]interface{}{"alpha_pref": 0, "cap": 0, "eligible_c": 0, "eligible_q": 0, "pins_inserted": 0, "pins_missing": 0, "pins_truncated": 0},
					"metrics":            nil,
					"structured_summary": json.RawMessage(summary.wire),
				})
				got := testutil.DecodedValueAt(t, stdout, format, "structured_summary")
				testutil.RequireSameJSONTree(t, got, summary.wire)
			})
		}
	}
}

func TestGetDryRunJob_MetricsAndRuleRunSpecIDsAreTheWireValues(t *testing.T) {
	const (
		metrics       = `{"total_eligible": 1000, "clean": {"rate": 1.0, "count": 1000}}`
		datasetRuleID = "5f0c8a52-3b9e-4c1d-9a7e-2d4b6f8e1a3c"
		revisionID    = "0e9d8c7b-6a5f-4e3d-2c1b-a09f8e7d6c5b"
	)
	for _, format := range []string{"json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			stdout := runGetDryRunJob(t, format, map[string]interface{}{
				"id": 42, "dataset_id": 10, "state": "finished",
				"rule_run_specs": []map[string]interface{}{{
					"position": 0, "dataset_rule_id": datasetRuleID, "rule_revision_id": revisionID,
					"row_filter": nil,
				}},
				"sampling": map[string]interface{}{"alpha_pref": 0, "cap": 0, "eligible_c": 0, "eligible_q": 0, "pins_inserted": 0, "pins_missing": 0, "pins_truncated": 0},
				"metrics":  json.RawMessage(metrics),
			})
			testutil.RequireSameJSONTree(t, testutil.DecodedValueAt(t, stdout, format, "rule_run_specs"),
				`[{"position": 0, "dataset_rule_id": "`+datasetRuleID+`", "dataset_rule_suggestion_id": null, "rule_revision_id": "`+revisionID+`", "row_filter": null}]`)
			testutil.RequireSameJSONTree(t, testutil.DecodedValueAt(t, stdout, format, "metrics"), metrics)
		})
	}
}

// A count in metrics prints as the integer the server sent: a 7-digit one in
// YAML, and one past 2^53 unrounded in JSON.
func TestGetDryRunJob_MetricsKeepIntegers(t *testing.T) {
	body := map[string]interface{}{
		"id": 42, "dataset_id": 10, "state": "finished",
		"rule_run_specs": []map[string]interface{}{{"position": 0, "dataset_rule_id": nil}},
		"sampling":       map[string]interface{}{"alpha_pref": 0, "cap": 0, "eligible_c": 0, "eligible_q": 0, "pins_inserted": 0, "pins_missing": 0, "pins_truncated": 0},
		"metrics":        json.RawMessage(`{"total_eligible": 1234567, "clean": {"count": 9007199254740993}}`),
	}
	if out := runGetDryRunJob(t, "yaml", body); !strings.Contains(string(out), "total_eligible: 1234567\n") {
		t.Errorf("-o yaml should print total_eligible: 1234567, got:\n%s", out)
	}
	if out := runGetDryRunJob(t, "json", body); !strings.Contains(string(out), `"count": 9007199254740993`) {
		t.Errorf("-o json should print count: 9007199254740993, got:\n%s", out)
	}
}

// A rule param and a produced field's schema print an integer past 2^53 as the
// server sent it.
func TestGetDryRunJob_RuleRunSpecsKeepTheirDigits(t *testing.T) {
	body := map[string]interface{}{
		"id": 42, "dataset_id": 10, "state": "finished",
		"rule_run_specs": json.RawMessage(`[{"position": 0, "dataset_rule_id": null,
			"params": {"threshold": 12345678901234567},
			"produced_fields": [{"rule_field_name": "score", "dataset_field_name": "score",
				"field_schema": {"maximum": 12345678901234567}}]}]`),
		"sampling": map[string]interface{}{"alpha_pref": 0, "cap": 0, "eligible_c": 0, "eligible_q": 0, "pins_inserted": 0, "pins_missing": 0, "pins_truncated": 0},
		"metrics":  nil,
	}
	for _, tc := range []struct{ format, param, schema string }{
		{"json", `"threshold": 12345678901234567`, `"maximum": 12345678901234567`},
		{"yaml", "threshold: 12345678901234567\n", "maximum: 12345678901234567\n"},
	} {
		t.Run(tc.format, func(t *testing.T) {
			out := string(runGetDryRunJob(t, tc.format, body))
			for _, want := range []string{tc.param, tc.schema} {
				if !strings.Contains(out, want) {
					t.Errorf("-o %s should contain %q, got:\n%s", tc.format, want, out)
				}
			}
		})
	}
}

// get dry-run-job prints the job record itself, so a failed run's
// error_message and rule_load_errors reach -o json and -o yaml as the server
// sent them, stack traces included.
func TestGetDryRunJob_CarriesFailureDetail(t *testing.T) {
	const loadErrors = `[{"position": 0, "rule_revision_id": "0e9d8c7b-6a5f-4e3d-2c1b-a09f8e7d6c5b",
		"error_type": "NameError", "message": "name 'foo' is not defined", "stack_trace": "Traceback ..."}]`
	body := map[string]interface{}{
		"id": 42, "dataset_id": 10, "state": "error",
		"rule_run_specs":   []map[string]interface{}{{"position": 0, "dataset_rule_id": nil}},
		"sampling":         map[string]interface{}{"alpha_pref": 0, "cap": 0, "eligible_c": 0, "eligible_q": 0, "pins_inserted": 0, "pins_missing": 0, "pins_truncated": 0},
		"metrics":          nil,
		"error_message":    "Dry run crashed on **orders**",
		"rule_load_errors": json.RawMessage(loadErrors),
	}
	for _, format := range []string{"json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			out := runGetDryRunJob(t, format, body)
			if got := testutil.DecodedValueAt(t, out, format, "error_message"); got != "Dry run crashed on **orders**" {
				t.Errorf("error_message = %q, want the wire value", got)
			}
			testutil.RequireSameJSONTree(t, testutil.DecodedValueAt(t, out, format, "rule_load_errors"), loadErrors)
		})
	}
}

// runGetDryRunJob runs `get dry-run-job 42 -o format` against a server that
// answers with body, and returns stdout.
func runGetDryRunJob(t *testing.T, format string, body map[string]interface{}) []byte {
	t.Helper()
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testDryRunOrgID)
	env.SetupCredential(endpointKey, testDryRunOrgID, "test-token")
	mock.RegisterHandler("GET", "/api/orgs/"+testDryRunOrgID+"/dry-runs/42", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, body)
	})

	cmd := setupDryRunJobTestCommand()
	cmd.SetArgs([]string{"get", "dry-run-job", "42", "-o", format})
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v\n%s", err, stderr.String())
	}
	return stdout.Bytes()
}

func TestGetDryRunJob_InvalidID(t *testing.T) {
	cmd := setupDryRunJobTestCommand()
	cmd.SetArgs([]string{"get", "dry-run-job", "abc"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for invalid ID")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
}

func TestGetDryRunJob_NoArgs(t *testing.T) {
	cmd := setupDryRunJobTestCommand()
	cmd.SetArgs([]string{"get", "dry-run-job"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for no args")
	}
}

func TestGetDryRunJobs_Success(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testDryRunOrgID)
	env.SetupCredential(endpointKey, testDryRunOrgID, "test-token")

	// Mock table lookup
	mock.RegisterHandler("GET", "/api/orgs/"+testDryRunOrgID+"/datasets/10", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"id": 10, "name": "test_table",
		})
	})

	// Mock dry-run jobs list
	mock.RegisterHandler("GET", "/api/orgs/"+testDryRunOrgID+"/datasets/10/dry-runs", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"results": []map[string]interface{}{
				{"id": 1, "dataset_id": 10, "dataset_name": "test_table", "state": "finished"},
				{"id": 2, "dataset_id": 10, "dataset_name": "test_table", "state": "running"},
			},
		})
	})

	cmd := setupDryRunJobTestCommand()
	cmd.SetArgs([]string{"get", "dry-run-jobs", "--table-id", "10", "-o", "json"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "finished") {
		t.Errorf("output should contain state 'finished', got: %s", output)
	}
}

func TestGetDryRunJobs_MissingTableFlag(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testDryRunOrgID)
	env.SetupCredential(endpointKey, testDryRunOrgID, "test-token")

	cmd := setupDryRunJobTestCommand()
	cmd.SetArgs([]string{"get", "dry-run-jobs"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing table flag")
	}
	// Test the code, not the text: the message now names the positional form
	// too (see TestGetDryRunJobs_NoSelectorMentionsThePositional), so this
	// only pins the exit code — a client-side usage error.
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
}

// A table is required here, so --table-id 0 already failed — but through
// ResolveTable's default branch, which told a user who had just typed
// --table-id that they must specify --table-id. The command now names the flag
// they actually got wrong, and does it before any request.
func TestGetDryRunJobs_RejectsNonPositiveTableID(t *testing.T) {
	for _, value := range []string{"0", "-2"} {
		t.Run(value, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()

			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
			env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testDryRunOrgID)
			env.SetupCredential(endpointKey, testDryRunOrgID, "test-token")

			calls := 0
			mock.RegisterHandler("GET", "/api/orgs/"+testDryRunOrgID+"/datasets", func(w http.ResponseWriter, _ *http.Request) {
				calls++
				testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
					"results": []interface{}{}, "total_rows": 0, "page": 1,
				})
			})

			cmd := setupDryRunJobTestCommand()
			cmd.SetArgs([]string{"get", "dry-run-jobs", "--table-id", value})

			var buf bytes.Buffer
			cmd.SetOut(&buf)
			cmd.SetErr(&buf)

			err := cmd.Execute()
			if err == nil {
				t.Fatalf("expected an error for --table-id %s", value)
			}
			if !strings.Contains(err.Error(), "--table-id must be a positive integer") {
				t.Errorf("error should name the flag and the rule, got: %v", err)
			}
			if strings.Contains(err.Error(), "must specify") {
				t.Errorf("error must not tell the user to pass the flag they just passed, got: %v", err)
			}
			if calls != 0 {
				t.Errorf("expected /datasets never to be called, got %d call(s)", calls)
			}
		})
	}
}

// writeDryRunJobFrames hands each websocket frame to --watch's writer and
// returns stdout, stderr and the state each frame reported.
func writeDryRunJobFrames(t *testing.T, format string, frames ...string) (string, string, []string) {
	t.Helper()
	var out, errOut bytes.Buffer
	cmd := &cobra.Command{}
	cmd.Flags().String("output", format, "")
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	write, err := newDryRunJobFrameWriter(cmd, 42)
	if err != nil {
		t.Fatalf("newDryRunJobFrameWriter: %v", err)
	}
	var states []string
	for _, frame := range frames {
		state, err := write(json.RawMessage(frame))
		if err != nil {
			t.Fatalf("write(%s): %v", frame, err)
		}
		states = append(states, state)
	}
	return out.String(), errOut.String(), states
}

// Each format writes one record per frame: a compact line in json, a document
// in yaml, the job id in name, and a state line in the default view.
func TestDryRunJobWatchFramesPerFormat(t *testing.T) {
	frames := []string{`{"state": "running"}`, `{"state": "finished"}`}
	tests := []struct {
		format string
		want   string
	}{
		{"json", "{\"state\":\"running\"}\n{\"state\":\"finished\"}\n"},
		{"yaml", "---\nstate: running\n---\nstate: finished\n"},
		{"name", "dry-run-job/42\ndry-run-job/42\n"},
		{"table", "dry-run-job 42: running\ndry-run-job 42: finished\n"},
	}
	for _, tt := range tests {
		out, _, states := writeDryRunJobFrames(t, tt.format, frames...)
		if out != tt.want {
			t.Errorf("-o %s wrote %q, want %q", tt.format, out, tt.want)
		}
		if strings.Join(states, ",") != "running,finished" {
			t.Errorf("-o %s states = %v, want running,finished", tt.format, states)
		}
	}
}

// A frame that is not JSON is reported on stderr and skipped, in every format.
func TestDryRunJobWatchSkipsAMalformedFrame(t *testing.T) {
	for _, format := range []string{"json", "yaml", "name", "table"} {
		out, errOut, states := writeDryRunJobFrames(t, format, `{not json`, `{"state": "running"}`)
		if !strings.Contains(errOut, "warning:") {
			t.Errorf("-o %s: stderr = %q, want a warning", format, errOut)
		}
		if states[0] != "" || states[1] != "running" || strings.Contains(out, "not json") {
			t.Errorf("-o %s: states = %v, out = %q; want the bad frame skipped", format, states, out)
		}
	}
}
