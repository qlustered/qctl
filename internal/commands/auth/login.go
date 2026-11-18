package auth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/auth"
	"github.com/qlustered/qctl/internal/auth/oauth"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	"github.com/spf13/cobra"
)

// NewLoginCommand creates the auth login command
func NewLoginCommand() *cobra.Command {
	var (
		tokenName           string
		allowPlaintextStore bool
	)

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Login via browser-based OAuth",
		Long: `Login to Qluster using browser-based OAuth authentication.

This command will:
1. Open your default browser to authenticate with Kinde
2. Wait for you to complete the authentication flow
3. Exchange the authentication token for a CLI access token
4. Store the access token securely in your OS keyring (or a 0600 file -- see below)

The access token goes into your OS keyring. It falls back to a 0600 file when
no keyring is available -- silently, since that is the normal state on a
container, CI box, or headless server -- and with a warning on stderr when a
keyring that is up, and still answering other reads, refuses the write.
--allow-plaintext-token-store skips the keyring on purpose when storing. Later
commands use the newer of the file and keyring credentials. Set
QCTL_ALLOW_PLAINTEXT_TOKENS=1 to skip the keyring on later reads too.
Without that environment opt-in, login warns if an older keyring entry is
readable. The flag does not stop 'auth logout' from trying to clear that entry.`,
		Annotations: cmdutil.WithFormats(catalog.For("session"), "table"),
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := auth.ValidateTokenNameBase(tokenName); err != nil {
				return err
			}
			// Bootstrap without auth (we're logging in, so no existing credentials needed)
			cmdCtx, err := cmdutil.BootstrapWithoutAuth(cmd)
			if err != nil {
				return err
			}

			// Display login context
			displayLoginContext(cmd.OutOrStdout(), cmdCtx.Config, cmdCtx.ServerURL)

			// Resolve Kinde configuration
			kindeHost := config.ResolveKindeHost(cmdCtx.Config)
			kindeClientID := config.ResolveKindeClientID(cmdCtx.Config)

			if kindeClientID == "" {
				//nolint:staticcheck // Preserve the established multi-line CLI guidance verbatim.
				return fmt.Errorf("Auth configuration not available.\nRun 'qctl config set-context <name> --server <url>' to configure")
			}

			// Perform OAuth login
			return performOAuthLogin(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(),
				cmdCtx, kindeHost, kindeClientID, tokenName, allowPlaintextStore)
		},
	}

	addLoginFlags(cmd, &tokenName, &allowPlaintextStore)
	return cmd
}

// displayLoginContext prints the current context and server info.
func displayLoginContext(out io.Writer, cfg *config.Config, serverURL string) {
	if cfg.CurrentContext != "" {
		fmt.Fprintf(out, "Context: %s\n", cfg.CurrentContext)
	}
	fmt.Fprintf(out, "Server:  %s\n", serverURL)
	fmt.Fprintln(out)
}

// performOAuthLogin executes the OAuth 2.0 Device Authorization flow
func performOAuthLogin(ctx context.Context, stdout, stderr io.Writer, cmdCtx *cmdutil.CommandContext, kindeHost, kindeClientID, tokenName string, allowPlaintextStore bool) error {
	// 1. Create Kinde client
	kindeClient := oauth.NewKindeClient(kindeHost, kindeClientID)

	// 2. Request device code
	fmt.Fprintln(stdout, "Initiating authentication...")
	deviceAuth, err := kindeClient.RequestDeviceCode(ctx)
	if err != nil {
		return fmt.Errorf("failed to initiate device auth: %w", err)
	}

	// 3-5. Show the URL, open a browser, and poll. Shared with switch-org.
	kindeTokens, err := promptAndPollForKindeTokens(ctx, stdout, stderr, kindeClient, deviceAuth)
	if err != nil {
		return err
	}

	// 6. Exchange Kinde token for Atlas CLI token (with retry on failure).
	// The name is minted here, not in the exchange helper, because the same
	// string has to reach both the server and the local credential: it is the
	// only handle qctl will have on this session when logout needs to revoke it.
	mintedName, err := auth.MintTokenName(tokenName)
	if err != nil {
		return err
	}

	fmt.Fprintln(stdout, "Obtaining CLI access token...")
	authClient := auth.NewClient(cmdCtx.ServerURL, cmdCtx.Verbosity)
	exchangeResp, err := exchangeWithRetry(ctx, stdout, stderr, authClient, kindeTokens.AccessToken, mintedName)
	if err != nil {
		return fmt.Errorf("failed to exchange for CLI token: %w", err)
	}

	// 7. Store credential
	storeResult, err := storeCredential(
		ctx, cmdCtx.ServerURL, cmdCtx.Verbosity, exchangeResp, mintedName, allowPlaintextStore)
	if err != nil {
		return err
	}
	reportStoreResult(stdout, stderr, storeResult, "this organization")

	// 8. Fetch user info to cache organization names
	fmt.Fprintln(stdout, "Fetching organization info...")
	userInfo, err := authClient.GetMe(exchangeResp.AccessToken, exchangeResp.OrganizationID)
	if err != nil {
		fmt.Fprintf(stderr, "Warning: could not fetch user info: %v\n", err)
	}

	// 9. Update context with organization info
	if err := updateContextAfterLogin(cmdCtx.Config, exchangeResp, userInfo); err != nil {
		// Non-fatal: warn but continue
		fmt.Fprintf(stderr, "Warning: failed to update context: %v\n", err)
	}

	// Display org name if available, otherwise fall back to ID
	orgDisplay := exchangeResp.OrganizationID
	if userInfo != nil {
		for i, id := range userInfo.ActiveOrganizationIDs {
			if id == exchangeResp.OrganizationID && i < len(userInfo.ActiveOrganizationNames) {
				orgDisplay = userInfo.ActiveOrganizationNames[i]
				break
			}
		}
	}
	fmt.Fprintf(stdout, "\nSuccessfully logged in to organization %s\n", orgDisplay)
	return nil
}

type credentialStoreResult struct {
	StorageWarning     string
	ReplacementWarning string
	RevokedPrevious    bool
	// RevokedOther names a replaced credential ordinary commands were not
	// reading, when its session was revoked.
	RevokedOther *otherSession
}

// otherSession names a replaced credential by its token name and by where it
// was stored.
type otherSession struct {
	Name  string
	Where string
}

// reportStoreResult prints what storeCredential did besides storing: the
// keyring downgrade, the live session it ended, and a cleanup it could not
// finish. scope names the organization the replaced session belonged to.
func reportStoreResult(stdout, stderr io.Writer, result credentialStoreResult, scope string) {
	if result.StorageWarning != "" {
		fmt.Fprintln(stderr, result.StorageWarning)
	}
	if result.RevokedPrevious {
		fmt.Fprintf(stdout, "Revoked the previous CLI session for %s.\n", scope)
	}
	if other := result.RevokedOther; other != nil {
		fmt.Fprintf(stdout, "Revoked the other CLI session %q for %s, whose credential was stored in %s.\n",
			other.Name, scope, other.Where)
	}
	if result.ReplacementWarning != "" {
		fmt.Fprintln(stderr, result.ReplacementWarning)
	}
}

// storeCredential stores the new access token, then retires every stored
// credential it replaced. Storing first is deliberate: if local storage fails,
// the still-stored old credential remains the only usable handle instead of
// both server sessions becoming unreachable from qctl.
func storeCredential(
	ctx context.Context,
	serverURL string,
	verbosity int,
	exchangeResp *auth.CLIExchangeResponse,
	tokenName string,
	allowPlaintextStore bool,
) (credentialStoreResult, error) {
	var result credentialStoreResult
	endpointKey, err := config.NormalizeEndpointKey(serverURL)
	if err != nil {
		return result, fmt.Errorf("failed to normalize endpoint: %w", err)
	}

	preferPlaintext := allowPlaintextStore || auth.IsPlaintextAllowed()
	credStore := auth.NewCredentialStore(preferPlaintext)
	// Every copy is read, not just the one Retrieve picks: a keyring write
	// overwrites the keyring's copy even when the file's newer one is the pick,
	// and a refused keyring write replaces the file's copy and removes the
	// keyring's.
	replaced, retrieveErr := storedCopies(preferPlaintext, endpointKey, exchangeResp.OrganizationID)

	cred := &auth.Credential{
		CreatedAt:      time.Now(),
		AccessToken:    exchangeResp.AccessToken,
		ExpiresAt:      time.Now().Add(time.Duration(exchangeResp.ExpiresIn) * time.Second),
		OrganizationID: exchangeResp.OrganizationID,
		TokenName:      tokenName,
	}
	client := auth.NewClient(serverURL, verbosity)

	result.StorageWarning, err = credStore.Store(endpointKey, cred)
	if err != nil {
		return result, storeFailedError(ctx, client, cred, err)
	}
	if allowPlaintextStore && !auth.IsPlaintextAllowed() &&
		credStore.HasReadableKeyringEntry(endpointKey, cred.OrganizationID) {
		result.StorageWarning = fmt.Sprintf(
			"Warning: an older OS keyring credential remains for this server and organization "+
				"(service %q, entry %q). Later commands compare creation times when choosing between it "+
				"and the new file token. Set QCTL_ALLOW_PLAINTEXT_TOKENS=1 to make later reads use the file only. "+
				"Use 'qctl auth sessions' to inspect and revoke the older session if it is still active.",
			auth.KeyringServiceName, auth.MakeCredentialKey(endpointKey, cred.OrganizationID))
	}

	if retrieveErr != nil {
		if !errors.Is(retrieveErr, auth.ErrCredentialNotFound) {
			result.ReplacementWarning = fmt.Sprintf(
				"Warning: qctl stored the new credential, but could not read the previous one before replacing it: %v\n"+
					"Use 'qctl auth sessions' to inspect active sessions and revoke any older one by name or ID.",
				retrieveErr)
		}
		return result, nil
	}
	retireReplacedCopies(ctx, client, preferPlaintext, endpointKey, replaced, cred, &result)
	return result, nil
}

// storedCopies returns the distinct credentials stored for endpointKey and
// orgID, the one Retrieve returns first. With preferPlaintext it reads the
// fallback file alone.
func storedCopies(preferPlaintext bool, endpointKey, orgID string) ([]auth.StoredCopy, error) {
	credStore := auth.NewCredentialStore(preferPlaintext)
	if !preferPlaintext {
		return credStore.RetrieveCopies(endpointKey, orgID)
	}
	cred, err := credStore.Retrieve(endpointKey, orgID)
	if err != nil {
		return nil, err
	}
	return []auth.StoredCopy{{Credential: cred, InFile: true}}, nil
}

// retireReplacedCopies runs retireReplacedCredential for each of replaced, the
// copies storedCopies returned before replacement was stored, and records the
// outcomes in result. A live copy that storedCopies still returns afterwards
// is left alone.
func retireReplacedCopies(
	ctx context.Context,
	client *auth.Client,
	preferPlaintext bool,
	endpointKey string,
	replaced []auth.StoredCopy,
	replacement *auth.Credential,
	result *credentialStoreResult,
) {
	remaining, remainingErr := storedCopies(preferPlaintext, endpointKey, replacement.OrganizationID)
	var warnings []string
	for i, c := range replaced {
		if !c.Credential.IsExpired() && remainingErr == nil && holdsToken(remaining, c.Credential.AccessToken) {
			continue
		}
		where := ""
		if i > 0 {
			where = copyLocation(c)
		}
		revoked, warning := retireReplacedCredential(ctx, client, c.Credential, replacement, where)
		switch {
		case revoked && where == "":
			result.RevokedPrevious = true
		case revoked:
			result.RevokedOther = &otherSession{Name: c.Credential.TokenName, Where: where}
		}
		if warning != "" {
			warnings = append(warnings, warning)
		}
	}
	result.ReplacementWarning = strings.Join(warnings, "\n")
}

// holdsToken reports whether any of copies carries token.
func holdsToken(copies []auth.StoredCopy, token string) bool {
	return slices.ContainsFunc(copies, func(c auth.StoredCopy) bool {
		return c.Credential.AccessToken == token
	})
}

// storeFailedError reports a failed store after revoking the session the
// exchange has just minted. Nothing qctl reads will present that session's
// token -- it was stored nowhere, or where it landed is hidden -- so left alone
// it would run out its lifetime with no local handle, which is the orphan this
// command exists to prevent, and every retry of the login would leave one more.
// Its own token can revoke it: a session that belongs to the caller is all the
// revoke route asks for.
func storeFailedError(ctx context.Context, client *auth.Client, cred *auth.Credential, storeErr error) error {
	_, revoked, err := revokeSessions(ctx, client, cred.AccessToken, cred.OrganizationID, cred.TokenName)
	if err == nil && revoked > 0 {
		return fmt.Errorf("failed to store credential: %w\n\n%s", storeErr,
			"The CLI session this login created was revoked, so nothing is left running. "+
				"qctl keeps using the credential it had before this login, if there was one.")
	}
	reason := ""
	if err != nil {
		reason = fmt.Sprintf(" (%v)", err)
	}
	return fmt.Errorf("failed to store credential: %w\n\n%s", storeErr, fmt.Sprintf(
		"The CLI session this login created, %q, could not be revoked%s.%s\n"+
			"Revoke it from another login with 'qctl auth sessions revoke %s'.",
		cred.TokenName, reason, credentialValiditySuffix(cred), cred.TokenName))
}

// retireReplacedCredential ends the server session behind a credential that
// storeCredential has just replaced, so that it is not left running with no
// local handle. It returns whether a live session was revoked, and a warning
// for stderr when one could not be. where is empty for the credential ordinary
// commands were reading, and otherwise names where the other one was stored.
//
// Which token authenticates the revoke depends on whether the old one still
// can. A live one revokes its own session, which reaches it even when the new
// login is a different user. An expired or rejected one cannot, and its session
// is already unusable -- but a delegation has no expiry of its own, so the
// server goes on listing it under 'auth sessions' until something revokes it,
// and with tokens that last a day, the usual re-login is exactly this case. So
// the new token revokes it by name instead. The revoke route checks only that
// the caller owns the delegation (capi/capi/views/user_view.py,
// revoke_my_delegation), which reaches the same user's session and never
// another user's. That cleanup is silent either way: there is no live
// credential left to report on.
func retireReplacedCredential(
	ctx context.Context,
	client *auth.Client,
	previous, replacement *auth.Credential,
	where string,
) (bool, string) {
	// Revoking by the new session's own name would end the login just made.
	if previous.TokenName == replacement.TokenName {
		return false, ""
	}
	unnamed := "the previous CLI session"
	named := fmt.Sprintf("the previous CLI session %q", previous.TokenName)
	if where != "" {
		unnamed = "the other CLI session, whose credential was stored in " + where + ","
		named = fmt.Sprintf("the other CLI session %q, whose credential was stored in %s", previous.TokenName, where)
	}
	live := !previous.IsExpired()
	if previous.TokenName == "" {
		if !live {
			return false, ""
		}
		return false, fmt.Sprintf(
			"Warning: %s was not revoked because its stored credential has no token name.%s\n"+
				"Use 'qctl auth sessions' to find it and revoke it by ID.",
			unnamed, credentialValiditySuffix(previous))
	}
	orgID := replacement.OrganizationID

	if live {
		_, revoked, err := revokeSessions(ctx, client, previous.AccessToken, orgID, previous.TokenName)
		if err == nil {
			return revoked > 0, ""
		}
		if !errors.Is(err, auth.ErrTokenRejected) {
			// Report the failure alone, never alongside a "Revoked" line. A
			// name can match more than one delegation, and revokeSessions
			// stops at the first failure, so revoked can be non-zero here --
			// but "Revoked the previous CLI session" and "could not revoke the
			// previous CLI session" are the same singular claim twice, in
			// opposite directions. The warning carries the whole outcome.
			lead := "could not revoke " + named
			if revoked > 0 {
				lead = fmt.Sprintf(
					"revoked %d of the CLI sessions named %q, but could not revoke the rest",
					revoked, previous.TokenName)
			}
			return false, fmt.Sprintf(
				"Warning: qctl stored the new credential, but %s: %v%s\n"+
					"Retry with 'qctl auth sessions revoke %s'.",
				lead, err, credentialValiditySuffix(previous), previous.TokenName)
		}
		// Rejected: the server has already ended that session. What is left of
		// it is the listing, which the cleanup below removes.
	}
	_, _, _ = revokeSessions(ctx, client, replacement.AccessToken, orgID, previous.TokenName)
	return false, ""
}

func credentialValiditySuffix(cred *auth.Credential) string {
	if cred.ExpiresAt.IsZero() {
		return " It may remain valid until the server expires it."
	}
	return fmt.Sprintf(" It may remain valid until %s.", cred.ExpiresAt.Format(time.RFC3339))
}

// updateContextAfterLogin updates the context with organization info after login
func updateContextAfterLogin(cfg *config.Config, exchangeResp *auth.CLIExchangeResponse, userInfo *auth.UserMeResponse) error {
	if cfg.CurrentContext == "" {
		return nil // No context to update
	}

	ctx, err := cfg.GetCurrentContext()
	if err != nil {
		return err
	}

	// Always update to the org returned by the login exchange
	ctx.Organization = exchangeResp.OrganizationID

	// Cache organization names from /users/me response
	if userInfo != nil && len(userInfo.ActiveOrganizationIDs) > 0 {
		// Resolve the current org's display name
		for i, id := range userInfo.ActiveOrganizationIDs {
			if id == ctx.Organization && i < len(userInfo.ActiveOrganizationNames) {
				ctx.OrganizationName = userInfo.ActiveOrganizationNames[i]
				break
			}
		}

		// Cache the full org list for switch-org / set-context resolution
		ctx.Organizations = make([]config.OrganizationRef, 0, len(userInfo.ActiveOrganizationIDs))
		for i, id := range userInfo.ActiveOrganizationIDs {
			name := ""
			if i < len(userInfo.ActiveOrganizationNames) {
				name = userInfo.ActiveOrganizationNames[i]
			}
			ctx.Organizations = append(ctx.Organizations, config.OrganizationRef{
				ID:   id,
				Name: name,
			})
		}
	}

	return cfg.Save()
}

// addLoginFlags adds flags for the login command
func addLoginFlags(cmd *cobra.Command, tokenName *string, allowPlaintextStore *bool) {
	cmd.Flags().StringVar(tokenName, "token-name", "", "Name for this CLI token (for audit trail); qctl appends a unique suffix so 'qctl auth logout' can revoke this session")
	cmd.Flags().BoolVar(allowPlaintextStore, "allow-plaintext-token-store", false, "Store the token in a 0600 file without writing to the OS keyring; check for an older entry unless QCTL_ALLOW_PLAINTEXT_TOKENS is set")
}

// exchangeWithRetry attempts the token exchange and prompts for retry on transient failures.
// Permanent errors (4xx, auth issues, account setup errors) are returned immediately
// without offering retry, since retrying won't help.
func exchangeWithRetry(ctx context.Context, stdout, stderr io.Writer, authClient *auth.Client, kindeAccessToken, tokenName string) (*auth.CLIExchangeResponse, error) {
	for {
		resp, err := authClient.ExchangeForCLIToken(ctx, kindeAccessToken, tokenName)
		if err == nil {
			return resp, nil
		}

		fmt.Fprintf(stderr, "Error: %v\n", err)

		// Only offer retry for transient errors (network issues, server 5xx).
		// Permanent errors (4xx → bad usage / not found / unauthorized) won't
		// be fixed by retrying from the client.
		if !isRetryable(err) {
			return nil, err
		}

		if !promptRetry(stdout) {
			return nil, err
		}
		fmt.Fprintln(stdout, "Retrying...")
	}
}

// isRetryable returns true for transient errors worth retrying (network failures,
// generic server errors). Returns false when the server returned a structured error
// with an error code — those are intentional responses that won't change on retry.
func isRetryable(err error) bool {
	var opErr *apierror.OperationalError
	if errors.As(err, &opErr) && opErr.ErrorCode != "" {
		return false
	}
	return true
}

// promptRetry asks the user if they want to retry the operation
func promptRetry(out io.Writer) bool {
	fmt.Fprint(out, "Would you like to retry? [y/N]: ")
	var input string
	if _, err := fmt.Scanln(&input); err != nil && input == "" {
		return false
	}
	input = strings.TrimSpace(strings.ToLower(input))
	return input == "y" || input == "yes"
}
