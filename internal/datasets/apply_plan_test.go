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

func TestValidateTableUpdateAllowsOmittedSchemaAndRejectsChangedSchema(t *testing.T) {
	existing := &DatasetFull{
		DestinationID: 3, DatabaseName: "analytics", SchemaName: "default",
		TableName: "orders", BackupSettingsID: 1,
	}
	manifest := &TableManifest{Spec: TableSpec{
		DestinationID: 3, DatabaseName: "analytics", TableName: "orders", BackupSettingsID: 1,
	}}
	if err := validateTableUpdate(existing, manifest); err != nil {
		t.Fatalf("omitted schema should preserve the live value: %v", err)
	}
	manifest.Spec.SchemaName = "public"
	if err := validateTableUpdate(existing, manifest); qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
		t.Fatalf("changed schema exit code = %d, want %d: %v", qerrors.GetExitCode(err), qerrors.ExitBadUsage, err)
	}
}

func TestTableManifestChangesReportsOnlyAuthoredChangedPaths(t *testing.T) {
	oldFormats := []string{"%Y-%m-%d"}
	newFormats := []string{"%Y-%m-%d", "%d/%m/%Y"}
	oldMapping := map[string]string{"N": "no"}
	emptyMapping := map[string]string{}
	existing := &DatasetFull{
		ID:                 3,
		VersionID:          5,
		Name:               "orders",
		DestinationID:      4,
		DatabaseName:       "analytics",
		SchemaName:         "public",
		TableName:          "orders",
		BackupSettingsID:   2,
		MigrationPolicy:    api.ApplyAsap,
		DataLoadingProcess: api.DataLoadingProcessAppend,
		AnomalyThreshold:   10,
		DetectAnomalies:    boolPtr(true),
		SettingsModel: api.SettingsSchema{
			ID:              7,
			VersionID:       9,
			DatetimeFormats: &oldFormats,
			ValueMapping:    oldMapping,
		},
	}
	manifest := &TableManifest{
		APIVersion: pkgmanifest.APIVersionV1,
		Kind:       "Table",
		Metadata:   pkgmanifest.Metadata{Name: "orders"},
		Spec: TableSpec{
			DestinationID:      4,
			DatabaseName:       "analytics",
			SchemaName:         "public",
			TableName:          "orders",
			BackupSettingsID:   2,
			MigrationPolicy:    api.ApplyAsap,
			DataLoadingProcess: api.DataLoadingProcessAppend,
			AnomalyThreshold:   intPointer(12),
			DetectAnomalies:    boolPtr(true),
			SettingsModel: &SettingsModel{
				DatetimeFormats: &newFormats,
				ValueMapping:    &emptyMapping,
			},
		},
	}

	body, err := manifestToPatchRequest(existing, manifest)
	if err != nil {
		t.Fatalf("manifestToPatchRequest: %v", err)
	}
	changes, err := tableManifestChanges(existing, manifest, &body)
	if err != nil {
		t.Fatalf("tableManifestChanges: %v", err)
	}

	gotPaths := make([]string, 0, len(changes))
	for _, change := range changes {
		gotPaths = append(gotPaths, change.Path)
	}
	wantPaths := []string{
		"spec.anomaly_threshold",
		"spec.settings_model.datetime_formats",
		"spec.settings_model.value_mapping",
	}
	if !reflect.DeepEqual(gotPaths, wantPaths) {
		t.Fatalf("changed paths = %#v, want %#v", gotPaths, wantPaths)
	}

	want := "spec.anomaly_threshold: 10 → 12\n" +
		"spec.settings_model.datetime_formats: [\"%Y-%m-%d\"] → [\"%Y-%m-%d\",\"%d/%m/%Y\"]\n" +
		"spec.settings_model.value_mapping: {\"N\":\"no\"} → {}\n"
	if got := FormatTableFieldChanges(changes, ""); got != want {
		t.Errorf("formatted changes:\n%s\nwant:\n%s", got, want)
	}
}

func TestTableManifestChangesMarksCreateValuesAbsent(t *testing.T) {
	manifest := &TableManifest{
		APIVersion: pkgmanifest.APIVersionV1,
		Kind:       "Table",
		Metadata:   pkgmanifest.Metadata{Name: "orders"},
		Spec: TableSpec{
			DestinationID:      4,
			DatabaseName:       "analytics",
			SchemaName:         "public",
			TableName:          "orders",
			BackupSettingsID:   2,
			MigrationPolicy:    api.ApplyAsap,
			DataLoadingProcess: api.DataLoadingProcessAppend,
			AnomalyThreshold:   intPointer(12),
		},
	}
	body, err := manifestToPostRequest(manifest)
	if err != nil {
		t.Fatalf("manifestToPostRequest: %v", err)
	}
	changes, err := tableManifestChanges(nil, manifest, &body)
	if err != nil {
		t.Fatalf("tableManifestChanges: %v", err)
	}

	want := "metadata.name: <absent> → \"orders\"\n" +
		"spec.anomaly_threshold: <absent> → 12\n" +
		"spec.backup_settings_id: <absent> → 2\n" +
		"spec.data_loading_process: <absent> → \"append\"\n" +
		"spec.database_name: <absent> → \"analytics\"\n" +
		"spec.destination_id: <absent> → 4\n" +
		"spec.migration_policy: <absent> → \"apply_asap\"\n" +
		"spec.schema_name: <absent> → \"public\"\n" +
		"spec.table_name: <absent> → \"orders\"\n"
	if got := FormatTableFieldChanges(changes, ""); got != want {
		t.Errorf("formatted create changes:\n%s\nwant:\n%s", got, want)
	}
}

// The diff block goes straight to a terminal and its values are the server's
// own bytes. json.Marshal escapes C0 but leaves C1 alone, so U+009B -- a CSI
// introducer by itself -- is the shape that survives the encoder.
func TestFormatTableFieldChangesSanitizesValues(t *testing.T) {
	changes := []TableFieldChange{{
		Path:      "spec.database_name",
		OldValue:  json.RawMessage("\"analytics\x1b[31m\""),
		OldExists: true,
		NewValue:  json.RawMessage("\"analytics\u009b31m\""),
		NewExists: true,
	}}

	got := FormatTableFieldChanges(changes, "  ")
	if strings.ContainsRune(got, '\x1b') || strings.ContainsRune(got, '') {
		t.Fatalf("diff line kept a terminal control: %q", got)
	}
	for _, want := range []string{`analytics\x1b[31m`, `analytics\x9b31m`} {
		if !strings.Contains(got, want) {
			t.Errorf("diff line missing visible escape %q: %q", want, got)
		}
	}
}
