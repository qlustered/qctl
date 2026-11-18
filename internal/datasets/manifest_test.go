package datasets

import (
	"reflect"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
)

// TestSettingsSchemaToSettingsModel_IsFaithfulToWhatTheServerSent replaces
// TestSettingsSchemaToSettingsModel_PreservesNilAndZeroDefaults, which asserted
// that a zero-value SettingsSchema converts to an EMPTY SettingsModel -- the
// lossy read qctl-m5d filed. Its input was a Go zero value, not a shape the
// server ever sends: null_values and the seven other settings named below are
// required on the wire, so a cleared one arrives as [] or 0, not as absent.
//
// What the converter must do with that is carry it. An emptied value that reads
// back as an ABSENT key survives an update (the PATCH is seeded from the read)
// but not the clone workflow -- `get table -o yaml`, edit the name, `apply -f`
// -- where api.NewSettingsSchema declares omitempty only on xls_date_mode, so the
// absent key becomes a null, atlas set_field skips a null on a NOT NULL column,
// and the server's own non-empty default lands in place of the emptied value.
func TestSettingsSchemaToSettingsModel_IsFaithfulToWhatTheServerSent(t *testing.T) {
	got, err := settingsSchemaToSettingsModel(nil)
	if err != nil {
		t.Fatalf("settingsSchemaToSettingsModel(nil): %v", err)
	}
	if got != nil {
		t.Fatalf("settingsSchemaToSettingsModel(nil) = %#v, want nil", got)
	}

	// A settings model whose owner has cleared every list and zeroed every
	// scalar, in the shape the server actually sends it: required keys present
	// and empty, not missing.
	cleared := &SettingsSchema{
		ID:           7,
		NullValues:   []string{},
		BooleanTrue:  []string{},
		BooleanFalse: []string{},
		IgnoreLinesThatIncludeOnlySubsetOfCharacters: []string{},
		IgnoreLinesThatIncludeOnlySubsetOfWords:      []string{},
		IgnoreFieldsInSignatureCalculation:           []string{},
		DatetimeAllowedCharacters:                    "",
		DecimalFieldPadding:                          0,
	}

	sm, err := settingsSchemaToSettingsModel(cleared)
	if err != nil {
		t.Fatalf("settingsSchemaToSettingsModel: %v", err)
	}

	// The eight settings the old len/zero guards could drop that have a NON-EMPTY
	// backend default -- six lists here, datetime_allowed_characters and
	// decimal_field_padding below. Dropping any one of them from the manifest is
	// what silently restores that default on a clone.
	lists := map[string]*[]string{
		"null_values":   sm.NullValues,
		"boolean_true":  sm.BooleanTrue,
		"boolean_false": sm.BooleanFalse,
		"ignore_lines_that_include_only_subset_of_characters": sm.IgnoreLinesThatIncludeOnlySubsetOfCharacters,
		"ignore_lines_that_include_only_subset_of_words":      sm.IgnoreLinesThatIncludeOnlySubsetOfWords,
		"ignore_fields_in_signature_calculation":              sm.IgnoreFieldsInSignatureCalculation,
	}
	for key, got := range lists {
		if got == nil {
			t.Errorf("%s: a cleared list read back as an absent key; applying this manifest as a new table restores the server default", key)
			continue
		}
		if len(*got) != 0 {
			t.Errorf("%s = %v, want an empty list", key, *got)
		}
	}
	if sm.DatetimeAllowedCharacters == nil || *sm.DatetimeAllowedCharacters != "" {
		t.Errorf("datetime_allowed_characters = %v, want an empty string the manifest actually carries", sm.DatetimeAllowedCharacters)
	}
	if sm.DecimalFieldPadding == nil || *sm.DecimalFieldPadding != 0 {
		t.Errorf("decimal_field_padding = %v, want 0 -- the schema says ge=0, so 0 is a legal value and not a sentinel", sm.DecimalFieldPadding)
	}

	// A key the server did not send at all stays absent. Only the nullable
	// columns can be in that state; everything above is required on the wire.
	if sm.KeyColumns != nil || sm.ArrayDelimiters != nil {
		t.Errorf("a null on the wire became a set key: key_columns=%v array_delimiters=%v", sm.KeyColumns, sm.ArrayDelimiters)
	}
}

func TestSettingsSchemaToSettingsModel_ConvertsGeneratedFieldShapes(t *testing.T) {
	arrayDelimiters := []string{"|"}
	datetimeFormats := []string{"2006-01-02"}
	fieldsToExpand := []string{"payload"}
	limitSignatureTo := []string{"order_id"}
	syncKeyFields := []string{"order_id", "line_no"}
	falsePerColumn := map[string][]string{"active": {"no"}}
	truePerColumn := map[string][]string{"active": {"yes"}}
	validForField := map[string][]string{"code": {"unknown"}}
	datetimePerColumn := map[string][]string{"created_at": {"2006-01-02"}}
	ignoreMatchers := map[string][]string{"created_at": {"DateMatcher"}}
	monetaryOverrides := map[string]bool{"amount": true}
	nullPerColumn := map[string][]string{"amount": {"N/A"}}
	enableInteger := false
	enableSmallInteger := true
	ignoreNewFields := false
	inferDatetime := true
	nonStringNullable := false
	percentToDecimal := true
	stringNullable := false
	trimStrings := true
	useText := false

	got, err := settingsSchemaToSettingsModel(&SettingsSchema{
		ID:                                           7,
		ArrayDelimiters:                              &arrayDelimiters,
		BooleanFalse:                                 []string{"false"},
		BooleanFalsePerColumn:                        &falsePerColumn,
		BooleanTrue:                                  []string{"true"},
		BooleanTruePerColumn:                         &truePerColumn,
		DataMarkedAsValidForDBField:                  &validForField,
		DatetimeAllowedCharacters:                    "-/:",
		DatetimeFormats:                              &datetimeFormats,
		DatetimeFormatsPerColumn:                     &datetimePerColumn,
		DecimalFieldPadding:                          2,
		DefaultValueForFieldWhenCastingError:         map[string]any{"amount": nil},
		DollarValueIfWordInFieldName:                 []string{"amount"},
		EnableInteger:                                &enableInteger,
		EnableSmallInteger:                           &enableSmallInteger,
		EncryptColumns:                               []string{"ssn"},
		FieldNameFullConversion:                      map[string]string{"fname": "first_name"},
		FieldNamePartConversion:                      map[string]string{"addr": "address"},
		FieldsToExpand:                               &fieldsToExpand,
		IgnoreColumnNames:                            []string{"internal"},
		IgnoreColumnNamesThatHaveAnyOfWords:          []string{"secret"},
		IgnoreFieldsInSignatureCalculation:           []string{"updated_at"},
		IgnoreLinesThatIncludeOnlySubsetOfCharacters: []string{"="},
		IgnoreLinesThatIncludeOnlySubsetOfWords:      []string{"total"},
		IgnoreMatchers:                               &ignoreMatchers,
		IgnoreNotSeenBeforeFieldsWhenImporting:       &ignoreNewFields,
		InferDatetime:                                &inferDatetime,
		InferDatetimeForColumns:                      []string{"created_at"},
		LimitFieldsInSignatureCalculationTo:          &limitSignatureTo,
		MaxBoundaryElementLength:                     256,
		MonetaryColumnsOverride:                      &monetaryOverrides,
		NonStringFieldsAreAllNullable:                &nonStringNullable,
		NullValues:                                   []string{"NULL"},
		NullValuesPerColumn:                          &nullPerColumn,
		PercentToDecimal:                             &percentToDecimal,
		StringFieldPadding:                           8,
		StringFieldsCanBeNullable:                    &stringNullable,
		KeyColumns:                                   &syncKeyFields,
		TrimStringInsteadOfRaisingErr:                &trimStrings,
		UseTextInsteadOfString:                       &useText,
		ValueMapping:                                 map[string]string{"N": "no"},
		ValueMappingPerColumn:                        map[string]map[string]string{"active": {"Y": "yes"}},
		XlsDateMode:                                  api.Mode1,
	})
	if err != nil {
		t.Fatalf("settingsSchemaToSettingsModel: %v", err)
	}

	want := &SettingsModel{
		ArrayDelimiters:                              &arrayDelimiters,
		BooleanFalse:                                 &[]string{"false"},
		BooleanFalsePerColumn:                        &falsePerColumn,
		BooleanTrue:                                  &[]string{"true"},
		BooleanTruePerColumn:                         &truePerColumn,
		DataMarkedAsValidForDBField:                  &validForField,
		DatetimeAllowedCharacters:                    stringPtr("-/:"),
		DatetimeFormats:                              &datetimeFormats,
		DatetimeFormatsPerColumn:                     &datetimePerColumn,
		DecimalFieldPadding:                          intPtr(2),
		DefaultValueForFieldWhenCastingError:         &map[string]any{"amount": nil},
		DollarValueIfWordInFieldName:                 &[]string{"amount"},
		EnableInteger:                                &enableInteger,
		EnableSmallInteger:                           &enableSmallInteger,
		EncryptColumns:                               &[]string{"ssn"},
		FieldNameFullConversion:                      &map[string]string{"fname": "first_name"},
		FieldNamePartConversion:                      &map[string]string{"addr": "address"},
		FieldsToExpand:                               &fieldsToExpand,
		IgnoreColumnNames:                            &[]string{"internal"},
		IgnoreColumnNamesThatHaveAnyOfWords:          &[]string{"secret"},
		IgnoreFieldsInSignatureCalculation:           &[]string{"updated_at"},
		IgnoreLinesThatIncludeOnlySubsetOfCharacters: &[]string{"="},
		IgnoreLinesThatIncludeOnlySubsetOfWords:      &[]string{"total"},
		IgnoreMatchers:                               &ignoreMatchers,
		IgnoreNotSeenBeforeFieldsWhenImporting:       &ignoreNewFields,
		InferDatetime:                                &inferDatetime,
		InferDatetimeForColumns:                      &[]string{"created_at"},
		LimitFieldsInSignatureCalculationTo:          &limitSignatureTo,
		MaxBoundaryElementLength:                     intPtr(256),
		MonetaryColumnsOverride:                      &monetaryOverrides,
		NonStringFieldsAreAllNullable:                &nonStringNullable,
		NullValues:                                   &[]string{"NULL"},
		NullValuesPerColumn:                          &nullPerColumn,
		PercentToDecimal:                             &percentToDecimal,
		StringFieldPadding:                           intPtr(8),
		StringFieldsCanBeNullable:                    &stringNullable,
		KeyColumns:                                   &syncKeyFields,
		TrimStringInsteadOfRaisingErr:                &trimStrings,
		UseTextInsteadOfString:                       &useText,
		ValueMapping:                                 &map[string]string{"N": "no"},
		ValueMappingPerColumn:                        &map[string]map[string]string{"active": {"Y": "yes"}},
		XlsDateMode:                                  xlsDateModePtr(api.Mode1),
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("settingsSchemaToSettingsModel() = %#v, want %#v", got, want)
	}
}

// TestSettingsSchemaToSettingsModel_KeepsAnEmptyList covers the three keys
// qctl-o22 added, which used to differ from each other for a reason that no
// longer exists.
//
// key_columns and limit_fields_in_signature_calculation_to are nullable
// columns with default=None, so the read type gives them as *[]string;
// ignore_column_names_that_have_any_of_words is NOT NULL with default=[]
// (atlas SettingsModel in cettings/cettings/models/settings_model.py) and
// arrives as a bare []string. The old converter copied the first two and
// guarded the third with len > 0, so a stored [] survived on two keys and
// vanished on the third. The JSON round trip carries all three, and the
// distinction that survives is the one that means something: [] is a value,
// null is an absent key.
func TestSettingsSchemaToSettingsModel_KeepsAnEmptyList(t *testing.T) {
	empty := []string{}
	got, err := settingsSchemaToSettingsModel(&SettingsSchema{
		ID:                                  7,
		LimitFieldsInSignatureCalculationTo: &empty,
		KeyColumns:                          &empty,
		IgnoreColumnNamesThatHaveAnyOfWords: []string{},
	})
	if err != nil {
		t.Fatalf("settingsSchemaToSettingsModel: %v", err)
	}

	for key, list := range map[string]*[]string{
		"limit_fields_in_signature_calculation_to": got.LimitFieldsInSignatureCalculationTo,
		"key_columns": got.KeyColumns,
		"ignore_column_names_that_have_any_of_words": got.IgnoreColumnNamesThatHaveAnyOfWords,
	} {
		if list == nil {
			t.Errorf("%s: a stored empty list came back absent; the read is lossy and an apply of this manifest would restore the old value", key)
			continue
		}
		if len(*list) != 0 {
			t.Errorf("%s = %v, want an empty list", key, *list)
		}
	}

	// A nullable list the server sent as null is genuinely absent, not empty.
	// The id is what makes this a settings model at all -- see
	// TestSettingsSchemaToSettingsModel_NoSettingsModelIsNoBlock for the other
	// reading of a zero struct.
	none, err := settingsSchemaToSettingsModel(&SettingsSchema{ID: 7})
	if err != nil {
		t.Fatalf("settingsSchemaToSettingsModel: %v", err)
	}
	if none.KeyColumns != nil || none.LimitFieldsInSignatureCalculationTo != nil {
		t.Errorf("a settings model with neither key set produced %#v; nil must stay nil, or every manifest claims to set them", none)
	}
}

// TestSettingsModelCoversEverySettingsKey is the tripwire for the gap qctl-o22
// reported. api.SettingsSchema carries key_columns,
// limit_fields_in_signature_calculation_to and
// ignore_column_names_that_have_any_of_words; SettingsModel carried none of
// them, so a table that had them read back as a manifest that did not mention
// them and apply could never set them -- and make test stayed green, because
// every existing settings tripwire walks a type that already had them.
// TestEverySettingsManifestFieldIsOverlaid and
// TestSettingsPatchBaseCarriesEveryField both start FROM SettingsModel, so a key
// it lacks is a key they never ask about, and
// TestSettingsSchemaAndOptionalShareKeySets compares the two generated types to
// each other. Nothing compared the manifest to the wire. This does.
//
// The read schema minus four deliberate exclusions IS the manifest's key set,
// in both directions. The exclusions are checked for existence too, so a regen
// that renames or drops one leaves a stale entry that fails here rather than a
// hole that hides a real key.
func TestSettingsModelCoversEverySettingsKey(t *testing.T) {
	excluded := map[string]string{
		"id":               "server-owned",
		"version_id":       "server-owned",
		"whodunit_id":      "server-owned",
		"schema_overrides": "deliberately absent -- PATCH writes it with none of kind: TableSchema's guards; TestSettingsModelHasNoSchemaOverrides is the dedicated tripwire",
	}

	wire := jsonKeys(reflect.TypeOf(api.SettingsSchema{}))
	for key, why := range excluded {
		if !wire[key] {
			t.Errorf("%s is on the exclusion list but no longer on api.SettingsSchema (%s); delete the entry rather than leaving it to swallow a key a later regen gives that name", key, why)
		}
		delete(wire, key)
	}

	manifest := manifestKeys(t, reflect.TypeOf(SettingsModel{}), "SettingsModel")
	for key := range wire {
		if !manifest[key] {
			t.Errorf("api.SettingsSchema carries %s but SettingsModel cannot express it: a table that has it reads back as a manifest that does not mention it, and no apply can ever set it", key)
		}
	}
	for key := range manifest {
		if wire[key] {
			continue
		}
		if why, ok := excluded[key]; ok {
			t.Errorf("SettingsModel exposes %s, which is left off on purpose (%s)", key, why)
			continue
		}
		t.Errorf("SettingsModel exposes %s, which is on no settings schema at all -- a misspelled tag round-trips through YAML and then sets nothing", key)
	}
}

// manifestKeys is jsonKeys with the two differences a manifest type needs.
// It reads BOTH tags and reports a disagreement, because the yaml tag is what
// the user writes and the json tag is what the reflection-based patch tests
// compare; and it falls back to the Go field name rather than skipping an
// untagged field, because an untagged field is not absent from the wire -- it is
// present under its Go name (encoding/json) or its lowercased name (yaml.v3).
// That fallback is what makes such a field FAIL its key-set test instead of
// vanishing from it, the same hole TestSettingsModelHasNoSchemaOverrides checks
// the field name for.
//
// Both manifest types go through it -- SettingsModel here and TableSpec in
// TestTableSpecCoversEveryDatasetWriteKey -- which is why the type's name is a
// parameter.
func manifestKeys(t *testing.T, rt reflect.Type, typeName string) map[string]bool {
	t.Helper()
	keys := map[string]bool{}
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		yamlKey := tagKey(f.Tag.Get("yaml"), f.Name)
		jsonKey := tagKey(f.Tag.Get("json"), f.Name)
		if yamlKey != jsonKey {
			t.Errorf("%s.%s is %q in yaml and %q in json; the file the user writes and the body apply sends disagree about its name", typeName, f.Name, yamlKey, jsonKey)
		}
		keys[yamlKey] = true
	}
	return keys
}

func tagKey(tag, fallback string) string {
	if comma := strings.Index(tag, ","); comma >= 0 {
		tag = tag[:comma]
	}
	if tag == "" {
		return fallback
	}
	return tag
}

func xlsDateModePtr(m api.XLSDateMode) *api.XLSDateMode { return &m }

// TestSettingsSchemaToSettingsModel_NoSettingsModelIsNoBlock covers the state the
// faithful read created a hazard in: a response that carried no settings model at
// all.
//
// api.DataSetSchemaFull declares SettingsModel as a VALUE, so an absent or null
// settings_model decodes to the zero struct with no error, and every required key
// on it is then a legitimate-looking zero. xls_date_mode is the one that turns
// that from noise into a broken manifest: XLSDateMode has exactly two members,
// mode_0 and mode_1, so the "" a zero struct produces is not a value a user could
// have chosen and not one the server will accept. Emitting it hands back a
// manifest that looks applyable and is refused on the enum.
//
// atlas declares the field required and non-nullable on the detail response
// (DataSetSchemaFull, capi/capi/schemas/settings_schema.py), so this is a
// defence rather than a path anyone has walked -- the same posture, and the
// same id sentinel, as the guard in applyPatchStructuredOverrides.
func TestSettingsSchemaToSettingsModel_NoSettingsModelIsNoBlock(t *testing.T) {
	got, err := settingsSchemaToSettingsModel(&SettingsSchema{})
	if err != nil {
		t.Fatalf("settingsSchemaToSettingsModel: %v", err)
	}
	if got != nil {
		t.Fatalf("a response with no settings model produced a manifest block: %#v", got)
	}

	// The specific damage, spelled out so a later "restore faithfulness here"
	// change has to argue with it: an enum with no empty member.
	full, err := settingsSchemaToSettingsModel(&SettingsSchema{ID: 7})
	if err != nil {
		t.Fatalf("settingsSchemaToSettingsModel: %v", err)
	}
	if full.XlsDateMode == nil {
		t.Fatal("a real settings model must carry xls_date_mode; the read is faithful to what the server sent")
	}
	if *full.XlsDateMode != "" {
		t.Fatalf("fixture drift: a zero SettingsSchema should give xls_date_mode %q, got %q", "", *full.XlsDateMode)
	}
	for _, member := range []api.XLSDateMode{api.Mode0, api.Mode1} {
		if member == "" {
			t.Errorf("XLSDateMode gained an empty member (%q); the id guard above is no longer the only thing keeping \"\" out of a manifest", member)
		}
	}
}
