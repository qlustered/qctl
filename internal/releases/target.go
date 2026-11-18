package releases

import (
	"os"

	"github.com/qlustered/qctl/internal/config"
)

// SkipEnv names the environment variable that bypasses the release check when
// set to a true value ("1", "true").
const SkipEnv = "QCTL_SKIP_RELEASE_CHECK"

// SkipEnvSet reports whether SkipEnv is set to a true value.
func SkipEnvSet() bool {
	return isTrue(os.Getenv(SkipEnv))
}

// Target is the server an invocation will reach.
type Target struct {
	// Server is empty when no server is configured.
	Server string
	// Source is the precedence step that supplied Server.
	Source config.ServerSource
	// Context is the effective context's name, empty when there is none.
	Context string
	// Skip is set by SkipEnv, or by the effective context's skipReleaseCheck
	// when that context supplies Server.
	Skip bool
}

// ResolveTarget finds the server a command line with a's --config, --context
// and --server would reach: --server, then QCTL_SERVER, then the effective
// context's server. It returns an error only when loading the config fails,
// such as for a missing --config file or an unknown --context. The config
// package's path and context overrides are restored before it returns.
func ResolveTarget(a Args) (Target, error) {
	prevPath, prevContext := config.PathOverride(), config.ContextOverride()
	config.SetPathOverride(a.Config)
	config.SetContextOverride(a.Context)
	defer func() {
		config.SetPathOverride(prevPath)
		config.SetContextOverride(prevContext)
	}()

	t := Target{Skip: SkipEnvSet()}
	cfg, err := config.Load()
	if err != nil {
		return t, err
	}
	if _, ok := cfg.Contexts[cfg.CurrentContext]; ok {
		t.Context = cfg.CurrentContext
	}
	server, source, err := config.SelectServer(cfg, a.Server)
	if err != nil {
		// SelectServer fails only when no step supplies a server.
		return t, nil
	}
	t.Server, t.Source = server, source
	if source == config.ServerSourceContext && cfg.Contexts[cfg.CurrentContext].SkipReleaseCheck {
		t.Skip = true
	}
	return t, nil
}
