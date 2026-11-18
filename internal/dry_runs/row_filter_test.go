package dry_runs //nolint:revive // Matches the resource domain.

import (
	"encoding/json"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/pkg/manifest"
	"gopkg.in/yaml.v3"
)

func TestDryRunRowFilterPresenceAndStrictFields(t *testing.T) {
	for _, doc := range []string{
		`{"rule_revision_id":"id","row_filter":null}`,
		"rule_revision_id: id\nrow_filter: null\n",
	} {
		var spec CreateRuleRunSpec
		if err := manifest.StrictUnmarshal([]byte(doc), &spec); err != nil {
			t.Fatal(err)
		}
		if !spec.RowFilterSet || spec.RowFilter != nil {
			t.Fatalf("explicit null lost: %+v", spec)
		}
	}
	for _, doc := range []string{
		`{"rule_revision_id":"id","row_filter":{"conditions":[{"column_name":"status","operator":"eq","value":"completed"}]}}`,
		"rule_revision_id: id\nrow_filter:\n  predicate:\n    source: 'def applies(row): return row[\"amount\"] == decimal(\"0\")'\n    columns: [amount]\n",
	} {
		var spec CreateRuleRunSpec
		if err := manifest.StrictUnmarshal([]byte(doc), &spec); err != nil {
			t.Fatal(err)
		}
		if err := spec.RowFilter.Validate(false, "filter"); err != nil {
			t.Fatal(err)
		}
		if !spec.RowFilterSet {
			t.Fatal("filter presence lost")
		}
	}
	for _, doc := range []string{
		`{"rule_revision_id":"id","typo":true}`,
		"rule_revision_id: id\ntypo: true\n",
		`{"rule_revision_id":"id","row_filter":{"conditions":[{"column":"status","operator":"is_blank"}]}}`,
		"rule_revision_id: id\nrow_filter:\n  conditions:\n    - column: status\n      operator: is_blank\n",
	} {
		var spec CreateRuleRunSpec
		if err := manifest.StrictUnmarshal([]byte(doc), &spec); err == nil {
			t.Fatalf("unknown field accepted: %s", doc)
		}
	}
}

func TestDryRunCoverageKeepsLegacyAbsence(t *testing.T) {
	for _, tc := range []struct {
		summary string
		checked []int
	}{
		{`{"total_rows":100,"checked_rows":0}`, []int{0}},
		{`{"total_rows":100,"baseline_checked_rows":12,"candidate_checked_rows":23}`, []int{12, 23}},
		{`{"clean":{},"warning":{},"blocker":{}}`, nil},
	} {
		job := &api.DryRunJobFullSchema{}
		job.StructuredSummary = &api.DryRunJobFullSchema_StructuredSummary{}
		if err := json.Unmarshal([]byte(tc.summary), job.StructuredSummary); err != nil {
			t.Fatal(err)
		}
		view := APIResponseToManifest(job, 0)
		if len(view.Status.RowCoverage) != len(tc.checked) {
			t.Fatalf("coverage=%v", view.Status.RowCoverage)
		}
		for i, n := range tc.checked {
			if got := view.Status.RowCoverage[i]; got.CheckedRows != n || got.TotalRows != 100 || got.Position != i {
				t.Fatalf("coverage=%+v", got)
			}
		}
	}
}

func TestDryRunCoverageUsesStoredPositions(t *testing.T) {
	job := &api.DryRunJobFullSchema{
		RuleRunSpecs:      []api.RuleRunSpec{{Position: 8}, {Position: 3}},
		StructuredSummary: &api.DryRunJobFullSchema_StructuredSummary{},
	}
	if err := json.Unmarshal([]byte(`{"total_rows":100,"baseline_checked_rows":12,"candidate_checked_rows":23}`), job.StructuredSummary); err != nil {
		t.Fatal(err)
	}
	coverage := APIResponseToManifest(job, 0).Status.RowCoverage
	if len(coverage) != 2 || coverage[0].Position != 3 || coverage[1].Position != 8 {
		t.Fatalf("coverage=%+v", coverage)
	}
}

func TestDryRunRuleSpecAcceptsAnAliasOutsideTheSpec(t *testing.T) {
	var job struct {
		Name  string              `yaml:"name"`
		Specs []CreateRuleRunSpec `yaml:"specs"`
	}
	doc := "name: &revision revision-id\nspecs:\n  - rule_revision_id: *revision\n    row_filter:\n      conditions:\n        - column_name: status\n          operator: eq\n          value: completed\n"
	if err := yaml.Unmarshal([]byte(doc), &job); err != nil {
		t.Fatal(err)
	}
	if len(job.Specs) != 1 || job.Specs[0].RuleRevisionID == nil || *job.Specs[0].RuleRevisionID != "revision-id" || !job.Specs[0].RowFilterSet {
		t.Fatalf("aliased spec lost: %+v", job.Specs)
	}
}
