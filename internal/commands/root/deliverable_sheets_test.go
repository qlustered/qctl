package root

import (
	"encoding/json"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/deliverables"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"golang.org/x/term"
)

const (
	sheetsDataID  = "5a000000-0000-4000-8000-000000000001"
	sheetsNotesID = "5a000000-0000-4000-8000-000000000002"
	sheetsColumnA = "5c000000-0000-4000-8000-000000000001"
	sheetsColumnB = "5c000000-0000-4000-8000-000000000002"
	sheetsColumnC = "5c000000-0000-4000-8000-000000000003"

	draftSheetBindingsPath = "/api/orgs/" + scopeTestOrgID + "/deliverable-revisions/" + draftRevisionID + "/sheet-bindings"

	// rowFilterCountRoute ends each table's row filter count path, and
	// sheetsCountPath is table 42's.
	rowFilterCountRoute = "/row-filter-test"
	sheetsCountPath     = "/api/orgs/" + scopeTestOrgID + "/datasets/42" + rowFilterCountRoute
	// sheetsCount is the count sheetsServer answers.
	sheetsCount            = `{"matched":12,"total":40,"capped":false,"predicate_errors":0,"first_predicate_error":null}`
	dataColumnBindingsPath = "/api/orgs/" + scopeTestOrgID + "/deliverable-sheet-bindings/" + sheetsDataID + "/column-bindings"

	// sheetsColumnBindings are sheet Data's column bindings: A from policy
	// and B from premium.
	sheetsColumnBindings = `[
    {"id":"` + sheetsColumnA + `","target_column_index":1,"source_type":"column","source_column":"policy","run_param":null,
     "format_override":null,"origin":"user","target_label":"Policy","needs_confirmation":false,"warnings":[]},
    {"id":"` + sheetsColumnB + `","target_column_index":2,"source_type":"column","source_column":"premium","run_param":null,
     "format_override":null,"origin":"user","target_label":"Premium","needs_confirmation":false,"warnings":[]}]`

	// sheetsDraftTree is draft release 2026.2 with template 8, sheet Data
	// (table 42, header row 1, data from row 2, columns A and B) and sheet
	// Notes (table 42, no header rows, data from row 1, no columns).
	sheetsDraftTree = `{"id":"` + draftRevisionID + `","deliverable_id":"` + liveDeliverableID + `","release":"2026.2","state":"draft",
 "description":null,"skeleton_id":8,"enabled_by_id":null,"enabled_at":null,
 "sheet_bindings":[
  {"id":"` + sheetsDataID + `","sheet_name":"Data","member_dataset_id":42,"member_series_id":null,"data_start_row":2,
   "header_rows":[1],"instruction_row_count":0,"row_filter":null,"sort_keys":[],"column_bindings":` + sheetsColumnBindings + `},
  {"id":"` + sheetsNotesID + `","sheet_name":"Notes","member_dataset_id":42,"member_series_id":null,"data_start_row":1,
   "header_rows":[],"instruction_row_count":0,"row_filter":null,"sort_keys":[],"column_bindings":[]}],
 "cell_bindings":[]}`

	// The spec.sheets entries that match sheetsDraftTree.
	sheetsData        = "    Data:\n      table: 42\n      header_rows: [1]\n      data_start_row: 2\n"
	sheetsDataColumns = "      columns:\n        A: {column: policy}\n        B: {column: premium}\n"
	sheetsNotes       = "    Notes:\n      table: 42\n      header_rows: []\n      data_start_row: 1\n"

	// exportSeriesID is the series of revisionTreeRecord's sheet Alpha.
	exportSeriesID = "11111111-2222-4333-8444-555555555555"
)

// sheetsManifest is the live deliverable's draft release 2026.2 with
// spec.sheets made of sheets.
func sheetsManifest(sheets ...string) string {
	return deliverableManifestHead + "monthly-bordereau\n" + liveDeliverableSpec + "  release: \"2026.2\"\n  sheets:\n" + strings.Join(sheets, "")
}

// The binding writes the sheetsManifest cases plan against sheetsDraftTree.
var (
	deleteColumnB   = "DELETE " + dataColumnBindingsPath + "/" + sheetsColumnB
	deleteNotes     = "DELETE " + draftSheetBindingsPath + "/" + sheetsNotesID
	patchData       = "PATCH " + draftSheetBindingsPath + "/" + sheetsDataID
	postColumn      = "POST " + dataColumnBindingsPath
	removalManifest = map[string]struct {
		manifest string
		write    string
	}{
		"column removal": {sheetsManifest(sheetsData, "      columns:\n        A: {column: policy}\n", sheetsNotes), deleteColumnB},
		"sheet removal":  {sheetsManifest(sheetsData, sheetsDataColumns), deleteNotes},
		// A layout change on a sheet whose columns are listed deletes nothing
		// as planned, but the server may re-anchor the columns first.
		"re-anchor": {sheetsManifest(strings.Replace(sheetsData, "data_start_row: 2", "data_start_row: 3", 1), sheetsDataColumns, sheetsNotes), patchData},
	}
)

// sheetsServer is deliverableManifestServer with sheetsDraftTree as release
// 2026.2's binding tree, answering every binding write sheetsManifest plans
// with its success status and table 42's row filter count with sheetsCount,
// and recording each body by request.
func sheetsServer(t *testing.T) *manifestServer {
	t.Helper()
	s := deliverableManifestServer(t)
	answerRevisionTree(t, s.MockAPIServer, draftRevisionID, sheetsDraftTree)
	for _, path := range []string{dataColumnBindingsPath + "/" + sheetsColumnB, draftSheetBindingsPath + "/" + sheetsNotesID} {
		s.RegisterHandler("DELETE", path, func(w http.ResponseWriter, r *http.Request) {
			checkDeliverableToken(t, r)
			w.WriteHeader(http.StatusNoContent)
		})
	}
	s.answer(t, "PATCH", draftSheetBindingsPath+"/"+sheetsDataID, `{"id":"`+sheetsDataID+`","sheet_name":"Data","member_dataset_id":42,`+
		`"member_series_id":null,"data_start_row":3,"header_rows":[1],"instruction_row_count":0,"row_filter":null,"sort_keys":[],`+
		`"column_bindings":`+sheetsColumnBindings+`,"anchor_report":{"column_bindings":[],"cell_bindings":[]}}`)
	s.answer(t, "POST", dataColumnBindingsPath, `{"id":"`+sheetsColumnC+`","target_column_index":3,"source_type":"column",`+
		`"source_column":"claims","run_param":null,"format_override":null,"origin":"user","target_label":"Claims","needs_confirmation":false,"warnings":[]}`)
	s.answer(t, "POST", sheetsCountPath, sheetsCount)
	return s
}

// requireNonTTY skips a test whose refusal needs a stdin that cannot prompt.
func requireNonTTY(t *testing.T) {
	t.Helper()
	if term.IsTerminal(int(os.Stdin.Fd())) {
		t.Skip("requires non-TTY stdin")
	}
}

// answerPrompts makes every confirmation prompt readable and answers it with
// answer.
func answerPrompts(t *testing.T, answer string) {
	t.Helper()
	cmdutil.SetReader(strings.NewReader(answer))
	t.Cleanup(cmdutil.ResetReader)
}

func TestDiffDeliverableReAnchorPlansNoDelete(t *testing.T) {
	sheetsServer(t)
	path := writeManifest(t, t.TempDir(), "deliverable.yaml", removalManifest["re-anchor"].manifest)
	out, _, err := runManifest(t, "diff", "-f", path)
	if code := qerrors.GetExitCode(err); code != 1 {
		t.Fatalf("exit = %d, want 1; error = %v", code, err)
	}
	if !strings.HasPrefix(out, "PATCH /deliverable-revisions/"+draftRevisionID+"/sheet-bindings/"+sheetsDataID+"\n") || strings.Contains(out, "DELETE ") {
		t.Errorf("diff = %s, want the sheet PATCH and no DELETE", out)
	}
}

func TestApplyDeliverableBindingRemovalWithoutYesExitsTwoBeforeAnyWrite(t *testing.T) {
	requireNonTTY(t)
	for name, tc := range removalManifest {
		t.Run(name, func(t *testing.T) {
			s := sheetsServer(t)
			path := writeManifest(t, t.TempDir(), "deliverable.yaml", tc.manifest)
			out, _, err := runManifest(t, "apply", "-f", path)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit = %d, want %d; error = %v", code, qerrors.ExitBadUsage, err)
			}
			if got := s.writes(); len(got) != 0 {
				t.Errorf("writes = %v, want none", got)
			}
			if out != "" {
				t.Errorf("stdout = %q", out)
			}
		})
	}
}

func TestApplyDeliverableBindingRemovalWithYesSendsIt(t *testing.T) {
	for name, tc := range removalManifest {
		t.Run(name, func(t *testing.T) {
			s := sheetsServer(t)
			path := writeManifest(t, t.TempDir(), "deliverable.yaml", tc.manifest)
			if _, _, err := runManifest(t, "apply", "-f", path, "--yes"); err != nil {
				t.Fatal(err)
			}
			if got := s.writes(); !reflect.DeepEqual(got, []string{tc.write}) {
				t.Errorf("writes = %v, want %v", got, []string{tc.write})
			}
			if tc.write == patchData {
				assertSeriesJSON(t, s.bodies[patchData], `{"member_dataset_id":42,"sheet_name":"Data","data_start_row":3,"header_rows":[1],`+
					`"instruction_row_count":0,"member_series_id":null,"sort_keys":[]}`)
			}
		})
	}
}

// Only a removal, or a re-anchor of listed columns, asks.
func TestApplyDeliverableBindingAdditionNeedsNoConfirmation(t *testing.T) {
	requireNonTTY(t)
	s := sheetsServer(t)
	path := writeManifest(t, t.TempDir(), "deliverable.yaml",
		sheetsManifest(sheetsData, sheetsDataColumns, "        C: {column: claims}\n", sheetsNotes))
	if _, _, err := runManifest(t, "apply", "-f", path); err != nil {
		t.Fatal(err)
	}
	if got := s.writes(); !reflect.DeepEqual(got, []string{postColumn}) {
		t.Errorf("writes = %v, want %v", got, []string{postColumn})
	}
	assertSeriesJSON(t, s.bodies[postColumn],
		`{"target_column_index":3,"source_type":"column","source_column":"claims","run_param":null,"format_override":null}`)
}

func TestApplyDeliverableBindingRemovalPromptsAtATerminal(t *testing.T) {
	for _, tc := range []struct {
		name, answer string
		says         string
		writes       []string
	}{
		{name: "column removal", answer: "y\n", writes: []string{deleteColumnB},
			says: `deliverable/monthly-bordereau: remove sheet "Data" column B` + "\n" +
				"DELETE /deliverable-sheet-bindings/" + sheetsDataID + "/column-bindings/" + sheetsColumnB + "\n"},
		{name: "sheet removal", answer: "y\n", writes: []string{deleteNotes},
			says: `deliverable/monthly-bordereau: remove sheet "Notes"` + "\n" +
				"DELETE /deliverable-revisions/" + draftRevisionID + "/sheet-bindings/" + sheetsNotesID + "\n"},
		{name: "re-anchor", answer: "y\n", writes: []string{patchData},
			says: "deliverable/monthly-bordereau: the server may move column bindings when it re-anchors a sheet, and apply then removes " +
				"any binding left outside the sheet's listed columns\n" +
				"PATCH /deliverable-revisions/" + draftRevisionID + "/sheet-bindings/" + sheetsDataID + "\n"},
		{name: "column removal", answer: "n\n",
			says: `deliverable/monthly-bordereau: remove sheet "Data" column B` + "\n"},
	} {
		t.Run(tc.name+"/"+strings.TrimSpace(tc.answer), func(t *testing.T) {
			s := sheetsServer(t)
			answerPrompts(t, tc.answer)
			path := writeManifest(t, t.TempDir(), "deliverable.yaml", removalManifest[tc.name].manifest)
			out, errOut, err := runManifest(t, "apply", "-f", path)
			if err != nil {
				t.Fatalf("error = %v, want a confirmed apply or a skipped document", err)
			}
			if !strings.HasPrefix(out, tc.says) {
				t.Errorf("stdout =\n%s\nwant it to start\n%s", out, tc.says)
			}
			if !strings.Contains(errOut, "Apply these deliverable changes? [y/N]") {
				t.Errorf("stderr = %q, want the prompt", errOut)
			}
			if got := s.writes(); !reflect.DeepEqual(got, tc.writes) {
				t.Errorf("writes = %v, want %v", got, tc.writes)
			}
		})
	}
}

// Under -o json or yaml a removal needs --yes even where it could prompt.
func TestApplyDeliverableBindingRemovalUnderStructuredOutputNeedsYes(t *testing.T) {
	for _, format := range []string{"yaml", "json"} {
		t.Run(format, func(t *testing.T) {
			s := sheetsServer(t)
			answerPrompts(t, "y\n")
			path := writeManifest(t, t.TempDir(), "deliverable.yaml", removalManifest["column removal"].manifest)
			out, _, err := runManifest(t, "apply", "-f", path, "-o", format)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit = %d, want %d; error = %v", code, qerrors.ExitBadUsage, err)
			}
			if got := s.writes(); len(got) != 0 || out != "" {
				t.Fatalf("writes = %v and stdout = %q, want neither", got, out)
			}
			out, _, err = runManifest(t, "apply", "-f", path, "-o", format, "--yes")
			if err != nil {
				t.Fatal(err)
			}
			if got := s.writes(); !reflect.DeepEqual(got, []string{deleteColumnB}) {
				t.Errorf("writes = %v, want %v", got, []string{deleteColumnB})
			}
			if m := mustParseDeliverable(t, out); m.Metadata.Slug != "monthly-bordereau" {
				t.Errorf("result manifest = %+v", m)
			}
		})
	}
}

// A protected context must be named with --context for a removal, even with
// --yes; a write that removes nothing is not gated.
func TestApplyDeliverableBindingRemovalOnAProtectedContext(t *testing.T) {
	requireNonTTY(t)
	addition := sheetsManifest(sheetsData, sheetsDataColumns, "        C: {column: claims}\n", sheetsNotes)
	for _, tc := range []struct {
		name     string
		manifest string
		named    bool
		code     int
		writes   []string
	}{
		{name: "removal", manifest: removalManifest["column removal"].manifest, code: qerrors.ExitBadUsage},
		{name: "re-anchor", manifest: removalManifest["re-anchor"].manifest, code: qerrors.ExitBadUsage},
		{name: "removal on a named context", manifest: removalManifest["column removal"].manifest, named: true, writes: []string{deleteColumnB}},
		{name: "addition", manifest: addition, writes: []string{postColumn}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := sheetsServer(t)
			cfg, err := config.Load()
			if err != nil {
				t.Fatal(err)
			}
			cfg.Contexts[cfg.CurrentContext].Protected = nil
			if err := cfg.Save(); err != nil {
				t.Fatal(err)
			}
			path := writeManifest(t, t.TempDir(), "deliverable.yaml", tc.manifest)
			args := []string{"apply", "-f", path, "--yes"}
			if tc.named {
				args = append(args, "--context", cfg.CurrentContext)
			}
			_, err = runRootWithContextFlag(t, args...)
			if code := qerrors.GetExitCode(err); code != tc.code {
				t.Fatalf("exit = %d, want %d; error = %v", code, tc.code, err)
			}
			if got := s.writes(); !reflect.DeepEqual(got, tc.writes) {
				t.Errorf("writes = %v, want %v", got, tc.writes)
			}
		})
	}
}

// exportServer is deliverableManifestServer with revisionTreeRecord as the
// enabled release's binding tree and the table series that tree's sheet
// Alpha follows.
func exportServer(t *testing.T) *manifestServer {
	t.Helper()
	s := deliverableManifestServer(t)
	// A mirror has neither its own table nor a row filter.
	tree := strings.Replace(revisionTreeRecord, `"aggregate_member_dataset_id":42,"aggregate_member_series_id":null,"same_rows_as_sheet_binding_id":`,
		`"aggregate_member_dataset_id":null,"aggregate_member_series_id":null,"same_rows_as_sheet_binding_id":`, 1)
	tree = strings.Replace(tree, `"row_filter":{"description":"Total premiums","conditions":[]}`, `"row_filter":null`, 1)
	answerRevisionTree(t, s.MockAPIServer, enabledRevisionID, tree)
	s.RegisterHandler("GET", seriesTestPath, func(w http.ResponseWriter, r *http.Request) {
		checkDeliverableToken(t, r)
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`[{"id":"`+exportSeriesID+`","organization_id":"550e8400-e29b-41d4-a716-446655440000",`+
			`"name":"Monthly claims","description":null,"created_at":"2026-08-01T12:00:00Z","updated_at":"2026-10-01T12:00:00Z","members":[]}]`))
	})
	return s
}

func TestGetDeliverableExportsSheetsThatApplyUnchanged(t *testing.T) {
	format0, series := "0.00", "Monthly claims"
	one := 1
	want := map[string]deliverables.ManifestSheet{
		"Alpha": {Table: "42", Series: &series, HeaderRows: []int{1}, DataStartRow: 3, InstructionRowCount: &one,
			RowOrder: []deliverables.ManifestSortKey{{Column: "policy"}, {Column: "premium", Descending: true}},
			Columns:  map[string]deliverables.ManifestColumn{"AA": {RunParam: "period"}, "B": {Column: "premium", Format: &format0}}},
		"Zeta": {Table: "9007199254740993", HeaderRows: []int{1, 2}, DataStartRow: 4, InstructionRowCount: &one,
			Columns: map[string]deliverables.ManifestColumn{}},
	}
	for _, format := range []string{"yaml", "json"} {
		t.Run(format, func(t *testing.T) {
			s := exportServer(t)
			exported, _, err := runManifest(t, "get", "deliverable", "monthly-bordereau", "-o", format)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Count(exported, "row_filter"); got != 1 {
				t.Errorf("export carries %d row filters, want Alpha's:\n%s", got, exported)
			}
			m := mustParseDeliverable(t, exported)
			if filter := m.Spec.Sheets["Alpha"].RowFilter; filter == nil || filter.DescriptionText() != "Positive premiums" ||
				len(filter.Conditions) != 1 || *filter.Conditions[0].Value != "9007199254740993" || !m.Spec.Sheets["Alpha"].Has("row_filter") {
				t.Errorf("Alpha row_filter = %+v, want the stored filter", filter)
			}
			if m.Spec.Release != "2026.1" || len(m.Spec.Sheets) != len(want) {
				t.Fatalf("export = %+v, want release 2026.1 with sheets Alpha and Zeta", m.Spec)
			}
			for name, sheet := range want {
				got := m.Spec.Sheets[name]
				if got.Table != sheet.Table || !reflect.DeepEqual(got.Series, sheet.Series) || !reflect.DeepEqual(got.HeaderRows, sheet.HeaderRows) ||
					got.DataStartRow != sheet.DataStartRow || !reflect.DeepEqual(got.InstructionRowCount, sheet.InstructionRowCount) ||
					!reflect.DeepEqual(got.RowOrder, sheet.RowOrder) || !reflect.DeepEqual(got.Columns, sheet.Columns) {
					t.Errorf("spec.sheets.%s = %+v, want %+v", name, got, sheet)
				}
			}
			path := writeManifest(t, t.TempDir(), "deliverable."+format, exported)

			out, errOut, err := runManifest(t, "diff", "-f", path, "-o", "table")
			if err != nil || out != "" || errOut != "" {
				t.Errorf("diff: stdout = %q, stderr = %q, error = %v", out, errOut, err)
			}
			out, errOut, err = runManifest(t, "apply", "-f", path, "-o", "table")
			if err != nil {
				t.Fatal(err)
			}
			if out != "deliverable/monthly-bordereau unchanged\n" || errOut != "" {
				t.Errorf("apply: stdout = %q, stderr = %q", out, errOut)
			}
			if got := s.writes(); len(got) != 0 {
				t.Errorf("writes = %v", got)
			}
		})
	}
}

// An unchanged apply prints the export get deliverable prints.
func TestApplyDeliverableUnchangedReturnsTheExport(t *testing.T) {
	exportServer(t)
	exported, _, err := runManifest(t, "get", "deliverable", "monthly-bordereau", "-o", "yaml")
	if err != nil {
		t.Fatal(err)
	}
	path := writeManifest(t, t.TempDir(), "deliverable.yaml", exported)
	out, _, err := runManifest(t, "apply", "-f", path, "-o", "yaml")
	if err != nil {
		t.Fatal(err)
	}
	if out != exported {
		t.Errorf("apply -o yaml =\n%s\nwant the export\n%s", out, exported)
	}
}

// With nothing to write, diff still prints the confirmation notes, on stderr.
func TestDiffDeliverableUnchangedPrintsConfirmationNotesOnStderr(t *testing.T) {
	s := sheetsServer(t)
	answerRevisionTree(t, s.MockAPIServer, draftRevisionID, strings.Replace(sheetsDraftTree,
		`"target_label":"Premium","needs_confirmation":false`, `"target_label":null,"needs_confirmation":true`, 1))
	path := writeManifest(t, t.TempDir(), "deliverable.yaml", sheetsManifest(sheetsData, sheetsDataColumns, sheetsNotes))
	out, errOut, err := runManifest(t, "diff", "-f", path)
	if err != nil {
		t.Fatalf("exit = %d, want 0; error = %v", qerrors.GetExitCode(err), err)
	}
	want := `Note: sheet "Data" column B needs confirmation: a template change or a sheet rename left it in place with no header label ` +
		"to re-anchor by; check: qctl describe deliverable-revision monthly-bordereau --release 2026.2; " +
		"confirm: qctl deliverable confirm-columns monthly-bordereau --release 2026.2 --sheet Data --column B\n"
	if out != "" || errOut != want {
		t.Errorf("stdout = %q, want empty; stderr =\n%s\nwant\n%s", out, errOut, want)
	}
}

// sheetsDataFilter is a spec.sheets row_filter line keeping premiums over
// operand, described as Positive premiums.
func sheetsDataFilter(operand string) string {
	return `      row_filter: {description: Positive premiums, conditions: [{column_name: premium, operator: gt, value: "` + operand + `"}]}` + "\n"
}

// diff prints the row filter a sheet PATCH sends, and apply sends it.
func TestApplyDeliverableSendsASheetRowFilter(t *testing.T) {
	requireNonTTY(t)
	s := sheetsServer(t)
	path := writeManifest(t, t.TempDir(), "deliverable.yaml", sheetsManifest(sheetsData, sheetsDataFilter("0"), sheetsDataColumns, sheetsNotes))
	out, _, err := runManifest(t, "diff", "-f", path)
	if code := qerrors.GetExitCode(err); code != 1 {
		t.Fatalf("diff exit = %d, want 1; error = %v", code, err)
	}
	want := "PATCH /deliverable-revisions/" + draftRevisionID + "/sheet-bindings/" + sheetsDataID + `
# changes row_filter
{
  "data_start_row": 2,
  "header_rows": [
    1
  ],
  "instruction_row_count": 0,
  "member_dataset_id": 42,
  "member_series_id": null,
  "row_filter": {
    "conditions": [
      {
        "case_sensitive": false,
        "column_name": "premium",
        "operator": "gt",
        "value": "0",
        "value_high": null,
        "value_list": null
      }
    ],
    "description": "Positive premiums"
  },
  "sheet_name": "Data",
  "sort_keys": []
}
Row filter spec.sheets.Data.row_filter on table 42:
  Keeps 12 of 40 rows
`
	if out != want {
		t.Errorf("diff =\n%s\nwant\n%s", out, want)
	}
	assertSeriesJSON(t, s.bodies["POST "+sheetsCountPath], `{"row_filter":{"description":null,`+
		`"conditions":[{"column_name":"premium","operator":"gt","value":"0","value_high":null,"value_list":null,"case_sensitive":false}]}}`)
	if _, _, err := runManifest(t, "apply", "-f", path); err != nil {
		t.Fatal(err)
	}
	if got := s.writes(); !reflect.DeepEqual(got, []string{patchData}) {
		t.Fatalf("writes = %v, want %v", got, []string{patchData})
	}
	assertSeriesJSON(t, s.bodies[patchData], `{"member_dataset_id":42,"sheet_name":"Data","data_start_row":2,"header_rows":[1],`+
		`"instruction_row_count":0,"member_series_id":null,"sort_keys":[],"row_filter":{"description":"Positive premiums",`+
		`"conditions":[{"column_name":"premium","operator":"gt","value":"0","value_high":null,"value_list":null,"case_sensitive":false}]}}`)
}

// A filter keeping other rows than the stored one under its description is
// refused before any write, by diff and by apply.
func TestDeliverableRefusesANewSheetFilterUnderTheStoredDescription(t *testing.T) {
	requireNonTTY(t)
	s := sheetsServer(t)
	answerRevisionTree(t, s.MockAPIServer, draftRevisionID, strings.Replace(sheetsDraftTree,
		`"header_rows":[1],"instruction_row_count":0,"row_filter":null`,
		`"header_rows":[1],"instruction_row_count":0,"row_filter":{"description":"Positive premiums",`+
			`"conditions":[{"column_name":"premium","operator":"gt","value":"0","value_high":null,"value_list":null,"case_sensitive":false}],"predicate":null}`, 1))
	path := writeManifest(t, t.TempDir(), "deliverable.yaml", sheetsManifest(sheetsData, sheetsDataFilter("100"), sheetsDataColumns, sheetsNotes))
	for _, verb := range []string{"diff", "apply"} {
		out, _, err := runManifest(t, verb, "-f", path)
		if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
			t.Fatalf("%s exit = %d, want %d; error = %v", verb, code, qerrors.ExitBadUsage, err)
		}
		if !strings.Contains(err.Error(), `spec.sheets.Data.row_filter keeps different rows from the stored filter but repeats its description "Positive premiums"`) {
			t.Errorf("%s error = %v", verb, err)
		}
		if got := s.writes(); len(got) != 0 || out != "" {
			t.Fatalf("%s: writes = %v and stdout = %q, want neither", verb, got, out)
		}
	}
}
