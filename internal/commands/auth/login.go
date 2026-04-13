package auth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/auth"
	"github.com/qlustered/qctl/internal/auth/oauth"
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
4. Store the access token securely in your OS keyring

The access token will be stored in the OS keyring if available,
or in a local file if plaintext storage is allowed.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Bootstrap without auth (we're logging in, so no existing credentials needed)
			cmdCtx, err := cmdutil.BootstrapWithoutAuth(cmd)
			if err != nil {
				return err
			}

			// Display login context
			displayLoginContext(cmdCtx.Config, cmdCtx.ServerURL)

			// Resolve Kinde configuration
			kindeHost := config.ResolveKindeHost(cmdCtx.Config)
			kindeClientID := config.ResolveKindeClientID(cmdCtx.Config)

			if kindeClientID == "" {
				return fmt.Errorf("Auth configuration not available.\nRun 'qctl config set-context <name> --server <url>' to configure")
			}

			// Perform OAuth login
			return performOAuthLogin(cmd.Context(), cmdCtx, kindeHost, kindeClientID, tokenName, allowPlaintextStore)
		},
	}

	addLoginFlags(cmd, &tokenName, &allowPlaintextStore)
	return cmd
}

// displayLoginContext prints the current context and server info.
func displayLoginContext(cfg *config.Config, serverURL string) {
	if cfg.CurrentContext != "" {
		fmt.Printf("Context: %s\n", cfg.CurrentContext)
	}
	fmt.Printf("Server:  %s\n", serverURL)
	fmt.Println()
}

// performOAuthLogin executes the OAuth 2.0 Device Authorization flow
func performOAuthLogin(ctx context.Context, cmdCtx *cmdutil.CommandContext, kindeHost, kindeClientID, tokenName string, allowPlaintextStore bool) error {
	// 1. Create Kinde client
	kindeClient := oauth.NewKindeClient(kindeHost, kindeClientID)

	// 2. Request device code
	fmt.Println("Initiating authentication...")
	deviceAuth, err := kindeClient.RequestDeviceCode(ctx)
	if err != nil {
		return fmt.Errorf("failed to initiate device auth: %w", err)
	}

	// 3. Display verification URL and user code
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

	// 4. Try to open browser
	if err := oauth.OpenBrowser(verificationURL); err != nil {
		fmt.Fprintf(os.Stderr, "(Could not open browser automatically)\n")
	}

	// 5. Poll for token with progress indicator
	fmt.Print("Waiting for authentication")
	kindeTokens, err := kindeClient.PollForToken(ctx, deviceAuth.DeviceCode, deviceAuth.Interval, deviceAuth.ExpiresIn, func() {
		fmt.Print(".")
	})
	fmt.Println() // newline after dots

	if err != nil {
		return fmt.Errorf("authentication failed: %w", err)
	}

	// 6. Exchange Kinde token for Atlas CLI token (with retry on failure)
	fmt.Println("Obtaining CLI access token...")
	authClient := auth.NewClient(cmdCtx.ServerURL, cmdCtx.Verbosity)
	exchangeResp, err := exchangeWithRetry(ctx, authClient, kindeTokens.AccessToken, tokenName)
	if err != nil {
		return fmt.Errorf("failed to exchange for CLI token: %w", err)
	}

	// 7. Store credential
	if err := storeCredential(cmdCtx.ServerURL, exchangeResp, tokenName, allowPlaintextStore); err != nil {
		return err
	}

	// 8. Fetch user info to cache organization names
	fmt.Println("Fetching organization info...")
	userInfo, err := authClient.GetMe(exchangeResp.AccessToken, exchangeResp.OrganizationID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not fetch user info: %v\n", err)
	}

	// 9. Update context with organization info
	if err := updateContextAfterLogin(cmdCtx.Config, exchangeResp, userInfo); err != nil {
		// Non-fatal: warn but continue
		fmt.Fprintf(os.Stderr, "Warning: failed to update context: %v\n", err)
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
	fmt.Printf("\nSuccessfully logged in to organization %s\n", orgDisplay)
	return nil
}

// storeCredential stores the access token in the credential store
func storeCredential(serverURL string, exchangeResp *auth.CLIExchangeResponse, tokenName string, allowPlaintextStore bool) error {
	endpointKey, err := config.NormalizeEndpointKey(serverURL)
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

	return nil
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
	cmd.Flags().StringVar(tokenName, "token-name", "", "Name for this CLI token (for audit trail)")
	cmd.Flags().BoolVar(allowPlaintextStore, "allow-plaintext-token-store", false, "Allow storing tokens in plaintext file if keyring fails")
}

// exchangeWithRetry attempts the token exchange and prompts for retry on transient failures.
// Permanent errors (4xx, auth issues, account setup errors) are returned immediately
// without offering retry, since retrying won't help.
func exchangeWithRetry(ctx context.Context, authClient *auth.Client, kindeAccessToken, tokenName string) (*auth.CLIExchangeResponse, error) {
	for {
		resp, err := authClient.ExchangeForCLIToken(ctx, kindeAccessToken, tokenName)
		if err == nil {
			return resp, nil
		}

		fmt.Fprintf(os.Stderr, "Error: %v\n", err)

		// Only offer retry for transient errors (network issues, server 5xx).
		// Permanent errors (4xx → bad usage / not found / unauthorized) won't
		// be fixed by retrying from the client.
		if !isRetryable(err) {
			return nil, err
		}

		if !promptRetry() {
			return nil, err
		}
		fmt.Println("Retrying...")
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
func promptRetry() bool {
	fmt.Print("Would you like to retry? [y/N]: ")
	var input string
	fmt.Scanln(&input)
	input = strings.TrimSpace(strings.ToLower(input))
	return input == "y" || input == "yes"
}
