package root

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/deliverables"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const (
	newDeliverableID = "0c0c0c0c-2222-4333-8444-999999999999"
	newRevisionID    = "f3f3f3f3-2222-4333-8444-aaaaaaaaaaaa"

	deliverableManifestHead = "apiVersion: qluster.ai/v1\nkind: Deliverable\nmetadata:\n  slug: "

	// liveDeliverableSpec is the live deliverable's basic fields.
	liveDeliverableSpec = "spec:\n  name: Monthly bordereau\n  receiver_label: Acme Re\n" +
		"  filename_pattern: \"bordereau-{period}.xlsx\"\n  period_param: period\n"

	// newDeliverableManifest creates quarterly-claims with release v1 from
	// the template next to the manifest.
	newDeliverableManifest = deliverableManifestHead + "quarterly-claims\nspec:\n  name: Quarterly claims\n" +
		"  filename_pattern: \"claims-{period}.xlsx\"\n  release: v1\n  template: templates/claims.xlsx\n"

	newDeliverableRecord = `{"id":"` + newDeliverableID + `","organization_id":"550e8400-e29b-41d4-a716-446655440000",` +
		`"name":"Quarterly claims","slug":"quarterly-claims","receiver_label":"","filename_pattern":"claims-{period}.xlsx",` +
		`"period_param":null,"archived_at":null,"enabled_revision_id":null,` +
		`"created_at":"2026-10-08T12:00:00Z","updated_at":"2026-10-08T12:00:00Z"}`
)

// createdRevisionRecord is the answer to a revision create for release on
// deliverable.
func createdRevisionRecord(deliverable, release string) string {
	return `{"id":"` + newRevisionID + `","deliverable_id":"` + deliverable + `","release":"` + release + `",` +
		`"state":"draft","description":null,"skeleton_id":41,"enabled_by_id":null,"enabled_at":null,` +
		`"sheet_bindings":[],"cell_bindings":[],"anchor_report":null}`
}

// manifestServer is the template server with the deliverable writes apply
// sends, each recording the last body it received.
type manifestServer struct {
	*templateTestServer
	bodies map[string]string
}

// deliverableManifestServer serves the deliverable reads and writes, with
// apply's flags reset and the current context allowlisted.
func deliverableManifestServer(t *testing.T) *manifestServer {
	t.Helper()
	s := &manifestServer{templateTestServer: templateServer(t), bodies: map[string]string{}}
	resetSeriesWriteFlags(t)
	t.Cleanup(func() { resetSeriesWriteFlags(t) })
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	allow := false
	cfg.Contexts[cfg.CurrentContext].Protected = &allow
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	s.answerFinalize(t, orgTemplateFinalize, http.StatusOK, finalizedTemplate{id: 41}.body())
	s.answer(t, "POST", deliverableTestPath, newDeliverableRecord)
	s.answer(t, "POST", deliverableTestPath+"/"+newDeliverableID+"/revisions", createdRevisionRecord(newDeliverableID, "v1"))
	s.answer(t, "PATCH", deliverableTestPath+"/"+liveDeliverableID,
		strings.Replace(liveDeliverableRecord, `"receiver_label":"Acme Re"`, `"receiver_label":"Acme Reinsurance"`, 1))
	s.answer(t, "POST", deliverableTestPath+"/"+liveDeliverableID+"/revisions", createdRevisionRecord(liveDeliverableID, "2026.3"))
	// A created release has no bindings.
	answerRevisionTree(t, s.MockAPIServer, newRevisionID, bareRevisionTree(newRevisionID, "v1", "draft"))
	s.RegisterHandler(http.MethodGet, "/api/orgs/"+scopeTestOrgID+"/datasets/42/migration-details", func(w http.ResponseWriter, r *http.Request) {
		checkDeliverableToken(t, r)
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"fields":{"policy":{"current":{}},"premium":{"current":{}},"claims":{"current":{}}}}`))
	})
	return s
}

// answer registers body as the 200 answer to method on path.
func (s *manifestServer) answer(t *testing.T, method, path, body string) {
	t.Helper()
	s.RegisterHandler(method, path, func(w http.ResponseWriter, r *http.Request) {
		checkDeliverableToken(t, r)
		s.bodies[method+" "+path] = readBody(t, r)
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(body))
	})
}

// writes returns every request other than a GET or a row filter count.
func (s *manifestServer) writes() []string {
	var out []string
	for _, r := range s.Requests() {
		if !strings.HasPrefix(r, "GET ") && !strings.HasSuffix(r, rowFilterCountRoute) {
			out = append(out, r)
		}
	}
	return out
}

// counts returns every row filter count request.
func (s *manifestServer) counts() []string {
	var out []string
	for _, r := range s.Requests() {
		if strings.HasSuffix(r, rowFilterCountRoute) {
			out = append(out, r)
		}
	}
	return out
}

// writeManifest writes content to name under dir and returns its path.
func writeManifest(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// runManifest executes the real tree and returns stdout and stderr apart.
func runManifest(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := GetRootCmd()
	var out, errOut bytes.Buffer
	cmd.SetArgs(args)
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

// mustParseDeliverable parses out as a Deliverable manifest.
func mustParseDeliverable(t *testing.T, out string) *deliverables.Manifest {
	t.Helper()
	m, err := deliverables.ParseManifest([]byte(out))
	if err != nil {
		t.Fatalf("ParseManifest: %v\n%s", err, out)
	}
	return m
}

func TestApplyDeliverableCreatesItAndItsReleaseFromTheTemplate(t *testing.T) {
	s := deliverableManifestServer(t)
	dir := t.TempDir()
	writeManifest(t, dir, "templates/claims.xlsx", string(templateFileBytes))
	path := writeManifest(t, dir, "deliverable.yaml", newDeliverableManifest)

	out, errOut, err := runManifest(t, "apply", "-f", path)
	if err != nil {
		t.Fatal(err)
	}
	want := "deliverable/quarterly-claims created\n" +
		`release "v1" created as a draft (from scratch)` + "\n" +
		"template attached: deliverable-template/41 (new upload)\n"
	if out != want || errOut != "" {
		t.Errorf("stdout =\n%s\nwant\n%s\nstderr = %q", out, want, errOut)
	}
	wantRequests := []string{deliverableRequest(), orgTemplateUploadURL, templateStoragePUT, orgTemplateFinalize,
		"POST " + deliverableTestPath, "POST " + deliverableTestPath + "/" + newDeliverableID + "/revisions"}
	// Reads of the created release's binding tree are left out: this test
	// does not pin how many the result's export takes.
	got := slices.DeleteFunc(s.Requests(), func(r string) bool { return r == "GET "+revisionTestPath+newRevisionID })
	if !reflect.DeepEqual(got, wantRequests) {
		t.Errorf("requests = %v, want %v", got, wantRequests)
	}
	if query := s.listQueries[0]; query.Get("search_query") != "quarterly-claims" || query.Get("include_archived") != "true" {
		t.Errorf("slug lookup query = %v", query)
	}
	assertJSONBodies(t, "upload-url", s.uploadURLBodies, `{"original_filename":"claims.xlsx","file_size":`+strconv.Itoa(len(templateFileBytes))+`}`)
	assertJSONBodies(t, "finalize", s.finalizeBodies, finalizeBody(41, false))
	if len(s.stored) != 1 || !bytes.Equal(s.stored[0], templateFileBytes) {
		t.Errorf("stored = %q", s.stored)
	}
	assertSeriesJSON(t, s.bodies["POST "+deliverableTestPath], `{"name":"Quarterly claims","filename_pattern":"claims-{period}.xlsx"}`)
	assertSeriesJSON(t, s.bodies["POST "+deliverableTestPath+"/"+newDeliverableID+"/revisions"],
		`{"release":"v1","skeleton_id":41,"acknowledge_lossy":false}`)
}

func TestApplyDeliverableStoppedAfterTheCreatePrintsWhatItWrote(t *testing.T) {
	s := deliverableManifestServer(t)
	s.RegisterHandler("POST", deliverableTestPath+"/"+newDeliverableID+"/revisions", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusConflict, json.RawMessage(`{"detail":{"msg":"Release 'v1' already exists.","error_id":""}}`))
	})
	dir := t.TempDir()
	writeManifest(t, dir, "templates/claims.xlsx", string(templateFileBytes))
	path := writeManifest(t, dir, "deliverable.yaml", newDeliverableManifest)

	out, _, err := runManifest(t, "apply", "-f", path, "-o", "yaml")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Fatalf("exit = %d, want %d; error = %v", code, qerrors.ExitBadUsage, err)
	}
	m := mustParseDeliverable(t, out)
	if m.Metadata.Slug != "quarterly-claims" || m.Has("release") {
		t.Errorf("stdout manifest = %+v, want the created deliverable without the failed release", m)
	}
}

func TestApplyDeliverableUpdatesAnExistingOne(t *testing.T) {
	revisionsPath := deliverableTestPath + "/" + liveDeliverableID + "/revisions"
	for _, tc := range []struct {
		name     string
		spec     string
		writes   []string
		body     string
		human    string
		release  string
		receiver string
	}{
		{name: "basic field",
			spec:   strings.Replace(liveDeliverableSpec, "Acme Re", "Acme Reinsurance", 1),
			writes: []string{"PATCH " + deliverableTestPath + "/" + liveDeliverableID},
			body: `{"name":"Monthly bordereau","receiver_label":"Acme Reinsurance",` +
				`"filename_pattern":"bordereau-{period}.xlsx","period_param":"period"}`,
			human:    "deliverable/monthly-bordereau updated\n",
			receiver: "Acme Reinsurance"},
		// Adding a release to an existing deliverable is an update of it.
		{name: "release only",
			spec:     liveDeliverableSpec + "  release: \"2026.3\"\n",
			writes:   []string{"POST " + revisionsPath},
			body:     `{"release":"2026.3","copy_from_revision_id":"` + enabledRevisionID + `"}`,
			human:    "deliverable/monthly-bordereau updated\n" + `release "2026.3" created as a draft (copied from "2026.1")` + "\n",
			release:  "2026.3",
			receiver: "Acme Re"},
	} {
		for _, format := range []string{"table", "yaml"} {
			t.Run(tc.name+"/"+format, func(t *testing.T) {
				s := deliverableManifestServer(t)
				path := writeManifest(t, t.TempDir(), "deliverable.yaml", deliverableManifestHead+"monthly-bordereau\n"+tc.spec)
				out, errOut, err := runManifest(t, "apply", "-f", path, "-o", format)
				if err != nil {
					t.Fatal(err)
				}
				if got := s.writes(); !reflect.DeepEqual(got, tc.writes) {
					t.Fatalf("writes = %v, want %v", got, tc.writes)
				}
				assertSeriesJSON(t, s.bodies[tc.writes[0]], tc.body)
				if format == "table" {
					if out != tc.human || errOut != "" {
						t.Errorf("stdout = %q, want %q; stderr = %q", out, tc.human, errOut)
					}
					return
				}
				if errOut != tc.human {
					t.Errorf("stderr = %q, want %q", errOut, tc.human)
				}
				m := mustParseDeliverable(t, out)
				if m.Spec.Release != tc.release || *m.Spec.ReceiverLabel != tc.receiver || m.Metadata.Slug != "monthly-bordereau" {
					t.Errorf("result manifest = %+v", m.Spec)
				}
			})
		}
	}
}

func TestApplyDeliverableUnchangedPrintsTheTemplateNote(t *testing.T) {
	s := deliverableManifestServer(t)
	dir := t.TempDir()
	path := writeManifest(t, dir, "deliverable.yaml", deliverableManifestHead+"monthly-bordereau\n"+liveDeliverableSpec+
		"  release: \"2026.2\"\n  template: claims.xlsx\n")
	out, errOut, err := runManifest(t, "apply", "-f", path)
	if err != nil {
		t.Fatal(err)
	}
	want := "deliverable/monthly-bordereau unchanged\n" +
		`Note: spec.template is not compared with the template of draft release "2026.2"; to replace it: ` +
		"qctl upload deliverable-template " + cmdutil.ShellArg(filepath.Join(dir, "claims.xlsx")) +
		" --deliverable monthly-bordereau --release 2026.2\n"
	if out != want || errOut != "" {
		t.Errorf("stdout =\n%s\nwant\n%s\nstderr = %q", out, want, errOut)
	}
	// The table view prints no export, so the release's tree is not read.
	wantRequests := []string{deliverableRequest(), deliverableRequest(liveDeliverableID), deliverableRequest(liveDeliverableID, "revisions")}
	if got := s.Requests(); !reflect.DeepEqual(got, wantRequests) {
		t.Errorf("requests = %v, want %v", got, wantRequests)
	}
}

func TestDiffDeliverableUnchangedPrintsTheTemplateNoteOnStderr(t *testing.T) {
	deliverableManifestServer(t)
	dir := t.TempDir()
	path := writeManifest(t, dir, "deliverable.yaml", deliverableManifestHead+"monthly-bordereau\n"+liveDeliverableSpec+
		"  release: \"2026.2\"\n  template: claims.xlsx\n")
	out, errOut, err := runManifest(t, "diff", "-f", path)
	if code := qerrors.GetExitCode(err); err != nil {
		t.Fatalf("exit = %d, want 0; error = %v", code, err)
	}
	want := `Note: spec.template is not compared with the template of draft release "2026.2"; to replace it: ` +
		"qctl upload deliverable-template " + cmdutil.ShellArg(filepath.Join(dir, "claims.xlsx")) +
		" --deliverable monthly-bordereau --release 2026.2\n"
	if out != "" || errOut != want {
		t.Errorf("stdout = %q, want empty; stderr =\n%s\nwant\n%s", out, errOut, want)
	}
}

func TestDeliverableManifestOnATrashedDeliverableExitsTwoBeforeAnyWrite(t *testing.T) {
	for _, verb := range []string{"apply", "diff"} {
		t.Run(verb, func(t *testing.T) {
			s := deliverableManifestServer(t)
			path := writeManifest(t, t.TempDir(), "deliverable.yaml", deliverableManifestHead+
				"weekly-claims\nspec:\n  name: Weekly claims\n  filename_pattern: claims.xlsx\n")
			out, _, err := runManifest(t, verb, "-f", path)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit = %d, want %d; error = %v", code, qerrors.ExitBadUsage, err)
			}
			if out != "" {
				t.Errorf("stdout = %q", out)
			}
			if got := s.Requests(); !reflect.DeepEqual(got, []string{deliverableRequest()}) {
				t.Errorf("requests = %v, want only the slug lookup", got)
			}
		})
	}
}

func TestDiffDeliverablePrintsTheRequestsAndExitsOne(t *testing.T) {
	for _, tc := range []struct {
		name     string
		manifest string
		says     []string
	}{
		{name: "patch",
			manifest: deliverableManifestHead + "monthly-bordereau\n" + strings.Replace(liveDeliverableSpec, "Acme Re", "Acme Reinsurance", 1),
			says: []string{`receiver_label: "Acme Re" → "Acme Reinsurance"` + "\nPATCH /deliverables/" + liveDeliverableID + "\n" +
				"# takes effect immediately: a deliverable's basic fields are not versioned\n"}},
		{name: "create with a template",
			manifest: newDeliverableManifest,
			says: []string{"PUT /deliverable-skeletons/upload-url\n", "POST /deliverable-skeletons/finalize\n", "POST /deliverables\n",
				"POST /deliverables/<created-deliverable-id>/revisions\n", `"skeleton_id": "<uploaded-template-id>"`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := deliverableManifestServer(t)
			dir := t.TempDir()
			writeManifest(t, dir, "templates/claims.xlsx", string(templateFileBytes))
			path := writeManifest(t, dir, "deliverable.yaml", tc.manifest)
			out, errOut, err := runManifest(t, "diff", "-f", path)
			if code := qerrors.GetExitCode(err); code != 1 {
				t.Fatalf("exit = %d, want 1; error = %v", code, err)
			}
			if errOut != "" {
				t.Errorf("stderr = %q", errOut)
			}
			for _, want := range tc.says {
				if !strings.Contains(out, want) {
					t.Errorf("stdout lacks %q:\n%s", want, out)
				}
			}
			if got := s.writes(); len(got) != 0 || len(s.stored) != 0 {
				t.Errorf("diff wrote %v and stored %d files", got, len(s.stored))
			}
		})
	}
}

func TestGetDeliverableExportAppliesUnchangedAndDiffsClean(t *testing.T) {
	for _, format := range []string{"yaml", "json"} {
		t.Run(format, func(t *testing.T) {
			s := deliverableManifestServer(t)
			exported, _, err := runManifest(t, "get", "deliverable", "monthly-bordereau", "-o", format)
			if err != nil {
				t.Fatal(err)
			}
			m := mustParseDeliverable(t, exported)
			if m.Metadata.Slug != "monthly-bordereau" || m.Spec.Release != "2026.1" || m.Spec.Template != "" {
				t.Errorf("export = %+v", m)
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

// A template path in a multi-document file is relative to the file's
// directory.
func TestApplyDeliverableInAMultiDocumentFile(t *testing.T) {
	s := deliverableManifestServer(t)
	dir := t.TempDir()
	writeManifest(t, dir, "templates/claims.xlsx", string(templateFileBytes))
	path := writeManifest(t, dir, "deliverables.yaml",
		deliverableManifestHead+"monthly-bordereau\n"+liveDeliverableSpec+"---\n"+newDeliverableManifest)
	out, _, err := runManifest(t, "apply", "-f", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"deliverable/monthly-bordereau unchanged\n", "deliverable/quarterly-claims created\n",
		"template attached: deliverable-template/41 (new upload)\n", "2 documents: 2 succeeded"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
	if len(s.stored) != 1 || !bytes.Equal(s.stored[0], templateFileBytes) {
		t.Errorf("stored = %q", s.stored)
	}
	want := []string{orgTemplateUploadURL, templateStoragePUT, orgTemplateFinalize,
		"POST " + deliverableTestPath, "POST " + deliverableTestPath + "/" + newDeliverableID + "/revisions"}
	if got := s.writes(); !reflect.DeepEqual(got, want) {
		t.Errorf("writes = %v, want %v", got, want)
	}
}
