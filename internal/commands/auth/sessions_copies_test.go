package auth

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	qauth "github.com/qlustered/qctl/internal/auth"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

// keptKeyringCopyLine is the start of what revoking the file copy's session
// says about the keyring copy it left alone.
const keptKeyringCopyLine = `The credential of the other CLI session "` + keyringCopyName +
	`", found in the OS keyring, was kept`

// editKeyringCopy rewrites the keyring copy's stored credential.
func (c twoCopies) editKeyringCopy(t *testing.T, edit func(*qauth.Credential)) {
	t.Helper()
	key := qauth.MakeCredentialKey(c.endpointKey, logoutOrgID)
	var cred qauth.Credential
	if err := json.Unmarshal([]byte(c.keyring[key]), &cred); err != nil {
		t.Fatalf("unmarshal keyring copy: %v", err)
	}
	edit(&cred)
	credJSON, err := json.Marshal(cred)
	if err != nil {
		t.Fatalf("marshal keyring copy: %v", err)
	}
	c.keyring[key] = string(credJSON)
}

// The defect: revoking this machine's session deleted both stored copies,
// stranding the other copy's session live with no local handle on it.
func TestSessionsRevoke_CurrentOfTwoCopiesKeepsTheOther(t *testing.T) {
	env, mock := newTwoCopyLogout(t)
	server := newTwoSessionServer(t, mock)
	copies := seedTwoCopies(t, env, mock.Server.URL, false, testutil.KeyringStub{})

	stdout, stderr, err := runSessionsCommand(t, "revoke", fileCopyName)
	if err != nil {
		t.Fatalf("Execute() error = %v; stdout: %q stderr: %q", err, stdout, stderr)
	}
	if server.live[fileCopyID] {
		t.Error("this machine's session was not revoked")
	}
	if !server.live[keyringCopyID] {
		t.Error("the other session was revoked, but it was not named")
	}
	copies.wantHeld(t, true, false)
	for _, want := range []string{
		`Revoked CLI session "` + fileCopyName + `".`,
		"its stored credential was removed too.",
		keptKeyringCopyLine + ", and later commands will use it.",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout = %q, want it to contain %q", stdout, want)
		}
	}
	if strings.Contains(stdout, "qctl auth login") {
		t.Errorf("stdout = %q, must not ask for a login: a usable credential is still stored", stdout)
	}

	// "Later commands will use it" is a claim about the next command.
	stdout, stderr, err = runSessionsCommand(t, "-o", "json")
	if err != nil {
		t.Fatalf("list after revoke: Execute() error = %v; stdout: %q stderr: %q", err, stdout, stderr)
	}
	var rows []sessionView
	if err := json.Unmarshal([]byte(stdout), &rows); err != nil {
		t.Fatalf("unmarshal list: %v; stdout: %q", err, stdout)
	}
	if len(rows) != 1 || rows[0].Name != keyringCopyName || !rows[0].Current {
		t.Errorf("list after revoke = %+v, want only %q, marked current", rows, keyringCopyName)
	}
}

// Under the opt-in, ordinary reads skip the keyring, so the kept keyring copy
// is not what the next command uses.
func TestSessionsRevoke_CurrentOfTwoCopiesUnderTheOptInAsksForALogin(t *testing.T) {
	env, mock := newTwoCopyLogout(t)
	server := newTwoSessionServer(t, mock)
	copies := seedTwoCopies(t, env, mock.Server.URL, true, testutil.KeyringStub{})

	stdout, stderr, err := runSessionsCommand(t, "revoke", fileCopyName)
	if err != nil {
		t.Fatalf("Execute() error = %v; stdout: %q stderr: %q", err, stdout, stderr)
	}
	if !server.live[keyringCopyID] {
		t.Error("the other session was revoked, but it was not named")
	}
	copies.wantHeld(t, true, false)
	want := keptKeyringCopyLine + ". Run 'qctl auth login' before the next authenticated command."
	if !strings.Contains(stdout, want) {
		t.Errorf("stdout = %q, want it to contain %q", stdout, want)
	}
}

func TestSessionsRevoke_CurrentOfTwoCopiesWithAnExpiredOtherAsksForALogin(t *testing.T) {
	env, mock := newTwoCopyLogout(t)
	newTwoSessionServer(t, mock)
	copies := seedTwoCopies(t, env, mock.Server.URL, false, testutil.KeyringStub{})
	copies.editKeyringCopy(t, func(c *qauth.Credential) { c.ExpiresAt = time.Now().Add(-time.Minute) })

	stdout, stderr, err := runSessionsCommand(t, "revoke", fileCopyName)
	if err != nil {
		t.Fatalf("Execute() error = %v; stdout: %q stderr: %q", err, stdout, stderr)
	}
	copies.wantHeld(t, true, false)
	want := keptKeyringCopyLine + ". Run 'qctl auth login' before the next authenticated command."
	if !strings.Contains(stdout, want) {
		t.Errorf("stdout = %q, want it to contain %q", stdout, want)
	}
}

// A copy whose session was revoked but which stays in its half is named with
// the by-hand remedy, and the other copy is still kept.
func TestSessionsRevoke_CurrentCopyThatCouldNotBeRemovedIsNamed(t *testing.T) {
	env, mock := newTwoCopyLogout(t)
	server := newTwoSessionServer(t, mock)
	copies := seedTwoCopies(t, env, mock.Server.URL, false, testutil.KeyringStub{
		Delete: func(_, _ string) error { return errors.New("keyring locked") },
	})
	// Newer than the file copy, so the keyring copy is this machine's session.
	copies.editKeyringCopy(t, func(c *qauth.Credential) { c.CreatedAt = time.Now().Add(time.Minute) })

	stdout, _, err := runSessionsCommand(t, "revoke", keyringCopyName)
	if err == nil {
		t.Fatalf("Execute() = nil, want an error; stdout: %q", stdout)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitGenericError {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitGenericError)
	}
	if server.live[keyringCopyID] {
		t.Error("this machine's session was not revoked")
	}
	copies.wantHeld(t, true, true)
	credKey := qauth.MakeCredentialKey(copies.endpointKey, logoutOrgID)
	for _, want := range []string{
		"revoked this CLI session on the server, but failed to remove the stored credential from the OS keyring",
		credKey,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err.Error(), want)
		}
	}
}

// A kept credential stored before qctl minted names is named by where it is.
func TestSessionsRevoke_KeptUnnamedCopyIsNamedByLocation(t *testing.T) {
	env, mock := newTwoCopyLogout(t)
	newTwoSessionServer(t, mock)
	copies := seedTwoCopies(t, env, mock.Server.URL, false, testutil.KeyringStub{})
	copies.editKeyringCopy(t, func(c *qauth.Credential) { c.TokenName = "" })

	stdout, stderr, err := runSessionsCommand(t, "revoke", fileCopyName)
	if err != nil {
		t.Fatalf("Execute() error = %v; stdout: %q stderr: %q", err, stdout, stderr)
	}
	copies.wantHeld(t, true, false)
	want := "The other stored credential, found in the OS keyring, was kept, and later commands will use it."
	if !strings.Contains(stdout, want) {
		t.Errorf("stdout = %q, want it to contain %q", stdout, want)
	}
}

// When the store cannot be read again after the revoke, which copy belonged
// to the revoked session is unknown, so nothing is deleted.
func TestSessionsRevoke_UnreadableStoreAfterTheRevokeDeletesNothing(t *testing.T) {
	env, mock := newTwoCopyLogout(t)
	newTwoSessionServer(t, mock)
	var copies twoCopies
	reads := 0
	copies = seedTwoCopies(t, env, mock.Server.URL, false, testutil.KeyringStub{
		Get: func(_, user string) (string, error) {
			reads++
			if reads > 1 {
				return "not json", nil
			}
			return copies.keyring[user], nil
		},
	})

	stdout, _, err := runSessionsCommand(t, "revoke", fileCopyName)
	if err == nil {
		t.Fatalf("Execute() = nil, want an error; stdout: %q", stdout)
	}
	if reads < 2 {
		t.Fatalf("keyring read %d time(s); the store was never re-read after the revoke", reads)
	}
	copies.wantHeld(t, true, true)
	if want := "revoked this CLI session on the server, but could not read the stored credential"; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to contain %q", err.Error(), want)
	}
}
