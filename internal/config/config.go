package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	// APIVersion is the config file version
	APIVersion = "qctl/v1"

	// ConfigDirName is the directory name for qctl config
	ConfigDirName = ".qctl"

	// ConfigFileName is the config file name
	ConfigFileName = "config"

	// DefaultOutputFormat is the default output format
	DefaultOutputFormat = "table"

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
	Server           string            `yaml:"server"`                     // API base URL including /api
	Output           string            `yaml:"output,omitempty"`           // Default output format: table|json|yaml|name
	Organization     string            `yaml:"organization,omitempty"`     // Default org ID (UUID)
	OrganizationName string            `yaml:"organizationName,omitempty"` // Display name for the default org
	Organizations    []OrganizationRef `yaml:"organizations,omitempty"`    // Cached list of available orgs
	KindeHost        string            `yaml:"kindeHost,omitempty"`        // Kinde OAuth host URL
	KindeClientID    string            `yaml:"kindeClientId,omitempty"`    // Kinde OAuth client ID
}

// Config represents the main configuration structure
type Config struct {
	Contexts       map[string]*Context `yaml:"contexts"`
	APIVersion     string              `yaml:"apiVersion"`
	CurrentContext string              `yaml:"currentContext"`
}

// GetConfigPath is a variable that returns the path to the config file
// It can be overridden in tests
var GetConfigPath = func() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}
	return filepath.Join(home, ConfigDirName, ConfigFileName), nil
}

// Load loads the configuration from the config file
func Load() (*Config, error) {
	configPath, err := GetConfigPath()
	if err != nil {
		return nil, err
	}

	// If config file doesn't exist, return a default config
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return &Config{
			APIVersion:     APIVersion,
			CurrentContext: "",
			Contexts:       make(map[string]*Context),
		}, nil
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

	return &cfg, nil
}

// Save saves the configuration to the config file
func (c *Config) Save() error {
	configPath, err := GetConfigPath()
	if err != nil {
		return err
	}

	// Create config directory if it doesn't exist
	configDir := filepath.Dir(configPath)
	if err := os.MkdirAll(configDir, 0700); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(configPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}

// GetCurrentContext returns the current context configuration
func (c *Config) GetCurrentContext() (*Context, error) {
	if c.CurrentContext == "" {
		return nil, fmt.Errorf("no current context set")
	}

	ctx, ok := c.Contexts[c.CurrentContext]
	if !ok {
		return nil, fmt.Errorf("current context %q not found", c.CurrentContext)
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
		return fmt.Errorf("context %q does not exist", name)
	}
	c.CurrentContext = name
	return nil
}

// DeleteContext removes a context from the configuration
func (c *Config) DeleteContext(name string) error {
	if c.CurrentContext == name {
		return fmt.Errorf("cannot delete current context %q", name)
	}
	if _, ok := c.Contexts[name]; !ok {
		return fmt.Errorf("context %q does not exist", name)
	}
	delete(c.Contexts, name)
	return nil
}

// ResolveServer resolves the server URL from various sources
// Priority: serverFlag > QCTL_SERVER env > current context server
func ResolveServer(cfg *Config, serverFlag string) (string, error) {
	// 1. Check --server flag
	if serverFlag != "" {
		return serverFlag, nil
	}

	// 2. Check QCTL_SERVER environment variable
	if envServer := os.Getenv("QCTL_SERVER"); envServer != "" {
		return envServer, nil
	}

	// 3. Get from current context
	ctx, err := cfg.GetCurrentContext()
	if err != nil {
		return "", fmt.Errorf("no server specified and no current context: %w", err)
	}

	if ctx.Server == "" {
		return "", fmt.Errorf("server not configured in current context %q", cfg.CurrentContext)
	}

	return ctx.Server, nil
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

// ResolveOrganization resolves the organization from various sources
// Priority: orgFlag > QCTL_ORG env > current context organization
// Returns the raw input value - callers must resolve name/partial-UUID to full UUID
func ResolveOrganization(cfg *Config, orgFlag string) (string, error) {
	// 1. Check --org flag
	if orgFlag != "" {
		return orgFlag, nil
	}

	// 2. Check QCTL_ORG environment variable
	if envOrg := os.Getenv("QCTL_ORG"); envOrg != "" {
		return envOrg, nil
	}

	// 3. Get from current context
	ctx, err := cfg.GetCurrentContext()
	if err != nil {
		return "", fmt.Errorf("no organization specified and no current context: %w", err)
	}

	if ctx.Organization == "" {
		return "", fmt.Errorf("organization not configured in current context %q.\n\nSet a default organization with:\n  qctl config set-context --current --org <name-or-id>", cfg.CurrentContext)
	}

	return ctx.Organization, nil
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
