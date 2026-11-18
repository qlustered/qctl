package describe

import (
	"encoding/json"
	"testing"

	"github.com/qlustered/qctl/internal/testutil"
)

func TestDescribeFileRoutingAndContradictedFixes(t *testing.T) {
	pending, routed := true, false
	period, zero, count := "2026-Q3", 0, 12
	for _, tc := range []struct {
		name    string
		pending *bool
		period  *string
		count   *int
	}{
		{name: "plain"},
		{name: "waiting", pending: &pending},
		{name: "routed", pending: &routed, period: &period, count: &count},
		{name: "zero fixes", pending: &routed, period: &period, count: &zero},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := sampleStoredItemFull()
			file.RoutingPending, file.RoutingPeriodKey = tc.pending, tc.period
			file.ContradictedExternalFixesCount = tc.count
			jsonOut := runDescribeFile(t, setupFileDescribeTestCommand(), file, "describe", "file", "1", "-o", "json")
			yamlOut := runDescribeFile(t, setupFileDescribeTestCommand(), file, "describe", "file", "1")
			testutil.AssertYAMLReadsAsJSON(t, yamlOut, jsonOut)

			var manifest struct {
				Status map[string]any `json:"status"`
			}
			if err := json.Unmarshal([]byte(jsonOut), &manifest); err != nil {
				t.Fatal(err)
			}
			var wantPeriod, wantCount any
			if tc.period != nil {
				wantPeriod = *tc.period
			}
			if tc.count != nil {
				wantCount = float64(*tc.count)
			}
			for key, want := range map[string]any{
				"routing_pending":                   tc.pending != nil && *tc.pending,
				"routing_period_key":                wantPeriod,
				"contradicted_external_fixes_count": wantCount,
			} {
				if got, ok := manifest.Status[key]; !ok || got != want {
					t.Errorf("status.%s = %v, present=%v; want %v", key, got, ok, want)
				}
			}
		})
	}
}
