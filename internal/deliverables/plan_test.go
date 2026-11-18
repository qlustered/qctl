package deliverables

import (
	"encoding/json"
	"errors"
	"io/fs"
	"maps"
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
	planItemPath     = testPath + "/" + monthlyID
	planMintPath     = skeletonsPath + "/upload-url"
	planFinalizePath = skeletonsPath + "/finalize"
	planMinted       = `{"url":"http://mock.local` + putTargetPath + `","http_method":"PUT","key":"k","skeleton_id":41}`
	planListing      = "GET " + testPath
	planRead         = "GET " + planItemPath
	planRevisions    = "GET " + revisionsPath
	planTrashed      = "2026-10-02T12:00:00Z"
)

// planDeliverable is deliverable carrier-a-bordereau with changes applied.
func planDeliverable(changes map[string]any) map[string]any {
	item := map[string]any{"id": monthlyID, "organization_id": testOrgID, "name": "Carrier A bordereau",
		"slug": "carrier-a-bordereau", "receiver_label": "Carrier A", "filename_pattern": "CarrierA_{period}.xlsx",
		"period_param": "period", "archived_at": nil, "enabled_revision_id": nil, "created_at": nil, "updated_at": nil}
	maps.Copy(item, changes)
	return item
}

// planRevision is revision with changes applied.
func planRevision(revision map[string]any, changes map[string]any) map[string]any {
	out := maps.Clone(revision)
	maps.Copy(out, changes)
	return out
}

// planBackend answers the reads Prepare makes and the writes Apply makes,
// and keeps the last body each write route received.
type planBackend struct {
	mock   *testutil.MockAPIServer
	bodies map[string]map[string]json.RawMessage
}

// servePlan lists current, when it is not nil, under its slug, and lists
// revisions as its revisions.
func servePlan(t *testing.T, current map[string]any, revisions ...map[string]any) *planBackend {
	t.Helper()
	b := &planBackend{mock: testutil.NewMockAPIServer(), bodies: map[string]map[string]json.RawMessage{}}
	t.Cleanup(b.mock.Close)
	listed := []map[string]any{}
	if current != nil {
		listed = append(listed, current)
		b.mock.RegisterHandler(http.MethodGet, planItemPath, func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, current)
		})
	}
	b.mock.RegisterHandler(http.MethodGet, testPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]any{"results": listed, "total_rows": len(listed), "page": 1})
	})
	if revisions == nil {
		revisions = []map[string]any{}
	}
	b.mock.RegisterHandler(http.MethodGet, revisionsPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, revisions)
	})
	for _, id := range []int{5, 6} {
		b.mock.RegisterHandler(http.MethodGet, bpDatasets+"/"+strconv.Itoa(id)+"/migration-details", func(w http.ResponseWriter, _ *http.Request) {
			fields := map[string]api.FieldMigrationInfo{}
			for _, name := range []string{"policy_id", "amount", "claim_id", "carrier", "period", "period_label"} {
				fields[name] = api.FieldMigrationInfo{Current: &api.FieldDatabaseDetailDict{}}
			}
			testutil.RespondJSON(w, http.StatusOK, api.MigrationDetailResponse{Fields: fields})
		})
	}
	// The trees Apply reads back: the created release's, and each listed one's.
	b.mock.RegisterHandler(http.MethodGet, bpTreePath+revV4, func(w http.ResponseWriter, _ *http.Request) {
		respondBody(w, http.StatusOK, createdRevision)
	})
	for _, revision := range revisions {
		tree := planRevision(revision, map[string]any{"deliverable_id": monthlyID, "sheet_bindings": []any{}, "cell_bindings": []any{}})
		b.mock.RegisterHandler(http.MethodGet, bpTreePath+revision["id"].(string), func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, tree)
		})
	}
	b.answer(t, http.MethodPut, planMintPath, http.StatusOK, planMinted)
	b.answer(t, http.MethodPut, putTargetPath, http.StatusOK, "")
	b.answer(t, http.MethodPost, planFinalizePath, http.StatusOK, finalizedTemplate)
	b.answer(t, http.MethodPost, testPath, http.StatusOK, writtenDeliverable)
	b.answer(t, http.MethodPatch, planItemPath, http.StatusOK, writtenDeliverable)
	b.answer(t, http.MethodPost, revisionsPath, http.StatusOK, createdRevision)
	return b
}

// answer responds to method and path with status and body, recording a JSON
// request body.
func (b *planBackend) answer(t *testing.T, method, path string, status int, body string) {
	b.mock.RegisterHandler(method, path, func(w http.ResponseWriter, r *http.Request) {
		if path != putTargetPath {
			b.bodies[method+" "+path] = recordJSON(t, r)
		}
		respondBody(w, status, body)
	})
}

func (b *planBackend) client() *Client { return NewClient(b.mock.URL(), testOrgID, 0) }

// writes returns every request other than a GET.
func (b *planBackend) writes() []string {
	var out []string
	for _, request := range b.mock.Requests() {
		if !strings.HasPrefix(request, http.MethodGet+" ") {
			out = append(out, request)
		}
	}
	return out
}

// planManifest parses the carrier-a-bordereau manifest with spec.
func planManifest(t *testing.T, spec string) *Manifest {
	t.Helper()
	m, err := ParseManifest([]byte(deliverableManifestHeader + spec))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	return m
}

// planLines returns each request as "METHOD path".
func planLines(p *Plan) []string {
	var out []string
	for _, request := range p.Requests {
		out = append(out, request.Method+" "+request.Path)
	}
	return out
}

// planBody returns body as compact JSON with sorted keys and unescaped < and >.
func planBody(t *testing.T, body map[string]any) string {
	t.Helper()
	var out strings.Builder
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(body); err != nil {
		t.Fatal(err)
	}
	return strings.TrimSuffix(out.String(), "\n")
}

// planWorkbook writes an 8-byte carrier-a.xlsx and returns its directory.
func planWorkbook(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "carrier-a.xlsx"), []byte("workbook"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestPrepareCreatesADeliverableWhoseNameGeneratesItsSlug(t *testing.T) {
	longName := "Carrier A bordereau for the September 2026 quarterly reporting cycle"
	for _, tc := range []struct {
		name string
		slug string
		spec string
		want string
	}{
		{name: "unwritten receiver_label and period_param are left out", spec: deliverableManifestBasics,
			want: `{"filename_pattern":"CarrierA_{period}.xlsx","name":"Carrier A bordereau"}`},
		{name: "written receiver_label and null period_param are sent",
			spec: deliverableManifestBasics + "  receiver_label: \"\"\n  period_param: null\n",
			want: `{"filename_pattern":"CarrierA_{period}.xlsx","name":"Carrier A bordereau","period_param":null,"receiver_label":""}`},
		{name: "a long name's slug is cut to 59 characters", slug: "carrier-a-bordereau-for-the-september-2026-quarterly-report",
			spec: "  name: " + longName + "\n  filename_pattern: a.xlsx\n",
			want: `{"filename_pattern":"a.xlsx","name":"` + longName + `"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := servePlan(t, nil)
			m := planManifest(t, tc.spec)
			if tc.slug != "" {
				m.Metadata.Slug = tc.slug
			}
			p, err := b.client().Prepare("token", m, ".")
			if err != nil {
				t.Fatalf("Prepare: %v", err)
			}
			if got, want := planLines(p), []string{"POST /deliverables"}; !reflect.DeepEqual(got, want) {
				t.Fatalf("requests = %v, want %v", got, want)
			}
			if got := planBody(t, p.Requests[0].Body); got != tc.want {
				t.Errorf("body = %s, want %s", got, tc.want)
			}
			if p.Current != nil || p.Changes != nil || p.Slug != m.Metadata.Slug {
				t.Errorf("Current = %v, Changes = %v, Slug = %q", p.Current, p.Changes, p.Slug)
			}
			if got, want := b.mock.Requests(), []string{planListing}; !reflect.DeepEqual(got, want) {
				t.Errorf("requests made = %v, want %v", got, want)
			}
		})
	}
}

func TestPrepareRefusesASlugTheNameDoesNotGenerate(t *testing.T) {
	for _, tc := range []struct {
		name, slug, spec, generated string
	}{
		{name: "different slug", slug: "carrier-a", spec: deliverableManifestBasics, generated: `"carrier-a-bordereau"`},
		{name: "uncut long slug", slug: "carrier-a-bordereau-for-the-september-2026-quarterly-reporting-cycle",
			spec:      "  name: Carrier A bordereau for the September 2026 quarterly reporting cycle\n  filename_pattern: a.xlsx\n",
			generated: `"carrier-a-bordereau-for-the-september-2026-quarterly-report"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := servePlan(t, nil)
			m := planManifest(t, tc.spec)
			m.Metadata.Slug = tc.slug
			p, err := b.client().Prepare("token", m, ".")
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage || p != nil {
				t.Fatalf("plan = %v, exit = %d, want nil and %d; error = %v", p, code, qerrors.ExitBadUsage, err)
			}
			if !strings.Contains(err.Error(), "set metadata.slug to "+tc.generated) {
				t.Errorf("error does not name the generated slug %s: %v", tc.generated, err)
			}
		})
	}
}

func TestPrepareRefusesANameThatGeneratesNoSlug(t *testing.T) {
	b := servePlan(t, nil)
	m := planManifest(t, "  name: \"???\"\n  filename_pattern: a.xlsx\n")
	p, err := b.client().Prepare("token", m, ".")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage || p != nil {
		t.Fatalf("plan = %v, exit = %d, want nil and %d; error = %v", p, code, qerrors.ExitBadUsage, err)
	}
	if strings.Contains(err.Error(), "set metadata.slug") {
		t.Errorf("error tells the user to set metadata.slug to a slug that cannot exist: %v", err)
	}
	if got, want := b.mock.Requests(), []string{planListing}; !reflect.DeepEqual(got, want) {
		t.Errorf("requests made = %v, want %v", got, want)
	}
}

func TestPrepareRefusesADeliverableInTheTrash(t *testing.T) {
	dir := planWorkbook(t)
	b := servePlan(t, planDeliverable(map[string]any{"archived_at": planTrashed, "name": "Old name"}))
	m := planManifest(t, deliverableManifestBasics+"  release: v2\n  template: carrier-a.xlsx\n")
	p, err := b.client().Prepare("token", m, dir)
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage || p != nil {
		t.Fatalf("plan = %v, exit = %d, want nil and %d; error = %v", p, code, qerrors.ExitBadUsage, err)
	}
	if !strings.Contains(err.Error(), "restore it and apply again: qctl undelete deliverable carrier-a-bordereau") {
		t.Errorf("error does not name the undelete command: %v", err)
	}
	if got, want := b.mock.Requests(), []string{planListing}; !reflect.DeepEqual(got, want) {
		t.Errorf("requests made = %v, want the listing only", got)
	}
}

func TestPreparePatchesTheBasicFields(t *testing.T) {
	for _, tc := range []struct {
		name    string
		spec    string
		body    string
		changes []Change
	}{
		{name: "absent receiver_label and period_param keep the stored values", spec: deliverableManifestBasics},
		{name: "written values equal to the stored ones", spec: deliverableManifestBasics + "  receiver_label: Carrier A\n  period_param: period\n"},
		{name: "a rename keeps the slug and seeds the other three from the read",
			spec:    "  name: Carrier A monthly bordereau\n  filename_pattern: CarrierA_{period}.xlsx\n",
			body:    `{"filename_pattern":"CarrierA_{period}.xlsx","name":"Carrier A monthly bordereau","period_param":"period","receiver_label":"Carrier A"}`,
			changes: []Change{{Field: "name", Old: "Carrier A bordereau", New: "Carrier A monthly bordereau"}}},
		{name: "null period_param clears it", spec: deliverableManifestBasics + "  period_param: null\n",
			body:    `{"filename_pattern":"CarrierA_{period}.xlsx","name":"Carrier A bordereau","period_param":null,"receiver_label":"Carrier A"}`,
			changes: []Change{{Field: "period_param", Old: "period"}}},
		{name: "receiver_label and filename_pattern",
			spec: "  name: Carrier A bordereau\n  filename_pattern: B_{period}.xlsx\n  receiver_label: \"\"\n",
			body: `{"filename_pattern":"B_{period}.xlsx","name":"Carrier A bordereau","period_param":"period","receiver_label":""}`,
			changes: []Change{{Field: "receiver_label", Old: "Carrier A", New: ""},
				{Field: "filename_pattern", Old: "CarrierA_{period}.xlsx", New: "B_{period}.xlsx"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := servePlan(t, planDeliverable(nil))
			p, err := b.client().Prepare("token", planManifest(t, tc.spec), ".")
			if err != nil {
				t.Fatalf("Prepare: %v", err)
			}
			if p.Current == nil || p.Current.ID.String() != monthlyID {
				t.Fatalf("Current = %+v, want the fresh read", p.Current)
			}
			if got, want := b.mock.Requests(), []string{planListing, planRead}; !reflect.DeepEqual(got, want) {
				t.Errorf("requests made = %v, want %v", got, want)
			}
			if !reflect.DeepEqual(p.Changes, tc.changes) {
				t.Errorf("changes = %#v, want %#v", p.Changes, tc.changes)
			}
			if tc.body == "" {
				if len(p.Requests) != 0 {
					t.Fatalf("requests = %v, want none", planLines(p))
				}
				return
			}
			if got, want := planLines(p), []string{"PATCH /deliverables/" + monthlyID}; !reflect.DeepEqual(got, want) {
				t.Fatalf("requests = %v, want %v", got, want)
			}
			if got := planBody(t, p.Requests[0].Body); got != tc.body {
				t.Errorf("body = %s, want %s", got, tc.body)
			}
			if note := p.Requests[0].Note; !strings.Contains(note, "takes effect immediately") || !strings.Contains(note, "not versioned") {
				t.Errorf("note = %q", note)
			}
		})
	}
}

func TestApplySendsTheSeededPatchWithNullForAClearedPeriodParam(t *testing.T) {
	b := servePlan(t, planDeliverable(nil))
	c := b.client()
	p, err := c.Prepare("token", planManifest(t, "  name: Carrier A monthly bordereau\n  filename_pattern: CarrierA_{period}.xlsx\n  period_param: null\n"), ".")
	if err != nil {
		t.Fatal(err)
	}
	r, err := c.Apply("token", p)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got, want := b.writes(), []string{"PATCH " + planItemPath}; !reflect.DeepEqual(got, want) {
		t.Fatalf("writes = %v, want %v", got, want)
	}
	sent := b.bodies["PATCH "+planItemPath]
	if len(sent) != 4 || string(sent["period_param"]) != "null" || string(sent["name"]) != `"Carrier A monthly bordereau"` ||
		string(sent["receiver_label"]) != `"Carrier A"` || string(sent["filename_pattern"]) != `"CarrierA_{period}.xlsx"` {
		t.Errorf("PATCH body = %v", sent)
	}
	if !r.Updated || r.Created || r.Revision != nil || r.Template != nil || r.Deliverable == nil || r.Manifest == nil || r.Manifest.Has("release") {
		t.Errorf("result = %+v", r)
	}
}

func TestPrepareRefusesADescriptionAnExistingReleaseDoesNotHave(t *testing.T) {
	old := map[string]any{"description": "Old layout"}
	for _, tc := range []struct {
		name      string
		revisions []map[string]any
		spec      string
		says      []string
		never     string
	}{
		{name: "draft", revisions: []map[string]any{enabledV2, planRevision(draftV3, old)},
			spec: "  release: v3\n  description: September layout\n",
			says: []string{`draft release "v3"`, "bump spec.release, or delete the draft and apply again: qctl delete deliverable-revision carrier-a-bordereau --release v3"}},
		{name: "draft with a label that needs quoting", revisions: []map[string]any{planRevision(draftV4, old)},
			spec: "  release: v4 beta\n  description: September layout\n",
			says: []string{"qctl delete deliverable-revision carrier-a-bordereau --release 'v4 beta'"}},
		{name: "enabled", revisions: []map[string]any{disabledV1, planRevision(enabledV2, old)},
			spec: "  release: v2\n  description: September layout\n", never: "delete",
			says: []string{`release "v2", which is enabled and cannot change; bump spec.release`}},
		{name: "disabled, against a null description", revisions: []map[string]any{planRevision(disabledV1, old)},
			spec: "  release: v1\n  description: null\n", never: "delete",
			says: []string{`release "v1", which is disabled and cannot change; bump spec.release`}},
		{name: "a written description against none stored", revisions: []map[string]any{enabledV2},
			spec: "  release: v2\n  description: September layout\n", says: []string{"bump spec.release"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := servePlan(t, planDeliverable(nil), tc.revisions...)
			// The rename would PATCH, so the refusal must come before any write is planned.
			m := planManifest(t, "  name: Carrier A monthly bordereau\n  filename_pattern: a.xlsx\n"+tc.spec)
			p, err := b.client().Prepare("token", m, ".")
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage || p != nil {
				t.Fatalf("plan = %v, exit = %d, want nil and %d; error = %v", p, code, qerrors.ExitBadUsage, err)
			}
			for _, want := range tc.says {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error lacks %q: %v", want, err)
				}
			}
			if tc.never != "" && strings.Contains(err.Error(), tc.never) {
				t.Errorf("error mentions %q: %v", tc.never, err)
			}
			if got, want := b.mock.Requests(), []string{planListing, planRead, planRevisions}; !reflect.DeepEqual(got, want) {
				t.Errorf("requests made = %v, want %v", got, want)
			}
		})
	}
}

func TestPrepareLeavesAMatchingReleaseUnchanged(t *testing.T) {
	layout := map[string]any{"description": "September layout"}
	for _, tc := range []struct {
		name      string
		revisions []map[string]any
		spec      string
		want      string
	}{
		{name: "enabled, same description", revisions: []map[string]any{disabledV1, planRevision(enabledV2, layout)},
			spec: "  release: v2\n  description: September layout\n", want: revV2},
		{name: "enabled, description not written", revisions: []map[string]any{planRevision(enabledV2, layout)},
			spec: "  release: v2\n", want: revV2},
		{name: "disabled, null against none stored", revisions: []map[string]any{disabledV1, enabledV2},
			spec: "  release: v1\n  description: null\n", want: revV1},
		{name: "draft, same description", revisions: []map[string]any{enabledV2, planRevision(draftV3, layout)},
			spec: "  release: v3\n  description: September layout\n", want: revV3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := servePlan(t, planDeliverable(nil), tc.revisions...)
			p, err := b.client().Prepare("token", planManifest(t, deliverableManifestBasics+tc.spec), ".")
			if err != nil {
				t.Fatalf("Prepare: %v", err)
			}
			if len(p.Requests) != 0 || len(p.Notes) != 0 || p.CopyFrom != nil {
				t.Fatalf("requests = %v, notes = %v, CopyFrom = %v; want unchanged", planLines(p), p.Notes, p.CopyFrom)
			}
			if p.Revision == nil || p.Revision.ID.String() != tc.want {
				t.Errorf("Revision = %+v, want %s", p.Revision, tc.want)
			}
			r, err := b.client().Apply("token", p)
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if got := b.writes(); len(got) != 0 {
				t.Errorf("writes = %v, want none", got)
			}
			if r.Created || r.Updated || r.Manifest == nil || r.Manifest.Status.RevisionID == nil || r.Manifest.Status.RevisionID.String() != tc.want {
				t.Errorf("result = %+v", r)
			}
		})
	}
}

func TestPrepareNotesATemplateOnAnExistingRelease(t *testing.T) {
	escaped := planRevision(draftV4, map[string]any{"release": "v5\x1b[31m"})
	for _, tc := range []struct {
		name      string
		revisions []map[string]any
		spec      string
		note      string
	}{
		{name: "draft", revisions: []map[string]any{enabledV2, draftV3},
			spec: "  release: v3\n  template: missing.xlsx\n  acknowledge_lossy: true\n",
			note: `spec.template is not compared with the template of draft release "v3"; to replace it: ` +
				"qctl upload deliverable-template manifests/missing.xlsx --deliverable carrier-a-bordereau --release v3 --acknowledge-lossy"},
		{name: "draft with a label that needs quoting", revisions: []map[string]any{draftV4},
			spec: "  release: v4 beta\n  template: missing.xlsx\n",
			note: `spec.template is not compared with the template of draft release "v4 beta"; to replace it: ` +
				"qctl upload deliverable-template manifests/missing.xlsx --deliverable carrier-a-bordereau --release 'v4 beta'"},
		{name: "draft with a label sanitizing changes", revisions: []map[string]any{escaped},
			spec: "  release: \"v5\\x1b[31m\"\n  template: missing.xlsx\n",
			note: `spec.template is not compared with the template of draft release "v5\\x1b[31m"; to replace it: ` +
				"qctl upload deliverable-template manifests/missing.xlsx --deliverable carrier-a-bordereau --release " + revV4},
		{name: "enabled", revisions: []map[string]any{enabledV2},
			spec: "  release: v2\n  template: missing.xlsx\n",
			note: `spec.template is not compared with the template of release "v2", which is enabled and cannot change; ` +
				"bump spec.release to create a release with it"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := servePlan(t, planDeliverable(nil), tc.revisions...)
			p, err := b.client().Prepare("token", planManifest(t, deliverableManifestBasics+tc.spec), "manifests")
			if err != nil {
				t.Fatalf("Prepare: %v", err)
			}
			if len(p.Requests) != 0 || p.TemplatePath != "" {
				t.Fatalf("requests = %v, TemplatePath = %q; want nothing uploaded", planLines(p), p.TemplatePath)
			}
			if want := []string{tc.note}; !reflect.DeepEqual(p.Notes, want) {
				t.Errorf("notes =\n%q\nwant\n%q", p.Notes, want)
			}
			if got, want := b.mock.Requests(), []string{planListing, planRead, planRevisions}; !reflect.DeepEqual(got, want) {
				t.Errorf("requests made = %v, want %v", got, want)
			}
		})
	}
}

func TestPrepareCreatesAMissingRelease(t *testing.T) {
	for _, tc := range []struct {
		name      string
		current   map[string]any
		revisions []map[string]any
		spec      string
		lines     []string
		body      string
		copyFrom  string
		note      string
	}{
		{name: "copying the enabled revision", current: planDeliverable(nil), revisions: []map[string]any{disabledV1, enabledV2, draftV3},
			spec: "  release: v4\n", lines: []string{"POST /deliverables/" + monthlyID + "/revisions"},
			body: `{"copy_from_revision_id":"` + revV2 + `","release":"v4"}`, copyFrom: revV2,
			note: `creates draft release "v4" as a copy of enabled release "v2"`},
		{name: "from scratch when none is enabled", current: planDeliverable(nil), revisions: []map[string]any{disabledV1, draftV3},
			spec: "  release: v5\n  description: null\n", lines: []string{"POST /deliverables/" + monthlyID + "/revisions"},
			body: `{"description":null,"release":"v5"}`, note: `creates draft release "v5" from scratch`},
		{name: "on a deliverable being created", spec: "  release: v1\n  description: September layout\n",
			lines: []string{"POST /deliverables", "POST /deliverables/<created-deliverable-id>/revisions"},
			body:  `{"description":"September layout","release":"v1"}`, note: `creates draft release "v1" from scratch`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := servePlan(t, tc.current, tc.revisions...)
			p, err := b.client().Prepare("token", planManifest(t, deliverableManifestBasics+tc.spec), ".")
			if err != nil {
				t.Fatalf("Prepare: %v", err)
			}
			if got := planLines(p); !reflect.DeepEqual(got, tc.lines) {
				t.Fatalf("requests = %v, want %v", got, tc.lines)
			}
			revision := p.Requests[len(p.Requests)-1]
			if got := planBody(t, revision.Body); got != tc.body {
				t.Errorf("revision body = %s, want %s", got, tc.body)
			}
			if revision.Note != tc.note {
				t.Errorf("note = %q, want %q", revision.Note, tc.note)
			}
			copyFrom := ""
			if p.CopyFrom != nil {
				copyFrom = p.CopyFrom.ID.String()
			}
			if copyFrom != tc.copyFrom {
				t.Errorf("CopyFrom = %q, want %q", copyFrom, tc.copyFrom)
			}
			if p.Revision != nil || p.TemplatePath != "" {
				t.Errorf("Revision = %v, TemplatePath = %q", p.Revision, p.TemplatePath)
			}
		})
	}
}

func TestPrepareUploadsTheTemplateBeforeEveryOtherWrite(t *testing.T) {
	dir := planWorkbook(t)
	for _, tc := range []struct {
		name     string
		dir      string
		template string
	}{
		{name: "relative to the manifest", dir: dir, template: "carrier-a.xlsx"},
		{name: "absolute", dir: "elsewhere", template: filepath.Join(dir, "carrier-a.xlsx")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := servePlan(t, planDeliverable(nil), disabledV1, enabledV2)
			m := planManifest(t, "  name: Carrier A monthly bordereau\n  filename_pattern: CarrierA_{period}.xlsx\n"+
				"  release: v3\n  template: "+tc.template+"\n  acknowledge_lossy: true\n")
			p, err := b.client().Prepare("token", m, tc.dir)
			if err != nil {
				t.Fatalf("Prepare: %v", err)
			}
			want := []string{"PUT /deliverable-skeletons/upload-url", "<upload method> <presigned upload URL>", "POST /deliverable-skeletons/finalize",
				"PATCH /deliverables/" + monthlyID, "POST /deliverables/" + monthlyID + "/revisions"}
			if got := planLines(p); !reflect.DeepEqual(got, want) {
				t.Fatalf("requests =\n%v\nwant\n%v", got, want)
			}
			if path := filepath.Join(dir, "carrier-a.xlsx"); p.TemplatePath != path || !p.AcknowledgeLossy {
				t.Errorf("TemplatePath = %q, AcknowledgeLossy = %v; want %q, true", p.TemplatePath, p.AcknowledgeLossy, path)
			}
			for i, body := range map[int]string{
				0: `{"file_size":8,"original_filename":"carrier-a.xlsx"}`,
				2: `{"acknowledge_lossy":true,"skeleton_id":"<uploaded-template-id>"}`,
				4: `{"acknowledge_lossy":true,"copy_from_revision_id":"` + revV2 + `","release":"v3","skeleton_id":"<uploaded-template-id>"}`,
			} {
				if got := planBody(t, p.Requests[i].Body); got != body {
					t.Errorf("request %d body = %s, want %s", i, got, body)
				}
			}
			if p.Requests[1].Body != nil {
				t.Errorf("transfer body = %v, want nil", p.Requests[1].Body)
			}
		})
	}
}

func TestPrepareChecksTheTemplateFileBeforeAnyWrite(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "folder.xlsx"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		template string
		code     int
		missing  bool
	}{
		{name: "missing", template: "missing.xlsx", code: qerrors.ExitGenericError, missing: true},
		{name: "directory", template: "folder.xlsx", code: qerrors.ExitBadUsage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := servePlan(t, planDeliverable(nil), enabledV2)
			p, err := b.client().Prepare("token", planManifest(t, deliverableManifestBasics+"  release: v3\n  template: "+tc.template+"\n"), dir)
			if code := qerrors.GetExitCode(err); code != tc.code || p != nil {
				t.Fatalf("plan = %v, exit = %d, want nil and %d; error = %v", p, code, tc.code, err)
			}
			if got := errors.Is(err, fs.ErrNotExist); got != tc.missing {
				t.Errorf("errors.Is(err, fs.ErrNotExist) = %v, want %v; error = %v", got, tc.missing, err)
			}
			if got := b.writes(); len(got) != 0 {
				t.Errorf("writes = %v, want none", got)
			}
		})
	}
}

// planFullCreate prepares the create of carrier-a-bordereau with release v2
// and template carrier-a.xlsx, and returns the template's directory.
func planFullCreate(t *testing.T, b *planBackend) (*Plan, string) {
	t.Helper()
	dir := planWorkbook(t)
	m := planManifest(t, deliverableManifestBasics+"  receiver_label: Carrier A\n  period_param: period\n"+
		"  release: v2\n  description: September layout\n  template: carrier-a.xlsx\n")
	p, err := b.client().Prepare("token", m, dir)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	return p, dir
}

func TestApplyAttachesTheTemplateIDTheFinalizeReturned(t *testing.T) {
	b := servePlan(t, nil)
	p, _ := planFullCreate(t, b)
	r, err := b.client().Apply("token", p)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	want := []string{"PUT " + planMintPath, "PUT " + putTargetPath, "POST " + planFinalizePath, "POST " + testPath, "POST " + revisionsPath}
	if got := b.writes(); !reflect.DeepEqual(got, want) {
		t.Fatalf("writes =\n%v\nwant\n%v", got, want)
	}
	if got := string(b.bodies["POST "+planFinalizePath]["skeleton_id"]); got != "41" {
		t.Errorf("finalize skeleton_id = %s, want the minted 41", got)
	}
	revision := b.bodies["POST "+revisionsPath]
	if got := string(revision["skeleton_id"]); got != "7" {
		t.Errorf("revision skeleton_id = %s, want 7, the id the finalize returned", got)
	}
	if string(revision["acknowledge_lossy"]) != "false" || string(revision["release"]) != `"v2"` || revision["copy_from_revision_id"] != nil {
		t.Errorf("revision body = %v", revision)
	}
	if !r.Created || r.Updated || r.Template == nil || r.Template.SkeletonID != 7 || r.Revision == nil || r.Revision.ID.String() != revV4 {
		t.Fatalf("result = %+v", r)
	}
	if m := r.Manifest; m == nil || m.Spec.Release != "v2" || m.Status.RevisionID.String() != revV4 || m.Status.ID.String() != monthlyID {
		t.Errorf("manifest = %+v", m)
	}
}

func TestWriteRequestsPrintsAFullCreate(t *testing.T) {
	b := servePlan(t, nil)
	p, dir := planFullCreate(t, b)
	var out strings.Builder
	if err := p.WriteRequests(&out); err != nil {
		t.Fatal(err)
	}
	want := `PUT /deliverable-skeletons/upload-url
{
  "file_size": 8,
  "original_filename": "carrier-a.xlsx"
}
<upload method> <presigned upload URL>
# sends DIR/carrier-a.xlsx to the URL, with the method, the upload-URL response names
POST /deliverable-skeletons/finalize
# the revision gets the template id this returns, an identical earlier upload's when there is one
{
  "acknowledge_lossy": false,
  "skeleton_id": "<uploaded-template-id>"
}
POST /deliverables
{
  "filename_pattern": "CarrierA_{period}.xlsx",
  "name": "Carrier A bordereau",
  "period_param": "period",
  "receiver_label": "Carrier A"
}
POST /deliverables/<created-deliverable-id>/revisions
# creates draft release "v2" from scratch
{
  "acknowledge_lossy": false,
  "description": "September layout",
  "release": "v2",
  "skeleton_id": "<uploaded-template-id>"
}
`
	if got, want := out.String(), strings.ReplaceAll(want, "DIR", dir); got != want {
		t.Errorf("output =\n%s\nwant\n%s", got, want)
	}
}

func TestWriteRequestsPrintsAPatchAndARevisionCopy(t *testing.T) {
	b := servePlan(t, planDeliverable(nil), disabledV1, enabledV2)
	m := planManifest(t, "  name: Carrier A monthly bordereau\n  filename_pattern: CarrierA_{period}.xlsx\n  period_param: null\n  release: v3\n")
	p, err := b.client().Prepare("token", m, ".")
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := p.WriteRequests(&out); err != nil {
		t.Fatal(err)
	}
	want := `name: "Carrier A bordereau" → "Carrier A monthly bordereau"
period_param: "period" → null
PATCH /deliverables/` + monthlyID + `
# takes effect immediately: a deliverable's basic fields are not versioned
{
  "filename_pattern": "CarrierA_{period}.xlsx",
  "name": "Carrier A monthly bordereau",
  "period_param": null,
  "receiver_label": "Carrier A"
}
POST /deliverables/` + monthlyID + `/revisions
# creates draft release "v3" as a copy of enabled release "v2"
{
  "copy_from_revision_id": "` + revV2 + `",
  "release": "v3"
}
`
	if got := out.String(); got != want {
		t.Errorf("output =\n%s\nwant\n%s", got, want)
	}
}

func TestWriteRequestsPrintsNotesAfterTheRequestsAndEscapesControls(t *testing.T) {
	b := servePlan(t, planDeliverable(nil), planRevision(draftV3, map[string]any{"release": "v3\x1b[2J\nx"}))
	m := planManifest(t, "  name: \"Carrier\\x1b[31m A\"\n  filename_pattern: CarrierA_{period}.xlsx\n"+
		"  release: \"v3\\x1b[2J\\nx\"\n  template: missing.xlsx\n")
	p, err := b.client().Prepare("token", m, ".")
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := p.WriteRequests(&out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if strings.ContainsAny(text, "\x1b") {
		t.Errorf("output holds a raw escape:\n%q", text)
	}
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	last := lines[len(lines)-1]
	if !strings.HasPrefix(last, "Note: spec.template is not compared with the template of draft release ") ||
		!strings.HasSuffix(last, "--release "+revV3) {
		t.Errorf("last line = %q, want the template note naming the revision id", last)
	}
	if !strings.HasPrefix(lines[0], `name: "Carrier A bordereau" → "Carrier\u001b[31m A"`) {
		t.Errorf("first line = %q", lines[0])
	}
}

func TestApplyStopsAtTheRevisionPostAfterCreatingTheDeliverable(t *testing.T) {
	b := servePlan(t, nil)
	b.answer(t, http.MethodPost, revisionsPath, http.StatusConflict,
		`{"detail":{"msg":"Release 'v2' already exists for this Deliverable.","error_id":""}}`)
	c := b.client()
	p, err := c.Prepare("token", planManifest(t, deliverableManifestBasics+"  release: v2\n"), ".")
	if err != nil {
		t.Fatal(err)
	}
	r, err := c.Apply("token", p)
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage || r == nil {
		t.Fatalf("result = %v, exit = %d, want the created deliverable and %d; error = %v", r, code, qerrors.ExitBadUsage, err)
	}
	if !r.Created || r.Revision != nil || r.Manifest == nil || r.Manifest.Spec.Name != "Carrier A bordereau" || r.Manifest.Has(keyRelease) {
		t.Errorf("result = %+v, manifest = %+v; want the created deliverable without the failed release", r, r.Manifest)
	}
	for _, want := range []string{
		"deliverable/carrier-a-bordereau apply stopped at step 2 of 2 (POST /deliverables/" + monthlyID + "/revisions), 1 completed",
		"Release 'v2' already exists", "qctl diff -f on the same manifest shows what is left, and applying it again finishes it",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusConflict {
		t.Errorf("error metadata lost: %#v", err)
	}
	if got, want := b.writes(), []string{"POST " + testPath, "POST " + revisionsPath}; !reflect.DeepEqual(got, want) {
		t.Errorf("writes = %v, want the deliverable created first", got)
	}
}

func TestApplyReturnsAFirstRequestFailureUnchanged(t *testing.T) {
	b := servePlan(t, planDeliverable(nil))
	b.answer(t, http.MethodPatch, planItemPath, http.StatusNotFound,
		`{"detail":{"msg":"Deliverable can not be found in this organization.","error_code":"NOT_FOUND","error_id":""}}`)
	c := b.client()
	p, err := c.Prepare("token", planManifest(t, "  name: Renamed\n  filename_pattern: a.xlsx\n"), ".")
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Apply("token", p)
	if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
		t.Fatalf("exit = %d, want %d; error = %v", code, qerrors.ExitNotFound, err)
	}
	if strings.Contains(err.Error(), "stopped at step") {
		t.Errorf("a first-request failure names a step: %v", err)
	}
}

func TestApplyStopsAtAFailedTemplateTransferBeforeAnyOtherWrite(t *testing.T) {
	b := servePlan(t, nil)
	b.answer(t, http.MethodPut, putTargetPath, http.StatusForbidden, "denied")
	p, _ := planFullCreate(t, b)
	_, err := b.client().Apply("token", p)
	if code := qerrors.GetExitCode(err); code != qerrors.ExitGenericError || err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("exit = %d, want %d; error = %v", code, qerrors.ExitGenericError, err)
	}
	if got, want := b.writes(), []string{"PUT " + planMintPath, "PUT " + putTargetPath}; !reflect.DeepEqual(got, want) {
		t.Errorf("writes = %v, want %v", got, want)
	}
}

func TestApplyNamesAcknowledgeLossyForALossyTemplate(t *testing.T) {
	lossy := `{"detail":{"msg":"Generated files cannot keep: xl/media/image1.png.","error_code":"SKELETON_LOSSY","error_id":"",` +
		`"context":{"fidelity_report":{"is_lossy":true,"lost_parts":["xl/media/image1.png"]},"skeleton_id":7}}}`
	for _, tc := range []struct {
		name   string
		method string
		path   string
		// step and finish are "" when nothing past the template was written,
		// and the error must then say neither.
		step, finish string
		writes       []string
	}{
		{name: "at the finalize", method: http.MethodPost, path: planFinalizePath,
			writes: []string{"PUT " + planMintPath, "PUT " + putTargetPath, "POST " + planFinalizePath}},
		{name: "at the revision POST", method: http.MethodPost, path: revisionsPath,
			step: "step 5 of 5 (POST /deliverables/" + monthlyID + "/revisions), 4 completed: ",
			finish: "\nqctl diff -f on the same manifest shows what is left, and " +
				"applying it again with spec.acknowledge_lossy: true finishes it",
			writes: []string{"PUT " + planMintPath, "PUT " + putTargetPath, "POST " + planFinalizePath, "POST " + testPath, "POST " + revisionsPath}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := servePlan(t, nil)
			b.answer(t, tc.method, tc.path, http.StatusConflict, lossy)
			p, _ := planFullCreate(t, b)
			_, err := b.client().Apply("token", p)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit = %d, want %d; error = %v", code, qerrors.ExitBadUsage, err)
			}
			var refusal *TemplateLossyError
			if !errors.As(err, &refusal) || refusal.TemplateID != 7 {
				t.Fatalf("err = %#v, want a *TemplateLossyError for template 7", err)
			}
			for _, want := range []string{tc.step, "  - xl/media/image1.png", "spec.acknowledge_lossy: true accepts the listed losses"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error lacks %q: %v", want, err)
				}
			}
			if !strings.HasSuffix(err.Error(), tc.finish) {
				t.Errorf("error does not end with %q: %v", tc.finish, err)
			}
			if tc.step == "" && (strings.Contains(err.Error(), "stopped at step") || strings.Contains(err.Error(), "applying it again")) {
				t.Errorf("error reports a partial apply when only the template was written: %v", err)
			}
			if got := b.writes(); !reflect.DeepEqual(got, tc.writes) {
				t.Errorf("writes = %v, want %v", got, tc.writes)
			}
		})
	}
}

func TestFormatApplyResult(t *testing.T) {
	t.Run("full create", func(t *testing.T) {
		b := servePlan(t, nil)
		p, _ := planFullCreate(t, b)
		r, err := b.client().Apply("token", p)
		if err != nil {
			t.Fatal(err)
		}
		want := "release \"v2\" created as a draft (from scratch)\n" +
			"template attached: deliverable-template/7 (reused: an identical workbook was uploaded earlier)\n" +
			"\nRe-anchored bindings (0):\n"
		if got := FormatApplyResult(p, r); got != want {
			t.Errorf("output =\n%s\nwant\n%s", got, want)
		}
	})
	t.Run("copy with notes", func(t *testing.T) {
		reused := false
		p := &Plan{CopyFrom: &Revision{Release: "v1\x1b"}, Notes: []string{"first\nsecond"}}
		r := &ApplyResult{
			Revision: &RevisionCreated{DeliverableRevisionCreateResponse: &api.DeliverableRevisionCreateResponse{ID: uuid.New(), Release: "v2"}},
			Template: &TemplateResult{SkeletonFinalizeResponse: &api.SkeletonFinalizeResponse{SkeletonID: 9, ReusedExistingSkeleton: &reused}},
		}
		want := "release \"v2\" created as a draft (copied from \"v1\\\\x1b\")\n" +
			"template attached: deliverable-template/9 (new upload)\n" +
			"Note: first\\nsecond\n"
		if got := FormatApplyResult(p, r); got != want {
			t.Errorf("output =\n%s\nwant\n%s", got, want)
		}
	})
	t.Run("unchanged", func(t *testing.T) {
		if got := FormatApplyResult(&Plan{}, &ApplyResult{}); got != "" {
			t.Errorf("output = %q, want empty", got)
		}
	})
}
