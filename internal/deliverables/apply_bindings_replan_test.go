package deliverables

import (
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
)

func TestApplyReplansMovedColumnsOfACopyWithATemplate(t *testing.T) {
	for _, tc := range []struct {
		name      string
		headerRow int
		column    string
		index     int
		reanchor  bool
	}{
		{name: "revision response", headerRow: 1, column: "B", index: 2},
		{name: "revision then sheet responses", headerRow: 2, column: "C", index: 3, reanchor: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := serveApply(t, planDeliverable(nil), disabledV1, enabledV2)
			label := "Amount"
			amount := bpColumn(bpColumnA, 1, "amount")
			amount.TargetLabel = &label
			source := bpTree(bpSheet(bpDataID, "Data", 5, []int{1}, 2, amount))
			source.ID, source.Release, source.State = uuid.MustParse(revV2), "v2", api.DeliverableRevisionStateEnabled
			oldTemplate := 6
			source.SkeletonID = &oldTemplate
			s.trees(revV2, source)

			// The replacement template has Amount at B1, so copying moves
			// A to B. With a layout PATCH it also has Amount at C2, which
			// moves B to C when header_rows becomes [2].
			headers := map[string][]string{"1": {"", label}}
			if tc.reanchor {
				headers["2"] = []string{"", "", label}
			}
			inventory := &api.WorkbookInventory{Sheets: []api.WorkbookSheetInventory{{Name: "Data", MaxRow: tc.headerRow, MaxColumn: 3,
				HeaderRows: headers, MergedRanges: []string{}}}}
			moved := bpColumn(abCopyColumn, 2, "amount")
			moved.TargetLabel = &label
			created := bpTree(bpSheet(abCopyData, "Data", 5, []int{1}, 2, moved))
			created.ID, created.WorkbookInventory = uuid.MustParse(revV4), inventory
			copyReport := &api.BindingAnchorReport{CellBindings: []api.CellAnchorChange{}, ColumnBindings: []api.ColumnAnchorChange{{
				ColumnBindingID: moved.ID, SheetBindingID: uuid.MustParse(abCopyData), SheetName: "Data", TargetLabel: &label,
				FromColumnIndex: 1, ToColumnIndex: 2, Outcome: api.BindingAnchorOutcomeMoved}}}
			s.on(http.MethodPut, "/deliverable-skeletons/upload-url", planMinted)
			s.on(http.MethodPut, putTargetPath, "")
			s.on(http.MethodPost, "/deliverable-skeletons/finalize", api.SkeletonFinalizeResponse{SkeletonID: 7, WorkbookInventory: *inventory})
			s.on(http.MethodPost, "/deliverables/"+monthlyID+"/revisions", api.DeliverableRevisionCreateResponse{ID: created.ID,
				DeliverableID: created.DeliverableID, Release: created.Release, State: created.State, SkeletonID: created.SkeletonID,
				CellBindings: created.CellBindings, WorkbookInventory: inventory, AnchorReport: copyReport, SheetBindings: created.SheetBindings})

			want := []string{
				`PUT /deliverable-skeletons/upload-url {"file_size":8,"original_filename":"carrier-a.xlsx"}`,
				"PUT " + putTargetPath + " <8 bytes>",
				`POST /deliverable-skeletons/finalize {"acknowledge_lossy":false,"skeleton_id":41}`,
				"POST /deliverables/" + monthlyID + `/revisions {"acknowledge_lossy":false,"copy_from_revision_id":"` + revV2 + `","release":"v3","skeleton_id":7}`,
			}
			if tc.reanchor {
				moved.TargetColumnIndex = tc.index
				s.on(http.MethodPatch, "/deliverable-revisions/"+revV4+"/sheet-bindings/"+abCopyData, api.SheetBindingPatchResponse{ID: uuid.MustParse(abCopyData), SheetName: "Data", MemberDatasetID: 5, HeaderRows: []int{2}, DataStartRow: 3,

					AnchorReport: &api.BindingAnchorReport{CellBindings: []api.CellAnchorChange{}, ColumnBindings: []api.ColumnAnchorChange{{
						ColumnBindingID: moved.ID, SheetBindingID: uuid.MustParse(abCopyData), SheetName: "Data", TargetLabel: &label,
						FromColumnIndex: 2, ToColumnIndex: 3, Outcome: api.BindingAnchorOutcomeMoved}}}, ColumnBindings: &[]api.ColumnBindingResponse{moved},
				})
				want = append(want, "PATCH /deliverable-revisions/"+revV4+"/sheet-bindings/"+abCopyData+
					` {"data_start_row":3,"header_rows":[2],"instruction_row_count":0,"member_dataset_id":5,"member_series_id":null,"sheet_name":"Data","sort_keys":[]}`)
			}
			formatted := bpFormatted(moved, "0.00")
			s.on(http.MethodPatch, abColumns(abCopyData)+"/"+abCopyColumn, formatted)
			final := bpTree(bpSheet(abCopyData, "Data", 5, []int{tc.headerRow}, tc.headerRow+1, formatted))
			final.ID, final.WorkbookInventory = created.ID, inventory
			s.trees(revV4, final)
			m := bpManifest(t, "  template: carrier-a.xlsx\n", bpSheetDoc("Data", bpBase,
				"header_rows: ["+strconv.Itoa(tc.headerRow)+"]", "data_start_row: "+strconv.Itoa(tc.headerRow+1),
				`columns: {`+tc.column+`: {column: amount, format: "0.00"}}`))
			p, n := s.prepare(m, planWorkbook(t))
			if !p.MayRemove || len(p.Removals) != 1 {
				t.Fatalf("MayRemove = %v, Removals = %q", p.MayRemove, p.Removals)
			}
			// Before either response the plan removes A and creates B or C.
			planned := planLines(p)
			if tail := planned[len(planned)-2:]; !reflect.DeepEqual(tail, []string{
				"DELETE " + abColumns(copiedSheetBindingID) + "/" + copiedColumnBindingID,
				"POST " + abColumns(copiedSheetBindingID),
			}) {
				t.Fatalf("planned column writes = %v", tail)
			}

			r, err := s.client().Apply("token", p)
			if err != nil {
				t.Fatal(err)
			}
			want = append(want, "PATCH "+abColumns(abCopyData)+"/"+abCopyColumn+
				` {"format_override":"0.00","run_param":null,"source_column":"amount","source_type":"column","target_column_index":`+strconv.Itoa(tc.index)+`}`,
				abV4Tree)
			s.expect(n, want...)
			if got := planLines(p); !reflect.DeepEqual(got, planned) {
				t.Errorf("Apply changed plan requests: %v", got)
			}
			writes, reports := 1, 0
			if tc.reanchor {
				writes, reports = 2, 1
			}
			if r.BindingWrites != writes || len(r.AnchorReports) != reports {
				t.Errorf("BindingWrites = %d, AnchorReports = %v", r.BindingWrites, r.AnchorReports)
			}
			column := r.Manifest.Spec.Sheets["Data"].Columns[tc.column]
			if column.Column != "amount" || column.Format == nil || *column.Format != "0.00" || r.Manifest.Status.RevisionID.String() != revV4 {
				t.Errorf("result manifest = %+v", r.Manifest)
			}
			output := FormatApplyResult(p, r)
			if !strings.Contains(output, "Column Data!A -> B: moved; label: Amount") ||
				(tc.reanchor && !strings.Contains(output, "Column Data!B -> C: moved; label: Amount")) {
				t.Errorf("missing anchor report:\n%s", output)
			}
		})
	}
}
