package datasets

import (
	"reflect"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
)

// excludedDatasetWriteKeys names every key on the dataset write schemas that
// TableSpec deliberately cannot say, with the reason. TableSpec's doc comment
// carries the long form of the two judgement calls; the rest are mechanical.
var excludedDatasetWriteKeys = map[string]string{
	"name":                     "the manifest's identity, carried as metadata.name rather than a spec key",
	"organization_id":          "context -- the org comes from -O/--org, the current context or QCTL_ORG, never from a file",
	"whodunit_id":              "server-owned -- who last changed the table",
	"clean_rows_count":         "read-only counter, projected onto TableStatus",
	"bad_rows_count":           "read-only counter, projected onto TableStatus",
	"excluded_row_signatures":  "internal -- signatures of deleted rows, a ledger the row operations keep",
	"destinations_list":        "an options list the web UI's destination picker renders, not a property of the table",
	"disabled_fields":          "server-computed: DATASET_PATCH_REQUEST_IGNORE_FIELDS plus settings_model.key_columns while the keys are locked, and PATCH ignores the key",
	"not_anomalies_per_column": "deliberate (qctl-r4h): an append-only ledger alert and warning resolution write, so a manifest snapshot would revert other people's judgements on the next apply -- see TableSpec's doc comment, and qctl-njt for reading it back",
	"redirect_post_submit_to":  "deliberate (qctl-r4h): not a dataset column at all -- CAPI stamps it onto the response, and a value sent back is setattr'd onto an ORM instance where no column persists it, so a spec key would accept a value and change nothing",
}

// patchOnlyDatasetWriteKeys are the two keys the update schema has and the
// create schema does not. Both are the optimistic-concurrency pair apply fills
// in from the read; neither is manifest content.
var patchOnlyDatasetWriteKeys = map[string]string{
	"id":         "server-owned -- apply resolves the table and fills it in",
	"version_id": "server-owned -- the optimistic-concurrency token, seeded from the read",
}

// These spec keys are written by dedicated dataset PUTs after create or PATCH.
var separateDatasetWriteKeys = map[string]bool{"table_kinds": true, "column_standard_fields": true}

// TestTableSpecCoversEveryDatasetWriteKey is the tripwire for the gap qctl-r4h
// reported, one level up from TestSettingsModelCoversEverySettingsKey.
//
// api.DataSetPostRequestSchema carried compression_type_of_backup_data and
// model_labels and TableSpec said neither, so a table that had them read back as
// a manifest that did not mention them and no apply could ever set them -- and
// the suite stayed green, because every other dataset test starts FROM TableSpec
// and a key it lacks is a key none of them asks about. Nothing compared the spec
// to the wire. This does, in both directions.
//
// The exclusions are checked for existence too: a regen that renames one leaves
// a stale entry that fails here, rather than a hole quietly exempting whatever
// real key inherits the name.
func TestTableSpecCoversEveryDatasetWriteKey(t *testing.T) {
	wire := jsonKeys(reflect.TypeOf(api.DataSetPostRequestSchema{}))
	for key, why := range excludedDatasetWriteKeys {
		if !wire[key] {
			t.Errorf("%s is on the exclusion list but no longer on api.DataSetPostRequestSchema (%s); delete the entry rather than leaving it to swallow a key a later regen gives that name", key, why)
		}
		delete(wire, key)
	}

	spec := manifestKeys(t, reflect.TypeOf(TableSpec{}), "TableSpec")
	for key := range wire {
		if !spec[key] {
			t.Errorf("api.DataSetPostRequestSchema carries %s but TableSpec cannot express it: a table that has it reads back as a manifest that does not mention it, and no apply can ever set it. Add it to the spec, or to excludedDatasetWriteKeys with the reason", key)
		}
	}
	for key := range spec {
		if separateDatasetWriteKeys[key] {
			continue
		}
		if wire[key] {
			continue
		}
		if why, ok := excludedDatasetWriteKeys[key]; ok {
			t.Errorf("TableSpec exposes %s, which is left off on purpose (%s)", key, why)
			continue
		}
		t.Errorf("TableSpec exposes %s, which is on no dataset write schema at all -- a misspelled tag round-trips through YAML and then sets nothing", key)
	}
}

// TestDatasetPatchAddsNothingTheCreateSchemaLacks is the "on BOTH write schemas"
// half. The test above compares the manifest to the CREATE schema, so a key a
// regen adds to the update surface alone would pass it unseen -- and a key qctl
// can set on an existing table but not on a new one is exactly as invisible to a
// manifest as the two this issue found.
func TestDatasetPatchAddsNothingTheCreateSchemaLacks(t *testing.T) {
	post := jsonKeys(reflect.TypeOf(api.DataSetPostRequestSchema{}))
	patch := jsonKeys(reflect.TypeOf(api.DataSetPatchRequestSchema{}))

	for key, why := range patchOnlyDatasetWriteKeys {
		if !patch[key] {
			t.Errorf("%s is listed as a patch-only key but is no longer on api.DataSetPatchRequestSchema (%s); delete the entry", key, why)
		}
		delete(patch, key)
	}

	for key := range patch {
		if !post[key] {
			t.Errorf("api.DataSetPatchRequestSchema carries %s and api.DataSetPostRequestSchema does not, so TestTableSpecCoversEveryDatasetWriteKey never asks about it: decide whether the spec should express it, then either wire it up or name it in patchOnlyDatasetWriteKeys", key)
		}
	}
}

// specKeysMissingFromThePatchSchema are the five fields a create sets and an
// update cannot: apply validates them as immutable, the patch schema does not
// declare them, and the backend ignores them on a PATCH anyway
// (DATASET_PATCH_REQUEST_IGNORE_FIELDS).
var specKeysMissingFromThePatchSchema = map[string]string{
	"destination_id":     "immutable after create",
	"database_name":      "immutable after create",
	"schema_name":        "immutable after create",
	"table_name":         "immutable after create",
	"backup_settings_id": "immutable after create",
}

// TestEveryTableSpecFieldReachesTheWriteBodies is the other half of expressing a
// key: declaring it on TableSpec puts it in the file a user writes, and only the
// converters put it on the wire.
//
// apply has three bodies to fill -- the create built on server defaults, the
// create built straight from the manifest when the defaults fetch fails, and the
// update -- and all three are hand-written field lists. A spec key wired into
// two of them looks completely correct in review and in the yaml round trip; the
// only symptom is a value that silently fails to apply, on one path or in one
// direction. This sets every spec field at once and asserts each key arrives
// non-null in each body.
func TestEveryTableSpecFieldReachesTheWriteBodies(t *testing.T) {
	rt := reflect.TypeOf(TableSpec{})
	for key := range specKeysMissingFromThePatchSchema {
		if !manifestKeys(t, rt, "TableSpec")[key] {
			t.Errorf("specKeysMissingFromThePatchSchema names %s, which TableSpec no longer has; delete the entry rather than leaving it to exempt whatever inherits the name", key)
		}
	}
	for key := range separateDatasetWriteKeys {
		if !manifestKeys(t, rt, "TableSpec")[key] {
			t.Errorf("separateDatasetWriteKeys names %s, which TableSpec no longer has", key)
		}
	}

	build := func() *TableManifest {
		manifest := &TableManifest{
			APIVersion: pkgmanifest.APIVersionV1,
			Kind:       "Table",
			Metadata:   pkgmanifest.Metadata{Name: "orders"},
		}
		spec := reflect.ValueOf(&manifest.Spec).Elem()
		for i := 0; i < rt.NumField(); i++ {
			spec.Field(i).Set(marshallableValue(t, rt.Field(i).Type))
		}
		return manifest
	}

	existing := &DatasetFull{ID: 123, VersionID: 5, Name: "orders", SettingsModel: SettingsSchema{ID: 9, VersionID: 2}}

	bodies := []struct {
		name string
		skip map[string]string
		body map[string]any
	}{
		{name: "create from server defaults", body: func() map[string]any {
			req := defaultsToPostRequest(&api.DataSetSchemaFullDraft{})
			if err := applyManifestOverrides(&req, build()); err != nil {
				t.Fatalf("applyManifestOverrides: %v", err)
			}
			return marshalToMap(t, req)
		}()},
		{name: "create from the manifest alone", body: func() map[string]any {
			req, err := manifestToPostRequest(build())
			if err != nil {
				t.Fatalf("manifestToPostRequest: %v", err)
			}
			return marshalToMap(t, req)
		}()},
		{name: "update", skip: specKeysMissingFromThePatchSchema, body: func() map[string]any {
			req, err := manifestToPatchRequest(existing, build())
			if err != nil {
				t.Fatalf("manifestToPatchRequest: %v", err)
			}
			return marshalToMap(t, req)
		}()},
	}

	for _, b := range bodies {
		t.Run(b.name, func(t *testing.T) {
			for i := 0; i < rt.NumField(); i++ {
				key := tagKey(rt.Field(i).Tag.Get("yaml"), rt.Field(i).Name)
				if _, skipped := b.skip[key]; skipped {
					continue
				}
				if separateDatasetWriteKeys[key] {
					continue
				}
				if got, ok := b.body[key]; !ok || got == nil {
					t.Errorf("spec.%s does not reach this body (%v): the manifest can say it and apply cannot send it", key, got)
				}
			}
		})
	}
}

// marshallableValue is nonQuickCreateValue's sibling: a value for any spec field
// whose MARSHALLED form is not null, so "the converter forgot this field" and
// "the field was set" cannot look alike. That is why slices and maps are
// allocated non-empty rather than left as a pointer to a nil one -- *[]string
// pointing at a nil slice marshals to null, which is exactly what a dropped
// field looks like.
func marshallableValue(t *testing.T, rt reflect.Type) reflect.Value {
	t.Helper()

	switch rt.Kind() {
	case reflect.Pointer:
		elem := rt.Elem()
		val := reflect.New(elem)
		switch elem.Kind() {
		case reflect.Slice:
			slice := reflect.MakeSlice(elem, 1, 1)
			slice.Index(0).Set(marshallableValue(t, elem.Elem()))
			val.Elem().Set(slice)
		case reflect.Map:
			m := reflect.MakeMap(elem)
			m.SetMapIndex(marshallableValue(t, elem.Key()), marshallableValue(t, elem.Elem()))
			val.Elem().Set(m)
		}
		return val
	case reflect.String:
		return reflect.ValueOf("x").Convert(rt)
	case reflect.Int:
		return reflect.ValueOf(1).Convert(rt)
	default:
		t.Fatalf("TableSpec grew a %s field and this helper has no value for that kind; add one rather than letting the field skip the check", rt.Kind())
		return reflect.Value{}
	}
}
