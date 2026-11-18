package root

import (
	"encoding/json"
	"maps"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestDeliverableBindingRefusalsWithTemplatePrecedeEveryWrite(t *testing.T) {
	for _, tc := range []struct {
		name, release, tree, message string
	}{
		{name: "enabled release", release: "2026.1",
			tree:    strings.ReplaceAll(strings.Replace(sheetsDraftTree, `"state":"draft"`, `"state":"enabled"`, 1), draftRevisionID, enabledRevisionID),
			message: "bump spec.release"},
		{name: "disabled release", release: "2026.2", tree: strings.Replace(sheetsDraftTree, `"state":"draft"`, `"state":"disabled"`, 1),
			message: "bump spec.release"},
		{name: "draft without template", release: "2026.2",
			tree:    strings.Replace(bareRevisionTree(draftRevisionID, "2026.2", "draft"), `"skeleton_id":7`, `"skeleton_id":null`, 1),
			message: "which has no template; upload one first"},
		{name: "copied sheet mirrored by unmanaged cell", release: "2026.3",
			tree: strings.ReplaceAll(strings.Replace(strings.Replace(sheetsDraftTree, `"state":"draft"`, `"state":"enabled"`, 1),
				`"cell_bindings":[]`, `"cell_bindings":[{"id":"30000000-0000-4000-8000-000000000001","sheet_name":"Summary",`+
					`"cell_ref":"B1","source_type":"aggregate","aggregate_func":"count","same_rows_as_sheet_binding_id":"`+sheetsNotesID+`"}]`, 1),
				draftRevisionID, enabledRevisionID),
			message: `cell bindings Summary!B1 total its rows`},
	} {
		for _, verb := range []string{"apply", "diff"} {
			t.Run(tc.name+"/"+verb, func(t *testing.T) {
				s := sheetsServer(t)
				id := draftRevisionID
				if tc.release != "2026.2" {
					id = enabledRevisionID
				}
				tree := tc.tree
				if id == enabledRevisionID {
					tree = strings.Replace(tree, `"release":"2026.2"`, `"release":"2026.1"`, 1)
				}
				answerRevisionTree(t, s.MockAPIServer, id, tree)
				if tc.name == "disabled release" {
					s.RegisterHandler(http.MethodGet, deliverableTestPath+"/"+liveDeliverableID+"/revisions", func(w http.ResponseWriter, r *http.Request) {
						checkDeliverableToken(t, r)
						testutil.RespondJSON(w, http.StatusOK,
							json.RawMessage(strings.Replace(liveDeliverableRevisions, `"state":"draft"`, `"state":"disabled"`, 1)))
					})
				}
				// Both the basic-field PATCH and spec.template must wait for
				// the binding plan, including a real upload on the new copy.
				manifest := strings.Replace(sheetsManifest(sheetsData, sheetsDataColumns), "Acme Re", "Renamed receiver", 1)
				manifest = strings.Replace(manifest, `release: "2026.2"`, `release: "`+tc.release+`"`+"\n  template: claims.xlsx", 1)
				dir := t.TempDir()
				writeManifest(t, dir, "claims.xlsx", string(templateFileBytes))
				path := writeManifest(t, dir, "deliverable.yaml", manifest)
				args := []string{verb, "-f", path}
				if verb == "apply" {
					args = append(args, "--yes")
				}
				out, _, err := runManifest(t, args...)
				if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
					t.Fatalf("exit = %d, want 2; error = %v", code, err)
				}
				if !strings.Contains(err.Error(), tc.message) {
					t.Errorf("error = %v, want %q", err, tc.message)
				}
				if writes := s.writes(); len(writes) != 0 || out != "" {
					t.Errorf("writes = %v, stdout = %q; want neither", writes, out)
				}
			})
		}
	}
}

func TestApplyDeliverableResumesOnlyUnfinishedBindingWrites(t *testing.T) {
	s := sheetsServer(t)
	var tree map[string]any
	if err := json.Unmarshal([]byte(sheetsDraftTree), &tree); err != nil {
		t.Fatal(err)
	}
	s.RegisterHandler(http.MethodGet, revisionTestPath+draftRevisionID, func(w http.ResponseWriter, r *http.Request) {
		checkDeliverableToken(t, r)
		testutil.RespondJSON(w, http.StatusOK, tree)
	})
	s.RegisterHandler(http.MethodPatch, draftSheetBindingsPath+"/"+sheetsDataID, func(w http.ResponseWriter, r *http.Request) {
		checkDeliverableToken(t, r)
		s.bodies[patchData] = readBody(t, r)
		var patched map[string]any
		if err := json.Unmarshal([]byte(reanchoredDataPatch), &patched); err != nil {
			t.Error(err)
		}
		stored := maps.Clone(patched)
		delete(stored, "anchor_report")
		tree["sheet_bindings"].([]any)[0] = stored
		testutil.RespondJSON(w, http.StatusOK, patched)
	})
	attempts := 0
	s.RegisterHandler(http.MethodPost, dataColumnBindingsPath, func(w http.ResponseWriter, r *http.Request) {
		checkDeliverableToken(t, r)
		s.bodies[postColumn] = readBody(t, r)
		if attempts++; attempts == 1 {
			testutil.RespondJSON(w, http.StatusServiceUnavailable, json.RawMessage(`{"detail":"temporarily unavailable"}`))
			return
		}
		column := map[string]any{"id": sheetsColumnC, "target_column_index": 3, "source_type": "column", "source_column": "claims",
			"run_param": nil, "format_override": nil, "origin": "user", "target_label": "Claims", "needs_confirmation": false}
		data := tree["sheet_bindings"].([]any)[0].(map[string]any)
		data["column_bindings"] = append(data["column_bindings"].([]any), column)
		testutil.RespondJSON(w, http.StatusOK, column)
	})
	path := writeManifest(t, t.TempDir(), "deliverable.yaml", reanchorManifest)
	out, _, err := runManifest(t, "apply", "-f", path, "--yes")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitGenericError || !strings.Contains(out, "binding writes: 1 sheet updated") {
		t.Fatalf("exit = %d, stdout = %q; error = %v", code, out, err)
	}
	if got, want := s.writes(), []string{patchData, postColumn}; !reflect.DeepEqual(got, want) {
		t.Fatalf("first writes = %v, want %v", got, want)
	}

	diff, _, err := runManifest(t, "diff", "-f", path)
	if code := qerrors.GetExitCode(err); code != 1 || !strings.HasPrefix(diff, "POST /deliverable-sheet-bindings/"+sheetsDataID+"/column-bindings\n") ||
		strings.Contains(diff, "PATCH ") || strings.Contains(diff, "DELETE ") {
		t.Fatalf("remaining diff = %q, exit = %d; error = %v", diff, code, err)
	}
	out, _, err = runManifest(t, "apply", "-f", path, "--yes")
	if err != nil || out != "deliverable/monthly-bordereau updated\nbinding writes: 1 column created\n" {
		t.Fatalf("resumed stdout = %q; error = %v", out, err)
	}
	if got, want := s.writes(), []string{patchData, postColumn, postColumn}; !reflect.DeepEqual(got, want) {
		t.Errorf("all writes = %v, want %v", got, want)
	}
	assertSeriesJSON(t, s.bodies[postColumn], `{"target_column_index":3,"source_type":"column","source_column":"claims","run_param":null,"format_override":null}`)
	out, _, err = runManifest(t, "apply", "-f", path, "--yes")
	if err != nil || out != "deliverable/monthly-bordereau unchanged\n" || len(s.writes()) != 3 {
		t.Errorf("converged stdout = %q, writes = %v; error = %v", out, s.writes(), err)
	}
}

func TestApplyDeliverableTemplateCopyRequiresRemovalConfirmation(t *testing.T) {
	for _, tc := range []struct {
		name, format, answer, message string
		yes, protected, named, writes bool
		code                          int
	}{
		{name: "noninteractive", message: "requires confirmation", code: qerrors.ExitBadUsage},
		{name: "protected despite yes", yes: true, protected: true, message: "must name it with --context", code: qerrors.ExitBadUsage},
		{name: "json despite prompt", format: "json", answer: "y\n", message: "--yes", code: qerrors.ExitBadUsage},
		{name: "yaml despite prompt", format: "yaml", answer: "y\n", message: "--yes", code: qerrors.ExitBadUsage},
		{name: "declined", answer: "n\n"},
		{name: "accepted", answer: "y\n", writes: true},
		{name: "yes", yes: true, writes: true},
		{name: "named protected context", yes: true, protected: true, named: true, writes: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.answer == "" {
				requireNonTTY(t)
			} else {
				answerPrompts(t, tc.answer)
			}
			s := sheetsServer(t)
			var source api.DeliverableRevisionResponse
			if err := json.Unmarshal([]byte(sheetsDraftTree), &source); err != nil {
				t.Fatal(err)
			}
			data := (*source.SheetBindings)[:1]
			source.ID, source.Release, source.State = uuid.MustParse(enabledRevisionID), "2026.1", api.DeliverableRevisionStateEnabled
			source.SheetBindings = &data
			s.RegisterHandler(http.MethodGet, revisionTestPath+enabledRevisionID, func(w http.ResponseWriter, r *http.Request) {
				checkDeliverableToken(t, r)
				testutil.RespondJSON(w, http.StatusOK, source)
			})
			// All listed columns match the source. MayRemove alone gates
			// the upload and copy, because replacement can move them.
			copiedData := data[0]
			copiedData.ID = uuid.New()
			columns := append([]api.ColumnBindingResponse{}, (*copiedData.ColumnBindings)...)
			for i := range columns {
				columns[i].ID = uuid.New()
			}
			copiedData.ColumnBindings = &columns
			templateID := mintedTemplateID
			created := api.DeliverableRevisionCreateResponse{ID: uuid.MustParse(newRevisionID), DeliverableID: uuid.MustParse(liveDeliverableID),
				Release: "2026.3", State: api.DeliverableRevisionStateDraft, SkeletonID: &templateID,
				CellBindings: &[]api.CellBindingResponse{}, SheetBindings: &[]api.SheetBindingResponse{copiedData}}
			s.answerFinalize(t, orgTemplateFinalize, http.StatusOK,
				strings.ReplaceAll(finalizedTemplate{id: templateID}.body(), `"Claims"`, `"Data"`))
			revisions := deliverableTestPath + "/" + liveDeliverableID + "/revisions"
			s.RegisterHandler(http.MethodPost, revisions, func(w http.ResponseWriter, r *http.Request) {
				checkDeliverableToken(t, r)
				s.bodies[http.MethodPost+" "+revisions] = readBody(t, r)
				testutil.RespondJSON(w, http.StatusOK, created)
			})
			s.RegisterHandler(http.MethodGet, revisionTestPath+newRevisionID, func(w http.ResponseWriter, r *http.Request) {
				checkDeliverableToken(t, r)
				testutil.RespondJSON(w, http.StatusOK, created)
			})
			cfg, err := config.Load()
			if err != nil {
				t.Fatal(err)
			}
			if tc.protected {
				cfg.Contexts[cfg.CurrentContext].Protected = nil
				if err := cfg.Save(); err != nil {
					t.Fatal(err)
				}
			}
			manifest := strings.Replace(sheetsManifest(sheetsData, sheetsDataColumns), `release: "2026.2"`, "release: \"2026.3\"\n  template: claims.xlsx", 1)
			dir := t.TempDir()
			writeManifest(t, dir, "claims.xlsx", string(templateFileBytes))
			path := writeManifest(t, dir, "deliverable.yaml", manifest)
			args := []string{"apply", "-f", path}
			if tc.yes {
				args = append(args, "--yes")
			}
			if tc.format != "" {
				args = append(args, "-o", tc.format)
			}
			var out string
			if tc.named {
				out, err = runRootWithContextFlag(t, append(args, "--context", cfg.CurrentContext)...)
			} else {
				out, _, err = runManifest(t, args...)
			}
			if code := qerrors.GetExitCode(err); code != tc.code {
				t.Fatalf("exit = %d, want %d; error = %v", code, tc.code, err)
			}
			if tc.message != "" && !strings.Contains(err.Error(), tc.message) {
				t.Errorf("error = %v, want %q", err, tc.message)
			}
			var want []string
			if tc.writes {
				want = []string{orgTemplateUploadURL, templateStoragePUT, orgTemplateFinalize, "POST " + revisions}
				assertSeriesJSON(t, s.bodies["POST "+revisions], `{"release":"2026.3","copy_from_revision_id":"`+enabledRevisionID+`","skeleton_id":41,"acknowledge_lossy":false}`)
			}
			if got := s.writes(); !reflect.DeepEqual(got, want) {
				t.Errorf("writes = %v, want %v", got, want)
			}
			if tc.answer != "" && tc.format == "" && (!strings.Contains(out, "the server may move column bindings") || strings.Contains(out, "DELETE ")) {
				t.Errorf("confirmation should describe possible removals with no planned DELETE:\n%s", out)
			}
		})
	}
}
