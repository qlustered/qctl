package datasets

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
)

// settingsEditManifest is a table manifest whose only settings edit is the one
// the bug report used: two more entries under datetime_formats.
func settingsEditManifest(sm *SettingsModel) *TableManifest {
	return &TableManifest{
		APIVersion: pkgmanifest.APIVersionV1,
		Kind:       "Table",
		Metadata:   pkgmanifest.Metadata{Name: "orders"},
		Spec: TableSpec{
			MigrationPolicy:    "ask_user",
			DataLoadingProcess: "append",
			SettingsModel:      sm,
		},
	}
}

// TestPatchCarriesSettingsModelVersion covers the reported 409: the backend
// compares body.settings_model.version_id with the stored settings model and
// refuses the write when they differ, and api.SettingsSchemaOptional serialises
// id/version_id unconditionally, so a block built from zero always mismatched.
func TestPatchCarriesSettingsModelVersion(t *testing.T) {
	formats := []string{"%Y-%m-%d", "%d/%m/%Y"}
	existing := &DatasetFull{
		ID:            3,
		VersionID:     1,
		SettingsModel: SettingsSchema{ID: 7, VersionID: 2},
	}

	req, err := manifestToPatchRequest(existing, settingsEditManifest(&SettingsModel{
		DatetimeFormats: &formats,
	}))
	if err != nil {
		t.Fatalf("manifestToPatchRequest: %v", err)
	}

	if req.VersionID != 1 {
		t.Errorf("dataset version_id = %d, want 1", req.VersionID)
	}
	if req.SettingsModel == nil {
		t.Fatal("settings_model block missing from the patch")
	}
	if req.SettingsModel.ID != 7 {
		t.Errorf("settings_model.id = %d, want 7", req.SettingsModel.ID)
	}
	if req.SettingsModel.VersionID != 2 {
		t.Errorf("settings_model.version_id = %d, want 2", req.SettingsModel.VersionID)
	}
}

// TestPatchPreservesUnsetSettingsFields covers the second half of the same bug:
// the backend applies every key the raw body carries, and a null on a nullable
// column writes SQL NULL, so a field the manifest never mentioned must go out
// with its stored value rather than as null.
func TestPatchPreservesUnsetSettingsFields(t *testing.T) {
	formats := []string{"%Y-%m-%d"}
	existing := &DatasetFull{
		ID:        3,
		VersionID: 1,
		SettingsModel: SettingsSchema{
			ID:                          7,
			VersionID:                   2,
			DatetimeFormats:             &[]string{"%d/%m/%Y"},
			FieldsToExpand:              &[]string{"payload"},
			DataMarkedAsValidForDBField: &map[string][]string{"status": {"ok"}},
			KeyColumns:                  &[]string{"order_id"},
			IgnoreColumnNames:           []string{"internal_"},
			ValueMapping:                map[string]string{"Y": "yes"},
		},
	}

	req, err := manifestToPatchRequest(existing, settingsEditManifest(&SettingsModel{
		DatetimeFormats: &formats,
	}))
	if err != nil {
		t.Fatalf("manifestToPatchRequest: %v", err)
	}

	sm := req.SettingsModel
	if sm == nil {
		t.Fatal("settings_model block missing from the patch")
	}
	if sm.DatetimeFormats == nil || len(*sm.DatetimeFormats) != 1 || (*sm.DatetimeFormats)[0] != "%Y-%m-%d" {
		t.Errorf("datetime_formats = %v, want the manifest's single entry", sm.DatetimeFormats)
	}

	// Nullable columns: a null here is a silent wipe.
	if sm.FieldsToExpand == nil || len(*sm.FieldsToExpand) != 1 {
		t.Errorf("fields_to_expand = %v, want it preserved from the read", sm.FieldsToExpand)
	}
	if sm.DataMarkedAsValidForDBField == nil || len(*sm.DataMarkedAsValidForDBField) != 1 {
		t.Errorf("data_marked_as_valid_for_db_field = %v, want it preserved from the read", sm.DataMarkedAsValidForDBField)
	}
	// key_columns is nullable too. Since qctl-o22 the manifest CAN express it,
	// which is exactly why this case still matters: a manifest that leaves it out
	// must leave the stored keys alone, and re-keying a sync dataset is a change
	// the backend refuses outright once the table has ingested.
	if sm.KeyColumns == nil || len(*sm.KeyColumns) != 1 {
		t.Errorf("key_columns = %v, want it preserved from the read", sm.KeyColumns)
	}
	// NOT NULL columns: the server would skip a null, but sending one still
	// misrepresents the request.
	if sm.IgnoreColumnNames == nil || len(*sm.IgnoreColumnNames) != 1 {
		t.Errorf("ignore_column_names = %v, want it preserved from the read", sm.IgnoreColumnNames)
	}
	if sm.ValueMapping == nil || len(*sm.ValueMapping) != 1 {
		t.Errorf("value_mapping = %v, want it preserved from the read", sm.ValueMapping)
	}
}

// TestPatchClearsSettingsFieldsWrittenEmpty pins the other side of the overlay:
// an omitted key keeps the stored value, but a key written as an empty list or
// map is an edit and must reach the server as [] / {}.
func TestPatchClearsSettingsFieldsWrittenEmpty(t *testing.T) {
	empty := []string{}
	existing := &DatasetFull{
		ID:        3,
		VersionID: 1,
		SettingsModel: SettingsSchema{
			ID:                7,
			VersionID:         2,
			IgnoreColumnNames: []string{"internal_"},
			ValueMapping:      map[string]string{"Y": "yes"},
		},
	}

	req, err := manifestToPatchRequest(existing, settingsEditManifest(&SettingsModel{
		IgnoreColumnNames: &empty,
		ValueMapping:      &map[string]string{},
	}))
	if err != nil {
		t.Fatalf("manifestToPatchRequest: %v", err)
	}

	sm := req.SettingsModel
	if sm == nil {
		t.Fatal("settings_model block missing from the patch")
	}
	if sm.IgnoreColumnNames == nil || len(*sm.IgnoreColumnNames) != 0 {
		t.Errorf("ignore_column_names = %v, want an empty list", sm.IgnoreColumnNames)
	}
	if sm.ValueMapping == nil || len(*sm.ValueMapping) != 0 {
		t.Errorf("value_mapping = %v, want an empty map", sm.ValueMapping)
	}
}

// TestPatchOmitsSettingsModelWhenTheManifestHasNone keeps the block off the wire
// for a manifest that does not set spec.settings_model: sending one would submit
// the nested version check and rewrite every settings column for no reason.
func TestPatchOmitsSettingsModelWhenTheManifestHasNone(t *testing.T) {
	existing := &DatasetFull{
		ID:            3,
		VersionID:     1,
		SettingsModel: SettingsSchema{ID: 7, VersionID: 2},
	}

	req, err := manifestToPatchRequest(existing, settingsEditManifest(nil))
	if err != nil {
		t.Fatalf("manifestToPatchRequest: %v", err)
	}
	if req.SettingsModel != nil {
		t.Errorf("settings_model = %+v, want it absent", req.SettingsModel)
	}

	// The wire, not just the field: settings_model is one of the four keys on
	// api.DataSetPatchRequestSchema carrying omitempty, and it is that omitempty
	// which turns a nil into an absent key rather than a null.
	raw, err := json.Marshal(datasetPatchWireBody{body: &req})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := body["settings_model"]; ok {
		t.Error("settings_model is on the wire; it must be omitted entirely")
	}
}

// TestSettingsPatchDropsSchemaOverrides: schema_overrides is deliberately absent
// from the manifest (TestSettingsModelHasNoSchemaOverrides) because PATCH writes
// it with none of the guards `kind: TableSchema` goes through. Seeding the patch
// from the read must not smuggle it back in.
func TestSettingsPatchDropsSchemaOverrides(t *testing.T) {
	existing := &SettingsSchema{
		ID:              7,
		VersionID:       2,
		SchemaOverrides: &map[string]api.OverridableFieldDetailDict{"amount": {}},
	}

	req, err := settingsPatchFromExisting(existing, &SettingsModel{})
	if err != nil {
		t.Fatalf("settingsPatchFromExisting: %v", err)
	}
	if req.SchemaOverrides != nil {
		t.Errorf("schema_overrides = %v, want it dropped from the patch", req.SchemaOverrides)
	}

	raw, err := json.Marshal(datasetPatchWireBody{body: &api.DataSetPatchRequestSchema{SettingsModel: req}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var settings map[string]json.RawMessage
	if err := json.Unmarshal(body["settings_model"], &settings); err != nil {
		t.Fatalf("decode settings_model: %v", err)
	}
	if _, ok := settings["schema_overrides"]; ok {
		t.Error("schema_overrides is on the wire; it must be omitted entirely")
	}
}

// TestSettingsPatchOverlaysTheThreeLateSettings walks the whole update path for
// the keys qctl-o22 added: a read that already holds all three, a manifest that
// changes one and re-states one unchanged, and the wire body that comes out.
//
// It matters most for key_columns, whose failure mode is invisible from
// qctl. Re-keying a sync dataset is refused outright once the table has ingested
// (atlas capi/capi/views/settings_view_core.py patch_dataset_core), so a
// dropped edit here does not surface as a rejected request -- the server answers
// 200 over the value it already had, and apply reports success.
func TestSettingsPatchOverlaysTheThreeLateSettings(t *testing.T) {
	existing := &DatasetFull{
		ID:        3,
		VersionID: 1,
		SettingsModel: SettingsSchema{
			ID:                                  7,
			VersionID:                           2,
			KeyColumns:                          &[]string{"order_id"},
			LimitFieldsInSignatureCalculationTo: &[]string{"order_id"},
			IgnoreColumnNamesThatHaveAnyOfWords: []string{"internal"},
		},
	}

	req, err := manifestToPatchRequest(existing, settingsEditManifest(&SettingsModel{
		KeyColumns:                          &[]string{"order_id", "line_no"},
		IgnoreColumnNamesThatHaveAnyOfWords: &[]string{"internal", "debug"},
	}))
	if err != nil {
		t.Fatalf("manifestToPatchRequest: %v", err)
	}

	raw, err := json.Marshal(req.SettingsModel)
	if err != nil {
		t.Fatalf("marshal settings block: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("unmarshal settings block: %v", err)
	}

	for key, want := range map[string][]any{
		"key_columns": {"order_id", "line_no"},
		"ignore_column_names_that_have_any_of_words": {"internal", "debug"},
		// Untouched by the manifest, and nullable: the read is the only thing
		// keeping it off the wire as null.
		"limit_fields_in_signature_calculation_to": {"order_id"},
	} {
		if got := body[key]; !reflect.DeepEqual(got, want) {
			t.Errorf("settings_model.%s = %v, want %v", key, got, want)
		}
	}
}

// TestSettingsPatchBaseCarriesEveryField is the tripwire behind settingsPatchBase's
// JSON round trip. Every field the read returns must reach the patch, because a
// field left behind goes out as null and the backend applies that null. It fills
// api.SettingsSchema exhaustively by reflection rather than by name, so a column
// a later spec adds is covered the day it appears.
func TestSettingsPatchBaseCarriesEveryField(t *testing.T) {
	seeded := &SettingsSchema{}
	sv := reflect.ValueOf(seeded).Elem()
	for i := 0; i < sv.NumField(); i++ {
		fillNonZero(sv.Field(i))
	}

	base, err := settingsPatchBase(seeded)
	if err != nil {
		t.Fatalf("settingsPatchBase: %v", err)
	}

	// schema_overrides is dropped on purpose; TestSettingsPatchDropsSchemaOverrides
	// owns that. Every other field must have survived.
	dropped := map[string]bool{"SchemaOverrides": true}

	v := reflect.ValueOf(base).Elem()
	for i := 0; i < v.NumField(); i++ {
		name := v.Type().Field(i).Name
		if dropped[name] {
			if !v.Field(i).IsZero() {
				t.Errorf("%s survived into the patch; it is meant to be dropped", name)
			}
			continue
		}
		if v.Field(i).IsZero() {
			t.Errorf("%s is not carried from the read, so it goes out as null", name)
		}
	}
}

// TestSettingsPatchBaseSurvivesNoSettingsModel: a table with no settings model
// reads back as a zero SettingsSchema, which must not be an error.
func TestSettingsPatchBaseSurvivesNoSettingsModel(t *testing.T) {
	base, err := settingsPatchBase(nil)
	if err != nil {
		t.Fatalf("settingsPatchBase(nil): %v", err)
	}
	if base == nil {
		t.Fatal("settingsPatchBase(nil) = nil, want an empty block")
	}
}

// fillNonZero gives v a distinctive non-zero value for its type, deep enough to
// survive a JSON round trip. A struct is left alone — the caller walks the
// top-level fields itself, and a struct reached through a map or slice only has
// to leave its container non-empty. That also keeps a self-referential generated
// type from recursing forever.
func fillNonZero(v reflect.Value) {
	switch v.Kind() {
	case reflect.Pointer:
		p := reflect.New(v.Type().Elem())
		fillNonZero(p.Elem())
		v.Set(p)
	case reflect.Slice:
		s := reflect.MakeSlice(v.Type(), 1, 1)
		fillNonZero(s.Index(0))
		v.Set(s)
	case reflect.Array:
		for i := 0; i < v.Len(); i++ {
			fillNonZero(v.Index(i))
		}
	case reflect.Map:
		m := reflect.MakeMap(v.Type())
		key := reflect.New(v.Type().Key()).Elem()
		fillNonZero(key)
		val := reflect.New(v.Type().Elem()).Elem()
		fillNonZero(val)
		m.SetMapIndex(key, val)
		v.Set(m)
	case reflect.Interface:
		v.Set(reflect.ValueOf("seeded"))
	case reflect.String:
		v.SetString("seeded")
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(7)
	case reflect.Float32, reflect.Float64:
		v.SetFloat(7)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(7)
	}
}

// TestYAMLEmptyLiteralsAreEdits closes the YAML half of the overlay contract.
// TestPatchClearsSettingsFieldsWrittenEmpty builds its manifest in Go, so it
// proves the overlay treats an empty value as an edit but never that a manifest
// can express one. The table page tells users to "write it as [] or {}" to clear
// a setting, and that instruction is only true if the decoder yields a non-nil
// empty value rather than the nil an absent key gives. Decoded exactly as apply
// decodes it (internal/commands/apply/dataset.go:parseDatasetManifest).
func TestYAMLEmptyLiteralsAreEdits(t *testing.T) {
	var tolerant TableManifestWithStatus
	if err := pkgmanifest.StrictUnmarshal([]byte(`
apiVersion: qluster.ai/v1
kind: Table
metadata:
  name: orders
spec:
  destination_id: 3
  database_name: analytics
  schema_name: public
  table_name: orders
  migration_policy: ask_user
  data_loading_process: append
  settings_model:
    ignore_column_names: []
    value_mapping: {}
`), &tolerant); err != nil {
		t.Fatalf("StrictUnmarshal: %v", err)
	}

	sm := tolerant.Spec.SettingsModel
	if sm == nil {
		t.Fatal("settings_model missing from the decoded manifest")
	}
	// A nil here would read as "the manifest never mentioned it", so the overlay
	// would keep the stored value and the documented way to clear would silently
	// do nothing.
	if sm.IgnoreColumnNames == nil {
		t.Error("ignore_column_names: [] decoded to nil, so it would be treated as absent and would not clear")
	} else if len(*sm.IgnoreColumnNames) != 0 {
		t.Errorf("ignore_column_names = %v, want an empty list", *sm.IgnoreColumnNames)
	}
	if sm.ValueMapping == nil {
		t.Error("value_mapping: {} decoded to nil, so it would be treated as absent and would not clear")
	} else if len(*sm.ValueMapping) != 0 {
		t.Errorf("value_mapping = %v, want an empty map", *sm.ValueMapping)
	}
}

// TestYAMLExplicitNullIsAbsent checks that a null setting is omitted while
// an empty list setting remains present.
func TestYAMLExplicitNullIsAbsent(t *testing.T) {
	var m TableManifestWithStatus
	if err := pkgmanifest.StrictUnmarshal([]byte(`
apiVersion: qluster.ai/v1
kind: Table
metadata:
  name: orders
spec:
  settings_model:
    key_columns: null
    limit_fields_in_signature_calculation_to: []
`), &m); err != nil {
		t.Fatalf("StrictUnmarshal: %v", err)
	}

	sm := m.Spec.SettingsModel
	if sm == nil {
		t.Fatal("settings_model missing from the decoded manifest")
	}
	if sm.KeyColumns != nil {
		t.Errorf("key_columns: null decoded to %v, want nil -- if this ever becomes a non-nil empty list the manifest starts sending [], which the backend refuses", *sm.KeyColumns)
	}
	if sm.LimitFieldsInSignatureCalculationTo == nil {
		t.Error("limit_fields_in_signature_calculation_to: [] decoded to nil, so it would be treated as absent and would not clear")
	} else if len(*sm.LimitFieldsInSignatureCalculationTo) != 0 {
		t.Errorf("limit_fields_in_signature_calculation_to = %v, want an empty list", *sm.LimitFieldsInSignatureCalculationTo)
	}
}

// TestSettingsPatchBasePreservesLargeNumbers guards the JSON round trip's one
// lossy spot. default_value_for_field_when_casting_error is a map[string]any, so
// a plain json.Unmarshal decodes every number to float64 and re-encoding a value
// past 2^53 hands the server back a different one. The column is NOT NULL, so
// before the base was seeded from the read this field went out as null and the
// server skipped it -- echoing it back is what makes the fidelity matter.
func TestSettingsPatchBasePreservesLargeNumbers(t *testing.T) {
	// 12345678901234567 > 2^53, so float64 cannot hold it exactly. The seed is
	// decoded as GetDataset decodes the read.
	var read SettingsSchema
	if err := jsonnum.Unmarshal([]byte(`{"id":7,"version_id":2,"default_value_for_field_when_casting_error":{"account_id":12345678901234567}}`), &read); err != nil {
		t.Fatalf("decode the read: %v", err)
	}
	base, err := settingsPatchBase(&read)
	if err != nil {
		t.Fatalf("settingsPatchBase: %v", err)
	}

	raw, err := json.Marshal(base.DefaultValueForFieldWhenCastingError)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if want := `{"account_id":12345678901234567}`; string(raw) != want {
		t.Errorf("default_value_for_field_when_casting_error = %s, want %s", raw, want)
	}
}

// TestEverySettingsManifestFieldIsOverlaid is the tripwire for the four
// overlaySettings* helpers, and it exists because the overlay made a missed
// field QUIETER. Before it, a SettingsModel field nobody wired up went out as
// null: a visible wipe, or a 400. Now the base already carries the STORED value
// for that key, so a missed field means the PATCH sends the old value, the
// server answers 200 and apply prints "table updated successfully" while the
// user's edit was silently dropped.
//
// It walks SettingsModel by reflection rather than by name, so a field added to
// the manifest and wired into none of the helpers fails here the day it appears.
func TestEverySettingsManifestFieldIsOverlaid(t *testing.T) {
	// The manifest asks for one set of values; the stored settings model holds a
	// different one for every field. Any key that comes back holding the stored
	// value was not overlaid.
	manifest := &SettingsModel{}
	mv := reflect.ValueOf(manifest).Elem()
	for i := 0; i < mv.NumField(); i++ {
		fillNonZero(mv.Field(i))
	}

	stored := &SettingsSchema{}
	sv := reflect.ValueOf(stored).Elem()
	for i := 0; i < sv.NumField(); i++ {
		fillOther(sv.Field(i))
	}

	patch, err := settingsPatchFromExisting(stored, manifest)
	if err != nil {
		t.Fatalf("settingsPatchFromExisting: %v", err)
	}

	wantJSON := jsonMap(t, manifest)
	gotJSON := jsonMap(t, patch)

	for key, want := range wantJSON {
		got, ok := gotJSON[key]
		if !ok {
			t.Errorf("%s: the manifest can set it but it is not on the patch at all", key)
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: patch has %v, want the manifest's %v -- the field is not wired into any overlaySettings* helper, so the edit is silently dropped", key, got, want)
		}
	}
}

// TestEverySettingsManifestFieldReachesTheCreateBody is the overlay tripwire's
// missing twin, and it lives next to it because the two share fillNonZero and
// jsonMap. It is here because of qctl-o22: SettingsModel's key list is
// hand-copied into three converters (read, create, update) and only the update
// one was ever checked, so a key could be wired into two of the three and the
// suite would still be green.
//
// A create failure is as quiet as the update one it mirrors.
// api.NewSettingsSchema declares omitempty only on xls_date_mode, so an unwired
// field goes out as an explicit null, and creation writes through the same
// update_from_body -> set_field path a PATCH does (atlas
// capi/capi/views/settings_view_core.py, the _update_dataset_from_body call
// under the POST handler): set_field skips a null on a NOT NULL column and the
// Python default lands, and writes SQL NULL on a nullable one. Either way the
// POST answers 201 and the table exists -- with a setting the manifest asked
// for and the server never stored.
func TestEverySettingsManifestFieldReachesTheCreateBody(t *testing.T) {
	manifest := &SettingsModel{}
	mv := reflect.ValueOf(manifest).Elem()
	for i := 0; i < mv.NumField(); i++ {
		fillNonZero(mv.Field(i))
	}

	body, err := settingsModelToNewSettingsSchema(manifest)
	if err != nil {
		t.Fatalf("settingsModelToNewSettingsSchema: %v", err)
	}
	if body == nil {
		t.Fatal("settingsModelToNewSettingsSchema returned nil for a fully populated manifest")
	}

	wantJSON := jsonMap(t, manifest)
	gotJSON := jsonMap(t, body)

	for key, want := range wantJSON {
		got, ok := gotJSON[key]
		if !ok {
			t.Errorf("%s: the manifest can set it but api.NewSettingsSchema has no such key at all", key)
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: the POST body carries %v, want the manifest's %v -- the field is not wired into settingsModelToNewSettingsSchema, so creating a table silently ignores it", key, got, want)
		}
	}
}

func jsonMap(t *testing.T, v any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return m
}

// fillOther is fillNonZero's counterpart: same shapes, different values, so a
// field that keeps the stored value instead of the manifest's is visible.
func fillOther(v reflect.Value) {
	switch v.Kind() {
	case reflect.Pointer:
		p := reflect.New(v.Type().Elem())
		fillOther(p.Elem())
		v.Set(p)
	case reflect.Slice:
		s := reflect.MakeSlice(v.Type(), 1, 1)
		fillOther(s.Index(0))
		v.Set(s)
	case reflect.Array:
		for i := 0; i < v.Len(); i++ {
			fillOther(v.Index(i))
		}
	case reflect.Map:
		m := reflect.MakeMap(v.Type())
		key := reflect.New(v.Type().Key()).Elem()
		fillOther(key)
		val := reflect.New(v.Type().Elem()).Elem()
		fillOther(val)
		m.SetMapIndex(key, val)
		v.Set(m)
	case reflect.Interface:
		v.Set(reflect.ValueOf("stored"))
	case reflect.String:
		v.SetString("stored")
	case reflect.Bool:
		v.SetBool(false) // fillNonZero uses true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(9)
	case reflect.Float32, reflect.Float64:
		v.SetFloat(9)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(9)
	}
}

// TestSettingsSchemaAndOptionalShareKeySets pins the premise settingsPatchBase's
// JSON round trip rests on. TestSettingsPatchBaseCarriesEveryField walks the
// PATCH type only, so it catches a key Optional has and Schema lacks, but stays
// green if a regen adds a read-side settings column with no counterpart on the
// PATCH type -- at which point the round trip is no longer exact and the doc
// comment's "the same 47 JSON keys" has quietly become false.
func TestSettingsSchemaAndOptionalShareKeySets(t *testing.T) {
	read := jsonKeys(reflect.TypeOf(api.SettingsSchema{}))
	patch := jsonKeys(reflect.TypeOf(api.SettingsSchemaOptional{}))

	for key := range read {
		if !patch[key] {
			t.Errorf("%s is on the read schema but not the PATCH schema, so it cannot be carried across", key)
		}
	}
	for key := range patch {
		if !read[key] {
			t.Errorf("%s is on the PATCH schema but not the read schema, so it is never seeded and goes out as null", key)
		}
	}
}

func jsonKeys(t reflect.Type) map[string]bool {
	keys := map[string]bool{}
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		if comma := strings.Index(tag, ","); comma >= 0 {
			tag = tag[:comma]
		}
		keys[tag] = true
	}
	return keys
}
