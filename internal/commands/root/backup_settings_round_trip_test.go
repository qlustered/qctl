package root

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/backup_settings"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/testutil"
	"gopkg.in/yaml.v3"
)

func TestBackupSettingsDescribeApplyByID(t *testing.T) {
	for _, format := range []string{"yaml", "json"} {
		for _, tc := range []struct {
			name, storedName, newName string
			nullName, typed, omitName bool
		}{
			{name: "unnamed", nullName: true},
			{name: "empty name", typed: true},
			{name: "adopt", nullName: true, newName: "nightly", typed: true},
			{name: "named", storedName: "nightly"},
			{name: "keep name", storedName: "nightly", omitName: true, typed: true},
			{name: "rename", storedName: "old", newName: "nightly"},
		} {
			t.Run(format+"/"+tc.name, func(t *testing.T) {
				env := testutil.NewTestEnv(t)
				defer env.Cleanup()
				mock := testutil.NewMockAPIServer()
				defer mock.Close()
				env.SetupConfigWithOrg(mock.URL(), "", scopeTestOrgID)
				endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
				env.SetupCredential(endpointKey, scopeTestOrgID, "token")
				row := backup_settings.BackupSettingsFull{
					ID: 7, VersionID: 3, Name: &tc.storedName, BackupType: api.BackupTypeS3,
					BackupS3Bucket: "archive", BackupS3AccessKey: "sensitive-access",
					BackupS3SecretKey: testutil.StringPtr("sensitive-secret"), CreatedAt: time.Now(),
				}
				if tc.nullName {
					row.Name = nil
				}
				base := "/api/orgs/" + scopeTestOrgID + "/backup-settings"
				gets, patches, unexpected := 0, 0, 0
				mock.RegisterHandler("GET", base+"/7", func(w http.ResponseWriter, _ *http.Request) {
					gets++
					testutil.RespondJSON(w, http.StatusOK, row)
				})
				for _, method := range []string{"GET", "POST"} {
					mock.RegisterHandler(method, base, func(w http.ResponseWriter, _ *http.Request) {
						unexpected++
						testutil.RespondError(w, http.StatusInternalServerError, "unexpected name lookup or create")
					})
				}
				mock.RegisterHandler("PATCH", base, func(w http.ResponseWriter, r *http.Request) {
					patches++
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Fatal(err)
					}
					if body["id"] != float64(7) || body["version_id"] != float64(4) || body["backup_s3_bucket"] != "updated" {
						t.Errorf("PATCH identity/version/bucket = %#v", body)
					}
					wantName := tc.storedName
					if tc.newName != "" {
						wantName = tc.newName
					} else if tc.omitName {
						wantName = ""
					}
					if got, present := body["name"]; (wantName == "" && present) || (wantName != "" && got != wantName) {
						t.Errorf("PATCH name = %#v, present = %t, want %q (omitted when empty)", got, present, wantName)
					}
					for _, key := range []string{"backup_s3_access_key", "backup_s3_secret_key", "backup_gpg_private_key"} {
						if _, present := body[key]; present {
							t.Errorf("PATCH sent omitted credential %s", key)
						}
					}
					if tc.newName != "" {
						row.Name = &tc.newName
					}
					testutil.RespondJSON(w, http.StatusOK, row)
				})

				emitted, err := runRoot(t, "describe", "backup-settings", "7", "-o", format)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(emitted, "sensitive-") || strings.Contains(emitted, "backup_s3_secret_key") {
					t.Fatalf("describe exposed credentials: %s", emitted)
				}
				var m backup_settings.Manifest
				if err := yaml.Unmarshal([]byte(emitted), &m); err != nil {
					t.Fatal(err)
				}
				if m.Metadata.ID != "7" || m.Metadata.Name != tc.storedName {
					t.Fatalf("describe metadata = %#v", m.Metadata)
				}
				m.Spec.BackupS3Bucket = testutil.StringPtr("updated")
				if tc.newName != "" {
					m.Metadata.Name = tc.newName
				} else if tc.omitName {
					m.Metadata.Name = ""
				}
				m.Status.ID = 99
				row.VersionID = 4
				var edited []byte
				if format == "json" {
					edited, err = json.Marshal(&m)
				} else {
					edited, err = yaml.Marshal(&m)
				}
				if err != nil {
					t.Fatal(err)
				}
				path := env.CreateFile("backup-settings."+format, string(edited))
				args := []string{"apply"}
				if tc.typed {
					args = append(args, "backup-settings")
				}
				out, err := runRoot(t, append(args, "-f", path, "-o", "table")...)
				wantRef := tc.storedName
				if tc.newName != "" {
					wantRef = tc.newName
				} else if wantRef == "" {
					wantRef = "7"
				}
				if err != nil || out != fmt.Sprintf("backup-settings/%s updated\n", wantRef) {
					t.Fatalf("apply = %q, %v", out, err)
				}
				if gets != 2 || patches != 1 || unexpected != 0 {
					t.Errorf("requests: GET detail=%d, PATCH=%d, unexpected=%d", gets, patches, unexpected)
				}
			})
		}
	}
}
