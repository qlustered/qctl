package apply

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/dataset_rules"
	"github.com/qlustered/qctl/internal/testutil"
)

// An upgrade with no column_mapping reads the preview with upgraded_from, is
// not refused for the fields the predecessor binds, and shows them as
// inherited.
func TestApplyTableRuleInstantiate_UpgradeWithoutMappingInheritsBindings(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)

	revision := sampleRuleRevisionFull(nil)
	revision.InputColumns = []string{"email", "year"}
	registerUpgradePredecessor(mock, 1, revision.FamilyID, http.StatusOK)
	var previewedFrom []string
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevID+"/details", func(w http.ResponseWriter, r *http.Request) {
		from := r.URL.Query().Get("upgraded_from")
		previewedFrom = append(previewedFrom, from)
		answer := revision
		if from == testTableRuleID {
			answer.FieldColumnCandidates = &[]api.RuleFieldColumnCandidatesSchema{
				boundCandidate("email", "email_col", api.RuleFieldBindingSourceInherited),
				boundCandidate("year", "model_year", api.RuleFieldBindingSourceInherited),
			}
		} else {
			answer.FieldColumnCandidates = &[]api.RuleFieldColumnCandidatesSchema{
				unboundCandidate("email", true), unboundCandidate("year", true),
			}
		}
		testutil.RespondJSON(w, http.StatusOK, answer)
	})
	var request map[string]json.RawMessage
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevID+"/instantiate", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		created := sampleDatasetRuleDetailForApply()
		created.ColumnMappingDict = map[string]string{"email": "email_col", "year": "model_year"}
		inherited := api.RuleFieldBindingSourceInherited
		emailCol, modelYear := "email_col", "model_year"
		created.FieldBindings = &[]api.RuleFieldBindingSchema{
			{RuleFieldName: "email", Required: true, Status: api.Bound, BoundTo: &emailCol, Source: &inherited},
			{RuleFieldName: "year", Required: true, Status: api.Bound, BoundTo: &modelYear, Source: &inherited},
		}
		testutil.RespondJSON(w, http.StatusCreated, created)
	})

	out, err := execApply(t, env.CreateFile("upgrade.yaml", upgradeManifest(testTableRuleID)))
	if err != nil {
		t.Fatalf("apply upgrade: %v\n%s", err, out)
	}
	if len(previewedFrom) == 0 || previewedFrom[len(previewedFrom)-1] != testTableRuleID {
		t.Errorf("the binding preview was not read with upgraded_from: %q", previewedFrom)
	}
	if raw, sent := request["rule_column_mapping"]; sent && string(raw) != "null" {
		t.Errorf("rule_column_mapping must be left to the server, sent %s", raw)
	}
	for _, want := range []string{
		"Column Mapping   : email -> email_col (inherited), year -> model_year (inherited)\n",
		"  bindings    : email -> email_col (inherited), year -> model_year (inherited)\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n---\n%s", want, out)
		}
	}
}

// -o json writes the created rule as the server answered it, field_bindings
// included.
func TestApplyTableRuleInstantiate_JSONCarriesFieldBindings(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()

	registerRuleRevisionDetails(mock)
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevID+"/instantiate", func(w http.ResponseWriter, r *http.Request) {
		created := sampleDatasetRuleDetailForApply()
		normalized := api.RuleFieldBindingSourceNormalized
		column := "e_mail"
		created.FieldBindings = &[]api.RuleFieldBindingSchema{
			{RuleFieldName: "email", Required: true, Status: api.Bound, BoundTo: &column, Source: &normalized, Candidates: []api.RuleFieldColumnCandidateSchema{}},
		}
		testutil.RespondJSON(w, http.StatusCreated, created)
	})

	stdout, _, err := execApplyStreams(t, env.CreateFile("rule.yaml", tableRuleInstantiateDoc("email_check")), "--yes", "-o", "json")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	var doc struct {
		FieldBindings []api.RuleFieldBindingSchema `json:"field_bindings"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
	}
	if len(doc.FieldBindings) != 1 || doc.FieldBindings[0].BoundTo == nil || *doc.FieldBindings[0].BoundTo != "e_mail" ||
		doc.FieldBindings[0].Source == nil || *doc.FieldBindings[0].Source != api.RuleFieldBindingSourceNormalized {
		t.Errorf("field_bindings = %+v\n%s", doc.FieldBindings, stdout)
	}
}

// The preview speaks for the create only when every entry has a status, and
// only while no field the create maps itself frees a column the preview
// inherited for it.
func TestInstantiateBindingsUnbound(t *testing.T) {
	inheritedEmail := boundCandidate("email", "email_col", api.RuleFieldBindingSourceInherited)
	for name, tc := range map[string]struct {
		explicit   map[string]string
		candidates []api.RuleFieldColumnCandidatesSchema
		want       []string
	}{
		"required unbound": {
			candidates: []api.RuleFieldColumnCandidatesSchema{unboundCandidate("email", true), unboundCandidate("year", true)},
			want:       []string{"email", "year"},
		},
		"optional unbound": {
			candidates: []api.RuleFieldColumnCandidatesSchema{unboundCandidate("year", false)},
		},
		"ambiguous": {
			candidates: []api.RuleFieldColumnCandidatesSchema{conceptCandidate("year", "year", "a", "b")},
		},
		"explicit": {
			explicit:   map[string]string{"year": "model_year"},
			candidates: []api.RuleFieldColumnCandidatesSchema{unboundCandidate("year", true)},
		},
		"older server": {
			candidates: []api.RuleFieldColumnCandidatesSchema{{RuleFieldName: "year", Required: true}},
		},
		"inherited column freed by an explicit mapping": {
			explicit:   map[string]string{"email": "other_col"},
			candidates: []api.RuleFieldColumnCandidatesSchema{inheritedEmail, unboundCandidate("year", true)},
		},
		"inherited column kept by an explicit mapping": {
			explicit:   map[string]string{"email": "email_col"},
			candidates: []api.RuleFieldColumnCandidatesSchema{inheritedEmail, unboundCandidate("year", true)},
			want:       []string{"year"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			rev := sampleRuleRevisionFull(nil)
			rev.InputColumns = []string{"email", "year"}
			rev.FieldColumnCandidates = &tc.candidates
			b := newInstantiateBindings(&dataset_rules.TableRuleApplyManifest{}, &rev, tc.explicit)
			var got []string
			for _, binding := range b.unbound() {
				got = append(got, binding.RuleFieldName)
			}
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("unbound = %v, want %v", got, tc.want)
			}
			if err := b.refuseUnbound(manifestDoc{Source: "rule.yaml"}, 1); (err != nil) != (len(tc.want) > 0) {
				t.Errorf("refuseUnbound = %v, want a refusal: %v", err, len(tc.want) > 0)
			}
		})
	}
}
