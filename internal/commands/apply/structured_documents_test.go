package apply

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/testutil"
	"gopkg.in/yaml.v3"
)

// The kinds that printed only prose return a document under -o json|yaml, and
// their prose moves to stderr.

// A TableSchema document carries the migration the PATCH answered and the
// review alert its redirect names.
func TestApplyTableSchemaJSONNamesTheMigrationAlert(t *testing.T) {
	_, mock := setupTableSchemaApplyEnv(t)
	registerSchemaPatch(t, mock, http.StatusOK, tableSchemaPatchOK)

	stdout, stderr, err := execApplyStreams(t, writeManifest(t, retypeAmount), "--yes", "-o", "json")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
	}
	if got["name"] != "orders" || got["id"] != float64(42) || got["action"] != "updated" ||
		got["alert_id"] != float64(99) || got["redirect_post_submit_to"] != "/datasets/42/alerts/99" {
		t.Errorf("document = %v", got)
	}
	if _, ok := got["migration"].(map[string]any); !ok {
		t.Errorf("document has no migration object: %v", got)
	}
	if !strings.Contains(stderr, "table-schema/orders updated") {
		t.Errorf("stderr lacks the result line:\n%s", stderr)
	}
}

// A review policy's document is the PUT answer.
func TestApplyRuleReviewPolicyYAMLIsThePutAnswer(t *testing.T) {
	env, mock := setupPolicyTestEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-review-policy", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, effectiveRulePolicyJSON("system_default"))
	})
	mock.RegisterHandler("PUT", "/api/orgs/"+testOrgID+"/rule-review-policy", func(w http.ResponseWriter, _ *http.Request) {
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

	stdout, stderr, err := execApplyStreams(t, manifestPath, "-o", "yaml")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	var got map[string]any
	if err := yaml.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not YAML: %v\n%s", err, stdout)
	}
	if got["id"] != "11111111-2222-3333-4444-555555555555" || got["min_reviewers"] != 2 {
		t.Errorf("document is not the PUT answer: %v", got)
	}
	if want := "rule-review-policy/org-rule-review-policy created (organization)\n"; stderr != want {
		t.Errorf("stderr = %q, want %q", stderr, want)
	}
}

// A patched Rule's PATCH answers only {result}, so its document is the
// revision read back after it.
func TestApplyRuleJSONIsTheRevisionReadBack(t *testing.T) {
	env, mock := setupRuleTest(t)
	defer env.Cleanup()
	defer mock.Close()
	registerRuleGetHandler(mock, testRuleID, sampleLiveRule())
	registerRulePatchHandler(mock, testRuleID, nil)
	manifestPath := env.CreateFile("rule.yaml", `
apiVersion: qluster.ai/v1
kind: Rule
metadata:
  id: `+testRuleID+`
  name: check_email
spec:
  state: disabled
`)

	stdout, stderr, err := execApplyStreams(t, manifestPath, "-o", "json")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
	}
	if got["id"] != testRuleID || got["name"] != "check_email" {
		t.Errorf("document is not the revision: %v", got)
	}
	if gets := countRequests(mock, "/rule-revisions/"+testRuleID+"/details"); gets != 2 {
		t.Errorf("revision reads = %d, want 2 (the diff and the read back)", gets)
	}
	if !strings.Contains(stderr, "patched") {
		t.Errorf("stderr lacks the patched line:\n%s", stderr)
	}
}
