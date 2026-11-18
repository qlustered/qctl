package root

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"gopkg.in/yaml.v3"
)

func sheetsJSON(t *testing.T, doc string) string {
	t.Helper()
	var value any
	if err := yaml.Unmarshal([]byte(doc), &value); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestDeliverableSheetValidationFailsBeforeAuth(t *testing.T) {
	for _, tc := range []struct{ sheet, message string }{
		{"table: 7\n      header_rows: []\n      data_start_row: 1048577\n", "spec.sheets.Data.data_start_row must be at most 1048576"},
		{"table: 7\n      header_rows: [2147483648]\n      data_start_row: 1048576\n", "spec.sheets.Data.header_rows[0] must be at most 1048576"},
		{"table: 7\n      header_rows: []\n      data_start_row: 1\n      columns: []\n", "spec.sheets.Data.columns: expected a mapping"},
		{"table: 7\n      header_rows: []\n      data_start_row: 1\n      totals: []\n", "spec.sheets.Data.totals: unknown field"},
		{"table: 7\n      header_rows: []\n      data_start_row: 1\n      columns: {\"03\": {column: amount}}\n", "numeric indexes cannot have leading zeros"},
		{"table: 7\n      header_rows: []\n      data_start_row: 1\n      columns: {\"\": {column: amount}}\n", `spec.sheets.Data.columns[""]`},
	} {
		for _, format := range []string{"yaml", "json"} {
			for _, verb := range []string{"apply", "diff"} {
				t.Run(tc.message+"/"+format+"/"+verb, func(t *testing.T) {
					env := testutil.NewTestEnv(t)
					mock := testutil.NewMockAPIServer()
					t.Cleanup(mock.Close)
					env.SetupConfigWithOrg(mock.URL(), "", scopeTestOrgID)
					doc := sheetsManifest("    Data:\n      " + tc.sheet)
					if format == "json" {
						doc = sheetsJSON(t, doc)
					}
					path := writeManifest(t, t.TempDir(), "deliverable."+format, doc)
					out, _, err := runManifest(t, verb, "-f", path, "--context", "default")
					if qerrors.GetExitCode(err) != qerrors.ExitBadUsage || !strings.Contains(err.Error(), tc.message) {
						t.Fatalf("error = %v (exit %d), want exit 2 and %q", err, qerrors.GetExitCode(err), tc.message)
					}
					if out != "" || len(mock.Requests()) != 0 {
						t.Errorf("stdout = %q, requests = %v; want neither", out, mock.Requests())
					}
				})
			}
		}
	}
}

func TestDeliverableSheetJSONNumericTableID(t *testing.T) {
	for _, verb := range []string{"apply", "diff"} {
		t.Run(verb+" unchanged", func(t *testing.T) {
			s := sheetsServer(t)
			path := writeManifest(t, t.TempDir(), "deliverable.json", sheetsJSON(t, sheetsManifest(sheetsData, sheetsDataColumns, sheetsNotes)))
			out, errOut, err := runManifest(t, verb, "-f", path)
			if err != nil {
				t.Fatal(err)
			}
			if (verb == "apply" && !strings.Contains(out, "unchanged")) || (verb == "diff" && out != "") || errOut != "" {
				t.Errorf("stdout = %q, stderr = %q", out, errOut)
			}
			if len(s.writes()) != 0 {
				t.Errorf("unexpected writes: %v", s.writes())
			}
		})
	}
	t.Run("apply sends an integer ID", func(t *testing.T) {
		s := sheetsServer(t)
		doc := sheetsManifest(strings.Replace(sheetsData, "data_start_row: 2", "data_start_row: 3", 1), sheetsNotes)
		path := writeManifest(t, t.TempDir(), "deliverable.json", sheetsJSON(t, doc))
		if _, _, err := runManifest(t, "apply", "-f", path); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(s.writes(), []string{patchData}) {
			t.Fatalf("writes = %v, want only %s", s.writes(), patchData)
		}
		assertSeriesJSON(t, s.bodies[patchData], `{"member_dataset_id":42,"sheet_name":"Data","data_start_row":3,"header_rows":[1],`+
			`"instruction_row_count":0,"member_series_id":null,"sort_keys":[]}`)
	})
}
