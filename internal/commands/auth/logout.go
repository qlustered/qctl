package auth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/qlustered/qctl/internal/auth"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/spf13/cobra"
)

// NewLogoutCommand creates the auth logout command
func NewLogoutCommand() *cobra.Command {
	var localOnly bool
	var allSessions bool

	cmd := &cobra.Command{
		Use:   "logout",
		Short: "Revoke this CLI session and remove stored credentials",
		Long: `Logout from Qluster: revoke this CLI session on the server, then remove
the stored credential.

Revoking is what actually ends the session. The bearer token stays valid on
the server until it expires, so deleting the local copy on its own does not
shorten its life.

Two cases cannot be revoked, and qctl says so on stderr rather than implying
the session ended:

  * a credential stored before qctl named its CLI sessions, which cannot be
    matched to a session on the server -- log in again to replace it
  * --local, which skips the server call on purpose

A third case never reaches the revoke at all. If the OS keyring is up but
refuses to hand the credential back -- while still answering reads of other
entries, which is how qctl tells that from a keyring that is simply absent --
and no fallback file holds one either, logout stops there and exits non-zero
rather than reporting nothing to do. --local does not bypass it, because that
read happens before the server call. The case does not arise under
QCTL_ALLOW_PLAINTEXT_TOKENS: logout still reads the keyring entry there, but a
keyring that refuses counts as holding nothing, so there is no refusal to
report. An entry an earlier keyring login left is still read, has its session
revoked like any other stored credential, and has its removal attempted --
reported if it demonstrably survived -- and on a desktop each of those keyring
accesses can raise an unlock prompt.

The OS keyring and the fallback file can hold two different credentials for
the same server and organization -- a login with
--allow-plaintext-token-store after an earlier keyring login leaves exactly
that -- and each is its own session on the server. Logout revokes each one with
its own token and reports each outcome. A credential whose revoke fails is
kept, so a retry can still reach it, while the other is removed.

If the server cannot be reached, the credential is kept so you can retry.

Deleting the local copy is reported separately: any copy that could not be
removed is named -- the keyring entry, the fallback file, or both -- together
with what to do about it by hand, and logout exits non-zero.

--all is a different operation. It ends EVERY session and bearer credential
this user holds on the server -- every browser session, every CLI login on
every machine, every API token, in every organization -- and then removes the
local copies for each organization qctl can name on that server. This is the
step to take after a password or MFA reset: the server is never told that a
credential changed at the identity provider, so nothing else ends the sessions
somebody else may be holding. It revokes the token it is called with, so it
needs one: --all cannot be combined with --local, and a credential that has
already expired is refused before the call, since it can no longer authenticate
the revocation.

qctl reports the delegation and API-token counts the server returns, never a
local guess. If the server refuses the credential (401) or the caller (403) it
says so, removes the local copies anyway -- they are worthless either way --
and points at the web app's "Sign out of all devices"; if the server cannot be
reached, nothing is deleted, so you can retry.`,
		Annotations: cmdutil.WithFormats(catalog.For("session"), "table"),
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if allSessions {
				if localOnly {
					return errAllWithLocal
				}
				return runLogoutAll(cmd)
			}
			return runLogout(cmd, localOnly)
		},
	}

	cmd.Flags().BoolVar(&localOnly, "local", false,
		"Only remove the stored credential; do not contact the server")
	cmd.Flags().BoolVar(&allSessions, "all", false,
		"Revoke every session and CLI token for this user on the server, in every organization")

	return cmd
}

// errAllWithLocal rejects the one flag pair here that cannot mean anything.
//
// It is a hand-written NewBadUsage rather than cmdutil.ValidateConflictingFlags
// because the shared message ("cannot use both --all and --local flags") says
// the pair is refused without saying why, and the why is the entire difference
// between these two flags: one is a server-side revocation, the other exists
// precisely so that nothing is sent to the server. The exit code is the same 2
// either way, which is what the test asserts on.
var errAllWithLocal = qerrors.NewBadUsage(
	"--all revokes on the server; it cannot be combined with --local.")

func runLogout(cmd *cobra.Command, localOnly bool) error {
	// Write through cobra's writers, not os.Stdout/os.Stderr. They default to
	// exactly those when unset, so the CLI is unchanged -- but it makes the
	// stderr warning below (the security-relevant half of this command's
	// contract) something a test can actually assert on.
	stdout, stderr := cmd.OutOrStdout(), cmd.ErrOrStderr()

	cmdCtx, err := cmdutil.BootstrapWithoutAuth(cmd)
	if err != nil {
		return err
	}

	// Resolve the org the way every other command does. Logout used to key the
	// credential store on the raw --org string, so an org NAME matched nothing
	// and still printed success.
	orgFlag, _ := cmd.Root().PersistentFlags().GetString("org")
	orgID, _, err := cmdutil.ResolveOrganization(cmdCtx.Config, orgFlag)
	if err != nil {
		return err
	}

	endpointKey, err := config.NormalizeEndpointKey(cmdCtx.ServerURL)
	if err != nil {
		return fmt.Errorf("failed to normalize endpoint: %w", err)
	}

	// Opt into plaintext-only storage via env; the keyring and file fallback
	// are both cleaned up regardless.
	credStore := auth.NewCredentialStore(auth.IsPlaintextAllowed())
	credKey := auth.MakeCredentialKey(endpointKey, orgID)

	// Every copy Delete would remove is read first. The keyring and the file
	// can hold two different logins' tokens, each with its own live session,
	// and revoking only the one Retrieve picks would orphan the other.
	copies, retrieveErr := credStore.RetrieveCopies(endpointKey, orgID)
	if retrieveErr != nil {
		// Only a genuinely absent credential is "nothing to do". Any other
		// failure -- an unreadable or unparseable credentials file -- must not
		// be swallowed: doing so skips the revoke and still reports success,
		// leaving a live token on the server.
		if !errors.Is(retrieveErr, auth.ErrCredentialNotFound) {
			return fmt.Errorf("failed to read the stored credential: %w", retrieveErr)
		}

		// Nothing stored: the delete is a no-op and there is nothing to end.
		if err := credStore.Delete(endpointKey, orgID); err != nil {
			return deleteFailedError(err, false, credKey)
		}
		fmt.Fprintf(stdout, "No stored credential for organization %s; nothing to do.\n", orgID)
		return nil
	}

	current := endCopySession(cmd.Context(), cmdCtx, orgID, copies[0], "", localOnly)
	var other *copyOutcome
	if len(copies) > 1 {
		o := endCopySession(cmd.Context(), cmdCtx, orgID, copies[1], copyLocation(copies[1]), localOnly)
		other = &o
	}

	deleteErr := deleteEndedCopies(credStore, endpointKey, orgID, current, other)
	return reportLogout(stdout, stderr, orgID, credKey, current, other, deleteErr)
}

// deleteEndedCopies deletes the stored copies whose session ended.
//
// A copy whose revoke failed is deliberately kept: either it is the only
// thing that can authenticate a retry, or the retry is hopeless and the token
// is still live on the server, where forgetting it silently would be an untrue
// reassurance. So once any revoke has failed, only the copy whose session
// ended is deleted, from the halves that hold it.
func deleteEndedCopies(store *auth.CredentialStore, endpointKey, orgID string, current copyOutcome, other *copyOutcome) error {
	otherEnded := other == nil || other.err == nil
	switch {
	case current.err == nil && otherEnded:
		return store.Delete(endpointKey, orgID)
	case current.err == nil:
		return deleteCopy(store, endpointKey, orgID, current.copy)
	case other != nil && otherEnded:
		return deleteCopy(store, endpointKey, orgID, other.copy)
	}
	return nil
}

// reportLogout prints what logout did with each stored copy and returns the
// command's error.
func reportLogout(
	stdout, stderr io.Writer,
	orgID, credKey string,
	current copyOutcome,
	other *copyOutcome,
	deleteErr error,
) error {
	switch {
	case current.err != nil:
		// The current copy was kept. The other copy's outcome already
		// happened, so it is reported alongside.
		err := keptCredentialError(current.err)
		if other == nil {
			return err
		}
		other.report(stdout, stderr)
		if other.err != nil {
			return fmt.Errorf("%w\n\nThe other CLI session %q, whose credential was found in %s, could not be revoked either: %w\n%s",
				err, other.copy.Credential.TokenName, other.where, other.err, "Its credential was kept too.")
		}
		if deleteErr != nil {
			return strandedCopyError(err, deleteErr, other.copy.InKeyring, "the other credential", credKey)
		}
		return err

	case other != nil && other.err != nil:
		current.report(stdout, stderr)
		err := other.keptError()
		if deleteErr != nil {
			return strandedCopyError(err, deleteErr, current.copy.InKeyring, "this session's credential", credKey)
		}
		return err

	case deleteErr != nil:
		if other != nil {
			other.report(stdout, stderr)
		}
		return deleteFailedError(deleteErr, current.revoked, credKey)
	}

	current.report(stdout, stderr)
	if other != nil {
		other.report(stdout, stderr)
	}
	fmt.Fprintf(stdout, "Successfully logged out from organization %s\n", orgID)
	return nil
}

// copyOutcome is what logout did with one stored copy of the credential.
// where is empty for the first copy RetrieveCopies returns, and names the
// second copy's location otherwise.
type copyOutcome struct {
	copy    auth.StoredCopy
	where   string
	revoked bool
	note    string
	err     error
}

// endCopySession runs endSessionOnServer for one stored copy.
func endCopySession(
	ctx context.Context,
	cmdCtx *cmdutil.CommandContext,
	orgID string,
	c auth.StoredCopy,
	where string,
	localOnly bool,
) copyOutcome {
	o := copyOutcome{copy: c, where: where}
	o.revoked, o.note, o.err = endSessionOnServer(ctx, cmdCtx, orgID, c.Credential, localOnly, where)
	return o
}

// deleteCopy removes c from the halves that hold it and leaves the other half
// alone.
func deleteCopy(store *auth.CredentialStore, endpointKey, orgID string, c auth.StoredCopy) error {
	var errs []error
	if c.InKeyring {
		errs = append(errs, store.DeleteKeyringCopy(endpointKey, orgID))
	}
	if c.InFile {
		errs = append(errs, store.DeleteFileCopy(endpointKey, orgID))
	}
	return errors.Join(errs...)
}

// report prints what ending this copy's session did. It prints nothing for a
// copy whose revoke failed.
func (o copyOutcome) report(stdout, stderr io.Writer) {
	if o.err != nil {
		return
	}
	if o.revoked {
		if o.where == "" {
			fmt.Fprintln(stdout, "Revoked this CLI session on the server.")
		} else {
			fmt.Fprintf(stdout, "Revoked the other CLI session %q on the server, whose credential was found in %s.\n",
				o.copy.Credential.TokenName, o.where)
		}
	}
	if o.note != "" {
		fmt.Fprintln(stderr, o.note)
	}
}

// keptError reports the other copy's failed revoke.
func (o copyOutcome) keptError() error {
	remedy := "Its credential was kept so you can retry with 'qctl auth logout'.\n" +
		"To forget it anyway without revoking, run 'qctl auth logout --local'."
	if errors.Is(o.err, auth.ErrDelegationForbidden) {
		remedy = "Retrying will not help: the server refuses this request every time.\n" +
			"Its credential was kept because the token is still valid on the server.\n" +
			"To forget it locally anyway, run 'qctl auth logout --local'."
	}
	return fmt.Errorf("could not revoke the other CLI session %q, whose credential was found in %s: %w\n\n%s",
		o.copy.Credential.TokenName, o.where, o.err, remedy)
}

// strandedCopyError appends to err a copy whose session ended but which could
// not be removed from the one half of the store that held it. subject names
// the copy.
func strandedCopyError(err, deleteErr error, inKeyring bool, subject, credKey string) error {
	if inKeyring {
		return fmt.Errorf("%w\n\nqctl also could not remove %s from the OS keyring: %w\n%s",
			err, subject, deleteErr, keyringEntryRemedy(credKey))
	}
	return fmt.Errorf("%w\n\nqctl also could not clear %s from %s: %w\n%s",
		err, subject, auth.CredentialsFileLocation(), deleteErr, "Inspect it by hand.")
}

// copyLocation names where a copy that is not the current one was found.
func copyLocation(c auth.StoredCopy) string {
	if c.InKeyring {
		return "the OS keyring"
	}
	return auth.CredentialsFileLocation()
}

// endSessionOnServer ends the session behind one stored credential on the
// server. A non-empty where names where a second stored credential was found,
// and makes the note describe it as the other one.
//
// It returns (revoked, note, err). A non-empty note is a warning to print on
// stderr when the session could NOT be ended but forgetting it locally is still
// the right outcome. A non-nil error means the caller must KEEP the credential:
// either a retry can still succeed and the stored token is the only thing that
// can authenticate it, or the server refuses permanently and the token is still
// live, so forgetting it would be an untrue reassurance. The error is
// revokeSessions' own; keptCredentialError adds the recovery.
//
// The route named `logout` is not the one called here, and cannot be: a bearer
// caller reaches it only with all_sessions=true, which is the whole-account
// revocation --all performs, and is refused with HTTP 400 for the single
// session this function ends (capi/capi/views/user_view.py). A bearer
// credential names no cookie session, so there is nothing there for it to end.
// The delegation revoke is the bearer-callable equivalent, and the backend
// documents it as revoking every CLI token from one device/session.
//
// Before atlas-l14.34 the reason was different and broader -- the route
// authenticated on the browser's oauth_session cookie and enforced CSRF, so no
// bearer caller could reach it at all. That is no longer true, and a reader who
// takes it as still true would conclude, wrongly, that qctl cannot revoke
// everything from the CLI.
//
// The match-and-revoke loop lives in revokeSessions (sessions.go), shared with
// `auth sessions revoke` and with the replacement a second login performs; the
// error ladder below is what is particular to logout.
func endSessionOnServer(
	ctx context.Context,
	cmdCtx *cmdutil.CommandContext,
	orgID string,
	cred *auth.Credential,
	localOnly bool,
	where string,
) (bool, string, error) {
	subject := "the access token"
	unnamedRemedy := "Run 'qctl auth login' to replace it with a revocable one."
	if where != "" {
		subject = "the other access token, found in " + where + ","
		unnamedRemedy = "After logging in again, find its unnamed session with 'qctl auth sessions' and revoke it by ID."
	}
	stillValid := fmt.Sprintf(
		"Warning: %s was NOT revoked and stays valid until %s.\nAnyone holding a copy can keep using it until then.",
		subject, cred.ExpiresAt.Format(time.RFC3339))
	if cred.ExpiresAt.IsZero() {
		// No expiry was recorded -- a hand-edited or partially-written
		// credentials.json. IsExpired() reports false for it (bootstrap relies
		// on that), so it reaches the arms below, where formatting the zero
		// time would present 0001-01-01T00:00:00Z as a real deadline.
		stillValid = fmt.Sprintf("Warning: %s was NOT revoked, and no expiry was recorded locally, so qctl cannot say when it stops working.\nAnyone holding a copy can keep using it until the server expires it.", subject)
	}

	switch {
	case cred.IsExpired():
		// Expiry already ended it; there is nothing left to revoke. This case
		// outranks the two below because their note promises the token "stays
		// valid until" an expiry that has already passed.
		return false, "", nil
	case localOnly:
		return false, stillValid + "\n(--local was passed, so qctl did not contact the server.)", nil
	case cred.TokenName == "":
		return false, stillValid +
			"\nThis credential was stored before qctl named its CLI sessions, so it cannot tell the server which session to end.\n" +
			unnamedRemedy, nil
	}

	authClient := auth.NewClient(cmdCtx.ServerURL, cmdCtx.Verbosity)
	_, revoked, err := revokeSessions(ctx, authClient, cred.AccessToken, orgID, cred.TokenName)
	if errors.Is(err, auth.ErrTokenRejected) {
		// The server already refuses this token — the session is over.
		return revoked > 0, "", nil
	}
	if err != nil {
		return false, "", err
	}

	return revoked > 0, "", nil
}

// keptCredentialError adds to a failed revoke of the current credential the
// recovery the user has, which depends on whether a retry can succeed.
func keptCredentialError(err error) error {
	if errors.Is(err, auth.ErrDelegationForbidden) {
		return terminalRevokeError(err)
	}
	return keepCredentialError(err)
}

// keepCredentialError wraps a transport/server failure with the recovery the
// user has. %w keeps the Unwrap chain intact so the exit code survives.
func keepCredentialError(err error) error {
	return fmt.Errorf("%w\n\n%s", err,
		"The stored credential was kept so you can retry.\nTo forget it anyway without revoking, run 'qctl auth logout --local'.")
}

// terminalRevokeError reports a delegation call -- the list or the revoke --
// that failed permanently: retrying sends the identical request and gets the
// identical answer. The credential is still kept -- the token is live on the
// server, and forgetting it silently is the untrue reassurance this command
// exists to avoid -- but the wording points at --local rather than at a retry.
func terminalRevokeError(err error) error {
	return fmt.Errorf("%w\n\n%s", err,
		"Retrying will not help: the server refuses this request every time.\nThe stored credential was kept because the token is still valid on the server.\nTo forget it locally anyway, run 'qctl auth logout --local'.")
}

// deleteFailedError reports a logout whose server-side half finished but whose
// local half did not. It deliberately does not suggest a retry: when revoked is
// true the session is already over, so re-running the command would revoke
// nothing and fail the same way. The remaining action is a manual one, which is
// why the location is named. CredentialsFileLocation is exported for exactly
// this kind of report.
func deleteFailedError(err error, revoked bool, credKey string) error {
	lead, remedy := describeFailedDelete(err, revoked, credKey)
	return fmt.Errorf("%s: %w\n\n%s", lead, err, remedy)
}

// describeFailedDelete turns Delete's report of which halves it cleared into
// the two pieces of prose the user needs: what happened, and what is left to do
// by hand.
//
// Without the halves there is only one thing to say, and in keyring mode it is
// usually wrong. An unparseable credentials.json is not evidence that
// the token is in it -- yet a single message has to claim the whole delete
// failed and point at that file. It stays an error rather than a warning
// because qctl cannot prove a file it could not parse holds no copy; what
// changes is that the message now says which copy is provably gone.
func describeFailedDelete(err error, revoked bool, credKey string) (lead, remedy string) {
	pathLabel := auth.CredentialsFileLocation()
	fileRemedy := "Remove it by hand: " + pathLabel
	// Name the ENTRY, not the service. Every credential qctl has ever stored --
	// every server, every org -- is a separate entry under the one service
	// "qctl", so "remove the qctl entry", followed literally, wipes all of them,
	// and contradicts this command's own promise to touch only the current
	// server and org.
	keyringRemedy := keyringEntryRemedy(credKey)

	// What went right, in the order it happened, and the one thing that did not.
	var done []string
	if revoked {
		done = append(done, "revoked this CLI session on the server")
	}
	failed := "failed to delete the stored credential"
	remedy = fileRemedy

	var de *auth.DeleteError
	if errors.As(err, &de) {
		switch {
		case de.KeyringCleared && !de.FileCleared:
			// Say what KeyringCleared actually means. It is `keyringErr == nil`,
			// and deleteFromKeyring returns nil in four situations -- the entry
			// was removed, there was none, the platform has no keyring, or the
			// keyring cannot be consulted -- so "removed the credential from the
			// OS keyring" is true of the first only, and is a claim about a
			// keyring that may not exist on a headless box.
			//
			// "FOUND no copy" rather than "left no copy" because the fourth
			// situation defeats that one too: on a locked collection the delete
			// fails, the read-back that would prove the entry survived fails as
			// well, and the token may still be sitting in the keyring. This is
			// the hedge KeyringCleared's own doc comment carries -- "as far as
			// qctl can tell" -- said in the message that reads the field.
			done = append(done, "found no copy of the credential left in the OS keyring")
			failed = "could not clear the fallback credential store"
			// "could not clear", not "could not read": deleteFromFile fails on
			// write paths too (os.Remove when the map empties, and
			// writeCredentialsFile), where the read succeeded and naming it as
			// the failure is a wrong diagnosis.
			remedy = "qctl could not clear " + pathLabel + ", so it cannot confirm no copy of the token is left in it.\nInspect it by hand."
		case !de.KeyringCleared && de.FileCleared:
			failed = "failed to remove the credential from the OS keyring"
			remedy = keyringRemedy
		default:
			remedy = keyringRemedy + "\n" + fileRemedy
		}
	}

	if len(done) == 0 {
		return failed, remedy
	}
	return strings.Join(done, " and ") + ", but " + failed, remedy
}

// keyringEntryRemedy tells the user how to remove one keyring entry by hand.
func keyringEntryRemedy(credKey string) string {
	return fmt.Sprintf(
		"Remove that one entry from your OS keyring: service %q, entry %q.\n"+
			"Every other server and org you have logged into is a separate entry under\n"+
			"the same service, so do not remove the service itself.",
		auth.KeyringServiceName, credKey)
}

// runLogoutAll revokes every session and bearer credential this user holds on
// the server, then removes every local copy the revocation just invalidated.
//
// This is the CLI half of a compensating control: Atlas is never told that a
// credential changed at the identity provider, so a user whose password or MFA
// factor was reset has to end their own sessions on demand. Plain `auth logout`
// cannot do it -- it revokes this machine's delegation and nothing else.
func runLogoutAll(cmd *cobra.Command) error {
	stdout, stderr := cmd.OutOrStdout(), cmd.ErrOrStderr()

	// Bootstrap, not BootstrapWithoutAuth as the single-session path uses:
	// unlike a plain logout, this one has nothing to do without a credential,
	// because the stored bearer token is what authenticates the revocation.
	// Sharing the standard bootstrap also buys the messages a security command
	// most needs to get right -- in particular the one for an unreadable
	// credentials file, which deliberately does NOT tell the user to run
	// `auth login` over the top of it.
	cmdCtx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}

	endpointKey, err := config.NormalizeEndpointKey(cmdCtx.ServerURL)
	if err != nil {
		return fmt.Errorf("failed to normalize endpoint: %w", err)
	}

	credStore := auth.NewCredentialStore(auth.IsPlaintextAllowed())
	authClient := auth.NewClient(cmdCtx.ServerURL, cmdCtx.Verbosity)

	rev, revErr := authClient.RevokeAllSessions(cmd.Context(), cmdCtx.Credential.AccessToken, cmdCtx.OrganizationID)
	if revErr != nil {
		return reportRevokeAllFailure(revErr, stderr, credStore, cmdCtx.Config, endpointKey, cmdCtx.OrganizationID)
	}

	// Printed before the local cleanup, and from the server's counts rather
	// than from anything qctl knows: the sessions this ended are on machines
	// this one has no inventory of. It stays first even when the cleanup below
	// fails, because it already happened.
	fmt.Fprintf(stdout,
		"Revoked every session and token for this user on %s (%d delegations, %d API tokens).\n",
		endpointKey, rev.DelegationsRevoked, rev.APITokensRevoked)

	removed, clearErr := clearStoredCredentials(credStore, cmdCtx.Config, endpointKey, cmdCtx.OrganizationID)
	if clearErr != nil {
		return clearErr
	}

	fmt.Fprintf(stdout, "Removed stored credentials for %d organization(s).\n", removed)
	return nil
}

// reportRevokeAllFailure decides what a failed revoke-all does to the local
// copies, which is the only decision in this command with a wrong answer that
// is silent: deleting them on a failure that a retry could have fixed destroys
// the only credential able to authenticate that retry, and keeping them on a
// failure that already killed them leaves the user believing in tokens the
// server has stopped honouring.
func reportRevokeAllFailure(
	revErr error,
	stderr io.Writer,
	store *auth.CredentialStore,
	cfg *config.Config,
	endpointKey, resolvedOrgID string,
) error {
	var refused *auth.RevokeAllRefusedError
	if errors.As(revErr, &refused) {
		// 401 means the presented token is already dead; 403 means this caller
		// may not revoke here. Either way the stored copies are worthless, so
		// they go. The note is written BEFORE the delete, not after, because a
		// delete that fails returns early -- and the thing the user most needs
		// to know is that their other sessions may still be alive, not that a
		// local file could not be cleared.
		fmt.Fprintf(stderr,
			"Could not confirm the server-side revocation (%d). "+
				"Sign in to the web app and use \"Sign out of all devices\", or log in again and retry.\n",
			refused.StatusCode)
		if _, clearErr := clearStoredCredentials(store, cfg, endpointKey, resolvedOrgID); clearErr != nil {
			return clearErr
		}
		return revErr
	}

	if errors.Is(revErr, auth.ErrRevokeAllRejected) {
		// The server refused the request itself. A retry sends identical bytes
		// and gets the identical answer, and the only thing that could have
		// produced it on this transport is qctl asking for something a bearer
		// credential cannot do -- so the server's detail is passed through
		// without the retry advice the other failures carry.
		return revErr
	}

	// Unreachable, a 5xx, or an answer qctl could not read: nothing was
	// confirmed revoked, so every credential is kept and the wording is the one
	// the single-session path already uses for a retryable failure.
	return keepCredentialError(revErr)
}

// clearStoredCredentials removes the stored credential for every organization
// on this server that qctl can name, and reports how many actually held one.
func clearStoredCredentials(
	store *auth.CredentialStore,
	cfg *config.Config,
	endpointKey, resolvedOrgID string,
) (int, error) {
	// A credentials file qctl cannot read yields no organizations, and that is
	// deliberately not fatal here. The resolved organization is always a
	// candidate, so the delete below reaches the same file and reports it
	// through describeFailedClearAll, which says which copy survived; failing
	// here instead would report it with none of that detail AND would skip the
	// organizations the config does name.
	fileOrgs, _ := store.StoredOrganizations(endpointKey)

	removed := 0
	var failures []clearFailure
	for _, orgID := range candidateOrganizations(cfg, endpointKey, resolvedOrgID, fileOrgs) {
		// Count only what was demonstrably there. Delete is a no-op for an
		// organization that never had a credential -- every organization the
		// config caches is a candidate and most will not have one -- so
		// counting candidates instead would report a number the user cannot
		// square with what they ever logged into.
		_, retrieveErr := store.Retrieve(endpointKey, orgID)
		if delErr := store.Delete(endpointKey, orgID); delErr != nil {
			failures = append(failures, clearFailure{
				credKey: auth.MakeCredentialKey(endpointKey, orgID),
				err:     delErr,
			})
			continue
		}
		if retrieveErr == nil {
			removed++
		}
	}

	return removed, describeFailedClearAll(failures)
}

// candidateOrganizations lists every organization qctl might be holding a
// credential for on one server.
//
// It is a union because no single source is complete. The OS keyring cannot be
// enumerated at all (see CredentialStore.StoredOrganizations), so the
// organizations have to be named from somewhere: the one just resolved, every
// organization cached by a config context pointing at this server -- matched on
// the SERVER, not the context name, since one server is commonly reached
// through several contexts -- and every organization the fallback credentials
// file has an entry for.
//
// A keyring entry for an organization no source names is one qctl cannot see to
// delete. That is why the command reports the number of organizations it
// cleared rather than claiming it cleared them all.
func candidateOrganizations(cfg *config.Config, endpointKey, resolvedOrgID string, fileOrgs []string) []string {
	seen := make(map[string]bool)
	add := func(orgID string) {
		if orgID != "" {
			seen[orgID] = true
		}
	}

	add(resolvedOrgID)
	for _, ctx := range cfg.Contexts {
		if ctx == nil {
			continue
		}
		ctxKey, err := config.NormalizeEndpointKey(ctx.Server)
		if err != nil || ctxKey != endpointKey {
			continue
		}
		add(ctx.Organization)
		for _, org := range ctx.Organizations {
			add(org.ID)
		}
	}
	for _, orgID := range fileOrgs {
		add(orgID)
	}

	out := make([]string, 0, len(seen))
	for orgID := range seen {
		out = append(out, orgID)
	}
	// Sorted so the delete order, and any report of what survived it, is the
	// same on every run: ranging a map would shuffle both.
	sort.Strings(out)
	return out
}

// clearFailure is one organization's credential that survived the cleanup.
type clearFailure struct {
	err     error
	credKey string
}

// describeFailedClearAll reports a --all cleanup that could not remove every
// local copy of a credential the server has already revoked.
//
// It is describeFailedDelete's plural twin rather than a loop over it, because
// the two halves of a failed delete do not scale the same way. A keyring
// failure is per ENTRY, so every organization it hit has to be named; a file
// failure is one shared file, and looping would print the same paragraph about
// ~/.qctl/credentials.json once per organization the config happens to cache.
func describeFailedClearAll(failures []clearFailure) error {
	if len(failures) == 0 {
		return nil
	}

	var keyringEntries []string
	fileFailed := false
	errs := make([]error, 0, len(failures))
	for _, f := range failures {
		errs = append(errs, f.err)

		var de *auth.DeleteError
		if !errors.As(f.err, &de) {
			// No halves reported, so qctl cannot say which copy survived. The
			// file is the one it can still name truthfully.
			fileFailed = true
			continue
		}
		if !de.KeyringCleared {
			keyringEntries = append(keyringEntries, f.credKey)
		}
		if !de.FileCleared {
			fileFailed = true
		}
	}

	var remedies []string
	if len(keyringEntries) > 0 {
		// Name the ENTRIES, never the service: every credential qctl has ever
		// stored is one entry under the single service "qctl", so "remove the
		// qctl entry" followed literally wipes the servers this revocation
		// never touched.
		remedies = append(remedies, fmt.Sprintf(
			"Remove these entries from your OS keyring (service %q):\n  %s\n"+
				"Every other server you have logged into is a separate entry under the same\n"+
				"service, so do not remove the service itself.",
			auth.KeyringServiceName, strings.Join(keyringEntries, "\n  ")))
	}
	if fileFailed {
		pathLabel := auth.CredentialsFileLocation()
		remedies = append(remedies, "qctl could not clear "+pathLabel+
			", so it cannot confirm no copy of a revoked token is left in it.\nInspect it by hand.")
	}

	// %w over the joined causes, so the exit code still rides an unbroken
	// Unwrap chain rather than degrading to 1 through a rebuilt string.
	return fmt.Errorf("the sessions were revoked on the server, but %d stored credential(s) could not be removed: %w\n\n%s",
		len(failures), errors.Join(errs...), strings.Join(remedies, "\n\n"))
}
