package submit

import (
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/commands/guide/guidetest"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/testutil"
)

// fromLine returns output from the first line that starts with prefix.
func fromLine(t *testing.T, output, prefix string) string {
	t.Helper()
	i := strings.Index("\n"+output, "\n"+prefix)
	if i < 0 {
		t.Fatalf("no line starts with %q in:\n%s", prefix, output)
	}
	return output[i:]
}

// Pins the tail of the submit output quoted under request-review "## 2. Submit
// the rule", from the result table to the end.
func TestGuideQuoteSubmitDraftBreadcrumb(t *testing.T) {
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
	guidetest.AssertQuoted(t, "request-review", fromLine(t, output, "NAME "))
}

// Pins the submit output quoted under submit-rules "## 2. Submit it": an org
// without a review policy, from the line after the confirmation prompt.
func TestGuideQuoteSubmitRulesReport(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-revisions/submit",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
				"message": "Successfully submitted 1 rule(s)",
				"added":   [][]interface{}{{"gross-amount-positive-rule", "1.0.0"}},
			})
		})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
				"results": []map[string]interface{}{{
					"id":               "550e8400-e29b-41d4-a716-446655440000",
					"family_id":        "660e8400-e29b-41d4-a716-446655440000",
					"name":             "GrossAmountPositiveRule",
					"slug":             "gross-amount-positive-rule",
					"release":          "1.0.0",
					"state":            "enabled",
					"is_builtin":       false,
					"is_caf":           false,
					"is_default":       true,
					"affected_columns": []string{"Gross Amount"},
					"created_at":       "2026-07-01T10:00:00Z",
					"updated_at":       "2026-07-01T10:00:00Z",
				}},
			})
		})

	output, err := runSubmitRules(t)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	guidetest.AssertQuoted(t, "submit-rules", fromLine(t, output, "Importing rules from "))
}

// Pins the --check output quoted under submit-rules "### Check before you
// submit", from the line after the file summary.
func TestGuideQuoteSubmitRulesCheck(t *testing.T) {
	mock := setupCheckEnv(t)
	mock.RegisterHandler("POST", checkPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"message": "Submit would accept this file: 1 added, 0 updated, 0 unchanged, 0 skipped",
			"rules": []map[string]interface{}{
				{"name": "gross-amount-positive-rule", "release": "1.0.0", "action": "added", "reason": nil},
			},
		})
	})

	output, err := runCheckRules(t)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	guidetest.AssertQuoted(t, "submit-rules", fromLine(t, output, "Checking rules from "))
}
