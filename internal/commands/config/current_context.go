package config

import (
	"fmt"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

func newCurrentContextCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:         "current-context",
		Short:       "Display the current context",
		Long:        `Display the current context name and configuration.`,
		Annotations: cmdutil.WithFormats(catalog.For("context"), "table", "json", "yaml"),
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			ctx, err := cfg.GetCurrentContext()
			if err != nil {
				return err
			}

			format, _ := cmd.Flags().GetString("output")
			if format == string(output.FormatJSON) || format == string(output.FormatYAML) {
				doc, err := newContextDocument(cmd, cfg, ctx)
				if err != nil {
					return err
				}
				printer, err := output.NewPrinterFromCmd(cmd)
				if err != nil {
					return err
				}
				return printer.Print(doc)
			}

			verbosity, _ := cmd.Root().PersistentFlags().GetCount("verbose")

			// Print current context information
			fmt.Fprintf(cmd.OutOrStdout(), "Current context: %s\n", cfg.CurrentContext)
			fmt.Fprintf(cmd.OutOrStdout(), "  Server: %s\n", ctx.Server)
			if ctx.Output != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "  Output: %s\n", ctx.Output)
			}
			if ctx.Organization != "" {
				if ctx.OrganizationName != "" {
					// The name is cached from the server's organization list,
					// not typed by the user, so it is untrusted text on its way
					// to a terminal.
					orgName := terminal.Sanitize(ctx.OrganizationName)
					if verbosity > 0 {
						fmt.Fprintf(cmd.OutOrStdout(), "  Organization: %s (%s)\n", orgName, ctx.Organization)
					} else {
						fmt.Fprintf(cmd.OutOrStdout(), "  Organization: %s\n", orgName)
					}
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "  Organization: %s\n", ctx.Organization)
				}
			}
			if ctx.IsProtected() {
				fmt.Fprintf(cmd.OutOrStdout(), "  Protected: yes (destructive commands need --context %s; to allowlist: qctl config set-context %s --protected=false)\n",
					cfg.CurrentContext, cfg.CurrentContext)
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "  Protected: no (allowlisted)")
			}
			return nil
		},
	}

	return cmd
}

// contextDocument is the current context as -o json and -o yaml print it: the
// stored context, plus the scope the next API command would use.
type contextDocument struct {
	Name          string        `json:"name"`
	Server        *string       `json:"server"`
	Output        *string       `json:"output"`
	Organization  *orgDocument  `json:"organization"`
	Organizations []orgDocument `json:"organizations"`
	Protected     bool          `json:"protected"`
	Scope         scopeDocument `json:"scope"`
}

type orgDocument struct {
	ID   string  `json:"id"`
	Name *string `json:"name"`
}

// scopeDocument is the server and organization after --server, --org,
// QCTL_SERVER and QCTL_ORG, each with the setting that supplied it: flag, env
// or context.
type scopeDocument struct {
	Server             *string `json:"server"`
	ServerSource       *string `json:"server_source"`
	OrganizationID     *string `json:"organization_id"`
	OrganizationName   *string `json:"organization_name"`
	OrganizationSource *string `json:"organization_source"`
}

// newContextDocument fails only for an organization that is selected but does
// not resolve; a server or organization selected nowhere is null.
func newContextDocument(cmd *cobra.Command, cfg *config.Config, ctx *config.Context) (contextDocument, error) {
	doc := contextDocument{
		Name:          cfg.CurrentContext,
		Server:        nonEmpty(ctx.Server),
		Output:        nonEmpty(ctx.Output),
		Organizations: []orgDocument{},
		Protected:     ctx.IsProtected(),
	}
	if ctx.Organization != "" {
		doc.Organization = &orgDocument{ID: ctx.Organization, Name: nonEmpty(ctx.OrganizationName)}
	}
	for _, o := range ctx.Organizations {
		doc.Organizations = append(doc.Organizations, orgDocument{ID: o.ID, Name: nonEmpty(o.Name)})
	}

	serverFlag, _ := cmd.Flags().GetString("server")
	if server, source, err := config.SelectServer(cfg, serverFlag); err == nil {
		doc.Scope.Server = &server
		doc.Scope.ServerSource = nonEmpty(string(source))
	}

	orgFlag, _ := cmd.Root().PersistentFlags().GetString("org")
	if _, _, err := config.OrganizationFlagOrDefault(cfg, orgFlag); err != nil {
		return doc, nil
	}
	id, name, source, err := cmdutil.ResolveOrganizationWithSource(cfg, orgFlag)
	if err != nil {
		return contextDocument{}, err
	}
	doc.Scope.OrganizationID = &id
	doc.Scope.OrganizationName = nonEmpty(name)
	doc.Scope.OrganizationSource = nonEmpty(orgSourceToken(source))
	return doc, nil
}

func orgSourceToken(source config.OrgSource) string {
	switch source {
	case config.OrgSourceFlag:
		return "flag"
	case config.OrgSourceEnv:
		return "env"
	case config.OrgSourceContext:
		return "context"
	}
	return string(source)
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
