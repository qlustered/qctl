package testutil

import (
	"encoding/json"
	"testing"
)

// The passthrough tests compare command output against these fixtures, so a
// fixture that lost its validity keys would let them pass while pinning
// nothing. Hold each fixture to the shape its name promises.
func TestDryRunSummaryFixturesCarryTheirValidityShape(t *testing.T) {
	decode := func(doc string) map[string]interface{} {
		t.Helper()
		var tree map[string]interface{}
		if err := json.Unmarshal([]byte(doc), &tree); err != nil {
			t.Fatalf("fixture is not JSON: %v", err)
		}
		return tree
	}
	obj := func(tree map[string]interface{}, key string) map[string]interface{} {
		t.Helper()
		v, ok := tree[key].(map[string]interface{})
		if !ok {
			t.Fatalf("%q is %T, not an object", key, tree[key])
		}
		return v
	}
	requireAbsent := func(tree map[string]interface{}, where string, keys ...string) {
		t.Helper()
		for _, k := range keys {
			if _, ok := tree[k]; ok {
				t.Errorf("%s carries %q; a current server omits it there", where, k)
			}
		}
	}
	requireValue := func(tree map[string]interface{}, where, key string, want interface{}) {
		t.Helper()
		got, ok := tree[key]
		if !ok {
			t.Errorf("%s lacks %q", where, key)
			return
		}
		if got != want {
			t.Errorf("%s.%s = %#v, want %#v", where, key, got, want)
		}
	}

	allValid := decode(DryRunSummarySingleAllValid)
	requireValue(allValid, "all-valid summary", "mode", "single")
	for _, stat := range []string{"clean", "warning", "blocker"} {
		requireAbsent(obj(allValid, stat), "all-valid "+stat, "rate_valid", "not_estimable_reason")
	}

	single := decode(DryRunSummarySingleAllInvalid)
	requireValue(single, "single summary", "mode", "single")
	for stat, observed := range map[string]float64{"clean": 960, "warning": 28, "blocker": 12} {
		s := obj(single, stat)
		requireValue(s, stat, "rate_valid", false)
		requireValue(s, stat, "not_estimable_reason", "premium stratum not sampled")
		requireValue(s, stat, "count", observed)
	}

	pair := decode(DryRunSummaryPairIndependentAllInvalid)
	requireValue(pair, "pair summary", "mode", "pair")
	for _, comparison := range []string{"blockers", "warnings"} {
		c := obj(pair, comparison)
		requireValue(c, comparison, "delta_valid", false)
		requireValue(c, comparison, "is_significant", false)
		requireValue(c, comparison, "observed_delta", nil)
		for _, side := range []string{"baseline", "candidate"} {
			requireValue(obj(c, side), comparison+"."+side, "rate_valid", false)
		}
	}
}
