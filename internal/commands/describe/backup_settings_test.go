package describe

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/backup_settings"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

func setupBackupSettingsDescribeCommand() *cobra.Command {
	root := &cobra.Command{Use: "qctl"}
	root.PersistentFlags().String("server", "", "")
	root.PersistentFlags().String("user", "", "")
	root.PersistentFlags().StringP("output", "o", "table", "")
	root.PersistentFlags().Bool("no-headers", false, "")
	root.PersistentFlags().String("columns", "", "")
	root.PersistentFlags().Int("max-column-width", 80, "")
	root.PersistentFlags().Bool("allow-plaintext-secrets", false, "")
	root.PersistentFlags().Bool("allow-insecure-http", false, "")
	root.PersistentFlags().CountP("verbose", "v", "")
	describeCmd := &cobra.Command{Use: "describe"}
	describeCmd.AddCommand(NewBackupSettingsCommand())
	root.AddCommand(describeCmd)
	return root
}

func backupSettingsFullFixture() backup_settings.BackupSettingsFull {
	name, secret := "nightly", "masked-or-secret"
	return backup_settings.BackupSettingsFull{
		ID: 7, VersionID: 3, Name: &name, BackupType: api.BackupTypeS3,
		BackupS3Bucket: "archive", BackupS3AccessKey: secret, BackupS3SecretKey: &secret,
		BackupGpgPrivateKey: &secret, BackupGpgPassphrase: &secret, BackupGsServiceAccountKey: &secret,
		CreatedAt: time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC),
	}
}

func TestDescribeBackupSettingsOmitsCredentials(t *testing.T) {
	for _, output := range []string{"yaml", "json"} {
		t.Run(output, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()
			mock := testutil.NewMockAPIServer()
			defer mock.Close()
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/backup-settings/7", func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, backupSettingsFullFixture())
			})
			env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
			endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
			env.SetupCredential(endpointKey, testOrgID, "token")

			cmd := setupBackupSettingsDescribeCommand()
			var stdout bytes.Buffer
			cmd.SetOut(&stdout)
			args := []string{"describe", "backup-settings", "7"}
			if output == "json" {
				args = append(args, "-o", "json")
			}
			cmd.SetArgs(args)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			got := stdout.String()
			for _, want := range []string{"BackupSettings", "nightly", "archive", "version_id"} {
				if !strings.Contains(got, want) {
					t.Errorf("%s output missing %q:\n%s", output, want, got)
				}
			}
			for _, secret := range []string{"backup_s3_access_key", "backup_s3_secret_key", "backup_gpg_private_key", "backup_gpg_passphrase", "backup_gs_service_account_key", "masked-or-secret"} {
				if strings.Contains(got, secret) {
					t.Errorf("%s output exposed %q:\n%s", output, secret, got)
				}
			}
		})
	}
}

func TestDescribeBackupSettingsRejectsInvalidIDBeforeAuth(t *testing.T) {
	for _, id := range []string{"not-an-id", "0", "-1"} {
		t.Run(id, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()
			cmd := setupBackupSettingsDescribeCommand()
			args := []string{"describe", "backup-settings", id}
			if strings.HasPrefix(id, "-") {
				args = []string{"describe", "backup-settings", "--", id}
			}
			cmd.SetArgs(args)
			err := cmd.Execute()
			if err == nil {
				t.Fatal("expected error")
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d: %v", got, qerrors.ExitBadUsage, err)
			}
		})
	}
}
