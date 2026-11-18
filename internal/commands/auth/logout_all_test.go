package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	qauth "github.com/qlustered/qctl/internal/auth"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

// The second organization exists to prove the point of --all: a revocation that
// killed the tokens of every organization on the server must not leave one of
// them behind on disk, still looking like a login.
const (
	logoutOtherOrgID   = "c3d4e5f6-a7b8-9012-cdef-345678901234"
	logoutOtherOrgName = "Beta Industries"
)

func logoutAllPath() string { return "/api/orgs/" + logoutOrgID + "/logout" }

// setupLogoutAllConfig caches BOTH organizations on one context, which is what
// a user who has run `auth switch-org` ends up with.
func setupLogoutAllConfig(t *testing.T, server string) {
	t.Helper()
	cfg := &config.Config{
		APIVersion:     config.APIVersion,
		CurrentContext: "default",
		Contexts: map[string]*config.Context{
			"default": {
				Server:           server,
				Organization:     logoutOrgID,
				OrganizationName: logoutOrgName,
				Organizations: []config.OrganizationRef{
					{ID: logoutOrgID, Name: logoutOrgName},
					{ID: logoutOtherOrgID, Name: logoutOtherOrgName},
				},
			},
		},
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("save config: %v", err)
	}
}

// credentialGoneFor is credentialGone for an arbitrary organization: --all has
// to be checked against the organizations it was NOT pointed at.
func credentialGoneFor(t *testing.T, endpointKey, orgID string) bool {
	t.Helper()
	cred, err := newTestStore().Retrieve(endpointKey, orgID)
	return err != nil || cred == nil
}

// revocationBody is a 200 shaped the way atlas answers a bearer revoke-all:
// TokenDataSchema with the additive `revocation` object filled in.
func revocationBody(scope api.SessionRevocationSchemaScope, delegations, apiTokens int) api.TokenDataSchema {
	return api.TokenDataSchema{
		OrganizationID: uuid.MustParse(logoutOrgID),
		Revocation: &api.SessionRevocationSchema{
			RevokedAt:          time.Now().UTC(),
			Scope:              scope,
			DelegationsRevoked: delegations,
			APITokensRevoked:   apiTokens,
		},
	}
}

func TestLogoutAll_RevokesEverySessionAndClearsEveryOrganization(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	setupLogoutAllConfig(t, mock.Server.URL)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupCredentialNamed(endpointKey, logoutOrgID, "test-token", logoutTokenName)
	env.SetupCredentialNamed(endpointKey, logoutOtherOrgID, "other-token", "qctl@box-99887766")

	var gotQuery, gotAuth string
	mock.RegisterHandler("POST", logoutAllPath(), func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		gotAuth = r.Header.Get("Authorization")
		testutil.RespondJSON(w, http.StatusOK, revocationBody(api.Global, 3, 2))
	})

	stdout, stderr, err := runLogoutCmd(t, "--all")
	if err != nil {
		t.Fatalf("Execute() error = %v; stdout: %q stderr: %q", err, stdout, stderr)
	}

	// The request shape is the contract with atlas: a bearer caller is refused
	// with 400 unless all_sessions is true, and sending a user_id would narrow
	// the revocation to one organization -- silently making the "every
	// organization" promise in the output below false.
	if gotQuery != "all_sessions=true" {
		t.Errorf("query = %q, want exactly %q -- no user_id, and all_sessions must be true", gotQuery, "all_sessions=true")
	}
	if gotAuth != "Bearer test-token" {
		t.Errorf("Authorization = %q, want the stored bearer credential", gotAuth)
	}

	for _, orgID := range []string{logoutOrgID, logoutOtherOrgID} {
		if !credentialGoneFor(t, endpointKey, orgID) {
			t.Errorf("credential for organization %s survived --all; the server has already revoked it", orgID)
		}
	}

	wantRevoked := "Revoked every session and token for this user on " + endpointKey + " (3 delegations, 2 API tokens)."
	if !strings.Contains(stdout, wantRevoked) {
		t.Errorf("stdout = %q, want it to report %q", stdout, wantRevoked)
	}
	const wantRemoved = "Removed stored credentials for 2 organization(s)."
	if !strings.Contains(stdout, wantRemoved) {
		t.Errorf("stdout = %q, want it to report %q", stdout, wantRemoved)
	}
	// The counts are the server's. Asserting on the ORDER catches a rewrite
	// that reports the local cleanup before the revocation it depends on.
	if strings.Index(stdout, wantRevoked) > strings.Index(stdout, wantRemoved) {
		t.Errorf("stdout = %q, want the server-side revocation reported first", stdout)
	}
	wantNoWarning(t, stderr)
}

func TestLogoutAll_RejectsLocalBeforeAnyRequest(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	setupLogoutAllConfig(t, mock.Server.URL)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupCredentialNamed(endpointKey, logoutOrgID, "test-token", logoutTokenName)

	stdout, stderr, err := runLogoutCmd(t, "--all", "--local")
	if err == nil {
		t.Fatalf("--all --local must be refused; stdout: %q stderr: %q", stdout, stderr)
	}
	// The code, not the text: a substring assertion passes against a version
	// that exits 1 and leaves scripts unable to tell a usage error from a
	// failed revocation.
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d for a flag-combination failure", code, qerrors.ExitBadUsage)
	}
	if reqs := mock.Requests(); len(reqs) != 0 {
		t.Errorf("requests = %v, want none -- the refusal must come before any server call", reqs)
	}
	if credentialGoneFor(t, endpointKey, logoutOrgID) {
		t.Error("a refused flag combination must not delete the credential")
	}
}

func TestLogoutAll_RefusedCredentialRemovesTheLocalCopies(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
	}{
		{"401 the token is already dead", http.StatusUnauthorized},
		{"403 this caller may not revoke", http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()

			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			setupLogoutAllConfig(t, mock.Server.URL)
			endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
			env.SetupCredentialNamed(endpointKey, logoutOrgID, "test-token", logoutTokenName)
			env.SetupCredentialNamed(endpointKey, logoutOtherOrgID, "other-token", "qctl@box-99887766")

			mock.RegisterHandler("POST", logoutAllPath(), func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondError(w, tc.status, "nope")
			})

			stdout, stderr, err := runLogoutCmd(t, "--all")
			if err == nil {
				t.Fatalf("a refused revocation must exit non-zero; stdout: %q stderr: %q", stdout, stderr)
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitUnauthorized {
				t.Errorf("exit code = %d, want %d", code, qerrors.ExitUnauthorized)
			}

			// The stored copies are worthless whichever of the two it was, so
			// they go -- but the user is told qctl could not confirm the thing
			// they ran the command for.
			for _, orgID := range []string{logoutOrgID, logoutOtherOrgID} {
				if !credentialGoneFor(t, endpointKey, orgID) {
					t.Errorf("credential for organization %s survived a %d", orgID, tc.status)
				}
			}
			if !strings.Contains(stderr, "Could not confirm the server-side revocation") ||
				!strings.Contains(stderr, `"Sign out of all devices"`) {
				t.Errorf("stderr = %q, want the unconfirmed-revocation note naming the web app remedy", stderr)
			}
			if strings.Contains(stdout, "Revoked every session") {
				t.Errorf("stdout = %q, must not claim a revocation the server refused", stdout)
			}
		})
	}
}

func TestLogoutAll_UnreachableServerKeepsEveryCredential(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	// Any host but mock.local is a connection error from the stub transport,
	// which is exactly the case this row is about.
	const unreachable = "http://127.0.0.1:1"
	setupLogoutAllConfig(t, unreachable)
	endpointKey, _ := config.NormalizeEndpointKey(unreachable)
	env.SetupCredentialNamed(endpointKey, logoutOrgID, "test-token", logoutTokenName)
	env.SetupCredentialNamed(endpointKey, logoutOtherOrgID, "other-token", "qctl@box-99887766")

	stdout, stderr, err := runLogoutCmd(t, "--all")
	if err == nil {
		t.Fatalf("an unreachable server must exit non-zero; stdout: %q stderr: %q", stdout, stderr)
	}
	// Nothing was confirmed revoked, so the credential that can authenticate a
	// retry is the one thing that must not be thrown away.
	for _, orgID := range []string{logoutOrgID, logoutOtherOrgID} {
		if credentialGoneFor(t, endpointKey, orgID) {
			t.Errorf("credential for organization %s was deleted after an unreachable server", orgID)
		}
	}
	if !strings.Contains(err.Error(), "kept so you can retry") {
		t.Errorf("error = %q, want the existing retryable-failure wording", err)
	}
}

func TestLogoutAll_A200WithoutRevocationKeepsEveryCredential(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	setupLogoutAllConfig(t, mock.Server.URL)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupCredentialNamed(endpointKey, logoutOrgID, "test-token", logoutTokenName)

	// A 200 whose `revocation` is absent -- the shape a single-session logout
	// answers with. Reporting "0 delegations, 0 API tokens" for it would be a
	// fabricated reassurance about the only thing the user asked.
	mock.RegisterHandler("POST", logoutAllPath(), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.TokenDataSchema{OrganizationID: uuid.MustParse(logoutOrgID)})
	})

	stdout, stderr, err := runLogoutCmd(t, "--all")
	if err == nil {
		t.Fatalf("an unreported revocation must exit non-zero; stdout: %q stderr: %q", stdout, stderr)
	}
	if credentialGoneFor(t, endpointKey, logoutOrgID) {
		t.Error("credential deleted on an answer qctl could not confirm")
	}
	if strings.Contains(stdout, "Revoked every session") {
		t.Errorf("stdout = %q, must not report counts the server did not send", stdout)
	}
}

// The backend narrows a revocation to one organization only when an admin
// revokes for somebody else -- which qctl never asks for. If that ever changes
// underneath, the output's "every session and token for this user" and the
// deletion of every organization's credential both become false at once.
func TestLogoutAll_OrganizationScopedRevocationIsNotReportedAsGlobal(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	setupLogoutAllConfig(t, mock.Server.URL)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupCredentialNamed(endpointKey, logoutOrgID, "test-token", logoutTokenName)
	env.SetupCredentialNamed(endpointKey, logoutOtherOrgID, "other-token", "qctl@box-99887766")

	mock.RegisterHandler("POST", logoutAllPath(), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, revocationBody(api.Organization, 1, 0))
	})

	stdout, _, err := runLogoutCmd(t, "--all")
	if err == nil {
		t.Fatalf("an organization-scoped revocation must not be reported as global; stdout: %q", stdout)
	}
	if credentialGoneFor(t, endpointKey, logoutOtherOrgID) {
		t.Error("the other organization's credential was deleted on a revocation that did not cover it")
	}
	if strings.Contains(stdout, "Revoked every session") {
		t.Errorf("stdout = %q, must not claim a global revocation", stdout)
	}
}

func TestLogoutAll_BadRequestReportsTheServerDetail(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	setupLogoutAllConfig(t, mock.Server.URL)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupCredentialNamed(endpointKey, logoutOrgID, "test-token", logoutTokenName)

	const detail = "A bearer credential cannot end a single session."
	mock.RegisterHandler("POST", logoutAllPath(), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusBadRequest, map[string]string{"detail": detail})
	})

	stdout, stderr, err := runLogoutCmd(t, "--all")
	if err == nil {
		t.Fatalf("a 400 must exit non-zero; stdout: %q stderr: %q", stdout, stderr)
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
	if !strings.Contains(err.Error(), detail) {
		t.Errorf("error = %q, want the server's detail", err)
	}
	// A 400 means qctl built the request wrong; nothing was revoked, so the
	// retry advice the other failures carry would be misleading here.
	if strings.Contains(err.Error(), "kept so you can retry") {
		t.Errorf("error = %q, must not offer a retry for a request the server refuses every time", err)
	}
	if credentialGoneFor(t, endpointKey, logoutOrgID) {
		t.Error("credential deleted after a 400, which revoked nothing")
	}
}

// candidateOrganizations is what stands in for a keyring listing, which does
// not exist. A context pointing at the same SERVER under a different name is
// the case that a match on context name would miss.
func TestCandidateOrganizations_UnionsEverySourceForOneServer(t *testing.T) {
	const (
		sameServer  = "https://api.example.com"
		otherServer = "https://other.example.com"
		configOnly  = "11111111-1111-1111-1111-111111111111"
		fileOnly    = "22222222-2222-2222-2222-222222222222"
		elsewhere   = "33333333-3333-3333-3333-333333333333"
	)
	endpointKey, err := config.NormalizeEndpointKey(sameServer)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}

	cfg := &config.Config{
		Contexts: map[string]*config.Context{
			"prod": {Server: sameServer, Organization: logoutOrgID},
			// Same server, different context name, and the only source that
			// names configOnly.
			"prod-alt": {
				Server:        sameServer + "/",
				Organizations: []config.OrganizationRef{{ID: configOnly, Name: "Config Only"}},
			},
			// A different server entirely: its organizations belong to a
			// revocation this one did not perform.
			"staging": {Server: otherServer, Organization: elsewhere},
		},
	}

	got := candidateOrganizations(cfg, endpointKey, logoutOrgID, []string{fileOnly, logoutOrgID})
	want := []string{configOnly, fileOnly, logoutOrgID}
	if len(got) != len(want) {
		t.Fatalf("candidateOrganizations() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("candidateOrganizations() = %v, want %v (sorted, deduplicated)", got, want)
		}
	}
	for _, orgID := range got {
		if orgID == elsewhere {
			t.Error("an organization from another server must not be a candidate for this one's cleanup")
		}
	}
}

// The org flag resolves the same way as it does for a plain logout: --all is
// pointed at ONE organization's credential to authenticate with, even though
// what it revokes is not scoped to that organization. A logout that keyed on
// the raw --org string is the footgun TestLogout_ResolvesOrgNameToUUID retired;
// this proves --all did not reintroduce it on a separate code path.
func TestLogoutAll_ResolvesOrgNameToUUID(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	setupLogoutAllConfig(t, mock.Server.URL)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupCredentialNamed(endpointKey, logoutOrgID, "test-token", logoutTokenName)

	mock.RegisterHandler("POST", logoutAllPath(), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, revocationBody(api.Global, 1, 0))
	})

	stdout, stderr, err := runLogoutCmd(t, "--all", "--org", logoutOrgName)
	if err != nil {
		t.Fatalf("Execute() error = %v; stdout: %q stderr: %q", err, stdout, stderr)
	}
	// The org NAME must have been resolved to the UUID in the path; the mock
	// synthesises a 404 for any other path, so a raw name would never reach a
	// 200 at all.
	want := "POST " + logoutAllPath()
	if reqs := mock.Requests(); len(reqs) != 1 || reqs[0] != want {
		t.Errorf("requests = %v, want exactly [%s]", reqs, want)
	}
	if !credentialGoneFor(t, endpointKey, logoutOrgID) {
		t.Error("credential survived a successful --all")
	}
}

// A cleanup failure is reported ONCE for the shared fallback file, not once per
// organization: every organization the config caches is a candidate, so looping
// describeFailedDelete would print the same paragraph about credentials.json as
// many times as the user has ever switched org.
func TestLogoutAll_ASharedFileFailureIsReportedOnce(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	setupLogoutAllConfig(t, mock.Server.URL)
	// Keyring mode: Retrieve never opens the fallback file, so an unparseable
	// one is a delete failure and not a read failure.
	defer env.SetEnv("QCTL_ALLOW_PLAINTEXT_TOKENS", "")()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	entries := map[string]string{}
	for _, orgID := range []string{logoutOrgID, logoutOtherOrgID} {
		credJSON, err := json.Marshal(qauth.Credential{
			CreatedAt:      time.Now(),
			AccessToken:    "test-token",
			ExpiresAt:      time.Now().Add(time.Hour),
			OrganizationID: orgID,
			TokenName:      logoutTokenName,
		})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		entries[qauth.MakeCredentialKey(endpointKey, orgID)] = string(credJSON)
	}
	testutil.StubKeyring(t, testutil.KeyringStub{Entries: entries})

	credPath, err := qauth.CredentialsFilePath()
	if err != nil {
		t.Fatalf("CredentialsFilePath(): %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(credPath), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(credPath, []byte("{not json"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	mock.RegisterHandler("POST", logoutAllPath(), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, revocationBody(api.Global, 4, 1))
	})

	stdout, _, err := runLogoutCmd(t, "--all")
	if err == nil {
		t.Fatal("a cleanup that could not clear the fallback file must exit non-zero")
	}
	// The revocation happened, so it is still reported -- first, and before the
	// failure of the local half.
	if !strings.Contains(stdout, "Revoked every session and token for this user") {
		t.Errorf("stdout = %q, want the server-side revocation still reported: it happened", stdout)
	}
	if strings.Contains(stdout, "Removed stored credentials") {
		t.Errorf("stdout = %q, must not claim a cleanup that failed", stdout)
	}
	if !strings.Contains(err.Error(), "2 stored credential(s) could not be removed") {
		t.Errorf("error = %q, want it to count both organizations", err)
	}
	if n := strings.Count(err.Error(), "Inspect it by hand."); n != 1 {
		t.Errorf("the shared-file remedy appears %d times in %q, want exactly 1", n, err)
	}
	// Nothing survived in the keyring, so no entry should be named for removal.
	if strings.Contains(err.Error(), "Remove these entries from your OS keyring") {
		t.Errorf("error = %q, must not send the user to the keyring: the entries were removed", err)
	}
}

// The other half of the same reporter: a keyring that refuses names EVERY entry
// that survived, because those are per-organization and a remedy naming one of
// two leaves a live token behind.
func TestLogoutAll_SurvivingKeyringEntriesAreEachNamed(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	setupLogoutAllConfig(t, mock.Server.URL)
	defer env.SetEnv("QCTL_ALLOW_PLAINTEXT_TOKENS", "")()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	entries := map[string]string{}
	for _, orgID := range []string{logoutOrgID, logoutOtherOrgID} {
		credJSON, err := json.Marshal(qauth.Credential{
			CreatedAt:      time.Now(),
			AccessToken:    "test-token",
			ExpiresAt:      time.Now().Add(time.Hour),
			OrganizationID: orgID,
			TokenName:      logoutTokenName,
		})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		entries[qauth.MakeCredentialKey(endpointKey, orgID)] = string(credJSON)
	}
	// Delete refuses while Get keeps answering, which is the only shape the
	// store reports: it proves the entry survived rather than guessing.
	testutil.StubKeyring(t, testutil.KeyringStub{
		Entries: entries,
		Delete:  func(_, _ string) error { return errors.New("keyring locked") },
	})

	mock.RegisterHandler("POST", logoutAllPath(), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, revocationBody(api.Global, 4, 1))
	})

	_, _, err := runLogoutCmd(t, "--all")
	if err == nil {
		t.Fatal("a keyring that kept both entries must exit non-zero")
	}
	// Assert against the REMEDY, not the whole message: deleteFromKeyring's own
	// error text already quotes the entry it could not remove, so every key
	// appears in the joined causes whatever the remedy lists. Checking
	// err.Error() passes against a remedy that names only the first one -- it
	// was written that way first, and a mutation proved it green.
	_, remedy, found := strings.Cut(err.Error(), "Remove these entries from your OS keyring")
	if !found {
		t.Fatalf("error = %q, want a keyring remedy naming the entries that survived", err)
	}
	for _, orgID := range []string{logoutOrgID, logoutOtherOrgID} {
		if !strings.Contains(remedy, qauth.MakeCredentialKey(endpointKey, orgID)) {
			t.Errorf("remedy = %q, want it to name the surviving entry for organization %s", remedy, orgID)
		}
	}
	if !strings.Contains(err.Error(), "do not remove the service itself") {
		t.Errorf("error = %q, want the warning against removing the whole qctl service", err)
	}
}

// The page and --help both say an expired credential is refused before the
// call. That is Bootstrap's rule rather than this command's, which is exactly
// why it is worth binding: nothing in runLogoutAll would fail if it changed,
// and the claim would quietly become false.
func TestLogoutAll_ExpiredCredentialIsRefusedBeforeTheCall(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	setupLogoutAllConfig(t, mock.Server.URL)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)

	store := newTestStore()
	if _, err := store.Store(endpointKey, &qauth.Credential{
		CreatedAt:      time.Now().Add(-48 * time.Hour),
		AccessToken:    "stale-token",
		ExpiresAt:      time.Now().Add(-time.Hour),
		OrganizationID: logoutOrgID,
		TokenName:      logoutTokenName,
	}); err != nil {
		t.Fatalf("store: %v", err)
	}

	stdout, stderr, err := runLogoutCmd(t, "--all")
	if err == nil {
		t.Fatalf("an expired credential cannot authenticate a revocation; stdout: %q stderr: %q", stdout, stderr)
	}
	if reqs := mock.Requests(); len(reqs) != 0 {
		t.Errorf("requests = %v, want none -- a token the server will reject is not worth sending", reqs)
	}
	if !strings.Contains(err.Error(), "token expired") {
		t.Errorf("error = %q, want it to name the expiry so the remedy is log in again, then retry", err)
	}
	// Unlike a plain logout, --all does NOT forget an expired credential: the
	// user's other sessions are still alive, and the whole point of the command
	// is to end them. Deleting here would remove the only thing a fresh login
	// replaces in place.
	if credentialGoneFor(t, endpointKey, logoutOrgID) {
		t.Error("the expired credential was deleted; --all revoked nothing, so there was nothing to forget")
	}
}
