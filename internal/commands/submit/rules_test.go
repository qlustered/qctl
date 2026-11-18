package submit

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

const testOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

func setupSubmitRulesTestCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use: "qctl",
	}

	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format (table|json|yaml|name)")
	rootCmd.PersistentFlags().Bool("no-headers", false, "Don't print column headers")
	rootCmd.PersistentFlags().String("columns", "", "Comma-separated list of columns to display")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "Maximum width for table columns")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "Show secret fields in plaintext")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "Allow non-localhost http://")
	rootCmd.PersistentFlags().CountP("verbose", "v", "Verbosity level")

	submitCmd := &cobra.Command{
		Use:   "submit",
		Short: "Submit rule definitions",
	}
	submitCmd.AddCommand(NewRulesCommand())
	rootCmd.AddCommand(submitCmd)
	cmdutil.TypeArgsErrors(rootCmd)

	return rootCmd
}

func TestSubmitRulesCommand_PythonSubmit(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	submitCalled := false
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-revisions/submit", func(w http.ResponseWriter, r *http.Request) {
		submitCalled = true
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"message":     "Rules imported successfully",
			"added":       [][]interface{}{{"my_rule", "1.0.0"}},
			"not_changed": [][]interface{}{{"existing_rule", "2.0.0"}},
		})
	})

	tmpDir := t.TempDir()
	pyFile := filepath.Join(tmpDir, "rules.py")
	if err := os.WriteFile(pyFile, []byte("# rule definition\nclass MyRule:\n    pass\n"), 0644); err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	cmd := setupSubmitRulesTestCommand()
	cmd.SetArgs([]string{"submit", "rules", "-f", pyFile, "--yes"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if !submitCalled {
		t.Error("Submit endpoint was not called")
	}
}

func TestSubmitRulesCommand_NoFiles(t *testing.T) {
	cmd := setupSubmitRulesTestCommand()
	cmd.SetArgs([]string{"submit", "rules"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Expected error when no files specified")
	}

	if !strings.Contains(err.Error(), "at least one file is required") {
		t.Errorf("Expected 'at least one file is required' error, got: %v", err)
	}

	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
}

func TestSubmitRulesCommand_RejectsYAML(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	tmpDir := t.TempDir()
	yamlFile := filepath.Join(tmpDir, "rule.yaml")
	if err := os.WriteFile(yamlFile, []byte("apiVersion: qluster.ai/v1\nkind: Rule\n"), 0644); err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	cmd := setupSubmitRulesTestCommand()
	cmd.SetArgs([]string{"submit", "rules", "-f", yamlFile, "--yes"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Expected error for YAML file")
	}

	if !strings.Contains(err.Error(), yamlFile+" is not a .py file") {
		t.Errorf("Expected '.py files' error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "qctl apply -f "+yamlFile) {
		t.Errorf("Expected suggestion to use 'qctl apply -f', got: %v", err)
	}

	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
	if got := mock.Requests(); len(got) != 0 {
		t.Errorf("requests = %v, want none before the extension check", got)
	}
}

func TestSubmitRulesCommand_RejectsUnsupportedExtension(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	tmpDir := t.TempDir()
	txtFile := filepath.Join(tmpDir, "rules.txt")
	if err := os.WriteFile(txtFile, []byte("some content"), 0644); err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	cmd := setupSubmitRulesTestCommand()
	cmd.SetArgs([]string{"submit", "rules", "-f", txtFile, "--yes"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Expected error for unsupported file type")
	}

	if !strings.Contains(err.Error(), "only .py files, or a directory of them, are supported") {
		t.Errorf("Expected 'only .py files' error, got: %v", err)
	}
	// apply -f reads manifests, so it is no answer for a .txt file.
	if strings.Contains(err.Error(), "qctl apply") {
		t.Errorf("a non-manifest extension must not get the apply hint, got: %v", err)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
}

func TestSubmitRulesCommand_FileNotFoundIsGenericFailure(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	missing := filepath.Join(t.TempDir(), "missing.py")
	cmd := setupSubmitRulesTestCommand()
	cmd.SetArgs([]string{"submit", "rules", "-f", missing, "--yes"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
	if !strings.Contains(err.Error(), "failed to read file "+missing) {
		t.Errorf("expected missing-file error, got: %v", err)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitGenericError {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitGenericError)
	}
}

func TestSubmitRulesCommand_OversizeFileIsBadUsage(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	filePath := filepath.Join(t.TempDir(), "oversize.py")
	if err := os.WriteFile(filePath, []byte(strings.Repeat("x", maxFileTextLength+1)), 0600); err != nil {
		t.Fatalf("write oversize rule file: %v", err)
	}

	cmd := setupSubmitRulesTestCommand()
	cmd.SetArgs([]string{"submit", "rules", "-f", filePath, "--yes"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for oversize file")
	}
	if want := fmt.Sprintf("file %s exceeds maximum size: %d characters (limit: %d)", filePath, maxFileTextLength+1, maxFileTextLength); err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
}

// registerConflict422 registers a submit handler that returns the verified
// backend conflict shape and records the request body's verbose value.
func registerConflict422(mock *testutil.MockAPIServer, sentVerbose *interface{}) {
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-revisions/submit",
		func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(r.Body)
			var body map[string]interface{}
			_ = json.Unmarshal(raw, &body)
			*sentVerbose = body["verbose"]
			testutil.RespondJSON(w, http.StatusUnprocessableEntity, map[string]interface{}{
				"detail": map[string]interface{}{
					"msg":        "Rule DedupeEmails v2.1.0 is in 'enabled' state and cannot be modified. Bump the release to create a new revision.\n\nChanges detected:\ncompact-diff-marker",
					"error_code": "ERR_RULE_VERSION_CONFLICT",
					"diff":       "full-diff-marker",
				},
			})
		})
}

// writeTempRuleFile creates a minimal .py rule file and returns its path.
func writeTempRuleFile(t *testing.T) string {
	t.Helper()
	pyFile := filepath.Join(t.TempDir(), "rules.py")
	if err := os.WriteFile(pyFile, []byte("# rule definition\nclass MyRule:\n    pass\n"), 0644); err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	return pyFile
}

func TestSubmitRulesCommand_ConflictShowsHint(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	var sentVerbose interface{}
	registerConflict422(mock, &sentVerbose)

	cmd := setupSubmitRulesTestCommand()
	cmd.SetArgs([]string{"submit", "rules", "-f", writeTempRuleFile(t), "--yes"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected conflict error")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
	if sentVerbose == true {
		t.Error("verbose must not be true without -v")
	}
	if !strings.Contains(err.Error(), "cannot be modified") {
		t.Errorf("expected backend msg in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "Bump 'release' in RuleMetadata to create a new revision.") {
		t.Errorf("expected bump-release hint, got: %v", err)
	}
	if strings.Contains(err.Error(), "Full diff:") {
		t.Errorf("full diff must not print without -v, got: %v", err)
	}
}

func TestSubmitRulesCommand_VerboseConflictShowsFullDiff(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	var sentVerbose interface{}
	registerConflict422(mock, &sentVerbose)

	cmd := setupSubmitRulesTestCommand()
	cmd.SetArgs([]string{"submit", "rules", "-f", writeTempRuleFile(t), "--yes", "-v"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected conflict error")
	}
	if sentVerbose != true {
		t.Errorf("request body verbose = %v, want true with -v", sentVerbose)
	}
	if !strings.Contains(err.Error(), "Full diff:\nfull-diff-marker") {
		t.Errorf("expected full diff section, got: %v", err)
	}
	if !strings.Contains(err.Error(), "Bump 'release' in RuleMetadata to create a new revision.") {
		t.Errorf("expected bump-release hint, got: %v", err)
	}
}

// registerSubmitOK registers a submit handler whose response adds dedupe-emails@2.1.0.
func registerSubmitOK(mock *testutil.MockAPIServer) {
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-revisions/submit",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
				"message": "Successfully submitted 1 rule(s)",
				"added":   [][]interface{}{{"dedupe-emails", "2.1.0"}},
			})
		})
}

// registerRevisionList registers the state-fetch handler with one revision in the given state.
func registerRevisionList(mock *testutil.MockAPIServer, state string) {
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
				"results": []map[string]interface{}{{
					"id":               "550e8400-e29b-41d4-a716-446655440000",
					"family_id":        "660e8400-e29b-41d4-a716-446655440000",
					"name":             "dedupe-emails",
					"slug":             "dedupe-emails",
					"release":          "2.1.0",
					"state":            state,
					"is_builtin":       false,
					"is_caf":           false,
					"is_default":       false,
					"affected_columns": []string{"email"},
					"created_at":       "2026-07-01T10:00:00Z",
					"updated_at":       "2026-07-01T10:00:00Z",
				}},
			})
		})
}

// runSubmitRules executes `submit rules -f <file> --yes` and returns output + error.
func runSubmitRules(t *testing.T, extraArgs ...string) (string, error) {
	t.Helper()
	cmd := setupSubmitRulesTestCommand()
	args := append([]string{"submit", "rules", "-f", writeTempRuleFile(t), "--yes"}, extraArgs...)
	cmd.SetArgs(args)
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	err := cmd.Execute()
	return buf.String(), err
}

func TestSubmitRulesCommand_DraftBreadcrumb(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	registerSubmitOK(mock)
	registerRevisionList(mock, "draft")

	output, err := runSubmitRules(t)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(output, "rule/dedupe-emails@2.1.0 submitted (state: draft — review required before enable)") {
		t.Errorf("missing draft breadcrumb, got:\n%s", output)
	}
	if !strings.Contains(output, "Next: qctl review request dedupe-emails --release 2.1.0") {
		t.Errorf("missing next-command hint, got:\n%s", output)
	}
	if !strings.Contains(output, "New to the review flow? Run: qctl guide request-review") {
		t.Errorf("missing guide hint after draft breadcrumb, got:\n%s", output)
	}
}

func TestSubmitRulesCommand_DraftBreadcrumbOnLaterPage(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	registerSubmitOK(mock)
	var pages []string
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions",
		func(w http.ResponseWriter, r *http.Request) {
			pages = append(pages, r.URL.Query().Get("page"))
			page := 1
			row := map[string]interface{}{
				"id": "450e8400-e29b-41d4-a716-446655440000", "family_id": "460e8400-e29b-41d4-a716-446655440000",
				"name": "dedupe-emails-helper", "slug": "dedupe-emails-helper", "release": "1.0.0", "state": "enabled",
				"is_builtin": false, "is_caf": false, "is_default": false, "affected_columns": []string{"email"},
				"created_at": "2026-07-01T10:00:00Z", "updated_at": "2026-07-01T10:00:00Z",
			}
			if r.URL.Query().Get("page") == "2" {
				page = 2
				row["id"] = "550e8400-e29b-41d4-a716-446655440000"
				row["family_id"] = "560e8400-e29b-41d4-a716-446655440000"
				row["name"] = "dedupe-emails"
				row["slug"] = "dedupe-emails"
				row["release"] = "2.1.0"
				row["state"] = "draft"
			}
			testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
				"results": []map[string]interface{}{row}, "total_rows": 2, "page": page,
			})
		})

	output, err := runSubmitRules(t)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(output, "Next: qctl review request dedupe-emails --release 2.1.0") {
		t.Errorf("missing later-page draft breadcrumb, got:\n%s", output)
	}
	if got, want := strings.Join(pages, ","), "1,2"; got != want {
		t.Errorf("requested pages = %q, want %q", got, want)
	}
}

func TestSubmitRulesCommand_EnabledNoBreadcrumb(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	registerSubmitOK(mock)
	registerRevisionList(mock, "enabled")

	output, err := runSubmitRules(t)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if strings.Contains(output, "review request") {
		t.Errorf("enabled revision must not get a breadcrumb, got:\n%s", output)
	}
	if strings.Contains(output, "qctl guide request-review") {
		t.Errorf("no guide hint expected without a draft breadcrumb, got:\n%s", output)
	}
}

func TestSubmitRulesCommand_BreadcrumbFetchFailureStillSucceeds(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	registerSubmitOK(mock)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusInternalServerError, map[string]interface{}{
				"detail": map[string]interface{}{"msg": "boom"},
			})
		})

	output, err := runSubmitRules(t)
	if err != nil {
		t.Fatalf("a successful submit must never fail over a breadcrumb, got: %v", err)
	}
	if !strings.Contains(output, "Successfully submitted 1 rule(s)") {
		t.Errorf("plain submit output must still print, got:\n%s", output)
	}
	if strings.Contains(output, "review request") {
		t.Errorf("no breadcrumb expected on fetch failure, got:\n%s", output)
	}
}

func TestSubmitRulesCommand_ForceSkipNote(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	// Force-skipped rules are invisible in the response: nothing added, nothing unchanged.
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-revisions/submit",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
				"message": "No new rules were added (rules may already exist)",
				"added":   [][]interface{}{},
			})
		})

	output, err := runSubmitRules(t, "--force")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(output, "such rules were skipped") {
		t.Errorf("missing --force skip note, got:\n%s", output)
	}

	// Without --force the note must not print.
	output, err = runSubmitRules(t)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if strings.Contains(output, "such rules were skipped") {
		t.Errorf("skip note must only print with --force, got:\n%s", output)
	}
}

func TestSubmitRulesHelpMentionsGuide(t *testing.T) {
	cmd := setupSubmitRulesTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"submit", "rules", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "qctl guide request-review") {
		t.Errorf("submit rules --help missing guide breadcrumb\n%s", buf.String())
	}
}

// Rule names, releases and the backend's message are echoed straight into the
// human output (the result tables and the draft breadcrumb), so a crafted one
// must arrive as a visible escape.
func TestSubmitRulesCommand_SanitizesServerText(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	const craftedName = "dedupe\x1b[31mred"
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-revisions/submit",
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
				"message":     "Submitted \x1b]0;pwned\x07",
				"added":       [][]interface{}{{craftedName, "2.1.0"}},
				"not_changed": [][]interface{}{{"legacy\x1b[2J", "1.0.0"}},
			})
		})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions",
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
				"results": []map[string]interface{}{{
					"id":               "550e8400-e29b-41d4-a716-446655440000",
					"family_id":        "660e8400-e29b-41d4-a716-446655440000",
					"name":             craftedName,
					"slug":             craftedName,
					"release":          "2.1.0",
					"state":            "draft",
					"is_builtin":       false,
					"is_caf":           false,
					"is_default":       false,
					"affected_columns": []string{"email"},
					"created_at":       "2026-07-01T10:00:00Z",
					"updated_at":       "2026-07-01T10:00:00Z",
				}},
			})
		})

	output, err := runSubmitRules(t)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if strings.ContainsRune(output, '\x1b') {
		t.Errorf("raw ESC reached the terminal: %q", output)
	}
	for _, want := range []string{
		`Submitted \x1b]0;pwned\x07`,
		`dedupe\x1b[31mred`,
		`legacy\x1b[2J`,
		`Next: qctl review request dedupe\x1b[31mred --release 2.1.0`,
	} {
		if !strings.Contains(output, want) {
			t.Errorf("output missing visible escape %q, got:\n%s", want, output)
		}
	}
}

// checkPath is the route --check posts to.
const checkPath = "/api/orgs/" + testOrgID + "/rule-revisions/check"

// setupCheckEnv starts a mock server with a logged-in context and returns it.
func setupCheckEnv(t *testing.T) *testutil.MockAPIServer {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")
	return mock
}

// runCheckRules executes `submit rules -f <file> --check` with no --yes, so a
// confirmation prompt would fail the run as non-interactive.
func runCheckRules(t *testing.T, extraArgs ...string) (string, error) {
	t.Helper()
	cmd := setupSubmitRulesTestCommand()
	args := append([]string{"submit", "rules", "-f", writeTempRuleFile(t), "--check"}, extraArgs...)
	cmd.SetArgs(args)
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	err := cmd.Execute()
	return buf.String(), err
}

// registerCheckOK answers the check route with one rule per plan action.
func registerCheckOK(mock *testutil.MockAPIServer) {
	mock.RegisterHandler("POST", checkPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"message": "Submit would accept this file: 1 added, 1 updated, 1 unchanged, 1 skipped",
			"rules": []map[string]interface{}{
				{"name": "dedupe-emails", "release": "2.1.0", "action": "added", "reason": nil},
				{"name": "trim-names", "release": "1.0.0", "action": "updated", "reason": nil},
				{"name": "legacy-check", "release": "3.0.0", "action": "unchanged", "reason": nil},
				{"name": "locked-rule", "release": "1.2.0", "action": "skipped",
					"reason": "Rule LockedRule v1.2.0 is in 'enabled' state and has code changes. Skipping due to force=True."},
			},
		})
	})
}

func TestSubmitRulesCheck_AcceptListsEveryRuleAndWritesNothing(t *testing.T) {
	mock := setupCheckEnv(t)
	registerCheckOK(mock)

	output, err := runCheckRules(t, "--force")
	if err != nil {
		t.Fatalf("Execute() error = %v\n%s", err, output)
	}
	if got := mock.Requests(); len(got) != 1 || got[0] != "POST "+checkPath {
		t.Errorf("requests = %v, want exactly [POST %s]", got, checkPath)
	}
	for _, want := range []string{
		"Checking rules from rules.py...",
		"Submit would accept this file: 1 added, 1 updated, 1 unchanged, 1 skipped",
		"NAME           VERSION  ACTION            REASON",
		"dedupe-emails  2.1.0    added",
		"trim-names     1.0.0    updated in place",
		"legacy-check   3.0.0    unchanged",
		"locked-rule    1.2.0    skipped           Rule LockedRule v1.2.0 is in 'enabled' state",
		checkOnlyNote,
	} {
		if !strings.Contains(output, want) {
			t.Errorf("output missing %q, got:\n%s", want, output)
		}
	}
	if strings.Contains(output, forceSkipNote) {
		t.Errorf("--check lists skipped rules, so the submit-only --force note must not print:\n%s", output)
	}
}

func TestSubmitRulesCheck_NoReasonColumnWithoutASkip(t *testing.T) {
	mock := setupCheckEnv(t)
	mock.RegisterHandler("POST", checkPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"message": "Submit would accept this file: 1 added, 0 updated, 0 unchanged, 0 skipped",
			"rules":   []map[string]interface{}{{"name": "dedupe-emails", "release": "2.1.0", "action": "added", "reason": nil}},
		})
	})

	output, err := runCheckRules(t)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if strings.Contains(output, "REASON") {
		t.Errorf("REASON column printed with no skipped rule:\n%s", output)
	}
	if !strings.Contains(output, "NAME           VERSION  ACTION\n") {
		t.Errorf("expected a three-column header, got:\n%s", output)
	}
}

// The check must send byte-for-byte the body submit sends for the same file
// and flags, or its verdict is about a different request.
func TestSubmitRulesCheck_SendsTheSubmitBody(t *testing.T) {
	for _, flags := range [][]string{nil, {"--force"}, {"-v"}, {"--force", "-v"}} {
		t.Run(strings.Join(append([]string{"flags"}, flags...), " "), func(t *testing.T) {
			mock := setupCheckEnv(t)
			bodies := map[string]map[string]interface{}{}
			record := func(route string) http.HandlerFunc {
				return func(w http.ResponseWriter, r *http.Request) {
					raw, _ := io.ReadAll(r.Body)
					var body map[string]interface{}
					if err := json.Unmarshal(raw, &body); err != nil {
						t.Errorf("%s body is not JSON: %v", route, err)
					}
					bodies[route] = body
					testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
						"message": "ok", "added": [][]interface{}{}, "rules": []interface{}{},
					})
				}
			}
			mock.RegisterHandler("POST", checkPath, record("check"))
			mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-revisions/submit", record("submit"))

			pyFile := writeTempRuleFile(t)
			for _, mode := range []string{"--check", "--yes"} {
				cmd := setupSubmitRulesTestCommand()
				cmd.SetArgs(append([]string{"submit", "rules", "-f", pyFile, mode}, flags...))
				var buf bytes.Buffer
				cmd.SetOut(&buf)
				cmd.SetErr(&buf)
				if err := cmd.Execute(); err != nil {
					t.Fatalf("%s: Execute() error = %v\n%s", mode, err, buf.String())
				}
			}

			if bodies["check"] == nil || bodies["submit"] == nil {
				t.Fatalf("bodies = %v, want one from each route", bodies)
			}
			if bodies["check"]["file_text"] == nil {
				t.Errorf("check body carries no file_text: %v", bodies["check"])
			}
			want, _ := json.Marshal(bodies["submit"])
			got, _ := json.Marshal(bodies["check"])
			if string(got) != string(want) {
				t.Errorf("check body = %s\nsubmit body = %s", got, want)
			}
		})
	}
}

// registerCheckConflict answers the check route with the conflict 422 submit
// sends, and records the request body's verbose value.
func registerCheckConflict(mock *testutil.MockAPIServer, sentVerbose *interface{}) {
	mock.RegisterHandler("POST", checkPath, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]interface{}
		_ = json.Unmarshal(raw, &body)
		*sentVerbose = body["verbose"]
		testutil.RespondJSON(w, http.StatusUnprocessableEntity, map[string]interface{}{
			"detail": map[string]interface{}{
				"msg":        "Rule DedupeEmails v2.1.0 is in 'enabled' state and cannot be modified. Bump the release to create a new revision.\n\nChanges detected:\ncompact-diff-marker",
				"error_code": "ERR_RULE_VERSION_CONFLICT",
				"diff":       "full-diff-marker",
			},
		})
	})
}

func TestSubmitRulesCheck_ConflictRendersLikeSubmit(t *testing.T) {
	mock := setupCheckEnv(t)
	var sentVerbose interface{}
	registerCheckConflict(mock, &sentVerbose)

	output, err := runCheckRules(t)
	if err == nil {
		t.Fatalf("expected a rejection, got success:\n%s", output)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
	if sentVerbose == true {
		t.Error("verbose must not be true without -v")
	}
	for _, want := range []string{"submit would reject this file", "cannot be modified", versionConflictHint} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q, got: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "Full diff:") {
		t.Errorf("full diff must not print without -v, got: %v", err)
	}
	if strings.Contains(output, checkOnlyNote) {
		t.Errorf("a rejected check printed the accept note:\n%s", output)
	}
}

func TestSubmitRules_NonConflictRuleRejectionsHaveNoReleaseHint(t *testing.T) {
	for _, tc := range []struct {
		name      string
		msg       string
		emptyDiff bool
	}{
		{name: "unknown concept slug", msg: "Rule AlphaRule binds concept slug(s) not defined in the organization: no_such_concept"},
		{name: "unknown metadata keyword", msg: "AlphaRule: unknown metadata keyword(s) problem_domain_bindings", emptyDiff: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := setupCheckEnv(t)
			reject := func(w http.ResponseWriter, _ *http.Request) {
				detail := map[string]interface{}{"msg": tc.msg, "error_code": "ERR_RULE_VERSION_CONFLICT"}
				if tc.emptyDiff {
					detail["diff"] = ""
				}
				testutil.RespondJSON(w, http.StatusUnprocessableEntity, map[string]interface{}{
					"detail": detail,
				})
			}
			mock.RegisterHandler("POST", checkPath, reject)
			mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-revisions/submit", reject)

			for _, mode := range []string{"check", "submit"} {
				t.Run(mode, func(t *testing.T) {
					var err error
					if mode == "check" {
						_, err = runCheckRules(t)
					} else {
						cmd := setupSubmitRulesTestCommand()
						cmd.SetArgs([]string{"submit", "rules", "-f", writeTempRuleFile(t), "--yes"})
						cmd.SetOut(io.Discard)
						cmd.SetErr(io.Discard)
						err = cmd.Execute()
					}
					if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
						t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
					}
					if err != nil && strings.Contains(err.Error(), versionConflictHint) {
						t.Errorf("unexpected release hint: %v", err)
					}
				})
			}
		})
	}
}

func TestSubmitRulesCheck_VerboseConflictShowsFullDiff(t *testing.T) {
	mock := setupCheckEnv(t)
	var sentVerbose interface{}
	registerCheckConflict(mock, &sentVerbose)

	_, err := runCheckRules(t, "-v")
	if err == nil {
		t.Fatal("expected a rejection")
	}
	if sentVerbose != true {
		t.Errorf("request body verbose = %v, want true with -v", sentVerbose)
	}
	if !strings.Contains(err.Error(), "Full diff:\nfull-diff-marker") {
		t.Errorf("expected full diff section, got: %v", err)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
}

// A preflight violation is a plain-string 422 detail, not a conflict.
func TestSubmitRulesCheck_PreflightRejectionExitsTwo(t *testing.T) {
	mock := setupCheckEnv(t)
	mock.RegisterHandler("POST", checkPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusUnprocessableEntity, map[string]interface{}{
			"detail": "rule file rejected by preflight: import of 'os' is not allowed",
		})
	})

	_, err := runCheckRules(t)
	if err == nil {
		t.Fatal("expected a rejection")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
	if !strings.Contains(err.Error(), "import of 'os' is not allowed") {
		t.Errorf("error lost the preflight message: %v", err)
	}
	if strings.Contains(err.Error(), versionConflictHint) {
		t.Errorf("bump-release hint on a non-conflict rejection: %v", err)
	}
}

// A server without the route answers 405 (the path matches rule-revisions/{id},
// which has no POST) or 404; --check must say so and never fall back to submit.
func TestSubmitRulesCheck_UnsupportedServerExitsFive(t *testing.T) {
	for _, status := range []int{http.StatusMethodNotAllowed, http.StatusNotFound} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			mock := setupCheckEnv(t)
			mock.RegisterHandler("POST", checkPath, func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, status, map[string]interface{}{"detail": http.StatusText(status)})
			})
			submitCalled := false
			mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-revisions/submit",
				func(w http.ResponseWriter, _ *http.Request) {
					submitCalled = true
					testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{"message": "ok", "added": [][]interface{}{}})
				})

			_, err := runCheckRules(t)
			if err == nil {
				t.Fatal("expected an error from a server without the check route")
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitAPIIncompatible {
				t.Errorf("exit code = %d, want %d", got, qerrors.ExitAPIIncompatible)
			}
			if !strings.Contains(err.Error(), "does not support 'qctl submit rules --check'") {
				t.Errorf("error does not name the missing support: %v", err)
			}
			if submitCalled {
				t.Error("--check fell back to submit")
			}
		})
	}
}

func TestSubmitRulesCheck_ServerErrorIsAFailedCheck(t *testing.T) {
	mock := setupCheckEnv(t)
	mock.RegisterHandler("POST", checkPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusInternalServerError, map[string]interface{}{"detail": "Internal Server Error"})
	})

	_, err := runCheckRules(t)
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitGenericError {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitGenericError)
	}
	if !strings.Contains(err.Error(), "rule check failed") || strings.Contains(err.Error(), "would reject") {
		t.Errorf("a 500 must read as a failed check, not a verdict: %v", err)
	}
}

// Submit stops at the first rejected file, so the check does too.
func TestSubmitRulesCheck_StopsAtTheFirstRejectedFile(t *testing.T) {
	mock := setupCheckEnv(t)
	var sentVerbose interface{}
	registerCheckConflict(mock, &sentVerbose)

	cmd := setupSubmitRulesTestCommand()
	cmd.SetArgs([]string{"submit", "rules", "-f", writeTempRuleFile(t), "-f", writeTempRuleFile(t), "--check"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected a rejection")
	}
	if got := mock.Requests(); len(got) != 1 {
		t.Errorf("requests = %v, want one check before stopping", got)
	}
}

// Server-controlled text in the check report must arrive as visible escapes.
func TestSubmitRulesCheck_SanitizesServerText(t *testing.T) {
	mock := setupCheckEnv(t)
	mock.RegisterHandler("POST", checkPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"message": "Submit \x1b]0;pwned\x07",
			"rules": []map[string]interface{}{
				{"name": "dedupe\x1b[31mred", "release": "2.1.0", "action": "skipped", "reason": "locked\x1b[2J"},
				{"name": "odd", "release": "1.0.0", "action": "future\x1b[1m", "reason": nil},
			},
		})
	})

	output, err := runCheckRules(t)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if strings.ContainsRune(output, '\x1b') {
		t.Errorf("raw ESC reached the terminal: %q", output)
	}
	for _, want := range []string{`Submit \x1b]0;pwned\x07`, `dedupe\x1b[31mred`, `locked\x1b[2J`, `future\x1b[1m`} {
		if !strings.Contains(output, want) {
			t.Errorf("output missing visible escape %q, got:\n%s", want, output)
		}
	}
}

// runSubmitRulesSplit runs `submit rules` with args and returns stdout and
// stderr apart.
func runSubmitRulesSplit(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := setupSubmitRulesTestCommand()
	cmd.SetArgs(append([]string{"submit", "rules"}, args...))
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	err := cmd.Execute()
	return stdout.String(), stderr.String(), err
}

// -o json prints the server's answer, with not_changed [] when the server
// leaves it out, and the review breadcrumb moves to stderr.
func TestSubmitRulesWritesTheAnswer(t *testing.T) {
	mock := setupCheckEnv(t)
	registerSubmitOK(mock)
	registerRevisionList(mock, "draft")

	stdout, stderr, err := runSubmitRulesSplit(t, "-f", writeTempRuleFile(t), "--yes", "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stderr, "Next: qctl review request dedupe-emails --release 2.1.0") {
		t.Errorf("stderr missing the breadcrumb:\n%s", stderr)
	}
	var got struct {
		Message    string     `json:"message"`
		Added      [][]string `json:"added"`
		NotChanged *[]any     `json:"not_changed"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
	}
	if len(got.Added) != 1 || got.Added[0][0] != "dedupe-emails" || got.Added[0][1] != "2.1.0" {
		t.Errorf("added = %v, want [[dedupe-emails 2.1.0]]", got.Added)
	}
	if got.NotChanged == nil || len(*got.NotChanged) != 0 {
		t.Errorf("not_changed = %v, want []", got.NotChanged)
	}
}

// --check -o yaml prints the check answer, and two files print one List.
func TestSubmitRulesCheckWritesOneListForTwoFiles(t *testing.T) {
	mock := setupCheckEnv(t)
	registerCheckOK(mock)

	stdout, stderr, err := runSubmitRulesSplit(t,
		"-f", writeTempRuleFile(t), "-f", writeTempRuleFile(t), "--check", "-o", "yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stderr, checkOnlyNote) {
		t.Errorf("stderr missing the check note:\n%s", stderr)
	}
	var list struct {
		Kind  string `yaml:"kind"`
		Items []struct {
			Message string           `yaml:"message"`
			Rules   []map[string]any `yaml:"rules"`
		} `yaml:"items"`
	}
	if err := yaml.Unmarshal([]byte(stdout), &list); err != nil {
		t.Fatalf("stdout is not one YAML document: %v\n%s", err, stdout)
	}
	if list.Kind != "List" || len(list.Items) != 2 || len(list.Items[0].Rules) != 4 {
		t.Errorf("got %+v, want a List of two check answers", list)
	}
}

// submitPath is the route submit posts to.
const submitPath = "/api/orgs/" + testOrgID + "/rule-revisions/submit"

// writeRuleDir creates a directory holding a.py and b.py, whose texts are
// "# a\n" and "# b\n", beside what a directory submit skips: notes.txt,
// subdir/c.py and a subdirectory named nested.py.
func writeRuleDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, sub := range []string{"subdir", "nested.py"} {
		if err := os.Mkdir(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", sub, err)
		}
	}
	for name, text := range map[string]string{
		"b.py":                          "# b\n",
		"a.py":                          "# a\n",
		"notes.txt":                     "notes\n",
		filepath.Join("subdir", "c.py"): "# c\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

// recordFileTexts answers route with an empty success and appends each
// request body's file_text to texts.
func recordFileTexts(t *testing.T, mock *testutil.MockAPIServer, route string, texts *[]string) {
	mock.RegisterHandler("POST", route, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body struct {
			FileText string `json:"file_text"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("%s body is not JSON: %v", route, err)
		}
		*texts = append(*texts, body.FileText)
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"message": "ok", "added": [][]interface{}{}, "rules": []interface{}{},
		})
	})
}

// -f <dir> submits the .py files directly inside it, in name order, and
// nothing else there.
func TestSubmitRulesDirectorySubmitsItsPyFilesInNameOrder(t *testing.T) {
	mock := setupCheckEnv(t)
	var texts []string
	recordFileTexts(t, mock, submitPath, &texts)

	stdout, stderr, err := runSubmitRulesSplit(t, "-f", writeRuleDir(t), "--yes")
	if err != nil {
		t.Fatalf("Execute() error = %v\n%s%s", err, stdout, stderr)
	}
	if got, want := fmt.Sprintf("%q", texts), fmt.Sprintf("%q", []string{"# a\n", "# b\n"}); got != want {
		t.Errorf("submitted file texts = %s, want %s", got, want)
	}
	if got := mock.Requests(); len(got) != 2 || got[0] != "POST "+submitPath || got[1] != "POST "+submitPath {
		t.Errorf("requests = %v, want two submits", got)
	}
}

// --check over a directory checks the same files in the same order, and
// prints one List under -o json.
func TestSubmitRulesCheckDirectoryChecksEachFileAndPrintsOneList(t *testing.T) {
	mock := setupCheckEnv(t)
	var texts []string
	recordFileTexts(t, mock, checkPath, &texts)

	stdout, stderr, err := runSubmitRulesSplit(t, "-f", writeRuleDir(t), "--check", "-o", "json")
	if err != nil {
		t.Fatalf("Execute() error = %v\n%s%s", err, stdout, stderr)
	}
	if got, want := fmt.Sprintf("%q", texts), fmt.Sprintf("%q", []string{"# a\n", "# b\n"}); got != want {
		t.Errorf("checked file texts = %s, want %s", got, want)
	}
	if got := mock.Requests(); len(got) != 2 || got[0] != "POST "+checkPath || got[1] != "POST "+checkPath {
		t.Errorf("requests = %v, want two checks and no submit", got)
	}
	var list struct {
		Kind  string           `json:"kind"`
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal([]byte(stdout), &list); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
	}
	if list.Kind != "List" || len(list.Items) != 2 {
		t.Errorf("got %+v, want a List of two check answers", list)
	}
}

func TestSubmitRulesDirectoryWithoutPyFilesIsBadUsage(t *testing.T) {
	mock := setupCheckEnv(t)
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for _, name := range []string{"notes.txt", filepath.Join("subdir", "c.py")} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("# c\n"), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	_, _, err := runSubmitRulesSplit(t, "-f", dir, "--yes")
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (err: %v)", got, qerrors.ExitBadUsage, err)
	}
	if err == nil || !strings.Contains(err.Error(), "directory "+dir+" holds no .py file") {
		t.Errorf("error does not name the directory: %v", err)
	}
	if err != nil && strings.Contains(err.Error(), "qctl apply") {
		t.Errorf("a directory must not get the apply hint: %v", err)
	}
	if got := mock.Requests(); len(got) != 0 {
		t.Errorf("requests = %v, want none", got)
	}
}

// writeDirFiles creates a directory holding files, each name mapped to its
// text, and returns it.
func writeDirFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

// A directory laid out as a Python package submits its rule files and not its
// empty __init__.py, which sorts first and which the server refuses.
func TestSubmitRulesDirectorySkipsInitPy(t *testing.T) {
	mock := setupCheckEnv(t)
	var texts []string
	recordFileTexts(t, mock, submitPath, &texts)
	dir := writeDirFiles(t, map[string]string{"__init__.py": "", "orders.py": "# orders\n"})

	stdout, stderr, err := runSubmitRulesSplit(t, "-f", dir, "--yes")
	if err != nil {
		t.Fatalf("Execute() error = %v\n%s%s", err, stdout, stderr)
	}
	if got, want := fmt.Sprintf("%q", texts), fmt.Sprintf("%q", []string{"# orders\n"}); got != want {
		t.Errorf("submitted file texts = %s, want %s", got, want)
	}
}

// An __init__.py named by its own -f is sent like any other .py file.
func TestSubmitRulesSendsAnInitPyNamedByF(t *testing.T) {
	mock := setupCheckEnv(t)
	var texts []string
	recordFileTexts(t, mock, submitPath, &texts)
	dir := writeDirFiles(t, map[string]string{"__init__.py": "# init\n"})

	stdout, stderr, err := runSubmitRulesSplit(t, "-f", filepath.Join(dir, "__init__.py"), "--yes")
	if err != nil {
		t.Fatalf("Execute() error = %v\n%s%s", err, stdout, stderr)
	}
	if got, want := fmt.Sprintf("%q", texts), fmt.Sprintf("%q", []string{"# init\n"}); got != want {
		t.Errorf("submitted file texts = %s, want %s", got, want)
	}
}

func TestSubmitRulesDirectoryWithOnlyInitPyIsBadUsage(t *testing.T) {
	mock := setupCheckEnv(t)
	dir := writeDirFiles(t, map[string]string{"__init__.py": ""})

	_, _, err := runSubmitRulesSplit(t, "-f", dir, "--yes")
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (err: %v)", got, qerrors.ExitBadUsage, err)
	}
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"directory " + dir + " holds no .py file to submit", "other than __init__.py"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q, got: %v", want, err)
		}
	}
	if got := mock.Requests(); len(got) != 0 {
		t.Errorf("requests = %v, want none", got)
	}
}

// A path that does not exist fails before any request, with the same message
// and exit code whether it names a .py file or a directory.
func TestSubmitRulesMissingPathFailsBeforeAnyRequest(t *testing.T) {
	dir := t.TempDir()
	missingFile := filepath.Join(dir, "missing.py")
	missingDir := filepath.Join(dir, "rulez") + string(filepath.Separator)
	for _, tc := range []struct {
		name    string
		args    []string
		missing string
	}{
		{name: "missing .py file", args: []string{"-f", missingFile}, missing: missingFile},
		{name: "missing directory", args: []string{"-f", missingDir}, missing: missingDir},
		{name: "missing file after a present one", args: []string{"-f", writeTempRuleFile(t), "-f", missingFile}, missing: missingFile},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := setupCheckEnv(t)
			_, _, err := runSubmitRulesSplit(t, append(tc.args, "--yes")...)
			if got := qerrors.GetExitCode(err); got != qerrors.ExitGenericError {
				t.Errorf("exit code = %d, want %d (err: %v)", got, qerrors.ExitGenericError, err)
			}
			if !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("error does not wrap fs.ErrNotExist: %v", err)
			}
			if err == nil || !strings.HasPrefix(err.Error(), "failed to read file "+tc.missing+": ") {
				t.Errorf("error does not name the missing path: %v", err)
			}
			if got := mock.Requests(); len(got) != 0 {
				t.Errorf("requests = %v, want none", got)
			}
		})
	}
}

// With no -f at all, the error says files go after -f rather than that -f
// took only one path.
func TestSubmitRulesArgumentsWithoutFNameTheFlag(t *testing.T) {
	dir := writeRuleDir(t)
	a, b := filepath.Join(dir, "a.py"), filepath.Join(dir, "b.py")
	for _, tc := range []struct {
		name  string
		args  []string
		given string
	}{
		{name: "one file", args: []string{a}, given: fmt.Sprintf("%q was given as an argument", a)},
		{name: "two files", args: []string{a, b}, given: fmt.Sprintf("%q and 1 more were given as arguments", a)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := setupCheckEnv(t)
			_, _, err := runSubmitRulesSplit(t, append(tc.args, "--yes")...)
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d (err: %v)", got, qerrors.ExitBadUsage, err)
			}
			if err == nil {
				t.Fatal("expected an error")
			}
			for _, want := range []string{
				"qctl submit rules: files are passed with -f, but " + tc.given + "\n",
				"Give each file its own -f (-f a.py -f b.py), or pass their directory to submit the .py files in it (-f rules/)",
				"\nUsage: qctl submit rules",
			} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error missing %q, got:\n%v", want, err)
				}
			}
			if strings.Contains(err.Error(), "-f takes one path") {
				t.Errorf("an invocation without -f got the -f hint: %v", err)
			}
			if got := mock.Requests(); len(got) != 0 {
				t.Errorf("requests = %v, want none", got)
			}
		})
	}
}

// A shell glob after -f leaves its other files as arguments; the error says
// how to pass them.
func TestSubmitRulesStrayArgumentsNameTheFix(t *testing.T) {
	dir := writeRuleDir(t)
	a, b := filepath.Join(dir, "a.py"), filepath.Join(dir, "b.py")
	for _, tc := range []struct {
		name string
		args []string
		left string
	}{
		{name: "one stray", args: []string{"-f", a, b}, left: fmt.Sprintf("%q was left over as an argument", b)},
		{name: "two strays", args: []string{"-f", a, b, a}, left: fmt.Sprintf("%q and 1 more were left over as arguments", b)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := setupCheckEnv(t)
			_, _, err := runSubmitRulesSplit(t, append(tc.args, "--yes")...)
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d (err: %v)", got, qerrors.ExitBadUsage, err)
			}
			if err == nil {
				t.Fatal("expected an error")
			}
			for _, want := range []string{
				"qctl submit rules: -f takes one path, so " + tc.left + "\n",
				"Give each file its own -f (-f a.py -f b.py), or pass their directory to submit the .py files in it (-f rules/)",
				"\nUsage: qctl submit rules",
			} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error missing %q, got:\n%v", want, err)
				}
			}
			if got := mock.Requests(); len(got) != 0 {
				t.Errorf("requests = %v, want none", got)
			}
		})
	}
}

// A stray "false" is a boolean flag's value, so it keeps the shared hint
// rather than the -f one.
func TestSubmitRulesStrayBoolValueKeepsTheFlagHint(t *testing.T) {
	_, _, err := runSubmitRulesSplit(t, "-f", writeTempRuleFile(t), "--yes", "false")
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (err: %v)", got, qerrors.ExitBadUsage, err)
	}
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "Boolean flags take a value only as --yes=false") {
		t.Errorf("error lost the boolean-flag hint: %v", err)
	}
	if strings.Contains(err.Error(), "-f takes one path") {
		t.Errorf("a boolean value got the -f hint: %v", err)
	}
}
