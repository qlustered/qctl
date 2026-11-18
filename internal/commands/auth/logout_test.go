package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/api"
	qauth "github.com/qlustered/qctl/internal/auth"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const (
	logoutOrgID   = "b2c3d4e5-f6a7-8901-bcde-f23456789012"
	logoutOrgName = "Acme Corporation"
)

// testCredKey is the entry a keyring remedy must name: one server+org, not
// the whole "qctl" service.
var testCredKey = qauth.MakeCredentialKey("api.example.com", logoutOrgID)

func setupLogoutTestCommand() *cobra.Command {
	rootCmd := &cobra.Command{Use: "qctl"}
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("org", "", "Organization")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "Allow http://")
	rootCmd.PersistentFlags().CountP("verbose", "v", "Verbosity level")

	authCmd := &cobra.Command{Use: "auth", Short: "Authentication"}
	authCmd.AddCommand(NewLogoutCommand())
	rootCmd.AddCommand(authCmd)
	return rootCmd
}

func setupLogoutConfig(t *testing.T, server string) {
	t.Helper()
	cfg := &config.Config{
		APIVersion:     config.APIVersion,
		CurrentContext: "default",
		Contexts: map[string]*config.Context{
			"default": {
				Server:           server,
				Organization:     logoutOrgID,
				OrganizationName: logoutOrgName,
				Organizations:    []config.OrganizationRef{{ID: logoutOrgID, Name: logoutOrgName}},
			},
		},
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("save config: %v", err)
	}
}

// runLogoutCmd runs `qctl auth logout` and returns stdout and stderr
// SEPARATELY. The split is the point: the only thing distinguishing several
// rows of this command's contract is whether a warning was written to stderr,
// so a single merged buffer would make those rows indistinguishable.
func runLogoutCmd(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := setupLogoutTestCommand()
	cmd.SetArgs(append([]string{"auth", "logout"}, args...))
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	err = cmd.Execute()
	return outBuf.String(), errBuf.String(), err
}

// revokedLine is the stdout line qctl prints only when it actually ended the
// session on the server.
const revokedLine = "Revoked this CLI session on the server."

// wantNoWarning asserts stderr is empty: the session really did end, so there
// is no live token to warn anyone about.
func wantNoWarning(t *testing.T, stderr string) {
	t.Helper()
	if stderr != "" {
		t.Errorf("stderr = %q, want empty -- the session ended, so there is nothing left to warn about", stderr)
	}
}

// wantNotRevoked asserts qctl did not claim a server-side revocation it did
// not perform.
func wantNotRevoked(t *testing.T, stdout string) {
	t.Helper()
	if strings.Contains(stdout, revokedLine) {
		t.Errorf("stdout claims %q, but nothing was revoked; stdout: %q", revokedLine, stdout)
	}
}

// storedExpiry reads the expiry the stderr warning must name. Call it BEFORE
// logout, which deletes the credential.
func storedExpiry(t *testing.T, endpointKey string) string {
	t.Helper()
	cred, err := newTestStore().Retrieve(endpointKey, logoutOrgID)
	if err != nil {
		t.Fatalf("retrieve credential: %v", err)
	}
	return cred.ExpiresAt.Format(time.RFC3339)
}

// credentialGone reports whether the store has no credential left for the org
// these tests log out of.
func credentialGone(t *testing.T, endpointKey string) bool {
	t.Helper()
	cred, err := newTestStore().Retrieve(endpointKey, logoutOrgID)
	return err != nil || cred == nil
}

// The documented footgun this task retires: logout was the only command that
// keyed the store on the raw --org string, so an org NAME deleted nothing and
// still reported success.
func TestLogout_ResolvesOrgNameToUUID(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	setupLogoutConfig(t, mock.Server.URL)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupCredentialNamed(endpointKey, logoutOrgID, "test-token", "")

	stdout, stderr, err := runLogoutCmd(t, "--org", logoutOrgName, "--local")
	if err != nil {
		t.Fatalf("Execute() error = %v; stdout: %q stderr: %q", err, stdout, stderr)
	}
	if !credentialGone(t, endpointKey) {
		t.Error("credential survived a logout addressed by org name")
	}
	if !strings.Contains(stdout, "Successfully logged out from organization "+logoutOrgID) {
		t.Errorf("stdout = %q, want the success line naming the resolved UUID", stdout)
	}
}

func TestLogout_LocalFlagSkipsTheServer(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	setupLogoutConfig(t, mock.Server.URL)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupCredentialNamed(endpointKey, logoutOrgID, "test-token", logoutTokenName)

	failIfDelegationsCalled(t, mock, "--local logout")
	expiry := storedExpiry(t, endpointKey)

	stdout, stderr, err := runLogoutCmd(t, "--local")
	if err != nil {
		t.Fatalf("Execute() error = %v; stdout: %q stderr: %q", err, stdout, stderr)
	}
	if !credentialGone(t, endpointKey) {
		t.Error("credential survived --local logout")
	}
	wantNotRevoked(t, stdout)

	// --local forgets a token that is still live on the server. Saying so is
	// the whole point of the flag's warning.
	for _, want := range []string{"NOT revoked", "stays valid until " + expiry, "--local"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr = %q, want it to contain %q", stderr, want)
		}
	}
}

func TestLogout_NoStoredCredentialIsNotAnError(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	setupLogoutConfig(t, mock.Server.URL)

	stdout, stderr, err := runLogoutCmd(t)
	if err != nil {
		t.Fatalf("Execute() error = %v; stdout: %q stderr: %q", err, stdout, stderr)
	}
	if !strings.Contains(stdout, "nothing to do") {
		t.Errorf("stdout = %q, want it to say there was nothing to do", stdout)
	}
	wantNotRevoked(t, stdout)
	wantNoWarning(t, stderr)
}

func newTestStore() *qauth.CredentialStore {
	return qauth.NewCredentialStore(true)
}

const logoutTokenName = "qctl@box-a1b2c3d4"

func delegationsPath() string { return "/api/orgs/" + logoutOrgID + "/users/me/delegations" }

// failIfDelegationsCalled makes "qctl must not contact the server" an explicit
// assertion. Leaving the path unregistered is not enough on its own: the mock
// transport synthesises a 404 for an unknown path rather than failing the
// request, so the case would only fail by accident.
func failIfDelegationsCalled(t *testing.T, mock *testutil.MockAPIServer, which string) {
	t.Helper()
	mock.RegisterHandler("GET", delegationsPath(), func(w http.ResponseWriter, _ *http.Request) {
		t.Errorf("%s contacted the server", which)
		testutil.RespondJSON(w, http.StatusOK, api.DelegationListSchema{})
	})
}

func TestLogout_RevokesTheMatchingDelegation(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	setupLogoutConfig(t, mock.Server.URL)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupCredentialNamed(endpointKey, logoutOrgID, "test-token", logoutTokenName)

	const mine, theirs = "33333333-4444-5555-6666-777777777777", "99999999-9999-9999-9999-999999999999"
	otherName := "qctl@laptop-deadbeef"

	mock.RegisterHandler("GET", delegationsPath(), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.DelegationListSchema{
			Delegations: []api.DelegationSchema{
				{ID: theirs, Name: &otherName, OrganizationID: logoutOrgID},
				{ID: mine, Name: strPtrLogout(logoutTokenName), OrganizationID: logoutOrgID},
			},
		})
	})

	var revoked []string
	for _, id := range []string{mine, theirs} {
		id := id
		mock.RegisterHandler("DELETE", delegationsPath()+"/"+id, func(w http.ResponseWriter, _ *http.Request) {
			revoked = append(revoked, id)
			testutil.RespondJSON(w, http.StatusOK, api.RevocationResponseSchema{Success: true, Message: "revoked"})
		})
	}

	stdout, stderr, err := runLogoutCmd(t)
	if err != nil {
		t.Fatalf("Execute() error = %v; stdout: %q stderr: %q", err, stdout, stderr)
	}
	if len(revoked) != 1 || revoked[0] != mine {
		t.Errorf("revoked = %v, want exactly [%s] -- another machine's session must be left alone", revoked, mine)
	}
	if !credentialGone(t, endpointKey) {
		t.Error("credential survived a successful logout")
	}
	if !strings.Contains(stdout, revokedLine) {
		t.Errorf("stdout = %q, want it to report %q", stdout, revokedLine)
	}
	// The session is genuinely over, so there is no live-token warning.
	wantNoWarning(t, stderr)
}

func TestLogout_UnnamedCredentialSkipsTheServerAndWarns(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	setupLogoutConfig(t, mock.Server.URL)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupCredentialNamed(endpointKey, logoutOrgID, "test-token", "")

	failIfDelegationsCalled(t, mock, "logout of an unnamed credential")
	expiry := storedExpiry(t, endpointKey)

	stdout, stderr, err := runLogoutCmd(t)
	if err != nil {
		t.Fatalf("Execute() error = %v; stdout: %q stderr: %q", err, stdout, stderr)
	}
	if !credentialGone(t, endpointKey) {
		t.Error("credential survived logout of an unnamed session")
	}
	wantNotRevoked(t, stdout)

	// This is the security-relevant half of the feature: qctl could not end the
	// session, so it must say the token is still live, until when, and how to
	// get a revocable one.
	for _, want := range []string{"NOT revoked", "stays valid until " + expiry, "qctl auth login"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr = %q, want it to contain %q", stderr, want)
		}
	}
}

func TestLogout_401DeletesLocallyAndSucceeds(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	setupLogoutConfig(t, mock.Server.URL)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupCredentialNamed(endpointKey, logoutOrgID, "test-token", logoutTokenName)

	mock.RegisterHandler("GET", delegationsPath(), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondError(w, http.StatusUnauthorized, "Not authenticated")
	})

	stdout, stderr, err := runLogoutCmd(t)
	if err != nil {
		t.Fatalf("a rejected token is a completed logout, got error = %v; stdout: %q stderr: %q", err, stdout, stderr)
	}
	if !credentialGone(t, endpointKey) {
		t.Error("credential survived a 401 logout")
	}
	// The server already refuses the token, so nothing was revoked here and
	// nothing is left valid to warn about.
	wantNotRevoked(t, stdout)
	wantNoWarning(t, stderr)
}

// The one case where the credential must NOT be deleted: it is the only thing
// that can authenticate the retry.
func TestLogout_ServerErrorKeepsTheCredential(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	setupLogoutConfig(t, mock.Server.URL)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupCredentialNamed(endpointKey, logoutOrgID, "test-token", logoutTokenName)

	mock.RegisterHandler("GET", delegationsPath(), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondError(w, http.StatusInternalServerError, "boom")
	})

	stdout, stderr, err := runLogoutCmd(t)
	if err == nil {
		t.Fatalf("expected an error from a failing revoke; stdout: %q stderr: %q", stdout, stderr)
	}
	if got := qerrors.GetExitCode(err); got != 1 {
		t.Errorf("exit code = %d, want 1", got)
	}
	if credentialGone(t, endpointKey) {
		t.Error("credential was deleted after a failed revoke -- the retry can no longer authenticate")
	}
	if !strings.Contains(err.Error(), "--local") {
		t.Errorf("error should name the --local escape hatch, got %q", err.Error())
	}
	if strings.Contains(stdout, "Successfully logged out") {
		t.Errorf("stdout = %q, but the logout did not succeed", stdout)
	}
	// stderr carries the error cobra printed, and must NOT also carry the
	// "forgotten but still valid" note -- the credential was kept, not forgotten.
	if strings.Contains(stderr, "stays valid until") {
		t.Errorf("stderr = %q, but nothing was forgotten to warn about", stderr)
	}
}

func TestLogout_ExpiredCredentialSkipsTheServer(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	setupLogoutConfig(t, mock.Server.URL)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)

	store := newTestStore()
	if _, err := store.Store(endpointKey, &qauth.Credential{
		CreatedAt:      time.Now().Add(-48 * time.Hour),
		AccessToken:    "stale",
		ExpiresAt:      time.Now().Add(-time.Hour),
		OrganizationID: logoutOrgID,
		TokenName:      logoutTokenName,
	}); err != nil {
		t.Fatalf("store credential: %v", err)
	}

	failIfDelegationsCalled(t, mock, "logout of an expired credential")

	stdout, stderr, err := runLogoutCmd(t)
	if err != nil {
		t.Fatalf("Execute() error = %v; stdout: %q stderr: %q", err, stdout, stderr)
	}
	if !credentialGone(t, endpointKey) {
		t.Error("expired credential survived logout")
	}
	wantNotRevoked(t, stdout)

	// The contrast with the unnamed case above is the only thing separating
	// these two rows of the contract: expiry already ended the session, so
	// warning that the token "stays valid" would be a lie.
	wantNoWarning(t, stderr)
}

func TestLogout_ExpiredCredentialWithLocalFlagStillWarnsNothing(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	setupLogoutConfig(t, mock.Server.URL)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)

	store := newTestStore()
	if _, err := store.Store(endpointKey, &qauth.Credential{
		CreatedAt:      time.Now().Add(-48 * time.Hour),
		AccessToken:    "stale",
		ExpiresAt:      time.Now().Add(-time.Hour),
		OrganizationID: logoutOrgID,
		TokenName:      logoutTokenName,
	}); err != nil {
		t.Fatalf("store credential: %v", err)
	}

	failIfDelegationsCalled(t, mock, "--local logout of an expired credential")

	stdout, stderr, err := runLogoutCmd(t, "--local")
	if err != nil {
		t.Fatalf("Execute() error = %v; stdout: %q stderr: %q", err, stdout, stderr)
	}
	if !credentialGone(t, endpointKey) {
		t.Error("expired credential survived --local logout")
	}
	wantNotRevoked(t, stdout)

	// Expiry outranks --local. --local's warning explains that a *live* token
	// was left live; on an already-expired one it would name a timestamp in the
	// past and claim the token "stays valid until" it, which is the kind of
	// untrue reassurance this command exists to stop printing.
	wantNoWarning(t, stderr)
}

func TestLogout_NoMatchingDelegationStillSucceeds(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	setupLogoutConfig(t, mock.Server.URL)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupCredentialNamed(endpointKey, logoutOrgID, "test-token", logoutTokenName)

	mock.RegisterHandler("GET", delegationsPath(), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.DelegationListSchema{Delegations: nil})
	})

	stdout, stderr, err := runLogoutCmd(t)
	if err != nil {
		t.Fatalf("Execute() error = %v; stdout: %q stderr: %q", err, stdout, stderr)
	}
	if !credentialGone(t, endpointKey) {
		t.Error("credential survived logout with no matching delegation")
	}
	wantNotRevoked(t, stdout)
	wantNoWarning(t, stderr)
}

// A credentials file that cannot be parsed is NOT "nothing stored". Collapsing
// the two would make logout skip the revoke and still print success -- exactly
// the silent failure this command exists to prevent.
func TestLogout_UnreadableCredentialsFileIsAnError(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	setupLogoutConfig(t, mock.Server.URL)
	env.CreateFile(filepath.Join(config.ConfigDirName, qauth.CredentialsFileName), "{ this is not json")

	failIfDelegationsCalled(t, mock, "logout with an unreadable credentials file")

	stdout, stderr, err := runLogoutCmd(t)
	if err == nil {
		t.Fatalf("expected an error from an unreadable credentials file; stdout: %q stderr: %q", stdout, stderr)
	}
	if got := qerrors.GetExitCode(err); got != 1 {
		t.Errorf("exit code = %d, want 1", got)
	}
	if errors.Is(err, qauth.ErrCredentialNotFound) {
		t.Error("an unparseable credentials file was reported as a missing credential")
	}
	if strings.Contains(stdout, "Successfully logged out") || strings.Contains(stdout, "nothing to do") {
		t.Errorf("stdout = %q, but the logout did not succeed", stdout)
	}
}

func strPtrLogout(s string) *string { return &s }

// TestDeleteFailedError_DoesNotAskForARetry pins the wording for the failure
// CredentialStore.Delete can now report. Tested directly rather than through
// the command: reaching this branch end-to-end needs an unwritable file, and a
// chmod-based setup silently stops working when the suite runs as root.
func TestDeleteFailedError_DoesNotAskForARetry(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cause := errors.New("failed to unmarshal credentials file")

	afterRevoke := deleteFailedError(cause, true, testCredKey)
	if !strings.Contains(afterRevoke.Error(), "revoked this CLI session on the server, but failed to delete") {
		t.Errorf("message = %q, want it to say the session WAS revoked", afterRevoke.Error())
	}
	if strings.Contains(afterRevoke.Error(), "retry") {
		t.Errorf("message = %q, must not suggest a retry -- the session is already over", afterRevoke.Error())
	}
	if !errors.Is(afterRevoke, cause) {
		t.Error("deleteFailedError must wrap its cause with %w so the exit code survives")
	}
	// Without this the whole "Remove it by hand: <path>" suffix could be
	// deleted and every other assertion here would still pass -- yet naming the
	// file is the only actionable thing the message carries, since a retry is
	// deliberately not offered.
	if !strings.Contains(afterRevoke.Error(), "credentials.json") {
		t.Errorf("message = %q, want it to name the credentials file the user has to remove by hand", afterRevoke.Error())
	}

	noRevoke := deleteFailedError(cause, false, testCredKey).Error()
	if strings.Contains(noRevoke, "revoked this CLI session on the server") {
		t.Errorf("message = %q, but nothing was revoked", noRevoke)
	}
}

// A 404 from the revoke means the delegation row is gone: nothing is left to
// revoke and no retry can change that, so the logout is finished. Note this is
// NOT the already-revoked case -- the backend answers that with 200 and
// success:false, which RevokeDelegation already reports as a finished logout.
func TestLogout_RevokeNotFoundIsACompletedLogout(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	setupLogoutConfig(t, mock.Server.URL)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupCredentialNamed(endpointKey, logoutOrgID, "test-token", logoutTokenName)

	const mine = "33333333-4444-5555-6666-777777777777"
	mock.RegisterHandler("GET", delegationsPath(), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.DelegationListSchema{
			Delegations: []api.DelegationSchema{
				{ID: mine, Name: strPtrLogout(logoutTokenName), OrganizationID: logoutOrgID},
			},
		})
	})
	mock.RegisterHandler("DELETE", delegationsPath()+"/"+mine, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondError(w, http.StatusNotFound, "Delegation not found")
	})

	stdout, stderr, err := runLogoutCmd(t)
	if err != nil {
		t.Fatalf("a 404 revoke is a completed logout, got error = %v; stdout: %q stderr: %q", err, stdout, stderr)
	}
	if !credentialGone(t, endpointKey) {
		t.Error("credential survived a 404 logout")
	}
	// qctl revoked nothing on this run, so it must not claim it did -- and
	// there is no live token left to warn about either.
	wantNotRevoked(t, stdout)
	wantNoWarning(t, stderr)
}

// A 403 from the revoke is permanent: the delegation belongs to someone else.
// The credential is KEPT, because the token is still live on the server and
// forgetting it silently is the untrue reassurance this command exists to
// avoid -- but the message must not send the user round a retry loop.
func TestLogout_RevokeForbiddenIsTerminal(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	setupLogoutConfig(t, mock.Server.URL)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupCredentialNamed(endpointKey, logoutOrgID, "test-token", logoutTokenName)

	const mine = "33333333-4444-5555-6666-777777777777"
	mock.RegisterHandler("GET", delegationsPath(), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.DelegationListSchema{
			Delegations: []api.DelegationSchema{
				{ID: mine, Name: strPtrLogout(logoutTokenName), OrganizationID: logoutOrgID},
			},
		})
	})
	mock.RegisterHandler("DELETE", delegationsPath()+"/"+mine, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondError(w, http.StatusForbidden, "You do not own this delegation")
	})

	stdout, stderr, err := runLogoutCmd(t)
	if err == nil {
		t.Fatalf("expected an error from a 403 revoke; stdout: %q stderr: %q", stdout, stderr)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitUnauthorized {
		t.Errorf("exit code = %d, want %d (unauthorized)", got, qerrors.ExitUnauthorized)
	}
	if credentialGone(t, endpointKey) {
		t.Error("credential was deleted after a 403 -- the token is still live on the server")
	}
	if strings.Contains(err.Error(), "so you can retry") {
		t.Errorf("error = %q, must not suggest a retry: a 403 answers the same way every time", err.Error())
	}
	if !strings.Contains(err.Error(), "--local") {
		t.Errorf("error = %q, should still name the --local escape hatch", err.Error())
	}
}

// The same permanence one call earlier: a 403 on the LIST is a missing
// ORG_VIEW permission, which no retry fixes either.
func TestLogout_ListForbiddenIsTerminal(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	setupLogoutConfig(t, mock.Server.URL)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupCredentialNamed(endpointKey, logoutOrgID, "test-token", logoutTokenName)

	mock.RegisterHandler("GET", delegationsPath(), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondError(w, http.StatusForbidden, "Permission denied")
	})

	stdout, stderr, err := runLogoutCmd(t)
	if err == nil {
		t.Fatalf("expected an error from a 403 list; stdout: %q stderr: %q", stdout, stderr)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitUnauthorized {
		t.Errorf("exit code = %d, want %d (unauthorized)", got, qerrors.ExitUnauthorized)
	}
	if credentialGone(t, endpointKey) {
		t.Error("credential was deleted after a 403 -- the token is still live on the server")
	}
	if strings.Contains(err.Error(), "so you can retry") {
		t.Errorf("error = %q, must not suggest a retry", err.Error())
	}
}

// The 404 arm `continue`s rather than returning, so a second delegation sharing
// the name still gets its revoke attempted. Without that, this logout would end
// on the first (gone) row and leave the second session live on the server --
// the exact thing the duplicate-name loop exists to prevent. The three tests
// above all pass with a `return` in place of the `continue`; this one does not.
func TestLogout_RevokeNotFoundStillRevokesTheNextDelegation(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	setupLogoutConfig(t, mock.Server.URL)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupCredentialNamed(endpointKey, logoutOrgID, "test-token", logoutTokenName)

	const gone = "33333333-4444-5555-6666-777777777777"
	const live = "44444444-5555-6666-7777-888888888888"
	mock.RegisterHandler("GET", delegationsPath(), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.DelegationListSchema{
			Delegations: []api.DelegationSchema{
				{ID: gone, Name: strPtrLogout(logoutTokenName), OrganizationID: logoutOrgID},
				{ID: live, Name: strPtrLogout(logoutTokenName), OrganizationID: logoutOrgID},
			},
		})
	})
	mock.RegisterHandler("DELETE", delegationsPath()+"/"+gone, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondError(w, http.StatusNotFound, "Delegation not found")
	})
	revokedLive := false
	mock.RegisterHandler("DELETE", delegationsPath()+"/"+live, func(w http.ResponseWriter, _ *http.Request) {
		revokedLive = true
		testutil.RespondJSON(w, http.StatusOK, api.RevocationResponseSchema{Success: true, Message: "revoked"})
	})

	stdout, stderr, err := runLogoutCmd(t)
	if err != nil {
		t.Fatalf("logout failed: %v; stdout: %q stderr: %q", err, stdout, stderr)
	}
	if !revokedLive {
		t.Error("the second delegation was never revoked: the 404 ended the loop early")
	}
	// The point of this test: the line appears only if the run really revoked
	// something, which needs the 404 arm to have continued to the second row.
	if !strings.Contains(stdout, revokedLine) {
		t.Errorf("stdout = %q, want it to report %q", stdout, revokedLine)
	}
	if !credentialGone(t, endpointKey) {
		t.Error("credential survived a successful logout")
	}
	wantNoWarning(t, stderr)
}

// A credential with no recorded expiry used to make the security warning read
// "stays valid until 0001-01-01T00:00:00Z" -- a nonsense date presented as a
// real deadline. IsExpired() reports such a credential as NOT expired (see
// internal/cmdutil/bootstrap.go, which relies on that), so it reaches this arm.
func TestLogout_WarningOmitsAZeroExpiry(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	setupLogoutConfig(t, mock.Server.URL)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)

	store := newTestStore()
	if _, err := store.Store(endpointKey, &qauth.Credential{
		CreatedAt:      time.Now().Add(-time.Hour),
		AccessToken:    "no-expiry",
		OrganizationID: logoutOrgID,
		TokenName:      logoutTokenName,
		// ExpiresAt deliberately left at the zero time, as a hand-edited or
		// partially-written credentials.json would have it.
	}); err != nil {
		t.Fatalf("store credential: %v", err)
	}

	failIfDelegationsCalled(t, mock, "--local logout")

	stdout, stderr, err := runLogoutCmd(t, "--local")
	if err != nil {
		t.Fatalf("Execute() error = %v; stdout: %q stderr: %q", err, stdout, stderr)
	}
	if strings.Contains(stderr, "0001-01-01") {
		t.Errorf("stderr = %q, want no zero timestamp -- it reads as a real expiry", stderr)
	}
	if !strings.Contains(stderr, "NOT revoked") {
		t.Errorf("stderr = %q, want it to still warn that the token was not revoked", stderr)
	}
	// The check above is negative, so it also passes if the timestamp is merely
	// formatted differently -- switch logout.go to time.RFC1123 and the zero
	// time renders "Mon, 01 Jan 0001 00:00:00 UTC", which no longer contains
	// "0001-01-01". This asserts the zero-expiry arm actually ran.
	if !strings.Contains(stderr, "no expiry was recorded locally") {
		t.Errorf("stderr = %q, want it to say no expiry was recorded locally", stderr)
	}
	wantNotRevoked(t, stdout)
}

// TestDescribeFailedDelete_NamesTheHalfThatSurvived is item 2 of the ticket.
// A file Retrieve cannot parse is not evidence the token is in it:
// saying "failed to delete the
// stored credential" and pointing at that file is a wrong diagnosis with a
// confident remedy attached.
func TestDescribeFailedDelete_NamesTheHalfThatSurvived(t *testing.T) {
	path, err := qauth.CredentialsFilePath()
	if err != nil {
		t.Fatalf("CredentialsFilePath() error = %v", err)
	}

	tests := []struct {
		name         string
		err          error
		revoked      bool
		wantLead     string
		wantInRemedy []string
		notInRemedy  []string
	}{
		{
			name:         "keyring cleared, file unparseable",
			err:          &qauth.DeleteError{KeyringCleared: true, Err: errors.New("failed to unmarshal credentials file")},
			revoked:      true,
			wantLead:     "revoked this CLI session on the server and found no copy of the credential left in the OS keyring, but could not clear the fallback credential store",
			wantInRemedy: []string{path, "cannot confirm"},
			// Two dropped conditions, one row. The lead must not claim an entry
			// was REMOVED: KeyringCleared is `keyringErr == nil`, which
			// deleteFromKeyring also returns when there was no entry, when the
			// platform has no keyring, and when none can be consulted -- so on
			// a headless box that wording describes a keyring that is not
			// there. The exact match on wantLead pins that. And the remedy must
			// not say "could not read" even here, where the cause happens to be
			// a read: the next row is the same arm reached by a WRITE failure,
			// and one message serves both.
			notInRemedy: []string{"could not read"},
		},
		{
			// The write half of the same arm, and the row the branch did not
			// have. deleteFromFile fails on write paths too -- os.Remove when
			// the map empties, and writeCredentialsFile -- where the read
			// succeeded, so "qctl could not read <path>" is a wrong diagnosis
			// handed to a user who is already troubleshooting.
			name:         "keyring cleared, file write failed",
			err:          &qauth.DeleteError{KeyringCleared: true, Err: errors.New("failed to write credentials file: permission denied")},
			revoked:      true,
			wantLead:     "revoked this CLI session on the server and found no copy of the credential left in the OS keyring, but could not clear the fallback credential store",
			wantInRemedy: []string{path, "could not clear"},
			notInRemedy:  []string{"could not read"},
		},
		{
			name:         "keyring survived, file cleared",
			err:          &qauth.DeleteError{FileCleared: true, Err: errors.New("still in the OS keyring")},
			revoked:      true,
			wantLead:     "revoked this CLI session on the server, but failed to remove the credential from the OS keyring",
			wantInRemedy: []string{qauth.KeyringServiceName},
			notInRemedy:  []string{path},
		},
		{
			// wantInRemedy names the ENTRY, not just KeyringServiceName:
			// the service is "qctl" and the file remedy already contains
			// "~/.qctl/credentials.json", so asserting on the service alone
			// passes even when the keyring half of the remedy is gone.
			name:         "neither half cleared",
			err:          &qauth.DeleteError{Err: errors.New("both halves failed")},
			wantLead:     "failed to delete the stored credential",
			wantInRemedy: []string{qauth.KeyringServiceName, testCredKey, path},
		},
		{
			name:         "revoked, neither half cleared",
			err:          &qauth.DeleteError{Err: errors.New("both halves failed")},
			revoked:      true,
			wantLead:     "revoked this CLI session on the server, but failed to delete the stored credential",
			wantInRemedy: []string{qauth.KeyringServiceName, testCredKey, path},
		},
		{
			// A cause that is not a DeleteError tells qctl nothing about the
			// keyring, so the remedy must not send the user there. Row 2 has
			// the mirror guard on the file path; without this one the row
			// asserts a positive only.
			name:         "not a DeleteError at all",
			err:          errors.New("something else"),
			wantLead:     "failed to delete the stored credential",
			wantInRemedy: []string{path},
			notInRemedy:  []string{"OS keyring", testCredKey},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lead, remedy := describeFailedDelete(tt.err, tt.revoked, testCredKey)
			if lead != tt.wantLead {
				t.Errorf("lead  = %q\nwant   = %q", lead, tt.wantLead)
			}
			for _, want := range tt.wantInRemedy {
				if !strings.Contains(remedy, want) {
					t.Errorf("remedy = %q, want it to mention %q", remedy, want)
				}
			}
			for _, unwanted := range tt.notInRemedy {
				if strings.Contains(remedy, unwanted) {
					t.Errorf("remedy = %q, must NOT mention %q", remedy, unwanted)
				}
			}
		})
	}
}

// TestLogoutRemedies_NameTheFileASymlinkPointsAt: deleteFromFile acts on a
// symlinked credentials.json's target, so "cannot confirm no copy of the token
// is left in it" has to be said of that target, not of a link that holds
// nothing. One row per remedy that names the file.
func TestLogoutRemedies_NameTheFileASymlinkPointsAt(t *testing.T) {
	remedyOf := func(err error) string {
		_, remedy, _ := strings.Cut(err.Error(), "\n\n")
		return remedy
	}
	tests := []struct {
		name   string
		remedy func() string
		want   string
	}{
		{
			name: "logout, keyring cleared and file not",
			remedy: func() string {
				_, remedy := describeFailedDelete(
					&qauth.DeleteError{KeyringCleared: true, Err: errors.New("failed to unmarshal credentials file")},
					true, testCredKey)
				return remedy
			},
			want: "qctl could not clear %s, so it cannot confirm",
		},
		{
			name: "logout, neither half cleared",
			remedy: func() string {
				_, remedy := describeFailedDelete(&qauth.DeleteError{Err: errors.New("both halves failed")}, true, testCredKey)
				return remedy
			},
			want: "Remove it by hand: %s",
		},
		{
			name: "logout --all, file not cleared",
			remedy: func() string {
				return remedyOf(describeFailedClearAll([]clearFailure{{
					err:     &qauth.DeleteError{KeyringCleared: true, Err: errors.New("failed to unmarshal credentials file")},
					credKey: testCredKey,
				}}))
			},
			want: "qctl could not clear %s, so it cannot confirm",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testutil.NewTestEnv(t)
			location := testutil.SymlinkCredentialsFile(t, "{not json")

			want := fmt.Sprintf(tt.want, location)
			if got := tt.remedy(); !strings.Contains(got, want) {
				t.Errorf("remedy = %q\nwant it to contain %q", got, want)
			}
		})
	}
}

// TestLogout_KeyringCopyGoneButFileUnparseable drives the whole command down
// the path item 2 describes: the credential is found in the keyring, the
// session is revoked, the keyring entry is removed, and only then does an
// unrelated corrupt credentials.json turn up.
//
// Every step of that path is asserted, not just the exit code: an exit-code
// assertion alone passes just as well if the command failed on the very first
// read, which is the wrong reason and the wrong message.
func TestLogout_KeyringCopyGoneButFileUnparseable(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	setupLogoutConfig(t, mock.Server.URL)

	// Keyring mode, not the plaintext shortcut: this is the configuration the
	// ticket is about, and the one where Retrieve never opens the file. It has
	// to come AFTER NewTestEnv, which forces the plaintext opt-in on.
	defer env.SetEnv("QCTL_ALLOW_PLAINTEXT_TOKENS", "")()

	endpointKey, err := config.NormalizeEndpointKey(mock.Server.URL)
	if err != nil {
		t.Fatalf("NormalizeEndpointKey() error = %v", err)
	}
	cred := qauth.Credential{
		CreatedAt:      time.Now(),
		AccessToken:    "test-token",
		ExpiresAt:      time.Now().Add(time.Hour),
		OrganizationID: logoutOrgID,
		TokenName:      logoutTokenName,
	}
	credJSON, err := json.Marshal(cred)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	credKey := qauth.MakeCredentialKey(endpointKey, logoutOrgID)
	entries := map[string]string{credKey: string(credJSON)}
	testutil.StubKeyring(t, testutil.KeyringStub{Entries: entries})

	const mine = "33333333-4444-5555-6666-777777777777"
	mock.RegisterHandler("GET", delegationsPath(), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.DelegationListSchema{
			Delegations: []api.DelegationSchema{
				{ID: mine, Name: strPtrLogout(logoutTokenName), OrganizationID: logoutOrgID},
			},
		})
	})
	revoked := false
	mock.RegisterHandler("DELETE", delegationsPath()+"/"+mine, func(w http.ResponseWriter, _ *http.Request) {
		revoked = true
		testutil.RespondJSON(w, http.StatusOK, api.RevocationResponseSchema{Success: true, Message: "revoked"})
	})

	// A credentials.json that cannot be parsed and never held this token.
	credPath, err := qauth.CredentialsFilePath()
	if err != nil {
		t.Fatalf("CredentialsFilePath() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(credPath), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(credPath, []byte("{not json"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	stdout, stderr, err := runLogoutCmd(t)
	if err == nil {
		t.Fatalf("Execute() = nil, want an error -- the file could not be cleared; stdout: %q stderr: %q", stdout, stderr)
	}

	// The three steps before the failure, each proved separately. Without
	// these the test could be passing on a credential that was never found.
	if !revoked {
		t.Error("the delegation was never revoked -- the command failed before it reached the server half")
	}
	if _, stillThere := entries[credKey]; stillThere {
		t.Error("the keyring entry survived, so the message's claim that it is gone would be untrue")
	}
	if got, want := string(mustReadFile(t, credPath)), "{not json"; got != want {
		t.Errorf("credentials file = %q, want %q left untouched", got, want)
	}

	msg := err.Error()
	if !strings.Contains(msg, "found no copy of the credential left in the OS keyring") {
		t.Errorf("error must say the keyring copy is gone, got: %v", msg)
	}
	// ...without claiming an entry was removed: the same boolean is set when
	// there was nothing there and when there is no keyring at all.
	if strings.Contains(msg, "removed the credential from the OS keyring") {
		t.Errorf("error asserts a removal the DeleteError half does not establish, got: %v", msg)
	}
	if !strings.Contains(msg, credPath) {
		t.Errorf("error must name the file to inspect, got: %v", msg)
	}
	if qerrors.GetExitCode(err) != qerrors.ExitGenericError {
		t.Errorf("exit code = %d, want %d", qerrors.GetExitCode(err), qerrors.ExitGenericError)
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

// TestDescribeFailedDelete_NamesTheEntryNotTheWholeService: KeyringServiceName
// is "qctl", and EVERY credential qctl has ever stored -- every server, every
// org -- is an entry under that one service, keyed "endpoint|org-id". Telling
// the user to "remove the qctl entry from your OS keyring", followed
// literally, destroys all of them. It also contradicts this command's own
// documented promise that logout touches only the current server and org.
func TestDescribeFailedDelete_NamesTheEntryNotTheWholeService(t *testing.T) {
	credKey := qauth.MakeCredentialKey("api.example.com", logoutOrgID)

	for name, err := range map[string]error{
		"keyring survived, file cleared": &qauth.DeleteError{FileCleared: true, Err: errors.New("still in the OS keyring")},
		"neither half cleared":           &qauth.DeleteError{Err: errors.New("both halves failed")},
	} {
		t.Run(name, func(t *testing.T) {
			_, remedy := describeFailedDelete(err, false, credKey)
			if !strings.Contains(remedy, credKey) {
				t.Errorf("remedy = %q, want it to name the entry %q -- the service alone holds every server's and org's credential", remedy, credKey)
			}
			if strings.Contains(remedy, `the "qctl" entry`) {
				t.Errorf("remedy = %q, must not call the service an entry: removing it deletes every other server's and org's credential", remedy)
			}
		})
	}
}
