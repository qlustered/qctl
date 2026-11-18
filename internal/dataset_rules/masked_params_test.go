package dataset_rules //nolint:revive // Matches the resource domain.

import (
	"encoding/json"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"gopkg.in/yaml.v3"
)

func TestRefuseMaskedCarriedParamsNamesOnlyUntouchedMaskedKeys(t *testing.T) {
	live := map[string]interface{}{
		"a": "•••••", "b": "•••••abcd", "c": []interface{}{"•••••"}, "d": "plain", "e": "•••••", "f": "•••••",
	}
	err := RefuseMaskedCarriedParams("table-rule/x", live, map[string]interface{}{"a": 1}, []string{"e"})
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Fatalf("exit = %d (%v), want %d", code, err, qerrors.ExitBadUsage)
	}
	if !strings.Contains(err.Error(), "the value of: b, c, f.") {
		t.Errorf("error = %v, want it to name b, c and f only", err)
	}
	if err := RefuseMaskedCarriedParams("table-rule/x", live, map[string]interface{}{"a": 1, "b": 2, "c": 3, "f": 4}, []string{"e"}); err != nil {
		t.Errorf("every masked key edited: %v", err)
	}
}

// TestRoundTrippedMaskedRuleIsRefused: what a masked viewer's `get table-rule
// -o yaml` writes, re-applied, is refused before any request for its params
// and for its row filter alike.
func TestRoundTrippedMaskedRuleIsRefused(t *testing.T) {
	raw := `{"id":"0190aaaa-0000-4000-8000-000000000001","dataset_id":42,"instance_name":"r","severity":"warning",
		"params":{"max":"•••••","allowed":["•••••"]},
		"row_filter":{"description":"•••••rows","conditions":[{"column_name":"email","operator":"eq","value":"•••••.com","case_sensitive":false}]}}`
	for _, tc := range []struct {
		name, drop, want string
	}{
		{"params", "row_filter", "f.yaml holds masked text at spec.params.allowed[0], spec.params.max"},
		{"row_filter", "params", "spec.row_filter in f.yaml holds masked text at conditions[0].value, description"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body map[string]interface{}
			if err := json.Unmarshal([]byte(raw), &body); err != nil {
				t.Fatal(err)
			}
			delete(body, tc.drop)
			data, _ := json.Marshal(body)
			var detail DatasetRuleDetail
			if err := json.Unmarshal(data, &detail); err != nil {
				t.Fatal(err)
			}
			out, err := yaml.Marshal(APIResponseToManifest(&detail))
			if err != nil {
				t.Fatal(err)
			}
			var m TableRuleApplyManifest
			if err := yaml.Unmarshal(out, &m); err != nil {
				t.Fatal(err)
			}
			err = ValidatePatchManifest(&m, "f.yaml")
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit = %d (%v), want %d", code, err, qerrors.ExitBadUsage)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}
