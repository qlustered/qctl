package datasets

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"gopkg.in/yaml.v3"
)

// mustSchemaDetail decodes a migration-details fixture, so tests read as the
// wire shape rather than as hand-built generated structs (FieldDatabaseDetailDict
// .Type is a union whose only reliable constructor is JSON).
func mustSchemaDetail(raw string) *TableSchemaDetail {
	var d TableSchemaDetail
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		panic("schema fixture: " + err.Error())
	}
	return &d
}

// mustFieldInfo parses one field's entry of that response, for a test that
// adds a column to the fixture. Going through JSON is not ceremony: the
// definition's `type` is a generated union, and JSON is the only way to build
// one without reaching into client.gen.go.
func mustFieldInfo(raw string) FieldSchemaInfo {
	var f FieldSchemaInfo
	if err := json.Unmarshal([]byte(raw), &f); err != nil {
		panic("field fixture: " + err.Error())
	}
	return f
}

// schemaDetailFixture is the live schema every manifest test reads from:
// a draft on top of a current schema, with one field the draft removes.
func schemaDetailFixture() *TableSchemaDetail {
	return mustSchemaDetail(`{
  "confirmed": false,
  "created_at": "2026-07-09T10:00:00Z",
  "updated_at": "2026-07-11T07:00:00Z",
  "dataset_id": 42,
  "dataset_name": "orders",
  "diff_summary": {"total_fields": 4},
  "fields": {
    "id":       {"current": {"type": "Integer", "is_nullable": false, "indexed": true}},
    "amount":   {"action": "modified",
                 "current": {"type": "String", "args": [64], "is_nullable": true},
                 "new":     {"type": "String", "args": [64], "is_nullable": true, "display_decimals": 0, "original_name": "Amount ($)"}},
    "currency": {"current": {"type": "String", "args": [3], "is_nullable": false},
                 "new":     {"type": "String", "args": [3], "is_nullable": false}},
    "legacy":   {"action": "removed",
                 "current": {"type": "String", "args": [10], "is_nullable": true, "is_deleted": true}}
  },
  "migration_id": 91,
  "migration_model_version_id": 12,
  "settings_model_version_id": 7,
  "migration_state": "draft_with_current",
  "state": "draft"
}`)
}

// TestSchemaDetailToManifest_ShapeAndOmissions pins the three rules that make
// the file editable by hand: the draft's definition wins, an absent property is
// omitted rather than emitted as null, and original_name never appears.
func TestSchemaDetailToManifest_ShapeAndOmissions(t *testing.T) {
	m := SchemaDetailToManifest(schemaDetailFixture())

	if m.APIVersion != "qluster.ai/v1" || m.Kind != TableSchemaKind {
		t.Fatalf("envelope = %s/%s, want qluster.ai/v1/TableSchema", m.APIVersion, m.Kind)
	}
	if m.Metadata.Name != "orders" || m.Metadata.ID != "42" {
		t.Errorf("metadata = %+v, want name=orders id=42", m.Metadata)
	}
	if m.Status == nil || m.Status.SettingsModelVersionID != 7 || m.Status.MigrationModelVersionID != 12 ||
		m.Status.MigrationID != 91 || m.Status.SchemaState != "draft_with_current" || m.Status.State != "draft" ||
		m.Status.Confirmed {
		t.Errorf("status = %+v", m.Status)
	}

	amount, ok := m.Spec.Fields["amount"]
	if !ok {
		t.Fatal("amount missing from spec.fields")
	}
	// The draft's definition, not current's: original_name is only on `new`.
	if amount.Type != "String" || amount.Args == nil || len(*amount.Args) != 1 || (*amount.Args)[0] != 64 {
		t.Errorf("amount = %+v, want type String args [64]", amount)
	}
	if amount.OriginalName != nil {
		t.Error("original_name must never be emitted: it is inference provenance, not desired state")
	}
	if amount.DisplayDecimals == nil || *amount.DisplayDecimals != 0 {
		t.Errorf("amount.display_decimals = %v, want pointer to 0 (zero must not be mistaken for omission)", amount.DisplayDecimals)
	}
	if amount.Indexed != nil || amount.TrgmIndexed != nil || amount.IsDollar != nil {
		t.Errorf("absent properties must be omitted, not emitted: %+v", amount)
	}
	// A draft-removed field falls back to current, matching the grid.
	if _, ok := m.Spec.Fields["legacy"]; !ok {
		t.Error("a draft-removed field must still be emitted, matching the grid")
	}
	if m.Spec.Fields["legacy"].IsDeleted == nil || !*m.Spec.Fields["legacy"].IsDeleted {
		t.Error("legacy.is_deleted must round-trip")
	}
}

// TestSchemaManifestYAMLOmitsNothingItShouldEmit is the readability guard: the
// emitted YAML must not carry `null` at property level, because `null` at field
// level means "clear this override" and the two must not be confusable.
func TestSchemaManifestYAMLOmitsNothingItShouldEmit(t *testing.T) {
	out, err := yaml.Marshal(SchemaDetailToManifest(schemaDetailFixture()))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasSuffix(trimmed, ": null") {
			t.Errorf("emitted a null property, which collides with the field-level null: %q", trimmed)
		}
	}
	if !strings.Contains(string(out), "kind: TableSchema") {
		t.Errorf("manifest does not declare its kind:\n%s", out)
	}
}

func TestParseTableSchemaManifest_NullFieldIsPresentAndNil(t *testing.T) {
	m, err := ParseTableSchemaManifest([]byte(`
apiVersion: qluster.ai/v1
kind: TableSchema
metadata:
  id: 42
spec:
  fields:
    legacy: null
    amount:
      type: Decimal
      args: [12, 2]
`), "f.yaml")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if m.Metadata.ID != "42" {
		t.Errorf("metadata.id = %q, want \"42\" (yaml.v3 coerces the int)", m.Metadata.ID)
	}
	f, ok := m.Spec.Fields["legacy"]
	if !ok {
		t.Fatal("`legacy: null` must leave the key present")
	}
	if f != nil {
		t.Errorf("`legacy: null` must parse to a nil *FieldOverride, got %+v", f)
	}
	if m.Spec.Fields["amount"] == nil || m.Spec.Fields["amount"].Type != "Decimal" {
		t.Errorf("amount = %+v", m.Spec.Fields["amount"])
	}
}

func TestParseTableSchemaManifest_Rejections(t *testing.T) {
	cases := []struct{ name, doc string }{
		{"wrong apiVersion", "apiVersion: v1\nkind: TableSchema\nmetadata: {id: 42}\nspec: {fields: {}}\n"},
		{"wrong kind", "apiVersion: qluster.ai/v1\nkind: Table\nmetadata: {id: 42}\nspec: {fields: {}}\n"},
		{"no table named", "apiVersion: qluster.ai/v1\nkind: TableSchema\nmetadata: {}\nspec: {fields: {}}\n"},
		{"unknown property", "apiVersion: qluster.ai/v1\nkind: TableSchema\nmetadata: {id: 42}\nspec:\n  fields:\n    a: {type: String, bogus: 1}\n"},
		{"original_name written", "apiVersion: qluster.ai/v1\nkind: TableSchema\nmetadata: {id: 42}\nspec:\n  fields:\n    a: {type: String, original_name: Amount ($)}\n"},
		// `qctl explain table-schema` marks spec and spec.fields required.
		// Both of these used to parse, resolve the table over the network and
		// report "unchanged" at exit 0 -- a documented rule nothing enforced.
		{"no spec block", "apiVersion: qluster.ai/v1\nkind: TableSchema\nmetadata: {id: 42}\n"},
		{"empty spec", "apiVersion: qluster.ai/v1\nkind: TableSchema\nmetadata: {id: 42}\nspec: {}\n"},
		{"empty spec.fields", "apiVersion: qluster.ai/v1\nkind: TableSchema\nmetadata: {id: 42}\nspec: {fields: {}}\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseTableSchemaManifest([]byte(tc.doc), "f.yaml")
			if err == nil {
				t.Fatal("expected an error")
			}
			if code := qerrors.GetExitCode(err); code != 2 {
				t.Errorf("GetExitCode = %d, want 2", code)
			}
		})
	}
}

// TestParseTableSchemaManifest_NilBlockDoesNotHideALaterOffender pins the
// nil-skip in the original_name scan as a *continue*, not a bail-out. The names
// are scanned in sorted order, so `beta: null` is reached first: a loop that
// returned early on nil -- or dereferenced it -- would let `zeta`'s
// original_name through, and no other test would notice.
//
// The complementary case, a nil block beside a clean block parsing without
// error, is already covered by TestParseTableSchemaManifest_NullFieldIsPresentAndNil
// on the same document; it is deliberately not restated here.
func TestParseTableSchemaManifest_NilBlockDoesNotHideALaterOffender(t *testing.T) {
	_, err := ParseTableSchemaManifest([]byte(`
apiVersion: qluster.ai/v1
kind: TableSchema
metadata:
  id: 42
spec:
  fields:
    beta: null
    zeta:
      type: String
      original_name: Z
`), "f.yaml")
	if err == nil {
		t.Fatal("a nil block before an offending one must not hide it")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("GetExitCode = %d, want 2", code)
	}
}

// A status block round-trips harmlessly: apply must tolerate the block `get`
// emits, exactly as `apply -f <Table>` tolerates TableManifestWithStatus.
func TestParseTableSchemaManifest_ToleratesStatus(t *testing.T) {
	emitted, err := yaml.Marshal(SchemaDetailToManifest(schemaDetailFixture()))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := ParseTableSchemaManifest(emitted, "f.yaml"); err != nil {
		t.Fatalf("apply must accept exactly what get emits: %v", err)
	}
}

func TestSchemaTypeNameDropsArgs(t *testing.T) {
	d := schemaDetailFixture().Fields["amount"].New
	if got := SchemaTypeName(d); got != "String" {
		t.Errorf("SchemaTypeName = %q, want String (no args)", got)
	}
	if got := FormatFieldType(d); got != "String(64)" {
		t.Errorf("FormatFieldType = %q, want String(64) (args kept)", got)
	}
	if got := SchemaTypeName(nil); got != "" {
		t.Errorf("SchemaTypeName(nil) = %q, want \"\"", got)
	}
	_ = api.CurrentOnly // keep the api import honest across edits
}

// TestSettingsModelHasNoSchemaOverrides guards the second write path the spec
// (§3.5) deliberately leaves uncovered. See SettingsModel's own comment for
// why adding the field would be a privilege escalation rather than a
// completeness fix.
func TestSettingsModelHasNoSchemaOverrides(t *testing.T) {
	rt := reflect.TypeOf(SettingsModel{})
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		// The field NAME is checked as well as the tags, and that is not
		// redundant: Tag.Get returns "" for an untagged field, so a bare
		// `SchemaOverrides map[string]any` -- which yaml.v3 and encoding/json
		// would still marshal, under a defaulted key -- slips past a tag-only
		// scan. Both spellings must trip this. Do not "simplify" one away.
		tripped := strings.EqualFold(f.Name, "SchemaOverrides")
		for _, tag := range []string{"yaml", "json"} {
			tripped = tripped || strings.HasPrefix(f.Tag.Get(tag), "schema_overrides")
		}
		if tripped {
			t.Fatalf("SettingsModel.%s exposes schema_overrides on the Table manifest.\n"+
				"That route changes a schema under DATASET_CONFIGS_EDIT with no version check, no draft, "+
				"no review alert and no MIGRATION_EXECUTE. Use kind: TableSchema instead.", f.Name)
		}
	}
}

// TestParseTableSchemaManifest_DisplayDecimalsNullIsARequest pins the one
// property a block may state as null. Every spelling of a YAML null is the
// same request, and an omitted line is not that request -- the distinction no
// field of FieldOverride can carry on its own, because yaml.v3 writes the
// destination's zero value for a null scalar without consulting it.
func TestParseTableSchemaManifest_DisplayDecimalsNullIsARequest(t *testing.T) {
	m, err := ParseTableSchemaManifest([]byte(`
apiVersion: qluster.ai/v1
kind: TableSchema
metadata: {id: 42}
spec:
  fields:
    explicit:
      type: String
      display_decimals: null
    tilde:
      type: String
      display_decimals: ~
    empty:
      type: String
      display_decimals:
    valued:
      type: String
      display_decimals: 3
    omitted:
      type: String
    cleared: null
`), "f.yaml")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, name := range []string{"explicit", "tilde", "empty"} {
		f := m.Spec.Fields[name]
		if f == nil {
			t.Errorf("%s: block missing", name)
			continue
		}
		if !f.ResetDisplayDecimals {
			t.Errorf("%s: `display_decimals: null` must read as a reset request, got %+v", name, f)
		}
		if f.DisplayDecimals != nil {
			t.Errorf("%s: a reset carries no number, got %d", name, *f.DisplayDecimals)
		}
	}
	// The controls. Without them every assertion above is satisfied by a
	// marker that is always true, which is precisely the shape the old
	// behaviour had in reverse.
	if f := m.Spec.Fields["valued"]; f.ResetDisplayDecimals || f.DisplayDecimals == nil || *f.DisplayDecimals != 3 {
		t.Errorf("valued = %+v, want display_decimals 3 and no reset", f)
	}
	if f := m.Spec.Fields["omitted"]; f.ResetDisplayDecimals {
		t.Error("an omitted display_decimals keeps the live value and is not a reset")
	}
	if f, ok := m.Spec.Fields["cleared"]; !ok || f != nil {
		t.Errorf("`cleared: null` must stay a present, nil block (present=%v, block=%+v)", ok, f)
	}
}

// An emitted file asks for no reset: `get table-schema -o yaml` omits
// display_decimals for a column that has none, and an omitted line is not a
// null. This is the half of the round trip the identity test in
// schema_overrides_test.go cannot see, because a reset of an already-unset
// column sends nothing either way.
func TestSchemaDetailToManifest_EmitsNoResetRequest(t *testing.T) {
	emitted, err := yaml.Marshal(SchemaDetailToManifest(schemaDetailFixture()))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	m, err := ParseTableSchemaManifest(emitted, "f.yaml")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for name, f := range m.Spec.Fields {
		if f != nil && f.ResetDisplayDecimals {
			t.Errorf("round-tripping the emitted manifest asks to reset %q's display_decimals:\n%s", name, emitted)
		}
	}
	// The control: the field that HAS a value must still round-trip it, or the
	// loop above passes over a manifest carrying nothing.
	if f := m.Spec.Fields["amount"]; f == nil || f.DisplayDecimals == nil || *f.DisplayDecimals != 0 {
		t.Errorf("amount.display_decimals did not round-trip: %+v", f)
	}
}

// is_deleted is emitted only for a deleted column. A stated is_deleted is a
// request whichever value it holds, so copying the false the details route
// reports on a live column put it into every block of the emitted file.
func TestSchemaDetailToManifest_EmitsIsDeletedOnlyForADeletedColumn(t *testing.T) {
	detail := mustSchemaDetail(`{
  "dataset_id": 42, "dataset_name": "orders",
  "fields": {
    "live_false":     {"current": {"type": "String", "args": [8], "is_nullable": true, "is_deleted": false}},
    "live_absent":    {"current": {"type": "String", "args": [8], "is_nullable": true}},
    "live_null":      {"current": {"type": "String", "args": [8], "is_nullable": true, "is_deleted": null}},
    "deleted":        {"current": {"type": "String", "args": [8], "is_nullable": true, "is_deleted": true}},
    "draft_deletes":  {"action": "removed",
                       "current": {"type": "String", "args": [8], "is_nullable": false, "is_deleted": false},
                       "new":     {"type": "String", "args": [8], "is_nullable": true, "is_deleted": true}},
    "draft_restores": {"action": "added",
                       "current": {"type": "String", "args": [8], "is_nullable": true, "is_deleted": true},
                       "new":     {"type": "String", "args": [8], "is_nullable": true, "is_deleted": false}}
  },
  "migration_state": "draft_with_current", "state": "draft"
}`)
	m := SchemaDetailToManifest(detail)
	for name, wantDeleted := range map[string]bool{
		"live_false": false, "live_absent": false, "live_null": false,
		"deleted": true, "draft_deletes": true, "draft_restores": false,
	} {
		f := m.Spec.Fields[name]
		if f == nil {
			t.Fatalf("%s missing from spec.fields", name)
		}
		switch {
		case wantDeleted && (f.IsDeleted == nil || !*f.IsDeleted):
			t.Errorf("%s.is_deleted = %v, want true: a deleted column states it", name, f.IsDeleted)
		case !wantDeleted && f.IsDeleted != nil:
			t.Errorf("%s.is_deleted = %v, want omitted: a live column states none", name, *f.IsDeleted)
		}
	}
	out, err := yaml.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(out), "is_deleted: false") {
		t.Errorf("emitted `is_deleted: false`:\n%s", out)
	}
}
