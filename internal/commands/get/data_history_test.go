package get

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/testutil"
)

func TestGetDataCellHistory(t *testing.T) {
	body, err := os.ReadFile("../../dataview/testdata/cell_history.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []bool{false, true} {
		for _, format := range []string{"table", "json", "yaml"} {
			name := "clean/" + format
			if bad {
				name = "quarantine/" + format
			}
			t.Run(name, func(t *testing.T) {
				args := []string{"1", "--show-row-logs", "-o", format}
				if bad {
					args = append(args, "--is-bad-data")
				}
				out := runData(t, args, func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Query().Get("show_row_logs") != "true" {
						t.Errorf("missing show_row_logs: %s", r.URL.RawQuery)
					}
					if got := r.URL.Query().Get("is_bad_data"); (got == "true") != bad {
						t.Errorf("is_bad_data = %q, want %v", got, bad)
					}
					serveBody(body)(w, r)
				})
				switch format {
				case "json":
					var got, want bytes.Buffer
					if err := json.Compact(&got, []byte(out)); err != nil {
						t.Fatal(err)
					}
					if err := json.Compact(&want, body); err != nil {
						t.Fatal(err)
					}
					if got.String() != want.String() {
						t.Errorf("JSON lost cell history:\n%s", out)
					}
				case "yaml":
					testutil.AssertYAMLReadsAsJSON(t, out, string(body))
				default:
					for _, want := range []string{
						"| 11 | flag-only@example.com [F] |",
						"## CELL HISTORY", "Checked by Bea (admin)", "Row approved by cam@example.com (member)",
						"Sent for approval: October fixes", "Flag alert cleared", `"baseline":{"upstream":9007199254740993}`,
					} {
						if !strings.Contains(out, want) {
							t.Errorf("missing %q:\n%s", want, out)
						}
					}
				}
			})
		}
	}
}
