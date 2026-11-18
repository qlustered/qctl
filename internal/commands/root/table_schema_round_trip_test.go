package root

import (
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/datasets"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"gopkg.in/yaml.v3"
)

// atlasSchemaDetailJSON is a migration-details answer in the shape atlas
// builds it: capi/capi/controllers/migration_controller.py
// _extract_field_properties writes every key for every column, with
// is_nullable defaulting to true, indexed and is_deleted to false, and the
// rest to null. legacy is a deleted column.
const atlasSchemaDetailJSON = `{
  "confirmed": true, "created_at": "2026-07-01T10:00:00Z", "updated_at": "2026-07-02T10:00:00Z",
  "dataset_id": 42, "dataset_name": "orders",
  "diff_summary": {"total_fields": 3},
  "incompatible_overrides": null,
  "fields": {
    "amount":   {"action": null, "diffs": null, "current": {"type": "String", "args": [64], "is_nullable": true,  "indexed": false, "is_deleted": false,
                 "is_dollar": null, "is_percent": null, "display_decimals": null, "datetime_formats": null, "generated_by": null}},
    "currency": {"action": null, "diffs": null, "current": {"type": "String", "args": [3],  "is_nullable": false, "indexed": false, "is_deleted": false,
                 "is_dollar": null, "is_percent": null, "display_decimals": null, "datetime_formats": null, "generated_by": null}},
    "legacy":   {"action": null, "diffs": null, "current": {"type": "String", "args": [10], "is_nullable": true,  "indexed": false, "is_deleted": true,
                 "is_dollar": null, "is_percent": null, "display_decimals": null, "datetime_formats": null, "generated_by": null}}
  },
  "migration_id": 5, "migration_model_version_id": 9, "settings_model_version_id": 12,
  "migration_state": "current_only", "state": "current"
}`

// The documented edit loop, run through the real tree against an atlas-shaped
// schema: `get table-schema -o yaml`, edit one column, `diff -f`, `apply -f`.
// An unedited file must send nothing, and an edited one must send exactly the
// edit -- not exit 2 on the `is_deleted: false` atlas reports on every live
// column.
func TestTableSchemaRoundTripFromAnAtlasShapedSchema(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	env.SetupConfigWithOrg(mock.URL(), "", scopeTestOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, scopeTestOrgID, "token")

	base := "/api/orgs/" + scopeTestOrgID + "/datasets/42"
	mock.RegisterHandler("GET", base+"/migration-details", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(atlasSchemaDetailJSON))
	})
	var patches []string
	mock.RegisterHandler("PATCH", base+"/migrations", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		patches = append(patches, string(b))
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{
  "settings_version_id": 13, "draft_version_id": 10, "redirect_post_submit_to": null,
  "migration": {"id": 6, "confirmed": false, "created_at": "2026-07-03T10:00:00Z",
    "dataset_id": 42, "dataset_name": "orders", "down_id": 5,
    "incompatible_overrides": null, "model": {}, "settings_model_id": 3,
    "state": "draft", "suggested_field_validation_with_allowed_types": null, "updated_at": null}
}`))
	})
	// --yes is a persistent flag on the shared tree's apply command.
	t.Cleanup(func() {
		if apply, _, err := GetRootCmd().Find([]string{"apply"}); err == nil {
			if f := apply.PersistentFlags().Lookup("yes"); f != nil {
				_ = f.Value.Set("false")
				f.Changed = false
			}
		}
	})

	emitted, err := runRoot(t, "get", "table-schema", "42", "-o", "yaml")
	if err != nil {
		t.Fatalf("get table-schema -o yaml: %v", err)
	}
	// runRoot restores -o only at cleanup, and the verbs below take the default.
	resetPersistentFlag(t, GetRootCmd(), "output", "table")
	var m datasets.TableSchemaManifestWithStatus
	if err := yaml.Unmarshal([]byte(emitted), &m); err != nil {
		t.Fatalf("the emitted manifest does not parse: %v\n%s", err, emitted)
	}
	deleted := true
	for name, want := range map[string]*bool{"amount": nil, "currency": nil, "legacy": &deleted} {
		f := m.Spec.Fields[name]
		if f == nil {
			t.Fatalf("%s is missing from the emitted manifest:\n%s", name, emitted)
		}
		if !reflect.DeepEqual(f.IsDeleted, want) {
			t.Errorf("%s.is_deleted = %v, want %v -- only a deleted column states it:\n%s", name, ptrText(f.IsDeleted), ptrText(want), emitted)
		}
	}

	unedited := env.CreateFile("unedited.yaml", emitted)
	if _, err := runRoot(t, "diff", "-f", unedited); err != nil {
		t.Errorf("diff -f of the unedited file = %v (exit %d), want no differences", err, qerrors.GetExitCode(err))
	}
	out, err := runRoot(t, "apply", "-f", unedited, "--yes")
	if err != nil || !strings.Contains(out, "table-schema/orders unchanged") {
		t.Errorf("apply -f of the unedited file = %v (exit %d), want unchanged; got:\n%s", err, qerrors.GetExitCode(err), out)
	}
	if len(patches) != 0 {
		t.Fatalf("the unedited file sent %d PATCH(es): %v", len(patches), patches)
	}

	// Retype amount and nothing else.
	m.Spec.Fields["amount"].Type = "Decimal"
	m.Spec.Fields["amount"].Args = &[]int{12, 2}
	editedYAML, err := yaml.Marshal(&m)
	if err != nil {
		t.Fatalf("marshal the edited manifest: %v", err)
	}
	edited := env.CreateFile("edited.yaml", string(editedYAML))

	out, err = runRoot(t, "diff", "-f", edited)
	if code := qerrors.GetExitCode(err); code != 1 {
		t.Fatalf("diff -f of the edited file exit = %d (err %v), want 1 (differences)", code, err)
	}
	if !strings.Contains(out, "type: String → Decimal") {
		t.Errorf("diff does not show the retype:\n%s", out)
	}

	out, err = runRoot(t, "apply", "-f", edited, "--yes")
	if err != nil {
		t.Fatalf("apply -f of the edited file = %v (exit %d); output:\n%s", err, qerrors.GetExitCode(err), out)
	}
	if len(patches) != 1 {
		t.Fatalf("PATCHes = %d, want 1", len(patches))
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(patches[0]), &body); err != nil {
		t.Fatalf("PATCH body is not JSON: %q", patches[0])
	}
	want := map[string]any{
		"amount": map[string]any{
			"type": "Decimal", "args": []any{float64(12), float64(2)},
			"is_nullable": true, "indexed": false,
		},
	}
	if got := body["schema_overrides"]; !reflect.DeepEqual(got, want) {
		t.Errorf("schema_overrides = %#v\nwant %#v", got, want)
	}
}

func ptrText(b *bool) string {
	if b == nil {
		return "absent"
	}
	if *b {
		return "true"
	}
	return "false"
}
