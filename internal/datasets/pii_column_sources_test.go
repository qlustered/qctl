package datasets

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"gopkg.in/yaml.v3"
)

const (
	phoneRuleID = "11111111-1111-1111-1111-111111111111"
	emailRuleID = "22222222-2222-2222-2222-222222222222"
)

// piiSourcesTable is standardFieldTable with a hand-kept list and the sources
// atlas reports for it: cust_email by hand and by a rule, phone by a rule only.
func piiSourcesTable() *DatasetFull {
	table := standardFieldTable()
	table.PiiColumns = []string{"cust_email"}
	table.EffectivePiiColumns = &[]string{"cust_email", "phone"}
	table.PiiColumnSources = &[]api.PiiColumnSourceSchema{
		{Column: "cust_email", ListedByHand: true, Rules: []api.PiiColumnRuleSchema{
			{DatasetRuleID: uuid.MustParse(emailRuleID), InstanceName: "email_check"},
		}},
		{Column: "phone", Rules: []api.PiiColumnRuleSchema{
			{DatasetRuleID: uuid.MustParse(phoneRuleID), InstanceName: "phone_check"},
		}},
	}
	return table
}

func TestFormatPiiColumnSources(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   []PiiColumnSource
		want string
	}{
		{"not reported", nil, "(not reported by this server)"},
		{"none", []PiiColumnSource{}, "none"},
		{"by hand", []PiiColumnSource{{Column: "a", ListedByHand: true}}, "a (listed by hand)"},
		{"one rule", []PiiColumnSource{{Column: "a", Rules: []PiiColumnRule{{InstanceName: "r1"}}}}, "a (rule r1)"},
		{
			"both, two rules, two columns",
			[]PiiColumnSource{
				{Column: "a", ListedByHand: true, Rules: []PiiColumnRule{{InstanceName: "r1"}, {InstanceName: "r2"}}},
				{Column: "b", ListedByHand: true},
			},
			"a (listed by hand, rules r1, r2); b (listed by hand)",
		},
		{"no reason sent", []PiiColumnSource{{Column: "a"}}, "a"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var in *[]PiiColumnSource
			if tc.in != nil {
				in = &tc.in
			}
			if got := FormatPiiColumnSources(in); got != tc.want {
				t.Errorf("FormatPiiColumnSources = %q, want %q", got, tc.want)
			}
		})
	}
}

// The manifest's status block carries effective_pii_columns and
// pii_column_sources under their wire names, never under spec, and leaves both
// out when the response carried none or an empty list.
func TestTableManifestStatusCarriesPiiColumnSources(t *testing.T) {
	manifest, err := APIResponseToManifest(piiSourcesTable())
	if err != nil {
		t.Fatal(err)
	}
	wantSources := []any{
		map[string]any{"column": "cust_email", "listed_by_hand": true, "rules": []any{
			map[string]any{"dataset_rule_id": emailRuleID, "instance_name": "email_check"},
		}},
		map[string]any{"column": "phone", "listed_by_hand": false, "rules": []any{
			map[string]any{"dataset_rule_id": phoneRuleID, "instance_name": "phone_check"},
		}},
	}
	for _, marshal := range []struct {
		name      string
		fn        func(any) ([]byte, error)
		unmarshal func([]byte, any) error
	}{{"yaml", yaml.Marshal, yaml.Unmarshal}, {"json", json.Marshal, json.Unmarshal}} {
		t.Run(marshal.name, func(t *testing.T) {
			data, err := marshal.fn(manifest)
			if err != nil {
				t.Fatal(err)
			}
			var doc map[string]any
			if err := marshal.unmarshal(data, &doc); err != nil {
				t.Fatal(err)
			}
			status, _ := doc["status"].(map[string]any)
			if got, want := status["effective_pii_columns"], []any{"cust_email", "phone"}; !reflect.DeepEqual(got, want) {
				t.Errorf("status.effective_pii_columns = %#v, want %#v\n%s", got, want, data)
			}
			if got := status["pii_column_sources"]; !reflect.DeepEqual(got, wantSources) {
				t.Errorf("status.pii_column_sources = %#v, want %#v\n%s", got, wantSources, data)
			}
			spec, _ := doc["spec"].(map[string]any)
			for _, key := range []string{"effective_pii_columns", "pii_column_sources"} {
				if _, inSpec := spec[key]; inSpec {
					t.Errorf("%s leaked into spec:\n%s", key, data)
				}
			}
		})
	}

	for name, mutate := range map[string]func(*DatasetFull){
		"absent": func(table *DatasetFull) { table.EffectivePiiColumns, table.PiiColumnSources = nil, nil },
		"empty": func(table *DatasetFull) {
			table.EffectivePiiColumns, table.PiiColumnSources = &[]string{}, &[]api.PiiColumnSourceSchema{}
		},
	} {
		t.Run(name, func(t *testing.T) {
			table := piiSourcesTable()
			mutate(table)
			manifest, err := APIResponseToManifest(table)
			if err != nil {
				t.Fatal(err)
			}
			data, err := yaml.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"effective_pii_columns", "pii_column_sources"} {
				if strings.Contains(string(data), key) {
					t.Errorf("a response with %s %s must export no %s:\n%s", name, key, key, data)
				}
			}
		})
	}
}

// Apply reads past status.effective_pii_columns and status.pii_column_sources:
// an export re-applies to a table whose rules have since changed with no change
// and no write, and when the manifest does change something, no write body
// carries either key.
func TestExportedPiiColumnSourcesAreIgnoredByApply(t *testing.T) {
	ids := map[string]uuid.UUID{
		"old-concept": uuid.MustParse("44444444-4444-4444-4444-444444444444"),
		"old-kind":    uuid.MustParse("55555555-5555-5555-5555-555555555555"),
	}
	exported, err := APIResponseToManifest(piiSourcesTable())
	if err != nil {
		t.Fatal(err)
	}
	live := piiSourcesTable()
	live.EffectivePiiColumns = &[]string{"cust_email"}
	live.PiiColumnSources = &[]api.PiiColumnSourceSchema{{Column: "cust_email", ListedByHand: true, Rules: []api.PiiColumnRuleSchema{}}}

	for _, marshal := range []struct {
		name string
		fn   func(any) ([]byte, error)
	}{{"yaml", yaml.Marshal}, {"json", json.Marshal}} {
		t.Run(marshal.name, func(t *testing.T) {
			data, err := marshal.fn(exported)
			if err != nil {
				t.Fatal(err)
			}
			manifest, err := ParseTableManifest(data)
			if err != nil {
				t.Fatalf("a manifest carrying the PII status keys must parse: %v\n%s", err, data)
			}
			var requests []string
			server := standardFieldServer(t, live, ids, &requests)
			defer server.Close()
			client := NewClient(server.URL, assignmentOrg, 0)
			plan, err := client.PrepareApply("token", manifest)
			if err != nil {
				t.Fatal(err)
			}
			if changes := plan.Changes(); len(changes) != 0 {
				t.Errorf("re-applying the export plans changes:\n%s", FormatTableFieldChanges(changes, "  "))
			}
			if plan.RequestBody() != nil || plan.TableKindsBody() != nil || plan.ColumnConceptsBody() != nil {
				t.Errorf("re-applying the export would send %v / %v / %v", plan.RequestBody(), plan.TableKindsBody(), plan.ColumnConceptsBody())
			}

			manifest.Spec.PiiColumns = &[]string{"cust_email", "phone"}
			plan, err = client.PrepareApply("token", manifest)
			if err != nil {
				t.Fatal(err)
			}
			if plan.RequestBody() == nil {
				t.Fatal("an edited spec.pii_columns planned no update")
			}
			assertNoPiiStatusKeys(t, "update", marshalToMap(t, plan.RequestBody()))
		})
	}

	t.Run("creates", func(t *testing.T) {
		manifest := &exported.TableManifest
		draft := &api.DataSetSchemaFullDraft{
			EffectivePiiColumns: &[]string{"cust_email"},
			PiiColumnSources:    &[]api.PiiColumnSourceSchema{{Column: "cust_email", ListedByHand: true}},
		}
		fromDefaults := defaultsToPostRequest(draft)
		if err := applyManifestOverrides(&fromDefaults, manifest); err != nil {
			t.Fatal(err)
		}
		assertNoPiiStatusKeys(t, "create from server defaults", marshalToMap(t, fromDefaults))
		fromManifest, err := manifestToPostRequest(manifest)
		if err != nil {
			t.Fatal(err)
		}
		assertNoPiiStatusKeys(t, "create from the manifest alone", marshalToMap(t, fromManifest))
	})
}

func assertNoPiiStatusKeys(t *testing.T, body string, got map[string]any) {
	t.Helper()
	for _, key := range []string{"effective_pii_columns", "pii_column_sources"} {
		if _, present := got[key]; present {
			t.Errorf("the %s body carries %s: %v", body, key, got)
		}
	}
}
