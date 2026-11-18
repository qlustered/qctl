package config

import (
	"fmt"
	"sort"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/output"
	"github.com/spf13/cobra"
)

func newListContextsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:         "list-contexts",
		Aliases:     []string{"get-contexts"},
		Short:       "List all contexts",
		Long:        `List all configured contexts with their servers and users.`,
		Annotations: cmdutil.WithFormats(catalog.For("context"), "table", "json", "yaml", "name"),
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			// Get sorted context names for consistent output
			names := make([]string, 0, len(cfg.Contexts))
			for name := range cfg.Contexts {
				names = append(names, name)
			}
			sort.Strings(names)

			format, _ := cmd.Flags().GetString("output")
			if format != string(output.FormatTable) {
				printer, err := output.NewPrinterFromCmd(cmd)
				if err != nil {
					return err
				}
				return printer.NameAs("context", "name").Print(contextRows(cfg, names))
			}

			if len(cfg.Contexts) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No contexts configured")
				return nil
			}

			// Print header
			fmt.Fprintf(cmd.OutOrStdout(), "%-20s %-50s %-40s %s\n", "NAME", "SERVER", "ORGANIZATION", "PROTECTED")
			fmt.Fprintf(cmd.OutOrStdout(), "%-20s %-50s %-40s %s\n", "----", "------", "------------", "---------")

			// Print contexts
			for _, name := range names {
				ctx := cfg.Contexts[name]
				current := ""
				if name == cfg.CurrentContext {
					current = "*"
				}
				orgDisplay := renderOrgDisplay(ctx)
				protected := "no"
				if ctx.IsProtected() {
					protected = "yes"
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%-1s%-19s %-50s %-40s %s\n", current, name, ctx.Server, orgDisplay, protected)
			}

			return nil
		},
	}

	return cmd
}

// contextRow is one context as -o json and -o yaml print it.
type contextRow struct {
	Current          bool    `json:"current"`
	Name             string  `json:"name"`
	Server           *string `json:"server"`
	OrganizationID   *string `json:"organization_id"`
	OrganizationName *string `json:"organization_name"`
	Output           *string `json:"output"`
	Protected        bool    `json:"protected"`
}

// contextRows returns a row for each of names, in order, with an unset value
// as null.
func contextRows(cfg *config.Config, names []string) []contextRow {
	rows := make([]contextRow, 0, len(names))
	for _, name := range names {
		ctx := cfg.Contexts[name]
		rows = append(rows, contextRow{
			Current:          name == cfg.CurrentContext,
			Name:             name,
			Server:           nonEmpty(ctx.Server),
			OrganizationID:   nonEmpty(ctx.Organization),
			OrganizationName: nonEmpty(ctx.OrganizationName),
			Output:           nonEmpty(ctx.Output),
			Protected:        ctx.IsProtected(),
		})
	}
	return rows
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
