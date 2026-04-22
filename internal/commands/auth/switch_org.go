package auth

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/qlustered/qctl/internal/auth"
	"github.com/qlustered/qctl/internal/auth/oauth"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/org"
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
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			input := args[0]

			// Load config
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			// Get current context
			ctx, err := cfg.GetCurrentContext()
			if err != nil {
				return fmt.Errorf("no current context set: %w", err)
			}

			// Check if user has any cached organizations
			if len(ctx.Organizations) == 0 {
				return fmt.Errorf("no cached organizations found.\n\nPlease run 'qctl auth login' first to cache your organization list")
			}

			// Build org ID and name lists from cached organizations
			orgIDs := make([]string, len(ctx.Organizations))
			orgNames := make([]string, len(ctx.Organizations))
			for i, cachedOrg := range ctx.Organizations {
				orgIDs[i] = cachedOrg.ID
				orgNames[i] = cachedOrg.Name
			}

			// Resolve the input to an organization ID using the org resolver
			orgID, orgName, err := resolveOrganizationInteractive(input, orgIDs, orgNames)
			if err != nil {
				return err
			}

			// Try to bootstrap with auth to detect ops users
			cmdCtx, bootstrapErr := cmdutil.Bootstrap(cmd)
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

	cmd.Flags().StringVar(&tokenName, "token-name", "", "Name for this CLI token (for audit trail, ops users only)")
	cmd.Flags().BoolVar(&allowPlaintextStore, "allow-plaintext-token-store", false, "Allow storing tokens in plaintext file if keyring fails")

	return cmd
}

// switchOrgOps handles organization switching for ops users.
// It performs a device-flow re-authentication against Kinde, exchanges the
// resulting tokens for an ops-scoped CLI bearer token, and stores the credential.
func switchOrgOps(cmd *cobra.Command, cfg *config.Config, cmdCtx *cmdutil.CommandContext, authClient *auth.Client, targetOrgID, targetOrgName, tokenName string, allowPlaintextStore bool) error {
	// Resolve Kinde configuration
	kindeHost := config.ResolveKindeHost(cfg)
	kindeClientID := config.ResolveKindeClientID(cfg)
	if kindeClientID == "" {
		return fmt.Errorf("Auth configuration not available.\nRun 'qctl config set-context <name> --server <url>' to configure")
	}

	kindeClient := oauth.NewKindeClient(kindeHost, kindeClientID)

	// Request device code with openid scope (needed for id_token with auth_time)
	scopes := []string{"openid", "profile", "email"}
	fmt.Println("Ops user detected — re-authenticating to switch organization...")
	deviceAuth, err := kindeClient.RequestDeviceCode(cmd.Context(), scopes...)
	if err != nil {
		return fmt.Errorf("failed to initiate device auth: %w", err)
	}

	// Display verification URL and user code
	verificationURL := deviceAuth.VerificationURIComplete
	if verificationURL == "" {
		verificationURL = deviceAuth.VerificationURI
	}

	fmt.Printf("\n%s\n", strings.Repeat("=", 60))
	fmt.Printf("  To authenticate, open this URL in your browser:\n\n")
	fmt.Printf("    %s\n", verificationURL)
	if deviceAuth.UserCode != "" {
		fmt.Printf("\n  Verify this code matches: %s\n", deviceAuth.UserCode)
	}
	fmt.Printf("%s\n\n", strings.Repeat("=", 60))

	// Try to open browser
	if err := oauth.OpenBrowser(verificationURL); err != nil {
		fmt.Fprintf(os.Stderr, "(Could not open browser automatically)\n")
	}

	// Poll for token with progress indicator
	fmt.Print("Waiting for authentication")
	kindeTokens, err := kindeClient.PollForToken(cmd.Context(), deviceAuth.DeviceCode, deviceAuth.Interval, deviceAuth.ExpiresIn, func() {
		fmt.Print(".")
	})
	fmt.Println()

	if err != nil {
		return fmt.Errorf("authentication failed: %w", err)
	}

	if kindeTokens.IDToken == "" {
		return fmt.Errorf("no id_token received from Kinde (openid scope may not be configured)")
	}

	// Exchange Kinde tokens for ops-scoped CLI token
	fmt.Println("Obtaining CLI access token for target organization...")
	exchangeResp, err := authClient.ExchangeForOpsToken(cmd.Context(), kindeTokens.AccessToken, kindeTokens.IDToken, targetOrgID, tokenName)
	if err != nil {
		return fmt.Errorf("failed to exchange for ops CLI token: %w", err)
	}

	// Store credential for target org
	endpointKey, err := config.NormalizeEndpointKey(cmdCtx.ServerURL)
	if err != nil {
		return fmt.Errorf("failed to normalize endpoint: %w", err)
	}

	allowPlaintext := allowPlaintextStore || auth.IsPlaintextAllowed()
	credStore := auth.NewCredentialStore(allowPlaintext)

	cred := &auth.Credential{
		CreatedAt:      time.Now(),
		AccessToken:    exchangeResp.AccessToken,
		ExpiresAt:      time.Now().Add(time.Duration(exchangeResp.ExpiresIn) * time.Second),
		OrganizationID: exchangeResp.OrganizationID,
		TokenName:      tokenName,
	}

	if err := credStore.Store(endpointKey, cred); err != nil {
		return fmt.Errorf("failed to store credential: %w", err)
	}

	// Update context
	if err := updateContextOrganization(cfg, exchangeResp.OrganizationID, targetOrgName); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to update context: %v\n", err)
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
	fmt.Printf("Switched to %s\n", display)
	return nil
}

// switchOrgDefault handles organization switching for non-ops users.
// When cmdCtx and authClient are non-nil, it first calls the
// switch-organization-idp endpoint so the IDP reflects the new default.
// In all cases it updates the local config and hints to re-login.
func switchOrgDefault(cmd *cobra.Command, cfg *config.Config, cmdCtx *cmdutil.CommandContext, authClient *auth.Client, orgID, orgName string) error {
	if cmdCtx != nil && authClient != nil {
		if err := authClient.SwitchOrganizationIdp(cmd.Context(), cmdCtx.Credential.AccessToken, cmdCtx.OrganizationID, orgID); err != nil {
			return fmt.Errorf("failed to switch organization at IDP: %w", err)
		}
	}

	if err := updateContextOrganization(cfg, orgID, orgName); err != nil {
		return fmt.Errorf("failed to update config: %w", err)
	}

	if orgName != "" {
		fmt.Printf("Switched default organization to: %s\n", orgName)
	} else {
		fmt.Printf("Switched default organization to: %s\n", orgID)
	}

	verbosity, _ := cmd.Root().PersistentFlags().GetCount("verbose")
	if verbosity > 0 {
		fmt.Printf("Organization ID: %s\n", orgID)
	}

	fmt.Printf("\nNote: Run 'qctl auth login' if you need to authenticate with this organization.\n")
	return nil
}

// resolveOrganizationInteractive resolves input to an organization ID with interactive confirmation.
// This is different from org.Resolver.Resolve() which returns immediately on single fuzzy match.
func resolveOrganizationInteractive(input string, orgIDs, orgNames []string) (string, string, error) {
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
		return "", "", org.FormatNoMatchError(input, orgIDs, orgNames)
	case 1:
		// Single fuzzy match - ask for confirmation
		if confirmMatch(matches[0].Name) {
			return matches[0].ID, matches[0].Name, nil
		}
		return "", "", fmt.Errorf("organization switch cancelled")
	default:
		return "", "", org.FormatAmbiguousFuzzyError(input, matches)
	}
}

// confirmMatch prompts the user to confirm a fuzzy match
func confirmMatch(orgName string) bool {
	fmt.Printf("Did you mean '%s'? [y/N]: ", orgName)
	reader := bufio.NewReader(os.Stdin)
	response, err := reader.ReadString('\n')
	if err != nil {
		return false
	}

	response = strings.ToLower(strings.TrimSpace(response))
	return response == "y" || response == "yes"
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
