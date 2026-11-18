package delete

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/rule_versions"
	"github.com/qlustered/qctl/internal/stored_items"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

func uuidPtr(s string) *openapi_types.UUID {
	u := uuid.MustParse(s)
	return &u
}

const testRevisionUUID = "aaaa0000-bbbb-cccc-dddd-eeeeeeee0001"

// ShortID for testRevisionUUID: first 8 hex chars of "aaaa0000bbbbccccddddeeeeeeee0001"
const testRevisionShortID = "aaaa0000"

func TestDisplayUnsubmitResult_DeletedShortID(t *testing.T) {
	result := &rule_versions.RuleVersionUnsubmitResponse{
		Message: "2 rules deleted",
		Deleted: []rule_versions.UnsubmitResultItem{
			{Name: "email_validator", Release: "1.0.0", RevisionID: uuidPtr(testRevisionUUID)},
			{Name: "phone_validator", Release: "2.0.0", RevisionID: uuidPtr("aaaa0000-bbbb-cccc-dddd-eeeeeeee0002")},
		},
	}

	var buf bytes.Buffer
	displayUnsubmitResult(&buf, result, 0)
	out := buf.String()

	if !strings.Contains(out, "2 rules deleted") {
		t.Error("Expected message in output")
	}
	if !strings.Contains(out, "Deleted:") {
		t.Error("Expected 'Deleted:' header")
	}
	if !strings.Contains(out, "REVISION ID") {
		t.Error("Expected REVISION ID column when revision IDs are present")
	}
	if !strings.Contains(out, testRevisionShortID) {
		t.Errorf("Expected short ID %s in output, got:\n%s", testRevisionShortID, out)
	}
	// Full UUID should NOT appear at verbosity 0
	if strings.Contains(out, testRevisionUUID) {
		t.Error("Full UUID should not appear at verbosity 0")
	}
}

func TestDisplayUnsubmitResult_DeletedFullIDWithVerbose(t *testing.T) {
	result := &rule_versions.RuleVersionUnsubmitResponse{
		Message: "1 rule deleted",
		Deleted: []rule_versions.UnsubmitResultItem{
			{Name: "email_validator", Release: "1.0.0", RevisionID: uuidPtr(testRevisionUUID)},
		},
	}

	var buf bytes.Buffer
	displayUnsubmitResult(&buf, result, 1)
	out := buf.String()

	if !strings.Contains(out, testRevisionUUID) {
		t.Errorf("Expected full UUID %s at verbosity >= 1, got:\n%s", testRevisionUUID, out)
	}
}

func TestDisplayUnsubmitResult_DeletedWithoutRevisionID(t *testing.T) {
	result := &rule_versions.RuleVersionUnsubmitResponse{
		Message: "1 rule deleted",
		Deleted: []rule_versions.UnsubmitResultItem{
			{Name: "email_validator", Release: "1.0.0", RevisionID: nil},
		},
	}

	var buf bytes.Buffer
	displayUnsubmitResult(&buf, result, 0)
	out := buf.String()

	if !strings.Contains(out, "Deleted:") {
		t.Error("Expected 'Deleted:' header")
	}
	if strings.Contains(out, "REVISION ID") {
		t.Error("REVISION ID column should be hidden when all revision IDs are nil")
	}
	if !strings.Contains(out, "email_validator") {
		t.Error("Expected email_validator in output")
	}
	if !strings.Contains(out, "1.0.0") {
		t.Error("Expected version in output")
	}
}

func TestDisplayUnsubmitResult_NotFound(t *testing.T) {
	result := &rule_versions.RuleVersionUnsubmitResponse{
		Message: "0 rules deleted",
		NotFound: []rule_versions.UnsubmitResultItem{
			{Name: "missing_rule", Release: "1.0.0", RevisionID: nil},
		},
	}

	var buf bytes.Buffer
	displayUnsubmitResult(&buf, result, 0)
	out := buf.String()

	if !strings.Contains(out, "Not found:") {
		t.Error("Expected 'Not found:' header")
	}
	if strings.Contains(out, "REVISION ID") {
		t.Error("REVISION ID column should be hidden for not-found items with nil revision IDs")
	}
	if !strings.Contains(out, "missing_rule") {
		t.Error("Expected missing_rule in output")
	}
}

func TestDisplayUnsubmitResult_SkippedGroups(t *testing.T) {
	result := &rule_versions.RuleVersionUnsubmitResponse{
		Message: "0 rules deleted",
		Skipped: []rule_versions.UnsubmitSkippedGroup{
			{
				Reason: "rule is enabled",
				Rules: []rule_versions.UnsubmitResultItem{
					{Name: "active_rule", Release: "1.0.0", RevisionID: uuidPtr(testRevisionUUID)},
				},
			},
			{
				Reason: "no metadata.release",
				Rules: []rule_versions.UnsubmitResultItem{
					{Name: "bad_rule", Release: "unknown", RevisionID: nil},
				},
			},
		},
	}

	var buf bytes.Buffer
	displayUnsubmitResult(&buf, result, 0)
	out := buf.String()

	if !strings.Contains(out, "Skipped:") {
		t.Error("Expected 'Skipped:' header")
	}
	if !strings.Contains(out, "Reason: rule is enabled") {
		t.Error("Expected first reason")
	}
	if !strings.Contains(out, "Reason: no metadata.release") {
		t.Error("Expected second reason")
	}
	if !strings.Contains(out, "active_rule") {
		t.Error("Expected active_rule in output")
	}
	if !strings.Contains(out, "bad_rule") {
		t.Error("Expected bad_rule in output")
	}
	// Short ID at verbosity 0
	if !strings.Contains(out, testRevisionShortID) {
		t.Errorf("Expected short ID %s in output", testRevisionShortID)
	}
}

func TestDisplayUnsubmitResult_Mixed(t *testing.T) {
	result := &rule_versions.RuleVersionUnsubmitResponse{
		Message: "1 rule deleted, 1 not found, 1 skipped",
		Deleted: []rule_versions.UnsubmitResultItem{
			{Name: "good_rule", Release: "1.0.0", RevisionID: uuidPtr(testRevisionUUID)},
		},
		NotFound: []rule_versions.UnsubmitResultItem{
			{Name: "gone_rule", Release: "2.0.0", RevisionID: nil},
		},
		Skipped: []rule_versions.UnsubmitSkippedGroup{
			{
				Reason: "rule is builtin",
				Rules: []rule_versions.UnsubmitResultItem{
					{Name: "builtin_rule", Release: "1.0.0", RevisionID: uuidPtr("aaaa0000-bbbb-cccc-dddd-eeeeeeee0003")},
				},
			},
		},
	}

	var buf bytes.Buffer
	displayUnsubmitResult(&buf, result, 0)
	out := buf.String()

	if !strings.Contains(out, "Deleted:") {
		t.Error("Expected 'Deleted:' section")
	}
	if !strings.Contains(out, "Not found:") {
		t.Error("Expected 'Not found:' section")
	}
	if !strings.Contains(out, "Skipped:") {
		t.Error("Expected 'Skipped:' section")
	}
	if !strings.Contains(out, "good_rule") {
		t.Error("Expected good_rule in output")
	}
	if !strings.Contains(out, "gone_rule") {
		t.Error("Expected gone_rule in output")
	}
	if !strings.Contains(out, "builtin_rule") {
		t.Error("Expected builtin_rule in output")
	}
}

func TestDisplayUnsubmitResult_EmptyResult(t *testing.T) {
	result := &rule_versions.RuleVersionUnsubmitResponse{
		Message: "No rules matched",
	}

	var buf bytes.Buffer
	displayUnsubmitResult(&buf, result, 0)
	out := buf.String()

	if !strings.Contains(out, "No rules matched") {
		t.Error("Expected message in output")
	}
	if strings.Contains(out, "Deleted:") {
		t.Error("Should not show Deleted section when empty")
	}
	if strings.Contains(out, "Not found:") {
		t.Error("Should not show Not found section when empty")
	}
	if strings.Contains(out, "Skipped:") {
		t.Error("Should not show Skipped section when empty")
	}
}

func TestDisplayUnsubmitResult_MixedRevisionIDs(t *testing.T) {
	result := &rule_versions.RuleVersionUnsubmitResponse{
		Message: "2 rules deleted",
		Deleted: []rule_versions.UnsubmitResultItem{
			{Name: "rule_a", Release: "1.0.0", RevisionID: uuidPtr(testRevisionUUID)},
			{Name: "rule_b", Release: "2.0.0", RevisionID: nil},
		},
	}

	var buf bytes.Buffer
	displayUnsubmitResult(&buf, result, 0)
	out := buf.String()

	// Should show REVISION ID column because at least one item has it
	if !strings.Contains(out, "REVISION ID") {
		t.Error("Expected REVISION ID column when at least one item has a revision ID")
	}
	if !strings.Contains(out, testRevisionShortID) {
		t.Errorf("Expected short ID %s for rule_a", testRevisionShortID)
	}
}

func setupDeleteRulesTestCommand() *cobra.Command {
	rootCmd := &cobra.Command{Use: "qctl"}
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "Output format")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "")
	rootCmd.PersistentFlags().CountP("verbose", "v", "")
	deleteCmd := &cobra.Command{Use: "delete"}
	deleteCmd.AddCommand(NewRulesCommand())
	rootCmd.AddCommand(deleteCmd)
	return rootCmd
}

func TestDeleteRulesWithoutFilesExitsTwo(t *testing.T) {
	cmd := setupDeleteRulesTestCommand()
	cmd.SetArgs([]string{"delete", "rules"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected an error when no files are specified")
	}
	if !strings.Contains(err.Error(), "at least one file is required") {
		t.Errorf("error = %q", err.Error())
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
}

// Server-supplied rule names, releases, messages and skip reasons are rendered
// straight to the terminal here, so a crafted value must arrive as a visible
// escape rather than as a live ANSI sequence.
func TestDisplayUnsubmitResultSanitizesServerText(t *testing.T) {
	result := &rule_versions.RuleVersionUnsubmitResponse{
		Message: "removed \x1b[31mred",
		Deleted: []rule_versions.UnsubmitResultItem{
			{Name: "email\x1b]0;pwned\x07", Release: "1.0.0\x1b[2J"},
		},
		Skipped: []rule_versions.UnsubmitSkippedGroup{
			{
				Reason: "locked \x1b[31mred",
				Rules:  []rule_versions.UnsubmitResultItem{{Name: "phone\x1b[1m", Release: "2.0.0"}},
			},
		},
	}

	var buf bytes.Buffer
	displayUnsubmitResult(&buf, result, 0)
	out := buf.String()

	if strings.ContainsRune(out, '\x1b') {
		t.Errorf("raw ESC reached the terminal: %q", out)
	}
	for _, want := range []string{
		`removed \x1b[31mred`,
		`email\x1b]0;pwned\x07`,
		`1.0.0\x1b[2J`,
		`locked \x1b[31mred`,
		`phone\x1b[1m`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing visible escape %q, got:\n%s", want, out)
		}
	}
}

// The delete banner is the server's own description of what is about to go
// away, printed before the confirmation prompt.
func TestPrintDeleteContextSanitizesServerText(t *testing.T) {
	file := &stored_items.StoredItemFull{
		ID: 7, FileName: "invoices\x1b[31m.csv", DatasetID: 42,
		DatasetName: "orders\x1b]0;pwned\x07", DataSourceModelID: 3,
		DataSourceModelName: "s3\x1b[2J",
	}

	var buf strings.Builder
	printDeleteContext(&buf, "Acme\x1b[1m", "org\x1b[0m", file)
	out := buf.String()

	if strings.ContainsRune(out, '\x1b') {
		t.Errorf("raw ESC reached the terminal: %q", out)
	}
	for _, want := range []string{
		`invoices\x1b[31m.csv`,
		`orders\x1b]0;pwned\x07`,
		`s3\x1b[2J`,
		`Acme\x1b[1m`,
		`org\x1b[0m`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing visible escape %q, got:\n%s", want, out)
		}
	}
}

// runDeleteRules runs `delete rules` against a logged-in mock whose unsubmit
// route answers each call with the next of answers, and returns stdout and
// stderr apart.
func runDeleteRules(t *testing.T, answers []map[string]any, args ...string) (string, string, error) {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	calls := 0
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-revisions/unsubmit",
		func(w http.ResponseWriter, _ *http.Request) {
			answer := answers[min(calls, len(answers)-1)]
			calls++
			testutil.RespondJSON(w, http.StatusOK, answer)
		})

	cmd := setupDeleteRulesTestCommand()
	cmd.SetArgs(append([]string{"delete", "rules"}, args...))
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	err := cmd.Execute()
	return stdout.String(), stderr.String(), err
}

func writeRulesFile(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("class Rule: pass\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func unsubmitAnswer(revisionID string) map[string]any {
	return map[string]any{
		"message":   "1 rule deleted",
		"deleted":   []map[string]any{{"name": "email_validator", "release": "1.0.0", "revision_id": revisionID}},
		"not_found": []map[string]any{{"name": "gone", "release": "2.0.0", "revision_id": nil}},
		"skipped":   []map[string]any{},
	}
}

// -o json prints the server's answer with every list present, and the prose
// moves to stderr.
func TestDeleteRulesWritesTheAnswer(t *testing.T) {
	stdout, stderr, err := runDeleteRules(t, []map[string]any{unsubmitAnswer(testRevisionUUID)},
		"-f", writeRulesFile(t, "rules.py"), "--yes", "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stderr, "Deleted:") || !strings.Contains(stderr, "Organization") {
		t.Errorf("stderr missing the prose:\n%s", stderr)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
	}
	deleted, _ := got["deleted"].([]any)
	if got["message"] != "1 rule deleted" || len(deleted) != 1 {
		t.Errorf("document = %v, want the answer", got)
	}
	if skipped, ok := got["skipped"].([]any); !ok || len(skipped) != 0 {
		t.Errorf("skipped = %#v, want [] as the server sends it", got["skipped"])
	}
}

// Two files print one List, in file order; -o name prints rule/<revision-id>
// for each deleted revision across both.
func TestDeleteRulesSeveralFilesPrintOneList(t *testing.T) {
	second := "aaaa0000-bbbb-cccc-dddd-eeeeeeee0002"
	answers := []map[string]any{unsubmitAnswer(testRevisionUUID), unsubmitAnswer(second)}
	files := []string{"-f", writeRulesFile(t, "a.py"), "-f", writeRulesFile(t, "b.py"), "--yes"}

	stdout, _, err := runDeleteRules(t, answers, append(files, "-o", "json")...)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var list struct {
		Kind  string           `json:"kind"`
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal([]byte(stdout), &list); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
	}
	if list.Kind != "List" || len(list.Items) != 2 {
		t.Fatalf("got %+v, want a List of two answers", list)
	}

	stdout, _, err = runDeleteRules(t, answers, append(files, "-o", "name")...)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "rule/" + testRevisionUUID + "\nrule/" + second + "\n"
	if stdout != want {
		t.Errorf("-o name stdout = %q, want %q", stdout, want)
	}
}
