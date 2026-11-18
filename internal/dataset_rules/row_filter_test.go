package dataset_rules //nolint:revive // Matches the resource domain.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"gopkg.in/yaml.v3"
)

func TestRowFilterPatchPresence(t *testing.T) {
	for _, format := range []string{"yaml", "json"} {
		for _, value := range []string{"absent", "null", "object"} {
			t.Run(format+"/"+value, func(t *testing.T) {
				spec := map[string]any{"dataset_id": 42}
				if value == "null" {
					spec["row_filter"] = nil
				}
				if value == "object" {
					spec["row_filter"] = map[string]any{"description": "Completed rows", "conditions": []any{map[string]any{"column_name": "status", "operator": "eq", "value": "completed"}}}
				}
				doc := map[string]any{"apiVersion": "qluster.ai/v1", "kind": "TableRule", "metadata": map[string]any{"id": "rule-id"}, "spec": spec}
				var data []byte
				var err error
				if format == "yaml" {
					data, err = yaml.Marshal(doc)
				} else {
					data, err = json.Marshal(doc)
				}
				if err != nil {
					t.Fatal(err)
				}
				var m TableRuleApplyManifest
				// Both command loaders accept JSON through yaml.Unmarshal too.
				if err := yaml.Unmarshal(data, &m); err != nil {
					t.Fatal(err)
				}
				if err := ValidatePatchManifest(&m, "filter.yaml"); err != nil {
					t.Fatal(err)
				}
				patch := BuildPatchMap(&m)
				filter, present := patch["row_filter"]
				if present != (value != "absent") {
					t.Fatalf("presence = %t, patch=%v", present, patch)
				}
				if value == "null" && marshalPatch(t, patch) != `{"row_filter":null}` {
					t.Fatalf("clear = %v", filter)
				}
				if value == "object" && !strings.Contains(marshalPatch(t, patch), `"column_name":"status"`) {
					t.Fatalf("filter lost: %v", patch)
				}
			})
		}
	}
}

func TestRowFilterRoundTripAndDiff(t *testing.T) {
	var detail DatasetRuleDetail
	raw := `{"dataset_id":42,"row_filter":{"description":"Completed rows","conditions":[{"column_name":"status","operator":"eq","value":"completed","value_high":null,"value_list":null,"case_sensitive":false}],"predicate":null}}`
	if err := json.Unmarshal([]byte(raw), &detail); err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"yaml", "json"} {
		t.Run(format, func(t *testing.T) {
			exported := APIResponseToManifest(&detail)
			var data []byte
			var err error
			if format == "yaml" {
				data, err = yaml.Marshal(exported)
			} else {
				data, err = json.Marshal(exported)
			}
			if err != nil {
				t.Fatal(err)
			}
			var m TableRuleApplyManifest
			if err := yaml.Unmarshal(data, &m); err != nil {
				t.Fatal(err)
			}
			patch := BuildPatchMap(&m)
			changes := PatchFieldChanges(&detail, map[string]any{"row_filter": patch["row_filter"]})
			if len(changes) != 1 || changes[0].Changed {
				t.Fatalf("unedited filter drift: %+v", changes)
			}
			applied, staged := SplitPatchOutcome(map[string]any{"row_filter": patch["row_filter"]}, false)
			if len(applied) != 1 || applied[0] != "row_filter" || len(staged) != 0 {
				t.Fatalf("draft outcome: %v %v", applied, staged)
			}
			applied, staged = SplitPatchOutcome(map[string]any{"row_filter": patch["row_filter"]}, true)
			if len(applied) != 0 || len(staged) != 1 {
				t.Fatalf("candidate outcome: %v %v", applied, staged)
			}
		})
	}
}

func TestSavedRowFilterRequiresDescription(t *testing.T) {
	var m TableRuleApplyManifest
	if err := yaml.Unmarshal([]byte("spec:\n  dataset_id: 42\n  row_filter:\n    conditions:\n      - column_name: status\n        operator: is_blank\n"), &m); err != nil {
		t.Fatal(err)
	}
	if code := qerrors.GetExitCode(ValidatePatchManifest(&m, "filter.yaml")); code != qerrors.ExitBadUsage {
		t.Fatalf("exit=%d", code)
	}
}

func TestRowFilterExportNullRemovesLiveFilter(t *testing.T) {
	data, err := yaml.Marshal(APIResponseToManifest(&api.DatasetRuleDetailSchema{DatasetID: 42}))
	if err != nil {
		t.Fatal(err)
	}
	var m TableRuleApplyManifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if !m.RowFilterSet {
		t.Fatalf("unfiltered export must carry null: %s", data)
	}
}

func TestRowFilterPresenceFollowsYAMLMergePrecedence(t *testing.T) {
	for _, tc := range []struct {
		name string
		doc  string
		set  bool
	}{
		{"inherited null", "spec:\n  <<: &filter {row_filter: null}\n  dataset_id: 42\n", true},
		{"first spec wins", "<<: [{spec: {dataset_id: 42}}, {spec: {dataset_id: 42, row_filter: null}}]\n", false},
		{"first null wins", "<<: [{spec: {dataset_id: 42, row_filter: null}}, {spec: {dataset_id: 42}}]\n", true},
		{"explicit spec wins", "<<: {spec: {dataset_id: 42, row_filter: null}}\nspec: {dataset_id: 42}\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var m TableRuleApplyManifest
			if err := yaml.Unmarshal([]byte(tc.doc), &m); err != nil {
				t.Fatal(err)
			}
			_, set := BuildPatchMap(&m)["row_filter"]
			if set != tc.set {
				t.Fatalf("filter set=%t, want %t", set, tc.set)
			}
		})
	}
}

func TestRowFilterAcceptsAnOperandAliasOutsideTheFilter(t *testing.T) {
	var m TableRuleApplyManifest
	doc := "metadata: {instance_name: &status completed}\nspec:\n  dataset_id: 42\n  row_filter:\n    description: Completed rows\n    conditions:\n      - column_name: status\n        operator: eq\n        value: *status\n"
	if err := yaml.Unmarshal([]byte(doc), &m); err != nil {
		t.Fatal(err)
	}
	if !m.RowFilterSet || m.Spec.RowFilter.Conditions[0].Value == nil || *m.Spec.RowFilter.Conditions[0].Value != "completed" {
		t.Fatalf("aliased operand lost: %+v", m.Spec.RowFilter)
	}
}
