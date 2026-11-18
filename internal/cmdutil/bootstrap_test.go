package cmdutil

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/auth"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
	"github.com/zalando/go-keyring"
)

const bootstrapTestOrgID = "550e8400-e29b-41d4-a716-446655440000"

// newBootstrapTestCmd builds the minimum cobra command Bootstrap reads from.
// A standalone command is its own Root(), so the two flags Bootstrap fetches
// via cmd.Root().PersistentFlags() are registered as persistent here, and the
// two it fetches via cmd.Flags() are registered as local. Bootstrap makes no
// network call, so no mock server is needed.
func newBootstrapTestCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "bootstrap-test"}
	cmd.PersistentFlags().StringP("org", "O", "", "")
	cmd.PersistentFlags().CountP("verbose", "v", "")
	cmd.Flags().String("server", "", "")
	cmd.Flags().Bool("allow-insecure-http", false, "")
	return cmd
}

// The message 25 command tests assert on, and the only case where telling the
// user to log in is correct advice.
func TestBootstrapMissingCredentialIsNotLoggedIn(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	env.SetupConfigWithOrg("http://localhost:8000", "", bootstrapTestOrgID)

	_, err := Bootstrap(newBootstrapTestCmd())
	if err == nil {
		t.Fatal("expected an error when no credential is stored")
	}
	if !strings.Contains(err.Error(), "not logged in") {
		t.Errorf("error = %q, want it to say 'not logged in'", err)
	}
	if !strings.Contains(err.Error(), "qctl auth login") {
		t.Errorf("error = %q, want it to advise 'qctl auth login'", err)
	}
	// %w, not %v: the sentinel must survive so callers can branch on it and so
	// GetExitCode can still walk the chain.
	if !errors.Is(err, auth.ErrCredentialNotFound) {
		t.Error("the ErrCredentialNotFound sentinel did not survive wrapping")
	}
}

// A credentials file that exists but cannot be parsed is a different failure,
// and the "log in again" advice is actively harmful for it: login overwrites
// the file, destroying the token_name that is qctl's only handle on a CLI
// session which may still be live on the server.
func TestBootstrapUnreadableCredentialsFileIsNotReportedAsLoggedOut(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	env.SetupConfigWithOrg("http://localhost:8000", "", bootstrapTestOrgID)
	env.CreateFile(
		filepath.Join(config.ConfigDirName, auth.CredentialsFileName),
		"{ this is not json")

	_, err := Bootstrap(newBootstrapTestCmd())
	if err == nil {
		t.Fatal("expected an error from an unparseable credentials file")
	}
	if errors.Is(err, auth.ErrCredentialNotFound) {
		t.Error("an unparseable credentials file was reported as a missing credential")
	}
	if strings.Contains(err.Error(), "not logged in") {
		t.Errorf("error = %q, but the user may well still be logged in", err)
	}
	// The whole point of the ticket: do not send the user to the one command
	// that destroys the file they need to inspect.
	if strings.Contains(err.Error(), "qctl auth login") &&
		!strings.Contains(err.Error(), "Do NOT run 'qctl auth login'") {
		t.Errorf("error = %q, but it must not advise logging in again", err)
	}
	// %w kept the cause readable, so the user learns *why* it failed.
	if !strings.Contains(err.Error(), "failed to unmarshal credentials file") {
		t.Errorf("error = %q, want the underlying cause preserved by %%w", err)
	}
	// Actionable: name the file to go and look at.
	credPath, pathErr := auth.CredentialsFilePath()
	if pathErr != nil {
		t.Fatalf("CredentialsFilePath() error = %v", pathErr)
	}
	if !strings.Contains(err.Error(), credPath) {
		t.Errorf("error = %q, want it to name %s", err, credPath)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitGenericError {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitGenericError)
	}
	// NewTestEnv forces QCTL_ALLOW_PLAINTEXT_TOKENS=1, so Retrieve read the
	// file and never asked the keyring. Offering the keyring entry here would
	// send the user to inspect a store qctl did not consult -- the same
	// dropped-condition shape this branch exists to remove. Asserting on the
	// service name alone would pass vacuously: "qctl" is inside the path
	// ~/.qctl/credentials.json that the message legitimately names.
	if strings.Contains(err.Error(), "OS keyring") {
		t.Errorf("error = %q, but the plaintext opt-in meant the keyring was never consulted", err)
	}
}

// TestUnreadableCredentialError_OmitsTheKeyringWhenItWasNeverConsulted is the
// unit half of the assertion above. Under the plaintext opt-in Retrieve reads
// the file and nothing else, so the failure is necessarily the file's.
func TestUnreadableCredentialError_OmitsTheKeyringWhenItWasNeverConsulted(t *testing.T) {
	// auth.CredentialsFilePath() below reads $HOME. Without this the assertion
	// runs against the developer's real ~/.qctl -- read-only, but the only
	// non-hermetic test in this file, and a t.Fatal in any environment with no
	// HOME set.
	t.Setenv("HOME", t.TempDir())

	credKey := auth.MakeCredentialKey("api.example.com", bootstrapTestOrgID)
	got := unreadableCredentialError(errors.New("{ this is not json"), credKey, false).Error()

	if strings.Contains(got, "OS keyring") || strings.Contains(got, credKey) {
		t.Errorf("error = %q, want no keyring advice when the keyring was never consulted", got)
	}
	// The parts that must survive the trim: what failed, where to look, and
	// the warning that re-running login destroys it.
	credPath, err := auth.CredentialsFilePath()
	if err != nil {
		t.Fatalf("CredentialsFilePath() error = %v", err)
	}
	for _, want := range []string{
		"This is not the same as being logged out.",
		credPath,
		"Do NOT run 'qctl auth login'",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("error = %q, want it to keep %q", got, want)
		}
	}
}

// TestUnreadableCredentialError_NamesTheFileASymlinkPointsAt: "Inspect <path>"
// has to name the file the credential is in, which through a symlinked
// credentials.json is the link's target.
func TestUnreadableCredentialError_NamesTheFileASymlinkPointsAt(t *testing.T) {
	testutil.NewTestEnv(t)
	location := testutil.SymlinkCredentialsFile(t, "{ this is not json")

	credKey := auth.MakeCredentialKey("api.example.com", bootstrapTestOrgID)
	got := unreadableCredentialError(errors.New("{ this is not json"), credKey, false).Error()

	if want := "Inspect " + location + "."; !strings.Contains(got, want) {
		t.Errorf("error = %q\nwant it to contain %q", got, want)
	}
}

// TestUnreadableCredentialError_NamesTheEntryNotTheWholeService: the keyring
// service "qctl" holds one entry per server+org, so pointing the user at "the
// qctl entry" names every credential they have, not the one that failed.
// The wording also must not assert that an entry is there: this error is
// reached when the keyring REFUSED, which establishes nothing about what it
// holds.
func TestUnreadableCredentialError_NamesTheEntryNotTheWholeService(t *testing.T) {
	credKey := auth.MakeCredentialKey("api.example.com", bootstrapTestOrgID)
	got := unreadableCredentialError(errors.New("prompt dismissed"), credKey, true).Error()

	if !strings.Contains(got, credKey) {
		t.Errorf("error = %q, want it to name the entry %q", got, credKey)
	}
	if strings.Contains(got, `the "qctl" entry`) {
		t.Errorf("error = %q, must not call the service an entry: inspecting or removing it means every server's and org's credential", got)
	}
}

// TestBootstrapKeyringRefusedIsNotReportedAsLoggedOut is the end-to-end half
// of the branch above: not a hand-built error, but the message a real command
// hands its user when the OS keyring is up, refuses the read, and the fallback
// file holds nothing.
//
// No other test in this package could reach it. testutil.NewTestEnv forces
// QCTL_ALLOW_PLAINTEXT_TOKENS=1, and under that opt-in the READ goes straight
// to the file without consulting the keyring, so every existing bootstrap test
// runs the file-only branch.
//
// Mutation this pins: deleting the `if !errors.Is(err,
// auth.ErrCredentialNotFound)` arm in BootstrapFromConfig, so a refusing
// keyring is reported as "not logged in" with the advice to run
// 'qctl auth login' -- which overwrites credentials.json and with it the
// token_name that 'auth logout' needs to revoke a session that may still be
// live on the server.
func TestBootstrapKeyringRefusedIsNotReportedAsLoggedOut(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	env.SetupConfigWithOrg("http://localhost:8000", "", bootstrapTestOrgID)

	// Clear the forced plaintext opt-in so the keyring path actually runs.
	defer env.SetEnv("QCTL_ALLOW_PLAINTEXT_TOKENS", "")()

	endpointKey, err := config.NormalizeEndpointKey("http://localhost:8000")
	if err != nil {
		t.Fatalf("NormalizeEndpointKey() error = %v", err)
	}
	credKey := auth.MakeCredentialKey(endpointKey, bootstrapTestOrgID)

	// A keyring that is up and refuses this one entry. Every other read --
	// including the store's own reachability probe -- answers "no such entry",
	// which is what proves the keyring is running rather than absent.
	refused := errors.New("org.freedesktop.DBus.Error.AccessDenied")
	testutil.StubKeyring(t, testutil.KeyringStub{
		Get: func(_, user string) (string, error) {
			if user == credKey {
				return "", refused
			}
			return "", keyring.ErrNotFound
		},
	})
	// No credentials.json at all: the fallback genuinely holds nothing, so the
	// only thing standing between the user and "not logged in" is the keyring
	// classification under test.

	_, err = Bootstrap(newBootstrapTestCmd())
	if err == nil {
		t.Fatal("expected an error when the keyring refuses and no fallback credential exists")
	}
	got := err.Error()

	if errors.Is(err, auth.ErrCredentialNotFound) {
		t.Error("a refusing keyring was reported as a missing credential")
	}
	if strings.Contains(got, "not logged in") {
		t.Errorf("error = %q, but a keyring that refused established nothing about being logged in", got)
	}
	if !strings.Contains(got, "This is not the same as being logged out.") {
		t.Errorf("error = %q, want unreadableCredentialError's leading sentence", got)
	}
	if !strings.Contains(got, "Do NOT run 'qctl auth login'") {
		t.Errorf("error = %q, want the warning against re-running login -- it overwrites the token_name logout needs", got)
	}
	// The cause survived %w, so the user learns the keyring is what refused.
	if !strings.Contains(got, "the OS keyring refused to return the credential") {
		t.Errorf("error = %q, want the keyring cause preserved", got)
	}
	if !errors.Is(err, refused) {
		t.Errorf("error = %q, want the raw keyring failure still reachable via errors.Is", got)
	}
	// It names the one entry to inspect, not the service that holds them all.
	if !strings.Contains(got, credKey) {
		t.Errorf("error = %q, want it to name the entry %q", got, credKey)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitGenericError {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitGenericError)
	}
}

// Every 403 and 404 names the organization it was scoped to. That is only
// true if the resolution funnel records it, so this pins the recording rather
// than the note -- the note itself is internal/apierror's test.
//
// Three success paths reach this: a UUID found in the cache, a UUID absent
// from it, and a name resolved through org.Resolver. All three must record.
func TestResolveOrganizationRecordsTheScope(t *testing.T) {
	const uncachedID = "11111111-2222-3333-4444-555555555555"
	cfg := &config.Config{
		APIVersion:     config.APIVersion,
		CurrentContext: "default",
		Contexts: map[string]*config.Context{
			"default": {
				Server:       "http://localhost:8000",
				Organization: bootstrapTestOrgID,
				Organizations: []config.OrganizationRef{
					{ID: bootstrapTestOrgID, Name: "Qluster-dev"},
				},
			},
		},
	}

	tests := []struct {
		name       string
		orgFlag    string
		envOrg     string
		wantInNote []string
	}{
		{
			name:       "context default, name known from the cache",
			wantInNote: []string{"Qluster-dev", "(from the current context)"},
		},
		{
			name:       "--org by name",
			orgFlag:    "Qluster-dev",
			wantInNote: []string{"Qluster-dev", "(from the --org flag)"},
		},
		{
			name:       "QCTL_ORG by UUID",
			envOrg:     bootstrapTestOrgID,
			wantInNote: []string{"Qluster-dev", "(from QCTL_ORG)"},
		},
		{
			// A valid UUID absent from the cache resolves with an empty
			// NAME. "Scoped to organization  (from ...)" reads as a bug, so
			// the UUID is what the note shows.
			name:       "--org by a UUID that is not cached",
			orgFlag:    uncachedID,
			wantInNote: []string{uncachedID, "(from the --org flag)"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("QCTL_ORG", tt.envOrg)
			apierror.SetOrgScope("", "")
			t.Cleanup(func() { apierror.SetOrgScope("", "") })

			if _, _, err := resolveOrganization(cfg, tt.orgFlag); err != nil {
				t.Fatalf("resolveOrganization() error = %v", err)
			}

			note := apierror.ScopeNote()
			if note == "" {
				t.Fatal("resolveOrganization succeeded but recorded no scope")
			}
			for _, want := range tt.wantInNote {
				if !strings.Contains(note, want) {
					t.Errorf("note = %q, want it to contain %q", note, want)
				}
			}
		})
	}
}

func TestResolveOrganizationUsesCurrentContextNameWhenCacheMisses(t *testing.T) {
	const otherOrgID = "11111111-2222-3333-4444-555555555555"
	t.Setenv("QCTL_ORG", "")
	apierror.SetOrgScope("", "")
	t.Cleanup(func() { apierror.SetOrgScope("", "") })

	cfg := &config.Config{
		APIVersion:     config.APIVersion,
		CurrentContext: "default",
		Contexts: map[string]*config.Context{
			"default": {
				Organization:     bootstrapTestOrgID,
				OrganizationName: "Acme Corp",
			},
		},
	}

	for _, tt := range []struct {
		name       string
		orgFlag    string
		cachedOrgs []config.OrganizationRef
		wantID     string
		wantName   string
	}{
		{name: "current context", wantID: bootstrapTestOrgID, wantName: "Acme Corp"},
		{name: "explicit matching UUID", orgFlag: bootstrapTestOrgID, wantID: bootstrapTestOrgID, wantName: "Acme Corp"},
		{name: "blank cached name", cachedOrgs: []config.OrganizationRef{{ID: bootstrapTestOrgID}}, wantID: bootstrapTestOrgID, wantName: "Acme Corp"},
		{name: "named cache takes precedence", cachedOrgs: []config.OrganizationRef{{ID: bootstrapTestOrgID, Name: "Acme Updated"}}, wantID: bootstrapTestOrgID, wantName: "Acme Updated"},
		{name: "explicit other UUID", orgFlag: otherOrgID, wantID: otherOrgID},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg.Contexts["default"].Organizations = tt.cachedOrgs
			gotID, gotName, err := resolveOrganization(cfg, tt.orgFlag)
			if err != nil {
				t.Fatalf("resolveOrganization() error = %v", err)
			}
			if gotID != tt.wantID || gotName != tt.wantName {
				t.Errorf("resolveOrganization() = (%q, %q), want (%q, %q)", gotID, gotName, tt.wantID, tt.wantName)
			}
		})
	}
}

// A failed resolution has no organization to name, so resolveOrganization's
// error branch does not call SetOrgScope.
//
// What this test pins is that branch RECORDING something: it clears the scope
// itself before calling, so it would catch a stray SetOrgScope on the failure
// path. It cannot see the STALE case -- a scope left over from an earlier
// resolution in the same process -- because that pre-clear removes it.
//
// resolveOrganization does run more than once in a process (`qctl apply -f`
// resolves per document); what keeps the stale case harmless is the argument
// in resolveOrganization's own comment, about repeat calls reading the same
// inputs. Nothing here tests that.
func TestResolveOrganizationRecordsNothingOnFailure(t *testing.T) {
	t.Setenv("QCTL_ORG", "")
	apierror.SetOrgScope("", "")
	t.Cleanup(func() { apierror.SetOrgScope("", "") })

	cfg := &config.Config{APIVersion: config.APIVersion}
	if _, _, err := resolveOrganization(cfg, ""); err == nil {
		t.Fatal("expected an error with no context configured")
	}
	if note := apierror.ScopeNote(); note != "" {
		t.Errorf("ScopeNote() = %q, want \"\" after a failed resolution", note)
	}
}
