package deliverables

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

func TestMergedRangeHitsMatchesInstructionRowBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, ref string
		column    int
		want      bool
	}{
		{"above data", "B1:C2", 2, false},
		{"ends at first data row", "B1:C3", 2, true},
		{"enters instructions from above", "B2:C4", 2, true},
		{"wholly inside instructions", "B3:C4", 2, false},
		{"ends at last instruction row", "B3:C5", 3, false},
		{"reaches first row after instructions", "B3:C6", 3, true},
		{"below instructions", "B9:C10", 2, true},
		{"other column", "B3:C6", 4, false},
		{"left boundary", "AA6:AC7", 27, true},
		{"right boundary", "AA6:AC7", 29, true},
		{"last Excel column", "XFC6:XFD7", 16384, true},
		{"single cell", "C6", 3, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := mergedRangeHits([]string{tc.ref}, 3, 3, tc.column)
			if err != nil {
				t.Fatal(err)
			}
			if tc.want != (len(got) == 1) || tc.want && got[0] != tc.ref {
				t.Errorf("hits = %v, want hit: %v", got, tc.want)
			}
		})
	}
	if got, err := mergedRangeHits([]string{"B3:C3"}, 3, 0, 2); err != nil || len(got) != 1 {
		t.Fatalf("zero instruction rows: hits = %v, error = %v", got, err)
	}
}

func TestBindingPreflightChecksStoredInstructionCountAndFutureColumnSaves(t *testing.T) {
	for _, tc := range []struct {
		name, extra, ref string
		code             int
	}{
		{"stored instructions allow merge", "", "C2:C3", 0},
		{"clearing instructions rejects merge", "instruction_row_count: 0", "C2:C3", 2},
		{"merge crossing into stored instructions", "", "C1:C2", 2},
		{"layout re-anchor may recreate matching column", "data_start_row: 3", "C3:C5", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stored := bpSheet(bpDataID, "Data", 5, []int{1}, 2, bpColumn(bpColumnC, 3, "amount"))
			stored.InstructionRowCount = 2
			tree := bpTree(stored)
			tree.WorkbookInventory = &api.WorkbookInventory{Sheets: []api.WorkbookSheetInventory{{Name: "Data", MergedRanges: []string{tc.ref}}}}
			sheet := bpData("columns: {C: {column: amount, format: '0.00'}}", tc.extra)
			if strings.HasPrefix(tc.extra, "data_start_row:") {
				sheet = bpSheetDoc("Data", bpBase, "header_rows: [1]", tc.extra, "columns: {C: {column: amount}}")
			}
			p := bpPlan(t, sheet)
			_, err := p.bindingRequests(tree)
			if code := qerrors.GetExitCode(err); code != tc.code {
				t.Fatalf("exit = %d, want %d; error = %v", code, tc.code, err)
			}
		})
	}
}

func TestReplacementTemplateChecksItsInventoryAfterRevisionCreation(t *testing.T) {
	column := bpColumn(bpColumnC, 3, "amount")
	source := bpTree(bpSheet(bpDataID, "Data", 5, []int{1}, 2, column))
	source.ID = uuid.MustParse(revV2)
	source.WorkbookInventory = &api.WorkbookInventory{Sheets: []api.WorkbookSheetInventory{{Name: "Data", MergedRanges: []string{"C2:C5"}}}}
	p := bpPlan(t, bpData("columns: {C: {column: amount}}"))
	p.Revision, p.CopyFrom, p.TemplatePath = nil, &Revision{ID: source.ID}, "replacement.xlsx"
	if _, err := p.bindingRequests(source); err != nil {
		t.Fatalf("copy source's inventory must be skipped: %v", err)
	}
	created := bpTree(bpSheet(bpDataID, "Data", 5, []int{1}, 2))
	created.WorkbookInventory = source.WorkbookInventory
	_, err := p.bindingRequests(created)
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Fatalf("created template: exit = %d, want 2; error = %v", code, err)
	}
}

func TestBindingSchemaReadsAreDeduplicatedByResolvedTableID(t *testing.T) {
	b := servePlan(t, planDeliverable(nil), draftV3)
	b.serveBindings(revV3, bpTree())
	m := bpManifest(t, "", bpData("columns: {C: {column: amount}}"),
		bpSheetDoc("Other", "table: claims", "header_rows: []", "data_start_row: 1", "row_order: [{column: claim_id}]"))
	if _, err := b.client().Prepare("token", m, "."); err != nil {
		t.Fatal(err)
	}
	reads := 0
	for _, request := range b.mock.Requests() {
		if request == "GET "+bpDatasets+"/5/migration-details" {
			reads++
		}
	}
	if reads != 1 {
		t.Errorf("schema reads = %d, want 1; requests = %v", reads, b.mock.Requests())
	}
}

func TestBindingSchemaReadFailureKeepsExitCodeBeforeWrites(t *testing.T) {
	b := servePlan(t, planDeliverable(nil), draftV3)
	b.serveBindings(revV3, bpTree())
	b.mock.RegisterHandler(http.MethodGet, bpDatasets+"/5/migration-details", func(w http.ResponseWriter, _ *http.Request) {
		respondBody(w, http.StatusForbidden, `{"detail":"schema access denied"}`)
	})
	p, err := b.client().Prepare("token", bpManifest(t, "", bpData("columns: {C: {column: amount}}")), ".")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitUnauthorized || p != nil {
		t.Fatalf("plan = %v, exit = %d, want nil and unauthorized; error = %v", p, code, err)
	}
	if !strings.Contains(err.Error(), "spec.sheets.Data.columns.C.column") {
		t.Errorf("error lacks manifest key: %v", err)
	}
	for _, request := range b.mock.Requests() {
		if !strings.HasPrefix(request, "GET ") {
			t.Errorf("unexpected write: %s", request)
		}
	}
}

func TestRunParameterColumnsDoNotRequireATableSchema(t *testing.T) {
	b := servePlan(t, planDeliverable(nil), draftV3)
	b.serveBindings(revV3, bpTree())
	if _, err := b.client().Prepare("token", bpManifest(t, "", bpData("columns: {C: {run_param: period}}")), "."); err != nil {
		t.Fatal(err)
	}
	if got, want := b.mock.Requests(), []string{planListing, planRead, planRevisions, "GET " + bpTreePath + revV3}; !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %v, want %v", got, want)
	}
}

func TestReplacementTemplateSourceRefusalPrecedesUpload(t *testing.T) {
	b := servePlan(t, planDeliverable(nil), enabledV2)
	tree := bpTree(bpSheet(bpDataID, "Data", 5, []int{1}, 2, bpColumn(bpColumnC, 3, "gone")))
	tree.ID = uuid.MustParse(revV2)
	b.serveBindings(revV2, tree)
	m := planManifest(t, deliverableManifestBasics+"  release: v4\n  template: carrier-a.xlsx\n  sheets:\n"+
		bpData("columns: {C: {column: gone}}"))
	p, err := b.client().Prepare("token", m, planWorkbook(t))
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage || p != nil {
		t.Fatalf("plan = %v, exit = %d, want nil and 2; error = %v", p, code, err)
	}
	for _, request := range b.mock.Requests() {
		if !strings.HasPrefix(request, "GET ") {
			t.Errorf("unexpected write: %s", request)
		}
	}
}
