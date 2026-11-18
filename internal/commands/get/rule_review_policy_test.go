package get

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/config"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
	"github.com/qlustered/qctl/internal/review_policies"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

func setupRuleReviewPolicyTestCommand() *cobra.Command {
	rootCmd := &cobra.Command{Use: "qctl"}
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "Output format")
	rootCmd.PersistentFlags().Bool("no-headers", false, "")
	rootCmd.PersistentFlags().String("columns", "", "")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "")
	rootCmd.PersistentFlags().CountP("verbose", "v", "")
	getCmd := &cobra.Command{Use: "get"}
	getCmd.AddCommand(NewRuleReviewPolicyCommand())
	rootCmd.AddCommand(getCmd)
	return rootCmd
}

func ruleReviewPolicyEffectiveJSON(source string) map[string]interface{} {
	return map[string]interface{}{
		"auto_enable_on_approval": true,
		"designated_reviewer_ids": []string{"7f3c2a9e-1111-2222-3333-444455556666"},
		"dismiss_stale_reviews":   true,
		"min_reviewers":           2,
		"policy_id":               "11111111-2222-3333-4444-555555555555",
		"require_dry_run":         true,
		"require_dry_run_success": false,
		"requires_review":         true,
		"source":                  source,
	}
}

func TestGetRuleReviewPolicyOrgTable(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-review-policy", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, ruleReviewPolicyEffectiveJSON("organization"))
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "token")

	cmd := setupRuleReviewPolicyTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"get", "rule-review-policy"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	for _, want := range []string{"organization", "true", "2", "SOURCE", "MIN-REVIEWERS"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("output missing %q:\n%s", want, buf.String())
		}
	}
}

func TestGetRuleReviewPolicyYAMLRoundTrip(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	// --table-id 42 is the ID, used as given, then fetches the dataset-scoped
	// policy.
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/rule-review-policy", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, ruleReviewPolicyEffectiveJSON("dataset"))
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "token")

	cmd := setupRuleReviewPolicyTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"get", "rule-review-policy", "--table-id", "42", "-o", "yaml"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// The YAML output must parse as an apply-compatible manifest.
	var m review_policies.RuleReviewPolicyManifest
	if err := pkgmanifest.StrictUnmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("yaml output is not apply-compatible: %v\n%s", err, buf.String())
	}
	if m.Kind != "RuleReviewPolicy" || m.APIVersion != pkgmanifest.APIVersionV1 {
		t.Errorf("bad kind/apiVersion: %s %s", m.Kind, m.APIVersion)
	}
	if m.Spec.DatasetID != 42 {
		t.Errorf("spec.dataset_id = %d, want 42", m.Spec.DatasetID)
	}
	if errs := m.Validate(); len(errs) != 0 {
		t.Errorf("round-tripped manifest fails validation: %v", errs)
	}
	if m.Status == nil || m.Status.Source != "dataset" {
		t.Errorf("status.source missing, got %+v", m.Status)
	}
}

func TestGetRuleReviewPolicyOrgYAML(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-review-policy", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, ruleReviewPolicyEffectiveJSON("organization"))
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "token")

	cmd := setupRuleReviewPolicyTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"get", "rule-review-policy", "-o", "yaml"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	var m review_policies.RuleReviewPolicyManifest
	if err := pkgmanifest.StrictUnmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("yaml output is not apply-compatible: %v\n%s", err, buf.String())
	}
	if m.Metadata.Name != "org-rule-review-policy" {
		t.Errorf("metadata.name = %q, want %q", m.Metadata.Name, "org-rule-review-policy")
	}
	if m.Spec.DatasetID != 0 {
		t.Errorf("spec.dataset_id = %d, want 0 (org manifest must not carry a dataset scope)", m.Spec.DatasetID)
	}
	if m.Status == nil || m.Status.Source != "organization" {
		t.Errorf("status.source missing or wrong, got %+v", m.Status)
	}
}

func TestGetRuleReviewPolicyInvalidTableID(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "token")

	cmd := setupRuleReviewPolicyTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"get", "rule-review-policy", "--table-id", "0"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected an error for --table-id 0, got nil")
	}
	if !strings.Contains(err.Error(), "--table-id must be a positive integer") {
		t.Errorf("expected error containing %q, got: %v", "--table-id must be a positive integer", err)
	}
	assertBadUsageExit(t, err)
}

func execGetRuleReviewPolicy(t *testing.T, args ...string) error {
	t.Helper()
	cmd := setupRuleReviewPolicyTestCommand()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(append([]string{"get", "rule-review-policy"}, args...))
	return cmd.Execute()
}

func TestGetRuleReviewPolicyTableFlagIsAName(t *testing.T) {
	mock := newPolicyTestEnv(t)
	var lookedUp string
	registerTableNameLookup(t, mock, &lookedUp)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/7/rule-review-policy",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, ruleReviewPolicyEffectiveJSON("dataset"))
		})

	if err := execGetRuleReviewPolicy(t, "--table", "42"); err != nil {
		t.Fatalf("--table must be looked up as a name: %v", err)
	}
	if lookedUp != "42" {
		t.Errorf("--table 42 must query name=42, queried %q", lookedUp)
	}
}

func TestGetRuleReviewPolicyTableIDFlag(t *testing.T) {
	mock := newPolicyTestEnv(t)
	rejectTableNameLookup(t, mock)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/7/rule-review-policy",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, ruleReviewPolicyEffectiveJSON("dataset"))
		})

	if err := execGetRuleReviewPolicy(t, "--table-id", "7"); err != nil {
		t.Fatalf("--table-id must select by ID: %v", err)
	}
}
