package apply

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

func changePolicyResponseJSON(datasetID interface{}) map[string]interface{} {
	return map[string]interface{}{
		"id":                        "11111111-2222-3333-4444-555555555555",
		"organization_id":           testOrgID,
		"dataset_id":                datasetID,
		"require_distinct_approver": true,
		"operations":                map[string]interface{}{},
		"created_by_id":             "99999999-8888-7777-6666-555555555555",
		"created_at":                "2026-10-28T00:00:00Z",
		"updated_at":                "2026-10-28T00:00:00Z",
	}
}

func runApplyChangePolicy(t *testing.T, path string) (string, error) {
	t.Helper()
	rootCmd := setupApplyRoot()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"apply", "-f", path})
	err := rootCmd.Execute()
	return out.String(), err
}

func TestApplyChangeReviewPolicyOrgCreateExpandsAnchors(t *testing.T) {
	env, mock := setupPolicyTestEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/change-review-policy", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusNotFound, map[string]interface{}{"detail": map[string]interface{}{"msg": "No change review policy for this organization."}})
	})
	var received map[string]interface{}
	mock.RegisterHandler("PUT", "/api/orgs/"+testOrgID+"/change-review-policy", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}
		testutil.RespondJSON(w, http.StatusOK, changePolicyResponseJSON(nil))
	})

	path := env.CreateFile("policy.yaml", `
apiVersion: qluster.ai/v1
kind: ChangeReviewPolicy
metadata:
  name: org-change-review-policy
spec:
  require_distinct_approver: true
  operations:
    dataset_rule_patch:
      is_enabled: &ops-only {resolve: [ops], propose: [], approve: []}
      position: *ops-only
`)
	out, err := runApplyChangePolicy(t, path)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !strings.Contains(out, "change-review-policy/org-change-review-policy created (organization)") {
		t.Errorf("output = %q", out)
	}
	if received["require_distinct_approver"] != true {
		t.Errorf("require_distinct_approver = %v", received["require_distinct_approver"])
	}
	fields := received["operations"].(map[string]interface{})["dataset_rule_patch"].(map[string]interface{})
	if _, ok := fields["position"].(map[string]interface{}); !ok {
		t.Errorf("the alias was not sent as an explicit key: %#v", fields)
	}
}

func TestApplyChangeReviewPolicyTableUpdateInherits(t *testing.T) {
	env, mock := setupPolicyTestEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/change-review-policy", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, changePolicyResponseJSON(42))
	})
	var received map[string]interface{}
	mock.RegisterHandler("PUT", "/api/orgs/"+testOrgID+"/datasets/42/change-review-policy", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}
		testutil.RespondJSON(w, http.StatusOK, changePolicyResponseJSON(42))
	})

	path := env.CreateFile("policy.yaml", `
apiVersion: qluster.ai/v1
kind: ChangeReviewPolicy
metadata:
  name: table-42-change-review-policy
spec:
  dataset_id: 42
  operations: {}
`)
	out, err := runApplyChangePolicy(t, path)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !strings.Contains(out, "change-review-policy/table-42-change-review-policy updated (table 42)") {
		t.Errorf("output = %q", out)
	}
	if v, ok := received["require_distinct_approver"]; !ok || v != nil {
		t.Errorf("require_distinct_approver = %v (present %v), want null to inherit", v, ok)
	}
}

func TestApplyChangeReviewPolicyClientValidationSendsNothing(t *testing.T) {
	env, mock := setupPolicyTestEnv(t)

	path := env.CreateFile("policy.yaml", `
apiVersion: qluster.ai/v1
kind: ChangeReviewPolicy
metadata:
  name: org-change-review-policy
spec:
  require_distinct_approver: false
  operations:
    dataset_rule_patch:
      is_enabled: {resolve: [owner], propose: []}
`)
	out, err := runApplyChangePolicy(t, path)
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Fatalf("exit code = %d, want %d (err: %v)", got, qerrors.ExitBadUsage, err)
	}
	for _, want := range []string{
		"spec.operations.dataset_rule_patch.is_enabled.approve is required",
		`spec.operations.dataset_rule_patch.is_enabled.resolve holds unknown role "owner"`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q:\n%v\nout: %s", want, err, out)
		}
	}
	if reqs := mock.Requests(); len(reqs) != 0 {
		t.Errorf("sent requests before refusing: %v", reqs)
	}
}

func TestApplyChangeReviewPolicySurfacesTheServerPath(t *testing.T) {
	env, mock := setupPolicyTestEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/change-review-policy", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, changePolicyResponseJSON(nil))
	})
	msg := "operations.alert_resolve.invalid_value.no_action.approve cannot hold agent: the key modifies data"
	mock.RegisterHandler("PUT", "/api/orgs/"+testOrgID+"/change-review-policy", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusUnprocessableEntity, map[string]interface{}{
			"detail": map[string]interface{}{
				"msg": msg,
				"loc": []string{"body", "operations", "alert_resolve", "invalid_value", "no_action", "approve"},
			},
		})
	})

	path := env.CreateFile("policy.yaml", `
apiVersion: qluster.ai/v1
kind: ChangeReviewPolicy
metadata:
  name: org-change-review-policy
spec:
  require_distinct_approver: false
  operations:
    alert_resolve:
      invalid_value:
        no_action: {resolve: [agent], propose: [agent], approve: [agent]}
`)
	out, err := runApplyChangePolicy(t, path)
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Fatalf("exit code = %d, want %d (err: %v)", got, qerrors.ExitBadUsage, err)
	}
	if !strings.Contains(err.Error(), msg) {
		t.Errorf("error does not carry the server path:\n%v\nout: %s", err, out)
	}
}
