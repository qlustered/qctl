package root

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const (
	// testFilterZetaID and testFilterAlphaID are revisionTreeRecord's sheet
	// bindings: Zeta reads table 9007199254740993, and Alpha follows series
	// exportSeriesID with reference table 42.
	testFilterZetaID  = "10000000-0000-4000-8000-000000000001"
	testFilterAlphaID = "10000000-0000-4000-8000-000000000002"

	// testFilterReport is a report with a key the generated type lacks and
	// integers past float64's exact range.
	testFilterReport = `{"row_filter":{"description":"Positive premiums","conditions":[{"column_name":"premium","operator":"gt",` +
		`"value":"0","value_high":null,"value_list":null,"case_sensitive":false}],"predicate":null},"kept":12,"left_out":3,"blocked":1,` +
		`"left_out_sample":[{"member_dataset_id":9007199254740993,"row_id":7,"quarantined":true,` +
		`"values":{"policy":9007199254740993,"premium":"-1.50"}}],"future":18446744073709551615}`

	// testFilterReportText is what WriteReport prints for testFilterReport.
	testFilterReportText = "Row filter:\n" +
		"  description: Positive premiums\n" +
		"  conditions (a row must meet every one):\n" +
		"    - premium gt \"0\"\n" +
		"12 kept / 3 left out / 1 blocked\n" +
		"Blocked: 1 row the filter cannot decide, such as a quarantined value that does not parse or a predicate that raises an error.\n" +
		"Left out, lowest row ids first (1 of 3):\n" +
		"  row 7 (quarantined): policy=9007199254740993, premium=\"-1.50\"\n"

	testFilterTablesPath = "/api/orgs/" + scopeTestOrgID + "/datasets"
)

// testFilterReportPath is the row filter report path of sheet on revision.
func testFilterReportPath(revision, sheet string) string {
	return revisionTestPath + revision + "/sheet-bindings/" + sheet + "/row-filter-report"
}

func resetTestFilterFlags(t *testing.T) {
	t.Helper()
	cmd, _, err := GetRootCmd().Find([]string{"deliverable", "test-filter"})
	if err != nil {
		t.Fatal(err)
	}
	reset := func() {
		for _, name := range []string{"sheet", "release", "revision", "period", "filename", "table", "table-id"} {
			flag := cmd.Flags().Lookup(name)
			if err := flag.Value.Set(flag.DefValue); err != nil {
				t.Fatal(err)
			}
			flag.Changed = false
		}
	}
	reset()
	t.Cleanup(reset)
}

// testFilterServer is revisionTestServer answering every sheet binding's row
// filter report on either revision with status and body, recording each
// report request's query.
func testFilterServer(t *testing.T, status int, body string) (*deliverableTestServer, *[]string) {
	t.Helper()
	resetTestFilterFlags(t)
	s := revisionTestServer(t)
	var queries []string
	for _, revision := range []string{enabledRevisionID, draftRevisionID} {
		for _, sheet := range []string{testFilterZetaID, testFilterAlphaID} {
			s.RegisterHandler("GET", testFilterReportPath(revision, sheet), func(w http.ResponseWriter, r *http.Request) {
				checkDeliverableToken(t, r)
				queries = append(queries, r.URL.RawQuery)
				testutil.RespondJSON(w, status, json.RawMessage(body))
			})
		}
	}
	return s, &queries
}

func runTestFilter(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := GetRootCmd()
	var out, errOut bytes.Buffer
	cmd.SetArgs(append([]string{"deliverable", "test-filter"}, args...))
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	t.Cleanup(func() {
		cmd.SetArgs(nil)
		cmd.SetOut(nil)
		cmd.SetErr(nil)
		resetPersistentFlag(t, cmd, "output", "table")
	})
	_, err := executeC()
	return out.String(), errOut.String(), err
}

// reportRequests counts the row filter report requests among requests.
func reportRequests(requests []string) int {
	n := 0
	for _, request := range requests {
		if strings.HasSuffix(request, "/row-filter-report") {
			n++
		}
	}
	return n
}

func TestDeliverableTestFilterReportsASavedSheetFilter(t *testing.T) {
	header := `deliverable/` + liveDeliverableID + ` release "2026.1" sheet "Zeta" on table 9007199254740993` + "\n"
	for _, format := range []string{"table", "json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			s, queries := testFilterServer(t, http.StatusOK, testFilterReport)
			out, errOut, err := runTestFilter(t, "monthly-bordereau", "--sheet", "Zeta", "-o", format)
			if err != nil {
				t.Fatal(err)
			}
			want := []string{deliverableRequest(), deliverableRequest(liveDeliverableID, "revisions"),
				"GET " + revisionTestPath + enabledRevisionID, "GET " + testFilterReportPath(enabledRevisionID, testFilterZetaID)}
			if got := s.Requests(); !reflect.DeepEqual(got, want) {
				t.Errorf("requests = %v, want %v", got, want)
			}
			if !reflect.DeepEqual(*queries, []string{""}) {
				t.Errorf("report queries = %q, want one with no period_key", *queries)
			}
			prose := errOut
			switch format {
			case "json":
				var indented bytes.Buffer
				if err := json.Indent(&indented, []byte(testFilterReport), "", "  "); err != nil {
					t.Fatal(err)
				}
				if want := indented.String() + "\n"; out != want {
					t.Errorf("stdout =\n%s\nwant the response body\n%s", out, want)
				}
			case "yaml":
				testutil.AssertYAMLReadsAsJSON(t, out, testFilterReport)
			default:
				prose = out
				if errOut != "" {
					t.Errorf("stderr = %q", errOut)
				}
			}
			if want := header + testFilterReportText; prose != want {
				t.Errorf("report =\n%s\nwant\n%s", prose, want)
			}
		})
	}
}

func TestDeliverableTestFilterNamesMaskedFields(t *testing.T) {
	const report = `{"row_filter":null,"kept":4,"left_out":1,"blocked":0,` +
		`"left_out_sample":[{"member_dataset_id":9007199254740993,"row_id":7,"quarantined":false,` +
		`"values":{"email":"•••••.com","region":"US"}}],"masked_fields":["email"]}`
	for _, format := range []string{"table", "json"} {
		t.Run(format, func(t *testing.T) {
			testFilterServer(t, http.StatusOK, report)
			out, errOut, err := runTestFilter(t, "monthly-bordereau", "--sheet", "Zeta", "-o", format)
			if err != nil {
				t.Fatal(err)
			}
			prose := out
			if format == "json" {
				prose = errOut
				var indented bytes.Buffer
				if err := json.Indent(&indented, []byte(report), "", "  "); err != nil {
					t.Fatal(err)
				}
				if want := indented.String() + "\n"; out != want {
					t.Errorf("stdout =\n%s\nwant the response body\n%s", out, want)
				}
			}
			if want := "Left out, lowest row ids first:\n" +
				"  row 7: email=\"•••••.com\", region=\"US\"\n" +
				"Masked fields: email\n"; !strings.HasSuffix(prose, want) {
				t.Errorf("report =\n%s\nwant it to end\n%s", prose, want)
			}
		})
	}
}

func TestDeliverableTestFilterSelectsTheRevision(t *testing.T) {
	for _, tc := range []struct {
		name, ref, revision, release string
		flags                        []string
	}{
		{"enabled by default", liveDeliverableID, enabledRevisionID, "2026.1", nil},
		{"release of a draft", "Monthly bordereau", draftRevisionID, "2026.2", []string{"--release", "2026.2"}},
		{"uuid release", liveDeliverableID, draftRevisionID, "2026.2", []string{"--release", draftRevisionID}},
		{"revision ref", "deliverable/" + liveDeliverableID, draftRevisionID, "2026.2", []string{"--revision", "deliverable-revision/" + draftRevisionID}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := testFilterServer(t, http.StatusOK, testFilterReport)
			out, _, err := runTestFilter(t, append([]string{tc.ref, "--sheet", "Zeta"}, tc.flags...)...)
			if err != nil {
				t.Fatal(err)
			}
			requests := s.Requests()
			if n := len(requests); n < 2 || requests[n-2] != "GET "+revisionTestPath+tc.revision ||
				requests[n-1] != "GET "+testFilterReportPath(tc.revision, testFilterZetaID) {
				t.Errorf("requests = %v", requests)
			}
			if want := `release "` + tc.release + `" sheet "Zeta"`; !strings.Contains(out, want) {
				t.Errorf("stdout = %q, want %q", out, want)
			}
		})
	}
}

func TestDeliverableTestFilterSeriesSheetSendsThePeriod(t *testing.T) {
	_, queries := testFilterServer(t, http.StatusOK, `{"row_filter":null,"kept":40,"left_out":0,"blocked":0,"left_out_sample":[]}`)
	out, _, err := runTestFilter(t, liveDeliverableID, "--sheet", "Alpha", "--period", "2026-09")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*queries, []string{"period_key=2026-09"}) {
		t.Errorf("report queries = %q, want period_key=2026-09", *queries)
	}
	want := `deliverable/` + liveDeliverableID + ` release "2026.1" sheet "Alpha" on period "2026-09" of series ` + exportSeriesID + "\n" +
		"Row filter:\n  (no filter: keeps every row)\n40 kept / 0 left out / 0 blocked\n"
	if out != want {
		t.Errorf("stdout =\n%s\nwant\n%s", out, want)
	}
}

// The period is checked against the bound sheet before the report is read.
func TestDeliverableTestFilterChecksThePeriodAgainstTheSheet(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		wants []string
	}{
		{"series sheet without a period", []string{"--sheet", "Alpha"},
			[]string{"series " + exportSeriesID, "--period", "'qctl describe table-series " + exportSeriesID + "'"}},
		{"period on a fixed sheet", []string{"--sheet", "Zeta", "--period", "2026-09"},
			[]string{`sheet "Zeta" reads table 9007199254740993`, "--period applies only to a series sheet"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := testFilterServer(t, http.StatusOK, testFilterReport)
			out, _, err := runTestFilter(t, append([]string{liveDeliverableID}, tc.args...)...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit = %d, want 2; error = %v", code, err)
			}
			for _, want := range tc.wants {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %v, want %q", err, want)
				}
			}
			if n := reportRequests(s.Requests()); n != 0 || out != "" {
				t.Errorf("report requests = %d and stdout = %q, want neither", n, out)
			}
		})
	}
}

func TestDeliverableTestFilterRefusesWrongTargets(t *testing.T) {
	for _, tc := range []struct {
		name, sheet, detail string
		flags               []string
		code                int
		message             string
	}{
		{name: "missing sheet", sheet: "Missing", code: 3, message: `no sheet binding matches "Missing" on release "2026.1"`},
		{name: "sheet names are exact", sheet: "zeta", code: 3, message: "no sheet binding matches"},
		{name: "sheet UUID is not a name", sheet: testFilterZetaID, code: 3, message: "no sheet binding matches"},
		{name: "missing release", flags: []string{"--release", "missing"}, code: 3, message: "no revision matches"},
		{
			name: "parent changed", detail: strings.ReplaceAll(revisionTreeRecord, liveDeliverableID, trashedDeliverableID),
			code: 3, message: "does not belong",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := testFilterServer(t, http.StatusOK, testFilterReport)
			if tc.detail != "" {
				answerRevisionTree(t, s.MockAPIServer, enabledRevisionID, tc.detail)
			}
			sheet := tc.sheet
			if sheet == "" {
				sheet = "Zeta"
			}
			_, _, err := runTestFilter(t, append([]string{liveDeliverableID, "--sheet", sheet}, tc.flags...)...)
			if code := qerrors.GetExitCode(err); code != tc.code {
				t.Fatalf("exit = %d, want %d; error = %v", code, tc.code, err)
			}
			if !strings.Contains(err.Error(), tc.message) {
				t.Errorf("error = %v, want %q", err, tc.message)
			}
			if n := reportRequests(s.Requests()); n != 0 {
				t.Errorf("report requests = %d, want none", n)
			}
		})
	}
}

func TestDeliverableTestFilterBackendErrors(t *testing.T) {
	for _, tc := range []struct {
		status, code int
		body, text   string
	}{
		{422, 2, `{"detail":{"msg":"Filter column 'premium' is integer in table 43 but text in the reference table 42.","error_code":"SCOPE_ERROR"}}`,
			"Filter column 'premium'"},
		{409, 2, `{"detail":{"msg":"Table 42's data storage is not available yet.","error_code":"TABLE_SETUP_IN_PROGRESS"}}`,
			"still being prepared"},
		{409, 2, `{"detail":{"msg":"The sheet binding's series has no table for period '2026-13'.","error_code":"SCOPE_ERROR"}}`,
			"no table for period"},
		{404, 3, `{"detail":"Table 42 can not be found in this organization."}`, "can not be found"},
		{403, 4, `{"detail":"User is not authorized"}`, "This operation needs deliverable:admin"},
		{500, 1, `{"detail":"Internal error"}`, "Internal error"},
	} {
		t.Run(fmt.Sprintf("%d %s", tc.status, tc.text), func(t *testing.T) {
			s, _ := testFilterServer(t, tc.status, tc.body)
			out, _, err := runTestFilter(t, liveDeliverableID, "--sheet", "Alpha", "--period", "2026-13", "-o", "json")
			if code := qerrors.GetExitCode(err); code != tc.code {
				t.Fatalf("exit = %d, want %d; error = %v", code, tc.code, err)
			}
			if out != "" || !strings.Contains(err.Error(), tc.text) {
				t.Errorf("stdout = %q, error = %v, want %q", out, err, tc.text)
			}
			if n := reportRequests(s.Requests()); n != 1 {
				t.Errorf("report requests = %d, want one", n)
			}
		})
	}
}

// testFilterManifest is the live deliverable's release 2026.2 with sheet
// Data on table orders keeping positive premiums and sheet Notes with no row
// filter.
var testFilterManifest = sheetsManifest("    Data:\n      table: orders\n      header_rows: [1]\n      data_start_row: 2\n",
	sheetsDataFilter("0"), sheetsNotes)

// draftCountServer is newDeliverableTestServer answering the name lookup of
// table orders with table 42, and the row filter count of tables 42 and 7
// with status and body, recording each count request's body.
func draftCountServer(t *testing.T, status int, body string) (*deliverableTestServer, map[string]string) {
	t.Helper()
	resetTestFilterFlags(t)
	s := newDeliverableTestServer(t)
	s.RegisterHandler("GET", testFilterTablesPath, func(w http.ResponseWriter, r *http.Request) {
		checkDeliverableToken(t, r)
		if got := r.URL.Query().Get("name"); got != "orders" {
			t.Errorf("table name lookup = %q, want orders", got)
		}
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"results":[{"id":42,"name":"orders"}],"total_rows":1,"page":1}`))
	})
	bodies := map[string]string{}
	for _, table := range []string{"42", "7"} {
		path := testFilterTablesPath + "/" + table + rowFilterCountRoute
		s.RegisterHandler("POST", path, func(w http.ResponseWriter, r *http.Request) {
			checkDeliverableToken(t, r)
			bodies[path] = readBody(t, r)
			testutil.RespondJSON(w, status, json.RawMessage(body))
		})
	}
	return s, bodies
}

func TestDeliverableTestFilterCountsADraftFilter(t *testing.T) {
	const wantBody = `{"row_filter":{"description":null,"conditions":[{"column_name":"premium","operator":"gt","value":"0",` +
		`"value_high":null,"value_list":null,"case_sensitive":false}]}}`
	for _, format := range []string{"table", "json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			s, bodies := draftCountServer(t, http.StatusOK, sheetsCount)
			path := writeManifest(t, t.TempDir(), "deliverable.yaml", testFilterManifest)
			out, errOut, err := runTestFilter(t, "-f", path, "--sheet", "Data", "-o", format)
			if err != nil {
				t.Fatal(err)
			}
			countPath := testFilterTablesPath + "/42" + rowFilterCountRoute
			if got, want := s.Requests(), []string{"GET " + testFilterTablesPath, "POST " + countPath}; !reflect.DeepEqual(got, want) {
				t.Errorf("requests = %v, want %v", got, want)
			}
			assertSeriesJSON(t, bodies[countPath], wantBody)
			prose := errOut
			switch format {
			case "json":
				assertSeriesJSON(t, out, sheetsCount)
			case "yaml":
				testutil.AssertYAMLReadsAsJSON(t, out, sheetsCount)
			default:
				prose = out
				if errOut != "" {
					t.Errorf("stderr = %q", errOut)
				}
			}
			if want := "Row filter spec.sheets.Data.row_filter on table 42:\n  Keeps 12 of 40 rows\n"; prose != want {
				t.Errorf("count =\n%s\nwant\n%s", prose, want)
			}
		})
	}
}

// A series sheet counts on its reference table, unless a table flag names
// another table, such as a period's.
func TestDeliverableTestFilterCountsASeriesSheet(t *testing.T) {
	manifest := strings.Replace(testFilterManifest, "      data_start_row: 2\n", "      data_start_row: 2\n      series: \""+exportSeriesID+"\"\n", 1)
	for _, tc := range []struct {
		name  string
		flags []string
		want  string
	}{
		{"reference table", nil, "Row filter spec.sheets.Data.row_filter on table 42, the series' reference table:\n  Keeps 12 of 40 rows\n"},
		{"table-id overrides", []string{"--table-id", "7"}, "Row filter spec.sheets.Data.row_filter on table 7:\n  Keeps 12 of 40 rows\n"},
		{"table ref overrides", []string{"--table-id", "table/7"}, "Row filter spec.sheets.Data.row_filter on table 7:\n  Keeps 12 of 40 rows\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := draftCountServer(t, http.StatusOK, sheetsCount)
			path := writeManifest(t, t.TempDir(), "deliverable.yaml", manifest)
			out, _, err := runTestFilter(t, append([]string{"-f", path, "--sheet", "Data"}, tc.flags...)...)
			if err != nil {
				t.Fatal(err)
			}
			if out != tc.want {
				t.Errorf("stdout =\n%s\nwant\n%s", out, tc.want)
			}
			if tc.flags != nil {
				want := []string{"POST " + testFilterTablesPath + "/7" + rowFilterCountRoute}
				if got := s.Requests(); !reflect.DeepEqual(got, want) {
					t.Errorf("requests = %v, want %v: a table flag skips the manifest's table", got, want)
				}
			}
		})
	}
}

func TestDeliverableTestFilterDraftFilterTheTableRejects(t *testing.T) {
	draftCountServer(t, http.StatusUnprocessableEntity, `{"detail":{"msg":"Row filter invalid: condition 1: column 'premium' is not in this table",`+
		`"error_code":"ERR_VALIDATION","loc":["body","row_filter"]}}`)
	path := writeManifest(t, t.TempDir(), "deliverable.yaml", testFilterManifest)
	out, _, err := runTestFilter(t, "-f", path, "--sheet", "Data")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Fatalf("exit = %d, want 2; error = %v", code, err)
	}
	if !strings.HasPrefix(err.Error(), "spec.sheets.Data.row_filter does not fit table 42: ") || !strings.Contains(err.Error(), "is not in this table") {
		t.Errorf("error = %v", err)
	}
	if out != "" {
		t.Errorf("stdout = %q", out)
	}
}

func TestDeliverableTestFilterDraftCountErrorsKeepTheirExitCode(t *testing.T) {
	for _, tc := range []struct {
		status, code int
	}{{403, 4}, {404, 3}, {500, 1}} {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			draftCountServer(t, tc.status, `{"detail":"refused"}`)
			path := writeManifest(t, t.TempDir(), "deliverable.yaml", testFilterManifest)
			_, _, err := runTestFilter(t, "-f", path, "--sheet", "Data")
			if code := qerrors.GetExitCode(err); code != tc.code {
				t.Errorf("exit = %d, want %d; error = %v", code, tc.code, err)
			}
			if strings.Contains(err.Error(), "does not fit") {
				t.Errorf("error = %v, want only a 422 to say the filter does not fit", err)
			}
		})
	}
}

// Every refusal below exits 2 and sends no request.
func TestDeliverableTestFilterLocalValidation(t *testing.T) {
	dir := t.TempDir()
	good := writeManifest(t, dir, "deliverable.yaml", testFilterManifest)
	for _, tc := range []struct {
		name    string
		args    []string
		message string
	}{
		{"neither input", []string{"--sheet", "Zeta"}, "nothing to test"},
		{"both inputs", []string{liveDeliverableID, "-f", good, "--sheet", "Data"}, "not both"},
		{"two refs", []string{liveDeliverableID, "other", "--sheet", "Zeta"}, "accepts at most 1 arg"},
		{"empty ref", []string{"", "--sheet", "Zeta"}, "cannot be empty"},
		{"no sheet", []string{liveDeliverableID}, "--sheet is required"},
		{"empty sheet", []string{liveDeliverableID, "--sheet", ""}, "--sheet is required"},
		{"empty sheet with -f", []string{"-f", good, "--sheet", ""}, "--sheet is required"},
		{"table with a ref", []string{liveDeliverableID, "--sheet", "Zeta", "--table", "orders"}, "--table cannot be used with a deliverable"},
		{"table-id with a ref", []string{liveDeliverableID, "--sheet", "Zeta", "--table-id", "7"}, "--table-id cannot be used with a deliverable"},
		{"blank period", []string{liveDeliverableID, "--sheet", "Alpha", "--period", " "}, "--period cannot be empty or blank"},
		{"empty period", []string{liveDeliverableID, "--sheet", "Alpha", "--period", ""}, "--period cannot be empty or blank"},
		{"empty release", []string{liveDeliverableID, "--sheet", "Zeta", "--release", ""}, "--release cannot be empty"},
		{"malformed revision", []string{liveDeliverableID, "--sheet", "Zeta", "--revision", "bad"}, "--revision must be a full UUID"},
		{"release and revision", []string{liveDeliverableID, "--sheet", "Zeta", "--release", "2026.2", "--revision", draftRevisionID}, "mutually exclusive"},
		{"empty -f", []string{"-f", "", "--sheet", "Data"}, "-f cannot be empty"},
		{"release with -f", []string{"-f", good, "--sheet", "Data", "--release", "2026.2"}, "--release cannot be used with -f"},
		{"revision with -f", []string{"-f", good, "--sheet", "Data", "--revision", draftRevisionID}, "--revision cannot be used with -f"},
		{"period with -f", []string{"-f", good, "--sheet", "Data", "--period", "2026-09"}, "--period cannot be used with -f"},
		{"both table flags with -f", []string{"-f", good, "--sheet", "Data", "--table", "orders", "--table-id", "7"}, "table specified more than once"},
		{"empty table with -f", []string{"-f", good, "--sheet", "Data", "--table", ""}, "--table cannot be empty"},
		{"name output", []string{liveDeliverableID, "--sheet", "Zeta", "-o", "name"}, `unknown output format "name"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := testFilterServer(t, http.StatusOK, testFilterReport)
			out, _, err := runTestFilter(t, tc.args...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit = %d, want 2; error = %v", code, err)
			}
			if !strings.Contains(err.Error(), tc.message) {
				t.Errorf("error = %v, want %q", err, tc.message)
			}
			if got := s.Requests(); len(got) != 0 || out != "" {
				t.Errorf("requests = %v and stdout = %q, want neither", got, out)
			}
		})
	}
}

// Every refusal below reads the -f file, exits 2 and sends no request.
func TestDeliverableTestFilterManifestRefusals(t *testing.T) {
	nullFilter := strings.Replace(testFilterManifest, sheetsNotes, sheetsNotes+"      row_filter: null\n", 1)
	for _, tc := range []struct {
		name, manifest, sheet, message string
	}{
		{"several documents", testFilterManifest + "---\n" + testFilterManifest, "Data", "reads one Deliverable document; "},
		{"empty file", "", "Data", "is empty: give one kind: Deliverable document"},
		{"another kind", "apiVersion: qluster.ai/v1\nkind: TableRule\nspec: {}\n", "Data", "kind: Deliverable"},
		{"missing sheet", testFilterManifest, "Premium", `has no sheet "Premium"; its sheets: "Data", "Notes"`},
		{"sheet names are exact", testFilterManifest, "data", `has no sheet "data"`},
		{"no sheets", deliverableManifestHead + "monthly-bordereau\n" + liveDeliverableSpec + "  release: \"2026.2\"\n", "Data",
			`has no sheet "Data"; its sheets: none`},
		{"missing row_filter", testFilterManifest, "Notes", "spec.sheets.Notes.row_filter is missing from "},
		{"null row_filter", nullFilter, "Notes", "spec.sheets.Notes.row_filter is null in "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := testFilterServer(t, http.StatusOK, testFilterReport)
			path := writeManifest(t, t.TempDir(), "deliverable.yaml", tc.manifest)
			out, _, err := runTestFilter(t, "-f", path, "--sheet", tc.sheet)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit = %d, want 2; error = %v", code, err)
			}
			if !strings.Contains(err.Error(), tc.message) {
				t.Errorf("error = %v, want %q", err, tc.message)
			}
			if got := s.Requests(); len(got) != 0 || out != "" {
				t.Errorf("requests = %v and stdout = %q, want neither", got, out)
			}
		})
	}
	t.Run("missing file", func(t *testing.T) {
		resetTestFilterFlags(t)
		testutil.NewTestEnv(t)
		_, _, err := runTestFilter(t, "-f", filepath.Join(t.TempDir(), "missing.yaml"), "--sheet", "Data")
		if err == nil || !strings.Contains(err.Error(), "failed to read file") {
			t.Errorf("error = %v, want the read failure", err)
		}
	})
}
