package config

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/org"
	"github.com/qlustered/qctl/internal/schema/cache"
	"github.com/spf13/cobra"
)

func newSetContextCommand() *cobra.Command {
	var (
		server     string
		output     string
		orgInput   string
		useCurrent bool
	)

	cmd := &cobra.Command{
		Use:   "set-context [name]",
		Short: "Create or update a context",
		Long: `Create a new context or update an existing context with the specified configuration.

At minimum, the --server flag must be provided when creating a new context.
The API endpoint will be validated before the context is saved.
After creating a new context, it will automatically become the current context.

Use --current to modify the current context without specifying its name.`,
		Example: `  # Create a new context for production
  qctl config set-context production --server https://api.qluster.ai

  # Create a context for local development
  qctl config set-context local --server http://localhost:8000

  # Update an existing context's output format
  qctl config set-context production --output json

  # Set the default organization for the current context
  qctl config set-context --current --org "Acme Corporation"

  # Set organization by UUID prefix
  qctl config set-context --current --org 550e8400`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			// Determine context name from args or --current flag
			var contextName string
			if useCurrent {
				if len(args) > 0 {
					return fmt.Errorf("cannot specify both --current and a context name")
				}
				if cfg.CurrentContext == "" {
					return fmt.Errorf("no current context set; use 'qctl config set-context <name> --server <url>' to create one")
				}
				contextName = cfg.CurrentContext
			} else {
				if len(args) == 0 {
					return fmt.Errorf("context name required (or use --current to modify the current context)")
				}
				contextName = args[0]
			}

			// Get existing context or create new one
			ctx, exists := cfg.Contexts[contextName]
			if !exists {
				// New context - server is required
				if server == "" {
					return fmt.Errorf("--server is required when creating a new context")
				}
				ctx = &config.Context{}
			}

			// Update context fields if provided
			if server != "" {
				// Strip trailing slash to prevent URL path issues (e.g., api//token)
				server = strings.TrimSuffix(server, "/")

				// Validate server URL format
				allowInsecure := config.IsInsecureHTTPAllowed()
				if flagValue, _ := cmd.Flags().GetBool("allow-insecure-http"); flagValue {
					allowInsecure = true
				}

				if err := config.ValidateServer(server, allowInsecure); err != nil {
					return err
				}

				// Validate that the API endpoint exists by making a request
				if err := validateAPIEndpoint(server); err != nil {
					return fmt.Errorf("failed to validate API endpoint: %w", err)
				}

				// Fetch and cache the OpenAPI spec for this server
				version, err := ensureSpecCached(cmd.Context(), server)
				if err != nil {
					return fmt.Errorf("failed to cache API schema: %w", err)
				}
				fmt.Printf("Cached OpenAPI spec for API version %s\n", version)

				// Fetch and cache auth config (Kinde host/client ID)
				authConfig, err := fetchAuthConfig(server)
				if err != nil {
					// Warn but don't fail - auth config is optional for endpoint validation
					fmt.Fprintf(os.Stderr, "Warning: could not fetch auth config: %v\n", err)
				} else {
					ctx.KindeHost = authConfig.KindeHost
					ctx.KindeClientID = authConfig.KindeClientID
				}

				ctx.Server = server
			}

			if output != "" {
				ctx.Output = output
			}

			// Handle --org flag: resolve org name/prefix to UUID
			if orgInput != "" {
				orgID, orgName, err := resolveOrgInput(ctx, orgInput)
				if err != nil {
					return err
				}
				ctx.Organization = orgID
				ctx.OrganizationName = orgName
			}

			// Save context
			cfg.SetContext(contextName, ctx)

			// Always switch to the new/updated context
			cfg.CurrentContext = contextName

			if err := cfg.Save(); err != nil {
				return fmt.Errorf("failed to save config: %w", err)
			}

			if exists {
				fmt.Printf("Updated context %q\n", contextName)
			} else {
				fmt.Printf("Created context %q\n", contextName)
			}
			fmt.Printf("Switched to context %q\n", contextName)

			return nil
		},
	}

	cmd.Flags().StringVar(&server, "server", "", "API server URL (e.g., http://localhost:8000)")
	cmd.Flags().StringVar(&output, "output", "", "Default output format (table|json|yaml|name)")
	cmd.Flags().StringVar(&orgInput, "org", "", "Default organization (name or UUID/prefix)")
	cmd.Flags().BoolVar(&useCurrent, "current", false, "Modify the current context (no name argument needed)")
	cmd.Flags().Bool("allow-insecure-http", false, "Allow non-localhost http:// endpoints")

	return cmd
}

// ValidateAPIEndpointFunc is a variable that can be overridden in tests
var ValidateAPIEndpointFunc = validateAPIEndpointDefault

// validateAPIEndpoint validates that the API endpoint exists by making a request
func validateAPIEndpoint(serverURL string) error {
	return ValidateAPIEndpointFunc(serverURL)
}

// EnsureSpecCachedFunc is a variable that can be overridden in tests
var EnsureSpecCachedFunc = cache.EnsureSpecCached

// ensureSpecCached fetches and caches the OpenAPI spec for the given server
func ensureSpecCached(ctx context.Context, serverURL string) (string, error) {
	return EnsureSpecCachedFunc(ctx, serverURL)
}

// validateAPIEndpointDefault is the default implementation that makes an HTTP request
func validateAPIEndpointDefault(serverURL string) error {
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	// Try to reach the API version endpoint to validate the server
	// We use /api/version since that's a known endpoint that doesn't require auth
	versionURL := serverURL + "/api/version"
	resp, err := client.Get(versionURL)
	if err != nil {
		return fmt.Errorf("cannot reach API endpoint %q: %w", serverURL, err)
	}
	defer resp.Body.Close()

	// Accept any response that indicates the server is reachable
	// Even 401/403/404 means the server exists and is responding
	// Only reject if we get a 5xx error or connection-level failures
	if resp.StatusCode >= 500 {
		return fmt.Errorf("API endpoint %q returned server error: %s", serverURL, resp.Status)
	}

	return nil
}

// resolveOrgInput resolves org input (name, UUID, or prefix) to an org ID and name.
// It uses the cached organizations in the context for resolution.
func resolveOrgInput(ctx *config.Context, input string) (string, string, error) {
	// If input is a valid UUID, use it directly
	if _, err := uuid.Parse(input); err == nil {
		// Look up name from cache if available
		for _, ref := range ctx.Organizations {
			if ref.ID == input {
				return input, ref.Name, nil
			}
		}
		// Valid UUID but not in cache - use it anyway (might be a new org)
		return input, "", nil
	}

	// No cached organizations - can't resolve
	if len(ctx.Organizations) == 0 {
		return "", "", fmt.Errorf("no cached organizations available.\nLogin first with 'qctl auth login' to cache organization list")
	}

	// Build resolver from cached organizations
	orgIDs := make([]string, len(ctx.Organizations))
	orgNames := make([]string, len(ctx.Organizations))
	for i, ref := range ctx.Organizations {
		orgIDs[i] = ref.ID
		orgNames[i] = ref.Name
	}

	resolver := org.NewResolver(orgIDs, orgNames)
	return resolver.Resolve(input)
}

// AuthConfig holds the authentication configuration from the backend
type AuthConfig struct {
	KindeHost     string `json:"kinde_host"`
	KindeClientID string `json:"kinde_cli_client_id"`
}

// FetchAuthConfigFunc is a variable that can be overridden in tests
var FetchAuthConfigFunc = fetchAuthConfigDefault

// fetchAuthConfig fetches the auth configuration from the API server
func fetchAuthConfig(serverURL string) (*AuthConfig, error) {
	return FetchAuthConfigFunc(serverURL)
}

// fetchAuthConfigDefault is the default implementation that makes an HTTP request
func fetchAuthConfigDefault(serverURL string) (*AuthConfig, error) {
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	authConfigURL := serverURL + "/api/auth/config"
	resp, err := client.Get(authConfigURL)
	if err != nil {
		return nil, fmt.Errorf("cannot reach auth config endpoint: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("auth config endpoint returned status %s", resp.Status)
	}

	var authConfig AuthConfig
	if err := json.NewDecoder(resp.Body).Decode(&authConfig); err != nil {
		return nil, fmt.Errorf("failed to parse auth config response: %w", err)
	}

	return &authConfig, nil
}
