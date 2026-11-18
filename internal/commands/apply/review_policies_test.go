package apply

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/testutil"
)

func effectiveRulePolicyJSON(source string) map[string]interface{} {
	return map[string]interface{}{
		"auto_enable_on_approval": false,
		"designated_reviewer_ids": []string{},
		"dismiss_stale_reviews":   false,
		"min_reviewers":           1,
		"policy_id":               nil,
		"require_dry_run":         false,
		"require_dry_run_success": false,
		"requires_review":         false,
		"source":                  source,
	}
}

func rulePolicyResponseJSON() map[string]interface{} {
	return map[string]interface{}{
		"id":                      "11111111-2222-3333-4444-555555555555",
		"organization_id":         testOrgID,
		"dataset_id":              nil,
		"requires_review":         true,
		"min_reviewers":           2,
		"designated_reviewer_ids": []string{},
		"dismiss_stale_reviews":   false,
		"require_dry_run":         false,
		"require_dry_run_success": false,
		"auto_enable_on_approval": false,
		"created_at":              "2026-06-10T00:00:00Z",
		"created_by_id":           "99999999-8888-7777-6666-555555555555",
		"updated_at":              "2026-06-10T00:00:00Z",
	}
}

func setupPolicyTestEnv(t *testing.T) (*testutil.TestEnv, *testutil.MockAPIServer) {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "token")
	return env, mock
}

func TestApplyRuleReviewPolicyOrgCreate(t *testing.T) {
	env, mock := setupPolicyTestEnv(t)

	// No explicit org policy yet: effective policy comes from system default.
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-review-policy", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, effectiveRulePolicyJSON("system_default"))
	})

	var received map[string]interface{}
	mock.RegisterHandler("PUT", "/api/orgs/"+testOrgID+"/rule-review-policy", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}
		testutil.RespondJSON(w, http.StatusOK, rulePolicyResponseJSON())
	})

	manifestPath := env.CreateFile("policy.yaml", `
apiVersion: qluster.ai/v1
kind: RuleReviewPolicy
metadata:
  name: org-rule-review-policy
spec:
  requires_review: true
  min_reviewers: 2
`)

	rootCmd := setupApplyRoot()
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"apply", "-f", manifestPath})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if got, want := received["requires_review"], true; got != want {
		t.Errorf("requires_review = %v, want %v", got, want)
	}
	if got, want := received["min_reviewers"], float64(2); got != want {
		t.Errorf("min_reviewers = %v, want %v", got, want)
	}
	// Omitted optional fields must be ABSENT from the payload.
	for _, key := range []string{"dismiss_stale_reviews", "require_dry_run", "require_dry_run_success", "auto_enable_on_approval", "designated_reviewer_ids"} {
		if _, present := received[key]; present {
			t.Errorf("omitted field %q must not be sent, got %v", key, received[key])
		}
	}
	if want := "rule-review-policy/org-rule-review-policy created (organization)\n"; stdout.String() != want {
		t.Errorf("output = %q, want %q", stdout.String(), want)
	}
}

func TestApplyRuleReviewPolicyOrgUpdate(t *testing.T) {
	env, mock := setupPolicyTestEnv(t)

	// An explicit org policy already exists (source matches scope) => "updated".
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-review-policy", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, effectiveRulePolicyJSON("organization"))
	})
	mock.RegisterHandler("PUT", "/api/orgs/"+testOrgID+"/rule-review-policy", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, rulePolicyResponseJSON())
	})

	manifestPath := env.CreateFile("policy.yaml", `
apiVersion: qluster.ai/v1
kind: RuleReviewPolicy
metadata:
  name: org-rule-review-policy
spec:
  requires_review: true
  min_reviewers: 2
`)

	rootCmd := setupApplyRoot()
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"apply", "-f", manifestPath})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if want := "rule-review-policy/org-rule-review-policy updated (organization)\n"; stdout.String() != want {
		t.Errorf("output = %q, want %q", stdout.String(), want)
	}
}

func TestApplyRuleReviewPolicyDatasetCreateWhenInherited(t *testing.T) {
	env, mock := setupPolicyTestEnv(t)

	// Effective policy at dataset scope is inherited from the org:
	// applying pins explicit values at dataset scope => "created".
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/rule-review-policy", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, effectiveRulePolicyJSON("organization"))
	})
	putCalled := false
	mock.RegisterHandler("PUT", "/api/orgs/"+testOrgID+"/datasets/42/rule-review-policy", func(w http.ResponseWriter, r *http.Request) {
		putCalled = true
		testutil.RespondJSON(w, http.StatusOK, rulePolicyResponseJSON())
	})

	manifestPath := env.CreateFile("policy.yaml", `
apiVersion: qluster.ai/v1
kind: RuleReviewPolicy
metadata:
  name: orders-rule-review-policy
spec:
  dataset_id: 42
  requires_review: true
`)

	rootCmd := setupApplyRoot()
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"apply", "-f", manifestPath})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !putCalled {
		t.Fatal("expected PUT to dataset rule-review-policy endpoint")
	}
	if want := "rule-review-policy/orders-rule-review-policy created (table 42)\n"; stdout.String() != want {
		t.Errorf("output = %q, want %q", stdout.String(), want)
	}
}

func TestApplyRuleReviewPolicyDatasetUpdate(t *testing.T) {
	env, mock := setupPolicyTestEnv(t)

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/rule-review-policy", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, effectiveRulePolicyJSON("dataset"))
	})
	mock.RegisterHandler("PUT", "/api/orgs/"+testOrgID+"/datasets/42/rule-review-policy", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, rulePolicyResponseJSON())
	})

	manifestPath := env.CreateFile("policy.yaml", `
apiVersion: qluster.ai/v1
kind: RuleReviewPolicy
metadata:
  name: orders-rule-review-policy
spec:
  dataset_id: 42
  requires_review: true
`)

	rootCmd := setupApplyRoot()
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"apply", "-f", manifestPath})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if want := "rule-review-policy/orders-rule-review-policy updated (table 42)\n"; stdout.String() != want {
		t.Errorf("output = %q, want %q", stdout.String(), want)
	}
}

func TestApplyRuleReviewPolicyValidationFailure(t *testing.T) {
	env, _ := setupPolicyTestEnv(t)

	manifestPath := env.CreateFile("policy.yaml", `
apiVersion: qluster.ai/v1
kind: RuleReviewPolicy
metadata:
  name: bad-policy
spec:
  min_reviewers: 0
`)

	rootCmd := setupApplyRoot()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"apply", "-f", manifestPath})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if !strings.Contains(err.Error(), "between 1 and 10") {
		t.Errorf("expected min_reviewers error, got: %v", err)
	}
}

func TestApplyRuleReviewPolicyOrgCreateWhenNoPolicy(t *testing.T) {
	env, mock := setupPolicyTestEnv(t)

	// GET returns 404: no policy exists at any level => "created".
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-review-policy", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusNotFound, map[string]interface{}{"detail": "not found"})
	})
	mock.RegisterHandler("PUT", "/api/orgs/"+testOrgID+"/rule-review-policy", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, rulePolicyResponseJSON())
	})

	manifestPath := env.CreateFile("policy.yaml", `
apiVersion: qluster.ai/v1
kind: RuleReviewPolicy
metadata:
  name: org-rule-review-policy
spec:
  requires_review: true
`)

	rootCmd := setupApplyRoot()
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"apply", "-f", manifestPath})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if want := "rule-review-policy/org-rule-review-policy created (organization)\n"; stdout.String() != want {
		t.Errorf("output = %q, want %q", stdout.String(), want)
	}
}

func TestApplyRuleReviewPolicyPutServerError(t *testing.T) {
	env, mock := setupPolicyTestEnv(t)

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-review-policy", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, effectiveRulePolicyJSON("system_default"))
	})
	mock.RegisterHandler("PUT", "/api/orgs/"+testOrgID+"/rule-review-policy", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusUnprocessableEntity, map[string]interface{}{"detail": "min_reviewers exceeds organization seat count"})
	})

	manifestPath := env.CreateFile("policy.yaml", `
apiVersion: qluster.ai/v1
kind: RuleReviewPolicy
metadata:
  name: org-rule-review-policy
spec:
  requires_review: true
`)

	rootCmd := setupApplyRoot()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"apply", "-f", manifestPath})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error from failed PUT, got nil")
	}
	if !strings.Contains(err.Error(), "failed to apply rule review policy") {
		t.Errorf("expected wrapped apply error, got: %v", err)
	}
}

func dataPolicyResponseJSON(source string, withID bool) map[string]interface{} {
	resp := map[string]interface{}{
		"id":                       nil,
		"organization_id":          testOrgID,
		"dataset_id":               nil,
		"requires_review":          true,
		"min_reviewers":            2,
		"designated_reviewer_ids":  []string{},
		"auto_release_on_approval": false,
		"source":                   source,
	}
	if withID {
		resp["id"] = "22222222-3333-4444-5555-666666666666"
	}
	return resp
}

func TestApplyDataReviewPolicyOrgCreate(t *testing.T) {
	env, mock := setupPolicyTestEnv(t)

	// No explicit org policy: GET returns system-default values, no id.
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/data-review-policy", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, dataPolicyResponseJSON("system_default", false))
	})

	var received map[string]interface{}
	mock.RegisterHandler("PUT", "/api/orgs/"+testOrgID+"/data-review-policy", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}
		testutil.RespondJSON(w, http.StatusOK, dataPolicyResponseJSON("organization", true))
	})

	manifestPath := env.CreateFile("policy.yaml", `
apiVersion: qluster.ai/v1
kind: DataReviewPolicy
metadata:
  name: org-data-review-policy
spec:
  requires_review: true
  min_reviewers: 2
`)

	rootCmd := setupApplyRoot()
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"apply", "-f", manifestPath})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got, want := received["requires_review"], true; got != want {
		t.Errorf("requires_review = %v, want %v", got, want)
	}
	if got, want := received["min_reviewers"], float64(2); got != want {
		t.Errorf("min_reviewers = %v, want %v", got, want)
	}
	for _, key := range []string{"designated_reviewer_ids", "auto_release_on_approval"} {
		if _, present := received[key]; present {
			t.Errorf("omitted field %q must not be sent", key)
		}
	}
	if want := "data-review-policy/org-data-review-policy created (organization)\n"; stdout.String() != want {
		t.Errorf("output = %q, want %q", stdout.String(), want)
	}
}

func TestApplyDataReviewPolicyDatasetUpdate(t *testing.T) {
	env, mock := setupPolicyTestEnv(t)

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/data-review-policy", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, dataPolicyResponseJSON("dataset", true))
	})
	mock.RegisterHandler("PUT", "/api/orgs/"+testOrgID+"/datasets/42/data-review-policy", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, dataPolicyResponseJSON("dataset", true))
	})

	manifestPath := env.CreateFile("policy.yaml", `
apiVersion: qluster.ai/v1
kind: DataReviewPolicy
metadata:
  name: orders-data-review-policy
spec:
  dataset_id: 42
  requires_review: true
`)

	rootCmd := setupApplyRoot()
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"apply", "-f", manifestPath})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if want := "data-review-policy/orders-data-review-policy updated (table 42)\n"; stdout.String() != want {
		t.Errorf("output = %q, want %q", stdout.String(), want)
	}
}

func TestApplyDataReviewPolicyMissingRequiresReview(t *testing.T) {
	env, _ := setupPolicyTestEnv(t)

	manifestPath := env.CreateFile("policy.yaml", `
apiVersion: qluster.ai/v1
kind: DataReviewPolicy
metadata:
  name: bad-policy
spec:
  min_reviewers: 2
`)

	rootCmd := setupApplyRoot()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"apply", "-f", manifestPath})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if !strings.Contains(err.Error(), "requires_review is required") {
		t.Errorf("expected requires_review error, got: %v", err)
	}
}

func TestApplyDataReviewPolicyOrgCreateWhenNoPolicy(t *testing.T) {
	env, mock := setupPolicyTestEnv(t)

	// GET returns 404: no policy exists at any level => "created".
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/data-review-policy", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusNotFound, map[string]interface{}{"detail": "not found"})
	})
	mock.RegisterHandler("PUT", "/api/orgs/"+testOrgID+"/data-review-policy", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, dataPolicyResponseJSON("organization", true))
	})

	manifestPath := env.CreateFile("policy.yaml", `
apiVersion: qluster.ai/v1
kind: DataReviewPolicy
metadata:
  name: org-data-review-policy
spec:
  requires_review: true
`)

	rootCmd := setupApplyRoot()
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"apply", "-f", manifestPath})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if want := "data-review-policy/org-data-review-policy created (organization)\n"; stdout.String() != want {
		t.Errorf("output = %q, want %q", stdout.String(), want)
	}
}

func TestApplyDataReviewPolicyPutServerError(t *testing.T) {
	env, mock := setupPolicyTestEnv(t)

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/data-review-policy", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, dataPolicyResponseJSON("system_default", false))
	})
	mock.RegisterHandler("PUT", "/api/orgs/"+testOrgID+"/data-review-policy", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusInternalServerError, map[string]interface{}{"detail": "internal server error"})
	})

	manifestPath := env.CreateFile("policy.yaml", `
apiVersion: qluster.ai/v1
kind: DataReviewPolicy
metadata:
  name: org-data-review-policy
spec:
  requires_review: true
`)

	rootCmd := setupApplyRoot()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"apply", "-f", manifestPath})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error from failed PUT, got nil")
	}
	if !strings.Contains(err.Error(), "failed to apply data review policy") {
		t.Errorf("expected wrapped apply error, got: %v", err)
	}
}

// Both policy kinds echo the manifest's own metadata.name, which qctl did not
// author: an ESC sequence in it prints as a visible escape.
func TestApplyReviewPolicySanitizesTheAppliedName(t *testing.T) {
	for _, tc := range []struct {
		kind     string
		route    string
		manifest string
		want     string
	}{
		{
			kind:  "RuleReviewPolicy",
			route: "/api/orgs/" + testOrgID + "/rule-review-policy",
			manifest: `
apiVersion: qluster.ai/v1
kind: RuleReviewPolicy
metadata:
  name: "org-policy\x1b[31m"
spec:
  requires_review: true
`,
			want: `rule-review-policy/org-policy\x1b[31m created (organization)` + "\n",
		},
		{
			kind:  "DataReviewPolicy",
			route: "/api/orgs/" + testOrgID + "/data-review-policy",
			manifest: `
apiVersion: qluster.ai/v1
kind: DataReviewPolicy
metadata:
  name: "org-policy\x1b[31m"
spec:
  requires_review: true
`,
			want: `data-review-policy/org-policy\x1b[31m created (organization)` + "\n",
		},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			env, mock := setupPolicyTestEnv(t)
			mock.RegisterHandler("GET", tc.route, func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, effectiveRulePolicyJSON("system_default"))
			})
			mock.RegisterHandler("PUT", tc.route, func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, rulePolicyResponseJSON())
			})

			manifestPath := env.CreateFile("policy.yaml", tc.manifest)
			rootCmd := setupApplyRoot()
			var stdout bytes.Buffer
			rootCmd.SetOut(&stdout)
			rootCmd.SetArgs([]string{"apply", "-f", manifestPath})
			if err := rootCmd.Execute(); err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			if got := stdout.String(); strings.ContainsRune(got, '\x1b') || got != tc.want {
				t.Errorf("applied name was not sanitized: %q, want %q", got, tc.want)
			}
		})
	}
}
