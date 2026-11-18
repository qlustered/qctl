package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/qlustered/qctl/internal/apierror"
	qauth "github.com/qlustered/qctl/internal/auth"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/spf13/cobra"
)

// sessionView is one row of the session inventory.
type sessionView struct {
	Current        bool          `json:"current" yaml:"current"`
	Name           string        `json:"name" yaml:"name"`
	Status         sessionStatus `json:"status" yaml:"status"`
	ID             string        `json:"id" yaml:"id"`
	OrganizationID string        `json:"organization_id" yaml:"organization_id"`
	CreatedAt      time.Time     `json:"created_at" yaml:"created_at"`
	ExpiresAt      *time.Time    `json:"expires_at" yaml:"expires_at"`
	FirstUsedAt    *time.Time    `json:"first_used_at" yaml:"first_used_at"`
}

// NewSessionsCommand creates the session inventory and its revoke subcommand.
func NewSessionsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sessions",
		Short: "List active CLI sessions",
		Long: "List the CLI sessions the server holds for the current user and organization.\n\n" +
			"A session is listed until something revokes it, which is not the same as being\n" +
			"usable: the STATUS column reports whether its token can still authenticate\n" +
			"(active), timed out (expired), was revoked outright (revoked), or could not be\n" +
			"classified (unknown), and EXPIRES names the expiry behind that verdict.\n\n" +
			"--never-used lists only sessions whose tokens all expired without a recorded\n" +
			"first use. Fresh tokens are excluded. --columns first_used_at shows the\n" +
			"earliest recorded use; it does not measure recent activity.",
		Args:        cobra.NoArgs,
		Annotations: cmdutil.WithFormats(catalog.For("session"), "table", "json", "yaml", "name"),
		RunE:        runListSessions,
	}
	cmd.Flags().Bool("never-used", false, "List only sessions whose tokens all expired without a recorded first use")
	cmd.AddCommand(newRevokeSessionCommand())
	cmdutil.DeclareColumns(cmd, []sessionView(nil))
	return cmd
}

func runListSessions(cmd *cobra.Command, _ []string) error {
	neverUsed, err := cmd.Flags().GetBool("never-used")
	if err != nil {
		return fmt.Errorf("failed to read --never-used: %w", err)
	}
	cmdCtx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}

	client := qauth.NewClient(cmdCtx.ServerURL, cmdCtx.Verbosity)
	delegations, err := client.ListDelegations(
		cmd.Context(), cmdCtx.Credential.AccessToken, cmdCtx.OrganizationID)
	if errors.Is(err, qauth.ErrTokenRejected) {
		return qerrors.NewUnauthorized("the server rejected the stored credential")
	}
	if err != nil {
		return fmt.Errorf("failed to list CLI sessions: %w", err)
	}

	// The session list is the load-bearing half of this command: a user
	// auditing what can reach their org needs the inventory even when the
	// liveness lookup is the thing that broke. So a failure here degrades the
	// STATUS column rather than failing the command -- every row falls to
	// "unknown" and one line on stderr says why, which is also what tells that
	// unknown apart from a delegation no token row names. The bulk revoke makes
	// the opposite call, and deliberately: see revokeExpiredSessions.
	liveness, livenessErr := fetchSessionLiveness(
		cmd.Context(), client, cmdCtx.Credential.AccessToken, cmdCtx.OrganizationID)
	if livenessErr != nil {
		if neverUsed {
			if errors.Is(livenessErr, qauth.ErrTokenRejected) {
				return qerrors.NewUnauthorized("the server rejected the stored credential")
			}
			return fmt.Errorf("could not identify never-used CLI sessions: %w", livenessErr)
		}
		fmt.Fprintf(cmd.ErrOrStderr(),
			"Warning: could not check which CLI sessions can still authenticate: %v\n"+
				"Every session below is listed as %q; run 'qctl auth sessions' again to retry.\n",
			livenessErr, statusUnknown)
	}

	rows, unknown := sessionRows(delegations, liveness, cmdCtx.Credential.TokenName, neverUsed)
	if unknown > 0 {
		fmt.Fprintf(cmd.ErrOrStderr(), "Warning: omitted %d CLI session(s) whose token usage could not be determined.\n", unknown)
	}

	if !cmd.Flags().Changed("columns") {
		if err := cmd.Flags().Set("columns", "current,name,status,expires_at,created_at,id"); err != nil {
			return fmt.Errorf("failed to set default columns: %w", err)
		}
	}
	printer, err := output.NewPrinterFromCmd(cmd)
	if err != nil {
		return fmt.Errorf("failed to create output printer: %w", err)
	}
	if err := printer.Print(rows); err != nil {
		return fmt.Errorf("failed to print CLI sessions: %w", err)
	}
	return nil
}

func sessionRows(
	delegations []qauth.Delegation,
	liveness map[string]sessionLiveness,
	currentName string,
	neverUsed bool,
) ([]sessionView, int) {
	rows := make([]sessionView, 0, len(delegations))
	unknown := 0
	for _, delegation := range delegations {
		live := livenessFor(liveness, delegation.ID)
		if neverUsed && !live.ExpiredUnused {
			if live.Status == statusUnknown {
				unknown++
			}
			continue
		}
		rows = append(rows, sessionView{
			Current:        delegation.Name != "" && delegation.Name == currentName,
			Name:           delegation.Name,
			Status:         live.Status,
			ID:             delegation.ID,
			OrganizationID: delegation.OrganizationID,
			CreatedAt:      delegation.CreatedAt,
			ExpiresAt:      live.ExpiresAt,
			FirstUsedAt:    live.FirstUsedAt,
		})
	}
	return rows, unknown
}

func newRevokeSessionCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "revoke [name-or-id]",
		Short: "Revoke CLI sessions",
		Long: "Revoke active CLI sessions by exact name, or revoke one by delegation ID.\n\n" +
			"--expired instead revokes every session the inventory reports as no longer\n" +
			"able to authenticate, which is how a machine clears what daily logins have\n" +
			"piled up. It never touches a session it could not classify, nor this one.",
		Args:        cobra.MaximumNArgs(1),
		Annotations: cmdutil.WithFormats(catalog.For("session"), "table"),
		RunE:        runRevokeSession,
	}
	cmd.Flags().Bool("expired", false,
		"Revoke every CLI session that can no longer authenticate, instead of naming one")
	return cmd
}

func runRevokeSession(cmd *cobra.Command, args []string) error {
	expired, err := cmd.Flags().GetBool("expired")
	if err != nil {
		return fmt.Errorf("failed to read --expired: %w", err)
	}

	// Validated before Bootstrap, so a malformed invocation exits 2 without
	// reaching for a credential -- the order the empty-identifier check has
	// always used.
	switch {
	case expired && len(args) > 0:
		return qerrors.NewBadUsage(
			"--expired revokes every session it finds dead, so it cannot also be given one to revoke")
	case !expired && len(args) == 0:
		return qerrors.NewBadUsage(
			"specify the session name or ID to revoke, or --expired to revoke every session that can no longer authenticate")
	case !expired && strings.TrimSpace(args[0]) == "":
		return qerrors.NewBadUsage("session name or ID must not be empty")
	}

	cmdCtx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}

	client := qauth.NewClient(cmdCtx.ServerURL, cmdCtx.Verbosity)
	if expired {
		return revokeExpiredSessions(cmd, cmdCtx, client)
	}
	return revokeNamedSession(cmd, cmdCtx, client, args[0])
}

func revokeNamedSession(
	cmd *cobra.Command,
	cmdCtx *cmdutil.CommandContext,
	client *qauth.Client,
	identifier string,
) error {
	matched, revokedDelegations, revoked, err := revokeSessionsWithResults(
		cmd.Context(), client, cmdCtx.Credential.AccessToken, cmdCtx.OrganizationID, identifier)
	if errors.Is(err, qauth.ErrTokenRejected) {
		return qerrors.NewUnauthorized("the server rejected the stored credential")
	}
	currentMatched := matchesSession(matched, cmdCtx.Credential.TokenName)
	if err != nil {
		return revokeFailedError(identifier, revoked, currentMatched, err)
	}
	if len(matched) == 0 {
		return qerrors.NewNotFound(apierror.WithScopeNote(
			fmt.Sprintf("no active CLI session named or identified by %q", identifier)))
	}
	if !currentMatched && len(revokedDelegations) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), describeRevoke(identifier, len(matched), revoked))
		return nil
	}
	endpointKey, err := config.NormalizeEndpointKey(cmdCtx.ServerURL)
	if err != nil {
		return fmt.Errorf("failed to normalize endpoint: %w", err)
	}
	store := qauth.NewCredentialStore(qauth.IsPlaintextAllowed())
	cleanupTargets := revokedDelegations
	if currentMatched {
		// Keep the existing logout behavior when this session was already gone:
		// its local handle is dead either way and must not be left behind.
		cleanupTargets = matched
	}
	forgotten, kept, err := forgetEndedCopies(
		store, endpointKey, cmdCtx.OrganizationID, cleanupTargets, revoked > 0)
	if err != nil {
		return err
	}

	fmt.Fprintln(cmd.OutOrStdout(), describeRevoke(identifier, len(matched), revoked))
	if currentMatched && len(forgotten) > 0 {
		// The credential is gone either way -- a session already ended on the
		// server is still dead locally -- and "had already ended; nothing was
		// revoked" would otherwise log the user out without a word about it.
		fmt.Fprintln(cmd.OutOrStdout(), describeForgotten(store, endpointKey, cmdCtx.OrganizationID, kept))
	} else if len(forgotten) > 0 {
		fmt.Fprintln(cmd.OutOrStdout(), describeRemovedRevokedCopies(forgotten))
	}
	return nil
}

// forgetEndedCopies deletes each stored copy of the credential whose session
// is among matched. It reports whether it deleted any, and returns the copy it
// kept, if one belongs to another session.
func forgetEndedCopies(
	store *qauth.CredentialStore,
	endpointKey, orgID string,
	matched []qauth.Delegation,
	revoked bool,
) (forgotten []qauth.StoredCopy, kept *qauth.StoredCopy, err error) {
	credKey := qauth.MakeCredentialKey(endpointKey, orgID)
	copies, err := store.RetrieveCopies(endpointKey, orgID)
	if errors.Is(err, qauth.ErrCredentialNotFound) {
		return nil, nil, nil
	}
	if err != nil {
		failed := "could not read the stored credential to remove it"
		if revoked {
			failed = "revoked this CLI session on the server, but " + failed
		}
		return nil, nil, fmt.Errorf("%s: %w", failed, err)
	}

	var ended []qauth.StoredCopy
	for _, c := range copies {
		if matchesSession(matched, c.Credential.TokenName) {
			ended = append(ended, c)
		} else {
			kept = &c
		}
	}
	switch {
	case len(ended) == 0:
		return nil, nil, nil
	case kept == nil:
		if err := store.Delete(endpointKey, orgID); err != nil {
			return nil, nil, deleteFailedError(err, revoked, credKey)
		}
	default:
		if err := deleteCopy(store, endpointKey, orgID, ended[0]); err != nil {
			return nil, nil, endedCopyDeleteFailedError(err, ended[0], revoked, credKey)
		}
	}
	return ended, kept, nil
}

func describeRemovedRevokedCopies(copies []qauth.StoredCopy) string {
	removed := make([]string, 0, len(copies))
	for _, copy := range copies {
		name := "unnamed CLI session"
		if copy.Credential.TokenName != "" {
			name = fmt.Sprintf("CLI session %q", copy.Credential.TokenName)
		}
		removed = append(removed, fmt.Sprintf("%s from %s", name, copyLocation(copy)))
	}
	if len(removed) == 1 {
		return fmt.Sprintf("Removed the stored credential for %s because it was revoked.", removed[0])
	}
	return fmt.Sprintf("Removed stored credentials for revoked CLI sessions: %s.", strings.Join(removed, ", "))
}

// endedCopyDeleteFailedError reports a stored copy whose session was revoked
// but which could not be removed from the one half of the store that holds it.
func endedCopyDeleteFailedError(err error, c qauth.StoredCopy, revoked bool, credKey string) error {
	failed := "failed to clear the stored credential from " + qauth.CredentialsFileLocation()
	remedy := "Inspect it by hand."
	if c.InKeyring {
		failed = "failed to remove the stored credential from the OS keyring"
		remedy = keyringEntryRemedy(credKey)
	}
	if revoked {
		failed = "revoked this CLI session on the server, but " + failed
	}
	return fmt.Errorf("%s: %w\n\n%s", failed, err, remedy)
}

// describeForgotten reports the local half of revoking this machine's session.
// kept is the stored copy that belongs to another session, or nil.
func describeForgotten(store *qauth.CredentialStore, endpointKey, orgID string, kept *qauth.StoredCopy) string {
	const removed = "That was this machine's session, so its stored credential was removed too."
	const login = "Run 'qctl auth login' before the next authenticated command."
	if kept == nil {
		return removed + " " + login
	}

	other := fmt.Sprintf("The other stored credential, found in %s, was kept", copyLocation(*kept))
	if name := kept.Credential.TokenName; name != "" {
		other = fmt.Sprintf("The credential of the other CLI session %q, found in %s, was kept", name, copyLocation(*kept))
	}
	next, err := store.Retrieve(endpointKey, orgID)
	if err == nil && !next.IsExpired() && next.AccessToken == kept.Credential.AccessToken {
		return removed + " " + other + ", and later commands will use it."
	}
	return removed + " " + other + ". " + login
}

// matchesSession reports whether the session named tokenName is among the
// delegations matched. An empty name matches nothing.
func matchesSession(matched []qauth.Delegation, tokenName string) bool {
	if tokenName == "" {
		return false
	}
	for _, delegation := range matched {
		if delegation.Name == tokenName {
			return true
		}
	}
	return false
}

// revokeFailedError reports a revoke that stopped partway. revokeSessions halts
// at the first failure but still reports what it revoked before it, and telling
// the user only that the command failed hides those revocations -- against this
// command's own rule of separating what was matched from what was really
// revoked. The local credential is deliberately left alone here: which
// delegations the loop reached is known, which of them was this machine's is
// not, and deleting the handle to a session that may still be live is the
// orphan this whole branch exists to prevent.
func revokeFailedError(identifier string, revoked int, currentMatched bool, err error) error {
	if revoked == 0 {
		return fmt.Errorf("failed to revoke CLI session %q: %w", identifier, err)
	}
	remedy := fmt.Sprintf(
		"%d of the CLI sessions named %q were revoked before this failure. "+
			"Run 'qctl auth sessions' to see which are left.", revoked, identifier)
	if currentMatched {
		remedy += "\nThis machine's own session was among the matches and may be one of them; " +
			"its stored credential was kept, so 'qctl auth logout --local' forgets it if so."
	}
	return fmt.Errorf("failed to revoke CLI session %q: %w\n\n%s", identifier, err, remedy)
}

// describeRevoke says what a revoke actually did. A session can be listed and
// then end before its DELETE lands -- revoked elsewhere (200 with
// success:false) or gone (404) -- so "Revoked" is claimed only for the ones
// this run revoked.
func describeRevoke(identifier string, matched, revoked int) string {
	switch {
	case revoked == 0 && matched == 1:
		return fmt.Sprintf("CLI session %q had already ended; nothing was revoked.", identifier)
	case revoked == 0:
		return fmt.Sprintf("All %d CLI sessions named %q had already ended; nothing was revoked.", matched, identifier)
	case matched == 1:
		return fmt.Sprintf("Revoked CLI session %q.", identifier)
	case revoked == matched:
		return fmt.Sprintf("Revoked %d CLI sessions named %q.", revoked, identifier)
	default:
		return fmt.Sprintf("Revoked %d of %d CLI sessions named %q; the others had already ended.",
			revoked, matched, identifier)
	}
}

// revokeSessions revokes every active delegation whose exact name matches
// identifier and discards which ones this call actually revoked.
func revokeSessions(
	ctx context.Context,
	client *qauth.Client,
	accessToken, orgID, identifier string,
) (matched []qauth.Delegation, revoked int, err error) {
	matched, _, revoked, err = revokeSessionsWithResults(ctx, client, accessToken, orgID, identifier)
	return matched, revoked, err
}

// revokeSessionsWithResults revokes every active delegation whose exact name matches
// identifier. A delegation UUID takes precedence and addresses exactly one
// session, including a legacy session that has no name.
//
// It returns the delegations it attempted, the ones it really revoked, and the
// count. A delegation already ended before its DELETE is not in the revoked
// list, so local cleanup cannot mistake it for one this call ended.
// The first other failure stops the loop and is returned as-is --
// ErrTokenRejected and ErrDelegationForbidden included -- for each caller to
// map to its own outcome.
func revokeSessionsWithResults(
	ctx context.Context,
	client *qauth.Client,
	accessToken, orgID, identifier string,
) (matched, revokedDelegations []qauth.Delegation, revoked int, err error) {
	delegations, err := client.ListDelegations(ctx, accessToken, orgID)
	if err != nil {
		return nil, nil, 0, err
	}

	return revokeEach(ctx, client, accessToken, orgID, matchDelegations(delegations, identifier))
}

// matchDelegations selects the delegations identifier names. A delegation UUID
// takes precedence over a name and addresses exactly one session; otherwise
// every exact name match is selected.
func matchDelegations(delegations []qauth.Delegation, identifier string) []qauth.Delegation {
	matchByID := false
	for _, delegation := range delegations {
		if delegation.ID == identifier {
			matchByID = true
			break
		}
	}

	var matched []qauth.Delegation
	for _, delegation := range delegations {
		if (matchByID && delegation.ID != identifier) || (!matchByID && delegation.Name != identifier) {
			continue
		}
		matched = append(matched, delegation)
	}
	return matched
}

// revokeEach revokes the given delegations in order and reports the attempted
// prefix, the delegations it really revoked, and their count.
//
// Returning the attempted prefix rather than the whole list is what keeps a
// failed revoke's message honest. A caller asking "was this machine's session
// among them?" is asking whether its credential may already be dead, and a
// delegation the loop never reached is one the server certainly still holds --
// counting it would turn "your session is safe" into "your session may be
// gone".
func revokeEach(
	ctx context.Context,
	client *qauth.Client,
	accessToken, orgID string,
	targets []qauth.Delegation,
) (attempted, revokedDelegations []qauth.Delegation, revoked int, err error) {
	for _, delegation := range targets {
		attempted = append(attempted, delegation)
		ok, _, revokeErr := client.RevokeDelegation(ctx, accessToken, orgID, delegation.ID)
		if errors.Is(revokeErr, qauth.ErrDelegationGone) {
			continue
		}
		if revokeErr != nil {
			return attempted, revokedDelegations, revoked, revokeErr
		}
		if ok {
			revokedDelegations = append(revokedDelegations, delegation)
			revoked++
		}
	}
	return attempted, revokedDelegations, revoked, nil
}

// revokeExpiredSessions is the bulk cleanup: it ends every session the join
// reports as unable to authenticate, which is what a machine needs after a
// stretch of daily logins, each of which left a delegation that outlives the
// token behind it.
func revokeExpiredSessions(
	cmd *cobra.Command,
	cmdCtx *cmdutil.CommandContext,
	client *qauth.Client,
) error {
	ctx := cmd.Context()
	delegations, err := client.ListDelegations(ctx, cmdCtx.Credential.AccessToken, cmdCtx.OrganizationID)
	if errors.Is(err, qauth.ErrTokenRejected) {
		return qerrors.NewUnauthorized("the server rejected the stored credential")
	}
	if err != nil {
		return fmt.Errorf("failed to list CLI sessions: %w", err)
	}

	// The exact inverse of what the inventory does with this same failure, and
	// deliberately so. A list can degrade a column to "unknown" and still be
	// worth printing; a revoke cannot, because there is no honest revocation to
	// perform on a guess -- every session would look equally dead, and the one
	// live login on another machine would go with the rest.
	liveness, err := fetchSessionLiveness(
		ctx, client, cmdCtx.Credential.AccessToken, cmdCtx.OrganizationID)
	if errors.Is(err, qauth.ErrTokenRejected) {
		return qerrors.NewUnauthorized("the server rejected the stored credential")
	}
	if err != nil {
		return fmt.Errorf(
			"could not tell which CLI sessions are still usable, so none were revoked: %w", err)
	}

	dead, unclassified := expiredSessions(delegations, liveness, cmdCtx.Credential.TokenName)
	if len(dead) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No expired CLI sessions to revoke.")
		printUnclassifiedNote(cmd, unclassified)
		return nil
	}

	// The local credential is never part of this: expiredSessions excludes this
	// machine's own session, so unlike the named revoke there is nothing local
	// to clean up or report.
	_, revokedDelegations, revoked, err := revokeEach(
		ctx, client, cmdCtx.Credential.AccessToken, cmdCtx.OrganizationID, dead)
	if errors.Is(err, qauth.ErrTokenRejected) {
		return qerrors.NewUnauthorized("the server rejected the stored credential")
	}
	if err != nil {
		return revokeExpiredFailedError(revoked, err)
	}
	if len(revokedDelegations) > 0 {
		endpointKey, err := config.NormalizeEndpointKey(cmdCtx.ServerURL)
		if err != nil {
			return fmt.Errorf("revoked expired CLI sessions, but failed to normalize endpoint: %w", err)
		}
		store := qauth.NewCredentialStore(qauth.IsPlaintextAllowed())
		forgotten, _, err := forgetEndedCopies(
			store, endpointKey, cmdCtx.OrganizationID, revokedDelegations, true)
		if err != nil {
			return err
		}
		if len(forgotten) > 0 {
			fmt.Fprintln(cmd.OutOrStdout(), describeRemovedRevokedCopies(forgotten))
		}
	}

	fmt.Fprintln(cmd.OutOrStdout(), describeExpiredRevoke(len(dead), revoked))
	printUnclassifiedNote(cmd, unclassified)
	return nil
}

// expiredSessions selects what a bulk cleanup may end, and returns separately
// how many sessions it refused to judge.
//
// Two exclusions, both load-bearing. A session the join could not classify is
// left alone: qctl does not know whether it can authenticate, and a cleanup
// that ends what it cannot classify is how a live session on another machine
// disappears. This machine's own session is left alone too -- not because it
// could plausibly be dead, since the token authenticating this very call is by
// definition live, but because `current` is a token_name string match, so two
// delegations sharing a name are both current, and revoking the dead one would
// delete the local credential belonging to the live one.
func expiredSessions(
	delegations []qauth.Delegation,
	liveness map[string]sessionLiveness,
	tokenName string,
) (dead []qauth.Delegation, unclassified int) {
	for _, delegation := range delegations {
		if tokenName != "" && delegation.Name == tokenName {
			continue
		}
		switch livenessFor(liveness, delegation.ID).Status {
		case statusExpired, statusRevoked:
			dead = append(dead, delegation)
		case statusUnknown:
			unclassified++
		}
	}
	return dead, unclassified
}

// printUnclassifiedNote reports the sessions the cleanup deliberately left
// behind. Silence would read as "everything dead is gone now", which is the
// one thing a user runs this command to be able to believe.
func printUnclassifiedNote(cmd *cobra.Command, unclassified int) {
	if unclassified == 0 {
		return
	}
	if unclassified == 1 {
		fmt.Fprintln(cmd.OutOrStdout(),
			"1 CLI session could not be classified and was left alone; "+
				"run 'qctl auth sessions' and revoke it by ID if it should go.")
		return
	}
	fmt.Fprintf(cmd.OutOrStdout(),
		"%d CLI sessions could not be classified and were left alone; "+
			"run 'qctl auth sessions' and revoke them by ID if they should go.\n", unclassified)
}

// describeExpiredRevoke mirrors describeRevoke's rule: "Revoked" is claimed
// only for the sessions this run actually revoked, since one listed as dead can
// also be gone entirely by the time its DELETE lands.
func describeExpiredRevoke(matched, revoked int) string {
	switch {
	case revoked == 0 && matched == 1:
		return "The one expired CLI session had already ended; nothing was revoked."
	case revoked == 0:
		return fmt.Sprintf(
			"All %d expired CLI sessions had already ended; nothing was revoked.", matched)
	case matched == 1:
		return "Revoked 1 expired CLI session."
	case revoked == matched:
		return fmt.Sprintf("Revoked %d expired CLI sessions.", revoked)
	default:
		return fmt.Sprintf(
			"Revoked %d of %d expired CLI sessions; the others had already ended.", revoked, matched)
	}
}

// revokeExpiredFailedError reports a bulk cleanup that stopped partway. Like
// the named revoke's, it names what it revoked before the failure rather than
// hiding those behind a bare "failed".
func revokeExpiredFailedError(revoked int, err error) error {
	if revoked == 0 {
		return fmt.Errorf("failed to revoke the expired CLI sessions: %w", err)
	}
	// The remedy is built separately, as revokeFailedError builds its own: a
	// format string ending in a period is what staticcheck's ST1005 refuses.
	remedy := fmt.Sprintf(
		"%d of them were revoked before this failure. "+
			"Run 'qctl auth sessions' to see which are left.", revoked)
	return fmt.Errorf("failed to revoke the expired CLI sessions: %w\n\n%s", err, remedy)
}
