package get

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
	"github.com/qlustered/qctl/internal/review_policies"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

func setupChangeReviewPolicyTestCommand() *cobra.Command {
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
	getCmd.AddCommand(NewChangeReviewPolicyCommand())
	rootCmd.AddCommand(getCmd)
	return rootCmd
}

func changeReviewOperationsJSON() map[string]interface{} {
	return map[string]interface{}{
		"alert_resolve": map[string]interface{}{
			"invalid_value": map[string]interface{}{
				"no_action": map[string]interface{}{"resolve": []string{"admin", "ops"}, "propose": []string{"editor"}, "approve": []string{"admin"}},
			},
		},
		"dataset_rule_patch": map[string]interface{}{
			"is_enabled": map[string]interface{}{"resolve": []string{"ops"}, "propose": []string{}, "approve": []string{}},
		},
	}
}

func setupChangeReviewPolicyEnv(t *testing.T) *testutil.MockAPIServer {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "token")
	return mock
}

func getChangeReviewPolicyForTest(args ...string) (string, string, error) {
	cmd := setupChangeReviewPolicyTestCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs(append([]string{"get", "change-review-policy"}, args...))
	err := cmd.Execute()
	return stdout.String(), stderr.String(), err
}

func TestGetChangeReviewPolicyOrgTable(t *testing.T) {
	mock := setupChangeReviewPolicyEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/change-review-policy", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"id":                        "11111111-2222-3333-4444-555555555555",
			"organization_id":           testOrgID,
			"dataset_id":                nil,
			"require_distinct_approver": true,
			"operations":                changeReviewOperationsJSON(),
			"created_by_id":             "99999999-8888-7777-6666-555555555555",
			"created_at":                "2026-10-28T00:00:00Z",
			"updated_at":                "2026-10-28T00:00:00Z",
		})
	})

	stdout, stderr, err := getChangeReviewPolicyForTest()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	for _, want := range []string{"OPERATION", "KEY", "RESOLVE", "invalid_value/no_action", "admin,ops", "is_enabled"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "LISTED") {
		t.Errorf("the organization view has no LISTED column:\n%s", stdout)
	}
	if !strings.Contains(stderr, "require_distinct_approver: true") {
		t.Errorf("stderr summary missing:\n%s", stderr)
	}
}

func TestGetChangeReviewPolicyOrgMissingExitsNotFound(t *testing.T) {
	mock := setupChangeReviewPolicyEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/change-review-policy", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusNotFound, map[string]interface{}{"detail": map[string]interface{}{"msg": "No change review policy for this organization."}})
	})

	_, _, err := getChangeReviewPolicyForTest("-o", "yaml")
	if got := qerrors.GetExitCode(err); got != qerrors.ExitNotFound {
		t.Fatalf("exit code = %d, want %d (err: %v)", got, qerrors.ExitNotFound, err)
	}
}

func effectiveChangeReviewJSON(inForce bool) map[string]interface{} {
	if !inForce {
		return map[string]interface{}{
			"organization_id": testOrgID, "dataset_id": 42, "in_force": false,
			"source": nil, "organization_policy_id": nil, "dataset_policy_id": nil,
			"require_distinct_approver": nil, "operations": map[string]interface{}{}, "uncovered": map[string]interface{}{},
		}
	}
	return map[string]interface{}{
		"organization_id":           testOrgID,
		"dataset_id":                42,
		"in_force":                  true,
		"source":                    "dataset",
		"organization_policy_id":    "11111111-2222-3333-4444-555555555555",
		"dataset_policy_id":         "22222222-3333-4444-5555-666666666666",
		"require_distinct_approver": false,
		"operations":                changeReviewOperationsJSON(),
		"uncovered": map[string]interface{}{
			"alert_resolve":      [][]string{},
			"dataset_rule_patch": [][]string{{"position"}},
		},
	}
}

func TestGetChangeReviewPolicyEffectiveTableShowsUnlistedKeys(t *testing.T) {
	mock := setupChangeReviewPolicyEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/change-review-policy/effective", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, effectiveChangeReviewJSON(true))
	})

	stdout, stderr, err := getChangeReviewPolicyForTest("--table-id", "42")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !strings.Contains(stdout, "LISTED") || !strings.Contains(stdout, "position") {
		t.Errorf("stdout missing the unlisted key row:\n%s", stdout)
	}
	if !strings.Contains(stderr, "Source: dataset") || !strings.Contains(stderr, "unlisted keys: 1") {
		t.Errorf("stderr summary = %q", stderr)
	}
}

func TestGetChangeReviewPolicyEffectiveYAMLRoundTrips(t *testing.T) {
	mock := setupChangeReviewPolicyEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/change-review-policy/effective", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, effectiveChangeReviewJSON(true))
	})

	stdout, _, err := getChangeReviewPolicyForTest("--table-id", "42", "-o", "yaml")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	var m review_policies.ChangeReviewPolicyManifest
	if err := pkgmanifest.StrictUnmarshal([]byte(stdout), &m); err != nil {
		t.Fatalf("yaml output is not apply-compatible: %v\n%s", err, stdout)
	}
	if m.Kind != "ChangeReviewPolicy" || m.Spec.DatasetID != 42 {
		t.Errorf("kind/dataset_id = %s/%d", m.Kind, m.Spec.DatasetID)
	}
	if errs := m.Validate(); len(errs) != 0 {
		t.Errorf("round-tripped manifest fails validation: %v", errs)
	}
	if m.Status == nil || m.Status.Source != "dataset" || len(m.Status.Uncovered["dataset_rule_patch"]) != 1 {
		t.Errorf("status = %+v", m.Status)
	}
	// Keys print sorted: alert_resolve before dataset_rule_patch.
	if strings.Index(stdout, "alert_resolve:") > strings.Index(stdout, "dataset_rule_patch:") {
		t.Errorf("operations are not sorted:\n%s", stdout)
	}
}

func TestGetChangeReviewPolicyNotInForceExitsNotFound(t *testing.T) {
	mock := setupChangeReviewPolicyEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/change-review-policy/effective", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, effectiveChangeReviewJSON(false))
	})

	stdout, _, err := getChangeReviewPolicyForTest("--table-id", "42", "-o", "yaml")
	if got := qerrors.GetExitCode(err); got != qerrors.ExitNotFound {
		t.Fatalf("exit code = %d, want %d (err: %v)", got, qerrors.ExitNotFound, err)
	}
	if strings.Contains(stdout, "kind: ChangeReviewPolicy") {
		t.Errorf("printed a manifest for a policy not in force:\n%s", stdout)
	}
}

func TestGetChangeReviewPolicyTemplateDefaultsToYAML(t *testing.T) {
	mock := setupChangeReviewPolicyEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/change-review-policy/template", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"require_distinct_approver": false,
			"operations":                changeReviewOperationsJSON(),
		})
	})

	stdout, _, err := getChangeReviewPolicyForTest("--template")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	var m review_policies.ChangeReviewPolicyManifest
	if err := pkgmanifest.StrictUnmarshal([]byte(stdout), &m); err != nil {
		t.Fatalf("template is not an apply-compatible manifest: %v\n%s", err, stdout)
	}
	if m.Metadata.Name != "org-change-review-policy" || m.Spec.DatasetID != 0 || m.Status != nil {
		t.Errorf("manifest = %+v", m)
	}
	if errs := m.Validate(); len(errs) != 0 {
		t.Errorf("template fails validation: %v", errs)
	}

	tableOut, _, err := getChangeReviewPolicyForTest("--template", "-o", "table")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !strings.Contains(tableOut, "OPERATION") {
		t.Errorf("explicit -o table did not print rows:\n%s", tableOut)
	}
}

func TestGetChangeReviewPolicyTemplateRefusesATableSelector(t *testing.T) {
	mock := setupChangeReviewPolicyEnv(t)

	_, _, err := getChangeReviewPolicyForTest("--template", "--table-id", "42")
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Fatalf("exit code = %d, want %d (err: %v)", got, qerrors.ExitBadUsage, err)
	}
	if reqs := mock.Requests(); len(reqs) != 0 {
		t.Errorf("sent requests before refusing: %v", reqs)
	}
}
