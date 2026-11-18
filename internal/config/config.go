package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/pkg/privatefile"
	"gopkg.in/yaml.v3"
)

const (
	// APIVersion is the config file version
	APIVersion = "qctl/v1"

	// ConfigDirName is the directory name for qctl config
	ConfigDirName = ".qctl"

	// ConfigFileName is the config file name
	ConfigFileName = "config"

	// URL schemes
	schemeHTTP  = "http"
	schemeHTTPS = "https"
)

// OrganizationRef stores cached org ID/name pairs for local resolution
type OrganizationRef struct {
	ID   string `yaml:"id"`
	Name string `yaml:"name"`
}

// DefaultServer is the default API server URL
const DefaultServer = ""

// DefaultKindeHost is the default Kinde authentication host
const DefaultKindeHost = ""

// Context represents a single deployment context
type Context struct {
	Server           string            `yaml:"server"`                     // Origin only, no /api — generated operation paths already start with /api
	Output           string            `yaml:"output,omitempty"`           // Default output format: table|json|yaml|name — applied when --output is not passed and the command supports it
	Organization     string            `yaml:"organization,omitempty"`     // Default org ID (UUID)
	OrganizationName string            `yaml:"organizationName,omitempty"` // Display name for the default org
	Organizations    []OrganizationRef `yaml:"organizations,omitempty"`    // Cached list of available orgs
	KindeHost        string            `yaml:"kindeHost,omitempty"`        // Kinde OAuth host URL
	KindeClientID    string            `yaml:"kindeClientId,omitempty"`    // Kinde OAuth client ID
	Protected        *bool             `yaml:"protected,omitempty"`        // Absent or true: destructive commands must name this context with --context; false allowlists it
	SkipReleaseCheck bool              `yaml:"skipReleaseCheck,omitempty"` // True: qctl runs itself against this context's server instead of the release matching the server's version
}

// IsProtected reports whether c is protected: every context is, unless its
// protected key is false.
func (c *Context) IsProtected() bool {
	return c.Protected == nil || *c.Protected
}

// Config represents the main configuration structure
type Config struct {
	Contexts       map[string]*Context `yaml:"contexts"`
	APIVersion     string              `yaml:"apiVersion"`
	CurrentContext string              `yaml:"currentContext"`

	// override is the --context name load put in CurrentContext, and
	// storedContext the currentContext it replaced, which Save writes back.
	override      string
	storedContext string
}

// pathOverride is the explicit config path from --config, empty when unset.
var pathOverride string

// SetPathOverride points Load and Save at path instead of the default
// $HOME/.qctl/config. An empty path restores the default location.
func SetPathOverride(path string) {
	pathOverride = path
}

// PathOverride returns the config path set by SetPathOverride, empty when unset.
func PathOverride() string {
	return pathOverride
}

// contextOverride is the context named by --context, empty when unset.
var contextOverride string

// SetContextOverride makes Load and LoadOrCreate return a config whose
// CurrentContext is name. Save still writes the stored current context unless
// the command switched to another one. An empty name restores the stored one.
func SetContextOverride(name string) {
	contextOverride = name
}

// ContextOverride returns the context named by --context, empty when unset.
func ContextOverride() string {
	return contextOverride
}

// GetConfigPath is a variable that returns the path to the config file
// It can be overridden in tests
var GetConfigPath = func() (string, error) {
	if pathOverride != "" {
		return pathOverride, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}
	return filepath.Join(home, ConfigDirName, ConfigFileName), nil
}

// EnsureQctlDir creates the default qctl directory and sets its mode to 0700.
func EnsureQctlDir() (string, error) {
	dir, err := qctlDirPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("failed to create qctl directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", fmt.Errorf("failed to restrict qctl directory to mode 0700: %w", err)
	}
	return dir, nil
}

// Dir returns the default qctl directory, $HOME/.qctl. --config does not move it.
func Dir() (string, error) {
	return qctlDirPath()
}

func qctlDirPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}
	return filepath.Join(home, ConfigDirName), nil
}

// Load loads the configuration from the config file. A config file named by
// --config that does not exist is an error; a missing default config is just
// a first run and yields defaults.
func Load() (*Config, error) {
	return load(false)
}

// LoadOrCreate is Load for the commands that are about to write the config
// back out. For them a config file that does not exist yet is not a typo, it
// is the file they are about to create — the same reason
// `kubectl --kubeconfig=/new/path config set-context` works.
func LoadOrCreate() (*Config, error) {
	return load(true)
}

func load(createMissing bool) (*Config, error) {
	configPath, err := GetConfigPath()
	if err != nil {
		return nil, err
	}

	// If config file doesn't exist, return a default config
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		// A missing default config is just a first run. A missing config the
		// user named with --config is a typo, and silently starting from an
		// empty config would resurface it much later as "no API server
		// specified" — pointing at the wrong problem.
		if pathOverride != "" && !createMissing {
			return nil, fmt.Errorf("config file %s does not exist", configPath)
		}
		cfg := &Config{
			APIVersion:     APIVersion,
			CurrentContext: "",
			Contexts:       make(map[string]*Context),
		}
		if err := cfg.applyContextOverride(); err != nil {
			return nil, err
		}
		return cfg, nil
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	// Initialize contexts map if nil
	if cfg.Contexts == nil {
		cfg.Contexts = make(map[string]*Context)
	}

	if err := cfg.applyContextOverride(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// applyContextOverride switches CurrentContext to the --context name in
// memory. A name with no context is bad usage.
func (c *Config) applyContextOverride() error {
	if contextOverride == "" {
		return nil
	}
	if _, ok := c.Contexts[contextOverride]; !ok {
		return qerrors.NewBadUsagef("context %q not found. Available contexts: %s",
			contextOverride, c.contextNames())
	}
	c.override = contextOverride
	c.storedContext = c.CurrentContext
	c.CurrentContext = contextOverride
	return nil
}

// contextNames lists the configured context names, sorted, or "(none configured)".
func (c *Config) contextNames() string {
	names := make([]string, 0, len(c.Contexts))
	for name := range c.Contexts {
		names = append(names, name)
	}
	if len(names) == 0 {
		return "(none configured)"
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// Save saves the configuration to the config file.
//
// It goes through privatefile.Write, the same writer credentials.json uses,
// rather than os.WriteFile. The file holds no token, but it does hold the
// server URL, organization IDs and names, and the Kinde client ID, and
// os.WriteFile applied 0600 only when creating the file: one already at 0644
// kept that mode through every save. The replacement is also atomic, which
// matters here on its own account -- a truncated config loses every context
// the user has -- and a symlink to an existing config is written through, not
// replaced.
//
// The trades privatefile.Write documents apply, and each is a change from
// os.WriteFile: the config's directory must be readable and writable, not just
// the file; a config deliberately shared at a looser mode is tightened to
// 0600, and one saved by another user (sudo keeping HOME) becomes theirs; and a
// DANGLING symlink -- a dotfiles link whose target is not there yet -- is
// replaced by a plain file rather than creating its target.
func (c *Config) Save() error {
	configPath, err := GetConfigPath()
	if err != nil {
		return err
	}

	// Create config directory if it doesn't exist
	configDir := filepath.Dir(configPath)
	if defaultDir, pathErr := qctlDirPath(); pathErr == nil && configDir == defaultDir {
		if _, err := EnsureQctlDir(); err != nil {
			return err
		}
	} else if err := os.MkdirAll(configDir, 0o700); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	out := *c
	if c.override != "" && c.CurrentContext == c.override {
		out.CurrentContext = c.storedContext
	}
	data, err := yaml.Marshal(&out)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	return privatefile.Write(configPath, data, "config")
}

// SetupHint is actionable guidance shown whenever no usable context (and
// therefore no server) is configured. Creating a context also configures its
// server and switches to it, so a single command gets the user unblocked.
const SetupHint = "To get started, create a context that points at your Qluster API server:\n" +
	"\n" +
	"  qctl config set-context <name> --server <url>\n" +
	"\n" +
	"For example:\n" +
	"\n" +
	"  qctl config set-context local --server http://localhost:8000\n" +
	"\n" +
	"This creates the context, caches its API schema, and switches to it. " +
	"Switch between existing contexts with 'qctl config use-context <name>' " +
	"and see the active one with 'qctl config current-context'."

// GetCurrentContext returns the current context configuration
func (c *Config) GetCurrentContext() (*Context, error) {
	if c.CurrentContext == "" {
		return nil, fmt.Errorf("no current context is set.\n\n%s", SetupHint)
	}

	ctx, ok := c.Contexts[c.CurrentContext]
	if !ok {
		return nil, fmt.Errorf("current context %q not found.\n\n%s", c.CurrentContext, SetupHint)
	}

	return ctx, nil
}

// SetContext creates or updates a context
func (c *Config) SetContext(name string, ctx *Context) {
	if c.Contexts == nil {
		c.Contexts = make(map[string]*Context)
	}
	c.Contexts[name] = ctx
}

// UseContext sets the current context
func (c *Config) UseContext(name string) error {
	if _, ok := c.Contexts[name]; !ok {
		return qerrors.NewNotFound(fmt.Sprintf("context %q does not exist", name))
	}
	c.CurrentContext = name
	c.override = ""
	return nil
}

// IsCurrentContext reports whether name is the current context, either the
// one --context selected or the one the file stores.
func (c *Config) IsCurrentContext(name string) bool {
	return c.CurrentContext == name || (c.override != "" && c.storedContext == name)
}

// DeleteContext removes a context from the configuration
func (c *Config) DeleteContext(name string) error {
	if c.IsCurrentContext(name) {
		return qerrors.NewBadUsagef("cannot delete current context %q", name)
	}
	if _, ok := c.Contexts[name]; !ok {
		return qerrors.NewNotFound(fmt.Sprintf("context %q does not exist", name))
	}
	delete(c.Contexts, name)
	return nil
}

// ResolveServer resolves the server URL from various sources
// Priority: serverFlag > QCTL_SERVER env > current context server
func ResolveServer(cfg *Config, serverFlag string) (string, error) {
	server, _, err := ResolveServerWithSource(cfg, serverFlag)
	return server, err
}

// ResolveServerWithSource returns the server and the setting that supplied it.
func ResolveServerWithSource(cfg *Config, serverFlag string) (string, string, error) {
	server, source, err := SelectServer(cfg, serverFlag)
	if err != nil {
		return "", "", err
	}
	switch source {
	case ServerSourceFlag:
		return server, "from the --server flag", nil
	case ServerSourceEnv:
		return server, "from QCTL_SERVER", nil
	}
	return server, fmt.Sprintf("from context %q", cfg.CurrentContext), nil
}

// ServerSource names the precedence step that supplied the server.
type ServerSource string

const (
	// ServerSourceFlag is --server on the command line.
	ServerSourceFlag ServerSource = "flag"
	// ServerSourceEnv is the QCTL_SERVER environment variable.
	ServerSourceEnv ServerSource = "env"
	// ServerSourceContext is the server stored in the current context.
	ServerSourceContext ServerSource = "context"
)

// SelectServer returns the server in precedence order: --server, then
// QCTL_SERVER, then the current context's server, together with which of those
// supplied it.
func SelectServer(cfg *Config, serverFlag string) (string, ServerSource, error) {
	// 1. Check --server flag
	if serverFlag != "" {
		return serverFlag, ServerSourceFlag, nil
	}

	// 2. Check QCTL_SERVER environment variable
	if envServer := os.Getenv("QCTL_SERVER"); envServer != "" {
		return envServer, ServerSourceEnv, nil
	}

	// 3. Get from current context
	ctx, err := cfg.GetCurrentContext()
	if err != nil {
		return "", "", fmt.Errorf("no API server specified (pass --server, set QCTL_SERVER, or configure a context): %w", err)
	}

	if ctx.Server == "" {
		return "", "", fmt.Errorf("no server configured in current context %q.\n\nSet one with:\n  qctl config set-context %s --server <url>", cfg.CurrentContext, cfg.CurrentContext)
	}

	return ctx.Server, ServerSourceContext, nil
}

// ResolveKindeHost resolves the Kinde OAuth host from various sources
// Priority: QCTL_KINDE_HOST env > current context kindeHost > DefaultKindeHost
func ResolveKindeHost(cfg *Config) string {
	// 1. Check QCTL_KINDE_HOST environment variable
	if envHost := os.Getenv("QCTL_KINDE_HOST"); envHost != "" {
		return envHost
	}

	// 2. Get from current context if available
	ctx, err := cfg.GetCurrentContext()
	if err == nil && ctx.KindeHost != "" {
		return ctx.KindeHost
	}

	// 3. Return default
	return DefaultKindeHost
}

// ResolveKindeClientID resolves the Kinde OAuth client ID from various sources
// Priority: QCTL_KINDE_CLIENT_ID env > current context kindeClientId
// Returns empty string if not configured (required for OAuth)
func ResolveKindeClientID(cfg *Config) string {
	// 1. Check QCTL_KINDE_CLIENT_ID environment variable
	if envClientID := os.Getenv("QCTL_KINDE_CLIENT_ID"); envClientID != "" {
		return envClientID
	}

	// 2. Get from current context if available
	ctx, err := cfg.GetCurrentContext()
	if err == nil && ctx.KindeClientID != "" {
		return ctx.KindeClientID
	}

	return ""
}

// OrgSource names the precedence step that supplied the organization.
//
// The constants' VALUES are the rendered phrase, not a token. They are read
// verbatim into the organization-scope note that apierror appends to a 403
// or 404 (internal/apierror/scope.go), so the vocabulary a reader is
// given is the vocabulary of the precedence chain they can act on. Rewording
// the note means rewording these; TestOrgSourcePhrasesAreTheOnesTheNoteRenders
// pins them.
type OrgSource string

const (
	// OrgSourceFlag is --org / -O on the command line.
	OrgSourceFlag OrgSource = "from the --org flag"
	// OrgSourceEnv is the QCTL_ORG environment variable.
	OrgSourceEnv OrgSource = "from QCTL_ORG"
	// OrgSourceContext is the organization stored in the current context.
	OrgSourceContext OrgSource = "from the current context"
)

// OrganizationFlagOrDefault returns the organization identifier the user
// selected, in precedence order: --org, then QCTL_ORG, then the current
// context's organization, together with which of those three supplied it.
//
// It returns that value VERBATIM and does no resolution: a name stays a name, a
// UUID prefix stays a prefix. Anything that builds a URL wants
// cmdutil.ResolveOrganization instead, which turns the result into a real org
// UUID against the cached org list. The two were both called
// ResolveOrganization once, and picking the wrong one put a human-typed name
// straight into a request path.
//
// The source is returned rather than re-derived by the caller because these
// three branches are the only place the question is actually decided. A
// caller that asked `orgFlag != ""` again would be a second answer, and the
// two would drift the first time the precedence changed.
func OrganizationFlagOrDefault(cfg *Config, orgFlag string) (string, OrgSource, error) {
	// 1. Check --org flag
	if orgFlag != "" {
		return orgFlag, OrgSourceFlag, nil
	}

	// 2. Check QCTL_ORG environment variable
	if envOrg := os.Getenv("QCTL_ORG"); envOrg != "" {
		return envOrg, OrgSourceEnv, nil
	}

	// 3. Get from current context
	ctx, err := cfg.GetCurrentContext()
	if err != nil {
		return "", "", qerrors.NewBadUsage("no organization specified.\n" +
			"Ask the user which organization to use, then pass --org <full-organization-UUID>.")
	}

	if ctx.Organization == "" {
		return "", "", qerrors.NewBadUsagef("organization not configured in current context %q.\n"+
			"Ask the user which organization to use, then pass --org <full-organization-UUID>.", cfg.CurrentContext)
	}

	return ctx.Organization, OrgSourceContext, nil
}

// ValidateServer validates the server URL
// Rejects non-localhost http:// unless allowInsecure is true
func ValidateServer(serverURL string, allowInsecure bool) error {
	u, err := url.Parse(serverURL)
	if err != nil {
		return fmt.Errorf("invalid server URL: %w", err)
	}

	// Check if it's http (not https)
	if u.Scheme == schemeHTTP {
		// Check if it's localhost
		host := u.Hostname()
		isLocalhost := host == "localhost" || host == "127.0.0.1" || host == "::1"

		if !isLocalhost && !allowInsecure {
			return fmt.Errorf("non-localhost http:// endpoints require --allow-insecure-http flag or QCTL_INSECURE_HTTP=1")
		}
	}

	if u.Scheme != schemeHTTP && u.Scheme != schemeHTTPS {
		return fmt.Errorf("server URL must use http or https scheme")
	}

	return nil
}

// NormalizeEndpointKey normalizes a server URL into an endpoint key for credential storage
// Format: scheme://host:port (without path, normalized to lowercase)
func NormalizeEndpointKey(serverURL string) (string, error) {
	u, err := url.Parse(serverURL)
	if err != nil {
		return "", fmt.Errorf("invalid server URL: %w", err)
	}

	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	port := u.Port()

	// Use default ports if not specified
	if port == "" {
		switch scheme {
		case schemeHTTPS:
			port = "443"
		case schemeHTTP:
			port = "80"
		}
	}

	return fmt.Sprintf("%s://%s:%s", scheme, host, port), nil
}

// IsInsecureHTTPAllowed checks if insecure HTTP is allowed via environment variable
func IsInsecureHTTPAllowed() bool {
	env := os.Getenv("QCTL_INSECURE_HTTP")
	return env == "1" || env == "true"
}

// WatchTimeoutFromEnv reads QCTL_WATCH_TIMEOUT as a Go duration ("90s",
// "10m"; "0" means no limit). set is false when the variable is unset or
// empty. A value that does not parse, or is negative, is an error.
func WatchTimeoutFromEnv() (d time.Duration, set bool, err error) {
	raw := os.Getenv("QCTL_WATCH_TIMEOUT")
	if raw == "" {
		return 0, false, nil
	}
	d, err = time.ParseDuration(raw)
	if err != nil {
		return 0, true, fmt.Errorf("invalid QCTL_WATCH_TIMEOUT: %w (want a duration such as 90s or 10m, or 0 for no limit)", err)
	}
	if d < 0 {
		return 0, true, fmt.Errorf("invalid QCTL_WATCH_TIMEOUT %q: must not be negative (0 means no limit)", raw)
	}
	return d, true, nil
}
