package config

import (
	"fmt"

	"github.com/qlustered/qctl/internal/config"
	"github.com/spf13/cobra"
)

func newCurrentContextCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "current-context",
		Short: "Display the current context",
		Long:  `Display the current context name and configuration.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			if cfg.CurrentContext == "" {
				return fmt.Errorf("no current context set")
			}

			ctx, err := cfg.GetCurrentContext()
			if err != nil {
				return err
			}

			verbosity, _ := cmd.Root().PersistentFlags().GetCount("verbose")

			// Print current context information
			fmt.Printf("Current context: %s\n", cfg.CurrentContext)
			fmt.Printf("  Server: %s\n", ctx.Server)
			if ctx.Output != "" {
				fmt.Printf("  Output: %s\n", ctx.Output)
			}
			if ctx.Organization != "" {
				if ctx.OrganizationName != "" {
					if verbosity > 0 {
						fmt.Printf("  Organization: %s (%s)\n", ctx.OrganizationName, ctx.Organization)
					} else {
						fmt.Printf("  Organization: %s\n", ctx.OrganizationName)
					}
				} else {
					fmt.Printf("  Organization: %s\n", ctx.Organization)
				}
			}
			return nil
		},
	}

	return cmd
}
