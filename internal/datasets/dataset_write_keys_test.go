package datasets

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
)

// compressionAndLabelsManifest builds a manifest that sets both of the dataset-level keys
// TableSpec gained for qctl-r4h, on top of the minimum a create needs.
func compressionAndLabelsManifest() *TableManifest {
	compression := api.CompressionTypeSnappyStream
	labels := map[string]string{"cust_email": "Customer Email", "amt": "Amount"}

	return &TableManifest{
		APIVersion: pkgmanifest.APIVersionV1,
		Kind:       "Table",
		Metadata:   pkgmanifest.Metadata{Name: "orders"},
		Spec: TableSpec{
			DestinationID:               1,
			DatabaseName:                "analytics",
			SchemaName:                  "public",
			TableName:                   "orders",
			BackupSettingsID:            1,
			MigrationPolicy:             "apply_asap",
			DataLoadingProcess:          "append",
			CompressionTypeOfBackupData: &compression,
			ModelLabels:                 &labels,
		},
	}
}

// TestBothNewDatasetKeysReachEveryWriteBody covers the gap qctl-r4h reported and
// the shape of the bug that would come with fixing it badly.
//
// compression_type_of_backup_data and model_labels are on BOTH dataset write
// schemas, and apply has THREE bodies that have to carry them: the create built
// from server defaults (defaultsToPostRequest + applyManifestOverrides, the path
// a real create takes), the create built straight from the manifest
// (manifestToPostRequest, the fallback when the defaults fetch fails), and the
// update (manifestToPatchRequest). Wiring two of the three and missing the
// fallback is the realistic mistake, and it would only show up on a server whose
// GET /datasets/new was failing -- so all three are asserted here together.
//
// Each assertion is on the MARSHALLED body rather than the request struct: both
// wire types have carried these fields all along, so a struct-level check passes
// against the version where nothing put a value in them.
func TestBothNewDatasetKeysReachEveryWriteBody(t *testing.T) {
	wantCompression := "snappy_stream"
	wantLabels := map[string]any{"cust_email": "Customer Email", "amt": "Amount"}

	bodies := map[string]func(t *testing.T) map[string]any{
		"create from server defaults": func(t *testing.T) map[string]any {
			t.Helper()
			req := defaultsToPostRequest(&api.DataSetSchemaFullDraft{
				AnomalyThreshold:   50,
				MaxRetryCount:      3,
				MaxTriesToFixJSON:  3,
				MigrationPolicy:    api.ApplyAsap,
				DataLoadingProcess: api.DataLoadingProcessAppend,
			})
			if err := applyManifestOverrides(&req, compressionAndLabelsManifest()); err != nil {
				t.Fatalf("applyManifestOverrides: %v", err)
			}
			return marshalToMap(t, req)
		},
		"create from the manifest alone": func(t *testing.T) map[string]any {
			t.Helper()
			req, err := manifestToPostRequest(compressionAndLabelsManifest())
			if err != nil {
				t.Fatalf("manifestToPostRequest: %v", err)
			}
			return marshalToMap(t, req)
		},
		"update": func(t *testing.T) map[string]any {
			t.Helper()
			existing := &DatasetFull{
				ID:                          123,
				VersionID:                   5,
				Name:                        "orders",
				CompressionTypeOfBackupData: compressionPtr(api.CompressionTypeZip),
				ModelLabels:                 &map[string]string{"amt": "Amt"},
				SettingsModel:               SettingsSchema{ID: 9, VersionID: 2},
			}
			req, err := manifestToPatchRequest(existing, compressionAndLabelsManifest())
			if err != nil {
				t.Fatalf("manifestToPatchRequest: %v", err)
			}
			return marshalToMap(t, req)
		},
	}

	for name, build := range bodies {
		t.Run(name, func(t *testing.T) {
			body := build(t)

			if got := body["compression_type_of_backup_data"]; got != wantCompression {
				t.Errorf("compression_type_of_backup_data = %v, want %q -- the manifest asked for it and this body did not carry it", got, wantCompression)
			}
			if got := body["model_labels"]; !reflect.DeepEqual(got, wantLabels) {
				t.Errorf("model_labels = %v, want %v -- the manifest asked for it and this body did not carry it", got, wantLabels)
			}
		})
	}
}

// TestUpdateKeepsTheStoredValueForKeysTheManifestOmits is the other half of the
// overlay contract for the two new keys: a manifest that does not mention them
// must leave what the table already has alone. patchRequestFromExisting seeds
// both from the read, so the only way to break this is an overlay that writes
// unconditionally -- which is what `if spec.X != nil` in
// applyPatchStructuredOverrides prevents.
//
// compression_type_of_backup_data would be the quiet one: the column is
// nullable, so an unconditional nil overlay would clear a table's compression on
// every unrelated `qctl apply -f`.
func TestUpdateKeepsTheStoredValueForKeysTheManifestOmits(t *testing.T) {
	existing := &DatasetFull{
		ID:                          123,
		VersionID:                   5,
		Name:                        "orders",
		CompressionTypeOfBackupData: compressionPtr(api.CompressionTypeTgz),
		ModelLabels:                 &map[string]string{"amt": "Amount"},
		SettingsModel:               SettingsSchema{ID: 9, VersionID: 2},
	}
	manifest := &TableManifest{
		APIVersion: pkgmanifest.APIVersionV1,
		Kind:       "Table",
		Metadata:   pkgmanifest.Metadata{Name: "orders"},
		Spec: TableSpec{
			DestinationID:      1,
			DatabaseName:       "analytics",
			SchemaName:         "public",
			TableName:          "orders",
			BackupSettingsID:   1,
			MigrationPolicy:    "apply_asap",
			DataLoadingProcess: "append",
		},
	}

	req, err := manifestToPatchRequest(existing, manifest)
	if err != nil {
		t.Fatalf("manifestToPatchRequest: %v", err)
	}
	body := marshalToMap(t, req)

	if got, want := body["compression_type_of_backup_data"], "tgz"; got != want {
		t.Errorf("compression_type_of_backup_data = %v, want %q from the read -- an omitted key must not change the stored value", got, want)
	}
	if got, want := body["model_labels"], (map[string]any{"amt": "Amount"}); !reflect.DeepEqual(got, want) {
		t.Errorf("model_labels = %v, want %v from the read -- an omitted key must not change the stored value", got, want)
	}
}

// TestUpdateSendsExplicitEmptyModelLabelsToClearStoredLabels pins the
// distinction between omitting model_labels and writing model_labels: {}.
// The former is covered above and keeps the stored map; the latter must survive
// YAML decoding as a non-nil pointer and reach the PATCH body as an empty object
// so the backend clears the map.
func TestUpdateSendsExplicitEmptyModelLabelsToClearStoredLabels(t *testing.T) {
	manifest, err := ParseTableManifest([]byte(`
apiVersion: qluster.ai/v1
kind: Table
metadata:
  name: orders
spec:
  destination_id: 1
  database_name: analytics
  schema_name: public
  table_name: orders
  migration_policy: apply_asap
  data_loading_process: append
  backup_settings_id: 1
  model_labels: {}
`))
	if err != nil {
		t.Fatalf("ParseTableManifest: %v", err)
	}

	existing := &DatasetFull{
		ID:            123,
		VersionID:     5,
		Name:          "orders",
		ModelLabels:   &map[string]string{"amt": "Amount"},
		SettingsModel: SettingsSchema{ID: 9, VersionID: 2},
	}
	req, err := manifestToPatchRequest(existing, manifest)
	if err != nil {
		t.Fatalf("manifestToPatchRequest: %v", err)
	}
	body := marshalToMap(t, req)

	labels, ok := body["model_labels"]
	if !ok {
		t.Fatal("model_labels is missing from the patch body; explicit {} must not be treated as omission")
	}
	if want := (map[string]any{}); !reflect.DeepEqual(labels, want) {
		t.Errorf("model_labels = %#v, want an empty object so the stored labels are cleared", labels)
	}
}

// TestAPIResponseToManifestCarriesTheTwoNewKeys is the read half. Without it
// `qctl get table <t> -o yaml` is lossy for both keys: the table has a value,
// the manifest does not mention it, and re-applying that manifest as a new table
// silently drops it.
func TestAPIResponseToManifestCarriesTheTwoNewKeys(t *testing.T) {
	resp := &DatasetFull{
		ID:                          123,
		Name:                        "orders",
		VersionID:                   5,
		CompressionTypeOfBackupData: compressionPtr(api.CompressionTypeGzip),
		ModelLabels:                 &map[string]string{"cust_email": "Customer Email"},
		SettingsModel:               SettingsSchema{ID: 9, VersionID: 2},
	}

	manifest, err := APIResponseToManifest(resp)
	if err != nil {
		t.Fatalf("APIResponseToManifest: %v", err)
	}

	got := manifest.Spec.CompressionTypeOfBackupData
	if got == nil || *got != api.CompressionTypeGzip {
		t.Errorf("spec.compression_type_of_backup_data = %v, want gzip", got)
	}
	labels := manifest.Spec.ModelLabels
	if labels == nil || (*labels)["cust_email"] != "Customer Email" {
		t.Errorf("spec.model_labels = %v, want the label the server sent", labels)
	}
}

// TestAPIResponseToManifestLeavesAbsentKeysAbsent keeps the read faithful in the
// other direction. A table with no compression chosen must read back as a
// manifest with no such key, not as one naming a default the server never sent
// -- the same faithfulness rule settingsSchemaToSettingsModel documents one
// level down.
func TestAPIResponseToManifestLeavesAbsentKeysAbsent(t *testing.T) {
	manifest, err := APIResponseToManifest(&DatasetFull{
		ID:            123,
		Name:          "orders",
		SettingsModel: SettingsSchema{ID: 9, VersionID: 2},
	})
	if err != nil {
		t.Fatalf("APIResponseToManifest: %v", err)
	}

	if manifest.Spec.CompressionTypeOfBackupData != nil {
		t.Errorf("spec.compression_type_of_backup_data = %v, want nil for a response that carried none", *manifest.Spec.CompressionTypeOfBackupData)
	}
	if manifest.Spec.ModelLabels != nil {
		t.Errorf("spec.model_labels = %v, want nil for a response that carried none", *manifest.Spec.ModelLabels)
	}
}

// TestPiiColumnsRoundTrip covers spec.pii_columns on every write body and on
// the read: the manifest's list reaches both creates and the update, an update
// that omits the key resends the stored list, an explicit [] reaches the PATCH
// as an empty array, and the read emits the key only when the table has entries.
func TestPiiColumnsRoundTrip(t *testing.T) {
	stored := &DatasetFull{
		ID:            123,
		VersionID:     5,
		Name:          "orders",
		PiiColumns:    []string{"cust_email"},
		SettingsModel: SettingsSchema{ID: 9, VersionID: 2},
	}
	withPii := func(cols *[]string) *TableManifest {
		m := compressionAndLabelsManifest()
		m.Spec.CompressionTypeOfBackupData = nil
		m.Spec.ModelLabels = nil
		m.Spec.PiiColumns = cols
		return m
	}
	set := &[]string{"cust_email", "phone"}

	bodies := map[string]struct {
		build func(t *testing.T) map[string]any
		want  any
	}{
		"create from server defaults": {func(t *testing.T) map[string]any {
			t.Helper()
			req := defaultsToPostRequest(&api.DataSetSchemaFullDraft{})
			if err := applyManifestOverrides(&req, withPii(set)); err != nil {
				t.Fatalf("applyManifestOverrides: %v", err)
			}
			return marshalToMap(t, req)
		}, []any{"cust_email", "phone"}},
		"create from the manifest alone": {func(t *testing.T) map[string]any {
			t.Helper()
			req, err := manifestToPostRequest(withPii(set))
			if err != nil {
				t.Fatalf("manifestToPostRequest: %v", err)
			}
			return marshalToMap(t, req)
		}, []any{"cust_email", "phone"}},
		"update": {func(t *testing.T) map[string]any {
			t.Helper()
			req, err := manifestToPatchRequest(stored, withPii(set))
			if err != nil {
				t.Fatalf("manifestToPatchRequest: %v", err)
			}
			return marshalToMap(t, req)
		}, []any{"cust_email", "phone"}},
		"update omitting the key": {func(t *testing.T) map[string]any {
			t.Helper()
			req, err := manifestToPatchRequest(stored, withPii(nil))
			if err != nil {
				t.Fatalf("manifestToPatchRequest: %v", err)
			}
			return marshalToMap(t, req)
		}, []any{"cust_email"}},
		"update clearing with []": {func(t *testing.T) map[string]any {
			t.Helper()
			req, err := manifestToPatchRequest(stored, withPii(&[]string{}))
			if err != nil {
				t.Fatalf("manifestToPatchRequest: %v", err)
			}
			return marshalToMap(t, req)
		}, []any{}},
	}
	for name, tc := range bodies {
		t.Run(name, func(t *testing.T) {
			if got := tc.build(t)["pii_columns"]; !reflect.DeepEqual(got, tc.want) {
				t.Errorf("pii_columns = %#v, want %#v", got, tc.want)
			}
		})
	}

	t.Run("read", func(t *testing.T) {
		manifest, err := APIResponseToManifest(stored)
		if err != nil {
			t.Fatalf("APIResponseToManifest: %v", err)
		}
		if got := manifest.Spec.PiiColumns; got == nil || !reflect.DeepEqual(*got, []string{"cust_email"}) {
			t.Errorf("spec.pii_columns = %v, want [cust_email]", got)
		}
		empty := *stored
		empty.PiiColumns = []string{}
		manifest, err = APIResponseToManifest(&empty)
		if err != nil {
			t.Fatalf("APIResponseToManifest: %v", err)
		}
		if manifest.Spec.PiiColumns != nil {
			t.Errorf("spec.pii_columns = %v, want the key absent for a table with none", *manifest.Spec.PiiColumns)
		}
	})
}

func compressionPtr(c api.CompressionType) *api.CompressionType { return &c }

func marshalToMap(t *testing.T, v any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	return body
}

// TestSettingsModelPiiColumnsNamesItsNewHome covers a manifest written before
// pii_columns moved off the settings model: it exits 2 with the new location,
// not a "possible typo".
func TestSettingsModelPiiColumnsNamesItsNewHome(t *testing.T) {
	_, err := ParseTableManifest([]byte(`
apiVersion: qluster.ai/v1
kind: Table
metadata:
  name: orders
spec:
  destination_id: 1
  database_name: analytics
  table_name: orders
  backup_settings_id: 1
  settings_model:
    pii_columns: [cust_email]
`))
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Fatalf("exit code = %d, want %d (err: %v)", code, qerrors.ExitBadUsage, err)
	}
	if !strings.Contains(err.Error(), "spec.pii_columns") {
		t.Errorf("error = %v, want it to name spec.pii_columns", err)
	}
}
