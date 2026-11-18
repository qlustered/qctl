package get

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

func setupBackupSettingsGetCommand() *cobra.Command {
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
	getCmd := &cobra.Command{Use: "get"}
	getCmd.AddCommand(NewBackupSettingsCommand())
	root.AddCommand(getCmd)
	return root
}

func backupSettingsTinyFixture() backup_settings.BackupSettingsTiny {
	name := "nightly"
	return backup_settings.BackupSettingsTiny{
		ID: 7, Name: &name, BackupType: api.BackupTypeS3, Bucket: "archive",
		CreatedAt: time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC),
	}
}

func TestGetBackupSettings(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/backup-settings", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if query.Get("page") != "2" || query.Get("limit") != "5" || query.Get("order_by") != "bucket" ||
			query.Get("reverse") != "false" || query.Get("search_query") != "archive" {
			t.Errorf("query = %s", r.URL.RawQuery)
		}
		testutil.RespondJSON(w, http.StatusOK, backup_settings.ListPage{
			Results: []backup_settings.BackupSettingsTiny{backupSettingsTinyFixture()},
			Page:    testutil.IntPtr(2), TotalRows: testutil.IntPtr(1),
		})
	})
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "token")

	cmd := setupBackupSettingsGetCommand()
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetArgs([]string{"get", "backup-settings", "--page", "2", "--limit", "5", "--order-by", "bucket", "--reverse", "--search", "archive"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"nightly", "s3", "archive"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("output missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestGetBackupSettingsRejectsLocalErrorsBeforeAuth(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"page", []string{"--page", "0"}},
		{"sort", []string{"--order-by", "name"}},
		{"search", []string{"--search", "ab"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()
			cmd := setupBackupSettingsGetCommand()
			cmd.SetArgs(append([]string{"get", "backup-settings"}, tc.args...))
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
