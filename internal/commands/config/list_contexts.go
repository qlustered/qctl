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
			fmt.Printf("%-20s %-50s %-40s\n", "NAME", "SERVER", "ORGANIZATION")
			fmt.Printf("%-20s %-50s %-40s\n", "----", "------", "------------")

			// Print contexts
			for _, name := range names {
				ctx := cfg.Contexts[name]
				current := ""
				if name == cfg.CurrentContext {
					current = "*"
				}
				orgDisplay := renderOrgDisplay(ctx)
				fmt.Printf("%-1s%-19s %-50s %-40s\n", current, name, ctx.Server, orgDisplay)
			}

			return nil
		},
	}

	return cmd
}

// renderOrgDisplay formats the organization column for a context row.
// Appends "(+N more)" when the cached org list has more than one entry.
func renderOrgDisplay(ctx *config.Context) string {
	primary := ctx.OrganizationName
	if primary == "" {
		primary = ctx.Organization
	}

	extra := len(ctx.Organizations) - 1
	if extra <= 0 {
		return primary
	}

	if primary == "" {
		return fmt.Sprintf("(+%d orgs)", len(ctx.Organizations))
	}
	return fmt.Sprintf("%s (+%d more)", primary, extra)
}
