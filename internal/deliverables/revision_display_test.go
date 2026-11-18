package deliverables

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
)

func TestFormatRevisionPrintsEachRowFilterAsATextBlock(t *testing.T) {
	tree := `{"id":"8d0f4b1e-58a4-4f43-9c43-7f1f3b6f0a01","deliverable_id":"8d0f4b1e-58a4-4f43-9c43-7f1f3b6f0a02",
 "release":"2026.2","state":"draft","description":null,"skeleton_id":8,"enabled_by_id":null,"enabled_at":null,
 "sheet_bindings":[{"id":"8d0f4b1e-58a4-4f43-9c43-7f1f3b6f0a03","sheet_name":"Data","member_dataset_id":42,
  "member_series_id":null,"data_start_row":2,"header_rows":[1],"instruction_row_count":0,
  "row_filter":{"description":"UK policies over 100","conditions":[{"column_name":"region","operator":"eq","value":"UK",
   "value_high":null,"value_list":null,"case_sensitive":false}],
   "predicate":{"source":"def applies(row):\n    return row[\"premium\"] > 100\n","columns":["premium"]}},
  "sort_keys":[],"column_bindings":[]}],
 "cell_bindings":[{"id":"8d0f4b1e-58a4-4f43-9c43-7f1f3b6f0a04","sheet_name":"Data","cell_ref":"B1","source_type":"literal",
  "literal_value":"Total","run_param":null,"label_cell_ref":null,"target_label":null,"needs_confirmation":false,
  "format_override":null,"row_filter":null}]}`
	var r api.DeliverableRevisionResponse
	if err := json.Unmarshal([]byte(tree), &r); err != nil {
		t.Fatal(err)
	}

	out := FormatRevision(&RevisionDetail{DeliverableRevisionResponse: &r})

	for _, want := range []string{
		"    Row filter:\n      description: UK policies over 100\n",
		`      return row["premium"] > 100`,
		"  Data!B1 <- literal Total [8d0f4b1e-58a4-4f43-9c43-7f1f3b6f0a04]\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("FormatRevision() lacks %q:\n%s", want, out)
		}
	}
	if strings.Count(out, "Row filter:") != 1 {
		t.Errorf("FormatRevision() prints a row filter for the literal cell:\n%s", out)
	}
}

func TestFormatRevisionPrintsNoFilterOnlyForATotalWithItsOwnTable(t *testing.T) {
	total := `{"id":"8d0f4b1e-58a4-4f43-9c43-7f1f3b6f0a05","sheet_name":"Data","cell_ref":"B2","source_type":"aggregate",
  "aggregate_func":"sum","aggregate_column":"premium","aggregate_member_dataset_id":42,"aggregate_member_series_id":null,
  "same_rows_as_sheet_binding_id":null,"row_filter":null,"needs_confirmation":false,"label_cell_ref":null,"target_label":null,
  "format_override":null}`
	mirror := strings.Replace(strings.Replace(total, `"aggregate_member_dataset_id":42`, `"aggregate_member_dataset_id":null`, 1),
		`"same_rows_as_sheet_binding_id":null`, `"same_rows_as_sheet_binding_id":"8d0f4b1e-58a4-4f43-9c43-7f1f3b6f0a03"`, 1)
	for name, tc := range map[string]struct {
		cell string
		want bool
	}{"total": {total, true}, "mirror": {mirror, false}} {
		t.Run(name, func(t *testing.T) {
			var cell api.CellBindingResponse
			if err := json.Unmarshal([]byte(tc.cell), &cell); err != nil {
				t.Fatal(err)
			}
			r := api.DeliverableRevisionResponse{CellBindings: &[]api.CellBindingResponse{cell}}
			out := FormatRevision(&RevisionDetail{DeliverableRevisionResponse: &r})
			if got := strings.Contains(out, "(no filter: keeps every row)"); got != tc.want {
				t.Errorf("FormatRevision() prints no-filter line = %t, want %t:\n%s", got, tc.want, out)
			}
		})
	}
}
