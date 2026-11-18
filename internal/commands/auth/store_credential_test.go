package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/api"
	qauth "github.com/qlustered/qctl/internal/auth"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/zalando/go-keyring"
)

// These tests drive storeCredential directly rather than through a login. What
// they are about is which of the two credentials -- the one being replaced and
// the one replacing it -- authenticates the cleanup, and whether any call is
// made at all; a full device flow adds three more mocked endpoints without
// adding a thing to that question.

const (
	replacedToken  = "old-cli-token"
	replacementTok = "new-cli-token"
	replacedName   = "qctl@old-host-11111111"
	replacementNm  = "qctl@new-host-22222222"
	newSessionID   = "cccccccc-dddd-4eee-8fff-000000000000"
)

// delegationRecorder serves the two delegation endpoints and records the
// bearer token every call carried, which is the assertion these tests turn on.
type delegationRecorder struct {
	mu         sync.Mutex
	listAuth   []string
	deleted    []string
	deleteAuth map[string]string

	// ListStatus, when set, answers the list with the status it returns for
	// that bearer token; 0 or 200 means the normal listing.
	ListStatus func(bearer string) int
	// Revoke, when set, answers one delegation's DELETE with a status and, for
	// a 200, the success flag the body carries.
	Revoke func(id string) (int, bool)
}

func newDelegationRecorder(t *testing.T, mock *testutil.MockAPIServer, delegations ...api.DelegationSchema) *delegationRecorder {
	t.Helper()
	rec := &delegationRecorder{deleteAuth: map[string]string{}}
	path := "/api/orgs/" + loginOrgID + "/users/me/delegations"

	mock.RegisterHandler("GET", path, func(w http.ResponseWriter, r *http.Request) {
		bearer := r.Header.Get("Authorization")
		rec.mu.Lock()
		rec.listAuth = append(rec.listAuth, bearer)
		rec.mu.Unlock()
		if rec.ListStatus != nil {
			if status := rec.ListStatus(bearer); status != 0 && status != http.StatusOK {
				testutil.RespondError(w, status, "refused")
				return
			}
		}
		testutil.RespondJSON(w, http.StatusOK, api.DelegationListSchema{Delegations: delegations})
	})

	for _, d := range delegations {
		id := d.ID
		mock.RegisterHandler("DELETE", path+"/"+id, func(w http.ResponseWriter, r *http.Request) {
			rec.mu.Lock()
			rec.deleted = append(rec.deleted, id)
			rec.deleteAuth[id] = r.Header.Get("Authorization")
			rec.mu.Unlock()
			status, success := http.StatusOK, true
			if rec.Revoke != nil {
				status, success = rec.Revoke(id)
			}
			if status != http.StatusOK {
				testutil.RespondError(w, status, "refused")
				return
			}
			testutil.RespondJSON(w, http.StatusOK, api.RevocationResponseSchema{Success: success})
		})
	}
	return rec
}

func loginDelegation(id, name string) api.DelegationSchema {
	return api.DelegationSchema{
		ID:             id,
		OrganizationID: loginOrgID,
		Name:           &name,
		CreatedAt:      time.Now().Add(-time.Hour),
	}
}

// storePrevious writes the credential a later storeCredential call replaces.
// It goes through the store the case's mode reads from -- plaintext writes the
// fallback file, keyring mode the stubbed keyring -- and
// leaves QCTL_ALLOW_PLAINTEXT_TOKENS alone, which these tests set per case.
func storePrevious(t *testing.T, plaintext bool, endpointKey, name string, expiresAt time.Time) {
	t.Helper()
	if _, err := qauth.NewCredentialStore(plaintext).Store(endpointKey, &qauth.Credential{
		CreatedAt:      time.Now().Add(-time.Hour),
		AccessToken:    replacedToken,
		ExpiresAt:      expiresAt,
		OrganizationID: loginOrgID,
		TokenName:      name,
	}); err != nil {
		t.Fatalf("store previous credential: %v", err)
	}
}

func newExchangeResponse() *qauth.CLIExchangeResponse {
	return &qauth.CLIExchangeResponse{
		AccessToken:    replacementTok,
		TokenType:      "Bearer",
		ExpiresIn:      3600,
		OrganizationID: loginOrgID,
	}
}

// storeCredentialEnv prepares an isolated HOME and mock transport and returns
// the endpoint key the credential store is addressed by.
func storeCredentialEnv(t *testing.T) (*testutil.TestEnv, *testutil.MockAPIServer, string) {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	endpointKey, err := config.NormalizeEndpointKey(mock.Server.URL)
	if err != nil {
		t.Fatalf("normalize endpoint: %v", err)
	}
	return env, mock, endpointKey
}

func TestStoreCredential_PlaintextFlagWarnsAboutOlderKeyringEntry(t *testing.T) {
	_, mock, endpointKey := storeCredentialEnv(t)
	t.Setenv("QCTL_ALLOW_PLAINTEXT_TOKENS", "")
	old := &qauth.Credential{
		CreatedAt:      time.Now().Add(-time.Hour),
		AccessToken:    replacedToken,
		OrganizationID: loginOrgID,
		TokenName:      replacedName,
	}
	oldJSON, err := json.Marshal(old)
	if err != nil {
		t.Fatalf("marshal old credential: %v", err)
	}
	key := qauth.MakeCredentialKey(endpointKey, loginOrgID)
	entries := map[string]string{key: string(oldJSON)}
	testutil.StubKeyring(t, testutil.KeyringStub{Entries: entries})

	result, err := storeCredential(context.Background(), mock.Server.URL, 0,
		newExchangeResponse(), replacementNm, true)
	if err != nil {
		t.Fatalf("storeCredential() error = %v", err)
	}
	for _, want := range []string{key, "QCTL_ALLOW_PLAINTEXT_TOKENS=1", "auth sessions"} {
		if !strings.Contains(result.StorageWarning, want) {
			t.Errorf("StorageWarning = %q, want %q", result.StorageWarning, want)
		}
	}
	if strings.Contains(result.StorageWarning, replacedToken) {
		t.Error("StorageWarning includes the old access token")
	}
	if entries[key] != string(oldJSON) {
		t.Error("plaintext login modified the older keyring entry")
	}
	got, err := qauth.NewCredentialStore(false).Retrieve(endpointKey, loginOrgID)
	if err != nil {
		t.Fatalf("retrieve in a later default command: %v", err)
	}
	if got.AccessToken != replacementTok {
		t.Errorf("later default command uses %q, want new file token", got.AccessToken)
	}
}

// TestStoreCredential_ExpiredPreviousIsCleanedUpWithTheNewToken covers the
// common case, not an edge one: tokens last a day, so the next day's login
// replaces a credential that is expired locally. Its own token can no longer
// authenticate anything -- but a delegation has no expiry of its own, so the
// server goes on listing that session under `auth sessions` until something
// revokes it. The new token does, by name.
func TestStoreCredential_ExpiredPreviousIsCleanedUpWithTheNewToken(t *testing.T) {
	_, mock, endpointKey := storeCredentialEnv(t)
	storePrevious(t, true, endpointKey, replacedName, time.Now().Add(-time.Minute))
	rec := newDelegationRecorder(t, mock, loginDelegation(sessionsPreviousID, replacedName))

	result, err := storeCredential(context.Background(), mock.Server.URL, 0,
		newExchangeResponse(), replacementNm, true)
	if err != nil {
		t.Fatalf("storeCredential() error = %v", err)
	}
	if got := rec.deleteAuth[sessionsPreviousID]; got != "Bearer "+replacementTok {
		t.Errorf("revoke Authorization = %q, want the new token: the expired one cannot authenticate its own revoke", got)
	}
	if result.RevokedPrevious {
		t.Error("RevokedPrevious = true for a session whose token had already expired; no live session was ended, and login must not say one was")
	}
	if result.ReplacementWarning != "" {
		t.Errorf("ReplacementWarning = %q, want silence: an expired credential is not something the user can act on", result.ReplacementWarning)
	}
}

// TestStoreCredential_RejectedPreviousIsCleanedUpWithTheNewToken is the same
// outcome reached from the server's side: the stored credential still looks
// live locally, and the server refuses it. Falling back to the new token is
// what stops a rejected session staying listed forever.
func TestStoreCredential_RejectedPreviousIsCleanedUpWithTheNewToken(t *testing.T) {
	_, mock, endpointKey := storeCredentialEnv(t)
	storePrevious(t, true, endpointKey, replacedName, time.Now().Add(time.Hour))
	rec := newDelegationRecorder(t, mock, loginDelegation(sessionsPreviousID, replacedName))
	rec.ListStatus = func(bearer string) int {
		if bearer == "Bearer "+replacedToken {
			return http.StatusUnauthorized
		}
		return http.StatusOK
	}

	result, err := storeCredential(context.Background(), mock.Server.URL, 0,
		newExchangeResponse(), replacementNm, true)
	if err != nil {
		t.Fatalf("storeCredential() error = %v", err)
	}
	wantLists := []string{"Bearer " + replacedToken, "Bearer " + replacementTok}
	if strings.Join(rec.listAuth, ",") != strings.Join(wantLists, ",") {
		t.Errorf("list Authorization headers = %v, want %v: its own token first, then the new one after the refusal", rec.listAuth, wantLists)
	}
	if got := rec.deleteAuth[sessionsPreviousID]; got != "Bearer "+replacementTok {
		t.Errorf("revoke Authorization = %q, want the new token", got)
	}
	if result.RevokedPrevious {
		t.Error("RevokedPrevious = true for a session the server had already rejected")
	}
	if result.ReplacementWarning != "" {
		t.Errorf("ReplacementWarning = %q, want silence: there is no live session left to warn about", result.ReplacementWarning)
	}
}

// TestStoreCredential_KeepsAPreviousSessionTheFallbackFileStillHolds pins the
// one case where NOT revoking is the fix. A keyring store leaves
// credentials.json untouched, so the replaced token is still sitting there,
// and that is where plaintext-mode and keyring-less commands read from.
// Revoking it would break those readers to end a session that is not orphaned
// at all -- it still has a local handle.
func TestStoreCredential_KeepsAPreviousSessionTheFallbackFileStillHolds(t *testing.T) {
	env, mock, endpointKey := storeCredentialEnv(t)
	storePrevious(t, true, endpointKey, replacedName, time.Now().Add(time.Hour))

	// NewTestEnv forces plaintext storage; clear it so the keyring path runs.
	defer env.SetEnv("QCTL_ALLOW_PLAINTEXT_TOKENS", "")()
	testutil.StubKeyring(t, testutil.KeyringStub{})
	newDelegationRecorder(t, mock, loginDelegation(sessionsPreviousID, replacedName))

	result, err := storeCredential(context.Background(), mock.Server.URL, 0,
		newExchangeResponse(), replacementNm, false)
	if err != nil {
		t.Fatalf("storeCredential() error = %v", err)
	}
	if got := mock.Requests(); len(got) != 0 {
		t.Errorf("requests = %v, want none: the replaced session still has a local handle", got)
	}
	if result.RevokedPrevious {
		t.Error("RevokedPrevious = true, but nothing was revoked")
	}
	if result.ReplacementWarning != "" {
		t.Errorf("ReplacementWarning = %q, want silence", result.ReplacementWarning)
	}
	stored, err := qauth.NewCredentialStore(true).Retrieve(endpointKey, loginOrgID)
	if err != nil {
		t.Fatalf("fallback Retrieve() error = %v", err)
	}
	if stored.AccessToken != replacedToken {
		t.Errorf("fallback AccessToken = %q, want the still-usable replaced token", stored.AccessToken)
	}
}

// TestStoreCredential_RevokesAPreviousSessionTheKeyringOverwrites is the same
// keyring mode with the replaced credential where the new one lands. The
// overwrite leaves no local handle, so the session is orphaned unless this
// revokes it -- with its own token, which is what reaches it even when the new
// login belongs to a different user.
func TestStoreCredential_RevokesAPreviousSessionTheKeyringOverwrites(t *testing.T) {
	env, mock, endpointKey := storeCredentialEnv(t)
	defer env.SetEnv("QCTL_ALLOW_PLAINTEXT_TOKENS", "")()
	testutil.StubKeyring(t, testutil.KeyringStub{})
	storePrevious(t, false, endpointKey, replacedName, time.Now().Add(time.Hour))
	rec := newDelegationRecorder(t, mock, loginDelegation(sessionsPreviousID, replacedName))

	result, err := storeCredential(context.Background(), mock.Server.URL, 0,
		newExchangeResponse(), replacementNm, false)
	if err != nil {
		t.Fatalf("storeCredential() error = %v", err)
	}
	if got := rec.deleteAuth[sessionsPreviousID]; got != "Bearer "+replacedToken {
		t.Errorf("revoke Authorization = %q, want the replaced credential's own token", got)
	}
	if !result.RevokedPrevious {
		t.Error("RevokedPrevious = false after revoking a live session; login would not report what it ended")
	}
	if result.ReplacementWarning != "" {
		t.Errorf("ReplacementWarning = %q, want none after a clean revoke", result.ReplacementWarning)
	}
}

// TestStoreCredential_RevokesTheNewSessionWhenStoringFails covers the orphan
// this branch would otherwise create itself. The exchange has already minted a
// session; if storing its token fails, nothing qctl reads will ever present
// it, and every retry of the login would leave one more behind. Its own token
// can still revoke it, so it does -- and the session being replaced is left
// alone, because the credential that addresses it is still the stored one.
func TestStoreCredential_RevokesTheNewSessionWhenStoringFails(t *testing.T) {
	env, mock, endpointKey := storeCredentialEnv(t)
	credPath, err := qauth.CredentialsFilePath()
	if err != nil {
		t.Fatalf("credentials file path: %v", err)
	}
	// A directory where the fallback file belongs: the keyring refuses the
	// write and the file cannot take it either, so the token lands nowhere.
	if err := os.MkdirAll(credPath, 0700); err != nil {
		t.Fatalf("create the blocking directory: %v", err)
	}

	defer env.SetEnv("QCTL_ALLOW_PLAINTEXT_TOKENS", "")()
	previousJSON, err := json.Marshal(qauth.Credential{
		CreatedAt:      time.Now().Add(-time.Hour),
		AccessToken:    replacedToken,
		ExpiresAt:      time.Now().Add(time.Hour),
		OrganizationID: loginOrgID,
		TokenName:      replacedName,
	})
	if err != nil {
		t.Fatalf("marshal previous credential: %v", err)
	}
	testutil.StubKeyring(t, testutil.KeyringStub{
		Entries: map[string]string{qauth.MakeCredentialKey(endpointKey, loginOrgID): string(previousJSON)},
		Set:     func(_, _, _ string) error { return errors.New("org.freedesktop.DBus.Error.AccessDenied") },
	})
	rec := newDelegationRecorder(t, mock,
		loginDelegation(sessionsPreviousID, replacedName),
		loginDelegation(newSessionID, replacementNm))

	_, err = storeCredential(context.Background(), mock.Server.URL, 0,
		newExchangeResponse(), replacementNm, false)
	if err == nil {
		t.Fatal("storeCredential() error = nil, want the failed store reported")
	}
	if strings.Join(rec.deleted, ",") != newSessionID {
		t.Errorf("revoked %v, want only the session this login minted: the replaced one is still the stored credential", rec.deleted)
	}
	if got := rec.deleteAuth[newSessionID]; got != "Bearer "+replacementTok {
		t.Errorf("revoke Authorization = %q, want the new token", got)
	}
	for _, want := range []string{"failed to store credential", "revoked"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("storeCredential() error = %q, want it to contain %q", err, want)
		}
	}
}

// TestStoreCredential_DoesNotRevokeASessionSharingTheNewName: the replaced
// credential carrying the same name as the new one is what --token-name makes
// possible, and revoking by that name would end the login that was just made.
func TestStoreCredential_DoesNotRevokeASessionSharingTheNewName(t *testing.T) {
	_, mock, endpointKey := storeCredentialEnv(t)
	storePrevious(t, true, endpointKey, replacementNm, time.Now().Add(time.Hour))
	newDelegationRecorder(t, mock, loginDelegation(sessionsPreviousID, replacementNm))

	result, err := storeCredential(context.Background(), mock.Server.URL, 0,
		newExchangeResponse(), replacementNm, true)
	if err != nil {
		t.Fatalf("storeCredential() error = %v", err)
	}
	if got := mock.Requests(); len(got) != 0 {
		t.Errorf("requests = %v, want none: revoking that name would end the session just created", got)
	}
	if result.RevokedPrevious || result.ReplacementWarning != "" {
		t.Errorf("result = %+v, want a silent no-op", result)
	}
}

// TestStoreCredential_WarnsWhenAnUnnamedPreviousSessionIsStillLive: a
// credential stored before qctl minted names has no handle on its session, so
// there is nothing to match a delegation by. The session is live and about to
// become unreachable, which is exactly what the user has to be told, together
// with the surface that can still reach it by ID.
func TestStoreCredential_WarnsWhenAnUnnamedPreviousSessionIsStillLive(t *testing.T) {
	_, mock, endpointKey := storeCredentialEnv(t)
	storePrevious(t, true, endpointKey, "", time.Now().Add(time.Hour))
	newDelegationRecorder(t, mock, loginDelegation(sessionsPreviousID, replacedName))

	result, err := storeCredential(context.Background(), mock.Server.URL, 0,
		newExchangeResponse(), replacementNm, true)
	if err != nil {
		t.Fatalf("storeCredential() error = %v", err)
	}
	if got := mock.Requests(); len(got) != 0 {
		t.Errorf("requests = %v, want none: an unnamed credential matches no delegation", got)
	}
	for _, want := range []string{"no token name", "qctl auth sessions"} {
		if !strings.Contains(result.ReplacementWarning, want) {
			t.Errorf("ReplacementWarning = %q, want it to contain %q", result.ReplacementWarning, want)
		}
	}
}

// TestStoreCredential_IsSilentWhenAnUnnamedPreviousSessionHasExpired is the
// other half: same missing handle, but the session behind it is already
// unusable, so the warning would name a problem the user cannot have.
func TestStoreCredential_IsSilentWhenAnUnnamedPreviousSessionHasExpired(t *testing.T) {
	_, mock, endpointKey := storeCredentialEnv(t)
	storePrevious(t, true, endpointKey, "", time.Now().Add(-time.Minute))
	newDelegationRecorder(t, mock, loginDelegation(sessionsPreviousID, replacedName))

	result, err := storeCredential(context.Background(), mock.Server.URL, 0,
		newExchangeResponse(), replacementNm, true)
	if err != nil {
		t.Fatalf("storeCredential() error = %v", err)
	}
	if got := mock.Requests(); len(got) != 0 {
		t.Errorf("requests = %v, want none", got)
	}
	if result.ReplacementWarning != "" {
		t.Errorf("ReplacementWarning = %q, want silence for an expired unnamed credential", result.ReplacementWarning)
	}
}

// TestStoreCredential_RefusesToReplaceACorruptCredentialsFile pins the command
// contract shared by login and ops switch-org. The exchange has already minted
// a session, so refusing the local write must also retire that new session.
func TestStoreCredential_RefusesToReplaceACorruptCredentialsFile(t *testing.T) {
	_, mock, _ := storeCredentialEnv(t)
	credPath, err := qauth.CredentialsFilePath()
	if err != nil {
		t.Fatalf("credentials file path: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(credPath), 0700); err != nil {
		t.Fatalf("create the credentials directory: %v", err)
	}
	corrupt := []byte("{not json")
	if err := os.WriteFile(credPath, corrupt, 0600); err != nil {
		t.Fatalf("write the corrupt credentials file: %v", err)
	}
	rec := newDelegationRecorder(t, mock, loginDelegation(newSessionID, replacementNm))

	_, err = storeCredential(context.Background(), mock.Server.URL, 0,
		newExchangeResponse(), replacementNm, true)
	if err == nil {
		t.Fatal("storeCredential() error = nil, want the corrupt file to block login storage")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitGenericError {
		t.Errorf("exit code = %d, want %d: corrupt local state is not bad command usage", got, qerrors.ExitGenericError)
	}
	for _, want := range []string{"failed to store credential", credPath, "Moving this file aside", "token_name", "revoked"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("storeCredential() error = %q, want it to contain %q", err, want)
		}
	}
	if got := strings.Join(rec.deleted, ","); got != newSessionID {
		t.Errorf("revoked = %v, want only the session this failed login minted", rec.deleted)
	}
	got, readErr := os.ReadFile(credPath)
	if readErr != nil {
		t.Fatalf("read preserved credentials file: %v", readErr)
	}
	if string(got) != string(corrupt) {
		t.Errorf("credentials file = %q, want original corrupt bytes %q", got, corrupt)
	}
}

// TestStoreCredential_KeepsAPreviousSessionAFileEntryHoldsUnderAMismatchedOrg
// pins that the fallback-file guard is addressed by the organization the store
// was addressed with, not by the organization_id inside the stored JSON.
//
// Retrieve unmarshals what it finds and backfills nothing, so a legacy or
// hand-edited credentials.json can hold an entry whose organization_id
// disagrees with the key it lives under -- Store itself cannot produce one,
// since it derives the key from that very field. Keying the guard off the field
// would miss the entry, conclude the replaced token has no local handle, and
// revoke a session that credentials.json still serves to every keyring-less
// reader.
func TestStoreCredential_KeepsAPreviousSessionAFileEntryHoldsUnderAMismatchedOrg(t *testing.T) {
	env, mock, endpointKey := storeCredentialEnv(t)

	// Written by hand: the entry sits under the login's org key while carrying
	// no organization_id of its own, which is what an entry predating the field
	// looks like.
	credPath, err := qauth.CredentialsFilePath()
	if err != nil {
		t.Fatalf("credentials path: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(credPath), 0700); err != nil {
		t.Fatalf("create credentials dir: %v", err)
	}
	legacy := map[string]map[string]any{
		qauth.MakeCredentialKey(endpointKey, loginOrgID): {
			"created_at":      time.Now().Add(-time.Hour),
			"access_token":    replacedToken,
			"expires_at":      time.Now().Add(time.Hour),
			"organization_id": "",
			"token_name":      replacedName,
		},
	}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshal legacy credentials: %v", err)
	}
	if err := os.WriteFile(credPath, data, 0600); err != nil {
		t.Fatalf("write legacy credentials: %v", err)
	}

	defer env.SetEnv("QCTL_ALLOW_PLAINTEXT_TOKENS", "")()
	testutil.StubKeyring(t, testutil.KeyringStub{})
	newDelegationRecorder(t, mock, loginDelegation(sessionsPreviousID, replacedName))

	result, err := storeCredential(context.Background(), mock.Server.URL, 0,
		newExchangeResponse(), replacementNm, false)
	if err != nil {
		t.Fatalf("storeCredential() error = %v", err)
	}
	if got := mock.Requests(); len(got) != 0 {
		t.Errorf("requests = %v, want none: the file still holds the replaced token under this key", got)
	}
	if result.RevokedPrevious {
		t.Error("RevokedPrevious = true, but the replaced session still has a local handle")
	}
}

// TestStoreCredential_PartialRevokeNeverClaimsARevocationAndAFailureAtOnce
// covers a name matching more than one delegation, with one revoked and the
// next refused. "Revoked the previous CLI session" on stdout beside "could not
// revoke the previous CLI session" on stderr is the same singular claim twice,
// in opposite directions, so the warning has to carry the whole outcome alone.
func TestStoreCredential_PartialRevokeNeverClaimsARevocationAndAFailureAtOnce(t *testing.T) {
	const secondID = "dddddddd-eeee-4fff-8aaa-111111111111"
	_, mock, endpointKey := storeCredentialEnv(t)
	storePrevious(t, true, endpointKey, replacedName, time.Now().Add(time.Hour))
	rec := newDelegationRecorder(t, mock,
		loginDelegation(sessionsPreviousID, replacedName),
		loginDelegation(secondID, replacedName))
	rec.Revoke = func(id string) (int, bool) {
		if id == secondID {
			return http.StatusInternalServerError, false
		}
		return http.StatusOK, true
	}

	result, err := storeCredential(context.Background(), mock.Server.URL, 0,
		newExchangeResponse(), replacementNm, true)
	if err != nil {
		t.Fatalf("storeCredential() error = %v", err)
	}
	if result.RevokedPrevious {
		t.Error("RevokedPrevious = true alongside a failure warning: login would print a revocation and a non-revocation of the same session")
	}
	if !strings.Contains(result.ReplacementWarning, "revoked 1 of the CLI sessions") {
		t.Errorf("ReplacementWarning = %q, want it to report the partial revocation it did perform", result.ReplacementWarning)
	}
	if !strings.Contains(result.ReplacementWarning, "could not revoke the rest") {
		t.Errorf("ReplacementWarning = %q, want it to report the part that failed", result.ReplacementWarning)
	}
}

// seedKeyringAndFile leaves the two credentials a keyring login followed by a
// login with --allow-plaintext-token-store leaves behind, and returns the
// stubbed keyring's entries. keyringStub supplies any refusal the case needs.
func seedKeyringAndFile(
	t *testing.T,
	env *testutil.TestEnv,
	endpointKey string,
	keyringStub testutil.KeyringStub,
	keyringCopy, fileCopy qauth.Credential,
) map[string]string {
	t.Helper()
	if _, err := qauth.NewCredentialStore(true).Store(endpointKey, &fileCopy); err != nil {
		t.Fatalf("store the file copy: %v", err)
	}
	keyringJSON, err := json.Marshal(keyringCopy)
	if err != nil {
		t.Fatalf("marshal the keyring copy: %v", err)
	}
	if keyringStub.Entries == nil {
		keyringStub.Entries = map[string]string{}
	}
	keyringStub.Entries[qauth.MakeCredentialKey(endpointKey, loginOrgID)] = string(keyringJSON)
	t.Cleanup(env.SetEnv("QCTL_ALLOW_PLAINTEXT_TOKENS", ""))
	testutil.StubKeyring(t, keyringStub)
	return keyringStub.Entries
}

func storedCredential(token, name string, age time.Duration) qauth.Credential {
	return qauth.Credential{
		CreatedAt:      time.Now().Add(-age),
		AccessToken:    token,
		ExpiresAt:      time.Now().Add(time.Hour),
		OrganizationID: loginOrgID,
		TokenName:      name,
	}
}

// TestStoreCredential_RevokesTheKeyringCopyAKeyringWriteOverwrites is the
// orphan a keyring login used to leave when the fallback file held a newer
// credential than the keyring. That newer file copy is the one ordinary reads
// return, and it survives the write; the keyring copy is the one the write
// overwrites, so it is the one whose session has to end -- with its own token.
func TestStoreCredential_RevokesTheKeyringCopyAKeyringWriteOverwrites(t *testing.T) {
	env, mock, endpointKey := storeCredentialEnv(t)
	entries := seedKeyringAndFile(t, env, endpointKey, testutil.KeyringStub{},
		storedCredential(keyringCopyToken, keyringCopyName, 2*time.Hour),
		storedCredential(fileCopyToken, fileCopyName, time.Hour))
	rec := newDelegationRecorder(t, mock,
		loginDelegation(keyringCopyID, keyringCopyName),
		loginDelegation(fileCopyID, fileCopyName))

	result, err := storeCredential(context.Background(), mock.Server.URL, 0,
		newExchangeResponse(), replacementNm, false)
	if err != nil {
		t.Fatalf("storeCredential() error = %v", err)
	}
	if got := strings.Join(rec.deleted, ","); got != keyringCopyID {
		t.Errorf("revoked %v, want only the keyring copy's session: the file copy still has its local handle", rec.deleted)
	}
	if got := rec.deleteAuth[keyringCopyID]; got != "Bearer "+keyringCopyToken {
		t.Errorf("revoke Authorization = %q, want the overwritten credential's own token", got)
	}
	if result.RevokedPrevious {
		t.Error("RevokedPrevious = true, but the credential ordinary commands read was not revoked")
	}
	if result.ReplacementWarning != "" {
		t.Errorf("ReplacementWarning = %q, want none after a clean revoke", result.ReplacementWarning)
	}
	if !strings.Contains(entries[qauth.MakeCredentialKey(endpointKey, loginOrgID)], replacementTok) {
		t.Error("the keyring does not hold the new credential")
	}
	fileCred, err := qauth.NewCredentialStore(true).Retrieve(endpointKey, loginOrgID)
	if err != nil || fileCred.AccessToken != fileCopyToken {
		t.Errorf("fallback file = %+v, %v; want the file copy left in place", fileCred, err)
	}

	var stdout, stderr strings.Builder
	reportStoreResult(&stdout, &stderr, result, "this organization")
	want := `Revoked the other CLI session "` + keyringCopyName +
		`" for this organization, whose credential was stored in the OS keyring.` + "\n"
	if stdout.String() != want {
		t.Errorf("stdout = %q, want %q", stdout.String(), want)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want nothing", stderr.String())
	}
}

// TestStoreCredential_RevokesBothCopiesARefusedKeyringWriteReplaces: when the
// keyring refuses the write, Store puts the new credential in the file and
// removes the keyring's entry, so both stored credentials are gone and both
// sessions have to end, each with its own token.
func TestStoreCredential_RevokesBothCopiesARefusedKeyringWriteReplaces(t *testing.T) {
	env, mock, endpointKey := storeCredentialEnv(t)
	entries := seedKeyringAndFile(t, env, endpointKey, testutil.KeyringStub{
		Set: func(_, _, _ string) error { return errors.New("org.freedesktop.DBus.Error.AccessDenied") },
	},
		storedCredential(keyringCopyToken, keyringCopyName, time.Hour),
		storedCredential(fileCopyToken, fileCopyName, 2*time.Hour))
	rec := newDelegationRecorder(t, mock,
		loginDelegation(keyringCopyID, keyringCopyName),
		loginDelegation(fileCopyID, fileCopyName))

	result, err := storeCredential(context.Background(), mock.Server.URL, 0,
		newExchangeResponse(), replacementNm, false)
	if err != nil {
		t.Fatalf("storeCredential() error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("keyring entries = %v, want the refused entry removed", entries)
	}
	for id, token := range map[string]string{keyringCopyID: keyringCopyToken, fileCopyID: fileCopyToken} {
		if got := rec.deleteAuth[id]; got != "Bearer "+token {
			t.Errorf("revoke of %s Authorization = %q, want its own credential's token", id, got)
		}
	}
	if !result.RevokedPrevious {
		t.Error("RevokedPrevious = false, but the keyring copy ordinary commands read was revoked")
	}
	wantOther := otherSession{Name: fileCopyName, Where: qauth.CredentialsFileLocation()}
	if result.RevokedOther == nil || *result.RevokedOther != wantOther {
		t.Errorf("RevokedOther = %+v, want %+v", result.RevokedOther, wantOther)
	}
	if result.ReplacementWarning != "" {
		t.Errorf("ReplacementWarning = %q, want none after clean revokes", result.ReplacementWarning)
	}
}

// TestStoreCredential_NamesWhereTheOtherCopyWasStoredWhenItsRevokeFails: the
// warning for the other copy has to tell it apart from the one ordinary
// commands read, and still hand over the by-name retry.
func TestStoreCredential_NamesWhereTheOtherCopyWasStoredWhenItsRevokeFails(t *testing.T) {
	env, mock, endpointKey := storeCredentialEnv(t)
	seedKeyringAndFile(t, env, endpointKey, testutil.KeyringStub{},
		storedCredential(keyringCopyToken, keyringCopyName, 2*time.Hour),
		storedCredential(fileCopyToken, fileCopyName, time.Hour))
	rec := newDelegationRecorder(t, mock,
		loginDelegation(keyringCopyID, keyringCopyName),
		loginDelegation(fileCopyID, fileCopyName))
	rec.Revoke = func(string) (int, bool) { return http.StatusInternalServerError, false }

	result, err := storeCredential(context.Background(), mock.Server.URL, 0,
		newExchangeResponse(), replacementNm, false)
	if err != nil {
		t.Fatalf("storeCredential() error = %v", err)
	}
	if result.RevokedPrevious || result.RevokedOther != nil {
		t.Errorf("result = %+v, want no revocation claimed", result)
	}
	for _, want := range []string{
		`could not revoke the other CLI session "` + keyringCopyName + `", whose credential was stored in the OS keyring: `,
		"qctl auth sessions revoke " + keyringCopyName,
	} {
		if !strings.Contains(result.ReplacementWarning, want) {
			t.Errorf("ReplacementWarning = %q, want it to contain %q", result.ReplacementWarning, want)
		}
	}
	if strings.Contains(result.ReplacementWarning, fileCopyName) {
		t.Errorf("ReplacementWarning = %q names the file copy, which the write did not replace", result.ReplacementWarning)
	}
}

// TestStoreCredential_LeavesAnUnnamedCopyTheWriteDidNotReplace: a live
// credential with no token name still warns when it is replaced, but one the
// fallback file goes on holding was not replaced at all, and warning about it
// would claim a lost handle that is still there.
func TestStoreCredential_LeavesAnUnnamedCopyTheWriteDidNotReplace(t *testing.T) {
	env, mock, endpointKey := storeCredentialEnv(t)
	seedKeyringAndFile(t, env, endpointKey, testutil.KeyringStub{},
		storedCredential(keyringCopyToken, keyringCopyName, time.Hour),
		storedCredential(fileCopyToken, "", 2*time.Hour))
	rec := newDelegationRecorder(t, mock, loginDelegation(keyringCopyID, keyringCopyName))

	result, err := storeCredential(context.Background(), mock.Server.URL, 0,
		newExchangeResponse(), replacementNm, false)
	if err != nil {
		t.Fatalf("storeCredential() error = %v", err)
	}
	if got := strings.Join(rec.deleted, ","); got != keyringCopyID {
		t.Errorf("revoked %v, want the overwritten keyring copy's session", rec.deleted)
	}
	if !result.RevokedPrevious || result.RevokedOther != nil {
		t.Errorf("result = %+v, want only the previous session reported revoked", result)
	}
	if result.ReplacementWarning != "" {
		t.Errorf("ReplacementWarning = %q, want silence about a credential the file still holds", result.ReplacementWarning)
	}
}

// TestStoreCredential_KeepsACopyTheKeyringStillHoldsAfterAFileFallback: a
// keyring write that fails without a refusal falls back to the file and
// leaves the keyring entry where it was, so the credential it holds keeps its
// local handle there -- a check of the file alone would miss it.
func TestStoreCredential_KeepsACopyTheKeyringStillHoldsAfterAFileFallback(t *testing.T) {
	env, mock, endpointKey := storeCredentialEnv(t)
	keyringJSON, err := json.Marshal(storedCredential(keyringCopyToken, keyringCopyName, time.Hour))
	if err != nil {
		t.Fatalf("marshal the keyring copy: %v", err)
	}
	key := qauth.MakeCredentialKey(endpointKey, loginOrgID)
	defer env.SetEnv("QCTL_ALLOW_PLAINTEXT_TOKENS", "")()
	testutil.StubKeyring(t, testutil.KeyringStub{
		Entries: map[string]string{key: string(keyringJSON)},
		Set:     func(_, _, _ string) error { return keyring.ErrUnsupportedPlatform },
	})
	newDelegationRecorder(t, mock, loginDelegation(keyringCopyID, keyringCopyName))

	result, err := storeCredential(context.Background(), mock.Server.URL, 0,
		newExchangeResponse(), replacementNm, false)
	if err != nil {
		t.Fatalf("storeCredential() error = %v", err)
	}
	if got := mock.Requests(); len(got) != 0 {
		t.Errorf("requests = %v, want none: the keyring still holds the credential", got)
	}
	if result.RevokedPrevious || result.RevokedOther != nil || result.ReplacementWarning != "" {
		t.Errorf("result = %+v, want a silent no-op", result)
	}
}

// TestStoreCredential_PlaintextOptInNeverTouchesTheKeyring: with
// QCTL_ALLOW_PLAINTEXT_TOKENS set, Store writes the fallback file alone, so the
// file's copy is the only one a login can replace -- and on a desktop every
// keyring access can raise an unlock prompt the opt-in exists to avoid.
func TestStoreCredential_PlaintextOptInNeverTouchesTheKeyring(t *testing.T) {
	_, mock, endpointKey := storeCredentialEnv(t)
	t.Setenv("QCTL_ALLOW_PLAINTEXT_TOKENS", "1")
	storePrevious(t, true, endpointKey, replacedName, time.Now().Add(time.Hour))
	touched := func(op string) { t.Errorf("keyring %s under the plaintext opt-in", op) }
	testutil.StubKeyring(t, testutil.KeyringStub{
		Get:    func(_, _ string) (string, error) { touched("read"); return "", keyring.ErrNotFound },
		Set:    func(_, _, _ string) error { touched("write"); return nil },
		Delete: func(_, _ string) error { touched("delete"); return nil },
	})
	rec := newDelegationRecorder(t, mock, loginDelegation(sessionsPreviousID, replacedName))

	result, err := storeCredential(context.Background(), mock.Server.URL, 0,
		newExchangeResponse(), replacementNm, false)
	if err != nil {
		t.Fatalf("storeCredential() error = %v", err)
	}
	if got := rec.deleteAuth[sessionsPreviousID]; got != "Bearer "+replacedToken {
		t.Errorf("revoke Authorization = %q, want the replaced file credential's own token", got)
	}
	if !result.RevokedPrevious {
		t.Error("RevokedPrevious = false, but the file write replaced a live credential")
	}
}
