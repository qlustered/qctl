package root

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/deliverables"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const (
	templateOrgPath     = "/api/orgs/" + scopeTestOrgID + "/deliverable-skeletons/"
	templateStoragePath = "/storage/deliverable-template-object"

	orgTemplateUploadURL = "PUT " + templateOrgPath + "upload-url"
	orgTemplateFinalize  = "POST " + templateOrgPath + "finalize"
	templateStoragePUT   = "PUT " + templateStoragePath

	draftTemplateUploadURL = "PUT " + revisionTestPath + draftRevisionID + "/skeleton-upload-url"
	draftTemplateFinalize  = "POST " + revisionTestPath + draftRevisionID + "/skeleton/finalize"
)

// templateFileBytes is the content of every template file a case uploads.
var templateFileBytes = []byte("PK\x03\x04 template bytes \x00\xff")

const lossyTemplateReport = `{"version":2,"is_lossy":true,"losses":[` +
	`{"kind":"image","sheet":"Claims","count":1,"label":"1 image on Claims","parts":["xl/media/image1.png"]},` +
	`{"kind":"chart","sheet":"Summary","count":2,"label":"2 charts on Summary","parts":["xl/charts/chart1.xml"]}],` +
	`"lost_parts":["xl/media/image1.png","xl/charts/chart1.xml"],"notes":[]}`

const templateAnchorReport = `{"column_bindings":[{"column_binding_id":"20000000-0000-4000-8000-000000000001",` +
	`"sheet_binding_id":"10000000-0000-4000-8000-000000000002","sheet_name":"Claims","from_column_index":2,` +
	`"to_column_index":3,"outcome":"moved","reason":null,"target_label":"Premium"}],` +
	`"cell_bindings":[{"cell_binding_id":"30000000-0000-4000-8000-000000000001","sheet_name":"Summary",` +
	`"from_cell_ref":"C4","to_cell_ref":"C4","from_label_cell_ref":"B4","to_label_cell_ref":null,` +
	`"outcome":"broken","reason":"label_missing","target_label":"Total"}]}`

// finalizedTemplate is a finalize answer.
type finalizedTemplate struct {
	id       int
	keepVBA  bool
	lossy    bool
	reused   bool
	attached bool
	anchor   string
}

// body is the answer's JSON, with a field qctl does not model holding an
// integer past 2^53.
func (f finalizedTemplate) body() string {
	fidelity := `{"version":2,"is_lossy":false,"losses":[],"lost_parts":[],"notes":[]}`
	if f.lossy {
		fidelity = lossyTemplateReport
	}
	anchor := f.anchor
	if anchor == "" {
		anchor = "null"
	}
	return fmt.Sprintf(`{"skeleton_id":%d,"signature":"ab12cd34","keep_vba":%t,`+
		`"workbook_inventory":{"has_vba":%t,"sheets":[{"name":"Claims","max_row":10,"max_column":3,`+
		`"state":"visible","is_empty":false,"used_max_row":4,"used_max_column":3,`+
		`"header_rows":{"1":["Policy","Premium"]},"merged_ranges":[]}]},`+
		`"fidelity_report":%s,"attached":%t,"reused_existing_skeleton":%t,"anchor_report":%s,`+
		`"future":9007199254740993}`, f.id, f.keepVBA, f.keepVBA, fidelity, f.attached, f.reused, anchor)
}

// text is what FormatTemplate renders for the answer.
func (f finalizedTemplate) text(t *testing.T) string {
	t.Helper()
	var resp api.SkeletonFinalizeResponse
	if err := json.Unmarshal([]byte(f.body()), &resp); err != nil {
		t.Fatal(err)
	}
	return deliverables.FormatTemplate(&deliverables.TemplateResult{SkeletonFinalizeResponse: &resp})
}

// mintedTemplateID is the template every upload URL the test server mints names.
const mintedTemplateID = 41

// templateTestServer is the deliverable server plus the template routes. Each
// upload URL it mints names mintedTemplateID and points at
// templateStoragePath, which records the bytes it is sent.
type templateTestServer struct {
	*deliverableTestServer
	uploadURLBodies []string
	finalizeBodies  []string
	stored          [][]byte
}

func templateServer(t *testing.T) *templateTestServer {
	t.Helper()
	s := &templateTestServer{deliverableTestServer: newDeliverableTestServer(t)}
	resetUploadTemplateFlags(t)
	for _, path := range []string{orgTemplateUploadURL, draftTemplateUploadURL} {
		s.RegisterHandler("PUT", strings.TrimPrefix(path, "PUT "), func(w http.ResponseWriter, r *http.Request) {
			checkDeliverableToken(t, r)
			s.uploadURLBodies = append(s.uploadURLBodies, readBody(t, r))
			testutil.RespondJSON(w, http.StatusOK, map[string]any{"url": s.URL() + templateStoragePath,
				"http_method": "PUT", "fields": nil, "key": "deliverable-skeletons/x.xlsx", "skeleton_id": mintedTemplateID})
		})
	}
	s.RegisterHandler("PUT", templateStoragePath, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("the storage upload carried Authorization %q", got)
		}
		s.stored = append(s.stored, []byte(readBody(t, r)))
		w.WriteHeader(http.StatusOK)
	})
	return s
}

func readBody(t *testing.T, r *http.Request) string {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// answerFinalize registers status and body as the answer to the finalize
// request path, a request log entry, and records each request body.
func (s *templateTestServer) answerFinalize(t *testing.T, path string, status int, body string) {
	t.Helper()
	s.RegisterHandler("POST", strings.TrimPrefix(path, "POST "), func(w http.ResponseWriter, r *http.Request) {
		checkDeliverableToken(t, r)
		s.finalizeBodies = append(s.finalizeBodies, readBody(t, r))
		testutil.RespondJSON(w, status, json.RawMessage(body))
	})
}

// assertJSONBodies checks that each request body decodes to the JSON in want.
func assertJSONBodies(t *testing.T, label string, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s bodies = %q, want %d", label, got, len(want))
	}
	for i := range got {
		assertSeriesJSON(t, got[i], want[i])
	}
}

func resetUploadTemplateFlags(t *testing.T) {
	t.Helper()
	cmd, _, err := GetRootCmd().Find([]string{"upload", "deliverable-template"})
	if err != nil {
		t.Fatal(err)
	}
	reset := func() {
		for _, name := range []string{"template-id", "acknowledge-lossy", "deliverable", "release", "revision"} {
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

// templateFile writes templateFileBytes to name in a new directory.
func templateFile(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, templateFileBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// runUploadTemplate executes the real tree and returns stdout and stderr apart.
func runUploadTemplate(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := GetRootCmd()
	var out, errOut bytes.Buffer
	cmd.SetArgs(append([]string{"upload", "deliverable-template"}, args...))
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

func uploadURLBody(size int) string {
	return fmt.Sprintf(`{"original_filename":"bordereau.xlsx","file_size":%d}`, size)
}

func finalizeBody(id int, ack bool) string {
	return fmt.Sprintf(`{"skeleton_id":%d,"acknowledge_lossy":%t}`, id, ack)
}

func TestUploadDeliverableTemplateFinalizesOnTheOrganization(t *testing.T) {
	for _, tc := range []struct {
		name   string
		answer finalizedTemplate
	}{
		{name: "minted", answer: finalizedTemplate{id: 41}},
		{name: "reused", answer: finalizedTemplate{id: 7, reused: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := templateServer(t)
			s.answerFinalize(t, orgTemplateFinalize, http.StatusOK, tc.answer.body())
			out, errOut, err := runUploadTemplate(t, templateFile(t, "bordereau.xlsx"))
			if err != nil {
				t.Fatal(err)
			}
			want := fmt.Sprintf("deliverable-template/%d finalized from bordereau.xlsx, not attached\n", tc.answer.id) +
				tc.answer.text(t) +
				fmt.Sprintf("Next: qctl upload deliverable-template --template-id %d --deliverable <deliverable>\n", tc.answer.id)
			if out != want || errOut != "" {
				t.Errorf("stdout =\n%s\nwant\n%s\nstderr = %q", out, want, errOut)
			}
			if got, want := s.Requests(), []string{orgTemplateUploadURL, templateStoragePUT, orgTemplateFinalize}; !reflect.DeepEqual(got, want) {
				t.Errorf("requests = %v, want %v", got, want)
			}
			assertJSONBodies(t, "upload-url", s.uploadURLBodies, uploadURLBody(len(templateFileBytes)))
			assertJSONBodies(t, "finalize", s.finalizeBodies, finalizeBody(41, false))
			if len(s.stored) != 1 || !bytes.Equal(s.stored[0], templateFileBytes) {
				t.Errorf("stored = %q, want %q", s.stored, templateFileBytes)
			}
		})
	}
}

func TestUploadDeliverableTemplateFormats(t *testing.T) {
	answer := finalizedTemplate{id: 41}
	for _, format := range []string{"json", "yaml", "name"} {
		t.Run(format, func(t *testing.T) {
			s := templateServer(t)
			s.answerFinalize(t, orgTemplateFinalize, http.StatusOK, answer.body())
			out, errOut, err := runUploadTemplate(t, templateFile(t, "bordereau.xlsx"), "-o", format)
			if err != nil {
				t.Fatal(err)
			}
			switch format {
			case "json":
				assertSeriesJSON(t, out, answer.body())
				if !strings.Contains(out, "9007199254740993") {
					t.Errorf("number not kept as sent: %s", out)
				}
			case "yaml":
				testutil.AssertYAMLReadsAsJSON(t, out, answer.body())
			case "name":
				if out != "deliverable-template/41\n" {
					t.Errorf("stdout = %q", out)
				}
			}
			if !strings.HasPrefix(errOut, "deliverable-template/41 finalized from bordereau.xlsx, not attached\n") ||
				!strings.HasSuffix(errOut, "Next: qctl upload deliverable-template --template-id 41 --deliverable <deliverable>\n") {
				t.Errorf("stderr = %q", errOut)
			}
			if strings.Contains(out, "Next:") || strings.Contains(out, "Signature:") {
				t.Errorf("prose on stdout: %q", out)
			}
		})
	}
}

func TestUploadDeliverableTemplateRefusesBeforeAnyRequest(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "folder.xlsx")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	file := templateFile(t, "bordereau.xlsx")
	for _, tc := range []struct {
		name string
		args []string
		code int
		says string
	}{
		{name: "csv file", args: []string{templateFile(t, "bordereau.csv")}, code: qerrors.ExitBadUsage, says: "must end in .xlsx or .xlsm"},
		{name: "xls file", args: []string{templateFile(t, "bordereau.xls")}, code: qerrors.ExitBadUsage, says: "must end in .xlsx or .xlsm"},
		{name: "neither file nor id", code: qerrors.ExitBadUsage, says: "give a template file"},
		{name: "file and id", args: []string{file, "--template-id", "7"}, code: qerrors.ExitBadUsage, says: "not both"},
		{name: "release without deliverable", args: []string{file, "--release", "2026.2"}, code: qerrors.ExitBadUsage, says: "add --deliverable"},
		{name: "revision without deliverable", args: []string{file, "--revision", draftRevisionID}, code: qerrors.ExitBadUsage, says: "add --deliverable"},
		{name: "empty release", args: []string{file, "--deliverable", "monthly-bordereau", "--release", ""}, code: qerrors.ExitBadUsage, says: "--release cannot be empty"},
		{name: "release and revision", args: []string{file, "--deliverable", "monthly-bordereau", "--release", "2026.2", "--revision", draftRevisionID},
			code: qerrors.ExitBadUsage, says: "mutually exclusive"},
		{name: "blank deliverable", args: []string{file, "--deliverable", " "}, code: qerrors.ExitBadUsage, says: "cannot be empty"},
		{name: "zero template id", args: []string{"--template-id", "0"}, code: qerrors.ExitBadUsage, says: "--template-id must be a positive integer"},
		{name: "foreign template reference", args: []string{"--template-id", "table/7"}, code: qerrors.ExitBadUsage, says: "deliverable-template"},
		{name: "directory", args: []string{dir}, code: qerrors.ExitBadUsage, says: "is a directory"},
		{name: "missing file", args: []string{filepath.Join(t.TempDir(), "missing.xlsx")}, code: qerrors.ExitGenericError, says: "failed to access template file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := templateServer(t)
			_, _, err := runUploadTemplate(t, tc.args...)
			if code := qerrors.GetExitCode(err); code != tc.code {
				t.Fatalf("exit = %d, want %d; error = %v", code, tc.code, err)
			}
			if !strings.Contains(err.Error(), tc.says) {
				t.Errorf("error = %q, want it to say %q", err, tc.says)
			}
			if got := s.Requests(); len(got) != 0 {
				t.Errorf("requests = %v, want none", got)
			}
		})
	}
}

func TestUploadDeliverableTemplateLossyRefusalNamesTheRerun(t *testing.T) {
	lossy := `{"detail":{"msg":"Generated files cannot keep: xl/media/image1.png; xl/charts/chart1.xml. ` +
		`Upload a template without them, or acknowledge the loss to continue.","error_code":"SKELETON_LOSSY",` +
		`"error_id":"","context":{"fidelity_report":` + lossyTemplateReport + `,"skeleton_id":7}}}`
	for _, tc := range []struct {
		name     string
		args     []string
		finalize string
		requests []string
		rerun    string
	}{
		{name: "organization", finalize: orgTemplateFinalize,
			requests: []string{orgTemplateUploadURL, templateStoragePUT, orgTemplateFinalize},
			rerun:    "qctl upload deliverable-template --template-id 7 --acknowledge-lossy"},
		{name: "draft", args: []string{"--deliverable", "monthly-bordereau"}, finalize: draftTemplateFinalize,
			requests: []string{deliverableRequest(), deliverableRequest(liveDeliverableID), deliverableRequest(liveDeliverableID, "revisions"),
				draftTemplateUploadURL, templateStoragePUT, draftTemplateFinalize},
			rerun: "qctl upload deliverable-template --template-id 7 --acknowledge-lossy --deliverable monthly-bordereau --release 2026.2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := templateServer(t)
			s.answerFinalize(t, tc.finalize, http.StatusConflict, lossy)
			out, _, err := runUploadTemplate(t, append([]string{templateFile(t, "bordereau.xlsx")}, tc.args...)...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit = %d, want %d; error = %v", code, qerrors.ExitBadUsage, err)
			}
			var refusal *deliverables.TemplateLossyError
			if !errors.As(err, &refusal) || refusal.TemplateID != 7 {
				t.Errorf("err = %#v, want the *TemplateLossyError for template 7", err)
			}
			text := err.Error()
			for _, want := range []string{"Generated files cannot keep", "\n  - 1 image on Claims\n  - 2 charts on Summary\n",
				"\nThe file is uploaded as deliverable-template/7, and this request attached nothing.\n"} {
				if !strings.Contains(text, want) {
					t.Errorf("error lacks %q:\n%s", want, text)
				}
			}
			if want := "\nTo accept the loss without uploading again:\n  " + tc.rerun; !strings.HasSuffix(text, want) {
				t.Errorf("error =\n%s\nwant it to end with%s", text, want)
			}
			if strings.Contains(text, "41") {
				t.Errorf("error names the minted template 41, not the refusal's 7:\n%s", text)
			}
			if out != "" {
				t.Errorf("stdout = %q", out)
			}
			if got := s.Requests(); !reflect.DeepEqual(got, tc.requests) {
				t.Errorf("requests = %v, want %v", got, tc.requests)
			}
			assertJSONBodies(t, "finalize", s.finalizeBodies, finalizeBody(41, false))
		})
	}
}

func TestUploadDeliverableTemplateFinalizesAnUploadedTemplateAgain(t *testing.T) {
	s := templateServer(t)
	answer := finalizedTemplate{id: 7, lossy: true}
	s.answerFinalize(t, orgTemplateFinalize, http.StatusOK, answer.body())
	out, errOut, err := runUploadTemplate(t, "--template-id", "deliverable-template/7", "--acknowledge-lossy")
	if err != nil {
		t.Fatal(err)
	}
	want := "deliverable-template/7 finalized, not attached\n" + answer.text(t) +
		"Next: qctl upload deliverable-template --template-id 7 --deliverable <deliverable> --acknowledge-lossy\n"
	if out != want || errOut != "" {
		t.Errorf("stdout =\n%s\nwant\n%s\nstderr = %q", out, want, errOut)
	}
	if got, want := s.Requests(), []string{orgTemplateFinalize}; !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %v, want %v", got, want)
	}
	assertJSONBodies(t, "finalize", s.finalizeBodies, finalizeBody(7, true))
}

func TestUploadDeliverableTemplateAttachesToTheDraft(t *testing.T) {
	answer := finalizedTemplate{id: 41, attached: true, anchor: templateAnchorReport}
	for _, tc := range []struct {
		name     string
		selector []string
	}{
		{name: "only draft"},
		{name: "revision reference", selector: []string{"--revision", "deliverable-revision/" + draftRevisionID}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := templateServer(t)
			s.answerFinalize(t, draftTemplateFinalize, http.StatusOK, answer.body())
			args := append([]string{templateFile(t, "bordereau.xlsx"), "--deliverable", "monthly-bordereau"}, tc.selector...)
			out, errOut, err := runUploadTemplate(t, args...)
			if err != nil {
				t.Fatal(err)
			}
			want := `deliverable-template/41 attached to deliverable/monthly-bordereau release "2026.2"` + "\n" + answer.text(t) +
				"Next: qctl describe deliverable-revision monthly-bordereau --release 2026.2\n"
			if out != want || errOut != "" {
				t.Errorf("stdout =\n%s\nwant\n%s\nstderr = %q", out, want, errOut)
			}
			for _, line := range []string{"Re-anchored bindings (2):\n",
				"  Column Claims!B -> C: moved; label: Premium [20000000-0000-4000-8000-000000000001]\n",
				"  Cell Summary!C4: broken (label_missing); label: Total [30000000-0000-4000-8000-000000000001]\n"} {
				if !strings.Contains(out, line) {
					t.Errorf("stdout lacks %q", line)
				}
			}
			wantRequests := []string{deliverableRequest(), deliverableRequest(liveDeliverableID), deliverableRequest(liveDeliverableID, "revisions"),
				draftTemplateUploadURL, templateStoragePUT, draftTemplateFinalize}
			if got := s.Requests(); !reflect.DeepEqual(got, wantRequests) {
				t.Errorf("requests = %v, want %v", got, wantRequests)
			}
			if got := s.listQueries[0].Get("include_archived"); got != "true" {
				t.Errorf("include_archived = %q; the resolver must reach the trash", got)
			}
			assertJSONBodies(t, "upload-url", s.uploadURLBodies, uploadURLBody(len(templateFileBytes)))
			assertJSONBodies(t, "finalize", s.finalizeBodies, finalizeBody(41, false))
		})
	}
}

func TestUploadDeliverableTemplateRefusesARevisionThatIsNotADraft(t *testing.T) {
	s := templateServer(t)
	_, _, err := runUploadTemplate(t, templateFile(t, "bordereau.xlsx"), "--deliverable", "monthly-bordereau", "--release", "2026.1")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Fatalf("exit = %d, want %d; error = %v", code, qerrors.ExitBadUsage, err)
	}
	if want := `deliverable/monthly-bordereau release "2026.1" is enabled; only a draft revision's template can change`; err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}
	want := []string{deliverableRequest(), deliverableRequest(liveDeliverableID), deliverableRequest(liveDeliverableID, "revisions")}
	if got := s.Requests(); !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %v, want %v", got, want)
	}
}

// answerRevisions registers revisions as the live deliverable's revision list.
func (s *templateTestServer) answerRevisions(t *testing.T, revisions string) {
	t.Helper()
	s.RegisterHandler("GET", deliverableTestPath+"/"+liveDeliverableID+"/revisions", func(w http.ResponseWriter, r *http.Request) {
		checkDeliverableToken(t, r)
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(revisions))
	})
}

func TestUploadDeliverableTemplateWithNoDraftAdvisesAnUnattachedUpload(t *testing.T) {
	s := templateServer(t)
	s.answerRevisions(t, strings.ReplaceAll(liveDeliverableRevisions, `"state":"draft"`, `"state":"disabled"`))
	_, _, err := runUploadTemplate(t, templateFile(t, "bordereau.xlsx"), "--deliverable", "monthly-bordereau")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Fatalf("exit = %d, want %d; error = %v", code, qerrors.ExitBadUsage, err)
	}
	want := "deliverable " + liveDeliverableID + " has no draft revision; a template attaches only to a draft, " +
		"so upload it without --deliverable and attach it with --template-id once a draft exists; " +
		"use 'qctl get deliverable-revisions " + liveDeliverableID + "'"
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}
	wantRequests := []string{deliverableRequest(), deliverableRequest(liveDeliverableID), deliverableRequest(liveDeliverableID, "revisions")}
	if got := s.Requests(); !reflect.DeepEqual(got, wantRequests) {
		t.Errorf("requests = %v, want %v", got, wantRequests)
	}
}

func TestUploadDeliverableTemplateCommandLinesNameTheDraftSafely(t *testing.T) {
	lossy := `{"detail":{"msg":"Generated files cannot keep: xl/media/image1.png.","error_code":"SKELETON_LOSSY",` +
		`"error_id":"","context":{"fidelity_report":` + lossyTemplateReport + `,"skeleton_id":7}}}`
	for _, tc := range []struct {
		name, release, selector string
	}{
		{name: "label to quote", release: `Q3 '26`, selector: `'Q3 '\''26'`},
		{name: "label the terminal escapes", release: "2026\x1b2", selector: draftRevisionID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			label, err := json.Marshal(tc.release)
			if err != nil {
				t.Fatal(err)
			}
			revisions := strings.ReplaceAll(liveDeliverableRevisions, `"release":"2026.2"`, `"release":`+string(label))

			s := templateServer(t)
			s.answerRevisions(t, revisions)
			s.answerFinalize(t, draftTemplateFinalize, http.StatusConflict, lossy)
			_, _, err = runUploadTemplate(t, templateFile(t, "bordereau.xlsx"), "--deliverable", "monthly-bordereau")
			rerun := "qctl upload deliverable-template --template-id 7 --acknowledge-lossy --deliverable monthly-bordereau --release " + tc.selector
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage || !strings.HasSuffix(err.Error(), "\n  "+rerun) {
				t.Errorf("exit = %d, error =\n%v\nwant it to end with %s", code, err, rerun)
			}

			s = templateServer(t)
			s.answerRevisions(t, revisions)
			s.answerFinalize(t, draftTemplateFinalize, http.StatusOK, finalizedTemplate{id: 41, attached: true}.body())
			out, _, err := runUploadTemplate(t, templateFile(t, "bordereau.xlsx"), "--deliverable", "monthly-bordereau")
			if err != nil {
				t.Fatal(err)
			}
			if next := "Next: qctl describe deliverable-revision monthly-bordereau --release " + tc.selector + "\n"; !strings.HasSuffix(out, next) {
				t.Errorf("stdout =\n%s\nwant it to end with %s", out, next)
			}
		})
	}
}

func TestUploadDeliverableTemplateNotesAReusedVBASetting(t *testing.T) {
	for _, tc := range []struct {
		name   string
		file   string
		answer finalizedTemplate
		note   string
	}{
		{name: "xlsm reuses a template without VBA", file: "macro.xlsm", answer: finalizedTemplate{id: 7, reused: true},
			note: "Note: an identical workbook was uploaded earlier, and this template keeps that upload's VBA setting, " +
				"so VBA kept is false although the file ends in .xlsm.\n"},
		{name: "xlsx reuses a template with VBA", file: "bordereau.XLSX", answer: finalizedTemplate{id: 7, reused: true, keepVBA: true},
			note: "Note: an identical workbook was uploaded earlier, and this template keeps that upload's VBA setting, " +
				"so VBA kept is true although the file ends in .xlsx.\n"},
		{name: "xlsm reuses a template with VBA", file: "macro.xlsm", answer: finalizedTemplate{id: 7, reused: true, keepVBA: true}},
		{name: "xlsm minted without reuse", file: "macro.xlsm", answer: finalizedTemplate{id: 41}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := templateServer(t)
			s.answerFinalize(t, orgTemplateFinalize, http.StatusOK, tc.answer.body())
			out, _, err := runUploadTemplate(t, templateFile(t, tc.file))
			if err != nil {
				t.Fatal(err)
			}
			want := fmt.Sprintf("deliverable-template/%d finalized from %s, not attached\n", tc.answer.id, tc.file) +
				tc.answer.text(t) + tc.note +
				fmt.Sprintf("Next: qctl upload deliverable-template --template-id %d --deliverable <deliverable>\n", tc.answer.id)
			if out != want {
				t.Errorf("stdout =\n%s\nwant\n%s", out, want)
			}
		})
	}
}

func TestUploadDeliverableTemplateFinalizeFailureAfterTheUpload(t *testing.T) {
	invalid := `{"detail":{"msg":"Uploaded skeleton is not a valid workbook: File is not a zip file",` +
		`"error_code":"SKELETON_INVALID","error_id":"0A1B2C3D"}}`
	for _, tc := range []struct {
		name     string
		args     []string
		finalize string
		status   int
		body     string
		code     int
		hint     string
	}{
		{name: "server error", finalize: orgTemplateFinalize, status: http.StatusInternalServerError, body: `{"detail":"boom"}`,
			code: qerrors.ExitGenericError,
			hint: "\nThe file is uploaded as deliverable-template/41. To finalize it again without uploading:\n" +
				"  qctl upload deliverable-template --template-id 41"},
		{name: "server error on a draft", args: []string{"--acknowledge-lossy", "--deliverable", "monthly-bordereau"},
			finalize: draftTemplateFinalize, status: http.StatusBadGateway, body: `{"detail":"bad gateway"}`,
			code: qerrors.ExitGenericError,
			hint: "\nThe file is uploaded as deliverable-template/41. To finalize it again without uploading:\n" +
				"  qctl upload deliverable-template --template-id 41 --acknowledge-lossy --deliverable monthly-bordereau --release 2026.2"},
		{name: "unauthorized", finalize: orgTemplateFinalize, status: http.StatusUnauthorized, body: `{"detail":"Not authenticated"}`,
			code: qerrors.ExitUnauthorized,
			hint: "\nThe file is uploaded as deliverable-template/41. To finalize it again without uploading:\n" +
				"  qctl upload deliverable-template --template-id 41"},
		{name: "invalid workbook", finalize: orgTemplateFinalize, status: http.StatusUnprocessableEntity, body: invalid,
			code: qerrors.ExitBadUsage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := templateServer(t)
			s.answerFinalize(t, tc.finalize, tc.status, tc.body)
			_, _, err := runUploadTemplate(t, append([]string{templateFile(t, "bordereau.xlsx")}, tc.args...)...)
			if code := qerrors.GetExitCode(err); code != tc.code {
				t.Fatalf("exit = %d, want %d; error = %v", code, tc.code, err)
			}
			text := err.Error()
			if tc.hint == "" {
				if strings.Contains(text, "--template-id") || strings.Contains(text, "is uploaded as") {
					t.Errorf("a refusal re-finalizing cannot fix carries a re-run hint:\n%s", text)
				}
				if !strings.Contains(text, "not a valid workbook") {
					t.Errorf("error = %q", text)
				}
			} else if !strings.HasSuffix(text, tc.hint) {
				t.Errorf("error =\n%s\nwant it to end with%s", text, tc.hint)
			}
			if len(s.finalizeBodies) != 1 || len(s.stored) != 1 {
				t.Errorf("finalize bodies = %q, stored = %d; want one upload and one finalize", s.finalizeBodies, len(s.stored))
			}
		})
	}
}
