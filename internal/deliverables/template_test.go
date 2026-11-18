package deliverables

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const (
	skeletonsPath            = "/api/orgs/" + testOrgID + "/deliverable-skeletons"
	revisionSkeletonUpload   = revisionPath + "/skeleton-upload-url"
	revisionSkeletonFinalize = revisionPath + "/skeleton/finalize"
	putTargetPath            = "/bucket/put-target"
	postTargetPath           = "/bucket/post-target"
)

// templateFile writes a template named name and returns its path and bytes.
func templateFile(t *testing.T, name string) (string, []byte) {
	t.Helper()
	content := []byte("PK\x03\x04 not really a workbook \x00\xff\n")
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return path, content
}

// respondBody writes body verbatim, so a test controls every number literal.
func respondBody(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

// recordJSON decodes r's body into a map of raw values.
func recordJSON(t *testing.T, r *http.Request) map[string]json.RawMessage {
	t.Helper()
	var body map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Errorf("decode request body: %v", err)
	}
	return body
}

func revisionRef() *uuid.UUID {
	id := uuid.MustParse(revV3)
	return &id
}

func TestUploadTemplateMintsOnTheRouteTheRevisionChoosesAndPutsTheBytes(t *testing.T) {
	for _, tc := range []struct {
		name     string
		revision *uuid.UUID
		mintPath string
	}{
		{name: "organization", mintPath: skeletonsPath + "/upload-url"},
		{name: "revision", revision: revisionRef(), mintPath: revisionSkeletonUpload},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path, content := templateFile(t, "Book.XLSM")
			mock := testutil.NewMockAPIServer()
			defer mock.Close()
			var minted map[string]json.RawMessage
			mock.RegisterHandler("PUT", tc.mintPath, func(w http.ResponseWriter, r *http.Request) {
				minted = recordJSON(t, r)
				respondBody(w, http.StatusOK, `{"url":"http://mock.local`+putTargetPath+`?sig=1","http_method":"PUT",`+
					`"fields":{"Content-Type":"application/vnd.ms-excel.sheet.macroEnabled.12"},"key":"k","skeleton_id":41}`)
			})
			var sent []byte
			var contentType, query string
			mock.RegisterHandler("PUT", putTargetPath, func(w http.ResponseWriter, r *http.Request) {
				sent, _ = io.ReadAll(r.Body)
				contentType, query = r.Header.Get("Content-Type"), r.URL.RawQuery
				w.WriteHeader(http.StatusOK)
			})

			id, err := NewClient(mock.URL(), testOrgID, 0).UploadTemplate("token", tc.revision, path)
			if err != nil {
				t.Fatalf("UploadTemplate: %v", err)
			}
			if id != 41 {
				t.Errorf("id = %d, want 41", id)
			}
			if want := []string{"PUT " + tc.mintPath, "PUT " + putTargetPath}; !reflect.DeepEqual(mock.Requests(), want) {
				t.Errorf("requests = %v, want %v", mock.Requests(), want)
			}
			if got := string(minted["original_filename"]); got != `"Book.XLSM"` {
				t.Errorf("original_filename = %s, want the base name", got)
			}
			if got, want := string(minted["file_size"]), strconv.Itoa(len(content)); got != want {
				t.Errorf("file_size = %s, want %s", got, want)
			}
			if !bytes.Equal(sent, content) {
				t.Errorf("uploaded %q, want %q", sent, content)
			}
			if contentType != "application/vnd.ms-excel.sheet.macroEnabled.12" || query != "sig=1" {
				t.Errorf("Content-Type = %q, query = %q", contentType, query)
			}
		})
	}
}

func TestUploadTemplatePostsTheFieldsAsTheirJSONText(t *testing.T) {
	path, content := templateFile(t, "report.xlsx")
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	mock.RegisterHandler("PUT", skeletonsPath+"/upload-url", func(w http.ResponseWriter, _ *http.Request) {
		respondBody(w, http.StatusOK, `{"url":"http://mock.local`+postTargetPath+`","http_method":"POST","fields":{`+
			`"key":"deliverable-skeletons/x.xlsx","policy":"c2lnbmVk","x-amz-meta-size":12345,"ratio":1.50,`+
			`"big":12345678901234567890,"success":true,"secure":false,"gone":null},"key":"k","skeleton_id":7}`)
	})
	var values map[string][]string
	var uploaded []byte
	mock.RegisterHandler("POST", postTargetPath, func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("not multipart: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		values = r.MultipartForm.Value
		file, _, err := r.FormFile("file")
		if err != nil {
			t.Errorf("file part: %v", err)
		} else {
			uploaded, _ = io.ReadAll(file)
			_ = file.Close()
		}
		w.WriteHeader(http.StatusNoContent)
	})

	id, err := NewClient(mock.URL(), testOrgID, 0).UploadTemplate("token", nil, path)
	if err != nil {
		t.Fatalf("UploadTemplate: %v", err)
	}
	if id != 7 {
		t.Errorf("id = %d, want 7", id)
	}
	want := map[string][]string{
		"key": {"deliverable-skeletons/x.xlsx"}, "policy": {"c2lnbmVk"}, "x-amz-meta-size": {"12345"},
		"ratio": {"1.50"}, "big": {"12345678901234567890"}, "success": {"true"}, "secure": {"false"},
	}
	if !reflect.DeepEqual(values, want) {
		t.Errorf("fields = %v, want %v", values, want)
	}
	if !bytes.Equal(uploaded, content) {
		t.Errorf("uploaded %q, want %q", uploaded, content)
	}
}

func TestUploadTemplateRefusesAStructuredFieldBeforeSending(t *testing.T) {
	for _, field := range []string{`{"a":1}`, `[1]`} {
		t.Run(field, func(t *testing.T) {
			path, _ := templateFile(t, "report.xlsx")
			mock := testutil.NewMockAPIServer()
			defer mock.Close()
			mock.RegisterHandler("PUT", skeletonsPath+"/upload-url", func(w http.ResponseWriter, _ *http.Request) {
				respondBody(w, http.StatusOK, `{"url":"http://mock.local`+postTargetPath+`","http_method":"POST",`+
					`"fields":{"policy":`+field+`},"key":"k","skeleton_id":7}`)
			})
			_, err := NewClient(mock.URL(), testOrgID, 0).UploadTemplate("token", nil, path)
			if err == nil || !strings.Contains(err.Error(), `"policy"`) {
				t.Fatalf("err = %v, want one naming the field", err)
			}
			if got := mock.Requests(); len(got) != 1 {
				t.Errorf("requests = %v, want the mint only", got)
			}
		})
	}
}

func TestUploadTemplateRefusals(t *testing.T) {
	for _, tc := range []struct {
		name     string
		revision *uuid.UUID
		status   int
		body     string
		code     int
		says     string
	}{
		{name: "extension", status: http.StatusBadRequest, code: qerrors.ExitBadUsage, says: "Unsupported skeleton extension",
			body: `{"detail":{"msg":"Unsupported skeleton extension '.xls'. Supported: .xlsx, .xlsm","error_id":""}}`},
		{name: "no backup settings", status: http.StatusConflict, code: qerrors.ExitBadUsage, says: "backup settings",
			body: `{"detail":{"msg":"The organization has no backup settings.","error_id":""}}`},
		{name: "not a draft", revision: revisionRef(), status: http.StatusConflict, code: qerrors.ExitBadUsage, says: "not a draft",
			body: `{"detail":{"msg":"Revision v2 is not a draft.","error_id":""}}`},
		{name: "unknown revision", revision: revisionRef(), status: http.StatusNotFound, code: qerrors.ExitNotFound,
			body: `{"detail":{"msg":"Deliverable revision can not be found in this organization.","error_id":""}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path, _ := templateFile(t, "report.xlsx")
			mintPath := skeletonsPath + "/upload-url"
			if tc.revision != nil {
				mintPath = revisionSkeletonUpload
			}
			mock := testutil.NewMockAPIServer()
			defer mock.Close()
			mock.RegisterHandler("PUT", mintPath, func(w http.ResponseWriter, _ *http.Request) { respondBody(w, tc.status, tc.body) })
			_, err := NewClient(mock.URL(), testOrgID, 0).UploadTemplate("token", tc.revision, path)
			if code := qerrors.GetExitCode(err); code != tc.code {
				t.Fatalf("exit = %d, want %d; error = %v", code, tc.code, err)
			}
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != tc.status {
				t.Errorf("error metadata lost: %#v", err)
			}
			if !strings.Contains(err.Error(), tc.says) {
				t.Errorf("error lacks %q: %v", tc.says, err)
			}
			if got := mock.Requests(); len(got) != 1 {
				t.Errorf("requests = %v, want the mint only", got)
			}
		})
	}
}

func TestUploadTemplateChecksTheFileBeforeAnyRequest(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "folder.xlsx")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		path string
		code int
	}{
		{name: "directory", path: dir, code: qerrors.ExitBadUsage},
		{name: "missing", path: filepath.Join(t.TempDir(), "missing.xlsx"), code: qerrors.ExitGenericError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()
			_, err := NewClient(mock.URL(), testOrgID, 0).UploadTemplate("token", nil, tc.path)
			if code := qerrors.GetExitCode(err); code != tc.code {
				t.Fatalf("exit = %d, want %d; error = %v", code, tc.code, err)
			}
			if got := mock.Requests(); len(got) != 0 {
				t.Errorf("requests = %v, want none", got)
			}
		})
	}
}

const finalizedTemplate = `{"skeleton_id":7,"signature":"ab12","keep_vba":false,` +
	`"workbook_inventory":{"has_vba":false,"sheets":[]},"fidelity_report":{"version":2,"is_lossy":false,"losses":[],"lost_parts":[]},` +
	`"attached":false,"reused_existing_skeleton":true,"anchor_report":null}`

func TestFinalizeTemplateSendsAcknowledgeLossyOnTheChosenRoute(t *testing.T) {
	for _, tc := range []struct {
		name        string
		revision    *uuid.UUID
		path        string
		acknowledge bool
	}{
		{name: "organization", path: skeletonsPath + "/finalize"},
		{name: "organization acknowledged", path: skeletonsPath + "/finalize", acknowledge: true},
		{name: "revision", revision: revisionRef(), path: revisionSkeletonFinalize},
		{name: "revision acknowledged", revision: revisionRef(), path: revisionSkeletonFinalize, acknowledge: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()
			var sent map[string]json.RawMessage
			mock.RegisterHandler("POST", tc.path, func(w http.ResponseWriter, r *http.Request) {
				sent = recordJSON(t, r)
				respondBody(w, http.StatusOK, finalizedTemplate)
			})
			result, err := NewClient(mock.URL(), testOrgID, 0).FinalizeTemplate("token", tc.revision, 41, tc.acknowledge)
			if err != nil {
				t.Fatalf("FinalizeTemplate: %v", err)
			}
			if want := []string{"POST " + tc.path}; !reflect.DeepEqual(mock.Requests(), want) {
				t.Errorf("requests = %v, want %v", mock.Requests(), want)
			}
			wantAck := "false"
			if tc.acknowledge {
				wantAck = "true"
			}
			if got := string(sent["acknowledge_lossy"]); got != wantAck {
				t.Errorf("acknowledge_lossy = %q, want %q", got, wantAck)
			}
			if got := string(sent["skeleton_id"]); got != "41" {
				t.Errorf("skeleton_id = %q, want 41", got)
			}
			if result.SkeletonID != 7 || result.Signature != "ab12" || !*result.ReusedExistingSkeleton {
				t.Errorf("result = %+v, want the reused template 7", result.SkeletonFinalizeResponse)
			}
			if string(result.Body) != finalizedTemplate {
				t.Errorf("Body = %s, want the response body", result.Body)
			}
		})
	}
}

// serveFinalize answers the organization's finalize route with status and
// {"detail": detail}.
func serveFinalize(t *testing.T, status int, detail string) *testutil.MockAPIServer {
	t.Helper()
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	mock.RegisterHandler("POST", skeletonsPath+"/finalize", func(w http.ResponseWriter, _ *http.Request) {
		respondBody(w, status, `{"detail":`+detail+`}`)
	})
	return mock
}

func TestFinalizeTemplateListsTheLossesOfALossyRefusal(t *testing.T) {
	for _, tc := range []struct {
		name   string
		report string
		lines  []string
	}{
		{name: "version 2",
			report: `{"version":2,"is_lossy":true,"losses":[` +
				`{"kind":"image","sheet":"Claims","count":2,"label":"2 images on Claims"},` +
				`{"kind":"chart","sheet":null,"count":1,"label":"1 chart\u001b[31m"},` +
				`{"kind":"other","sheet":null,"count":1,"label":""}],` +
				`"lost_parts":["xl/media/image1.png"],"notes":["Pillow missing"]}`,
			lines: []string{"  - 2 images on Claims", `  - 1 chart\x1b[31m`}},
		{name: "version 1",
			report: `{"is_lossy":true,"lost_parts":["xl/media/image1.png","xl/charts/chart1.xml"]}`,
			lines:  []string{"  - xl/media/image1.png", "  - xl/charts/chart1.xml"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := serveFinalize(t, http.StatusConflict, `{"msg":"Generated files cannot keep: xl/media/image1.png. `+
				`Upload a template without them, or acknowledge the loss to continue.","error_code":"SKELETON_LOSSY","error_id":"",`+
				`"context":{"fidelity_report":`+tc.report+`,"skeleton_id":7}}`)
			_, err := NewClient(mock.URL(), testOrgID, 0).FinalizeTemplate("token", nil, 41, false)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit = %d, want %d; error = %v", code, qerrors.ExitBadUsage, err)
			}
			var lossy *TemplateLossyError
			if !errors.As(err, &lossy) {
				t.Fatalf("err = %#v, want a *TemplateLossyError", err)
			}
			if lossy.TemplateID != 7 || !lossy.Report.IsLossy {
				t.Errorf("TemplateID = %d, report = %+v; want the context's template 7", lossy.TemplateID, lossy.Report)
			}
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusConflict || apiErr.Code != "SKELETON_LOSSY" {
				t.Errorf("error metadata lost: %#v", err)
			}
			text := err.Error()
			want := apiErr.Error() + "\n" + strings.Join(tc.lines, "\n") +
				"\nThe file is uploaded as deliverable-template/7, and this request attached nothing."
			if text != want {
				t.Errorf("error =\n%s\nwant\n%s", text, want)
			}
			if !strings.Contains(text, "Generated files cannot keep") || strings.Contains(text, "\x1b") {
				t.Errorf("error = %q", text)
			}
		})
	}
}

func TestFinalizeTemplateKeepsOtherRefusalsPlain(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		detail string
		code   int
		says   string
	}{
		{name: "conflict without a code", status: http.StatusConflict, code: qerrors.ExitBadUsage, says: "backup settings",
			detail: `{"msg":"The organization has no backup settings.","error_id":""}`},
		{name: "lossy without a template id", status: http.StatusConflict, code: qerrors.ExitBadUsage, says: "Generated files",
			detail: `{"msg":"Generated files cannot keep: x.","error_code":"SKELETON_LOSSY","error_id":"","context":{"fidelity_report":{"is_lossy":true}}}`},
		{name: "lossy with a string template id", status: http.StatusConflict, code: qerrors.ExitBadUsage, says: "Generated files",
			detail: `{"msg":"Generated files cannot keep: x.","error_code":"SKELETON_LOSSY","error_id":"","context":{"fidelity_report":{"is_lossy":true},"skeleton_id":"7"}}`},
		{name: "invalid workbook", status: http.StatusUnprocessableEntity, code: qerrors.ExitBadUsage, says: "Uploaded skeleton is not a valid workbook: File is not a zip file",
			detail: `{"msg":"Uploaded skeleton is not a valid workbook: File is not a zip file","error_code":"SKELETON_INVALID","error_id":"0A1B2C3D"}`},
		{name: "unknown template", status: http.StatusNotFound, code: qerrors.ExitNotFound, says: "Skeleton 41 can not be found",
			detail: `{"msg":"Skeleton 41 can not be found in this organization.","error_id":""}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := serveFinalize(t, tc.status, tc.detail)
			_, err := NewClient(mock.URL(), testOrgID, 0).FinalizeTemplate("token", nil, 41, false)
			if code := qerrors.GetExitCode(err); code != tc.code {
				t.Fatalf("exit = %d, want %d; error = %v", code, tc.code, err)
			}
			var lossy *TemplateLossyError
			if errors.As(err, &lossy) {
				t.Errorf("err = %v, want no *TemplateLossyError", err)
			}
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != tc.status {
				t.Errorf("error metadata lost: %#v", err)
			}
			if !strings.Contains(err.Error(), tc.says) {
				t.Errorf("error lacks %q: %v", tc.says, err)
			}
		})
	}
}

func TestValidateTemplatePathTakesTheExtensionAsTheServerDoes(t *testing.T) {
	for _, tc := range []struct {
		path string
		ok   bool
		vba  bool
	}{
		{path: "book.XLSM", ok: true, vba: true},
		{path: "dir/Report.xlsx", ok: true},
		{path: "a.xlsx?x=1", ok: true},
		{path: "a.xlsm?x=1.csv", ok: true, vba: true},
		{path: "a.b.Xlsx", ok: true},
		{path: "a.xls"},
		{path: "a.csv"},
		{path: "noext"},
		{path: "a.xlsx.bak"},
		{path: "a?.xlsx"},
		{path: ".xlsx"},
		{path: "..xlsm"},
		{path: ""},
	} {
		t.Run(tc.path, func(t *testing.T) {
			err := ValidateTemplatePath(tc.path)
			want := qerrors.ExitBadUsage
			if tc.ok {
				want = 0
			}
			if code := qerrors.GetExitCode(err); code != want {
				t.Errorf("exit = %d, want %d; error = %v", code, want, err)
			}
			if got := TemplateKeepsVBA(tc.path); got != tc.vba {
				t.Errorf("TemplateKeepsVBA = %t, want %t", got, tc.vba)
			}
		})
	}
}

func TestFidelityLossLinesSwitchOnTheReportVersion(t *testing.T) {
	for _, tc := range []struct {
		name   string
		report string
		want   []string
	}{
		{name: "version 2 labels", want: []string{"2 images", "1 chart"},
			report: `{"version":2,"is_lossy":true,"losses":[{"kind":"i","sheet":null,"count":2,"label":"2 images"},{"kind":"x","sheet":null,"count":1,"label":""},{"kind":"c","sheet":null,"count":1,"label":"1 chart"}],"lost_parts":["raw"],"notes":["n"]}`},
		{name: "version 3 labels", want: []string{"2 images"},
			report: `{"version":3,"is_lossy":true,"losses":[{"kind":"i","sheet":null,"count":2,"label":"2 images"}],"lost_parts":["raw"]}`},
		{name: "version 2 without losses", report: `{"version":2,"is_lossy":false,"lost_parts":["raw"]}`},
		{name: "version 1 parts", want: []string{"xl/media/image1.png"},
			report: `{"version":1,"is_lossy":true,"losses":[{"kind":"i","sheet":null,"count":1,"label":"ignored"}],"lost_parts":["xl/media/image1.png",""],"notes":["n"]}`},
		{name: "no version", want: []string{"xl/media/image1.png"}, report: `{"is_lossy":true,"lost_parts":["xl/media/image1.png"]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var report api.FidelityReport
			if err := json.Unmarshal([]byte(tc.report), &report); err != nil {
				t.Fatal(err)
			}
			if got := FidelityLossLines(report); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("lines = %q, want %q", got, tc.want)
			}
		})
	}
}

const (
	columnMovedID  = "c0000000-0000-4000-8000-000000000001"
	columnBrokenID = "c0000000-0000-4000-8000-000000000002"
	cellMovedID    = "c0000000-0000-4000-8000-000000000003"
	cellFlaggedID  = "c0000000-0000-4000-8000-000000000004"
)

func TestFormatTemplateRendersTheInspectionAndTheAnchorReport(t *testing.T) {
	body := `{"skeleton_id":7,"signature":"ab12cd","keep_vba":true,"attached":true,"reused_existing_skeleton":true,` +
		`"workbook_inventory":{"has_vba":true,"sheets":[{"name":"Claims","max_row":10,"max_column":4,"merged_ranges":[],"header_rows":{"1":["Amount","Fee"]}}]},` +
		`"fidelity_report":{"version":2,"is_lossy":true,"losses":[{"kind":"image","sheet":"Claims","count":2,"label":"2 images on Claims"}],"lost_parts":["2 images on Claims"],"notes":["Pillow missing"]},` +
		`"anchor_report":{"column_bindings":[` +
		`{"column_binding_id":"` + columnMovedID + `","sheet_binding_id":"` + revV1 + `","sheet_name":"Claims","target_label":"Amount","outcome":"moved","reason":null,"from_column_index":3,"to_column_index":5},` +
		`{"column_binding_id":"` + columnBrokenID + `","sheet_binding_id":"` + revV1 + `","sheet_name":"Claims","target_label":"Fee","outcome":"broken","reason":"label_missing","from_column_index":4,"to_column_index":4}],` +
		`"cell_bindings":[` +
		`{"cell_binding_id":"` + cellMovedID + `","sheet_name":"Sum\u001b[31m","target_label":"Total\nDue","outcome":"moved","reason":null,"from_cell_ref":"B2","to_cell_ref":"B4","from_label_cell_ref":"A2","to_label_cell_ref":"A4"},` +
		`{"cell_binding_id":"` + cellFlaggedID + `","sheet_name":"Summary","target_label":null,"outcome":"needs_confirmation","reason":null,"from_cell_ref":"C7","to_cell_ref":"C7","from_label_cell_ref":null,"to_label_cell_ref":null}]}}`
	var response api.SkeletonFinalizeResponse
	if err := json.Unmarshal([]byte(body), &response); err != nil {
		t.Fatal(err)
	}
	out := FormatTemplate(&TemplateResult{SkeletonFinalizeResponse: &response, Body: json.RawMessage(body)})
	for _, want := range []string{
		"Signature:                ab12cd\nVBA kept:                 true\nReused existing template: true\n",
		"\nTemplate inventory (VBA: true):\n  Claims: 10 rows x 4 columns",
		"    Header row 1: Amount | Fee\n",
		"\nFidelity (version 2, lossy: true):\n  Loss: 2 images on Claims\n  Note: Pillow missing\n",
		"\nRe-anchored bindings (4):\n" +
			"  Column Claims!C -> E: moved; label: Amount [" + columnMovedID + "]\n" +
			"  Column Claims!D: broken (label_missing); label: Fee [" + columnBrokenID + "]\n" +
			`  Cell Sum\x1b[31m!B2 -> B4: moved; label: Total\nDue [` + cellMovedID + "]\n" +
			"  Cell Summary!C7: needs_confirmation [" + cellFlaggedID + "]\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\x1b") || strings.Contains(out, "Total\nDue") {
		t.Errorf("server text was not sanitized:\n%s", out)
	}
	if !strings.HasSuffix(out, "["+cellFlaggedID+"]\n") {
		t.Errorf("anchor report is not last:\n%s", out)
	}
}

func TestFormatTemplateWithoutAnAnchorReport(t *testing.T) {
	var response api.SkeletonFinalizeResponse
	if err := json.Unmarshal([]byte(finalizedTemplate), &response); err != nil {
		t.Fatal(err)
	}
	out := FormatTemplate(&TemplateResult{SkeletonFinalizeResponse: &response})
	if strings.Contains(out, "Re-anchored") {
		t.Errorf("output has an anchor section:\n%s", out)
	}
	if !strings.Contains(out, "Reused existing template: true\n") || !strings.Contains(out, "Fidelity (version 2, lossy: false):\n") {
		t.Errorf("output =\n%s", out)
	}
}
