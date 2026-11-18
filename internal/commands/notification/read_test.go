package notification

import (
	"bytes"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const testOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

var testNotificationUUID = uuid.MustParse("0190bbbb-1111-2222-3333-444444444444")

func setupNotificationTestCommand() *cobra.Command {
	// SilenceUsage/SilenceErrors mirror the real root (internal/commands/root/
	// root.go). Without them a RunE error dumps the whole usage block --
	// including the --type help, which lists every type name -- into the
	// stdout these tests capture, and an assertion like Contains(out,
	// "rule_upgrade") would pass off the dump rather than off the output.
	rootCmd := &cobra.Command{Use: "qctl", SilenceUsage: true, SilenceErrors: true}
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format")
	rootCmd.PersistentFlags().Bool("no-headers", false, "Don't print column headers")
	rootCmd.PersistentFlags().String("columns", "", "Columns to display")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "Maximum width for table columns")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "Show secret fields")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "Allow http://")
	rootCmd.PersistentFlags().CountP("verbose", "v", "Verbosity level")
	// Mirrors root.go: an -o value the verb does not declare exits 2.
	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		return cmdutil.ValidateOutputFormat(cmd)
	}
	rootCmd.AddCommand(NewCommand())
	// Mirrors root.go: an Args rejection exits 2.
	cmdutil.TypeArgsErrors(rootCmd)
	return rootCmd
}

func newNotificationTestEnv(t *testing.T) *testutil.MockAPIServer {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")
	return mock
}

func runNotification(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := setupNotificationTestCommand()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(append([]string{"notification"}, args...))
	err := cmd.Execute()
	return out.String(), errOut.String(), err
}

func TestReadOneMarksItRead(t *testing.T) {
	mock := newNotificationTestEnv(t)
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/notifications/"+testNotificationUUID.String()+"/read",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.MarkReadResponseSchema{Success: true, Message: "ok"})
		})

	out, errOut, err := runNotification(t, "read", testNotificationUUID.String())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "notification/" + testNotificationUUID.String() + " marked read"
	if !strings.Contains(out, want) {
		t.Errorf("output %q, want %q", out, want)
	}
	// The confirmation is the whole output: a mutating verb that succeeded
	// says nothing on stderr, so a caller can pipe stdout without noise.
	if errOut != "" {
		t.Errorf("stderr %q, want empty", errOut)
	}
}

// Marking read must never dismiss: they are separate columns with separate
// endpoints, and collapsing them would take something out of the inbox that
// the user only glanced at.
func TestReadNeverCallsDismiss(t *testing.T) {
	mock := newNotificationTestEnv(t)
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/notifications/"+testNotificationUUID.String()+"/read",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.MarkReadResponseSchema{Success: true, Message: "ok"})
		})
	dismissed := false
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/notifications/"+testNotificationUUID.String()+"/dismiss",
		func(w http.ResponseWriter, r *http.Request) {
			dismissed = true
			testutil.RespondJSON(w, http.StatusOK, api.DismissNotificationResponseSchema{Success: true})
		})

	if _, _, err := runNotification(t, "read", testNotificationUUID.String()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dismissed {
		t.Error("read called the dismiss route; the two axes are independent")
	}
}

func TestReadAllReportsTheCount(t *testing.T) {
	mock := newNotificationTestEnv(t)
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/notifications/mark-all-read",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.MarkAllReadResponseSchema{ReadCount: 12, Message: "ok"})
		})
	// The bulk path has its own dismiss-shaped neighbours, so the independence
	// invariant is pinned here too, not only on the single-id path.
	dismissed := false
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/notifications/dismiss-all",
		func(w http.ResponseWriter, r *http.Request) {
			dismissed = true
			testutil.RespondJSON(w, http.StatusOK, api.DismissAllResponseSchema{DismissedCount: 12, Message: "ok"})
		})
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/notifications/dismiss-by-type/rule_upgrade",
		func(w http.ResponseWriter, r *http.Request) {
			dismissed = true
			testutil.RespondJSON(w, http.StatusOK, api.DismissByTypeResponseSchema{DismissedCount: 12, Message: "ok"})
		})

	out, errOut, err := runNotification(t, "read", "--all")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "marked 12 notification(s) read") {
		t.Errorf("output %q", out)
	}
	if dismissed {
		t.Error("read --all called a dismiss route; the two axes are independent")
	}
	// Same contract as the single-id path: a mutating verb that succeeded says
	// nothing on stderr.
	if errOut != "" {
		t.Errorf("stderr %q, want empty", errOut)
	}
}

// An unknown --type value is refused by notifications.ParseTypes, and
// scopeFlags passes that error straight through: it must still be exit 2 at
// the command layer, and it must cost no request -- no handler is registered,
// so any call at all fails the test.
func TestReadAllRejectsAnUnknownType(t *testing.T) {
	newNotificationTestEnv(t)
	_, _, err := runNotification(t, "read", "--all", "--type", "bogus")
	if err == nil {
		t.Fatal("an unknown --type value was accepted")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code %d, want %d", got, qerrors.ExitBadUsage)
	}
}

// The types travel as a repeatable query parameter, so the scoped bulk mark is
// ONE request -- which is what makes it atomic where dismiss cannot be.
func TestReadAllWithTypesIsOneRequest(t *testing.T) {
	mock := newNotificationTestEnv(t)
	requests := 0
	var query url.Values
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/notifications/mark-all-read",
		func(w http.ResponseWriter, r *http.Request) {
			requests++
			query = r.URL.Query()
			testutil.RespondJSON(w, http.StatusOK, api.MarkAllReadResponseSchema{ReadCount: 4, Message: "ok"})
		})

	out, _, err := runNotification(t, "read", "--all", "--type", "rule_upgrade", "--type", "validation_failed")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if requests != 1 {
		t.Errorf("sent %d requests, want 1", requests)
	}
	if got := query["notification_type"]; len(got) != 2 {
		t.Errorf("notification_type = %v, want two values", got)
	}
	if !strings.Contains(out, "rule_upgrade, validation_failed") {
		t.Errorf("the scope was not named in the output: %q", out)
	}
}

func TestReadRejectsIDAndAllTogether(t *testing.T) {
	newNotificationTestEnv(t)
	_, _, err := runNotification(t, "read", testNotificationUUID.String(), "--all")
	if err == nil {
		t.Fatal("an id and --all were both accepted")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code %d, want %d", got, qerrors.ExitBadUsage)
	}
}

func TestReadRejectsNeitherIDNorAll(t *testing.T) {
	newNotificationTestEnv(t)
	_, _, err := runNotification(t, "read")
	if err == nil {
		t.Fatal("read with no id and no --all was accepted")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code %d, want %d", got, qerrors.ExitBadUsage)
	}
}

// --type scopes the bulk form. On a single id it would silently do nothing,
// which is the failure mode the repo refuses everywhere else.
func TestReadRejectsTypeWithoutAll(t *testing.T) {
	newNotificationTestEnv(t)
	_, _, err := runNotification(t, "read", testNotificationUUID.String(), "--type", "rule_upgrade")
	if err == nil {
		t.Fatal("--type without --all was accepted")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code %d, want %d", got, qerrors.ExitBadUsage)
	}
}

// A typo must cost no request: the id is validated before Bootstrap.
func TestReadRejectsAShortIDAtExitTwo(t *testing.T) {
	newNotificationTestEnv(t)
	_, _, err := runNotification(t, "read", "0190bbbb")
	if err == nil {
		t.Fatal("a short id was accepted")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code %d, want %d", got, qerrors.ExitBadUsage)
	}
}

func TestReadSurfacesA404AsExitThree(t *testing.T) {
	mock := newNotificationTestEnv(t)
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/notifications/"+testNotificationUUID.String()+"/read",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusNotFound, map[string]any{"detail": "Notification not found"})
		})

	_, _, err := runNotification(t, "read", testNotificationUUID.String())
	if err == nil {
		t.Fatal("a 404 was swallowed")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitNotFound {
		t.Errorf("exit code %d, want %d (ExitNotFound)", got, qerrors.ExitNotFound)
	}
}

// The bulk path wraps its own error, and that wrap is a separate Unwrap link
// from the single path's. TestReadSurfacesA404AsExitThree guards runRead's
// "failed to mark notification %s read" wrap in read.go; this guards its
// "failed to mark notifications read" wrap, which was reachable with no
// exit-code assertion on it at all -- a %v there keeps the message identical
// and floors the code at 1.
func TestReadAllSurfacesA404AsExitThree(t *testing.T) {
	mock := newNotificationTestEnv(t)
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/notifications/mark-all-read",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusNotFound, map[string]any{"detail": "not found"})
		})

	_, _, err := runNotification(t, "read", "--all")
	if err == nil {
		t.Fatal("a 404 on the bulk path was swallowed")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitNotFound {
		t.Errorf("exit code %d, want %d (ExitNotFound) -- read.go's bulk wrap severed the Unwrap chain", got, qerrors.ExitNotFound)
	}
}
