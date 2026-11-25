package describe

import (
	"bytes"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/dataset_rules"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/rule_versions"
	"github.com/qlustered/qctl/internal/testutil"
)

const predecessorRuleID = "550e8400-e29b-41d4-a716-446655440077"

func bindingPreviewDetail() rule_versions.RuleRevisionFull {
	detail := sampleRuleDetail()
	detail.AffectedColumns = []string{"domain", "email", "note"}
	source := func(s api.RuleFieldBindingSource) *api.RuleFieldBindingSource { return &s }
	column := func(s string) *string { return &s }
	fields := []api.RuleFieldColumnCandidatesSchema{
		{
			RuleFieldName: "domain",
			Required:      true,
			Status:        api.Ambiguous,
			Candidates: []api.RuleFieldColumnCandidateSchema{
				{Column: "domain_a", Reason: "type ok; original name 'domain'"},
				{Column: "domain_b", Reason: "type ok; shares 'domain' with 'domain'"},
			},
		},
		{
			RuleFieldName: "email",
			Required:      true,
			Status:        api.Bound,
			BoundTo:       column("email_address"),
			Source:        source(api.RuleFieldBindingSourceConcept),
		},
		{
			RuleFieldName: "note",
			Required:      false,
			Status:        api.Unbound,
		},
	}
	detail.FieldColumnCandidates = &fields
	return detail
}

// runBindingPreview runs describe rule against a mock whose detail endpoint
// records its query and serves detail.
func runBindingPreview(t *testing.T, detail rule_versions.RuleRevisionFull, args ...string) (string, url.Values, error) {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	var query url.Values
	mock.RegisterHandler("GET", detailEndpoint, func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Query()
		testutil.RespondJSON(w, http.StatusOK, detail)
	})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/dataset-rules", func(w http.ResponseWriter, r *http.Request) {
		totalRows := 1
		page := 1
		rule := sampleDatasetRuleTinyForList()
		rule.ID = uuid.MustParse(predecessorRuleID)
		var results []dataset_rules.DatasetRuleTiny
		if r.URL.Query().Get("instance_name") == rule.InstanceName {
			results = append(results, rule)
		}
		testutil.RespondJSON(w, http.StatusOK, dataset_rules.DatasetRuleList{Results: results, TotalRows: &totalRows, Page: &page})
	})

	cmd := setupRuleTestCommand()
	cmd.SetArgs(append([]string{"describe", "rule", testRuleRevisionID}, args...))
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	err := cmd.Execute()
	return buf.String(), query, err
}

func TestDescribeRuleBindingPreviewSendsDatasetIDAndRendersEachField(t *testing.T) {
	out, query, err := runBindingPreview(t, bindingPreviewDetail(), "--table-id", "42")
	if err != nil {
		t.Fatalf("Execute() error = %v\n%s", err, out)
	}
	if got := query.Get("dataset_id"); got != "42" {
		t.Errorf("dataset_id = %q, want 42", got)
	}
	if query.Has("upgraded_from") {
		t.Errorf("upgraded_from sent without --upgraded-from: %q", query.Get("upgraded_from"))
	}
	for _, want := range []string{
		"Table bindings (table 42):\n",
		"  FIELD   REQUIRED  STATUS     BOUND TO       SOURCE\n",
		"  domain  yes       ambiguous  -              -\n",
		"    domain_a — type ok; original name 'domain'\n",
		"    domain_b — type ok; shares 'domain' with 'domain'\n",
		"  email   yes       bound      email_address  concept\n",
		"  note    no        unbound    -              -\n",
		"    (no candidate columns)\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestDescribeRuleBindingPreviewJSONCarriesFieldColumnCandidates(t *testing.T) {
	out, query, err := runBindingPreview(t, bindingPreviewDetail(), "--table-id", "42", "-o", "json")
	if err != nil {
		t.Fatalf("Execute() error = %v\n%s", err, out)
	}
	if got := query.Get("dataset_id"); got != "42" {
		t.Errorf("dataset_id = %q, want 42", got)
	}
	for _, want := range []string{`"field_column_candidates"`, `"status": "ambiguous"`, `"bound_to": "email_address"`} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestDescribeRuleBindingPreviewUpgradedFromSendsThePredecessorUUID(t *testing.T) {
	detail := bindingPreviewDetail()
	inherited := api.RuleFieldBindingSourceInherited
	(*detail.FieldColumnCandidates)[1].Source = &inherited

	for _, ref := range []string{predecessorRuleID, "email_check"} {
		t.Run(ref, func(t *testing.T) {
			out, query, err := runBindingPreview(t, detail, "--table-id", "42", "--upgraded-from", ref)
			if err != nil {
				t.Fatalf("Execute() error = %v\n%s", err, out)
			}
			if got := query.Get("upgraded_from"); got != predecessorRuleID {
				t.Errorf("upgraded_from = %q, want %s", got, predecessorRuleID)
			}
			if got := query.Get("dataset_id"); got != "42" {
				t.Errorf("dataset_id = %q, want 42", got)
			}
			if !strings.Contains(out, "  email   yes       bound      email_address  inherited\n") {
				t.Errorf("output does not show the inherited binding:\n%s", out)
			}
		})
	}
}

func TestDescribeRuleUpgradedFromWithoutATableIsBadUsage(t *testing.T) {
	for _, args := range [][]string{
		{"--upgraded-from", predecessorRuleID},
		{"--upgraded-from", "", "--table-id", "42"},
	} {
		out, query, err := runBindingPreview(t, bindingPreviewDetail(), args...)
		if err == nil {
			t.Fatalf("%v: Execute() succeeded, want a usage error\n%s", args, out)
		}
		if code := qerrors.GetExitCode(err); code != 2 {
			t.Errorf("%v: exit code = %d, want 2 (%v)", args, code, err)
		}
		if query != nil {
			t.Errorf("%v: request sent before the usage error: %v", args, query)
		}
	}
}

func TestDescribeRuleBindingPreviewSanitizesServerText(t *testing.T) {
	detail := bindingPreviewDetail()
	(*detail.FieldColumnCandidates)[0].Candidates[0] = api.RuleFieldColumnCandidateSchema{
		Column: "dom\u202eain",
		Reason: "type ok\x1b[2J\nFAKE ROW",
	}
	out, _, err := runBindingPreview(t, detail, "--table-id", "42")
	if err != nil {
		t.Fatalf("Execute() error = %v\n%s", err, out)
	}
	if strings.ContainsAny(out, "\x1b\u202e") {
		t.Errorf("output carries a raw escape or bidi control:\n%q", out)
	}
	if !strings.Contains(out, `    dom\u202eain — type ok\x1b[2J\nFAKE ROW`+"\n") {
		t.Errorf("candidate not rendered as one escaped line:\n%s", out)
	}
}

func TestDescribeRuleWithoutATableSendsNoDatasetID(t *testing.T) {
	detail := sampleRuleDetail()
	out, query, err := runBindingPreview(t, detail)
	if err != nil {
		t.Fatalf("Execute() error = %v\n%s", err, out)
	}
	if len(query) != 0 {
		t.Errorf("query = %v, want none", query)
	}
	if want := rule_versions.FormatDescribe(&detail, false); out != want {
		t.Errorf("output differs from FormatDescribe:\ngot:\n%s\nwant:\n%s", out, want)
	}
	if strings.Contains(out, "Table bindings") {
		t.Errorf("output shows a binding section without a table:\n%s", out)
	}
}
