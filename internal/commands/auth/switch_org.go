package auth

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/auth"
	"github.com/qlustered/qctl/internal/auth/oauth"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/org"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// NewSwitchOrgCommand creates the auth switch-org command
func NewSwitchOrgCommand() *cobra.Command {
	var (
		tokenName           string
		allowPlaintextStore bool
	)

	cmd := &cobra.Command{
		Use:   "switch-org <org_id_or_name>",
		Short: "Switch the default organization",
		Long: `Switch the default organization for the current context.

You can specify the organization by:
  - Full organization name (exact, case-sensitive match)
  - Organization UUID (full or prefix, like git commit hashes)
  - Partial name (fuzzy match with confirmation)

For ops users, this command automatically authenticates with the target
organization via a device-flow re-authentication. For regular users, it
updates the local configuration and hints to re-login if needed.

Examples:
  qctl auth switch-org "Acme Corporation"
  qctl auth switch-org 550e8400
  qctl auth switch-org 550e8400-e29b-41d4-a716-446655440000`,
		Args:        cobra.ExactArgs(1),
		Annotations: cmdutil.WithFormats(catalog.For("org"), "table"), // NOT session: it is org's only write verb
		RunE: func(cmd *cobra.Command, args []string) error {
			input := args[0]
			if err := auth.ValidateTokenNameBase(tokenName); err != nil {
				return err
			}

			// Load config
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			// Get current context
			ctx, err := cfg.GetCurrentContext()
			if err != nil {
				return err
			}

			// Check if user has any cached organizations
			if len(ctx.Organizations) == 0 {
				return qerrors.NewBadUsage("no cached organizations found.\n" + apierror.LoginHint)
			}

			// Build org ID and name lists from cached organizations
			orgIDs := make([]string, len(ctx.Organizations))
			orgNames := make([]string, len(ctx.Organizations))
			for i, cachedOrg := range ctx.Organizations {
				orgIDs[i] = cachedOrg.ID
				orgNames[i] = cachedOrg.Name
			}

			// Resolve the input to an organization ID using the org resolver
			orgID, orgName, err := resolveOrganizationInteractive(cmd, input, orgIDs, orgNames)
			if err != nil {
				return err
			}

			// Try to bootstrap with auth to detect ops users
			cmdCtx, bootstrapErr := cmdutil.Bootstrap(cmd)

			// Bootstrap records the CURRENT organization as this
			// invocation's scope. From here on the command straddles two
			// organizations -- the current one in the request path and in
			// the whoami just below, the target in the
			// switch-organization-idp body -- and a 403 can be about either
			// of them:
			//
			//   - about the TARGET: "Not a member of target organization"
			//     (atlas switch_organization_idp, capi/capi/views/user_view.py),
			//     raised on a URL whose path org is the CURRENT one.
			//   - about the CURRENT org: the route's ORG_VIEW dependency
			//     (switch_organization_idp in user_view.py) rejects first,
			//     with "Bearer token is not authorized for this organization."
			//     (_authenticate_bearer_and_get_user_async,
			//     capi/capi/auth/permissions.py) or the permission-denied
			//     message (require_permission_async's dependency).
			//
			// Both reach the user through switchOrgDefault's
			// "failed to switch organization at IDP" wrap, unchanged
			// either way. A note naming the current org would be wrong
			// in the first case and right in the second, and nothing here
			// can tell which happened. What settles it is the note's advice,
			// which is wrong in both: "do not switch organizations or retry
			// with `--org`", printed by the command whose whole purpose is
			// switching organizations. So: no note. (The whoami's own error
			// is swallowed and never printed either way.)
			//
			// Clearing makes ScopeNote() return "" -- a documented,
			// first-class answer (apierror/scope.go) -- so no note ships.
			// Unconditional on purpose: it covers the ops branch as well as
			// this one, and Bootstrap can fail AFTER resolveOrganization has
			// already recorded a scope.
			apierror.SetOrgScope("", "")

			if bootstrapErr == nil {
				// We have a valid credential — check if user is ops
				authClient := auth.NewClient(cmdCtx.ServerURL, cmdCtx.Verbosity)
				userInfo, meErr := authClient.GetMe(cmdCtx.Credential.AccessToken, cmdCtx.OrganizationID)
				if meErr == nil {
					if userInfo.Role == "ops" {
						return switchOrgOps(cmd, cfg, cmdCtx, authClient, orgID, orgName, tokenName, allowPlaintextStore)
					}
					// Non-ops with valid auth: update IDP's default organization.
					return switchOrgDefault(cmd, cfg, cmdCtx, authClient, orgID, orgName)
				}
			}

			// Bootstrap or GetMe failed: update local config only.
			return switchOrgDefault(cmd, cfg, nil, nil, orgID, orgName)
		},
	}

	cmd.Flags().StringVar(&tokenName, "token-name", "", "Name for this CLI token (for audit trail, ops users only); qctl appends a unique suffix so 'qctl auth logout' can revoke this session")
	cmd.Flags().BoolVar(&allowPlaintextStore, "allow-plaintext-token-store", false, "Store the token in a 0600 file without writing to the OS keyring; check for an older entry unless QCTL_ALLOW_PLAINTEXT_TOKENS is set")
	cmd.Flags().Bool("yes", false, "Confirm a fuzzy organization name without prompting")

	return cmd
}

// switchOrgOps handles organization switching for ops users.
// It performs a device-flow re-authentication against Kinde, exchanges the
// resulting tokens for an ops-scoped CLI bearer token, and stores the credential.
func switchOrgOps(cmd *cobra.Command, cfg *config.Config, cmdCtx *cmdutil.CommandContext, authClient *auth.Client, targetOrgID, targetOrgName, tokenName string, allowPlaintextStore bool) error {
	stdout, stderr := cmd.OutOrStdout(), cmd.ErrOrStderr()

	// Resolve Kinde configuration
	kindeHost := config.ResolveKindeHost(cfg)
	kindeClientID := config.ResolveKindeClientID(cfg)
	if kindeClientID == "" {
		//nolint:staticcheck // Preserve the established multi-line CLI guidance verbatim.
		return fmt.Errorf("Auth configuration not available.\nRun 'qctl config set-context <name> --server <url>' to configure")
	}

	kindeTokens, err := reauthenticateWithKinde(cmd.Context(), stdout, stderr, kindeHost, kindeClientID)
	if err != nil {
		return err
	}

	// Exchange Kinde tokens for ops-scoped CLI token. The name is minted before
	// the exchange because the same string has to reach both the server and the
	// stored credential: it is the only handle logout will have on this session.
	mintedName, err := auth.MintTokenName(tokenName)
	if err != nil {
		return err
	}

	fmt.Fprintln(stdout, "Obtaining CLI access token for target organization...")
	exchangeResp, err := authClient.ExchangeForOpsToken(cmd.Context(), kindeTokens.AccessToken, kindeTokens.IDToken, targetOrgID, mintedName)
	if err != nil {
		return fmt.Errorf("failed to exchange for ops CLI token: %w", err)
	}

	// Store the target credential and retire any session it replaces.
	storeResult, err := storeCredential(
		cmd.Context(), cmdCtx.ServerURL, cmdCtx.Verbosity,
		exchangeResp, mintedName, allowPlaintextStore,
	)
	if err != nil {
		return err
	}
	reportStoreResult(stdout, stderr, storeResult, "the target organization")

	// Update context
	if err := updateContextOrganization(cfg, exchangeResp.OrganizationID, targetOrgName); err != nil {
		fmt.Fprintf(stderr, "Warning: failed to update context: %v\n", err)
	}

	// Fetch GetMe for target org to update org cache
	targetUserInfo, err := authClient.GetMe(exchangeResp.AccessToken, exchangeResp.OrganizationID)
	if err == nil {
		_ = updateContextAfterLogin(cfg, exchangeResp, targetUserInfo)
	}

	display := targetOrgName
	if display == "" {
		display = targetOrgID
	}
	fmt.Fprintf(stdout, "Switched to %s\n", display)
	return nil
}

// reauthenticateWithKinde runs the Kinde device flow for an ops user who is
// switching organization: it prints the verification URL, tries to open a
// browser, and polls until Kinde issues tokens. The openid scope is what makes
// Kinde return an id_token with auth_time, which the ops exchange requires.
func reauthenticateWithKinde(ctx context.Context, stdout, stderr io.Writer, kindeHost, kindeClientID string) (*oauth.KindeTokens, error) {
	kindeClient := oauth.NewKindeClient(kindeHost, kindeClientID)

	scopes := []string{"openid", "profile", "email"}
	fmt.Fprintln(stdout, "Ops user detected — re-authenticating to switch organization...")
	deviceAuth, err := kindeClient.RequestDeviceCode(ctx, scopes...)
	if err != nil {
		return nil, fmt.Errorf("failed to initiate device auth: %w", err)
	}

	kindeTokens, err := promptAndPollForKindeTokens(ctx, stdout, stderr, kindeClient, deviceAuth)
	if err != nil {
		return nil, err
	}

	if kindeTokens.IDToken == "" {
		return nil, fmt.Errorf("no id_token received from Kinde (openid scope may not be configured)")
	}

	return kindeTokens, nil
}

// switchOrgDefault handles organization switching for non-ops users.
// When cmdCtx and authClient are non-nil, it first calls the
// switch-organization-idp endpoint so the IDP reflects the new default.
// In all cases it updates the local config and hints to re-login.
func switchOrgDefault(cmd *cobra.Command, cfg *config.Config, cmdCtx *cmdutil.CommandContext, authClient *auth.Client, orgID, orgName string) error {
	stdout := cmd.OutOrStdout()

	if cmdCtx != nil && authClient != nil {
		if err := authClient.SwitchOrganizationIdp(cmd.Context(), cmdCtx.Credential.AccessToken, cmdCtx.OrganizationID, orgID); err != nil {
			return fmt.Errorf("failed to switch organization at IDP: %w", err)
		}
	}

	if err := updateContextOrganization(cfg, orgID, orgName); err != nil {
		return fmt.Errorf("failed to update config: %w", err)
	}

	if orgName != "" {
		fmt.Fprintf(stdout, "Switched default organization to: %s\n", terminal.Sanitize(orgName))
	} else {
		fmt.Fprintf(stdout, "Switched default organization to: %s\n", orgID)
	}

	verbosity, _ := cmd.Root().PersistentFlags().GetCount("verbose")
	if verbosity > 0 {
		fmt.Fprintf(stdout, "Organization ID: %s\n", orgID)
	}

	fmt.Fprintf(stdout, "\nIf authentication is needed: %s\n", apierror.LoginHint)
	return nil
}

// resolveOrganizationInteractive resolves input to an organization ID with interactive confirmation.
// This is different from org.Resolver.Resolve() which returns immediately on single fuzzy match.
func resolveOrganizationInteractive(cmd *cobra.Command, input string, orgIDs, orgNames []string) (string, string, error) {
	resolver := org.NewResolver(orgIDs, orgNames)

	// First, try exact and UUID matches (these don't need confirmation)
	// Check exact name match
	for i, name := range orgNames {
		if name == input && i < len(orgIDs) {
			return orgIDs[i], name, nil
		}
	}

	// Check full UUID match
	for i, id := range orgIDs {
		if strings.EqualFold(id, input) {
			name := ""
			if i < len(orgNames) {
				name = orgNames[i]
			}
			return id, name, nil
		}
	}

	// Try UUID prefix match
	if org.IsUUIDLike(input) {
		matches := resolver.FindUUIDMatches(input)
		if len(matches) == 1 {
			return matches[0].ID, matches[0].Name, nil
		} else if len(matches) > 1 {
			return "", "", org.FormatAmbiguousUUIDError(input, matches)
		}
	}

	// Try fuzzy name matching - this is where we add interactive confirmation
	matches := resolver.FindFuzzyNameMatches(input)
	switch len(matches) {
	case 0:
		return "", "", org.FormatNoMatchError(input)
	case 1:
		// Single fuzzy match - ask for confirmation
		if err := cmdutil.ConfirmOrganizationMatch(cmd, input, matches[0].ID, matches[0].Name); err != nil {
			return "", "", err
		}
		return matches[0].ID, matches[0].Name, nil
	default:
		return "", "", org.FormatAmbiguousFuzzyError(input, matches)
	}
}

// updateContextOrganization updates the current context with the new organization
func updateContextOrganization(cfg *config.Config, orgID, orgName string) error {
	if cfg.CurrentContext == "" {
		return nil // No context to update
	}

	ctx, err := cfg.GetCurrentContext()
	if err != nil {
		return err
	}

	ctx.Organization = orgID
	ctx.OrganizationName = orgName

	return cfg.Save()
}
