package cmdutil

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/auth"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/org"
	"github.com/spf13/cobra"
)

// CommandContext bundles resolved auth context for commands.
// It contains all the commonly needed values that are resolved
// from flags, environment variables, and configuration.
type CommandContext struct {
	Config           *config.Config
	Credential       *auth.Credential
	ServerURL        string
	Verbosity        int    // 0=off, 1-7=reserved, 8=curl with redacted tokens, 9=curl with full tokens
	OrganizationID   string // Resolved org UUID
	OrganizationName string // Resolved org name (for display)
}

// Bootstrap resolves config, server, credentials, and organization from command flags.
// This replaces the ~50-line boilerplate block that appears in most commands.
//
// It performs the following steps:
//  1. Load configuration from disk
//  2. Resolve server URL (flag > env > config)
//  3. Validate server URL (HTTPS required unless --allow-insecure-http)
//  4. Resolve organization (flag > env > config)
//  5. Retrieve stored credentials for the server/org
//  6. Check token expiration
//  7. Get verbose flag from root command
func Bootstrap(cmd *cobra.Command) (*CommandContext, error) {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	// Resolve server
	serverFlag, _ := cmd.Flags().GetString("server")
	allowInsecure := config.IsInsecureHTTPAllowed()
	if f, _ := cmd.Flags().GetBool("allow-insecure-http"); f {
		allowInsecure = true
	}

	serverURL, err := config.ResolveServer(cfg, serverFlag)
	if err != nil {
		return nil, err
	}

	// Validate server URL
	if err = config.ValidateServer(serverURL, allowInsecure); err != nil {
		return nil, err
	}

	// Resolve organization
	orgFlag, _ := cmd.Root().PersistentFlags().GetString("org")
	orgID, orgName, err := resolveOrganization(cfg, orgFlag)
	if err != nil {
		return nil, err
	}

	// Get endpoint key
	endpointKey, err := config.NormalizeEndpointKey(serverURL)
	if err != nil {
		return nil, fmt.Errorf("failed to normalize endpoint: %w", err)
	}

	// Check if plaintext storage is allowed
	allowPlaintext := auth.IsPlaintextAllowed()
	credStore := auth.NewCredentialStore(allowPlaintext)

	// Retrieve credential (keyed by endpoint + org)
	cred, err := credStore.Retrieve(endpointKey, orgID)
	if err != nil {
		return nil, fmt.Errorf("not logged in: %w\nPlease run 'qctl auth login' first", err)
	}

	// Check token expiration
	if cred.IsExpired() {
		return nil, fmt.Errorf("token expired, please run 'qctl auth login'")
	}

	// Get verbosity level (count flag)
	verbosity, _ := cmd.Root().PersistentFlags().GetCount("verbose")

	return &CommandContext{
		Config:           cfg,
		ServerURL:        serverURL,
		Credential:       cred,
		Verbosity:        verbosity,
		OrganizationID:   orgID,
		OrganizationName: orgName,
	}, nil
}

// BootstrapWithoutAuth resolves config and server without requiring authentication.
// Useful for commands like 'login' that don't need existing credentials.
func BootstrapWithoutAuth(cmd *cobra.Command) (*CommandContext, error) {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	// Resolve server
	serverFlag, _ := cmd.Flags().GetString("server")
	allowInsecure := config.IsInsecureHTTPAllowed()
	if f, _ := cmd.Flags().GetBool("allow-insecure-http"); f {
		allowInsecure = true
	}

	serverURL, err := config.ResolveServer(cfg, serverFlag)
	if err != nil {
		return nil, err
	}

	// Validate server URL
	if err = config.ValidateServer(serverURL, allowInsecure); err != nil {
		return nil, err
	}

	// Get verbosity level (count flag)
	verbosity, _ := cmd.Root().PersistentFlags().GetCount("verbose")

	return &CommandContext{
		Config:    cfg,
		ServerURL: serverURL,
		Verbosity: verbosity,
	}, nil
}

// resolveOrganization resolves an organization identifier to ID and name.
// The input can be:
//   - A full UUID (used directly)
//   - An organization name or UUID prefix (resolved using cached org list)
//
// Returns (orgID, orgName, error).
func resolveOrganization(cfg *config.Config, orgFlag string) (string, string, error) {
	// Get raw org input from flag/env/context
	rawOrg, err := config.ResolveOrganization(cfg, orgFlag)
	if err != nil {
		return "", "", err
	}

	// Try to parse as UUID - if valid, it's a direct org ID
	if _, parseErr := uuid.Parse(rawOrg); parseErr == nil {
		// It's a valid UUID - find the name from cache if available
		ctx, _ := cfg.GetCurrentContext()
		if ctx != nil {
			for _, cachedOrg := range ctx.Organizations {
				if cachedOrg.ID == rawOrg {
					return rawOrg, cachedOrg.Name, nil
				}
			}
		}
		// UUID is valid but not in cache - still return it without name
		return rawOrg, "", nil
	}

	// Not a valid UUID - need to resolve using org.Resolver
	ctx, err := cfg.GetCurrentContext()
	if err != nil {
		return "", "", fmt.Errorf("cannot resolve organization '%s': no context configured", rawOrg)
	}

	// Build org ID and name lists from cached organizations
	if len(ctx.Organizations) == 0 {
		return "", "", fmt.Errorf("cannot resolve organization '%s': no cached organizations.\n\nPlease run 'qctl auth login' to populate the organization cache,\nor use a full organization UUID", rawOrg)
	}

	orgIDs := make([]string, len(ctx.Organizations))
	orgNames := make([]string, len(ctx.Organizations))
	for i, cachedOrg := range ctx.Organizations {
		orgIDs[i] = cachedOrg.ID
		orgNames[i] = cachedOrg.Name
	}

	// Use resolver to find match
	resolver := org.NewResolver(orgIDs, orgNames)
	return resolver.Resolve(rawOrg)
}
