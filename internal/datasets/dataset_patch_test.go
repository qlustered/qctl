package datasets

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/google/uuid"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
)

// TestPatchBaseCarriesEveryField is the tripwire behind patchRequestFromExisting's
// JSON round trip, and the outer half of what TestSettingsPatchBaseCarriesEveryField
// does for the nested settings block. Every key the read returns must reach the
// patch: 23 of the patch schema's 27 keys carry no omitempty, so a key left
// behind is serialised as null and the backend applies every key the raw body
// carries. It fills api.DataSetSchemaFull exhaustively by reflection rather than
// by name, so a column a later spec adds is covered the day it appears.
func TestPatchBaseCarriesEveryField(t *testing.T) {
	read := &DatasetFull{}
	rv := reflect.ValueOf(read).Elem()
	for i := 0; i < rv.NumField(); i++ {
		fillNonZero(rv.Field(i))
	}

	base, err := patchRequestFromExisting(read)
	if err != nil {
		t.Fatalf("patchRequestFromExisting: %v", err)
	}

	// settings_model is dropped on purpose; TestPatchOmitsSettingsModelWhenTheManifestHasNone
	// owns that. Every other field must have survived.
	dropped := map[string]bool{"SettingsModel": true}

	v := reflect.ValueOf(base)
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

// TestPatchCarriesTheKeysTheHandCopyMissed is the regression proper. The
// field-by-field copy this seed replaced listed 22 of the 28 keys, so these four
// went out as null on every kind: Table update. They survived only because none
// of them is a nullable mapped column on the backend -- model_labels and
// organization_id are NOT NULL, so set_field skipped the null; destinations_list
// is a transient frontend-only attribute, so update_from_body's merge-patch
// guard skipped it; whodunit_id is overwritten with the caller's user id after
// the key loop. None of those is a property qctl controls.
func TestPatchCarriesTheKeysTheHandCopyMissed(t *testing.T) {
	org := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	who := uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	read := &DatasetFull{
		ID:               3,
		VersionID:        1,
		Name:             "orders",
		OrganizationID:   org,
		WhodunitID:       &who,
		ModelLabels:      &map[string]string{"amount": "Amount"},
		DestinationsList: []api.OptionTypeSchema{{Label: "warehouse"}},
	}

	body := patchBody(t, read)

	for _, key := range []string{"model_labels", "organization_id", "destinations_list", "whodunit_id"} {
		raw, ok := body[key]
		if !ok {
			t.Errorf("%s is missing from the patch body", key)
			continue
		}
		if string(raw) == "null" {
			t.Errorf("%s = null; the read's value must be carried", key)
		}
	}
}

// TestPatchBaseDropsTheReadOnlyKeys pins the claim in patchRequestFromExisting's
// doc comment: api.DataSetSchemaFull carries 15 keys the patch schema does not,
// and the decode drops them. All five fields apply refuses to change on an
// existing table are among those 15, so this seed cannot echo an immutable value
// even in principle. A regen that made one settable would turn this red rather
// than quietly start sending it.
func TestPatchBaseDropsTheReadOnlyKeys(t *testing.T) {
	read := &DatasetFull{}
	rv := reflect.ValueOf(read).Elem()
	for i := 0; i < rv.NumField(); i++ {
		fillNonZero(rv.Field(i))
	}

	body := patchBody(t, read)

	for _, key := range []string{
		"backup_settings_id", "database_name", "schema_name", "table_name", "destination_id",
		"state", "disabled_fields", "clean_rows_count", "bad_rows_count", "created_at",
	} {
		if _, ok := body[key]; ok {
			t.Errorf("%s is on the wire; the patch must not carry a read-only key", key)
		}
	}
}

// TestPatchBasePreservesLargeNumbers guards the outer round trip's lossy spot,
// the same one settingsPatchBase has: not_anomalies_per_column is a
// map[string]any, so a plain json.Unmarshal decodes every number to float64 and
// re-encoding a value past 2^53 hands the server back a different one. The hand
// copy this replaced was immune because it copied the map pointer.
func TestPatchBasePreservesLargeNumbers(t *testing.T) {
	// 12345678901234567 > 2^53, so float64 cannot hold it exactly. The seed is
	// decoded as GetDataset decodes the read.
	var read DatasetFull
	if err := jsonnum.Unmarshal([]byte(`{"id":3,"not_anomalies_per_column":{"account_id":12345678901234567}}`), &read); err != nil {
		t.Fatalf("decode the read: %v", err)
	}
	base, err := patchRequestFromExisting(&read)
	if err != nil {
		t.Fatalf("patchRequestFromExisting: %v", err)
	}

	raw, err := json.Marshal(base.NotAnomaliesPerColumn)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if want := `{"account_id":12345678901234567}`; string(raw) != want {
		t.Errorf("not_anomalies_per_column = %s, want %s", raw, want)
	}
}

// patchBody seeds a patch from read and returns it as the server sees it: the
// keys actually on the wire, so a test can tell an omitted key from one sent as
// null.
func patchBody(t *testing.T, read *DatasetFull) map[string]json.RawMessage {
	t.Helper()

	base, err := patchRequestFromExisting(read)
	if err != nil {
		t.Fatalf("patchRequestFromExisting: %v", err)
	}
	raw, err := json.Marshal(base)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return body
}

// TestPatchRefusesASettingsEditWhenTheReadHasNoSettingsModel covers the case
// qctl-55o left open. Both backend arms key off the STORED settings model, so
// sending a block built from a read that had none is refused as a version
// mismatch when the dataset has one, and silently dropped behind a 200 when it
// does not -- and apply prints "table updated successfully" over the second.
// Unreachable through today's response contract; a client-side message is still
// cheaper than either outcome.
func TestPatchRefusesASettingsEditWhenTheReadHasNoSettingsModel(t *testing.T) {
	formats := []string{"%Y-%m-%d"}
	read := &DatasetFull{ID: 3, VersionID: 1, Name: "orders"}

	_, err := manifestToPatchRequest(read, settingsEditManifest(&SettingsModel{DatetimeFormats: &formats}))
	if err == nil {
		t.Fatal("a settings edit over a read with no settings model was accepted")
	}
	if code := qerrors.GetExitCode(err); code != 1 {
		t.Errorf("exit code = %d, want 1: the manifest is fine, the response is not", code)
	}

	// The same manifest over a read that did carry one still applies. A settings
	// model's version_id is legitimately 0, so only the id may gate this.
	if _, err := manifestToPatchRequest(&DatasetFull{
		ID:            3,
		VersionID:     1,
		SettingsModel: SettingsSchema{ID: 7},
	}, settingsEditManifest(&SettingsModel{DatetimeFormats: &formats})); err != nil {
		t.Errorf("settings edit over a version_id 0 settings model: %v", err)
	}
}

// TestPatchBaseRefusesANilRead: the hand copy this seed replaced dereferenced
// its argument on the first line, so a nil read was a panic. A JSON round trip
// of nil is the string "null", which decodes into a zero patch quite happily --
// id 0, every key null -- so the seed says no instead.
func TestPatchBaseRefusesANilRead(t *testing.T) {
	if _, err := patchRequestFromExisting(nil); err == nil {
		t.Error("a nil read built a patch; it must be refused")
	}
}

// TestPatchWithNoSettingsEditSurvivesAReadWithNoSettingsModel keeps the guard
// scoped to what it is for: a manifest that never mentions spec.settings_model
// sends no block, so a read without one is not its problem.
func TestPatchWithNoSettingsEditSurvivesAReadWithNoSettingsModel(t *testing.T) {
	if _, err := manifestToPatchRequest(&DatasetFull{ID: 3, VersionID: 1}, settingsEditManifest(nil)); err != nil {
		t.Errorf("manifestToPatchRequest: %v", err)
	}
}
