package cmdutil

import (
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/auth"
	"github.com/qlustered/qctl/internal/client"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
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

	return BootstrapFromConfig(cmd, cfg)
}

// BootstrapFromConfig is like Bootstrap, but uses a pre-loaded config.
// Useful for callers that need to apply in-memory overrides (e.g. a --context
// flag that switches the active context for a single command) before
// resolving server/org/credentials.
func BootstrapFromConfig(cmd *cobra.Command, cfg *config.Config) (*CommandContext, error) {
	// Resolve server
	serverFlag, _ := cmd.Flags().GetString("server")
	allowInsecure := config.IsInsecureHTTPAllowed()
	if f, _ := cmd.Flags().GetBool("allow-insecure-http"); f {
		allowInsecure = true
	}

	serverURL, serverSource, err := config.ResolveServerWithSource(cfg, serverFlag)
	if err != nil {
		return nil, err
	}

	// Validate server URL
	if err = config.ValidateServer(serverURL, allowInsecure); err != nil {
		return nil, err
	}
	client.SetServerSource(serverURL, serverSource)

	endpointKey, err := config.NormalizeEndpointKey(serverURL)
	if err != nil {
		return nil, fmt.Errorf("failed to normalize endpoint: %w", err)
	}
	preferPlaintext := auth.IsPlaintextAllowed()
	credStore := auth.NewCredentialStore(preferPlaintext)

	// Resolve organization
	orgFlag, _ := cmd.Root().PersistentFlags().GetString("org")
	orgID, orgName, source, err := resolveScopedOrganization(cfg, orgFlag)
	if err != nil {
		if raw, _, _ := config.OrganizationFlagOrDefault(cfg, orgFlag); raw == "" {
			if authErr := checkServerCredentials(cfg, credStore, endpointKey, serverURL); authErr != nil {
				return nil, authErr
			}
		}
		return nil, err
	}

	// Retrieve credential (keyed by endpoint + org)
	cred, err := credStore.Retrieve(endpointKey, orgID)
	if err != nil {
		// Only a genuinely absent credential is "not logged in". Reporting an
		// unreadable or unparseable store that way attaches advice -- run
		// 'qctl auth login' -- that overwrites the credentials file, destroying
		// the token_name that is the only handle qctl has on a CLI session
		// which may still be live on the server.
		if !errors.Is(err, auth.ErrCredentialNotFound) {
			return nil, unreadableCredentialError(err, auth.MakeCredentialKey(endpointKey, orgID), !preferPlaintext)
		}
		return nil, authFailure(err, "not logged in", serverURL, orgID, orgName, source)
	}

	// Check token expiration
	if cred.IsExpired() {
		return nil, authFailure(nil, "token expired", serverURL, orgID, orgName, source)
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

	serverURL, serverSource, err := config.ResolveServerWithSource(cfg, serverFlag)
	if err != nil {
		return nil, err
	}

	// Validate server URL
	if err = config.ValidateServer(serverURL, allowInsecure); err != nil {
		return nil, err
	}
	client.SetServerSource(serverURL, serverSource)

	// Get verbosity level (count flag)
	verbosity, _ := cmd.Root().PersistentFlags().GetCount("verbose")

	return &CommandContext{
		Config:    cfg,
		ServerURL: serverURL,
		Verbosity: verbosity,
	}, nil
}

// ResolveOrganization resolves an organization identifier — a full UUID, a
// name, or a UUID prefix — to (id, name), using the context's cached
// organization list. Exported for commands that cannot use Bootstrap because
// they must still work without a valid credential (auth logout).
func ResolveOrganization(cfg *config.Config, orgFlag string) (string, string, error) {
	return resolveOrganization(cfg, orgFlag)
}

// ResolveOrganizationWithSource is ResolveOrganization plus the setting that
// selected the organization. It records nothing for the API error notes.
func ResolveOrganizationWithSource(cfg *config.Config, orgFlag string) (string, string, config.OrgSource, error) {
	return resolveOrganizationWithSource(cfg, orgFlag)
}

// resolveOrganization resolves and records the invocation's organization.
func resolveOrganization(cfg *config.Config, orgFlag string) (string, string, error) {
	id, name, _, err := resolveScopedOrganization(cfg, orgFlag)
	return id, name, err
}

func resolveScopedOrganization(cfg *config.Config, orgFlag string) (string, string, config.OrgSource, error) {
	orgID, orgName, source, err := resolveOrganizationWithSource(cfg, orgFlag)
	if err != nil {
		return "", "", "", err
	}

	// The name when the cache or matching context has one, the UUID when
	// neither does: an unnamed UUID resolves with an empty name, and
	// "Scoped to organization  (from ...)" reads as a bug.
	display := orgName
	if display == "" {
		display = orgID
	}
	apierror.SetOrgScope(display, string(source))

	return orgID, orgName, source, nil
}

// resolveOrganizationWithSource resolves the selected organization and its source.
func resolveOrganizationWithSource(cfg *config.Config, orgFlag string) (string, string, config.OrgSource, error) {
	// Get raw org input from flag/env/context
	rawOrg, source, err := config.OrganizationFlagOrDefault(cfg, orgFlag)
	if err != nil {
		return "", "", "", err
	}

	// Try to parse as UUID - if valid, it's a direct org ID
	if _, parseErr := uuid.Parse(rawOrg); parseErr == nil {
		// It's a valid UUID - find the name from cache or the matching context.
		ctx, _ := cfg.GetCurrentContext()
		if ctx != nil {
			for _, cachedOrg := range ctx.Organizations {
				if cachedOrg.ID == rawOrg && cachedOrg.Name != "" {
					return rawOrg, cachedOrg.Name, source, nil
				}
			}
			if ctx.Organization == rawOrg {
				return rawOrg, ctx.OrganizationName, source, nil
			}
		}
		// UUID is valid but unnamed - still return it without a name.
		return rawOrg, "", source, nil
	}

	// Not a valid UUID - need to resolve using org.Resolver
	ctx, err := cfg.GetCurrentContext()
	if err != nil {
		return "", "", "", qerrors.NewBadUsagef("cannot resolve organization %q: no context configured.\n"+
			"Ask the user which organization to use, then pass --org <full-organization-UUID>.", rawOrg)
	}

	// Build org ID and name lists from cached organizations
	if len(ctx.Organizations) == 0 {
		return "", "", "", qerrors.NewBadUsagef("cannot resolve organization %q: no cached organizations.\n"+
			"Ask the user which organization to use, then pass --org <full-organization-UUID>.", rawOrg)
	}

	orgIDs := make([]string, len(ctx.Organizations))
	orgNames := make([]string, len(ctx.Organizations))
	for i, cachedOrg := range ctx.Organizations {
		orgIDs[i] = cachedOrg.ID
		orgNames[i] = cachedOrg.Name
	}

	// Use resolver to find match
	resolver := org.NewResolver(orgIDs, orgNames)
	id, name, err := resolver.Resolve(rawOrg)
	if err != nil {
		return "", "", "", err
	}
	return id, name, source, nil
}

// unreadableCredentialError reports a credential store that exists but could
// not be read, as distinct from one that holds nothing. It deliberately does
// NOT tell the user to log in again: that overwrites the credentials file and
// destroys whatever is still recoverable from it.
//
// The location is a best effort. In the default (keyring) mode Retrieve tries
// the keyring and file, selecting the newer credential, so either can be the source;
// the wrapped cause says which, and the message names both places to look.
//
// keyringConsulted says whether Retrieve asked the keyring at all. Under the
// plaintext opt-in it does not -- it reads the file and nothing else -- so the
// failure is necessarily the file's, and sending the user off to inspect a
// keyring qctl never touched is noise on a message they are already reading
// because something went wrong.
func unreadableCredentialError(cause error, credKey string, keyringConsulted bool) error {
	location := auth.CredentialsFileLocation()
	where := fmt.Sprintf("Inspect %s.", location)
	if keyringConsulted {
		// The keyring half names the ENTRY, and only conditionally: this error
		// is also reached when the keyring REFUSED a read, which establishes
		// nothing about what it holds. Naming the service instead would point
		// the user at every credential they have, on every server and in every
		// org.
		where = fmt.Sprintf(
			"Inspect %s, and your OS keyring's entry for this server and org if it has\n"+
				"one: service %q, entry %q. Other servers and orgs are separate entries\n"+
				"under the same service, so do not remove the service itself.",
			location, auth.KeyringServiceName, credKey,
		)
	}
	advice := "This is not the same as being logged out.\n" + where + "\n" +
		"Do NOT run 'qctl auth login' until you have: it overwrites that file, and\n" +
		"with it the name of a CLI session that may still be live on the server."
	return fmt.Errorf("failed to read the stored credential: %w\n\n%s", cause, advice)
}
