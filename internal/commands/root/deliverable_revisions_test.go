package root

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const enabledRevisionID = "e1e1e1e1-2222-4333-8444-777777777777"
const draftRevisionID = "d2d2d2d2-2222-4333-8444-888888888888"
const revisionTestPath = "/api/orgs/" + scopeTestOrgID + "/deliverable-revisions/"

const revisionTreeRecord = `{
 "id":"e1e1e1e1-2222-4333-8444-777777777777",
 "deliverable_id":"c0ffee01-2222-4333-8444-555555555555",
 "release":"2026.1","state":"enabled","description":"First release",
 "enabled_by_id":"550e8400-e29b-41d4-a716-446655440000","enabled_at":"2026-09-01T12:00:00Z",
 "skeleton_id":7,"skeleton_lossy_acknowledged":true,
 "workbook_inventory":{"has_vba":false,"sheets":[
  {"name":"Zeta","max_row":50,"max_column":27,"state":"hidden","is_empty":false,
   "used_max_row":4,"used_max_column":27,"header_rows":{"2":[null,"Header"],"1":["Title",null]},"merged_ranges":["A1:B1"]},
  {"name":"Alpha","max_row":3,"max_column":2,"state":"visible","is_empty":false,
   "used_max_row":3,"used_max_column":2,"header_rows":{"1":["Premium",null]},"merged_ranges":[]}
 ]},
 "fidelity_report":{"version":2,"is_lossy":true,"lost_parts":["not a zip path"],
  "losses":[{"kind":"image","sheet":"Alpha","count":1,"label":"1 image on Alpha","parts":["xl/media/image1.png"]}],
  "benign_parts":["xl/styles.xml"],"notes":["Renderer note"]},
 "sheet_bindings":[
  {"id":"10000000-0000-4000-8000-000000000001","sheet_name":"Zeta","member_dataset_id":9007199254740993,
   "member_series_id":null,"data_start_row":4,"header_rows":[1,2],"instruction_row_count":1,"row_filter":null,"sort_keys":[],"column_bindings":[]},
  {"id":"10000000-0000-4000-8000-000000000002","sheet_name":"Alpha","member_dataset_id":42,
   "member_series_id":"11111111-2222-4333-8444-555555555555","data_start_row":3,"header_rows":[1],"instruction_row_count":1,
   "row_filter":{"description":"Positive premiums","conditions":[{"column_name":"premium","operator":"gt","value":"9007199254740993","value_high":null,"value_list":null}],"predicate":null},
   "sort_keys":[{"position":2,"column_name":"premium","descending":true},{"position":1,"column_name":"policy","descending":false}],
   "column_bindings":[
    {"id":"20000000-0000-4000-8000-000000000001","target_column_index":27,"source_type":"run_param","source_column":null,"run_param":"period","format_override":null,"origin":"user","target_label":"Period","needs_confirmation":true,"warnings":[]},
    {"id":"20000000-0000-4000-8000-000000000002","target_column_index":2,"source_type":"column","source_column":"premium","run_param":null,"format_override":"0.00","origin":"suggested","target_label":"Premium","needs_confirmation":false,"warnings":[]}
   ]}
 ],
 "cell_bindings":[
  {"id":"30000000-0000-4000-8000-000000000001","sheet_name":"Zeta","cell_ref":"A1","source_type":"literal","literal_value":"Report title","row_filter":null,"needs_confirmation":false},
  {"id":"30000000-0000-4000-8000-000000000002","sheet_name":"Alpha","cell_ref":"B1","source_type":"aggregate","aggregate_func":"sum","aggregate_column":"premium","aggregate_member_dataset_id":42,"aggregate_member_series_id":null,"same_rows_as_sheet_binding_id":"10000000-0000-4000-8000-000000000002","row_filter":{"description":"Total premiums","conditions":[]},"needs_confirmation":true,"label_cell_ref":"A1","target_label":"Total","format_override":"0.00"},
  {"id":"30000000-0000-4000-8000-000000000003","sheet_name":"Alpha","cell_ref":"A1","source_type":"run_param","run_param":"period","row_filter":null,"needs_confirmation":false}
 ]
}`

const templateRowsRecord = `{"sheet_name":"Premium & Claims","rows":[["Title",null,""],[null,"Policy\tID","Premium\namount"],["P-1","9","42"]],"merged_ranges":["A1:C1"],"max_row":50,"max_column":3}`

func resetRevisionFlags(t *testing.T) {
	t.Helper()
	cmd, _, err := GetRootCmd().Find([]string{"describe", "deliverable-revision"})
	if err != nil {
		t.Fatal(err)
	}
	reset := func() {
		for _, name := range []string{"release", "revision", "sheet", "limit"} {
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

func revisionTestServer(t *testing.T) *deliverableTestServer {
	t.Helper()
	resetRevisionFlags(t)
	s := newDeliverableTestServer(t)
	for _, id := range []string{enabledRevisionID, draftRevisionID} {
		body := revisionTreeRecord
		if id == draftRevisionID {
			body = strings.ReplaceAll(body, enabledRevisionID, draftRevisionID)
			body = strings.ReplaceAll(body, `"release":"2026.1","state":"enabled"`, `"release":"2026.2","state":"draft"`)
		}
		s.RegisterHandler("GET", revisionTestPath+id, func(w http.ResponseWriter, r *http.Request) {
			checkDeliverableToken(t, r)
			testutil.RespondJSON(w, http.StatusOK, json.RawMessage(body))
		})
		s.RegisterHandler("GET", revisionTestPath+id+"/skeleton/sheets/Premium & Claims/rows", func(w http.ResponseWriter, r *http.Request) {
			checkDeliverableToken(t, r)
			if limit := r.URL.Query().Get("limit"); limit != "10" && limit != "30" && limit != "100" {
				t.Errorf("limit = %q", limit)
			}
			testutil.RespondJSON(w, http.StatusOK, json.RawMessage(templateRowsRecord))
		})
	}
	return s
}

func TestGetDeliverableRevisionsFormats(t *testing.T) {
	for _, format := range []string{"table", "json", "yaml", "name"} {
		t.Run(format, func(t *testing.T) {
			s := revisionTestServer(t)
			out, err := runRoot(t, "get", "deliverable-revisions", "deliverable/"+liveDeliverableID, "-o", format)
			if err != nil {
				t.Fatal(err)
			}
			if got, want := s.Requests(), []string{deliverableRequest(liveDeliverableID, "revisions")}; !reflect.DeepEqual(got, want) {
				t.Fatalf("requests = %v, want %v", got, want)
			}
			switch format {
			case "json":
				assertSeriesJSON(t, out, liveDeliverableRevisions)
			case "yaml":
				testutil.AssertYAMLReadsAsJSON(t, out, liveDeliverableRevisions)
			case "name":
				if want := "deliverable-revision/" + draftRevisionID + "\ndeliverable-revision/" + enabledRevisionID + "\n"; out != want {
					t.Errorf("out = %q, want %q", out, want)
				}
			default:
				for _, want := range []string{"RELEASE", "STATE", "TEMPLATE", "2026.2", "draft", "2026.1", "enabled"} {
					if !strings.Contains(out, want) {
						t.Errorf("missing %q: %s", want, out)
					}
				}
			}
		})
	}
}

func TestDescribeDeliverableRevisionFormats(t *testing.T) {
	for _, format := range []string{"table", "json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			s := revisionTestServer(t)
			out, err := runRoot(t, "describe", "deliverable-revision", liveDeliverableID, "-o", format)
			if err != nil {
				t.Fatal(err)
			}
			want := []string{deliverableRequest(liveDeliverableID, "revisions"), "GET " + revisionTestPath + enabledRevisionID}
			if got := s.Requests(); !reflect.DeepEqual(got, want) {
				t.Errorf("requests = %v, want %v", got, want)
			}
			switch format {
			case "json":
				assertSeriesJSON(t, out, revisionTreeRecord)
			case "yaml":
				testutil.AssertYAMLReadsAsJSON(t, out, revisionTreeRecord)
			default:
				for _, want := range []string{"Release:", "2026.1", "Lossy acknowledged:", "true", "Enabled by:", "550e8400-e29b-41d4-a716-446655440000", "9007199254740993", "Row filter:\n      description: Positive premiums\n", "  - premium gt \"9007199254740993\"\n", "description: Total premiums\n", "origin: suggested", "needs confirmation: true", "same rows as sheet binding: 10000000-0000-4000-8000-000000000002", "1 image on Alpha", "Renderer note", "A1:B1", "Header row 1: Title | "} {
					if !strings.Contains(out, want) {
						t.Errorf("missing %q: %s", want, out)
					}
				}
				for _, pair := range [][2]string{{"Alpha [", "Zeta ["}, {"Column 2 (B)", "Column 27 (AA)"}, {"Sort 1:", "Sort 2:"}, {"Alpha!A1", "Alpha!B1"}, {"Alpha!B1", "Zeta!A1"}, {"Header row 1:", "Header row 2:"}} {
					if a, b := strings.Index(out, pair[0]), strings.Index(out, pair[1]); a < 0 || b <= a {
						t.Errorf("%q must precede %q: %s", pair[0], pair[1], out)
					}
				}
				if strings.Contains(out, "xl/styles.xml") || strings.Contains(out, "not a zip path") {
					t.Errorf("unexpected fidelity fallback: %s", out)
				}
			}
		})
	}
}

func TestDeliverableRevisionSelectors(t *testing.T) {
	for _, tc := range []struct {
		name  string
		flags []string
		id    string
		code  int
	}{
		{"release", []string{"--release", "2026.2"}, draftRevisionID, 0},
		{"uuid as release", []string{"--release", draftRevisionID}, draftRevisionID, 0},
		{"uuid", []string{"--revision", draftRevisionID}, draftRevisionID, 0},
		{"name ref", []string{"--revision", "deliverable-revision/" + draftRevisionID}, draftRevisionID, 0},
		{"missing release", []string{"--release", "missing"}, "", qerrors.ExitNotFound},
		{"wrong parent", []string{"--revision", seriesTestID}, "", qerrors.ExitNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := revisionTestServer(t)
			args := append([]string{"describe", "deliverable-revision", "monthly-bordereau", "-o", "json"}, tc.flags...)
			out, err := runRoot(t, args...)
			if code := qerrors.GetExitCode(err); code != tc.code {
				t.Fatalf("code = %d, want %d; %v", code, tc.code, err)
			}
			want := []string{deliverableRequest(), deliverableRequest(liveDeliverableID, "revisions")}
			if tc.code == 0 {
				want = append(want, "GET "+revisionTestPath+tc.id)
				if !strings.Contains(out, tc.id) {
					t.Errorf("missing revision %s: %s", tc.id, out)
				}
			} else if out != "" {
				t.Errorf("out = %q", out)
			}
			if got := s.Requests(); !reflect.DeepEqual(got, want) {
				t.Errorf("requests = %v, want %v", got, want)
			}
		})
	}
}

func TestDeliverableTemplateRowsFormats(t *testing.T) {
	for _, format := range []string{"table", "json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			s := revisionTestServer(t)
			out, err := runRoot(t, "describe", "deliverable-revision", liveDeliverableID, "--sheet", "Premium & Claims", "--limit", "30", "-o", format)
			if err != nil {
				t.Fatal(err)
			}
			want := []string{deliverableRequest(liveDeliverableID, "revisions"), "GET " + revisionTestPath + enabledRevisionID + "/skeleton/sheets/Premium & Claims/rows"}
			if got := s.Requests(); !reflect.DeepEqual(got, want) {
				t.Errorf("requests = %v, want %v", got, want)
			}
			switch format {
			case "json":
				assertSeriesJSON(t, out, templateRowsRecord)
			case "yaml":
				testutil.AssertYAMLReadsAsJSON(t, out, templateRowsRecord)
			default:
				for _, want := range []string{"Premium & Claims", "50 rows x 3 columns", "A1:C1", "ROW", "Title", `Policy\tID`, `Premium\namount`, "P-1"} {
					if !strings.Contains(out, want) {
						t.Errorf("missing %q: %s", want, out)
					}
				}
				for _, line := range strings.Split(out, "\n") {
					if strings.HasPrefix(line, "ROW") && strings.Join(strings.Fields(line), ",") != "ROW,A,B,C" {
						t.Errorf("grid header = %q", line)
					}
				}
			}
		})
	}
}

func TestDeliverableRevisionValidationBeforeBootstrap(t *testing.T) {
	for _, flags := range [][]string{{"--release", ""}, {"--revision", ""}, {"--sheet", ""}, {"--release", "v1", "--revision", draftRevisionID}, {"--revision", "d2d2d2d2"}, {"--limit", "0", "--sheet", "Premium"}, {"--limit", "101", "--sheet", "Premium"}, {"--limit", "10"}, {"-o", "name"}} {
		t.Run(strings.Join(flags, " "), func(t *testing.T) {
			resetRevisionFlags(t)
			err := runOutsideGetWithoutContext(t, append([]string{"describe", "deliverable-revision", liveDeliverableID}, flags...)...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("code = %d, want 2; %v", code, err)
			}
		})
	}
	for _, args := range [][]string{{"get", "deliverable-revisions"}, {"get", "deliverable-revisions", ""}, {"get", "deliverable-revisions", "table/42"}, {"describe", "deliverable-revision", " "}, {"describe", "deliverable-revision", "deliverable-revision/" + enabledRevisionID}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			resetRevisionFlags(t)
			if err := runOutsideGetWithoutContext(t, args...); qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
				t.Fatalf("want exit 2: %v", err)
			}
		})
	}
}

func TestDeliverableRevisionHTTPFailures(t *testing.T) {
	for _, tc := range []struct {
		name         string
		status, code int
		body, sub    string
	}{
		{"permission", 403, qerrors.ExitUnauthorized, `{"detail":{"msg":"No deliverable access"}}`, ""},
		{"not found", 404, qerrors.ExitNotFound, `{"detail":{"msg":"Revision gone"}}`, ""},
		{"no template", 409, qerrors.ExitBadUsage, `{"detail":{"msg":"No template attached"}}`, "/skeleton/sheets/Premium & Claims/rows"},
		{"unknown sheet", 404, qerrors.ExitNotFound, `{"detail":{"msg":"Unknown sheet","error_code":"SHEET_NOT_FOUND"}}`, "/skeleton/sheets/Premium & Claims/rows"},
		{"object 422", 422, qerrors.ExitBadUsage, `{"detail":{"msg":"Invalid template","error_code":"SKELETON_INVALID","context":{}}}`, "/skeleton/sheets/Premium & Claims/rows"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := revisionTestServer(t)
			s.RegisterHandler("GET", revisionTestPath+enabledRevisionID+tc.sub, func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, tc.status, json.RawMessage(tc.body))
			})
			args := []string{"describe", "deliverable-revision", liveDeliverableID, "-o", "json"}
			if tc.sub != "" {
				args = append(args, "--sheet", "Premium & Claims")
			}
			out, err := runRoot(t, args...)
			if code := qerrors.GetExitCode(err); code != tc.code {
				t.Fatalf("exit = %d, want %d; %v", code, tc.code, err)
			}
			if out != "" {
				t.Errorf("out = %q", out)
			}
			want := []string{deliverableRequest(liveDeliverableID, "revisions"), "GET " + revisionTestPath + enabledRevisionID + tc.sub}
			if got := s.Requests(); !reflect.DeepEqual(got, want) {
				t.Errorf("requests = %v, want %v", got, want)
			}
		})
	}
}

func TestDeliverableRevisionDefaultAndExactRelease(t *testing.T) {
	for _, tc := range []struct {
		name, list, release, id string
		code                    int
	}{
		{name: "sole draft", list: `[{"id":"` + draftRevisionID + `","release":"VersionOne","state":"draft"}]`, id: draftRevisionID},
		{name: "sole disabled", list: `[{"id":"` + draftRevisionID + `","release":"VersionOne","state":"disabled"}]`, id: draftRevisionID},
		{name: "multiple drafts", list: strings.ReplaceAll(liveDeliverableRevisions, `"state":"enabled"`, `"state":"draft"`), code: qerrors.ExitBadUsage},
		{name: "no revisions", list: `[]`, code: qerrors.ExitNotFound},
		{name: "exact label", list: `[{"id":"` + draftRevisionID + `","release":"VersionOne","state":"draft"}]`, release: "VersionOne", id: draftRevisionID},
		{name: "wrong case", list: `[{"id":"` + draftRevisionID + `","release":"VersionOne","state":"draft"}]`, release: "versionone", code: qerrors.ExitNotFound},
		{name: "UUID label wins", list: `[{"id":"` + draftRevisionID + `","release":"` + enabledRevisionID + `","state":"draft"},{"id":"` + enabledRevisionID + `","release":"v1","state":"enabled"}]`, release: enabledRevisionID, id: draftRevisionID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := revisionTestServer(t)
			s.RegisterHandler("GET", deliverableTestPath+"/"+liveDeliverableID+"/revisions", func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, json.RawMessage(tc.list))
			})
			args := []string{"describe", "deliverable-revision", liveDeliverableID, "-o", "json"}
			if tc.release != "" {
				args = append(args, "--release", tc.release)
			}
			out, err := runRoot(t, args...)
			if code := qerrors.GetExitCode(err); code != tc.code {
				t.Fatalf("exit = %d, want %d; %v", code, tc.code, err)
			}
			want := []string{deliverableRequest(liveDeliverableID, "revisions")}
			if tc.id != "" {
				want = append(want, "GET "+revisionTestPath+tc.id)
				if !strings.Contains(out, tc.id) {
					t.Errorf("out = %q", out)
				}
			} else if out != "" {
				t.Errorf("out = %q", out)
			}
			if got := s.Requests(); !reflect.DeepEqual(got, want) {
				t.Errorf("requests = %v, want %v", got, want)
			}
		})
	}
}

func TestDeliverableRevisionLegacyFidelity(t *testing.T) {
	s := revisionTestServer(t)
	var body map[string]json.RawMessage
	if err := json.Unmarshal([]byte(revisionTreeRecord), &body); err != nil {
		t.Fatal(err)
	}
	body["fidelity_report"] = json.RawMessage(`{"version":1,"is_lossy":true,"losses":[],"lost_parts":["xl/media/image1.png"],"notes":["Environment warning"]}`)
	body["description"] = json.RawMessage(`"Title\u001b[31m\nForged\tlabel"`)
	s.RegisterHandler("GET", revisionTestPath+enabledRevisionID, func(w http.ResponseWriter, _ *http.Request) { testutil.RespondJSON(w, http.StatusOK, body) })
	out, err := runRoot(t, "describe", "deliverable-revision", liveDeliverableID)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Lost part: xl/media/image1.png", "Note: Environment warning", `Title\x1b[31m\nForged\tlabel`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q: %s", want, out)
		}
	}
	if strings.Contains(out, "\x1b") {
		t.Errorf("terminal control survived: %q", out)
	}
}

func TestGetDeliverableRevisionsEmpty(t *testing.T) {
	for _, format := range []string{"table", "json", "yaml", "name"} {
		t.Run(format, func(t *testing.T) {
			s := revisionTestServer(t)
			out, err := runRoot(t, "get", "deliverable-revisions", trashedDeliverableID, "-o", format)
			if err != nil {
				t.Fatal(err)
			}
			if got := s.Requests(); !reflect.DeepEqual(got, []string{deliverableRequest(trashedDeliverableID, "revisions")}) {
				t.Errorf("requests = %v", got)
			}
			switch format {
			case "json":
				assertSeriesJSON(t, out, `[]`)
			case "yaml":
				testutil.AssertYAMLReadsAsJSON(t, out, `[]`)
			default:
				if out != "" {
					t.Errorf("out = %q", out)
				}
			}
		})
	}
}

func TestDeliverableTemplateRowsLimits(t *testing.T) {
	for _, tc := range []struct{ name, limit string }{{"default", "10"}, {"maximum", "100"}} {
		t.Run(tc.name, func(t *testing.T) {
			s := revisionTestServer(t)
			s.RegisterHandler("GET", revisionTestPath+enabledRevisionID+"/skeleton/sheets/Premium & Claims/rows", func(w http.ResponseWriter, r *http.Request) {
				if got := r.URL.Query().Get("limit"); got != tc.limit {
					t.Errorf("limit = %q, want %s", got, tc.limit)
				}
				testutil.RespondJSON(w, http.StatusOK, json.RawMessage(templateRowsRecord))
			})
			args := []string{"describe", "deliverable-revision", liveDeliverableID, "--sheet", "Premium & Claims", "-o", "json"}
			if tc.name != "default" {
				args = append(args, "--limit", tc.limit)
			}
			if _, err := runRoot(t, args...); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDeliverableRevisionDetailChecksParent(t *testing.T) {
	s := revisionTestServer(t)
	body := strings.ReplaceAll(revisionTreeRecord, liveDeliverableID, trashedDeliverableID)
	s.RegisterHandler("GET", revisionTestPath+enabledRevisionID, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(body))
	})
	out, err := runRoot(t, "describe", "deliverable-revision", liveDeliverableID, "-o", "json")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
		t.Fatalf("exit = %d, want 3; %v", code, err)
	}
	if out != "" || !strings.Contains(err.Error(), "Scoped to organization") {
		t.Errorf("out = %q, err = %v", out, err)
	}
}
