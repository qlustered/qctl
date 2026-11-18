package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/api"
	qauth "github.com/qlustered/qctl/internal/auth"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

// The two logins a keyring login followed by a login with
// --allow-plaintext-token-store leave behind: two tokens, two server sessions.
const (
	keyringCopyToken = "keyring-token"
	keyringCopyName  = "qctl@box-keyring"
	keyringCopyID    = "11111111-2222-3333-4444-555555555555"

	fileCopyToken = "file-token"
	fileCopyName  = "qctl@box-file"
	fileCopyID    = "66666666-7777-8888-9999-000000000000"
)

// otherRevokedLine is what logout prints when it ended the keyring copy's
// session in a run where the file copy is the current one.
const otherRevokedLine = `Revoked the other CLI session "` + keyringCopyName +
	`" on the server, whose credential was found in the OS keyring.`

const successLine = "Successfully logged out from organization " + logoutOrgID

// twoSessionServer answers the delegation routes for the two sessions. Like
// the server, it lists every live delegation to either token, and refuses a
// token whose own delegation has been revoked.
type twoSessionServer struct {
	live map[string]bool // delegation ID -> not yet revoked
	// failDelete answers a DELETE of that delegation with this status.
	failDelete map[string]int
	// revokedWith records the bearer each successful DELETE carried.
	revokedWith map[string]string
}

func newTwoSessionServer(t *testing.T, mock *testutil.MockAPIServer) *twoSessionServer {
	t.Helper()
	s := &twoSessionServer{
		live:        map[string]bool{keyringCopyID: true, fileCopyID: true},
		failDelete:  map[string]int{},
		revokedWith: map[string]string{},
	}
	delegationOf := map[string]string{
		"Bearer " + keyringCopyToken: keyringCopyID,
		"Bearer " + fileCopyToken:    fileCopyID,
	}
	names := map[string]string{keyringCopyID: keyringCopyName, fileCopyID: fileCopyName}

	mock.RegisterHandler("GET", delegationsPath(), func(w http.ResponseWriter, r *http.Request) {
		if !s.live[delegationOf[r.Header.Get("Authorization")]] {
			testutil.RespondError(w, http.StatusUnauthorized, "Not authenticated")
			return
		}
		var list api.DelegationListSchema
		for _, id := range []string{keyringCopyID, fileCopyID} {
			if s.live[id] {
				list.Delegations = append(list.Delegations,
					api.DelegationSchema{ID: id, Name: strPtrLogout(names[id]), OrganizationID: logoutOrgID})
			}
		}
		testutil.RespondJSON(w, http.StatusOK, list)
	})
	for _, id := range []string{keyringCopyID, fileCopyID} {
		mock.RegisterHandler("DELETE", delegationsPath()+"/"+id, func(w http.ResponseWriter, r *http.Request) {
			if status := s.failDelete[id]; status != 0 {
				testutil.RespondError(w, status, "revoke failed")
				return
			}
			s.live[id] = false
			s.revokedWith[id] = r.Header.Get("Authorization")
			testutil.RespondJSON(w, http.StatusOK, api.RevocationResponseSchema{Success: true, Message: "revoked"})
		})
	}
	return s
}

// twoCopies is a store holding both logins: the keyring copy an hour older
// than the file copy, so the file copy is the one Retrieve returns.
type twoCopies struct {
	endpointKey string
	keyring     map[string]string
}

// seedTwoCopies runs in keyring mode unless plaintext is true, and stubs the
// keyring with keyringOps (its Entries are replaced).
func seedTwoCopies(t *testing.T, env *testutil.TestEnv, server string, plaintext bool, keyringOps testutil.KeyringStub) twoCopies {
	t.Helper()
	if !plaintext {
		// After NewTestEnv, which forces the opt-in on.
		t.Cleanup(env.SetEnv("QCTL_ALLOW_PLAINTEXT_TOKENS", ""))
	}
	endpointKey, err := config.NormalizeEndpointKey(server)
	if err != nil {
		t.Fatalf("NormalizeEndpointKey() error = %v", err)
	}
	credJSON, err := json.Marshal(qauth.Credential{
		CreatedAt:      time.Now().Add(-time.Hour),
		AccessToken:    keyringCopyToken,
		ExpiresAt:      time.Now().Add(time.Hour),
		OrganizationID: logoutOrgID,
		TokenName:      keyringCopyName,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	keyringOps.Entries = map[string]string{qauth.MakeCredentialKey(endpointKey, logoutOrgID): string(credJSON)}
	testutil.StubKeyring(t, keyringOps)
	env.SetupCredentialNamed(endpointKey, logoutOrgID, fileCopyToken, fileCopyName)
	return twoCopies{endpointKey: endpointKey, keyring: keyringOps.Entries}
}

func (c twoCopies) keyringHeld() bool {
	_, ok := c.keyring[qauth.MakeCredentialKey(c.endpointKey, logoutOrgID)]
	return ok
}

func (c twoCopies) fileHeld(t *testing.T) bool {
	t.Helper()
	orgs, err := newTestStore().StoredOrganizations(c.endpointKey)
	if err != nil {
		t.Fatalf("StoredOrganizations() error = %v", err)
	}
	return slices.Contains(orgs, logoutOrgID)
}

// wantHeld asserts which halves still hold a credential after logout.
func (c twoCopies) wantHeld(t *testing.T, keyring, file bool) {
	t.Helper()
	if got := c.keyringHeld(); got != keyring {
		t.Errorf("keyring copy held = %v, want %v", got, keyring)
	}
	if got := c.fileHeld(t); got != file {
		t.Errorf("file copy held = %v, want %v", got, file)
	}
}

func newTwoCopyLogout(t *testing.T) (*testutil.TestEnv, *testutil.MockAPIServer) {
	t.Helper()
	env := testutil.NewTestEnv(t)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	setupLogoutConfig(t, mock.Server.URL)
	return env, mock
}

// The defect itself: logout revoked only the copy Retrieve picked and then
// deleted both, stranding the other session live with no local handle on it.
func TestLogout_TwoCopiesRevokesEachSessionWithItsOwnToken(t *testing.T) {
	env, mock := newTwoCopyLogout(t)
	server := newTwoSessionServer(t, mock)
	copies := seedTwoCopies(t, env, mock.Server.URL, false, testutil.KeyringStub{})

	stdout, stderr, err := runLogoutCmd(t)
	if err != nil {
		t.Fatalf("Execute() error = %v; stdout: %q stderr: %q", err, stdout, stderr)
	}
	want := map[string]string{keyringCopyID: "Bearer " + keyringCopyToken, fileCopyID: "Bearer " + fileCopyToken}
	for id, bearer := range want {
		if got := server.revokedWith[id]; got != bearer {
			t.Errorf("delegation %s revoked with %q, want %q", id, got, bearer)
		}
	}
	copies.wantHeld(t, false, false)
	for _, line := range []string{revokedLine, otherRevokedLine, successLine} {
		if !strings.Contains(stdout, line) {
			t.Errorf("stdout = %q, want it to contain %q", stdout, line)
		}
	}
	wantNoWarning(t, stderr)
}

// A copy whose revoke failed is the only handle on a live session, so it is
// kept while the copy whose session ended goes -- and a plain retry then
// finds only the kept one.
func TestLogout_TwoCopiesKeepsOnlyTheCopyWhoseRevokeFailed(t *testing.T) {
	env, mock := newTwoCopyLogout(t)
	server := newTwoSessionServer(t, mock)
	copies := seedTwoCopies(t, env, mock.Server.URL, false, testutil.KeyringStub{})
	server.failDelete[keyringCopyID] = http.StatusInternalServerError

	stdout, _, err := runLogoutCmd(t)
	if err == nil {
		t.Fatalf("Execute() = nil, want an error -- the other session is still live; stdout: %q", stdout)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitGenericError {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitGenericError)
	}
	copies.wantHeld(t, true, false)
	if !strings.Contains(stdout, revokedLine) {
		t.Errorf("stdout = %q, want it to report %q", stdout, revokedLine)
	}
	if strings.Contains(stdout, "Successfully") {
		t.Errorf("stdout = %q, but a session is still live", stdout)
	}
	for _, want := range []string{keyringCopyName, "the OS keyring", "retry with 'qctl auth logout'"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err.Error(), want)
		}
	}

	server.failDelete = map[string]int{}
	stdout, stderr, err := runLogoutCmd(t)
	if err != nil {
		t.Fatalf("retry: Execute() error = %v; stdout: %q stderr: %q", err, stdout, stderr)
	}
	if got := server.revokedWith[keyringCopyID]; got != "Bearer "+keyringCopyToken {
		t.Errorf("retry revoked the keyring copy's session with %q, want its own token", got)
	}
	copies.wantHeld(t, false, false)
	if !strings.Contains(stdout, successLine) {
		t.Errorf("retry stdout = %q, want %q", stdout, successLine)
	}
}

func TestLogout_TwoCopiesCurrentRevokeFailsKeepsTheCurrentCopy(t *testing.T) {
	env, mock := newTwoCopyLogout(t)
	server := newTwoSessionServer(t, mock)
	copies := seedTwoCopies(t, env, mock.Server.URL, false, testutil.KeyringStub{})
	server.failDelete[fileCopyID] = http.StatusInternalServerError

	stdout, _, err := runLogoutCmd(t)
	if err == nil {
		t.Fatalf("Execute() = nil, want an error; stdout: %q", stdout)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitGenericError {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitGenericError)
	}
	if server.revokedWith[keyringCopyID] == "" {
		t.Error("the other session was never revoked")
	}
	copies.wantHeld(t, false, true)
	if !strings.Contains(stdout, otherRevokedLine) {
		t.Errorf("stdout = %q, want it to report %q", stdout, otherRevokedLine)
	}
	for _, unwanted := range []string{revokedLine, "Successfully"} {
		if strings.Contains(stdout, unwanted) {
			t.Errorf("stdout = %q, must not contain %q", stdout, unwanted)
		}
	}
	if !strings.Contains(err.Error(), "The stored credential was kept so you can retry.") {
		t.Errorf("error = %q, want the keep-and-retry remedy", err.Error())
	}
}

func TestLogout_TwoCopiesBothRevokesFailKeepsBoth(t *testing.T) {
	env, mock := newTwoCopyLogout(t)
	server := newTwoSessionServer(t, mock)
	copies := seedTwoCopies(t, env, mock.Server.URL, false, testutil.KeyringStub{})
	server.failDelete[keyringCopyID] = http.StatusInternalServerError
	server.failDelete[fileCopyID] = http.StatusInternalServerError

	stdout, _, err := runLogoutCmd(t)
	if err == nil {
		t.Fatalf("Execute() = nil, want an error; stdout: %q", stdout)
	}
	copies.wantHeld(t, true, true)
	for _, unwanted := range []string{"Revoked", "Successfully"} {
		if strings.Contains(stdout, unwanted) {
			t.Errorf("stdout = %q, must not contain %q", stdout, unwanted)
		}
	}
	for _, want := range []string{keyringCopyName, "could not be revoked either", "Its credential was kept too."} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err.Error(), want)
		}
	}
}

// The other copy's 403 gets the same terminal wording, and exit code, as the
// current copy's.
func TestLogout_TwoCopiesOtherForbiddenIsTerminal(t *testing.T) {
	env, mock := newTwoCopyLogout(t)
	server := newTwoSessionServer(t, mock)
	copies := seedTwoCopies(t, env, mock.Server.URL, false, testutil.KeyringStub{})
	server.failDelete[keyringCopyID] = http.StatusForbidden

	_, _, err := runLogoutCmd(t)
	if err == nil {
		t.Fatal("Execute() = nil, want an error from a 403 revoke")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitUnauthorized {
		t.Errorf("exit code = %d, want %d (unauthorized)", got, qerrors.ExitUnauthorized)
	}
	copies.wantHeld(t, true, false)
	if !strings.Contains(err.Error(), "Retrying will not help") {
		t.Errorf("error = %q, want the terminal wording", err.Error())
	}
	if strings.Contains(err.Error(), "so you can retry") {
		t.Errorf("error = %q, must not suggest a retry: a 403 answers the same way every time", err.Error())
	}
}

// Deleting the ended copy from its one half can fail too; that copy is named,
// and the kept copy's error still leads.
func TestLogout_TwoCopiesNamesAnEndedCopyThatCouldNotBeRemoved(t *testing.T) {
	env, mock := newTwoCopyLogout(t)
	server := newTwoSessionServer(t, mock)
	copies := seedTwoCopies(t, env, mock.Server.URL, false, testutil.KeyringStub{
		Delete: func(_, _ string) error { return errors.New("keyring locked") },
	})
	server.failDelete[fileCopyID] = http.StatusInternalServerError

	stdout, _, err := runLogoutCmd(t)
	if err == nil {
		t.Fatalf("Execute() = nil, want an error; stdout: %q", stdout)
	}
	copies.wantHeld(t, true, true)
	if !strings.Contains(stdout, otherRevokedLine) {
		t.Errorf("stdout = %q, want it to report %q", stdout, otherRevokedLine)
	}
	credKey := qauth.MakeCredentialKey(copies.endpointKey, logoutOrgID)
	for _, want := range []string{
		"The stored credential was kept so you can retry.",
		"could not remove the other credential from the OS keyring",
		credKey,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err.Error(), want)
		}
	}
}

func TestLogout_LocalWithTwoCopiesWarnsAboutBoth(t *testing.T) {
	env, mock := newTwoCopyLogout(t)
	copies := seedTwoCopies(t, env, mock.Server.URL, false, testutil.KeyringStub{})
	failIfDelegationsCalled(t, mock, "--local logout")

	stdout, stderr, err := runLogoutCmd(t, "--local")
	if err != nil {
		t.Fatalf("Execute() error = %v; stdout: %q stderr: %q", err, stdout, stderr)
	}
	copies.wantHeld(t, false, false)
	for _, want := range []string{
		"Warning: the access token was NOT revoked",
		"Warning: the other access token, found in the OS keyring, was NOT revoked",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr = %q, want it to contain %q", stderr, want)
		}
	}
	if !strings.Contains(stdout, successLine) {
		t.Errorf("stdout = %q, want %q", stdout, successLine)
	}
}

// Under the opt-in the keyring is still read, because Delete still removes a
// keyring entry there -- and removing one nobody revoked is the orphan.
func TestLogout_PlaintextOptInRevokesAKeyringOnlyCredential(t *testing.T) {
	env, mock := newTwoCopyLogout(t)
	server := newTwoSessionServer(t, mock)
	copies := seedTwoCopies(t, env, mock.Server.URL, true, testutil.KeyringStub{})
	if err := newTestStore().DeleteFileCopy(copies.endpointKey, logoutOrgID); err != nil {
		t.Fatalf("DeleteFileCopy() error = %v", err)
	}

	stdout, stderr, err := runLogoutCmd(t)
	if err != nil {
		t.Fatalf("Execute() error = %v; stdout: %q stderr: %q", err, stdout, stderr)
	}
	if got := server.revokedWith[keyringCopyID]; got != "Bearer "+keyringCopyToken {
		t.Errorf("keyring copy's session revoked with %q, want its own token", got)
	}
	copies.wantHeld(t, false, false)
	for _, line := range []string{revokedLine, successLine} {
		if !strings.Contains(stdout, line) {
			t.Errorf("stdout = %q, want it to contain %q", stdout, line)
		}
	}
	wantNoWarning(t, stderr)
}
