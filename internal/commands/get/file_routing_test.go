package get

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/stored_items"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestGetFilesRoutingColumns(t *testing.T) {
	pending, routed := true, false
	period, count := "2026-Q3", 12
	waiting := sampleStoredItemTiny(2, "waiting.csv")
	waiting.RoutingPending = &pending
	assigned := sampleStoredItemTiny(3, "assigned.csv")
	assigned.RoutingPending = &routed
	assigned.RoutingPeriodKey = &period
	assigned.ContradictedExternalFixesCount = &count
	results := []stored_items.StoredItemTiny{sampleStoredItemTiny(1, "plain.csv"), waiting, assigned}
	anyQuery := func(*testing.T, *http.Request) {}

	out := runGetFiles(t, anyQuery, results, "get", "files")
	rows := tableRows(out)
	if !strings.Contains(rows["ID"], "ROUTING") || !strings.Contains(rows["2"], "pending") || !strings.Contains(rows["3"], period) {
		t.Errorf("default routing column missing:\n%s", out)
	}
	if strings.Contains(rows["ID"], "CONTRADICTED-EXTERNAL-FIXES-COUNT") {
		t.Errorf("contradicted fix count became a default column:\n%s", out)
	}

	out = runGetFiles(t, anyQuery, results, "get", "files", "--columns", "id,routing")
	rows = tableRows(out)
	if rows["1"] != "" || rows["2"] != "pending" || rows["3"] != period {
		t.Errorf("routing states:\n%s", out)
	}

	out = runGetFiles(t, anyQuery, results, "get", "files", "--columns", "id,routing_pending,routing_period_key,contradicted_external_fixes_count")
	rows = tableRows(out)
	if rows["1"] != "false" || rows["2"] != "true" || rows["3"] != "false 2026-Q3 12" {
		t.Errorf("raw routing and fix columns:\n%s", out)
	}

	declared := cmdutil.DeclaredColumns(NewFilesCommand())
	for _, want := range []string{"routing", "routing_pending", "routing_period_key", "contradicted_external_fixes_count"} {
		if !slices.Contains(declared, want) {
			t.Errorf("%s missing from declared columns: %v", want, declared)
		}
	}
}

func TestGetFilesRoutingStructuredOutput(t *testing.T) {
	pending, routed := true, false
	period, zero := "2026-07", 0
	waiting := sampleStoredItemTiny(2, "waiting.csv")
	waiting.RoutingPending = &pending
	assigned := sampleStoredItemTiny(3, "assigned.csv")
	assigned.RoutingPending, assigned.RoutingPeriodKey = &routed, &period
	assigned.ContradictedExternalFixesCount = &zero
	results := []stored_items.StoredItemTiny{waiting, assigned}
	anyQuery := func(*testing.T, *http.Request) {}
	jsonOut := runGetFiles(t, anyQuery, results, "get", "files", "-o", "json")
	yamlOut := runGetFiles(t, anyQuery, results, "get", "files", "-o", "yaml")
	testutil.AssertYAMLReadsAsJSON(t, yamlOut, jsonOut)

	var list struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 2 {
		t.Fatalf("expected two files: %s", jsonOut)
	}
	for i, want := range []map[string]any{
		{"routing_pending": true, "routing_period_key": nil, "contradicted_external_fixes_count": nil},
		{"routing_pending": false, "routing_period_key": period, "contradicted_external_fixes_count": float64(0)},
	} {
		for key, value := range want {
			if got, ok := list.Items[i][key]; !ok || got != value {
				t.Errorf("file %d %s = %v, present=%v; want %v", i, key, got, ok, value)
			}
		}
		if _, ok := list.Items[i]["routing"]; ok {
			t.Errorf("computed routing column leaked into API output: %s", jsonOut)
		}
	}
}
