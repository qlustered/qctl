package config

import (
	"context"
	"fmt"
	"io"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/launcher"
	"github.com/qlustered/qctl/internal/releases"
	"github.com/qlustered/qctl/internal/schema/cache"
	"github.com/qlustered/qctl/internal/version"
	"github.com/spf13/cobra"
)

// UseContextEnsureSpecCachedFunc is a variable that can be overridden in tests
var UseContextEnsureSpecCachedFunc = cache.EnsureSpecCached

// UseContextNewPreparerFunc builds the release preparer use-context checks
// the context's server with. Tests replace it.
var UseContextNewPreparerFunc = launcher.NewPreparer

// useContextEnsureSpecCached fetches and caches the OpenAPI spec for the given server
func useContextEnsureSpecCached(ctx context.Context, serverURL string) (string, error) {
	return UseContextEnsureSpecCachedFunc(ctx, serverURL)
}

func newUseContextCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "use-context <name>",
		Short: "Set the current context",
		Long: `Set the current context to the specified context name.

Before switching, use-context asks the context's server for its atlas
version, selects the qctl release with the same MAJOR.MINOR (the newest
patch), builds it if it is not installed yet, and reports the atlas version,
the running qctl, the selected release and their compatibility. A failed build
leaves the current context unchanged. The check is skipped when the context
sets skipReleaseCheck or QCTL_SKIP_RELEASE_CHECK is true.`,
		Args:        cobra.ExactArgs(1),
		Annotations: cmdutil.WithFormats(catalog.For("context"), "table"),
		RunE: func(cmd *cobra.Command, args []string) error {
			contextName := args[0]

			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			if err := cfg.UseContext(contextName); err != nil {
				return err
			}

			// Get the server URL for this context and ensure the OpenAPI spec is cached
			ctx := cfg.Contexts[contextName]
			if ctx != nil && ctx.Server != "" {
				version, err := useContextEnsureSpecCached(cmd.Context(), ctx.Server)
				if err != nil {
					return fmt.Errorf("failed to cache API schema: %w", err)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Cached OpenAPI spec for API version %s\n", version)
				if err := checkContextRelease(cmd, contextName, ctx); err != nil {
					return err
				}
			}

			if err := cfg.Save(); err != nil {
				return fmt.Errorf("failed to save config: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Switched to context %q\n", contextName)
			return nil
		},
	}

	return cmd
}

// checkContextRelease reports the qctl release for c's server, building it
// if needed, or that the check is skipped.
func checkContextRelease(cmd *cobra.Command, name string, c *config.Context) error {
	out := cmd.OutOrStdout()
	switch {
	case c.SkipReleaseCheck:
		fmt.Fprintf(out, "Release check skipped: context %q sets skipReleaseCheck\n", name)
		return nil
	case releases.SkipEnvSet():
		fmt.Fprintf(out, "Release check skipped: %s is set\n", releases.SkipEnv)
		return nil
	}
	p, err := UseContextNewPreparerFunc(cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	ctx, stop := launcher.Interruptible(cmd.Context())
	report, err := prepareContextRelease(ctx, p, c.Server, cmd.ErrOrStderr())
	if sig := stop(); sig != nil {
		return launcher.Interrupted(sig)
	}
	if err != nil {
		return err
	}
	report.print(out)
	return nil
}

// releaseReport is what use-context says about a server's release.
type releaseReport struct {
	atlas, selected, compatibility string
}

func (r releaseReport) print(w io.Writer) {
	fmt.Fprintf(w, "Atlas version:     %s\n", r.atlas)
	fmt.Fprintf(w, "Running qctl:      %s\n", version.Version)
	fmt.Fprintf(w, "Selected qctl:     %s\n", r.selected)
	fmt.Fprintf(w, "Compatibility:     %s\n", r.compatibility)
}

// prepareContextRelease resolves server's release and makes it ready.
// Warnings that do not change the outcome go to stderr.
func prepareContextRelease(ctx context.Context, p *launcher.Preparer, server string, stderr io.Writer) (releaseReport, error) {
	r := releaseReport{atlas: "unknown", selected: "none"}
	d, err := p.Resolver.Resolve(ctx, server)
	if err != nil {
		return r, fmt.Errorf("qctl release check for %s failed: %w\n\nTo skip it, set %s=1", server, err, releases.SkipEnv)
	}
	if d.IndexErr != nil {
		fmt.Fprintf(stderr, "qctl: warning: %v\n", d.IndexErr)
	}
	if d.AtlasVersion != "" {
		r.atlas = d.AtlasVersion
	}
	switch d.Outcome {
	case releases.OutcomeUnreachable:
		r.compatibility = "unknown (the server did not answer the version check)"
	case releases.OutcomeUnknown:
		r.compatibility = fmt.Sprintf("unknown (%v)", d.FetchErr)
	case releases.OutcomeNoRelease:
		r.compatibility = fmt.Sprintf("no qctl release for atlas %s; commands run the invoked qctl", d.Minor)
	case releases.OutcomeSelected:
		r.compatibility = "compatible"
		if d.IsRunning {
			r.selected = d.Selected + " (running)"
			break
		}
		ready, err := p.Ready(ctx, d)
		if err != nil {
			return r, err
		}
		ready.Release()
		if ready.IndexErr != nil {
			fmt.Fprintf(stderr, "qctl: warning: %v\n", ready.IndexErr)
		}
		state := "installed"
		if ready.Built {
			state = "built"
		}
		r.selected = fmt.Sprintf("%s (%s)", d.Selected, state)
	}
	return r, nil
}
