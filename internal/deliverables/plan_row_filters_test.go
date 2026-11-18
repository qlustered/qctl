package deliverables

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const rfCountBody = `{"matched":12,"total":40,"capped":false,"predicate_errors":0,"first_predicate_error":null}`

// rfCountPath is table's row filter count route.
func rfCountPath(table int) string {
	return bpDatasets + "/" + strconv.Itoa(table) + "/row-filter-test"
}

// rfBackend answers the count routes of tables 5 and 6 with status and body,
// and table 5's and 6's schema reads with columns.
type rfBackend struct {
	mock   *testutil.MockAPIServer
	bodies map[string][]string
}

func serveCounts(t *testing.T, status int, body string, columns ...string) *rfBackend {
	t.Helper()
	b := &rfBackend{mock: testutil.NewMockAPIServer(), bodies: map[string][]string{}}
	t.Cleanup(b.mock.Close)
	for _, table := range []int{5, 6} {
		path := rfCountPath(table)
		b.mock.RegisterHandler(http.MethodPost, path, func(w http.ResponseWriter, r *http.Request) {
			b.bodies[path] = append(b.bodies[path], string(mustJSON(t, recordJSON(t, r))))
			respondBody(w, status, body)
		})
		b.mock.RegisterHandler(http.MethodGet, bpDatasets+"/"+strconv.Itoa(table)+"/migration-details", func(w http.ResponseWriter, _ *http.Request) {
			fields := map[string]api.FieldMigrationInfo{}
			for _, name := range columns {
				fields[name] = api.FieldMigrationInfo{Current: &api.FieldDatabaseDetailDict{}}
			}
			testutil.RespondJSON(w, http.StatusOK, api.MigrationDetailResponse{Fields: fields})
		})
	}
	return b
}

func (b *rfBackend) client() *Client { return NewClient(b.mock.URL(), testOrgID, 0) }

// requests returns the requests b received whose path contains part.
func (b *rfBackend) requests(part string) []string {
	var out []string
	for _, request := range b.mock.Requests() {
		if strings.Contains(request, part) {
			out = append(out, request)
		}
	}
	return out
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// rfPlan plans m against tree, as Prepare does before counting.
func rfPlan(t *testing.T, m *Manifest, tree *api.DeliverableRevisionResponse) (*Plan, []Request) {
	t.Helper()
	p := cellPlan(t, m)
	p.Tree = tree
	requests, err := p.bindingRequests(tree)
	if err != nil {
		t.Fatalf("bindingRequests: %v", err)
	}
	return p, requests
}

// rfSheets parses carrier-a-bordereau release v3 with spec.sheets made of
// sheets.
func rfSheets(t *testing.T, sheets ...string) *Manifest {
	t.Helper()
	return bpManifest(t, "", sheets...)
}

func TestRowFilterCountsCoverEverySheetSaveThatSendsOrKeepsAFilter(t *testing.T) {
	filtered := bpFiltered(bpSheet(bpDataID, "Data", 5, []int{1}, 2), bpStoredFilter("Positive amounts"))
	plain := bpSheet(bpDataID, "Data", 5, []int{1}, 2)
	relayout := bpSheetDoc("Data", bpBase, "header_rows: [1]", "data_start_row: 3")
	const series = `series: "` + bpSeriesID + `"`
	for _, tc := range []struct {
		name   string
		stored []api.SheetBindingResponse
		sheet  string
		want   []RowFilterCount // Count left out
	}{
		{name: "a POST sends a filter", sheet: bpData(bpFilter("Positive amounts", "0")),
			want: []RowFilterCount{{Key: "spec.sheets.Data.row_filter", Table: 5}}},
		{name: "a PATCH sends a filter", stored: []api.SheetBindingResponse{plain}, sheet: bpData(bpFilter("Positive amounts", "0")),
			want: []RowFilterCount{{Key: "spec.sheets.Data.row_filter", Table: 5}}},
		{name: "a PATCH keeps the stored filter", stored: []api.SheetBindingResponse{filtered}, sheet: relayout,
			want: []RowFilterCount{{Key: "spec.sheets.Data.row_filter", Table: 5, Kept: true}}},
		{name: "a PATCH keeps the stored filter on its new table", stored: []api.SheetBindingResponse{filtered},
			sheet: bpSheetDoc("Data", "table: 6", "header_rows: [1]", "data_start_row: 2"),
			want:  []RowFilterCount{{Key: "spec.sheets.Data.row_filter", Table: 6, Kept: true}}},
		{name: "a series sheet", sheet: bpData(series, bpFilter("Positive amounts", "0")),
			want: []RowFilterCount{{Key: "spec.sheets.Data.row_filter", Table: 5, Series: true}}},
		{name: "a PATCH clearing the filter", stored: []api.SheetBindingResponse{filtered}, sheet: bpData("row_filter: null")},
		{name: "a POST without a filter", sheet: bpData()},
		{name: "a PATCH with no filter stored or sent", stored: []api.SheetBindingResponse{plain}, sheet: relayout},
		{name: "an unchanged sheet keeps its filter without a save", stored: []api.SheetBindingResponse{filtered}, sheet: bpData()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := serveCounts(t, http.StatusOK, rfCountBody)
			p, requests := rfPlan(t, rfSheets(t, tc.sheet), bpTree(tc.stored...))
			if err := b.client().countRowFilters("token", p, requests); err != nil {
				t.Fatal(err)
			}
			var got []RowFilterCount
			for _, count := range p.RowFilterCounts {
				if count.Count == nil || count.Count.Matched != 12 || count.Count.Total != 40 {
					t.Errorf("%s count = %+v, want the server's", count.Key, count.Count)
				}
				count.Count = nil
				got = append(got, count)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("counts = %+v, want %+v", got, tc.want)
			}
			if sent := b.requests("/row-filter-test"); len(sent) != len(tc.want) {
				t.Errorf("count requests = %v, want %d", sent, len(tc.want))
			}
			for _, bodies := range b.bodies {
				for _, body := range bodies {
					if want := `{"row_filter":{"conditions":[{"case_sensitive":false,"column_name":"amount","operator":"gt","value":"0",` +
						`"value_high":null,"value_list":null}],"description":null}}`; body != want {
						t.Errorf("count body = %s, want %s", body, want)
					}
				}
			}
			if len(p.RowFilterWarnings) != 0 {
				t.Errorf("warnings = %v", p.RowFilterWarnings)
			}
		})
	}
}

func TestRowFilterCountsCountACellFilterOnItsAggregateTable(t *testing.T) {
	stored := storedCell(t, "{total: {table: 6, func: sum, column: amount}}")
	stored.RowFilter = bpStoredFilter("Positive amounts")
	for _, tc := range []struct {
		name, total string
		kept        bool
	}{
		{name: "a sent filter", total: "{total: {table: 6, func: count, " + bpFilter("Positive amounts", "0") + "}}"},
		{name: "a kept filter", total: "{total: {table: 6, func: count}}", kept: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := serveCounts(t, http.StatusOK, rfCountBody)
			p, requests := rfPlan(t, cellManifest(t, "{Summary!B1: "+tc.total+"}"), treeWithCell(stored, bpSheet(bpDataID, "Data", 5, []int{1}, 2)))
			if err := b.client().countRowFilters("token", p, requests); err != nil {
				t.Fatal(err)
			}
			if got := b.requests("/row-filter-test"); !reflect.DeepEqual(got, []string{"POST " + rfCountPath(6)}) {
				t.Errorf("count requests = %v, want table 6's", got)
			}
			if len(p.RowFilterCounts) != 1 || p.RowFilterCounts[0].Key != "spec.cells.Summary!B1.total.row_filter" ||
				p.RowFilterCounts[0].Table != 6 || p.RowFilterCounts[0].Kept != tc.kept {
				t.Errorf("counts = %+v", p.RowFilterCounts)
			}
		})
	}
}

func TestRowFilterCountsAskOncePerTableAndRows(t *testing.T) {
	b := serveCounts(t, http.StatusOK, rfCountBody)
	m := cellManifest(t, "{Summary!B1: {total: {table: 5, func: count, "+bpFilter("Positive totals", "0")+"}}}",
		bpData(bpFilter("Positive amounts", "0")),
		bpSheetDoc("Other", bpBase, "header_rows: []", "data_start_row: 1", bpFilter("Amounts over zero", "0")),
		bpSheetDoc("Third", "table: 6", "header_rows: []", "data_start_row: 1", bpFilter("Positive amounts", "0")),
		bpSheetDoc("Wider", bpBase, "header_rows: []", "data_start_row: 1", bpFilter("Amounts over 10", "10")))
	p, requests := rfPlan(t, m, bpTree())
	if err := b.client().countRowFilters("token", p, requests); err != nil {
		t.Fatal(err)
	}
	if got, want := b.requests("/row-filter-test"), []string{"POST " + rfCountPath(5), "POST " + rfCountPath(6), "POST " + rfCountPath(5)}; !reflect.DeepEqual(got, want) {
		t.Errorf("count requests = %v, want %v", got, want)
	}
	var keys []string
	for _, count := range p.RowFilterCounts {
		keys = append(keys, count.Key+"@"+strconv.Itoa(count.Table))
	}
	if want := []string{"spec.sheets.Data.row_filter@5", "spec.sheets.Other.row_filter@5", "spec.sheets.Third.row_filter@6",
		"spec.sheets.Wider.row_filter@5", "spec.cells.Summary!B1.total.row_filter@5"}; !reflect.DeepEqual(keys, want) {
		t.Errorf("counts = %v, want %v", keys, want)
	}
}

func TestRowFilterCountUnprocessableIsABadUsageErrorNamingTheKey(t *testing.T) {
	refusal := `{"detail":{"msg":"Row filter invalid: condition 1: column 'amount' is not in this table","error_code":"ERR_VALIDATION",` +
		`"loc":["body","row_filter"]}}`
	filtered := bpFiltered(bpSheet(bpDataID, "Data", 5, []int{1}, 2), bpStoredFilter("Positive amounts"))
	for _, tc := range []struct {
		name, sheet, want string
		stored            []api.SheetBindingResponse
	}{
		{name: "a sent filter", sheet: bpData(bpFilter("Positive amounts", "0")),
			want: "spec.sheets.Data.row_filter does not fit table 5: "},
		{name: "a kept filter", sheet: bpSheetDoc("Data", bpBase, "header_rows: [1]", "data_start_row: 3"), stored: []api.SheetBindingResponse{filtered},
			want: "spec.sheets.Data.row_filter: the stored row filter, which this save keeps, does not fit table 5: "},
		{name: "a series sheet", sheet: bpData(`series: "`+bpSeriesID+`"`, bpFilter("Positive amounts", "0")),
			want: "spec.sheets.Data.row_filter does not fit table 5, the series' reference table: "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := serveCounts(t, http.StatusUnprocessableEntity, refusal)
			p, requests := rfPlan(t, rfSheets(t, tc.sheet), bpTree(tc.stored...))
			err := b.client().countRowFilters("token", p, requests)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit = %d, want 2; error = %v", code, err)
			}
			var apiErr *apierror.OperationalError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusUnprocessableEntity {
				t.Errorf("error chain lost the API error: %v", err)
			}
			if !strings.HasPrefix(err.Error(), tc.want) || !strings.Contains(err.Error(), "Row filter invalid") ||
				!strings.HasSuffix(err.Error(), "\n\nSet spec.sheets.Data.row_filter to a row filter that fits the table, or to null to clear it.") {
				t.Errorf("error = %q, want it to start %q", err, tc.want)
			}
		})
	}
}

func TestRowFilterCountFailuresAreWarnings(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"403", http.StatusForbidden, `{"detail":"User is not authorized to access table 5"}`},
		{"404", http.StatusNotFound, `{"detail":"Dataset not found"}`},
		{"429", http.StatusTooManyRequests, `{"detail":"Too many requests"}`},
		{"500", http.StatusInternalServerError, `{"detail":"Internal Server Error"}`},
		{"400", http.StatusBadRequest, `{"detail":"Bad request"}`},
		{"empty 200", http.StatusOK, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := serveCounts(t, tc.status, tc.body)
			p, requests := rfPlan(t, rfSheets(t, bpData(bpFilter("Positive amounts", "0"))), bpTree())
			if err := b.client().countRowFilters("token", p, requests); err != nil {
				t.Fatalf("error = %v, want a warning", err)
			}
			if len(p.RowFilterCounts) != 0 || len(p.RowFilterWarnings) != 1 ||
				!strings.HasPrefix(p.RowFilterWarnings[0], "could not count rows for spec.sheets.Data.row_filter: ") ||
				strings.Contains(p.RowFilterWarnings[0], "\n") {
				t.Errorf("counts = %+v, warnings = %q", p.RowFilterCounts, p.RowFilterWarnings)
			}
		})
	}
}

func TestWriteRowFilterCountsNamesKeyTableAndSeries(t *testing.T) {
	first := "boom"
	p := &Plan{
		RowFilterCounts: []RowFilterCount{
			{Key: "spec.sheets.Data.row_filter", Table: 5, Count: &api.RowFilterCountSchema{Matched: 12, Total: 40}},
			{Key: "spec.sheets.Kept.row_filter", Table: 6, Kept: true, Count: &api.RowFilterCountSchema{Matched: 1, Total: 1}},
			{Key: "spec.sheets.Monthly.row_filter", Table: 7, Series: true,
				Count: &api.RowFilterCountSchema{Matched: 10000, Total: 52000, Capped: true, PredicateErrors: 2, FirstPredicateError: &first}},
			{Key: "spec.cells.Summary!B1.total.row_filter", Table: 8, Count: &api.RowFilterCountSchema{}},
		},
		RowFilterWarnings: []string{"could not count rows for spec.sheets.Other.row_filter: denied"},
	}
	var b strings.Builder
	if err := p.WriteRowFilterCounts(&b); err != nil {
		t.Fatal(err)
	}
	want := `Row filter spec.sheets.Data.row_filter on table 5:
  Keeps 12 of 40 rows
Row filter spec.sheets.Kept.row_filter (the stored filter, which the save keeps) on table 6:
  Keeps 1 of 1 row
Row filter spec.sheets.Monthly.row_filter on table 7, the series' reference table:
  Keeps 10000+ of 52000 rows
  Counting stopped at a server limit, so the counts marked + are lower bounds.
  Not counted: 2+ rows where the predicate failed.
  First failure: boom
Row filter spec.cells.Summary!B1.total.row_filter on table 8:
  No live rows to count: the table is empty.
Warning: could not count rows for spec.sheets.Other.row_filter: denied
`
	if b.String() != want {
		t.Errorf("block =\n%s\nwant\n%s", b.String(), want)
	}
}

func TestWriteRowFilterCountWritesOneCountAsTheBlockDoes(t *testing.T) {
	first := "boom"
	for _, tc := range []struct {
		name  string
		count RowFilterCount
		want  string
	}{
		{name: "sent", count: RowFilterCount{Key: "spec.sheets.Data.row_filter", Table: 5, Count: &api.RowFilterCountSchema{Matched: 12, Total: 40}},
			want: "Row filter spec.sheets.Data.row_filter on table 5:\n  Keeps 12 of 40 rows\n"},
		{name: "kept on a series", count: RowFilterCount{Key: "spec.sheets.Monthly.row_filter", Table: 7, Series: true, Kept: true,
			Count: &api.RowFilterCountSchema{Matched: 10000, Total: 52000, Capped: true, PredicateErrors: 2, FirstPredicateError: &first}},
			want: "Row filter spec.sheets.Monthly.row_filter (the stored filter, which the save keeps) on table 7, the series' reference table:\n" +
				"  Keeps 10000+ of 52000 rows\n" +
				"  Counting stopped at a server limit, so the counts marked + are lower bounds.\n" +
				"  Not counted: 2+ rows where the predicate failed.\n" +
				"  First failure: boom\n"},
		{name: "empty table", count: RowFilterCount{Key: "spec.cells.Summary!B1.total.row_filter", Table: 8, Count: &api.RowFilterCountSchema{}},
			want: "Row filter spec.cells.Summary!B1.total.row_filter on table 8:\n  No live rows to count: the table is empty.\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var one strings.Builder
			if err := WriteRowFilterCount(&one, tc.count); err != nil {
				t.Fatal(err)
			}
			if one.String() != tc.want {
				t.Errorf("count =\n%s\nwant\n%s", one.String(), tc.want)
			}
			var block strings.Builder
			if err := (&Plan{RowFilterCounts: []RowFilterCount{tc.count}}).WriteRowFilterCounts(&block); err != nil {
				t.Fatal(err)
			}
			if block.String() != one.String() {
				t.Errorf("WriteRowFilterCounts =\n%s\nWriteRowFilterCount =\n%s", block.String(), one.String())
			}
		})
	}
}

// goneFilter is a stored filter reading columns amount and gone.
func goneFilter() *api.RowFilter {
	description, operand := "Positive gone", "0"
	return &api.RowFilter{Description: &description, Conditions: &[]api.RowFilterCondition{
		{ColumnName: "amount", Operator: api.Gt, Value: &operand}, {ColumnName: "gone", Operator: api.Gt, Value: &operand}}}
}

func TestStoredFiltersThatNoSaveTouchesAreCheckedForMissingColumns(t *testing.T) {
	sheetWarning := `spec.sheets.Data.row_filter: the stored row filter reads column "gone", which is not in table 5's non-deleted applied schema; ` +
		"revision validation reports it as an error, and enable and preview refuse the release, until spec.sheets.Data.row_filter " +
		"is set to a row filter that fits the table, or to null to clear it"
	stale := bpFiltered(bpSheet(bpDataID, "Data", 5, []int{1}, 2), goneFilter())
	cell := storedCell(t, "{total: {table: 6, func: count}}")
	cell.RowFilter = goneFilter()
	for _, tc := range []struct {
		name, cells, sheet string
		tree               *api.DeliverableRevisionResponse
		columns            []string
		want               []string
	}{
		{name: "an unchanged sheet", sheet: bpData(), tree: bpTree(stale), columns: []string{"amount"}, want: []string{sheetWarning}},
		{name: "two missing columns", sheet: bpData(), tree: bpTree(stale), columns: []string{"policy_id"},
			want: []string{strings.Replace(sheetWarning, `column "gone", which is`, `columns "amount", "gone", which are`, 1)}},
		{name: "every column present", sheet: bpData(), tree: bpTree(stale), columns: []string{"amount", "gone"}},
		{name: "a sheet whose PATCH keeps the filter", sheet: bpSheetDoc("Data", bpBase, "header_rows: [1]", "data_start_row: 3"),
			tree: bpTree(stale), columns: []string{"amount"}},
		{name: "a sheet whose PATCH clears the filter", sheet: bpData("row_filter: null"), tree: bpTree(stale), columns: []string{"amount"}},
		{name: "an unchanged total", cells: "{Summary!B1: {total: {table: 6, func: count}}}", tree: treeWithCell(cell), columns: []string{"amount"},
			want: []string{`spec.cells.Summary!B1.total.row_filter: the stored row filter reads column "gone", which is not in table 6's non-deleted ` +
				"applied schema; revision validation reports it as an error, and enable and preview refuse the release, until " +
				"spec.cells.Summary!B1.total.row_filter is set to a row filter that fits the table, or to null to clear it"}},
		{name: "a total whose save keeps the filter", cells: "{Summary!B1: {total: {table: 6, func: sum, column: amount}}}", tree: treeWithCell(cell),
			columns: []string{"amount"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := serveCounts(t, http.StatusOK, rfCountBody, tc.columns...)
			var m *Manifest
			if tc.cells != "" {
				m = cellManifest(t, tc.cells)
			} else {
				m = rfSheets(t, tc.sheet)
			}
			p, requests := rfPlan(t, m, tc.tree)
			b.client().warnMissingFilterColumns("token", p, requests)
			if !reflect.DeepEqual(p.RowFilterWarnings, tc.want) {
				t.Errorf("warnings =\n%q\nwant\n%q", p.RowFilterWarnings, tc.want)
			}
		})
	}
}

func TestMissingFilterColumnChecksReadEachTableOnce(t *testing.T) {
	b := serveCounts(t, http.StatusOK, rfCountBody, "amount")
	other := bpFiltered(bpSheet(bpOtherID, "Other", 5, []int{}, 1), goneFilter())
	p, requests := rfPlan(t, rfSheets(t, bpData(), bpSheetDoc("Other", bpBase, "header_rows: []", "data_start_row: 1")),
		bpTree(bpFiltered(bpSheet(bpDataID, "Data", 5, []int{1}, 2), goneFilter()), other))
	b.client().warnMissingFilterColumns("token", p, requests)
	if got := b.requests("/migration-details"); len(got) != 1 {
		t.Errorf("schema reads = %v, want one", got)
	}
	if len(p.RowFilterWarnings) != 2 {
		t.Errorf("warnings = %q, want Data's and Other's", p.RowFilterWarnings)
	}

	b = serveCounts(t, http.StatusOK, rfCountBody)
	p.RowFilterWarnings, p.tableColumns = nil, map[int]map[string]bool{5: {"amount": true, "gone": true}}
	b.client().warnMissingFilterColumns("token", p, requests)
	if got := b.requests("/migration-details"); len(got) != 0 || len(p.RowFilterWarnings) != 0 {
		t.Errorf("schema reads = %v, warnings = %q; want the loaded columns reused", got, p.RowFilterWarnings)
	}
}

func TestMissingFilterColumnReadFailureIsAWarning(t *testing.T) {
	b := serveCounts(t, http.StatusOK, rfCountBody)
	b.mock.RegisterHandler(http.MethodGet, bpDatasets+"/5/migration-details", func(w http.ResponseWriter, _ *http.Request) {
		respondBody(w, http.StatusForbidden, `{"detail":"schema access denied"}`)
	})
	p, requests := rfPlan(t, rfSheets(t, bpData()), bpTree(bpFiltered(bpSheet(bpDataID, "Data", 5, []int{1}, 2), goneFilter())))
	b.client().warnMissingFilterColumns("token", p, requests)
	if len(p.RowFilterWarnings) != 1 || !strings.HasPrefix(p.RowFilterWarnings[0], "could not check the columns spec.sheets.Data.row_filter reads: ") {
		t.Errorf("warnings = %q", p.RowFilterWarnings)
	}
}

// Prepare counts after the binding refusals, so a refused plan counts
// nothing, and a plan with no filter makes no count request.
func TestPrepareCountsOnlyAPlanItWouldSend(t *testing.T) {
	for _, tc := range []struct {
		name, sheet string
		counts      int
		code        int
	}{
		{name: "a sent filter", sheet: bpData(bpFilter("Positive amounts", "0")), counts: 1},
		{name: "no filter", sheet: bpData("columns: {C: {column: amount}}")},
		{name: "a refused plan", sheet: bpData(bpFilter("Positive amounts", "0"), "columns: {C: {column: typo}}"), code: qerrors.ExitBadUsage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := servePlan(t, planDeliverable(nil), draftV3)
			b.serveBindings(revV3, bpTree())
			b.mock.RegisterHandler(http.MethodPost, rfCountPath(5), func(w http.ResponseWriter, _ *http.Request) {
				respondBody(w, http.StatusOK, rfCountBody)
			})
			p, err := b.client().Prepare("token", bpManifest(t, "", tc.sheet), ".")
			if code := qerrors.GetExitCode(err); code != tc.code {
				t.Fatalf("exit = %d, want %d; error = %v", code, tc.code, err)
			}
			var counts []string
			for _, request := range b.mock.Requests() {
				if strings.HasSuffix(request, "/row-filter-test") {
					counts = append(counts, request)
				}
			}
			if len(counts) != tc.counts || err == nil && len(p.RowFilterCounts) != tc.counts {
				t.Errorf("count requests = %v, want %d", counts, tc.counts)
			}
		})
	}
}
