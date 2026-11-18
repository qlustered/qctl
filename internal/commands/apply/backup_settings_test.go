package apply

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/backup_settings"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

func applyBackupSettingsFixture(name string) backup_settings.BackupSettingsFull {
	return backup_settings.BackupSettingsFull{
		ID: 7, VersionID: 3, Name: &name, BackupType: api.BackupTypeS3,
		BackupS3Bucket: "archive", CreatedAt: time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC),
	}
}

func TestApplyBackupSettingsCreateAndGenericDispatch(t *testing.T) {
	for _, typed := range []bool{true, false} {
		t.Run(map[bool]string{true: "typed", false: "generic"}[typed], func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()
			mock := testutil.NewMockAPIServer()
			defer mock.Close()
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/backup-settings", func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, backup_settings.ListPage{Results: []backup_settings.BackupSettingsTiny{}, Page: testutil.IntPtr(1), TotalRows: testutil.IntPtr(0)})
			})
			var body map[string]any
			mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/backup-settings", func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				testutil.RespondJSON(w, http.StatusOK, applyBackupSettingsFixture("nightly"))
			})
			env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
			endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
			env.SetupCredential(endpointKey, testOrgID, "token")
			t.Setenv("BACKUP_SECRET", "expanded-secret")
			path := env.CreateFile("backup-settings.yaml", `
apiVersion: qluster.ai/v1
kind: BackupSettings
metadata:
  name: nightly
spec:
  backup_type: s3
  backup_s3_bucket: archive
  backup_s3_access_key: pa$$w0rd
  backup_s3_secret_key: ${BACKUP_SECRET}
status:
  id: 99
  version_id: 1
  created_at: 2025-01-01T00:00:00Z
`)

			cmd := setupApplyRoot()
			var stdout bytes.Buffer
			cmd.SetOut(&stdout)
			args := []string{"apply", "-f", path}
			if typed {
				args = []string{"apply", "backup-settings", "-f", path}
			}
			cmd.SetArgs(args)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			if got := stdout.String(); got != "backup-settings/nightly created\n" {
				t.Errorf("output = %q", got)
			}
			if body["name"] != "nightly" || body["backup_s3_secret_key"] != "expanded-secret" || body["backup_s3_access_key"] != "pa$$w0rd" {
				t.Errorf("body = %#v", body)
			}
			if _, ok := body["version_id"]; ok {
				t.Errorf("create body included status version: %#v", body)
			}
		})
	}
}

func TestApplyBackupSettingsUpdatePreservesOmittedCredentials(t *testing.T) {
	for _, backupType := range []string{"s3", "s3_compatible"} {
		for _, typed := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/typed=%t", backupType, typed), func(t *testing.T) {
				testApplyBackupSettingsUpdatePreservesOmittedCredentials(t, backupType, typed)
			})
		}
	}
}

func testApplyBackupSettingsUpdatePreservesOmittedCredentials(t *testing.T, backupType string, typed bool) {
	t.Helper()
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	name := "nightly"
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/backup-settings", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, backup_settings.ListPage{
			Results: []backup_settings.BackupSettingsTiny{{ID: 7, Name: &name, BackupType: api.BackupTypeS3, Bucket: "old", CreatedAt: time.Now()}},
			Page:    testutil.IntPtr(1), TotalRows: testutil.IntPtr(1),
		})
	})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/backup-settings/7", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, applyBackupSettingsFixture(name))
	})
	var body map[string]any
	mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/backup-settings", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		testutil.RespondJSON(w, http.StatusOK, applyBackupSettingsFixture(name))
	})
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "token")
	path := env.CreateFile("backup-settings.yaml", fmt.Sprintf(`
apiVersion: qluster.ai/v1
kind: BackupSettings
metadata:
  name: nightly
spec:
  backup_type: %s
  backup_s3_bucket: archive
  backup_s3_endpoint_url: https://store.example.com
`, backupType))

	cmd := setupApplyRoot()
	args := []string{"apply", "-f", path}
	if typed {
		args = []string{"apply", "backup-settings", "-f", path}
	}
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if body["id"] != float64(7) || body["version_id"] != float64(3) {
		t.Errorf("identity/version = %#v", body)
	}
	for _, key := range []string{"backup_s3_access_key", "backup_s3_secret_key", "backup_gpg_private_key"} {
		if _, ok := body[key]; ok {
			t.Errorf("omitted credential %q was sent: %#v", key, body)
		}
	}
}

func TestApplyBackupSettingsValidationIsBadUsage(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	path := env.CreateFile("backup-settings.yaml", `
apiVersion: qluster.ai/v1
kind: BackupSettings
metadata:
  name: nightly
spec:
  backup_type: s3
`)
	cmd := setupApplyRoot()
	cmd.SetArgs([]string{"apply", "backup-settings", "-f", path})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "spec.backup_s3_bucket") {
		t.Errorf("error = %v", err)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
}

func TestApplyBackupSettingsRejectsInvalidIDBeforeAuth(t *testing.T) {
	for _, id := range []string{"0", "-1", "not-an-id", "2.7", "999999999999999999999999"} {
		t.Run(id, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()
			path := env.CreateFile("invalid.yaml", fmt.Sprintf(`
apiVersion: qluster.ai/v1
kind: BackupSettings
metadata:
  id: %q
  name: nightly
spec:
  backup_type: s3
  backup_s3_bucket: archive
`, id))
			cmd := setupApplyRoot()
			cmd.SetArgs([]string{"apply", "-f", path})
			err := cmd.Execute()
			if qerrors.GetExitCode(err) != qerrors.ExitBadUsage || !strings.Contains(err.Error(), "metadata.id") {
				t.Fatalf("apply invalid ID = %v (exit %d)", err, qerrors.GetExitCode(err))
			}
		})
	}
}

func TestApplyBackupSettingsIDFailureNeverFallsBack(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusForbidden, http.StatusConflict} {
		for _, typed := range []bool{true, false} {
			t.Run(fmt.Sprintf("%d/typed=%t", status, typed), func(t *testing.T) {
				env := testutil.NewTestEnv(t)
				defer env.Cleanup()
				mock := testutil.NewMockAPIServer()
				defer mock.Close()
				base := "/api/orgs/" + testOrgID + "/backup-settings"
				gets, patches, unexpected := 0, 0, 0
				mock.RegisterHandler("GET", base+"/7", func(w http.ResponseWriter, _ *http.Request) {
					gets++
					if status == http.StatusConflict {
						testutil.RespondJSON(w, http.StatusOK, applyBackupSettingsFixture(""))
						return
					}
					testutil.RespondError(w, status, "cannot read selected ID")
				})
				mock.RegisterHandler("PATCH", base, func(w http.ResponseWriter, _ *http.Request) {
					patches++
					testutil.RespondError(w, status, "name or version conflict")
				})
				for _, method := range []string{"GET", "POST"} {
					mock.RegisterHandler(method, base, func(w http.ResponseWriter, _ *http.Request) {
						unexpected++
						testutil.RespondJSON(w, http.StatusOK, applyBackupSettingsFixture("nightly"))
					})
				}
				env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
				endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
				env.SetupCredential(endpointKey, testOrgID, "token")
				path := env.CreateFile("backup-settings.yaml", `
apiVersion: qluster.ai/v1
kind: BackupSettings
metadata:
  id: 7
  name: nightly
spec:
  backup_type: s3
  backup_s3_bucket: archive
`)
				cmd := setupApplyRoot()
				args := []string{"apply"}
				if typed {
					args = append(args, "backup-settings")
				}
				cmd.SetArgs(append(args, "-f", path))
				err := cmd.Execute()
				wantExit, wantPatches := qerrors.ExitNotFound, 0
				switch status {
				case http.StatusForbidden:
					wantExit = qerrors.ExitUnauthorized
				case http.StatusConflict:
					wantExit, wantPatches = qerrors.ExitBadUsage, 1
				}
				if qerrors.GetExitCode(err) != wantExit {
					t.Errorf("exit = %d, want %d: %v", qerrors.GetExitCode(err), wantExit, err)
				}
				if gets != 1 || patches != wantPatches || unexpected != 0 {
					t.Errorf("requests: GET detail=%d, PATCH=%d, unexpected=%d", gets, patches, unexpected)
				}
			})
		}
	}
}

func TestApplyBackupSettingsCreateRequiresS3Credentials(t *testing.T) {
	for _, backupType := range []string{"s3", "s3_compatible"} {
		for _, typed := range []bool{true, false} {
			for _, tc := range []struct {
				name, fields string
				missing      []string
			}{
				{"both omitted", "", []string{"backup_s3_access_key", "backup_s3_secret_key"}},
				{"access omitted", "  backup_s3_secret_key: test-secret\n", []string{"backup_s3_access_key"}},
				{"secret omitted", "  backup_s3_access_key: test-access\n", []string{"backup_s3_secret_key"}},
				{"access empty", "  backup_s3_access_key: ''\n  backup_s3_secret_key: test-secret\n", []string{"backup_s3_access_key"}},
				{"secret empty", "  backup_s3_access_key: test-access\n  backup_s3_secret_key: ''\n", []string{"backup_s3_secret_key"}},
				{"access env empty", "  backup_s3_access_key: ${BACKUP_EMPTY}\n  backup_s3_secret_key: test-secret\n", []string{"backup_s3_access_key"}},
				{"secret env empty", "  backup_s3_access_key: test-access\n  backup_s3_secret_key: ${BACKUP_EMPTY}\n", []string{"backup_s3_secret_key"}},
			} {
				t.Run(fmt.Sprintf("%s/typed=%t/%s", backupType, typed, tc.name), func(t *testing.T) {
					env := testutil.NewTestEnv(t)
					defer env.Cleanup()
					mock := testutil.NewMockAPIServer()
					defer mock.Close()
					lookups, creates := 0, 0
					mock.RegisterHandler(http.MethodGet, "/api/orgs/"+testOrgID+"/backup-settings", func(w http.ResponseWriter, _ *http.Request) {
						lookups++
						testutil.RespondJSON(w, http.StatusOK, backup_settings.ListPage{Results: []backup_settings.BackupSettingsTiny{}, TotalRows: testutil.IntPtr(0)})
					})
					mock.RegisterHandler(http.MethodPost, "/api/orgs/"+testOrgID+"/backup-settings", func(w http.ResponseWriter, _ *http.Request) {
						creates++
						testutil.RespondJSON(w, http.StatusOK, applyBackupSettingsFixture("nightly"))
					})
					env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
					endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
					env.SetupCredential(endpointKey, testOrgID, "token")
					t.Setenv("BACKUP_EMPTY", "")
					path := env.CreateFile("backup-settings.yaml", fmt.Sprintf(`
apiVersion: qluster.ai/v1
kind: BackupSettings
metadata:
  name: nightly
spec:
  backup_type: %s
  backup_s3_bucket: archive
  backup_s3_endpoint_url: https://store.example.com
%sstatus:
  id: 99
  version_id: 1
`, backupType, tc.fields))
					cmd := setupApplyRoot()
					var output bytes.Buffer
					cmd.SetOut(&output)
					cmd.SetErr(&output)
					args := []string{"apply", "-f", path}
					if typed {
						args = []string{"apply", "backup-settings", "-f", path}
					}
					cmd.SetArgs(args)
					err := cmd.Execute()
					if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
						t.Fatalf("exit code = %d, want %d: %v", got, qerrors.ExitBadUsage, err)
					}
					for _, field := range tc.missing {
						if !strings.Contains(err.Error(), "spec."+field) {
							t.Errorf("error = %v, want missing field %s", err, field)
						}
					}
					for _, secret := range []string{"test-access", "test-secret"} {
						if strings.Contains(err.Error()+output.String(), secret) {
							t.Errorf("diagnostic leaked credential %q", secret)
						}
					}
					if lookups != 1 || creates != 0 {
						t.Errorf("requests: lookups = %d, creates = %d; want 1 and 0", lookups, creates)
					}
				})
			}
		}
	}
}
