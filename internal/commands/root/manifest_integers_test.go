package root

import (
	"encoding/json"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"gopkg.in/yaml.v3"
)

func TestManifestFractionalIntegersFailBeforeAuth(t *testing.T) {
	for _, tc := range []struct {
		kind string
		spec string
		path string
		cmds [][]string
	}{
		{"Table", "destination_id: 2.7", "spec.destination_id", [][]string{{"apply"}, {"diff"}}},
		{"Table", "settings_model: {max_boundary_element_length: 2.7}", "spec.settings_model.max_boundary_element_length", [][]string{{"apply"}, {"diff"}}},
		{"TableSchema", "fields: {amount: {type: String, args: [2.7]}}", `spec.fields["amount"].args[0]`, [][]string{{"apply"}, {"diff"}}},
		{"Destination", "port: 2.7", "spec.port", [][]string{{"apply"}, {"apply", "destination"}}},
		{"BackupSettings", "backup_s3_default_buffer_size: 2.7", "spec.backup_s3_default_buffer_size", [][]string{{"apply"}, {"apply", "backup-settings"}}},
		{"CloudSource", "dataset_id: 2.7", "spec.dataset_id", [][]string{{"apply"}, {"apply", "cloud-source"}}},
		{"RuleReviewPolicy", "min_reviewers: 2.7", "spec.min_reviewers", [][]string{{"apply"}}},
		{"DataReviewPolicy", "min_reviewers: 2.7", "spec.min_reviewers", [][]string{{"apply"}}},
		{"LinkedTable", "spine: {identity_hash_version: 2.7}", "spec.spine.identity_hash_version", [][]string{{"apply"}, {"diff"}}},
		{"Deliverable", "sheets: {Data: {data_start_row: 2.7}}", `spec.sheets["Data"].data_start_row`, [][]string{{"apply"}, {"diff"}}},
		{"Deliverable", "sheets: {Data: {header_rows: [1.9]}}", `spec.sheets["Data"].header_rows[0]`, [][]string{{"apply"}, {"diff"}}},
		{"TableRule", "dataset_id: 2.7", "spec.dataset_id", [][]string{{"apply"}, {"diff"}, {"table-rule", "test-filter"}}},
		{"DryRunJob", "max_rows: 2.7", "spec.max_rows", [][]string{{"create", "dry-run-job"}}},
	} {
		for _, format := range []string{"yaml", "json"} {
			for _, args := range tc.cmds {
				t.Run(tc.kind+"/"+tc.path+"/"+format+"/"+strings.Join(args, " "), func(t *testing.T) {
					env := testutil.NewTestEnv(t)
					mock := testutil.NewMockAPIServer()
					t.Cleanup(mock.Close)
					env.SetupConfigWithOrg(mock.URL(), "", scopeTestOrgID)
					metadata := "{name: example}"
					switch tc.kind {
					case "Deliverable":
						metadata = "{slug: example}"
					case "TableRule", "DryRunJob":
						metadata = "{}"
					}
					data := "apiVersion: qluster.ai/v1\nkind: " + tc.kind + "\nmetadata: " + metadata + "\nspec: {" + tc.spec + "}\n"
					if format == "json" {
						var value any
						if err := yaml.Unmarshal([]byte(data), &value); err != nil {
							t.Fatal(err)
						}
						encoded, err := json.Marshal(value)
						if err != nil {
							t.Fatal(err)
						}
						data = string(encoded)
					}
					path := writeManifest(t, t.TempDir(), "manifest."+format, data)
					args = append(append([]string{}, args...), "-f", path, "--context", "default")
					out, _, err := runManifest(t, args...)
					if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
						t.Fatalf("exit = %d, want 2; error = %v", code, err)
					}
					if format == "yaml" && !strings.Contains(err.Error(), tc.path) {
						t.Errorf("error = %v, want field %s", err, tc.path)
					}
					if out != "" || len(mock.Requests()) != 0 {
						t.Errorf("stdout = %q, requests = %v; want neither", out, mock.Requests())
					}
				})
			}
		}
	}
}
