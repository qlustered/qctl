package config

import (
	"fmt"
	"sort"

	"github.com/qlustered/qctl/internal/config"
	"github.com/spf13/cobra"
)

func newListContextsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list-contexts",
		Short: "List all contexts",
		Long:  `List all configured contexts with their servers and users.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			if len(cfg.Contexts) == 0 {
				fmt.Println("No contexts configured")
				return nil
			}

			// Get sorted context names for consistent output
			names := make([]string, 0, len(cfg.Contexts))
			for name := range cfg.Contexts {
				names = append(names, name)
			}
			sort.Strings(names)

			// Print header
			fmt.Printf("%-20s %-50s %-30s\n", "NAME", "SERVER", "ORGANIZATION")
			fmt.Printf("%-20s %-50s %-30s\n", "----", "------", "------------")

			// Print contexts
			for _, name := range names {
				ctx := cfg.Contexts[name]
				current := ""
				if name == cfg.CurrentContext {
					current = "*"
				}
				orgDisplay := ctx.Organization
				if ctx.OrganizationName != "" {
					orgDisplay = ctx.OrganizationName
				}
				fmt.Printf("%-1s%-19s %-50s %-30s\n", current, name, ctx.Server, orgDisplay)
			}

			return nil
		},
	}

	return cmd
}
