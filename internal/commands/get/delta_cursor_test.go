package get

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/stored_items"
	"github.com/qlustered/qctl/internal/testutil"
)

// delta_cursor is an optional column, and a delta file carries the
// Delta tag in the default view.
func TestGetFilesShowsTheDeltaCursor(t *testing.T) {
	isDelta, cursor := true, 1042
	delta := sampleStoredItemTiny(2, "delta.csv")
	delta.IsDelta, delta.DeltaCursor = &isDelta, &cursor
	results := []stored_items.StoredItemTiny{sampleStoredItemTiny(1, "full.csv"), delta}
	anyQuery := func(*testing.T, *http.Request) {}

	out := runGetFiles(t, anyQuery, results, "get", "files", "--columns", "id,delta_cursor")
	rows := tableRows(out)
	if rows["ID"] != "DELTA-CURSOR" || rows["1"] != "" || rows["2"] != "1042" {
		t.Errorf("--columns id,delta_cursor:\n%s", out)
	}

	out = runGetFiles(t, anyQuery, results, "get", "files")
	rows = tableRows(out)
	if strings.Contains(rows["ID"], "DELTA-CURSOR") {
		t.Errorf("delta_cursor became a default column:\n%s", out)
	}
	if !strings.Contains(rows["2"], "Delta") || strings.Contains(rows["1"], "Delta") {
		t.Errorf("the Delta tag is not on the delta file alone:\n%s", out)
	}
}

// tableRows keys each non-blank line of a rendered table by its first cell and
// maps it to the rest of the line, spaces collapsed.
func tableRows(out string) map[string]string {
	rows := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if cells := strings.Fields(line); len(cells) > 0 {
			rows[cells[0]] = strings.Join(cells[1:], " ")
		}
	}
	return rows
}

func TestGetCloudSourcesShowsTheAppliedCursor(t *testing.T) {
	ce := newCloudSourcesEnv(t)
	ce.mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/data-sources", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"results": []interface{}{
				map[string]interface{}{"id": 7, "name": "Airbyte CDC", "delta_applied_cursor": 1000},
			},
			"total_rows": 1, "page": 1,
		})
	})

	rootCmd := setupCloudSourcesTestCommand()
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"get", "cloud-sources", "--columns", "id,delta_applied_cursor"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rows := tableRows(stdout.String())
	if rows["ID"] != "DELTA-APPLIED-CURSOR" || rows["7"] != "1000" {
		t.Errorf("--columns id,delta_applied_cursor:\n%s", stdout.String())
	}
}
