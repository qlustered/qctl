package inspect

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const testOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

func setupInspectTestCommand() *cobra.Command {
	rootCmd := &cobra.Command{Use: "qctl"}
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "json", "output format")
	rootCmd.PersistentFlags().Bool("no-headers", false, "")
	rootCmd.PersistentFlags().String("columns", "", "")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "")
	rootCmd.PersistentFlags().CountP("verbose", "v", "Verbosity level")

	inspectCmd := NewCommand()
	rootCmd.AddCommand(inspectCmd)
	return rootCmd
}

func TestInspectDryRunJob_CompactView(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dry-runs/42/preview/compact", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.CompactDryRunPreviewResponse{
			DryRunJobID: 42,
			State:       "finished",
			Stats: api.DryRunComparisonStats{
				TotalRowsProcessed:  100,
				RowsWithStateChange: 5,
				RowsWithValueChange: 3,
			},
			Repro: api.AgentReproMetadata{
				SnapshotID: 1,
			},
		})
	})

	cmd := setupInspectTestCommand()
	cmd.SetArgs([]string{"inspect", "dry-run-job", "42"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "42") {
		t.Errorf("output should contain dry_run_job_id 42, got: %s", output)
	}
	if !strings.Contains(output, "100") {
		t.Errorf("output should contain total_rows_processed 100, got: %s", output)
	}
}

func TestInspectDryRunJob_FullView(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dry-runs/42/preview", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.DryRunJobPreviewResponse{
			DryRunJobID: 42,
			State:       "finished",
			AllFields:   []string{"name", "email"},
			Stats: api.DryRunComparisonStats{
				TotalRowsProcessed: 50,
			},
		})
	})

	cmd := setupInspectTestCommand()
	cmd.SetArgs([]string{"inspect", "dry-run-job", "42", "--view", "full"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "name") {
		t.Errorf("output should contain field 'name', got: %s", output)
	}
	if !strings.Contains(output, "email") {
		t.Errorf("output should contain field 'email', got: %s", output)
	}
}

func TestInspectDryRunJob_YAMLOutput(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dry-runs/42/preview/compact", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.CompactDryRunPreviewResponse{
			DryRunJobID: 42,
			State:       "finished",
			Stats: api.DryRunComparisonStats{
				TotalRowsProcessed: 100,
			},
			Repro: api.AgentReproMetadata{
				SnapshotID: 1,
			},
		})
	})

	cmd := setupInspectTestCommand()
	cmd.SetArgs([]string{"inspect", "dry-run-job", "42", "-o", "yaml"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "dry_run_job_id: 42") {
		t.Errorf("YAML output should contain dry_run_job_id, got: %s", output)
	}
}

func TestInspectDryRunJob_InvalidID(t *testing.T) {
	cmd := setupInspectTestCommand()
	cmd.SetArgs([]string{"inspect", "dry-run-job", "abc"})

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

func TestInspectDryRunJob_NoArgs(t *testing.T) {
	cmd := setupInspectTestCommand()
	cmd.SetArgs([]string{"inspect", "dry-run-job"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for no args")
	}
}

func TestInspectDryRunJob_NotFound(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dry-runs/999/preview/compact", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondError(w, http.StatusNotFound, "Not found")
	})

	cmd := setupInspectTestCommand()
	cmd.SetArgs([]string{"inspect", "dry-run-job", "999"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for not found")
	}
}

// The compact view prints structured_summary as the server sent it: the
// validity keys it set survive, and the ones it omitted (a valid stat's
// rate_valid and not_estimable_reason) stay omitted rather than being filled in.
func TestInspectDryRunJob_CompactStructuredSummaryIsTheWireObject(t *testing.T) {
	summaries := []struct{ name, wire string }{
		{"single all valid", testutil.DryRunSummarySingleAllValid},
		{"single all invalid", testutil.DryRunSummarySingleAllInvalid},
		{"pair independent all invalid", testutil.DryRunSummaryPairIndependentAllInvalid},
	}
	for _, summary := range summaries {
		for _, format := range []string{"json", "yaml"} {
			t.Run(summary.name+"/"+format, func(t *testing.T) {
				env := testutil.NewTestEnv(t)
				defer env.Cleanup()

				mock := testutil.NewMockAPIServer()
				defer mock.Close()

				endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
				env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
				env.SetupCredential(endpointKey, testOrgID, "test-token")

				var structured api.CompactDryRunPreviewResponse_StructuredSummary
				if err := structured.UnmarshalJSON([]byte(summary.wire)); err != nil {
					t.Fatal(err)
				}
				mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dry-runs/42/preview/compact", func(w http.ResponseWriter, _ *http.Request) {
					testutil.RespondJSON(w, http.StatusOK, api.CompactDryRunPreviewResponse{
						DryRunJobID:       42,
						State:             "finished",
						StructuredSummary: &structured,
						Repro:             api.AgentReproMetadata{SnapshotID: 1},
					})
				})

				cmd := setupInspectTestCommand()
				cmd.SetArgs([]string{"inspect", "dry-run-job", "42", "--view", "compact", "-o", format})
				var stdout, stderr bytes.Buffer
				cmd.SetOut(&stdout)
				cmd.SetErr(&stderr)
				if err := cmd.Execute(); err != nil {
					t.Fatalf("Execute() error = %v\n%s", err, stderr.String())
				}

				got := testutil.DecodedValueAt(t, stdout.Bytes(), format, "structured_summary")
				testutil.RequireSameJSONTree(t, got, summary.wire)
			})
		}
	}
}

// Cell values print with the digits the server sent, including an integer
// past 2^53, in both views and both output formats.
func TestInspectDryRunJob_CellValuesKeepTheirDigits(t *testing.T) {
	const (
		big   = "12345678901234567"
		small = "1234567"
		// The float64 nearest to big.
		rounded = "12345678901234568"
	)
	views := []struct {
		name, path, body string
		args             []string
	}{
		{
			name: "full",
			path: "/api/orgs/" + testOrgID + "/dry-runs/42/preview",
			args: []string{"--view", "full"},
			body: `{"dry_run_job_id":42,"state":"finished","all_fields":["account_id","quantity"],` +
				`"stats":{"total_rows_processed":1},"results":[{"initial_signature_":"sig","diff":{},` +
				`"left":{"annotations":{},"state":"clean","values":{"account_id":` + big + `,"quantity":` + small + `}},` +
				`"right":{"annotations":{},"state":"clean","values":{"account_id":` + big + `,"quantity":` + small + `}}}]}`,
		},
		{
			name: "compact",
			path: "/api/orgs/" + testOrgID + "/dry-runs/42/preview/compact",
			body: `{"dry_run_job_id":42,"state":"finished","stats":{"total_rows_processed":1},` +
				`"repro":{"snapshot_id":1},"exemplars":[{"initial_signature_":"sig","is_state_flip":false,` +
				`"left_state":"clean","right_state":"clean","changed_fields":[{"field":"account_id",` +
				`"before":` + small + `,"after":` + big + `,"alert_changed":false,"value_changed":true,"warning_changed":false}],` +
				`"right_issues":{"field_values":{"account_id":` + big + `,"quantity":` + small + `}}}]}`,
		},
	}
	formats := []struct{ name, bigLine, smallLine string }{
		{"json", `"account_id": ` + big, `"quantity": ` + small},
		{"yaml", `account_id: ` + big, `quantity: ` + small},
	}
	for _, view := range views {
		for _, format := range formats {
			t.Run(view.name+"/"+format.name, func(t *testing.T) {
				env := testutil.NewTestEnv(t)
				defer env.Cleanup()

				mock := testutil.NewMockAPIServer()
				defer mock.Close()

				endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
				env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
				env.SetupCredential(endpointKey, testOrgID, "test-token")

				mock.RegisterHandler("GET", view.path, func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write([]byte(view.body))
				})

				cmd := setupInspectTestCommand()
				cmd.SetArgs(append([]string{"inspect", "dry-run-job", "42", "-o", format.name}, view.args...))
				var stdout, stderr bytes.Buffer
				cmd.SetOut(&stdout)
				cmd.SetErr(&stderr)
				if err := cmd.Execute(); err != nil {
					t.Fatalf("Execute() error = %v\n%s", err, stderr.String())
				}

				out := stdout.String()
				if strings.Contains(out, rounded) {
					t.Errorf("output holds the rounded %s:\n%s", rounded, out)
				}
				for _, want := range []string{format.bigLine, format.smallLine} {
					if !strings.Contains(out, want) {
						t.Errorf("output lacks %q:\n%s", want, out)
					}
				}
			})
		}
	}
}
