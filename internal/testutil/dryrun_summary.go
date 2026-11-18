package testutil

import (
	"encoding/json"
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

// Dry-run structured summaries exactly as an atlas server newer than 2.118.0
// serializes them. A stat whose rate is valid carries no rate_valid or
// not_estimable_reason key, and each summary carries its mode. Validity is
// decided per job, so every stat in one summary is valid or none is.
const (
	// DryRunSummarySingleAllValid is a single-mode summary whose rates are
	// valid.
	DryRunSummarySingleAllValid = `{"mode": "single", "total_eligible": 1000, "clean": {"rate": 1.0, "count": 1000}, "warning": {"rate": 0.0, "count": 0}, "blocker": {"rate": 0.0, "count": 0}, "top_blocker_reasons": [], "is_nondeterministic": false}`

	// DryRunSummarySingleAllInvalid is a single-mode summary whose rates are
	// not estimable, so each count is the observed row count.
	DryRunSummarySingleAllInvalid = `{"mode": "single", "total_eligible": 1000, "clean": {"rate": 1.0, "count": 960, "rate_valid": false, "not_estimable_reason": "premium stratum not sampled"}, "warning": {"rate": 0.0, "count": 28, "rate_valid": false, "not_estimable_reason": "premium stratum not sampled"}, "blocker": {"rate": 0.0, "count": 12, "rate_valid": false, "not_estimable_reason": "premium stratum not sampled"}, "top_blocker_reasons": [], "is_nondeterministic": false}`

	// DryRunSummaryPairIndependentAllInvalid is an independent pair-mode
	// summary whose rates are not estimable.
	DryRunSummaryPairIndependentAllInvalid = `{"mode": "pair", "comparison_kind": "independent", "total_eligible": 1000, "blockers": {"baseline": {"rate": 0.0, "count": 0, "rate_valid": false, "not_estimable_reason": "premium stratum not sampled"}, "candidate": {"rate": 0.0, "count": 0, "rate_valid": false, "not_estimable_reason": "premium stratum not sampled"}, "delta_rate": 0.0, "is_significant": false, "delta_valid": false, "observed_delta": null}, "warnings": {"baseline": {"rate": 0.0, "count": 0, "rate_valid": false, "not_estimable_reason": "premium stratum not sampled"}, "candidate": {"rate": 0.0, "count": 0, "rate_valid": false, "not_estimable_reason": "premium stratum not sampled"}, "delta_rate": 0.0, "is_significant": false, "delta_valid": false, "observed_delta": null}, "transitions": {"unblocked": 3, "newly_blocked": 7}, "top_divergent_reasons": [], "field_divergence": [], "column_overlap": {"shared_fields": [], "left_only_fields": ["a"], "right_only_fields": ["b"]}, "is_nondeterministic": false}`
)

// DecodedValueAt decodes out as format ("json" or "yaml") and returns the
// value at path, failing t if any key on the path is missing.
func DecodedValueAt(t *testing.T, out []byte, format string, path ...string) interface{} {
	t.Helper()
	var tree interface{}
	var err error
	switch format {
	case "json":
		err = json.Unmarshal(out, &tree)
	case "yaml":
		err = yaml.Unmarshal(out, &tree)
	default:
		t.Fatalf("DecodedValueAt: unknown format %q", format)
	}
	if err != nil {
		t.Fatalf("decoding %s output: %v\n%s", format, err, out)
	}
	for i, key := range path {
		obj, ok := tree.(map[string]interface{})
		if !ok {
			t.Fatalf("%v is %T, not an object:\n%s", path[:i], tree, out)
		}
		tree, ok = obj[key]
		if !ok {
			t.Fatalf("key %v missing from output:\n%s", path[:i+1], out)
		}
	}
	return tree
}

// RequireSameJSONTree fails t unless got, re-encoded as JSON, decodes to the
// same tree as the JSON document want: the same keys, values and nulls.
func RequireSameJSONTree(t *testing.T, got interface{}, want string) {
	t.Helper()
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("encoding %T: %v", got, err)
	}
	var gotTree, wantTree interface{}
	if err := json.Unmarshal(gotJSON, &gotTree); err != nil {
		t.Fatalf("decoding re-encoded value: %v", err)
	}
	if err := json.Unmarshal([]byte(want), &wantTree); err != nil {
		t.Fatalf("decoding want: %v", err)
	}
	if !reflect.DeepEqual(gotTree, wantTree) {
		wantJSON, _ := json.Marshal(wantTree)
		t.Fatalf("tree differs from the wire\n got: %s\nwant: %s", gotJSON, wantJSON)
	}
}
