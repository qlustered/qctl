package apply

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/commands/guide/guidetest"
	"github.com/qlustered/qctl/internal/linked_tables"
	"github.com/qlustered/qctl/internal/testutil"
)

// Pins the two outcome lines under onboard-table "## 3. Apply": the guide's
// short manifest creates the table, then re-applying it changes nothing.
func TestGuideQuoteTableApplyOutcomes(t *testing.T) {
	for _, out := range applyShortTableManifestTwice(t, []string{"apply", "table", "-f"}, "") {
		guidetest.AssertQuoted(t, "onboard-table", out)
	}
}

// Pins the output under instantiate-rule "### Attaching several rules at
// once": a two-document file applied with --yes. The guide's documents name no
// rule, so the mock answers each nameless instantiate, in file order, with the
// name the server generates from that rule's family.
func TestGuideQuoteTableRuleMultiDocInstantiate(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	registerRuleRevisionDetails(mock)
	generated := []struct {
		name, id string
		mapping  map[string]string
		source   api.RuleFieldBindingSource
	}{
		{"Pickup Before Destination", "283e9b44-1835-4b69-8417-2be0386493cc", map[string]string{"actual_start": "actual_start", "actual_end": "actual_end"}, api.RuleFieldBindingSourceOriginalName},
		{"Vehicle Not Ancient", "9c1f0e22-77a4-4c8e-9f31-6b2d4e7a1f05", map[string]string{"value": "vehicle_year"}, api.RuleFieldBindingSourceConcept},
	}
	posts := 0
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevID+"/instantiate", func(w http.ResponseWriter, r *http.Request) {
		var body api.InstantiateRuleJSONRequestBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.InstanceName != nil || posts >= len(generated) {
			t.Errorf("instantiate %d: want a nameless request, got instance_name %v (decode error: %v)", posts+1, body.InstanceName, err)
			return
		}
		rule := generated[posts]
		posts++
		created := sampleDatasetRuleDetailForApply()
		created.InstanceName = rule.name
		created.ID = uuid.MustParse(rule.id)
		created.DatasetID = body.DatasetID
		created.State = api.DatasetRuleStatePendingValidation
		queued := api.ValidationEvidenceKindQueued
		created.ValidationState = &queued
		created.ColumnMappingDict = rule.mapping
		bindings := []api.RuleFieldBindingSchema{}
		for field, column := range rule.mapping {
			bindings = append(bindings, api.RuleFieldBindingSchema{RuleFieldName: field, Required: true, Status: api.Bound, BoundTo: &column, Source: &rule.source})
		}
		created.FieldBindings = &bindings
		testutil.RespondJSON(w, http.StatusCreated, created)
	})

	// dataset_id 8 is the guide's table.
	registerTableRulesList(mock, 8)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/8/rule-suggestions", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.DatasetRuleSuggestionListSchema{Results: []api.DatasetRuleSuggestionTinySchema{}})
	})
	doc := strings.Replace(tableRuleInstantiateDoc("unnamed"), "dataset_id: 1\n", "dataset_id: 8\n", 1)
	doc = strings.Replace(doc, "  instance_name: unnamed\n", "", 1)
	if strings.Contains(doc, "instance_name") {
		t.Fatalf("the fixture still names its rule:\n%s", doc)
	}
	path := env.CreateFile("trips-table-rules.yaml", doc+"---\n"+doc)
	out, err := execApply(t, path, "--yes")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	guidetest.AssertQuoted(t, "instantiate-rule", out)
}

// Pins the enable recipe under link-tables "## 6. Enable it": re-applying a
// draft manifest whose revision already passed its dry run.
func TestGuideQuoteLinkedTableEnableRecipe(t *testing.T) {
	const revisionID = "5d0c2a4e-8b1f-4e3a-a6c9-7f2d1b0e4c33"
	var out bytes.Buffer
	writeLinkedTableApplied(&out, "orders-enriched", &linked_tables.ApplyResponse{
		Result: "unchanged", Release: "v1", RevisionState: "draft", DesiredState: "draft", Converged: true,
		CompositeSpecRevisionID: uuid.MustParse(revisionID),
	}, false)
	var next string
	for _, line := range strings.Split(out.String(), "\n") {
		if recipe, ok := strings.CutPrefix(line, "Next: "); ok {
			next = recipe
		}
	}
	next = strings.ReplaceAll(next, "orders-enriched", "<name>")
	guidetest.AssertQuoted(t, "link-tables", strings.ReplaceAll(next, revisionID, "<uuid>"))
}
