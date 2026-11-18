package deliverables

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const (
	abOrg = "/api/orgs/" + testOrgID

	abNewSheet   = "5a000000-0000-4000-8000-0000000000a1"
	abNewColumnA = "5c000000-0000-4000-8000-0000000000a1"
	abNewColumnC = "5c000000-0000-4000-8000-0000000000a3"
	abCopyData   = "5a000000-0000-4000-8000-0000000000b1"
	abCopyOld    = "5a000000-0000-4000-8000-0000000000b2"
	abCopyColumn = "5c000000-0000-4000-8000-0000000000b3"

	abV3Tree = "GET /deliverable-revisions/" + revV3
	abV4Tree = "GET /deliverable-revisions/" + revV4

	abFinish = "\nqctl diff -f on the same manifest shows what is left, and applying it again finishes it"
)

// abColumns is the column-bindings route of sheet binding id.
func abColumns(id string) string {
	return "/deliverable-sheet-bindings/" + id + "/column-bindings"
}

// abServer is a planBackend that records, in order, every request an on or
// fail handler answers, as "METHOD path body".
type abServer struct {
	*planBackend
	t    *testing.T
	sent []string
}

func serveApply(t *testing.T, current map[string]any, revisions ...map[string]any) *abServer {
	t.Helper()
	return &abServer{planBackend: servePlan(t, current, revisions...), t: t}
}

// on answers method on path, under the organization unless it starts with
// /bucket, with answers in turn, the last one again once they run out: a
// string verbatim, anything else as JSON, and no answer as an empty 204.
func (s *abServer) on(method, path string, answers ...any) {
	n := 0
	s.mock.RegisterHandler(method, abPath(path), func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		if len(answers) == 0 {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		answer := answers[min(n, len(answers)-1)]
		n++
		if text, ok := answer.(string); ok {
			respondBody(w, http.StatusOK, text)
			return
		}
		testutil.RespondJSON(w, http.StatusOK, answer)
	})
}

// fail answers method on path with status and body.
func (s *abServer) fail(method, path string, status int, body string) {
	s.mock.RegisterHandler(method, abPath(path), func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		respondBody(w, status, body)
	})
}

// trees answers the GET of revision id's tree with trees in turn, the last
// one again once they run out.
func (s *abServer) trees(id string, trees ...*api.DeliverableRevisionResponse) {
	n := 0
	s.mock.RegisterHandler(http.MethodGet, bpTreePath+id, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, trees[min(n, len(trees)-1)])
		n++
	})
}

func abPath(path string) string {
	if strings.HasPrefix(path, "/bucket") {
		return path
	}
	return abOrg + path
}

// record appends r's method, path without the organization prefix, and body:
// JSON with sorted keys, or its size when it is not a JSON object.
func (s *abServer) record(r *http.Request) {
	line := r.Method + " " + strings.TrimPrefix(r.URL.Path, abOrg)
	var data []byte
	if r.Body != nil {
		var err error
		if data, err = io.ReadAll(r.Body); err != nil {
			s.t.Errorf("read %s body: %v", line, err)
		}
	}
	if len(data) > 0 {
		var body map[string]any
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		if decoder.Decode(&body) == nil {
			line += " " + planBody(s.t, body)
		} else {
			line += fmt.Sprintf(" <%d bytes>", len(data))
		}
	}
	s.sent = append(s.sent, line)
}

// prepare plans m and returns the plan and the number of requests made so far.
func (s *abServer) prepare(m *Manifest, dir string) (*Plan, int) {
	s.t.Helper()
	p, err := s.client().Prepare("token", m, dir)
	if err != nil {
		s.t.Fatalf("Prepare: %v", err)
	}
	s.sent = nil
	return p, len(s.mock.Requests())
}

// since returns each request made after the first n, in order: a GET as
// "GET path", and any other as on or fail recorded it.
func (s *abServer) since(n int) []string {
	var out []string
	k := 0
	for _, request := range s.mock.Requests()[n:] {
		if strings.HasPrefix(request, http.MethodGet+" ") {
			out = append(out, strings.Replace(request, " "+abOrg, " ", 1))
			continue
		}
		if k >= len(s.sent) {
			out = append(out, request+" <unrecorded>")
			continue
		}
		out = append(out, s.sent[k])
		k++
	}
	return out
}

func (s *abServer) expect(n int, want ...string) {
	s.t.Helper()
	if got := s.since(n); !reflect.DeepEqual(got, want) {
		s.t.Errorf("requests =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestApplyCreatesASheetAndItsColumns(t *testing.T) {
	s := serveApply(t, planDeliverable(nil), enabledV2, draftV3)
	columnA, columnC := bpColumn(abNewColumnA, 1, "$period"), bpFormatted(bpColumn(abNewColumnC, 3, "amount"), "0.00")
	s.trees(revV3, bpTree(), bpTree(bpSheet(abNewSheet, "Data", 5, []int{1}, 2, columnA, columnC)))
	s.on(http.MethodPost, bpSheets, bpSheet(abNewSheet, "Data", 5, []int{1}, 2))
	s.on(http.MethodPost, abColumns(abNewSheet), columnA, columnC)
	p, n := s.prepare(bpManifest(t, "", bpData(`columns: {A: {run_param: period}, C: {column: amount, format: "0.00"}}`)), ".")
	placeholder := "POST " + abColumns(createdSheetBindingID)
	if got, want := planLines(p), []string{"POST " + bpSheets, placeholder, placeholder}; !reflect.DeepEqual(got, want) {
		t.Fatalf("plan = %v, want %v", got, want)
	}

	r, err := s.client().Apply("token", p)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	s.expect(n,
		"POST "+bpSheets+` {"data_start_row":2,"header_rows":[1],"member_dataset_id":5,"sheet_name":"Data"}`,
		"POST "+abColumns(abNewSheet)+` {"format_override":null,"run_param":"period","source_column":null,"source_type":"run_param","target_column_index":1}`,
		"POST "+abColumns(abNewSheet)+` {"format_override":"0.00","run_param":null,"source_column":"amount","source_type":"column","target_column_index":3}`,
		abV3Tree)
	if r.BindingWrites != 3 || r.Created || r.Updated || r.Revision != nil || r.AnchorReports != nil {
		t.Errorf("result = %+v", r)
	}
	columns := r.Manifest.Spec.Sheets["Data"].Columns
	if len(columns) != 2 || columns["A"].RunParam != "period" || columns["C"].Column != "amount" || r.Manifest.Status.RevisionID.String() != revV3 {
		t.Errorf("manifest = %+v, want the release read back with spec.sheets", r.Manifest)
	}
	if got, want := FormatApplyResult(p, r), "binding writes: 1 sheet created, 2 columns created\n"; got != want {
		t.Errorf("output =\n%s\nwant\n%s", got, want)
	}
}

func TestApplyPatchesASheetThenWritesItsColumns(t *testing.T) {
	s := serveApply(t, planDeliverable(nil), enabledV2, draftV3)
	flagged := bpFlagged(bpColumn(bpColumnA, 1, "$period"))
	claim, carrier := bpColumn(bpColumnC, 3, "claim_id"), bpColumn(abNewColumnC, 5, "carrier")
	s.trees(revV3,
		bpTree(bpSheet(bpDataID, "Data", 5, []int{1}, 2, flagged, bpColumn(bpColumnC, 3, "amount"), bpColumn(bpColumnD, 4, "policy_id")),
			bpSheet(bpOldID, "Old", 5, []int{1}, 2)),
		bpTree(bpSheet(bpDataID, "Data", 6, []int{1}, 2, flagged, claim, carrier)))
	s.on(http.MethodDelete, bpSheets+"/"+bpOldID)
	s.on(http.MethodPatch, bpSheets+"/"+bpDataID, api.SheetBindingPatchResponse{ID: uuid.MustParse(bpDataID), SheetName: "Data",
		MemberDatasetID: 6, HeaderRows: []int{1}, DataStartRow: 2, ColumnBindings: &[]api.ColumnBindingResponse{flagged, claim}})
	s.on(http.MethodDelete, bpDataColumns+"/"+bpColumnD)
	s.on(http.MethodPatch, bpDataColumns+"/"+bpColumnC, claim)
	s.on(http.MethodPost, bpDataColumns, carrier)
	p, n := s.prepare(bpManifest(t, "", bpSheetDoc("Data", "table: 6", "header_rows: [1]", "data_start_row: 2",
		"columns: {A: {run_param: period}, C: {column: claim_id}, E: {column: carrier}}")), ".")
	var diff strings.Builder
	if err := p.WriteRequests(&diff); err != nil {
		t.Fatal(err)
	}
	confirm := `Note: sheet "Data" column A needs confirmation: `
	if lines := strings.Split(strings.TrimSuffix(diff.String(), "\n"), "\n"); !strings.HasPrefix(lines[len(lines)-1], confirm) ||
		strings.Count(diff.String(), confirm) != 1 {
		t.Errorf("diff does not end with the one confirmation note:\n%s", diff.String())
	}

	r, err := s.client().Apply("token", p)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	s.expect(n,
		"DELETE "+bpSheets+"/"+bpOldID,
		"PATCH "+bpSheets+"/"+bpDataID+` {"data_start_row":2,"header_rows":[1],"instruction_row_count":0,"member_dataset_id":6,`+
			`"member_series_id":null,"sheet_name":"Data","sort_keys":[]}`,
		"DELETE "+bpDataColumns+"/"+bpColumnD,
		"PATCH "+bpDataColumns+"/"+bpColumnC+` {"format_override":null,"run_param":null,"source_column":"claim_id","source_type":"column","target_column_index":3}`,
		"POST "+bpDataColumns+` {"format_override":null,"run_param":null,"source_column":"carrier","source_type":"column","target_column_index":5}`,
		abV3Tree)
	want := "binding writes: 1 sheet deleted, 1 sheet updated, 1 column deleted, 1 column updated, 1 column created\n" + confirm +
		"a template change or a sheet rename left it in place with no header label to re-anchor by; " +
		"check: qctl describe deliverable-revision carrier-a-bordereau --release v3; " +
		"confirm: qctl deliverable confirm-columns carrier-a-bordereau --release v3 --sheet Data --column A\n"
	if got := FormatApplyResult(p, r); got != want {
		t.Errorf("output =\n%s\nwant\n%s", got, want)
	}
	if r.BindingWrites != 5 || r.AnchorReports != nil {
		t.Errorf("BindingWrites = %d, AnchorReports = %v", r.BindingWrites, r.AnchorReports)
	}
}

func TestApplyReadsTheBindingTreeOnceWithASeries(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			s := serveApply(t, planDeliverable(nil), enabledV2, draftV3)
			sheet := bpSheet(bpDataID, "Data", 5, []int{1}, 2, bpFlagged(bpColumn(bpColumnA, 1, "$period")))
			seriesID := uuid.MustParse(namedSeriesID)
			sheet.MemberSeriesID = &seriesID
			stored := bpTree(sheet)
			sheet.InstructionRowCount = 1
			final := bpTree(sheet)
			s.trees(revV3, stored, final)
			s.on(http.MethodPatch, bpSheets+"/"+bpDataID, sheet)
			p, n := s.prepare(bpManifest(t, "", bpData("instruction_row_count: 1")), ".")
			if status == http.StatusOK {
				s.on(http.MethodGet, "/table-series", exportSeriesBody)
			} else {
				s.fail(http.MethodGet, "/table-series", status, `{"detail":"no"}`)
			}

			r, err := s.client().Apply("token", p)
			s.expect(n, "PATCH "+bpSheets+"/"+bpDataID+
				` {"data_start_row":2,"header_rows":[1],"instruction_row_count":1,"member_dataset_id":5,`+
				`"member_series_id":"`+namedSeriesID+`","sheet_name":"Data","sort_keys":[]}`,
				abV3Tree, "GET /table-series")
			if r == nil || r.BindingWrites != 1 || len(r.confirmations) != 1 ||
				!strings.Contains(r.confirmations[0], `sheet "Data" column A needs confirmation`) {
				t.Fatalf("result = %+v, error = %v; want the write and confirmation note", r, err)
			}
			if status == http.StatusForbidden {
				if qerrors.GetExitCode(err) != qerrors.ExitUnauthorized ||
					!reflect.DeepEqual(r.Manifest, FromAPI(r.Deliverable, p.Revision)) {
					t.Fatalf("manifest = %+v, error = %v; want FromAPI and exit %d", r.Manifest, err, qerrors.ExitUnauthorized)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			exported := r.Manifest.Spec.Sheets["Data"]
			if exported.Series == nil || *exported.Series != "Monthly claims" || exported.InstructionRowCount == nil ||
				*exported.InstructionRowCount != 1 || exported.Columns["A"].RunParam != "period" {
				t.Fatalf("sheet = %+v; want the post-write binding and series name", exported)
			}
			assertExportRoundTrips(t, r.Manifest)
		})
	}
}

// serveReanchor serves draft v3 whose sheet Data binds amount at A under the
// header label Amount and policy_id at C with no label, and a manifest moving
// its header row down one, which the server answers by moving A to B, where
// the new header row has Amount, and leaving C; it returns the plan and the
// requests made.
func serveReanchor(t *testing.T) (*abServer, *Plan, int) {
	t.Helper()
	s := serveApply(t, planDeliverable(nil), enabledV2, draftV3)
	label := "Amount"
	amount := bpColumn(bpColumnA, 1, "amount")
	amount.TargetLabel = &label
	moved := amount
	moved.TargetColumnIndex = 2
	policy := bpColumn(bpColumnC, 3, "policy_id")
	formatted := bpFormatted(moved, "0.00")
	s.trees(revV3, bpTree(bpSheet(bpDataID, "Data", 5, []int{1}, 2, amount, policy)),
		bpTree(bpSheet(bpDataID, "Data", 5, []int{2}, 3, formatted, policy, bpColumn(abNewColumnA, 4, "$period"))))
	s.on(http.MethodPatch, bpSheets+"/"+bpDataID, api.SheetBindingPatchResponse{ID: uuid.MustParse(bpDataID), SheetName: "Data", MemberDatasetID: 5, HeaderRows: []int{2}, DataStartRow: 3,

		AnchorReport: &api.BindingAnchorReport{CellBindings: []api.CellAnchorChange{}, ColumnBindings: []api.ColumnAnchorChange{{
			ColumnBindingID: uuid.MustParse(bpColumnA), SheetBindingID: uuid.MustParse(bpDataID), SheetName: "Data", TargetLabel: &label,
			FromColumnIndex: 1, ToColumnIndex: 2, Outcome: api.BindingAnchorOutcomeMoved}}}, ColumnBindings: &[]api.ColumnBindingResponse{moved, policy},
	})
	s.on(http.MethodPatch, bpDataColumns+"/"+bpColumnA, formatted)
	s.on(http.MethodPost, bpDataColumns, bpColumn(abNewColumnA, 4, "$period"))
	p, n := s.prepare(bpManifest(t, "", bpSheetDoc("Data", bpBase, "header_rows: [2]", "data_start_row: 3",
		`columns: {B: {column: amount, format: "0.00"}, C: {column: policy_id}, D: {run_param: period}}`)), ".")
	// Against the stored tree, B is a new binding and A goes.
	want := []string{"PATCH " + bpSheets + "/" + bpDataID, "DELETE " + bpDataColumns + "/" + bpColumnA, "POST " + bpDataColumns, "POST " + bpDataColumns}
	if got := planLines(p); !reflect.DeepEqual(got, want) {
		t.Fatalf("plan = %v, want %v", got, want)
	}
	if want := []string{`sheet "Data" column A`}; !reflect.DeepEqual(p.Removals, want) || !p.MayRemove {
		t.Fatalf("Removals = %q, MayRemove = %v", p.Removals, p.MayRemove)
	}
	return s, p, n
}

const (
	abReanchorPatch = "PATCH " + bpSheets + "/" + bpDataID + ` {"data_start_row":3,"header_rows":[2],"instruction_row_count":0,` +
		`"member_dataset_id":5,"member_series_id":null,"sheet_name":"Data","sort_keys":[]}`
	abMovedPatch = "PATCH " + bpDataColumns + "/" + bpColumnA +
		` {"format_override":"0.00","run_param":null,"source_column":"amount","source_type":"column","target_column_index":2}`
	abPeriodPost = "POST " + bpDataColumns +
		` {"format_override":null,"run_param":"period","source_column":null,"source_type":"run_param","target_column_index":4}`
)

func TestApplyReplansColumnsFromAReanchoringPatch(t *testing.T) {
	s, p, n := serveReanchor(t)
	r, err := s.client().Apply("token", p)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// The moved binding is patched where the server put it: nothing is
	// deleted, and B is not created twice.
	s.expect(n, abReanchorPatch, abMovedPatch, abPeriodPost, abV3Tree)
	want := "binding writes: 1 sheet updated, 1 column updated, 1 column created\n" +
		"\nRe-anchored bindings (1):\n  Column Data!A -> B: moved; label: Amount [" + bpColumnA + "]\n"
	if got := FormatApplyResult(p, r); got != want {
		t.Errorf("output =\n%s\nwant\n%s", got, want)
	}
	if len(r.AnchorReports) != 1 || r.BindingWrites != 3 {
		t.Errorf("AnchorReports = %v, BindingWrites = %d", r.AnchorReports, r.BindingWrites)
	}
}

func TestApplyStopsHalfwayThroughTheColumnWrites(t *testing.T) {
	s, p, n := serveReanchor(t)
	s.fail(http.MethodPost, bpDataColumns, http.StatusUnprocessableEntity, `{"detail":{"msg":"merged","error_code":"BINDING_INVALID",`+
		`"error_id":"","context":{"errors":[{"binding_kind":"column_binding","binding_id":null,"loc":["body","target_column_index"],`+
		`"msg":"Target column 4 intersects merged range(s) ['D5:E5']."}]}}}`)
	r, err := s.client().Apply("token", p)
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage || r == nil {
		t.Fatalf("result = %v, exit = %d, want the partial result and %d; error = %v", r, code, qerrors.ExitBadUsage, err)
	}
	// The re-plan left three requests where the plan had four.
	head := "deliverable/carrier-a-bordereau apply stopped at step 3 of 3 (POST " + bpDataColumns + ` for sheet "Data" column D), 2 completed: ` +
		"create deliverable column binding failed: 1 binding issue(s):\n  - spec.sheets.Data.columns.D: Target column 4 intersects merged range(s) ['D5:E5']."
	if err.Error() != head+abFinish {
		t.Errorf("error =\n%s\nwant\n%s", err, head+abFinish)
	}
	var invalid *RevisionInvalidError
	if !errors.As(err, &invalid) || len(invalid.Issues) != 1 {
		t.Errorf("error chain lost the BINDING_INVALID issues: %#v", err)
	}
	s.expect(n, abReanchorPatch, abMovedPatch, abPeriodPost)
	if r.BindingWrites != 2 || len(r.AnchorReports) != 1 || r.Manifest == nil || r.Manifest.Spec.Release != "v3" || r.Manifest.Has(keySheets) {
		t.Errorf("result = %+v, manifest = %+v", r, r.Manifest)
	}
}

func TestApplyStoppedAtASheetWriteNamesTheSheet(t *testing.T) {
	s := serveApply(t, planDeliverable(nil), enabledV2, draftV3)
	s.trees(revV3, bpTree(bpSheet(bpDataID, "Data", 5, []int{1}, 2), bpSheet(bpOldID, "Old", 5, []int{1}, 2)))
	s.on(http.MethodDelete, bpSheets+"/"+bpOldID)
	s.fail(http.MethodPatch, bpSheets+"/"+bpDataID, http.StatusUnprocessableEntity, `{"detail":{"msg":"invalid","error_code":"BINDING_INVALID",`+
		`"error_id":"","context":{"errors":[{"binding_kind":"sheet_binding","binding_id":null,"loc":["body","data_start_row"],`+
		`"msg":"Template cell A3 is not empty."}]}}}`)
	p, n := s.prepare(bpManifest(t, "", bpSheetDoc("Data", "table: 6", "header_rows: [1]", "data_start_row: 2")), ".")
	r, err := s.client().Apply("token", p)
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage || r == nil || r.BindingWrites != 1 {
		t.Fatalf("result = %+v, exit = %d, want the partial result and %d; error = %v", r, code, qerrors.ExitBadUsage, err)
	}
	want := "deliverable/carrier-a-bordereau apply stopped at step 2 of 2 (PATCH " + bpSheets + "/" + bpDataID + ` for sheet "Data"), 1 completed: `
	if !strings.HasPrefix(err.Error(), want) {
		t.Errorf("error =\n%s\nwant it to start\n%s", err, want)
	}
	s.expect(n, "DELETE "+bpSheets+"/"+bpOldID,
		"PATCH "+bpSheets+"/"+bpDataID+` {"data_start_row":2,"header_rows":[1],"instruction_row_count":0,"member_dataset_id":6,`+
			`"member_series_id":null,"sheet_name":"Data","sort_keys":[]}`)
}

func TestApplyReplansACopyAgainstTheCreatedRelease(t *testing.T) {
	s := serveApply(t, planDeliverable(nil), disabledV1, enabledV2)
	source := bpTree(bpSheet(bpDataID, "Data", 5, []int{1}, 2, bpColumn(bpColumnC, 3, "amount")), bpSheet(bpOldID, "Old", 5, []int{1}, 2))
	source.ID, source.State = uuid.MustParse(revV2), api.DeliverableRevisionStateEnabled
	s.trees(revV2, source)
	copyData := bpSheet(abCopyData, "Data", 5, []int{1}, 2, bpColumn(abCopyColumn, 3, "amount"))
	created := bpTree(copyData, bpSheet(abCopyOld, "Old", 5, []int{1}, 2))
	s.on(http.MethodPost, "/deliverables/"+monthlyID+"/revisions", api.DeliverableRevisionCreateResponse{ID: uuid.MustParse(revV4),
		DeliverableID: uuid.MustParse(monthlyID), Release: "v3", State: api.DeliverableRevisionStateDraft, SkeletonID: created.SkeletonID,
		CellBindings: created.CellBindings, SheetBindings: created.SheetBindings})
	s.on(http.MethodDelete, "/deliverable-revisions/"+revV4+"/sheet-bindings/"+abCopyOld)
	claim := bpColumn(abCopyColumn, 3, "claim_id")
	s.on(http.MethodPatch, abColumns(abCopyData)+"/"+abCopyColumn, claim)
	final := bpTree(bpSheet(abCopyData, "Data", 5, []int{1}, 2, claim))
	final.ID = uuid.MustParse(revV4)
	s.trees(revV4, final)
	p, n := s.prepare(bpManifest(t, "", bpData("columns: {C: {column: claim_id}}")), ".")
	want := []string{"POST /deliverables/" + monthlyID + "/revisions",
		"DELETE /deliverable-revisions/<created-revision-id>/sheet-bindings/<copied-sheet-binding-id>",
		"PATCH " + abColumns(copiedSheetBindingID) + "/" + copiedColumnBindingID}
	if got := planLines(p); !reflect.DeepEqual(got, want) {
		t.Fatalf("plan = %v, want %v", got, want)
	}

	r, err := s.client().Apply("token", p)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := planLines(p); !reflect.DeepEqual(got, want) {
		t.Errorf("Apply changed the plan's requests to %v", got)
	}
	s.expect(n,
		"POST /deliverables/"+monthlyID+`/revisions {"copy_from_revision_id":"`+revV2+`","release":"v3"}`,
		"DELETE /deliverable-revisions/"+revV4+"/sheet-bindings/"+abCopyOld,
		"PATCH "+abColumns(abCopyData)+"/"+abCopyColumn+
			` {"format_override":null,"run_param":null,"source_column":"claim_id","source_type":"column","target_column_index":3}`,
		abV4Tree)
	want = []string{`release "v3" created as a draft (copied from "v2")`, "binding writes: 1 sheet deleted, 1 column updated", ""}
	if got := FormatApplyResult(p, r); got != strings.Join(want, "\n") {
		t.Errorf("output =\n%s\nwant\n%s", got, strings.Join(want, "\n"))
	}
	if r.Manifest.Status.RevisionID.String() != revV4 || r.Manifest.Spec.Sheets["Data"].Columns["C"].Column != "claim_id" {
		t.Errorf("manifest = %+v", r.Manifest)
	}
}

func TestApplyCreatesTheDeliverableThenTheReleaseThenItsSheets(t *testing.T) {
	dir := planWorkbook(t)
	s := serveApply(t, nil)
	s.on(http.MethodPut, "/deliverable-skeletons/upload-url", planMinted)
	s.on(http.MethodPut, putTargetPath, "")
	s.on(http.MethodPost, "/deliverable-skeletons/finalize", finalizedTemplate)
	s.on(http.MethodPost, "/deliverables", writtenDeliverable)
	template := 7
	s.on(http.MethodPost, "/deliverables/"+monthlyID+"/revisions", api.DeliverableRevisionCreateResponse{ID: uuid.MustParse(revV4),
		DeliverableID: uuid.MustParse(monthlyID), Release: "v1", State: api.DeliverableRevisionStateDraft, SkeletonID: &template,
		CellBindings: &[]api.CellBindingResponse{}, SheetBindings: &[]api.SheetBindingResponse{}})
	s.on(http.MethodPost, "/deliverable-revisions/"+revV4+"/sheet-bindings", bpSheet(abNewSheet, "Data", 5, []int{1}, 2))
	amount := bpColumn(abNewColumnA, 1, "amount")
	s.on(http.MethodPost, abColumns(abNewSheet), amount)
	final := bpTree(bpSheet(abNewSheet, "Data", 5, []int{1}, 2, amount))
	final.ID, final.Release = uuid.MustParse(revV4), "v1"
	s.trees(revV4, final)
	p, n := s.prepare(planManifest(t, deliverableManifestBasics+"  release: v1\n  template: carrier-a.xlsx\n  sheets:\n"+
		bpData("columns: {A: {column: amount}}")), dir)
	if got := planLines(p)[5:]; !reflect.DeepEqual(got, []string{"POST /deliverable-revisions/<created-revision-id>/sheet-bindings",
		"POST " + abColumns(createdSheetBindingID)}) {
		t.Fatalf("plan = %v", planLines(p))
	}

	r, err := s.client().Apply("token", p)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	s.expect(n,
		`PUT /deliverable-skeletons/upload-url {"file_size":8,"original_filename":"carrier-a.xlsx"}`,
		"PUT "+putTargetPath+" <8 bytes>",
		`POST /deliverable-skeletons/finalize {"acknowledge_lossy":false,"skeleton_id":41}`,
		`POST /deliverables {"filename_pattern":"CarrierA_{period}.xlsx","name":"Carrier A bordereau"}`,
		"POST /deliverables/"+monthlyID+`/revisions {"acknowledge_lossy":false,"release":"v1","skeleton_id":7}`,
		"POST /deliverable-revisions/"+revV4+`/sheet-bindings {"data_start_row":2,"header_rows":[1],"member_dataset_id":5,"sheet_name":"Data"}`,
		"POST "+abColumns(abNewSheet)+` {"format_override":null,"run_param":null,"source_column":"amount","source_type":"column","target_column_index":1}`,
		abV4Tree)
	want := "release \"v1\" created as a draft (from scratch)\n" +
		"template attached: deliverable-template/7 (reused: an identical workbook was uploaded earlier)\n" +
		"binding writes: 1 sheet created, 1 column created\n"
	if got := FormatApplyResult(p, r); got != want {
		t.Errorf("output =\n%s\nwant\n%s", got, want)
	}
	if !r.Created || r.BindingWrites != 2 || r.Manifest.Spec.Sheets["Data"].Columns["A"].Column != "amount" {
		t.Errorf("result = %+v, manifest = %+v", r, r.Manifest)
	}
}

func TestApplyNotesTheFlagsTheCreatedReleaseHolds(t *testing.T) {
	dir := planWorkbook(t)
	s := serveApply(t, planDeliverable(nil), disabledV1, enabledV2)
	source := bpTree(bpSheet(bpDataID, "Data", 5, []int{1}, 2, bpColumn(bpColumnC, 3, "amount")))
	source.ID, source.State = uuid.MustParse(revV2), api.DeliverableRevisionStateEnabled
	s.trees(revV2, source)
	// C has no header label to re-anchor by: under the new template the copy
	// leaves it at C and flags it.
	created := bpTree(bpSheet(abCopyData, "Data", 5, []int{1}, 2, bpFlagged(bpColumn(abCopyColumn, 3, "amount"))))
	created.ID = uuid.MustParse(revV4)
	s.trees(revV4, created)
	s.on(http.MethodPut, "/deliverable-skeletons/upload-url", planMinted)
	s.on(http.MethodPut, putTargetPath, "")
	s.on(http.MethodPost, "/deliverable-skeletons/finalize", finalizedTemplate)
	s.on(http.MethodPost, "/deliverables/"+monthlyID+"/revisions", api.DeliverableRevisionCreateResponse{ID: created.ID,
		DeliverableID: uuid.MustParse(monthlyID), Release: "v3", State: api.DeliverableRevisionStateDraft, SkeletonID: created.SkeletonID,
		CellBindings: created.CellBindings,
		AnchorReport: &api.BindingAnchorReport{CellBindings: []api.CellAnchorChange{}, ColumnBindings: []api.ColumnAnchorChange{{
			ColumnBindingID: uuid.MustParse(abCopyColumn), SheetBindingID: uuid.MustParse(abCopyData), SheetName: "Data",
			FromColumnIndex: 3, ToColumnIndex: 3, Outcome: api.BindingAnchorOutcomeNeedsConfirmation}}}, SheetBindings: created.SheetBindings,
	})
	p, n := s.prepare(bpManifest(t, "  template: carrier-a.xlsx\n", bpData("columns: {C: {column: amount}}")), dir)
	if len(p.Requests) != 4 || !p.MayRemove || len(p.confirmations) != 0 {
		t.Fatalf("plan = %v, MayRemove = %v, confirmations = %q", planLines(p), p.MayRemove, p.confirmations)
	}

	r, err := s.client().Apply("token", p)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// C matches the manifest, so apply leaves it, and its flag, alone.
	s.expect(n,
		`PUT /deliverable-skeletons/upload-url {"file_size":8,"original_filename":"carrier-a.xlsx"}`,
		"PUT "+putTargetPath+" <8 bytes>",
		`POST /deliverable-skeletons/finalize {"acknowledge_lossy":false,"skeleton_id":41}`,
		"POST /deliverables/"+monthlyID+`/revisions {"acknowledge_lossy":false,"copy_from_revision_id":"`+revV2+`","release":"v3","skeleton_id":7}`,
		abV4Tree)
	want := "release \"v3\" created as a draft (copied from \"v2\")\n" +
		"template attached: deliverable-template/7 (reused: an identical workbook was uploaded earlier)\n" +
		"\nRe-anchored bindings (1):\n  Column Data!C: needs_confirmation [" + abCopyColumn + "]\n" +
		`Note: spec.template replaces the template the copy of release "v2" starts with: the server may re-anchor ` +
		"the copied column bindings, and apply plans their writes from the created release\n" +
		`Note: sheet "Data" column C needs confirmation: a template change or a sheet rename left it in place with no header label to re-anchor by; ` +
		"check: qctl describe deliverable-revision carrier-a-bordereau --release v3; " +
		"confirm: qctl deliverable confirm-columns carrier-a-bordereau --release v3 --sheet Data --column C\n"
	if got := FormatApplyResult(p, r); got != want {
		t.Errorf("output =\n%s\nwant\n%s", got, want)
	}
}

func TestApplyDeletesASheet(t *testing.T) {
	stored := bpTree(bpSheet(bpDataID, "Data", 5, []int{1}, 2), bpSheet(bpOldID, "Old", 5, []int{1}, 2))
	notFound := `{"detail":{"msg":"Revision can not be found in this organization.","error_code":"NOT_FOUND","error_id":""}}`
	for _, tc := range []struct {
		name string
		// readBack is the status of the trees read after the write.
		readBack int
	}{
		{name: "read back", readBack: http.StatusOK},
		{name: "read back fails", readBack: http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := serveApply(t, planDeliverable(nil), enabledV2, draftV3)
			reads := 0
			s.mock.RegisterHandler(http.MethodGet, bpTreePath+revV3, func(w http.ResponseWriter, _ *http.Request) {
				if reads++; reads > 1 && tc.readBack != http.StatusOK {
					respondBody(w, tc.readBack, notFound)
					return
				}
				testutil.RespondJSON(w, http.StatusOK, stored)
			})
			s.on(http.MethodDelete, bpSheets+"/"+bpOldID)
			p, n := s.prepare(bpManifest(t, "", bpData()), ".")
			r, err := s.client().Apply("token", p)
			if r == nil || r.BindingWrites != 1 || r.Manifest == nil || r.Manifest.Spec.Release != "v3" {
				t.Fatalf("result = %+v; error = %v", r, err)
			}
			if got, want := FormatApplyResult(p, r), "binding writes: 1 sheet deleted\n"; got != want {
				t.Errorf("output = %q, want %q", got, want)
			}
			if tc.readBack == http.StatusOK {
				if err != nil {
					t.Fatalf("Apply: %v", err)
				}
				s.expect(n, "DELETE "+bpSheets+"/"+bpOldID, abV3Tree)
				return
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
				t.Fatalf("exit = %d, want %d; error = %v", code, qerrors.ExitNotFound, err)
			}
			if !strings.HasPrefix(err.Error(), "deliverable/carrier-a-bordereau: every write was sent, but reading it back failed: ") {
				t.Errorf("error = %v", err)
			}
			s.expect(n, "DELETE "+bpSheets+"/"+bpOldID, abV3Tree)
		})
	}
}

func TestApplyRefusesADeleteThePlanDidNotList(t *testing.T) {
	for _, tc := range []struct {
		name      string
		mayRemove bool
	}{
		{name: "unlisted"},
		{name: "listed by MayRemove", mayRemove: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := serveApply(t, planDeliverable(nil), enabledV2, draftV3)
			s.trees(revV3, bpTree(bpSheet(bpDataID, "Data", 5, []int{1}, 2)))
			// A binding saved under the header label Policy after the plan
			// read the tree, which the re-anchoring moves from B to C.
			label := "Policy"
			late := bpColumn(bpColumnC, 3, "policy_id")
			late.TargetLabel = &label
			s.on(http.MethodPatch, bpSheets+"/"+bpDataID, api.SheetBindingPatchResponse{ID: uuid.MustParse(bpDataID), SheetName: "Data", MemberDatasetID: 5, HeaderRows: []int{2}, DataStartRow: 3,

				AnchorReport: &api.BindingAnchorReport{CellBindings: []api.CellAnchorChange{}, ColumnBindings: []api.ColumnAnchorChange{{
					ColumnBindingID: late.ID, SheetBindingID: uuid.MustParse(bpDataID), SheetName: "Data", TargetLabel: &label,
					FromColumnIndex: 2, ToColumnIndex: 3, Outcome: api.BindingAnchorOutcomeMoved}}}, ColumnBindings: &[]api.ColumnBindingResponse{late},
			})
			s.on(http.MethodDelete, bpDataColumns+"/"+bpColumnC)
			s.on(http.MethodPost, bpDataColumns, bpColumn(abNewColumnA, 1, "amount"))
			p, n := s.prepare(bpManifest(t, "", bpSheetDoc("Data", bpBase, "header_rows: [2]", "data_start_row: 3", "columns: {A: {column: amount}}")), ".")
			if p.MayRemove || p.Removals != nil {
				t.Fatalf("MayRemove = %v, Removals = %q; want neither", p.MayRemove, p.Removals)
			}
			p.MayRemove = tc.mayRemove
			r, err := s.client().Apply("token", p)
			patch := "PATCH " + bpSheets + "/" + bpDataID + ` {"data_start_row":3,"header_rows":[2],"instruction_row_count":0,` +
				`"member_dataset_id":5,"member_series_id":null,"sheet_name":"Data","sort_keys":[]}`
			post := "POST " + bpDataColumns +
				` {"format_override":null,"run_param":null,"source_column":"amount","source_type":"column","target_column_index":1}`
			if tc.mayRemove {
				if err != nil {
					t.Fatalf("Apply: %v", err)
				}
				s.expect(n, patch, "DELETE "+bpDataColumns+"/"+bpColumnC, post, abV3Tree)
				return
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitGenericError || r == nil || r.BindingWrites != 1 {
				t.Fatalf("result = %+v, exit = %d, want the partial result and %d; error = %v", r, code, qerrors.ExitGenericError, err)
			}
			want := "deliverable/carrier-a-bordereau apply stopped at step 2 of 3 (DELETE " + bpDataColumns + "/" + bpColumnC +
				` for sheet "Data" column C), 1 completed: ` +
				`refusing to delete sheet "Data" column C, which the plan did not list among the removals to confirm` + abFinish
			if err.Error() != want {
				t.Errorf("error =\n%s\nwant\n%s", err, want)
			}
			s.expect(n, patch)
		})
	}
}

func TestApplyStopsAfterTheRevisionPostWhenTheCopyCannotBePlanned(t *testing.T) {
	s := serveApply(t, planDeliverable(nil), disabledV1, enabledV2)
	source := bpTree(bpSheet(bpDataID, "Data", 5, []int{1}, 2), bpSheet(bpOldID, "Old", 5, []int{1}, 2))
	source.ID, source.State = uuid.MustParse(revV2), api.DeliverableRevisionStateEnabled
	s.trees(revV2, source)
	// The copy holds a cell binding totalling Old that the source did not.
	created := bpTree(bpSheet(abCopyData, "Data", 5, []int{1}, 2), bpSheet(abCopyOld, "Old", 5, []int{1}, 2))
	oldID := uuid.MustParse(abCopyOld)
	s.on(http.MethodPost, "/deliverables/"+monthlyID+"/revisions", api.DeliverableRevisionCreateResponse{ID: uuid.MustParse(revV4),
		DeliverableID: uuid.MustParse(monthlyID), Release: "v3", State: api.DeliverableRevisionStateDraft, SkeletonID: created.SkeletonID,

		CellBindings: &[]api.CellBindingResponse{{SheetName: "Summary", CellRef: "B2", SameRowsAsSheetBindingID: &oldID}}, SheetBindings: created.SheetBindings,
	})
	p, n := s.prepare(bpManifest(t, "", bpData()), ".")
	r, err := s.client().Apply("token", p)
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage || r == nil || r.Revision == nil {
		t.Fatalf("result = %+v, exit = %d, want the created release and %d; error = %v", r, code, qerrors.ExitBadUsage, err)
	}
	want := "deliverable/carrier-a-bordereau apply stopped after step 1 of 2 (POST /deliverables/" + monthlyID + "/revisions), 1 completed: " +
		`spec.sheets has no sheet "Old", but cell bindings Summary!B2 total its rows`
	if !strings.HasPrefix(err.Error(), want) || !strings.HasSuffix(err.Error(), abFinish) {
		t.Errorf("error =\n%s\nwant it to start\n%s", err, want)
	}
	s.expect(n, "POST /deliverables/"+monthlyID+`/revisions {"copy_from_revision_id":"`+revV2+`","release":"v3"}`)
	if r.Manifest == nil || r.Manifest.Status.RevisionID.String() != revV4 {
		t.Errorf("manifest = %+v, want the created release", r.Manifest)
	}
}

func TestConfirmationNotesComeFromTheFinalTreeOnce(t *testing.T) {
	p := &Plan{Notes: []string{"plan note"}, confirmations: []string{"flag the plan read"}}
	var diff strings.Builder
	if err := p.WriteRequests(&diff); err != nil {
		t.Fatal(err)
	}
	if got, want := diff.String(), "Note: plan note\nNote: flag the plan read\n"; got != want {
		t.Errorf("diff = %q, want %q", got, want)
	}
	for _, tc := range []struct {
		name string
		r    *ApplyResult
		want string
	}{
		{name: "a result Apply did not return", r: &ApplyResult{}, want: "Note: plan note\nNote: flag the plan read\n"},
		{name: "the release read back", r: &ApplyResult{applied: true, confirmations: []string{"flag left"}},
			want: "Note: plan note\nNote: flag left\n"},
		{name: "nothing flagged after the writes", r: &ApplyResult{applied: true}, want: "Note: plan note\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := FormatApplyResult(p, tc.r); got != tc.want {
				t.Errorf("output = %q, want %q", got, tc.want)
			}
		})
	}
}
