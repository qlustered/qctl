package root

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const (
	suggestionsSheetID = "10000000-0000-4000-8000-000000000002"
	suggestionsPath    = "/api/orgs/" + scopeTestOrgID + "/deliverable-sheet-bindings/" + suggestionsSheetID + "/column-bindings/apply-suggestions"
)

const columnSuggestionsRecord = `{
 "created_count":2,"replaced_count":3,
 "column_bindings":[
  {"id":"20000000-0000-4000-8000-000000000003","target_column_index":27,"source_type":"run_param",
   "source_column":null,"run_param":"period","format_override":null,"origin":"user","target_label":"Period",
   "needs_confirmation":true,"match_reason":null,"warnings":[]},
  {"id":"20000000-0000-4000-8000-000000000001","target_column_index":2,"source_type":"column",
   "source_column":"premium","run_param":null,"format_override":"0.00","origin":"suggested","target_label":"Premium",
   "needs_confirmation":false,"match_reason":"same_name","warnings":[{"loc":["column","B"],"msg":"Formula overwritten","type":"formula"}]},
  {"id":"20000000-0000-4000-8000-000000000002","target_column_index":3,"source_type":"column",
   "source_column":"policy_number","run_param":null,"format_override":null,"origin":"suggested","target_label":"Policy",
   "needs_confirmation":false,"match_reason":"standard_field","warnings":[]}
 ],
 "empty_columns":[
  {"target_column_index":6,"header_label":"No match","candidate":null},
  {"target_column_index":4,"header_label":"Premiums","candidate":{"source_column":"net_premium","match_reason":"similar_name","score":87.5}},
  {"target_column_index":5,"header_label":"Previously removed","candidate":{"source_column":"broker","match_reason":"same_name","score":100}}
 ],"future":9007199254740993
}`

func resetColumnSuggestionsFlags(t *testing.T) {
	t.Helper()
	cmd, _, err := GetRootCmd().Find([]string{"deliverable", "accept-column-suggestions"})
	if err != nil {
		t.Fatal(err)
	}
	reset := func() {
		for _, name := range []string{"release", "revision", "sheet"} {
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

func columnSuggestionsServer(t *testing.T, status int, body string) *deliverableTestServer {
	t.Helper()
	resetColumnSuggestionsFlags(t)
	s := revisionTestServer(t)
	s.RegisterHandler("POST", suggestionsPath, func(w http.ResponseWriter, r *http.Request) {
		checkDeliverableToken(t, r)
		if r.Body != nil {
			if got := readBody(t, r); got != "" {
				t.Errorf("suggestions body = %q, want no body", got)
			}
		}
		testutil.RespondJSON(w, status, json.RawMessage(body))
	})
	return s
}

func runColumnSuggestions(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := GetRootCmd()
	var out, errOut bytes.Buffer
	cmd.SetArgs(append([]string{"deliverable", "accept-column-suggestions"}, args...))
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

func TestDeliverableColumnSuggestionsFormats(t *testing.T) {
	for _, format := range []string{"table", "json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			s := columnSuggestionsServer(t, http.StatusOK, columnSuggestionsRecord)
			out, errOut, err := runColumnSuggestions(t, "monthly-bordereau", "--sheet", "Alpha", "-o", format)
			if err != nil {
				t.Fatal(err)
			}
			wantRequests := []string{
				deliverableRequest(), deliverableRequest(liveDeliverableID, "revisions"),
				"GET " + revisionTestPath + draftRevisionID, "POST " + suggestionsPath,
			}
			if got := s.Requests(); !reflect.DeepEqual(got, wantRequests) {
				t.Errorf("requests = %v, want %v", got, wantRequests)
			}
			if got := s.listQueries[0].Get("include_archived"); got != "true" {
				t.Errorf("include_archived = %q", got)
			}
			report := errOut
			switch format {
			case "json":
				assertSeriesJSON(t, out, columnSuggestionsRecord)
			case "yaml":
				testutil.AssertYAMLReadsAsJSON(t, out, columnSuggestionsRecord)
			default:
				report = out
				if errOut != "" {
					t.Errorf("stderr = %q", errOut)
				}
			}
			for _, want := range []string{
				`release "2026.2" sheet "Alpha"`, "Created: 2", "Replaced: 3 (removed suggested bindings)",
				"Current column bindings (3):", "Column 2 (B) <- column premium", "origin: suggested; match: same_name",
				"origin: suggested; match: standard_field", "Column 27 (AA) <- run parameter period", "origin: user; match: -; needs confirmation: true",
				"Warning: column.B: Formula overwritten", "Empty columns (3):", "  Column 6 (F): No match\n",
				"hint: net_premium (similar_name, score 87.5; not applied)", "hint: broker (same_name, score 100; not applied)",
			} {
				if !strings.Contains(report, want) {
					t.Errorf("report lacks %q:\n%s", want, report)
				}
			}
			for _, pair := range [][2]string{{"Column 2 (B)", "Column 27 (AA)"}, {"Column 4 (D)", "Column 6 (F)"}} {
				if a, b := strings.Index(report, pair[0]), strings.Index(report, pair[1]); a < 0 || b <= a {
					t.Errorf("%q must precede %q:\n%s", pair[0], pair[1], report)
				}
			}
		})
	}
}

func TestDeliverableColumnSuggestionsSelectors(t *testing.T) {
	for _, tc := range []struct {
		name, ref string
		flags     []string
	}{
		{"sole draft", liveDeliverableID, nil},
		{"release", "Monthly bordereau", []string{"--release", "2026.2"}},
		{"uuid release", liveDeliverableID, []string{"--release", draftRevisionID}},
		{"revision ref", "deliverable/" + liveDeliverableID, []string{"--revision", "deliverable-revision/" + draftRevisionID}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := columnSuggestionsServer(t, http.StatusOK, columnSuggestionsRecord)
			_, _, err := runColumnSuggestions(t, append([]string{tc.ref, "--sheet", "Alpha"}, tc.flags...)...)
			if err != nil {
				t.Fatal(err)
			}
			requests := s.Requests()
			if n := len(requests); n < 3 || requests[n-2] != "GET "+revisionTestPath+draftRevisionID || requests[n-1] != "POST "+suggestionsPath {
				t.Errorf("requests = %v", requests)
			}
		})
	}
}

func TestDeliverableColumnSuggestionsLocalValidation(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{""},
		{"one", "two"},
		{liveDeliverableID},
		{liveDeliverableID, "--sheet", ""},
		{liveDeliverableID, "--sheet", "Alpha", "--release", ""},
		{liveDeliverableID, "--sheet", "Alpha", "--revision", ""},
		{liveDeliverableID, "--sheet", "Alpha", "--revision", "bad"},
		{liveDeliverableID, "--sheet", "Alpha", "--release", "2026.2", "--revision", draftRevisionID},
		{liveDeliverableID, "--sheet", "Alpha", "-o", "name"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			testutil.NewTestEnv(t)
			resetColumnSuggestionsFlags(t)
			_, _, err := runColumnSuggestions(t, args...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("exit = %d, want 2; error = %v", code, err)
			}
		})
	}
}

func TestDeliverableColumnSuggestionsRefusesWrongTargets(t *testing.T) {
	for _, tc := range []struct {
		name, revisions, detail, sheet string
		flags                          []string
		code                           int
		message                        string
	}{
		{name: "enabled", flags: []string{"--release", "2026.1"}, code: 2, message: "only be accepted on a draft"},
		{
			name: "disabled", revisions: strings.ReplaceAll(liveDeliverableRevisions, `"state":"draft"`, `"state":"disabled"`),
			code: 2, message: "has no draft revision",
		},
		{
			name: "multiple drafts", revisions: strings.ReplaceAll(liveDeliverableRevisions, `"state":"enabled"`, `"state":"draft"`),
			code: 2, message: "has 2 draft revisions",
		},
		{name: "no revisions", revisions: "[]", code: 3, message: "has no revisions"},
		{name: "missing release", flags: []string{"--release", "missing"}, code: 3, message: "no revision matches"},
		{name: "wrong revision", flags: []string{"--revision", seriesTestID}, code: 3, message: "no revision matches"},
		{name: "missing sheet", sheet: "missing", code: 3, message: "no sheet binding matches"},
		{name: "sheet names are exact", sheet: "alpha", code: 3, message: "no sheet binding matches"},
		{name: "sheet UUID is not a name", sheet: suggestionsSheetID, code: 3, message: "no sheet binding matches"},
		{
			name: "parent changed", detail: strings.ReplaceAll(revisionTreeRecord, liveDeliverableID, trashedDeliverableID),
			code: 3, message: "does not belong",
		},
		{name: "enabled after listing", detail: revisionTreeRecord, code: 2, message: "only be accepted on a draft"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := columnSuggestionsServer(t, http.StatusOK, columnSuggestionsRecord)
			if tc.revisions != "" {
				s.RegisterHandler("GET", deliverableTestPath+"/"+liveDeliverableID+"/revisions", func(w http.ResponseWriter, r *http.Request) {
					testutil.RespondJSON(w, http.StatusOK, json.RawMessage(tc.revisions))
				})
			}
			if tc.detail != "" {
				s.RegisterHandler("GET", revisionTestPath+draftRevisionID, func(w http.ResponseWriter, r *http.Request) {
					testutil.RespondJSON(w, http.StatusOK, json.RawMessage(tc.detail))
				})
			}
			sheet := tc.sheet
			if sheet == "" {
				sheet = "Alpha"
			}
			_, _, err := runColumnSuggestions(t, append([]string{liveDeliverableID, "--sheet", sheet}, tc.flags...)...)
			if code := qerrors.GetExitCode(err); code != tc.code {
				t.Fatalf("exit = %d, want %d; error = %v", code, tc.code, err)
			}
			if !strings.Contains(err.Error(), tc.message) {
				t.Errorf("error = %v, want %q", err, tc.message)
			}
			for _, request := range s.Requests() {
				if strings.HasPrefix(request, "POST ") {
					t.Errorf("refused target sent a mutation: %s", request)
				}
			}
		})
	}
}

func TestDeliverableColumnSuggestionsBackendErrors(t *testing.T) {
	for _, tc := range []struct {
		status, code int
		body         string
	}{
		{404, 3, `{"detail":{"msg":"Sheet is missing","error_code":"SHEET_NOT_FOUND"}}`},
		{409, 2, `{"detail":{"msg":"No template attached"}}`},
		{409, 2, `{"detail":{"msg":"Revision is no longer draft"}}`},
		{422, 2, `{"detail":{"msg":"Invalid binding","error_code":"BINDING_INVALID"}}`},
		{403, 4, `{"detail":"Forbidden"}`},
		{500, 1, `{"detail":"Internal error"}`},
		{502, 1, `{"detail":"Bad gateway"}`},
		{429, 1, `{"detail":"Throttled"}`},
		{200, 1, `null`},
	} {
		t.Run(fmt.Sprintf("%d %s", tc.status, tc.body), func(t *testing.T) {
			s := columnSuggestionsServer(t, tc.status, tc.body)
			_, _, err := runColumnSuggestions(t, liveDeliverableID, "--sheet", "Alpha", "--retries", "2")
			t.Cleanup(func() { resetPersistentFlag(t, GetRootCmd(), "retries", "0") })
			if code := qerrors.GetExitCode(err); code != tc.code {
				t.Errorf("exit = %d, want %d; error = %v", code, tc.code, err)
			}
			posts := 0
			for _, request := range s.Requests() {
				if request == "POST "+suggestionsPath {
					posts++
				}
			}
			if posts != 1 {
				t.Errorf("suggestions requests = %d, want one", posts)
			}
		})
	}
}

func TestDeliverableColumnSuggestionsRepeatedCallUsesServerCounts(t *testing.T) {
	s := columnSuggestionsServer(t, http.StatusOK, columnSuggestionsRecord)
	if _, _, err := runColumnSuggestions(t, liveDeliverableID, "--sheet", "Alpha"); err != nil {
		t.Fatal(err)
	}
	unchanged := strings.ReplaceAll(columnSuggestionsRecord, `"created_count":2,"replaced_count":3`, `"created_count":0,"replaced_count":0`)
	s.RegisterHandler("POST", suggestionsPath, func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(unchanged))
	})
	out, _, err := runColumnSuggestions(t, liveDeliverableID, "--sheet", "Alpha")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Created: 0", "Replaced: 0", "Current column bindings (3):", "Empty columns (3):", "not applied"} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
	if got := len(s.Requests()); got != 6 {
		t.Errorf("requests = %v, want three per explicit invocation", s.Requests())
	}
}

func TestDeliverableColumnSuggestionsWithNoMappingsOrHints(t *testing.T) {
	columnSuggestionsServer(t, http.StatusOK, `{"created_count":0,"replaced_count":1,"column_bindings":[],"empty_columns":[]}`)
	out, _, err := runColumnSuggestions(t, liveDeliverableID, "--sheet", "Alpha")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Created: 0", "Replaced: 1 (removed suggested bindings)", "Current column bindings (0):", "Empty columns (0):"} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
}
