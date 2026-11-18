package root

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const deliverablePreviewRecord = `{"sheets":[
 {"sheet_binding_id":"10000000-0000-4000-8000-000000000002","sheet_name":"Alpha","member_dataset_id":42,
 "columns":["premium","policy","$run.period","$run.period"],
 "rows":[{"policy":"P-2","premium":9007199254740993,"$run.period":"2026-09"},{"policy":"P-1","premium":"$12.00","$run.period":"2026-09"}],
 "in_scope_count":120,"indeterminate_count":3,"out_of_scope_count":7},
 {"sheet_binding_id":"10000000-0000-4000-8000-000000000001","sheet_name":"Empty","member_dataset_id":43,
 "columns":[],"rows":[],"in_scope_count":0,"indeterminate_count":0,"out_of_scope_count":9}],
 "cells":[{"sheet_name":"Alpha","cell_ref":"A1","value":"2026-09"},{"sheet_name":"Alpha","cell_ref":"B1","value":18446744073709551615},
 {"sheet_name":"Empty","cell_ref":"A1","value":null}],"future":true}`

func resetPreviewFlags(t *testing.T) {
	t.Helper()
	cmd, _, err := GetRootCmd().Find([]string{"inspect", "deliverable"})
	if err != nil {
		t.Fatal(err)
	}
	reset := func() {
		for _, name := range []string{"release", "revision", "period", "params-file", "max-rows"} {
			f := cmd.Flags().Lookup(name)
			if err := f.Value.Set(f.DefValue); err != nil {
				t.Fatal(err)
			}
			f.Changed = false
		}
	}
	reset()
	t.Cleanup(reset)
}

func previewTestServer(t *testing.T) *deliverableTestServer {
	t.Helper()
	resetPreviewFlags(t)
	return revisionTestServer(t)
}

func runPreview(t *testing.T, input string, args ...string) (string, string, error) {
	t.Helper()
	cmd := GetRootCmd()
	var out, stderr bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&stderr)
	cmd.SetIn(strings.NewReader(input))
	cmd.SetArgs(append([]string{"inspect", "deliverable"}, args...))
	t.Cleanup(func() {
		cmd.SetOut(nil)
		cmd.SetErr(nil)
		cmd.SetIn(nil)
		cmd.SetArgs(nil)
		resetPersistentFlag(t, cmd, "output", "table")
	})
	_, err := executeC()
	return out.String(), stderr.String(), err
}

func TestInspectDeliverableFormatsAndWireParams(t *testing.T) {
	for _, format := range []string{"table", "json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			s := previewTestServer(t)
			s.RegisterHandler("POST", revisionTestPath+enabledRevisionID+"/preview", func(w http.ResponseWriter, r *http.Request) {
				checkDeliverableToken(t, r)
				var request struct {
					Params map[string]json.RawMessage `json:"run_params"`
					Rows   int                        `json:"max_rows"`
					Typed  json.RawMessage            `json:"run_params_typed"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				if request.Rows != 50 || request.Typed != nil || len(request.Params) != 5 {
					t.Errorf("request = %#v", request)
				}
				for key, want := range map[string]string{"period": `"2026-09"`, "big": "9007199254740993", "float": "2026.0", "flag": "false", "max": "18446744073709551615"} {
					if got := string(request.Params[key]); got != want {
						t.Errorf("%s = %s, want %s", key, got, want)
					}
				}
				testutil.RespondJSON(w, http.StatusOK, json.RawMessage(deliverablePreviewRecord))
			})
			out, stderr, err := runPreview(t, "period: '2026-09'\nbig: 9007199254740993\nfloat: 2026.0\nflag: false\nmax: 18446744073709551615\n",
				"deliverable/"+liveDeliverableID, "--params-file", "-", "-o", format)
			if err != nil {
				t.Fatal(err)
			}
			counts := "120 kept / 7 left out / 3 blocked; showing 2 rows"
			if format == "table" {
				for _, want := range []string{counts, "(no preview rows)", "9007199254740993", "Alpha!B1 = 18446744073709551615", "Empty!A1 = null", "$12.00"} {
					if !strings.Contains(out, want) {
						t.Errorf("missing %q: %s", want, out)
					}
				}
				if strings.Index(out, "P-2") > strings.Index(out, "P-1") {
					t.Errorf("row order changed: %s", out)
				}
				if stderr != "" {
					t.Errorf("stderr = %q", stderr)
				}
			} else {
				if !strings.Contains(stderr, counts) || strings.Contains(out, "kept /") {
					t.Errorf("out = %s; stderr = %s", out, stderr)
				}
				if format == "json" {
					assertSeriesJSON(t, out, deliverablePreviewRecord)
				} else {
					testutil.AssertYAMLReadsAsJSON(t, out, deliverablePreviewRecord)
				}
			}
			want := []string{"GET " + deliverableTestPath + "/" + liveDeliverableID, deliverableRequest(liveDeliverableID, "revisions"),
				"GET " + revisionTestPath + enabledRevisionID, "POST " + revisionTestPath + enabledRevisionID + "/preview"}
			if got := s.Requests(); !reflect.DeepEqual(got, want) {
				t.Errorf("requests = %v, want %v", got, want)
			}
		})
	}
}

func TestInspectDeliverableSelectorsPeriodAndMaxRows(t *testing.T) {
	for _, tc := range []struct {
		name, id string
		args     []string
		rows     int
	}{
		{"enabled", enabledRevisionID, nil, 50},
		{"release", draftRevisionID, []string{"--release", "2026.2", "--max-rows", "1000"}, 1000},
		{"revision", draftRevisionID, []string{"--revision", "deliverable-revision/" + draftRevisionID, "--max-rows", "1"}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := previewTestServer(t)
			s.RegisterHandler("POST", revisionTestPath+tc.id+"/preview", func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					Params map[string]string `json:"run_params"`
					Rows   int               `json:"max_rows"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
				}
				if req.Rows != tc.rows || !reflect.DeepEqual(req.Params, map[string]string{"period": "2026-09"}) {
					t.Errorf("request = %#v", req)
				}
				testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"sheets":[],"cells":[]}`))
			})
			out, _, err := runPreview(t, "", append([]string{liveDeliverableID, "--period", "2026-09"}, tc.args...)...)
			if err != nil || out != "No preview sheets or cells.\n" {
				t.Fatalf("out = %q, err = %v", out, err)
			}
		})
	}
}

func TestInspectDeliverableLocalErrorsBeforeCredentials(t *testing.T) {
	for _, args := range [][]string{
		{""}, {" "}, {"table/42"}, {liveDeliverableID, "--max-rows", "0"}, {liveDeliverableID, "--max-rows", "1001"},
		{liveDeliverableID, "--max-rows", "-1"}, {liveDeliverableID, "--period", ""}, {liveDeliverableID, "--period", " "},
		{liveDeliverableID, "--params-file", ""}, {liveDeliverableID, "--release", ""}, {liveDeliverableID, "--revision", "bad"},
		{liveDeliverableID, "--release", "2026.2", "--revision", draftRevisionID},
		{liveDeliverableID, "--period", "2026-09", "--params-file", "unused.yaml"}, {liveDeliverableID, "-o", "name"},
		{liveDeliverableID, "--params-file", filepath.Join(t.TempDir(), "missing.yaml")},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			resetPreviewFlags(t)
			err := runOutsideGetWithoutContext(t, append([]string{"inspect", "deliverable"}, args...)...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit = %d, want 2: %v", code, err)
			}
		})
	}
	path := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(path, []byte("period: [2026]"), 0600); err != nil {
		t.Fatal(err)
	}
	resetPreviewFlags(t)
	if err := runOutsideGetWithoutContext(t, "inspect", "deliverable", liveDeliverableID, "--params-file", path); qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
		t.Fatalf("want exit 2: %v", err)
	}
}

func TestInspectDeliverableRefusesMissingParamsBeforePOST(t *testing.T) {
	for _, input := range []string{"{}", "period: null", "period: ' '", "Period: 2026"} {
		t.Run(input, func(t *testing.T) {
			s := previewTestServer(t)
			out, _, err := runPreview(t, input, liveDeliverableID, "--params-file", "-")
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage || out != "" || !strings.Contains(err.Error(), `"period"`) {
				t.Fatalf("out = %q; exit = %d; %v", out, code, err)
			}
			for _, request := range s.Requests() {
				if strings.HasPrefix(request, "POST") {
					t.Errorf("unexpected %s", request)
				}
			}
		})
	}
}

func TestInspectDeliverableChecksSelectedRevisionAndFilenameParams(t *testing.T) {
	s := previewTestServer(t)
	body := strings.ReplaceAll(revisionTreeRecord, enabledRevisionID, draftRevisionID)
	body = strings.ReplaceAll(body, `"run_param":"period"`, `"run_param":"draft_value"`)
	s.RegisterHandler("GET", revisionTestPath+draftRevisionID, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(body))
	})
	s.RegisterHandler("GET", deliverableTestPath+"/"+liveDeliverableID, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(strings.ReplaceAll(liveDeliverableRecord, "bordereau-{period}.xlsx", "bordereau-{filename_value}.xlsx")))
	})
	out, _, err := runPreview(t, "", liveDeliverableID, "--release", "2026.2", "--period", "2026-09")
	if qerrors.GetExitCode(err) != qerrors.ExitBadUsage || out != "" {
		t.Fatalf("out = %q, err = %v", out, err)
	}
	for _, value := range []string{"draft_value", "filename_value"} {
		if !strings.Contains(err.Error(), value) {
			t.Errorf("missing %s: %v", value, err)
		}
	}
	for _, request := range s.Requests() {
		if strings.HasPrefix(request, "POST") {
			t.Errorf("unexpected %s", request)
		}
	}
}

func TestInspectDeliverableHTTPFailures(t *testing.T) {
	for _, tc := range []struct {
		status, exit int
		body, text   string
	}{
		{403, qerrors.ExitUnauthorized, `{"detail":{"msg":"Permission denied"}}`, "Permission denied"},
		{404, qerrors.ExitNotFound, `{"detail":{"msg":"Revision gone"}}`, "Revision gone"},
		{409, qerrors.ExitBadUsage, `{"detail":{"msg":"No backup settings"}}`, "No backup settings"},
		{422, qerrors.ExitBadUsage, `{"detail":{"msg":"Revision invalid","error_code":"REVISION_INVALID","context":{"errors":[{"loc":["sheet_bindings","Alpha"],"msg":"Column missing"}]}}}`, "sheet_bindings.Alpha: Column missing"},
		{422, qerrors.ExitBadUsage, `{"detail":{"msg":"Series has no table for this period","error_code":"SCOPE_ERROR","loc":["body","run_params","period"]}}`, "Series has no table"},
		{500, qerrors.ExitGenericError, `{"detail":"Preview failed"}`, "Preview failed"},
	} {
		t.Run(tc.text, func(t *testing.T) {
			s := previewTestServer(t)
			s.RegisterHandler("POST", revisionTestPath+enabledRevisionID+"/preview", func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, tc.status, json.RawMessage(tc.body))
			})
			out, _, err := runPreview(t, "", liveDeliverableID, "--period", "2026-09", "-o", "json")
			if code := qerrors.GetExitCode(err); code != tc.exit {
				t.Fatalf("exit = %d, want %d: %v", code, tc.exit, err)
			}
			if out != "" || !strings.Contains(err.Error(), tc.text) {
				t.Errorf("out = %q, err = %v", out, err)
			}
		})
	}
}

func TestInspectDeliverableChecksRevisionParent(t *testing.T) {
	s := previewTestServer(t)
	s.RegisterHandler("GET", revisionTestPath+enabledRevisionID, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(strings.ReplaceAll(revisionTreeRecord, liveDeliverableID, trashedDeliverableID)))
	})
	out, _, err := runPreview(t, "", liveDeliverableID, "--period", "2026-09")
	if qerrors.GetExitCode(err) != qerrors.ExitNotFound || out != "" {
		t.Fatalf("out = %q, err = %v", out, err)
	}
	if len(s.Requests()) != 3 {
		t.Errorf("requests = %v", s.Requests())
	}
}

func TestInspectDeliverableRequiresPeriodQuestion(t *testing.T) {
	s := previewTestServer(t)
	s.RegisterHandler("GET", deliverableTestPath+"/"+liveDeliverableID, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(strings.ReplaceAll(liveDeliverableRecord, `"period_param":"period"`, `"period_param":null`)))
	})
	out, _, err := runPreview(t, "", liveDeliverableID, "--period", "2026-09")
	if qerrors.GetExitCode(err) != qerrors.ExitBadUsage || out != "" || !strings.Contains(err.Error(), "period parameter") {
		t.Fatalf("out = %q, err = %v", out, err)
	}
	for _, request := range s.Requests() {
		if strings.HasPrefix(request, "POST") {
			t.Errorf("unexpected %s", request)
		}
	}
}

func TestInspectDeliverableReadsParamsFile(t *testing.T) {
	s := previewTestServer(t)
	path := filepath.Join(t.TempDir(), "params.yaml")
	if err := os.WriteFile(path, []byte("period: '2026-09'"), 0600); err != nil {
		t.Fatal(err)
	}
	s.RegisterHandler("POST", revisionTestPath+enabledRevisionID+"/preview", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"period":"2026-09"`) {
			t.Errorf("body = %s", body)
		}
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"sheets":[],"cells":[]}`))
	})
	if _, _, err := runPreview(t, "", liveDeliverableID, "--params-file", path); err != nil {
		t.Fatal(err)
	}
}

// maskedPreviewRecord is a preview whose sheet carries two PII columns masked
// and whose sum cell over one of them comes back masked.
const maskedPreviewRecord = `{"sheets":[
 {"sheet_binding_id":"10000000-0000-4000-8000-000000000002","sheet_name":"Alpha","member_dataset_id":42,
 "columns":["policy","email","ssn"],
 "rows":[{"policy":"P-1","email":"•••••.com","ssn":"•••••"}],
 "masked_fields":["email","ssn"],"in_scope_count":1,"indeterminate_count":0,"out_of_scope_count":0}],
 "cells":[{"sheet_name":"Alpha","cell_ref":"B1","value":"•••••0.00","masked_fields":["premium"]},
 {"sheet_name":"Alpha","cell_ref":"B2","value":12}]}`

func TestInspectDeliverableNamesMaskedFields(t *testing.T) {
	for _, format := range []string{"table", "json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			s := previewTestServer(t)
			s.RegisterHandler("POST", revisionTestPath+enabledRevisionID+"/preview", func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, json.RawMessage(maskedPreviewRecord))
			})
			out, _, err := runPreview(t, "", "deliverable/"+liveDeliverableID, "--period", "2026-09", "-o", format)
			if err != nil {
				t.Fatal(err)
			}
			switch format {
			case "json":
				assertSeriesJSON(t, out, maskedPreviewRecord)
			case "yaml":
				testutil.AssertYAMLReadsAsJSON(t, out, maskedPreviewRecord)
			default:
				want := "Sheet Alpha (table 42): 1 kept / 0 left out / 0 blocked; showing 1 rows\n" +
					"Masked fields: email, ssn\n" +
					"policy  email      ssn\n" +
					"P-1     •••••.com  •••••\n" +
					"\n" +
					"Cells:\n" +
					"  Alpha!B1 = •••••0.00 (masked: premium)\n" +
					"  Alpha!B2 = 12\n"
				if out != want {
					t.Errorf("out:\n%s\nwant:\n%s", out, want)
				}
			}
		})
	}
}
