package config

import (
	"context"
	"fmt"

	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/schema/cache"
	"github.com/spf13/cobra"
)

// UseContextEnsureSpecCachedFunc is a variable that can be overridden in tests
var UseContextEnsureSpecCachedFunc = cache.EnsureSpecCached

// useContextEnsureSpecCached fetches and caches the OpenAPI spec for the given server
func useContextEnsureSpecCached(ctx context.Context, serverURL string) (string, error) {
	return UseContextEnsureSpecCachedFunc(ctx, serverURL)
}

func newUseContextCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "use-context <name>",
		Short: "Set the current context",
		Long:  `Set the current context to the specified context name.`,
		Args:  cobra.ExactArgs(1),
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
				fmt.Printf("Cached OpenAPI spec for API version %s\n", version)
			}

			if err := cfg.Save(); err != nil {
				return fmt.Errorf("failed to save config: %w", err)
			}

			fmt.Printf("Switched to context %q\n", contextName)
			return nil
		},
	}

	return cmd
}
