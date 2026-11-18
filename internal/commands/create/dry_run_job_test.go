package create

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/rule_versions"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const testOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

func setupCreateTestCommand() *cobra.Command {
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

	createCmd := NewCommand()
	rootCmd.AddCommand(createCmd)
	return rootCmd
}

func writeSpecFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "spec.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write spec file: %v", err)
	}
	return path
}

func TestCreateDryRunJob_WithExplicitCloudSourceID(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	// Mock cloud source lookup (no table needed when cloud-source-id is given)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/data-sources/456", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"id": 456, "name": "test_source",
		})
	})

	// Mock dry-run launch
	mock.RegisterHandler("PUT", "/api/orgs/"+testOrgID+"/dry-runs", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"dry_run_job_id": 99,
		})
	})

	specFile := writeSpecFile(t, `apiVersion: qluster.ai/v1
kind: DryRunJob
metadata: {}
spec:
  rule_run_specs:
    - dataset_rule_id: "550e8400-e29b-41d4-a716-446655440099"
`)

	cmd := setupCreateTestCommand()
	cmd.SetArgs([]string{"create", "dry-run-job", "-f", specFile, "--cloud-source-id", "456"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "99") {
		t.Errorf("output should contain dry_run_job_id 99, got: %s", output)
	}
}

func TestCreateDryRunJob_WithAutoDetectedCloudSource(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	// Mock table lookup
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/123", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"id": 123, "name": "test_table",
		})
	})

	// Mock cloud source list (exactly 1 for auto-detection)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/data-sources", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"results": []map[string]interface{}{
				{"id": 456, "name": "auto_source"},
			},
		})
	})

	// Mock dry-run launch
	mock.RegisterHandler("PUT", "/api/orgs/"+testOrgID+"/dry-runs", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"dry_run_job_id": 88,
		})
	})

	specFile := writeSpecFile(t, `apiVersion: qluster.ai/v1
kind: DryRunJob
metadata:
  table_id: 123
spec:
  rule_run_specs:
    - dataset_rule_id: "550e8400-e29b-41d4-a716-446655440099"
`)

	cmd := setupCreateTestCommand()
	cmd.SetArgs([]string{"create", "dry-run-job", "-f", specFile})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "88") {
		t.Errorf("output should contain dry_run_job_id 88, got: %s", output)
	}
}

func TestCreateDryRunJob_MultipleCloudSourcesExitTwo(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/123", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{"id": 123, "name": "test_table"})
	})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/data-sources", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"results": []map[string]interface{}{
				{"id": 456, "name": "source_a"},
				{"id": 457, "name": "source_b"},
			},
		})
	})

	specFile := writeSpecFile(t, `apiVersion: qluster.ai/v1
kind: DryRunJob
metadata:
  table_id: 123
spec:
  rule_run_specs:
    - dataset_rule_id: "550e8400-e29b-41d4-a716-446655440099"
`)

	cmd := setupCreateTestCommand()
	cmd.SetArgs([]string{"create", "dry-run-job", "-f", specFile})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "multiple cloud sources") {
		t.Fatalf("err = %v, want multiple-cloud-sources refusal", err)
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
}

func TestCreateDryRunJob_MissingFile(t *testing.T) {
	cmd := setupCreateTestCommand()
	cmd.SetArgs([]string{"create", "dry-run-job"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing --file flag")
	}

	if !strings.Contains(err.Error(), "--file is required") {
		t.Errorf("error = %q", err.Error())
	}

	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
}

func TestCreateDryRunJob_RuleNameResolution(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	ruleRevisionID := "550e8400-e29b-41d4-a716-446655440099"

	// Mock rule revisions list (for name resolution)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions", func(w http.ResponseWriter, r *http.Request) {
		totalRows := 1
		page := 1
		response := rule_versions.RuleRevisionList{
			Results: []rule_versions.RuleRevisionTiny{
				testutil.MakeRuleRevisionTiny(ruleRevisionID, "email_validator", "1.0.0", "enabled"),
			},
			TotalRows: &totalRows,
			Page:      &page,
		}
		testutil.RespondJSON(w, http.StatusOK, response)
	})

	// Mock cloud source lookup
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/data-sources/456", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"id": 456, "name": "test_source",
		})
	})

	// Mock dry-run launch
	mock.RegisterHandler("PUT", "/api/orgs/"+testOrgID+"/dry-runs", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"dry_run_job_id": 77,
		})
	})

	specFile := writeSpecFile(t, `apiVersion: qluster.ai/v1
kind: DryRunJob
metadata: {}
spec:
  rule_run_specs:
    - rule: email_validator
      release: "1.0.0"
`)

	cmd := setupCreateTestCommand()
	cmd.SetArgs([]string{"create", "dry-run-job", "-f", specFile, "--cloud-source-id", "456"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "77") {
		t.Errorf("output should contain dry_run_job_id 77, got: %s", output)
	}
}

func TestCreateDryRunJob_RuleNameWithoutRelease(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	ruleRevisionID := "550e8400-e29b-41d4-a716-446655440099"

	// Mock rule revisions list (single release — no release filter needed)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions", func(w http.ResponseWriter, r *http.Request) {
		totalRows := 1
		page := 1
		response := rule_versions.RuleRevisionList{
			Results: []rule_versions.RuleRevisionTiny{
				testutil.MakeRuleRevisionTiny(ruleRevisionID, "email_validator", "1.0.0", "enabled"),
			},
			TotalRows: &totalRows,
			Page:      &page,
		}
		testutil.RespondJSON(w, http.StatusOK, response)
	})

	// Mock cloud source lookup
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/data-sources/456", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"id": 456, "name": "test_source",
		})
	})

	// Mock dry-run launch
	mock.RegisterHandler("PUT", "/api/orgs/"+testOrgID+"/dry-runs", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"dry_run_job_id": 76,
		})
	})

	specFile := writeSpecFile(t, `apiVersion: qluster.ai/v1
kind: DryRunJob
metadata: {}
spec:
  rule_run_specs:
    - rule: email_validator
`)

	cmd := setupCreateTestCommand()
	cmd.SetArgs([]string{"create", "dry-run-job", "-f", specFile, "--cloud-source-id", "456"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "76") {
		t.Errorf("output should contain dry_run_job_id 76, got: %s", output)
	}
}

func TestCreateDryRunJob_ShortIDResolution(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	ruleRevisionID := "550e8400-e29b-41d4-a716-446655440099"

	// Mock rule revisions list (for short ID resolution)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions", func(w http.ResponseWriter, r *http.Request) {
		totalRows := 1
		page := 1
		response := rule_versions.RuleRevisionList{
			Results: []rule_versions.RuleRevisionTiny{
				testutil.MakeRuleRevisionTiny(ruleRevisionID, "email_validator", "1.0.0", "enabled"),
			},
			TotalRows: &totalRows,
			Page:      &page,
		}
		testutil.RespondJSON(w, http.StatusOK, response)
	})

	// Mock cloud source lookup
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/data-sources/456", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"id": 456, "name": "test_source",
		})
	})

	// Mock dry-run launch
	mock.RegisterHandler("PUT", "/api/orgs/"+testOrgID+"/dry-runs", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"dry_run_job_id": 75,
		})
	})

	specFile := writeSpecFile(t, `apiVersion: qluster.ai/v1
kind: DryRunJob
metadata: {}
spec:
  rule_run_specs:
    - rule_revision_id: "550e8400"
`)

	cmd := setupCreateTestCommand()
	cmd.SetArgs([]string{"create", "dry-run-job", "-f", specFile, "--cloud-source-id", "456"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "75") {
		t.Errorf("output should contain dry_run_job_id 75, got: %s", output)
	}
}

// ResolveTable already returned the table's real name over the wire; passing
// the ID back in its place produced "not found for table '123'", which reads as
// a table literally named "123".
func TestCreateDryRunJob_CloudSourceErrorNamesTheTable(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/123", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{"id": 123, "name": "test_table"})
	})

	// The table has a cloud source, just not the one the spec asks for.
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/data-sources", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"results": []map[string]interface{}{{"id": 456, "name": "other_source"}},
		})
	})

	specFile := writeSpecFile(t, `apiVersion: qluster.ai/v1
kind: DryRunJob
metadata:
  table_id: 123
  cloud_source: missing_source
spec:
  rule_run_specs:
    - dataset_rule_id: "550e8400-e29b-41d4-a716-446655440099"
`)

	cmd := setupCreateTestCommand()
	cmd.SetArgs([]string{"create", "dry-run-job", "-f", specFile})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected an error for a cloud source name that matches nothing")
	}
	if !strings.Contains(err.Error(), "table 'test_table'") {
		t.Errorf("error should name the table, got: %v", err)
	}
	if strings.Contains(err.Error(), "'123'") {
		t.Errorf("error passes the table ID off as a name: %v", err)
	}
}

// The flag overrides the spec's metadata, so a bad flag value must fail loudly
// rather than fall through to the spec (silently launching against a different
// table) or reach ResolveTable's "must specify either --table-id or --table".
func TestCreateDryRunJob_RejectsNonPositiveTableID(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	launched := 0
	mock.RegisterHandler("PUT", "/api/orgs/"+testOrgID+"/dry-runs", func(w http.ResponseWriter, _ *http.Request) {
		launched++
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{"dry_run_job_id": 99})
	})

	specFile := writeSpecFile(t, `apiVersion: qluster.ai/v1
kind: DryRunJob
metadata:
  table_id: 10
spec:
  rule_run_specs:
    - dataset_rule_id: "550e8400-e29b-41d4-a716-446655440099"
`)

	cmd := setupCreateTestCommand()
	cmd.SetArgs([]string{"create", "dry-run-job", "-f", specFile, "--table-id", "0"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected an error for --table-id 0")
	}
	if !strings.Contains(err.Error(), "--table-id must be a positive integer") {
		t.Errorf("error should name the flag and the rule, got: %v", err)
	}
	if launched != 0 {
		t.Errorf("expected no dry-run launch, got %d", launched)
	}
}

func TestCreateDryRunJob_InvalidSpecFile(t *testing.T) {
	specFile := writeSpecFile(t, `apiVersion: v2
kind: Wrong
metadata: {}
spec:
  rule_run_specs: []
`)

	cmd := setupCreateTestCommand()
	cmd.SetArgs([]string{"create", "dry-run-job", "-f", specFile})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for invalid spec file")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
}

// stubThrottleSleep drives the shared retry clock so the backoff schedule costs
// no wall time, and records what the code would have waited.
func stubThrottleSleep(t *testing.T) *[]time.Duration {
	t.Helper()
	var slept []time.Duration
	cmdutil.SetThrottleSleep(func(d time.Duration) { slept = append(slept, d) })
	t.Cleanup(cmdutil.ResetThrottleSleep)
	return &slept
}

// setupLaunchEnv wires the config, credential and cloud-source lookup that every
// launch test needs; the caller registers its own PUT /dry-runs handler.
func setupLaunchEnv(t *testing.T) (*testutil.TestEnv, *testutil.MockAPIServer) {
	t.Helper()
	env := testutil.NewTestEnv(t)
	mock := testutil.NewMockAPIServer()
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/data-sources/456", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{"id": 456, "name": "test_source"})
	})
	return env, mock
}

// execCreate runs the command with stdout and stderr kept apart, which is the
// point of these tests: stdout is a JSON document a script parses.
func execCreate(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := setupCreateTestCommand()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return outBuf.String(), errBuf.String(), err
}

func throttleSpecFile(t *testing.T) string {
	t.Helper()
	return writeSpecFile(t, `apiVersion: qluster.ai/v1
kind: DryRunJob
metadata: {}
spec:
  rule_run_specs:
    - dataset_rule_id: "550e8400-e29b-41d4-a716-446655440099"
`)
}

// The ticket's case: the user already has three dry runs EXECUTING on other
// sources, so the backend refuses this launch until one drains. Waiting and
// re-sending the identical request is what a human would do; the command does
// it for them (atlas create_and_run_dry_run's check_dry_run_rate_limit call,
// capi/capi/controllers/dry_run_controller.py). This
// mock sends no Retry-After, so it exercises the computed-backoff fallback --
// TestCreateDryRunJob_HonoursServerRetryAfter covers the header.
func TestCreateDryRunJob_RetriesAThrottledLaunch(t *testing.T) {
	env, mock := setupLaunchEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	slept := stubThrottleSleep(t)

	calls := 0
	mock.RegisterHandler("PUT", "/api/orgs/"+testOrgID+"/dry-runs", func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls <= 2 {
			testutil.RespondJSON(w, http.StatusTooManyRequests, map[string]interface{}{
				"detail": "You have 3 dry run(s) in progress (limit: 3). Please wait for an existing dry run to complete before starting a new one.",
			})
			return
		}
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{"dry_run_job_id": 99})
	})

	stdout, stderr, err := execCreate(t, "create", "dry-run-job", "-f", throttleSpecFile(t), "--cloud-source-id", "456")
	if err != nil {
		t.Fatalf("expected the retry to succeed, got: %v\nstderr:\n%s", err, stderr)
	}
	if calls != 3 {
		t.Fatalf("expected 3 launch attempts, got %d", calls)
	}
	if len(*slept) != 2 || (*slept)[0] != 2*time.Second || (*slept)[1] != 4*time.Second {
		t.Fatalf("backoff schedule = %v, want [2s 4s]", *slept)
	}
	// The delay must be explained, and explained somewhere that is not the
	// machine-readable stream.
	if !strings.Contains(stderr, "throttling") {
		t.Errorf("expected the wait to be explained on stderr, got:\n%s", stderr)
	}
	if strings.Contains(stdout, "throttling") {
		t.Errorf("progress prose leaked into stdout, which is parsed as JSON:\n%s", stdout)
	}
	var payload map[string]int
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("stdout is not a single JSON object (%v):\n%s", err, stdout)
	}
	if payload["dry_run_job_id"] != 99 {
		t.Errorf("dry_run_job_id = %d, want 99", payload["dry_run_job_id"])
	}
}

// The client-level test (TestLaunchDryRunJob_ThrottleCarriesRetryAfter) proves
// a 429's Retry-After header survives into *apierror.ThrottleError.RetryAfter,
// and the helper-level test (TestRetryOnThrottle_HonoursAnInRangeRetryAfter)
// proves RetryOnThrottle sleeps that value verbatim when in range. Neither
// proves the two are actually wired together end-to-end through this command:
// TestCreateDryRunJob_RetriesAThrottledLaunch's mock sends no Retry-After
// header at all, so it only exercises the computed-backoff fallback. This
// test closes that seam by sending a real Retry-After and asserting the
// command sleeps the header's value, not the computed 2s/4s schedule.
// The body is the object-shaped detail atlas actually sends
// (dry_run_limit_api_error, cettings/cettings/controllers/dry_run_helpers.py),
// and its context.retry_after_seconds deliberately disagrees with the header so
// the assertion also pins which of the two wins.
func TestCreateDryRunJob_HonoursServerRetryAfter(t *testing.T) {
	env, mock := setupLaunchEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	slept := stubThrottleSleep(t)

	calls := 0
	mock.RegisterHandler("PUT", "/api/orgs/"+testOrgID+"/dry-runs", func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "5")
			testutil.RespondJSON(w, http.StatusTooManyRequests, map[string]interface{}{
				"detail": map[string]interface{}{
					"msg":        "You have 3 dry run(s) in progress (limit: 3). Please wait for an existing dry run to complete before starting a new one.",
					"error_code": "DRY_RUN_RATE_LIMIT",
					"context": map[string]interface{}{
						"current_count": 3, "max_allowed": 3,
						"limit_kind": "concurrent_execution", "retry_after_seconds": 15,
					},
				},
			})
			return
		}
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{"dry_run_job_id": 99})
	})

	stdout, stderr, err := execCreate(t, "create", "dry-run-job", "-f", throttleSpecFile(t), "--cloud-source-id", "456")
	if err != nil {
		t.Fatalf("expected the retry to succeed, got: %v\nstderr:\n%s", err, stderr)
	}
	if calls != 2 {
		t.Fatalf("expected 2 launch attempts, got %d", calls)
	}
	if len(*slept) != 1 || (*slept)[0] != 5*time.Second {
		t.Fatalf("expected a single 5s wait honouring Retry-After, got %v (the computed backoff would be [2s])", *slept)
	}
	var payload map[string]int
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("stdout is not a single JSON object (%v):\n%s", err, stdout)
	}
	if payload["dry_run_job_id"] != 99 {
		t.Errorf("dry_run_job_id = %d, want 99", payload["dry_run_job_id"])
	}
}

// The same wiring, for a 429 the generated parser cannot read: an older server's
// bare-string detail, or a gateway shedding load. It reaches the command through
// Client.HandleError rather than through the response object, and the header has
// to survive that route too -- otherwise a throttle on the availability path
// silently loses the server's advice and falls back to the computed backoff.
func TestCreateDryRunJob_HonoursRetryAfterOnAnUnparseableBody(t *testing.T) {
	env, mock := setupLaunchEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	slept := stubThrottleSleep(t)

	calls := 0
	mock.RegisterHandler("PUT", "/api/orgs/"+testOrgID+"/dry-runs", func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "6")
			testutil.RespondJSON(w, http.StatusTooManyRequests, map[string]interface{}{
				"detail": "You have 3 dry run(s) in progress (limit: 3).",
			})
			return
		}
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{"dry_run_job_id": 99})
	})

	stdout, stderr, err := execCreate(t, "create", "dry-run-job", "-f", throttleSpecFile(t), "--cloud-source-id", "456")
	if err != nil {
		t.Fatalf("expected the retry to succeed, got: %v\nstderr:\n%s", err, stderr)
	}
	if calls != 2 {
		t.Fatalf("expected 2 launch attempts, got %d", calls)
	}
	if len(*slept) != 1 || (*slept)[0] != 6*time.Second {
		t.Fatalf("expected a single 6s wait honouring Retry-After, got %v (the computed backoff would be [2s])", *slept)
	}
	var payload map[string]int
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("stdout is not a single JSON object (%v):\n%s", err, stdout)
	}
	if payload["dry_run_job_id"] != 99 {
		t.Errorf("dry_run_job_id = %d, want 99", payload["dry_run_job_id"])
	}
}

// Exhausting the budget is still a failure, with the backend's own message and
// the throttle's exit code (1, not 2: the command was well-formed).
func TestCreateDryRunJob_GivesUpAfterTheAttemptLimit(t *testing.T) {
	env, mock := setupLaunchEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	stubThrottleSleep(t)

	calls := 0
	mock.RegisterHandler("PUT", "/api/orgs/"+testOrgID+"/dry-runs", func(w http.ResponseWriter, _ *http.Request) {
		calls++
		testutil.RespondJSON(w, http.StatusTooManyRequests, map[string]interface{}{
			"detail": "You have 3 dry run(s) in progress (limit: 3).",
		})
	})

	stdout, _, err := execCreate(t, "create", "dry-run-job", "-f", throttleSpecFile(t), "--cloud-source-id", "456")
	if err == nil {
		t.Fatal("expected an error once the attempts are exhausted")
	}
	if calls != cmdutil.ThrottleMaxAttempts {
		t.Fatalf("expected %d attempts, got %d", cmdutil.ThrottleMaxAttempts, calls)
	}
	if code := qerrors.GetExitCode(err); code != 1 {
		t.Errorf("exit code = %d, want 1 for a throttle", code)
	}
	if !strings.Contains(err.Error(), "3 dry run(s) in progress") {
		t.Errorf("the backend's own message was lost: %v", err)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Errorf("expected no JSON on a failed launch, got:\n%s", stdout)
	}
}

// A non-429 failure is permanent: retrying it just wastes the user's time, and
// its exit code must stay 2 rather than being remapped by the retry wrapper.
func TestCreateDryRunJob_DoesNotRetryAPermanentFailure(t *testing.T) {
	env, mock := setupLaunchEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	stubThrottleSleep(t)

	calls := 0
	mock.RegisterHandler("PUT", "/api/orgs/"+testOrgID+"/dry-runs", func(w http.ResponseWriter, _ *http.Request) {
		calls++
		testutil.RespondJSON(w, http.StatusBadRequest, map[string]interface{}{"detail": "rule_run_specs is invalid"})
	})

	_, _, err := execCreate(t, "create", "dry-run-job", "-f", throttleSpecFile(t), "--cloud-source-id", "456")
	if err == nil {
		t.Fatal("expected an error for a 400")
	}
	if calls != 1 {
		t.Fatalf("expected exactly 1 attempt for a permanent failure, got %d", calls)
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("exit code = %d, want 2 for a 4xx that is not a throttle", code)
	}
}

// A dataset_rule_id that is not a UUID is malformed input the CLI rejects
// before any request goes out, so it exits 2 like every other manifest
// rejection. Validate() checks only that the field is present, so this reaches
// buildLaunchRequest — it is not a defensive branch.
func TestCreateDryRunJob_RejectsMalformedDatasetRuleID(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/data-sources/456", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"id": 456, "name": "test_source",
		})
	})

	launched := 0
	mock.RegisterHandler("PUT", "/api/orgs/"+testOrgID+"/dry-runs", func(w http.ResponseWriter, _ *http.Request) {
		launched++
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{"dry_run_job_id": 99})
	})

	specFile := writeSpecFile(t, `apiVersion: qluster.ai/v1
kind: DryRunJob
metadata: {}
spec:
  rule_run_specs:
    - dataset_rule_id: "not-a-uuid"
`)

	cmd := setupCreateTestCommand()
	cmd.SetArgs([]string{"create", "dry-run-job", "-f", specFile, "--cloud-source-id", "456"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected an error for a non-UUID dataset_rule_id")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
	// The rendered message must not move.
	want := `failed to build launch request: invalid dataset_rule_id "not-a-uuid": invalid UUID length: 10`
	if err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
	if launched != 0 {
		t.Errorf("expected no dry-run launch, got %d", launched)
	}
}

// severity is a pass-through: the value the manifest names reaches the launch
// body untouched, and an omitted one is left off the wire entirely rather than
// sent as the default. That distinction is load-bearing on the read side --
// the launch controller stores rule_run_specs verbatim
// (create_and_run_dry_run, capi/capi/controllers/dry_run_controller.py) and
// only the rule loader resolves an absent severity, to as_declared
// (RuleController._get_dry_run_rules_and_versions,
// cettings/.../rule_controller_core.py), so a value qctl invents here
// would show up in `describe dry-run-job` as if the author had chosen it.
func TestCreateDryRunJob_SendsSeverityAndOmitsItWhenUnset(t *testing.T) {
	for _, tc := range []struct {
		name     string
		specLine string
		want     interface{} // nil = the key must be absent
	}{
		{"blocker", "      severity: blocker\n", "blocker"},
		{"warning", "      severity: warning\n", "warning"},
		{"as_declared", "      severity: as_declared\n", "as_declared"},
		{"omitted", "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()

			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
			env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
			env.SetupCredential(endpointKey, testOrgID, "test-token")

			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/data-sources/456", func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{"id": 456, "name": "test_source"})
			})

			var body map[string]interface{}
			mock.RegisterHandler("PUT", "/api/orgs/"+testOrgID+"/dry-runs", func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				if err := json.Unmarshal(raw, &body); err != nil {
					t.Errorf("launch body is not JSON: %v (%s)", err, raw)
				}
				testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{"dry_run_job_id": 99})
			})

			specFile := writeSpecFile(t, `apiVersion: qluster.ai/v1
kind: DryRunJob
metadata: {}
spec:
  rule_run_specs:
    - dataset_rule_id: "550e8400-e29b-41d4-a716-446655440099"
`+tc.specLine)

			cmd := setupCreateTestCommand()
			cmd.SetArgs([]string{"create", "dry-run-job", "-f", specFile, "--cloud-source-id", "456"})
			var buf bytes.Buffer
			cmd.SetOut(&buf)
			cmd.SetErr(&buf)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}

			specs, ok := body["rule_run_specs"].([]interface{})
			if !ok || len(specs) != 1 {
				t.Fatalf("rule_run_specs = %#v, want one entry", body["rule_run_specs"])
			}
			spec, ok := specs[0].(map[string]interface{})
			if !ok {
				t.Fatalf("rule_run_specs[0] = %#v, want an object", specs[0])
			}
			got, present := spec["severity"]
			if tc.want == nil {
				if present {
					t.Errorf("severity = %#v, want the key absent from the launch body", got)
				}
				return
			}
			if got != tc.want {
				t.Errorf("severity = %#v, want %#v", got, tc.want)
			}
		})
	}
}

// The launch prints {"dry_run_job_id": N}: as JSON with no -o and under -o
// json, as YAML under -o yaml, and dry-run-job/<id> under -o name.
func TestCreateDryRunJob_PrintsTheLaunchInEveryDeclaredFormat(t *testing.T) {
	env, mock := setupLaunchEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	mock.RegisterHandler("PUT", "/api/orgs/"+testOrgID+"/dry-runs", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{"dry_run_job_id": 99})
	})
	spec := throttleSpecFile(t)

	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{"default", nil, "{\n  \"dry_run_job_id\": 99\n}\n"},
		{"json", []string{"-o", "json"}, "{\n  \"dry_run_job_id\": 99\n}\n"},
		{"yaml", []string{"-o", "yaml"}, "dry_run_job_id: 99\n"},
		{"name", []string{"-o", "name"}, "dry-run-job/99\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			args := append([]string{"create", "dry-run-job", "-f", spec, "--cloud-source-id", "456"}, tt.args...)
			stdout, stderr, err := execCreate(t, args...)
			if err != nil {
				t.Fatalf("unexpected error: %v\nstderr:\n%s", err, stderr)
			}
			if stdout != tt.want {
				t.Errorf("stdout = %q, want %q", stdout, tt.want)
			}
		})
	}
}

// A series cloud source (456) reports its template table (100) as its own; 200
// is a period table of its series, 300 a table outside it.
const (
	dryRunSeriesID        = "5a0e8400-e29b-41d4-a716-446655440001"
	dryRunTemplateTableID = 100
	dryRunPeriodTableID   = 200
	dryRunOtherTableID    = 300
)

var dryRunSeriesTables = map[int]map[string]interface{}{
	dryRunTemplateTableID: {"id": dryRunTemplateTableID, "name": "sales"},
	dryRunPeriodTableID: {
		"id": dryRunPeriodTableID, "name": "sales_2026_09",
		"series": map[string]interface{}{"id": dryRunSeriesID, "name": "sales"},
		"period": map[string]interface{}{"period_key": "2026-09", "is_locked": false},
	},
	dryRunOtherTableID: {"id": dryRunOtherTableID, "name": "other"},
}

var dryRunSeriesSource = map[string]interface{}{
	"id": 456, "name": "sales_feed", "dataset_id": dryRunTemplateTableID, "dataset_name": "sales",
	"table_series_id": dryRunSeriesID, "routing_mode": "uploader_chooses",
}

// setupSeriesLaunch serves the tables, the series source by ID and in every
// source list, and a launch that records each request body it receives.
func setupSeriesLaunch(t *testing.T) (*testutil.MockAPIServer, *[]map[string]interface{}) {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	for id, row := range dryRunSeriesTables {
		mock.RegisterHandler("GET", fmt.Sprintf("/api/orgs/%s/datasets/%d", testOrgID, id), func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, row)
		})
	}
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		var results []interface{}
		for _, row := range dryRunSeriesTables {
			if row["name"] == r.URL.Query().Get("name") {
				results = append(results, row)
			}
		}
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{"results": results, "total_rows": len(results), "page": 1})
	})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/data-sources/456", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, dryRunSeriesSource)
	})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/data-sources", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"results": []interface{}{dryRunSeriesSource}, "total_rows": 1, "page": 1,
		})
	})

	var bodies []map[string]interface{}
	mock.RegisterHandler("PUT", "/api/orgs/"+testOrgID+"/dry-runs", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("launch body is not JSON: %v\n%s", err, raw)
		}
		bodies = append(bodies, body)
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{"dry_run_job_id": 99})
	})
	return mock, &bodies
}

// launchedDatasetID returns the dataset_id of the only launch, failing the test
// unless exactly one launch reached the server.
func launchedDatasetID(t *testing.T, bodies []map[string]interface{}) interface{} {
	t.Helper()
	if len(bodies) != 1 {
		t.Fatalf("expected 1 launch, got %d", len(bodies))
	}
	if got := bodies[0]["data_source_model_id"]; got != float64(456) {
		t.Errorf("data_source_model_id = %v, want 456", got)
	}
	return bodies[0]["dataset_id"]
}

func seriesSpecFile(t *testing.T, metadata string) string {
	t.Helper()
	return writeSpecFile(t, `apiVersion: qluster.ai/v1
kind: DryRunJob
metadata: `+metadata+`
spec:
  rule_run_specs:
    - dataset_rule_id: "550e8400-e29b-41d4-a716-446655440099"
`)
}

// The named table is the table the run goes on: a period table of the
// source's series is sent as dataset_id, however the source and table are
// named; the source's own table, or no table at all, sends null.
func TestCreateDryRunJob_SendsThePeriodTableAsDatasetID(t *testing.T) {
	cases := []struct {
		name     string
		metadata string
		args     []string
		want     interface{}
	}{
		{"source ID and period table", "{}", []string{"--cloud-source-id", "456", "--table", "sales_2026_09"}, float64(dryRunPeriodTableID)},
		{"source ID and period table ID", "{}", []string{"--cloud-source-id", "456", "--table-id", "200"}, float64(dryRunPeriodTableID)},
		{"source ID and own table", "{}", []string{"--cloud-source-id", "456", "--table", "sales"}, nil},
		{"source ID, no table", "{}", []string{"--cloud-source-id", "456"}, nil},
		{"auto-detected source", "{}", []string{"--table", "sales_2026_09"}, float64(dryRunPeriodTableID)},
		{"source by name", "{}", []string{"--cloud-source", "sales_feed", "--table", "sales_2026_09"}, float64(dryRunPeriodTableID)},
		{"metadata table, flag source ID", "{table: sales_2026_09}", []string{"--cloud-source-id", "456"}, float64(dryRunPeriodTableID)},
		{"flag table beats metadata table", "{table: sales_2026_09}", []string{"--cloud-source-id", "456", "--table", "sales"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, bodies := setupSeriesLaunch(t)
			args := append([]string{"create", "dry-run-job", "-f", seriesSpecFile(t, tc.metadata)}, tc.args...)
			if _, stderr, err := execCreate(t, args...); err != nil {
				t.Fatalf("Execute() error = %v\nstderr:\n%s", err, stderr)
			}
			if got := launchedDatasetID(t, *bodies); got != tc.want {
				t.Errorf("dataset_id = %v, want %v", got, tc.want)
			}
		})
	}
}

// A cloud source ID with a table outside its series is refused before any
// launch, whether the table belongs to no series or the source feeds none.
func TestCreateDryRunJob_RefusesATableTheSourceDoesNotFeed(t *testing.T) {
	cases := []struct {
		name   string
		source map[string]interface{}
		table  string
	}{
		{"table outside the series", dryRunSeriesSource, "other"},
		{"period table of an ordinary source's table", map[string]interface{}{
			"id": 456, "name": "plain", "dataset_id": dryRunOtherTableID, "dataset_name": "other",
		}, "sales_2026_09"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock, bodies := setupSeriesLaunch(t)
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/data-sources/456", func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, tc.source)
			})
			_, _, err := execCreate(t, "create", "dry-run-job", "-f", seriesSpecFile(t, "{}"), "--cloud-source-id", "456", "--table", tc.table)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit code = %d, want %d (err = %v)", code, qerrors.ExitBadUsage, err)
			}
			if !strings.Contains(err.Error(), "cloud source 456 feeds table ID") {
				t.Errorf("error should name the source's table, got: %v", err)
			}
			if len(*bodies) != 0 {
				t.Errorf("expected no launch, got %d", len(*bodies))
			}
		})
	}
}

// The launch's own refusals of a period table reach the user with the
// server's message and the exit code its status maps to.
func TestCreateDryRunJob_ReportsTheLaunchRefusingThePeriodTable(t *testing.T) {
	cases := []struct {
		status   int
		msg      string
		wantExit int
	}{
		{http.StatusBadRequest, "The target table does not belong to this cloud source.", qerrors.ExitBadUsage},
		{http.StatusForbidden, "You do not have access to table 200.", qerrors.ExitUnauthorized},
		{http.StatusNotFound, "The cloud source target table is unavailable.", qerrors.ExitNotFound},
	}
	for _, tc := range cases {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			mock, _ := setupSeriesLaunch(t)
			stubThrottleSleep(t)
			calls := 0
			mock.RegisterHandler("PUT", "/api/orgs/"+testOrgID+"/dry-runs", func(w http.ResponseWriter, _ *http.Request) {
				calls++
				testutil.RespondJSON(w, tc.status, map[string]interface{}{"detail": map[string]interface{}{"msg": tc.msg}})
			})
			stdout, _, err := execCreate(t, "create", "dry-run-job", "-f", seriesSpecFile(t, "{}"), "--cloud-source-id", "456", "--table", "sales_2026_09")
			if code := qerrors.GetExitCode(err); code != tc.wantExit {
				t.Fatalf("exit code = %d, want %d (err = %v)", code, tc.wantExit, err)
			}
			if !strings.Contains(err.Error(), tc.msg) {
				t.Errorf("the server's message was lost: %v", err)
			}
			if calls != 1 {
				t.Errorf("expected exactly 1 launch attempt, got %d", calls)
			}
			if strings.TrimSpace(stdout) != "" {
				t.Errorf("expected no JSON on a failed launch, got:\n%s", stdout)
			}
		})
	}
}
