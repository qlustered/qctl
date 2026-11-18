package create

import (
	"encoding/json"
	"testing"

	"github.com/qlustered/qctl/internal/dry_runs"
	"gopkg.in/yaml.v3"
)

func TestLaunchRequestCarriesRowFilterAndNull(t *testing.T) {
	for _, value := range []string{"null", `{conditions: [{column_name: status, operator: eq, value: completed}]}`} {
		var manifest dry_runs.DryRunJobCreateManifest
		doc := "spec:\n  rule_run_specs:\n    - rule_revision_id: 550e8400-e29b-41d4-a716-446655440099\n      row_filter: " + value + "\n"
		if err := yaml.Unmarshal([]byte(doc), &manifest); err != nil {
			t.Fatal(err)
		}
		req, err := buildLaunchRequest(42, &manifest)
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			Specs []map[string]json.RawMessage `json:"rule_run_specs"`
		}
		if err := json.Unmarshal(data, &body); err != nil {
			t.Fatal(err)
		}
		raw, exists := body.Specs[0]["row_filter"]
		if !exists {
			t.Fatalf("filter dropped: %s", data)
		}
		if value == "null" && string(raw) != "null" {
			t.Fatalf("null=%s", raw)
		}
		if value != "null" {
			var filter map[string]any
			if err := json.Unmarshal(raw, &filter); err != nil {
				t.Fatal(err)
			}
			if _, ok := filter["conditions"]; !ok {
				t.Fatalf("conditions lost: %s", raw)
			}
		}
	}
}
