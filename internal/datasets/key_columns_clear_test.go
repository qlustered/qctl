package datasets

import (
	"encoding/json"
	"testing"

	"github.com/qlustered/qctl/internal/api"
)

const keyColumnsManifestHead = `apiVersion: qluster.ai/v1
kind: Table
metadata:
  name: orders
spec:
  destination_id: 4
  database_name: analytics
  schema_name: public
  table_name: orders
  backup_settings_id: 2
  migration_policy: ask_user
  data_loading_process: mirror
`

func keyedTable() *DatasetFull {
	keys := []string{"order_id"}
	return &DatasetFull{
		ID: 3, VersionID: 5, Name: "orders", DestinationID: 4, DatabaseName: "analytics",
		SchemaName: "public", TableName: "orders", BackupSettingsID: 2,
		MigrationPolicy: api.AskUser, DataLoadingProcess: api.DataLoadingProcessMirror,
		SettingsModel: api.SettingsSchema{ID: 7, VersionID: 9, KeyColumns: &keys},
	}
}

// wireSettingsKeyColumns returns the settings_model.key_columns bytes the
// PATCH for manifest sends, and whether the key is there at all.
func wireSettingsKeyColumns(t *testing.T, existing *DatasetFull, manifest *TableManifest) (string, bool) {
	t.Helper()
	req, err := manifestToPatchRequest(existing, manifest)
	if err != nil {
		t.Fatalf("manifestToPatchRequest: %v", err)
	}
	raw, err := json.Marshal(datasetPatchWireBody{body: &req})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var body struct {
		SettingsModel map[string]json.RawMessage `json:"settings_model"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	value, ok := body.SettingsModel["key_columns"]
	return string(value), ok
}

func TestKeyColumnsWrittenNullClearsTheStoredKeys(t *testing.T) {
	for name, line := range map[string]string{
		"null":  "    key_columns: null\n",
		"tilde": "    key_columns: ~\n",
		"empty": "    key_columns:\n",
	} {
		t.Run(name, func(t *testing.T) {
			manifest, err := ParseTableManifest([]byte(keyColumnsManifestHead + "  settings_model:\n" + line))
			if err != nil {
				t.Fatalf("ParseTableManifest: %v", err)
			}
			got, ok := wireSettingsKeyColumns(t, keyedTable(), manifest)
			if !ok || got != "null" {
				t.Errorf("settings_model.key_columns on the wire = %q (present %v), want null", got, ok)
			}

			body, err := manifestToPatchRequest(keyedTable(), manifest)
			if err != nil {
				t.Fatalf("manifestToPatchRequest: %v", err)
			}
			changes, err := tableManifestChanges(keyedTable(), manifest, datasetPatchWireBody{body: &body})
			if err != nil {
				t.Fatalf("tableManifestChanges: %v", err)
			}
			want := "spec.settings_model.key_columns: [\"order_id\"] → null\n"
			if got := FormatTableFieldChanges(changes, ""); got != want {
				t.Errorf("changes = %q, want %q", got, want)
			}
		})
	}
}

func TestKeyColumnsOmittedKeepsTheStoredKeys(t *testing.T) {
	manifest, err := ParseTableManifest([]byte(keyColumnsManifestHead + "  settings_model:\n    datetime_formats: [\"%Y\"]\n"))
	if err != nil {
		t.Fatalf("ParseTableManifest: %v", err)
	}
	if got, _ := wireSettingsKeyColumns(t, keyedTable(), manifest); got != `["order_id"]` {
		t.Errorf("settings_model.key_columns on the wire = %q, want the stored [\"order_id\"]", got)
	}
}

func TestKeyColumnsNullOnAKeylessTableIsNoChange(t *testing.T) {
	manifest, err := ParseTableManifest([]byte(keyColumnsManifestHead + "  settings_model:\n    key_columns: null\n"))
	if err != nil {
		t.Fatalf("ParseTableManifest: %v", err)
	}
	keyless := keyedTable()
	keyless.SettingsModel.KeyColumns = nil
	body, err := manifestToPatchRequest(keyless, manifest)
	if err != nil {
		t.Fatalf("manifestToPatchRequest: %v", err)
	}
	changes, err := tableManifestChanges(keyless, manifest, datasetPatchWireBody{body: &body})
	if err != nil {
		t.Fatalf("tableManifestChanges: %v", err)
	}
	if len(changes) != 0 {
		t.Errorf("changes = %q, want none", FormatTableFieldChanges(changes, ""))
	}
}
