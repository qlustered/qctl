package root

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const bindingSchemaPath = "/api/orgs/" + scopeTestOrgID + "/datasets/42/migration-details"

func TestDeliverableBindingColumnRefusalsPrecedeEveryWrite(t *testing.T) {
	for _, tc := range []struct {
		name, sheet, columns, tree, schema, message string
	}{
		{name: "source column POST", sheet: sheetsData,
			columns: sheetsDataColumns + "        C: {column: typo}\n", message: "spec.sheets.Data.columns.C.column"},
		{name: "source column PATCH", sheet: sheetsData,
			columns: strings.Replace(sheetsDataColumns, "column: policy", "column: Policy", 1), message: `column "Policy"`},
		{name: "row order", sheet: sheetsData + "      row_order: [{column: typo}]\n",
			columns: sheetsDataColumns, message: "spec.sheets.Data.row_order[0].column"},
		{name: "kept row order resent by sheet PATCH",
			sheet: strings.Replace(sheetsData, "data_start_row: 2", "data_start_row: 3", 1), columns: sheetsDataColumns,
			tree:    strings.Replace(sheetsInventoryTree, `"sort_keys":[]`, `"sort_keys":[{"position":0,"column_name":"gone","descending":false}]`, 1),
			message: `column "gone"`},
		{name: "draft-only source column", sheet: sheetsData,
			columns: sheetsDataColumns + "        C: {column: pending}\n",
			schema:  `{"fields":{"pending":{"new":{"is_deleted":false}}}}`, message: `column "pending"`},
		{name: "deleted source column", sheet: sheetsData,
			columns: sheetsDataColumns + "        C: {column: gone}\n",
			schema:  `{"fields":{"gone":{"current":{"is_deleted":true},"new":{"is_deleted":false}}}}`, message: `column "gone"`},
		{name: "draft-only sort column", sheet: sheetsData + "      row_order: [{column: pending}]\n", columns: sheetsDataColumns,
			schema: `{"fields":{"pending":{"new":{"is_deleted":false}}}}`, message: `column "pending"`},
		{name: "merged target POST", sheet: sheetsData, columns: sheetsDataColumns + "        C: {run_param: period}\n",
			tree:    strings.Replace(sheetsInventoryTree, `"merged_ranges":[]`, `"merged_ranges":["C2:D4"]`, 1),
			message: "spec.sheets.Data.columns.C: target column C intersects merged ranges C2:D4"},
		{name: "merged target PATCH", sheet: sheetsData,
			columns: strings.Replace(sheetsDataColumns, "{column: policy}", "{column: policy, format: '0'}", 1),
			tree:    strings.Replace(sheetsInventoryTree, `"merged_ranges":[]`, `"merged_ranges":["A2:B4"]`, 1),
			message: "spec.sheets.Data.columns.A: target column A intersects merged ranges A2:B4"},
		{name: "matching column may be re-anchored", sheet: strings.Replace(sheetsData, "data_start_row: 2", "data_start_row: 3", 1),
			columns: sheetsDataColumns, tree: strings.Replace(sheetsInventoryTree, `"merged_ranges":[]`, `"merged_ranges":["A3:A5"]`, 1),
			message: "spec.sheets.Data.columns.A: target column A intersects merged ranges A3:A5"},
	} {
		for _, release := range []string{"existing draft", "new copy"} {
			for _, verb := range []string{"apply", "diff"} {
				t.Run(tc.name+"/"+release+"/"+verb, func(t *testing.T) {
					s := sheetsServer(t)
					tree := tc.tree
					if tree == "" {
						tree = sheetsInventoryTree
					}
					answerRevisionTree(t, s.MockAPIServer, draftRevisionID, tree)
					answerRevisionTree(t, s.MockAPIServer, enabledRevisionID,
						strings.ReplaceAll(strings.Replace(tree, `"state":"draft"`, `"state":"enabled"`, 1), draftRevisionID, enabledRevisionID))
					if tc.schema != "" {
						s.RegisterHandler(http.MethodGet, bindingSchemaPath, func(w http.ResponseWriter, r *http.Request) {
							checkDeliverableToken(t, r)
							testutil.RespondJSON(w, http.StatusOK, json.RawMessage(tc.schema))
						})
					}
					// Omitting Notes plans a DELETE before the invalid Data save.
					manifest := strings.Replace(sheetsManifest(tc.sheet, tc.columns), "Acme Re", "Renamed receiver", 1)
					if release == "new copy" {
						manifest = strings.Replace(manifest, `release: "2026.2"`, `release: "2026.3"`, 1)
					}
					path := writeManifest(t, t.TempDir(), "deliverable.yaml", manifest)
					args := []string{verb, "-f", path}
					if verb == "apply" {
						args = append(args, "--yes")
					}
					out, _, err := runManifest(t, args...)
					if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
						t.Fatalf("exit = %d, want 2; error = %v", code, err)
					}
					if !strings.Contains(err.Error(), tc.message) {
						t.Errorf("error = %v, want %q", err, tc.message)
					}
					if got := s.writes(); len(got) != 0 || out != "" {
						t.Errorf("writes = %v, stdout = %q; want neither", got, out)
					}
				})
			}
		}
	}
}

func TestDeliverableBindingAllowsAppliedColumnsWithPendingDeletion(t *testing.T) {
	s := sheetsServer(t)
	s.RegisterHandler(http.MethodGet, bindingSchemaPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"fields":{"policy":{"current":{"is_deleted":false},"new":{"is_deleted":true}}}}`))
	})
	path := writeManifest(t, t.TempDir(), "deliverable.yaml", sheetsManifest(
		sheetsData+"      row_order: [{column: policy}]\n", sheetsDataColumns+"        C: {column: policy}\n", sheetsNotes))
	out, _, err := runManifest(t, "diff", "-f", path)
	if code := qerrors.GetExitCode(err); code != 1 || !strings.Contains(out, "POST /deliverable-sheet-bindings/") {
		t.Fatalf("exit = %d, stdout = %q; want diff with column POST; error = %v", code, out, err)
	}
}

func TestDeliverableMetadataAndCellsDoNotReadTableSchemas(t *testing.T) {
	for _, extra := range []string{"", "  release: \"2026.2\"\n  cells:\n    'Data!A1': {run_param: period}\n"} {
		t.Run(extra, func(t *testing.T) {
			s := sheetsServer(t)
			manifest := deliverableManifestHead + "monthly-bordereau\n" + strings.Replace(liveDeliverableSpec, "Acme Re", "Renamed receiver", 1) + extra
			path := writeManifest(t, t.TempDir(), "deliverable.yaml", manifest)
			_, _, err := runManifest(t, "diff", "-f", path)
			if code := qerrors.GetExitCode(err); code != 1 {
				t.Fatalf("exit = %d, want diff; error = %v", code, err)
			}
			for _, request := range s.Requests() {
				if strings.Contains(request, "/migration-details") {
					t.Errorf("unexpected schema read: %s", request)
				}
			}
		})
	}
}
