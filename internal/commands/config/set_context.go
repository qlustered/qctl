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
	"github.com/qlustered/qctl/internal/catalog"
	qclient "github.com/qlustered/qctl/internal/client"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/org"
	"github.com/qlustered/qctl/internal/schema/cache"
	"github.com/spf13/cobra"
)

type setContextOptions struct {
	server     string
	output     string
	orgInput   string
	useCurrent bool
	protected  bool
	skipCheck  bool
}

func newSetContextCommand() *cobra.Command {
	opts := setContextOptions{}

	cmd := &cobra.Command{
		Use:   "set-context [name]",
		Short: "Create or update a context",
		Long: `Create a new context or update an existing context with the specified configuration.

At minimum, the --server flag must be provided when creating a new context.
The API endpoint will be validated before the context is saved.
After creating a new context, it will automatically become the current context.
Updating an existing context leaves the current context unchanged.

Use --current to modify the current context without specifying its name.

Every context is protected: its destructive commands must name it with
--context. --protected=false allowlists one, such as a local or development
context; that asks for confirmation and needs an interactive terminal, and
--yes does not answer it. --protected protects it again.

API commands normally run the qctl release whose MAJOR.MINOR matches the
server's atlas version, building it on first use. --skip-release-check makes
commands that reach this context's server run the invoked qctl instead, for a
local atlas that reuses release version numbers; --skip-release-check=false
restores the check.`,
		Example: `  # Create a new context for production
  qctl config set-context production --server https://api.qluster.ai

  # Create a context for local development
  qctl config set-context local --server http://localhost:8000

  # Update an existing context's output format
  qctl config set-context production --output json

  # Set the default organization for the current context
  qctl config set-context --current --org "Acme Corporation"

  # Set organization by UUID prefix
  qctl config set-context --current --org 550e8400

  # Let destructive commands run on a development context without --context
  qctl config set-context dev --protected=false

  # Run the invoked qctl against a local atlas, not a matching release
  qctl config set-context local --skip-release-check`,
		Args:        cobra.MaximumNArgs(1),
		Annotations: catalog.For("context"),
		RunE:        func(cmd *cobra.Command, args []string) error { return runSetContext(cmd, args, opts) },
	}

	cmd.Flags().StringVar(&opts.server, "server", "", "API server URL (e.g., http://localhost:8000)")
	cmd.Flags().StringVar(&opts.output, "output", "", "Default output format (table|json|yaml|name)")
	cobra.CheckErr(cmd.RegisterFlagCompletionFunc("output",
		func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
			return cmdutil.DefaultOutputFormats(), cobra.ShellCompDirectiveNoFileComp
		}))
	cmd.Flags().StringVar(&opts.orgInput, "org", "", "Default organization (name or UUID/prefix)")
	cmd.Flags().BoolVar(&opts.useCurrent, "current", false, "Modify the current context (no name argument needed)")
	cmd.Flags().BoolVar(&opts.protected, "protected", false, "Require --context on destructive commands against this context, the default (--protected=false allowlists it)")
	cmd.Flags().BoolVar(&opts.skipCheck, "skip-release-check", false, "Run the invoked qctl against this context's server instead of the release matching its version (--skip-release-check=false restores the check)")
	cmd.Flags().Bool("yes", false, "Confirm a fuzzy organization name without prompting")
	cmd.Flags().Bool("allow-insecure-http", false, "Allow non-localhost http:// endpoints")

	return cmd
}

func runSetContext(cmd *cobra.Command, args []string, opts setContextOptions) error {
	// LoadOrCreate, not Load: set-context is how you bootstrap a config, so
	// `--config /new/path config set-context` has to create that file.
	cfg, err := config.LoadOrCreate()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	contextName, err := setContextName(cfg, args, opts.useCurrent)
	if err != nil {
		return err
	}
	ctx, exists := cfg.Contexts[contextName]
	if !exists {
		if opts.server == "" {
			return qerrors.NewBadUsage("--server is required when creating a new context")
		}
		ctx = &config.Context{}
	}
	if err := checkSetContextBeforeServer(cmd, ctx, contextName, opts); err != nil {
		return err
	}
	if opts.server != "" {
		if err := updateContextServer(cmd, ctx, opts.server); err != nil {
			return err
		}
	}
	if cmd.Flags().Changed("protected") {
		protected := opts.protected
		ctx.Protected = &protected
	}
	if cmd.Flags().Changed("skip-release-check") {
		ctx.SkipReleaseCheck = opts.skipCheck
	}
	if opts.output != "" {
		ctx.Output = opts.output
	}
	if err := setContextOrganization(cmd, ctx, opts.orgInput); err != nil {
		return err
	}
	cfg.SetContext(contextName, ctx)
	if !exists {
		cfg.CurrentContext = contextName
	}
	if err := cfg.Save(); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}
	if exists {
		fmt.Fprintf(cmd.OutOrStdout(), "Updated context %q\n", contextName)
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "Created context %q\n", contextName)
		fmt.Fprintf(cmd.OutOrStdout(), "Switched to context %q\n", contextName)
	}
	return nil
}

// checkSetContextBeforeServer validates --output and asks before
// --protected=false removes ctx's protection.
func checkSetContextBeforeServer(cmd *cobra.Command, ctx *config.Context, contextName string, opts setContextOptions) error {
	if opts.output != "" {
		// set-context's --output is a local flag, so the root's validation
		// skips it by design (a command that registers its own --output owns
		// its own validation). Without this the config could hold a value no
		// command will ever honor, and a typo'd format would be masked by a
		// server-reachability error instead of failing at exit 2.
		if err := cmdutil.ValidateOutputFormatValue(opts.output, cmdutil.DefaultOutputFormats()); err != nil {
			return err
		}
	}
	if cmd.Flags().Changed("protected") && !opts.protected && ctx.IsProtected() {
		return cmdutil.ConfirmInTerminal(cmd.ErrOrStderr(),
			fmt.Sprintf("Remove protection from context %q?", contextName),
			fmt.Sprintf("context %q is protected; removing that needs a person at an interactive terminal.\n"+
				"Ask the user to run: qctl config set-context %s --protected=false", contextName, contextName))
	}
	return nil
}

func setContextOrganization(cmd *cobra.Command, ctx *config.Context, input string) error {
	if input == "" {
		return nil
	}
	id, name, err := resolveOrgInput(ctx, input)
	if err != nil {
		return err
	}
	if err := cmdutil.ConfirmOrganizationMatch(cmd, input, id, name); err != nil {
		return err
	}
	ctx.Organization, ctx.OrganizationName = id, name
	return nil
}

func setContextName(cfg *config.Config, args []string, useCurrent bool) (string, error) {
	if useCurrent {
		if len(args) > 0 {
			return "", qerrors.NewBadUsage("cannot specify both --current and a context name")
		}
		if cfg.CurrentContext == "" {
			return "", qerrors.NewBadUsage("no current context set; use 'qctl config set-context <name> --server <url>' to create one")
		}
		return cfg.CurrentContext, nil
	}
	if len(args) == 0 {
		return "", qerrors.NewBadUsage("context name required (or use --current to modify the current context)")
	}
	return args[0], nil
}

func updateContextServer(cmd *cobra.Command, ctx *config.Context, server string) error {
	server = strings.TrimSuffix(server, "/")
	allowInsecure := config.IsInsecureHTTPAllowed()
	if flagValue, _ := cmd.Flags().GetBool("allow-insecure-http"); flagValue {
		allowInsecure = true
	}
	if err := config.ValidateServer(server, allowInsecure); err != nil {
		return qerrors.Wrap(qerrors.ExitBadUsage, err, "")
	}
	if err := validateAPIEndpoint(server); err != nil {
		return fmt.Errorf("failed to validate API endpoint: %w", err)
	}
	version, err := ensureSpecCached(cmd.Context(), server)
	if err != nil {
		return fmt.Errorf("failed to cache API schema: %w", err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Cached OpenAPI spec for API version %s\n", version)
	authConfig, err := fetchAuthConfig(server)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not fetch auth config: %v\n", err)
	} else {
		ctx.KindeHost = authConfig.KindeHost
		ctx.KindeClientID = authConfig.KindeClientID
	}
	ctx.Server = server
	return nil
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

// probeTimeout is this file's own deadline for the two unauthenticated
// reachability probes below. Ten seconds rather than DefaultTimeout because
// the user is sitting at a prompt waiting to learn whether they typed the
// server right, and an explicit --request-timeout still overrides it -- which
// is the lever for a link slow enough that ten seconds is a false negative.
const probeTimeout = 10 * time.Second

// validateAPIEndpointDefault is the default implementation that makes an HTTP request
func validateAPIEndpointDefault(serverURL string) error {
	client := &http.Client{
		Timeout: qclient.ResolveTimeout(probeTimeout),
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
		return "", "", qerrors.NewBadUsage("no cached organizations available.\n" +
			"Ask the user which organization to use, then pass its full UUID to --org.")
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
		Timeout: qclient.ResolveTimeout(probeTimeout),
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
