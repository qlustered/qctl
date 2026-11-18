package auth

import (
	"fmt"
	"io"
	"strings"

	"github.com/qlustered/qctl/internal/auth"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// NewMeCommand creates the auth me command
func NewMeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "me",
		Aliases: []string{"whoami"},
		Short:   "Display information about the current user",
		Long: `Display information about the currently authenticated user, including their active organization.

A Withheld Permissions line lists the permissions your role grants that your
organization has turned off, such as dataset:data:write and source:data:write
when it turns off data editing; it is left out when there are none.`,
		Annotations: cmdutil.WithFormats(catalog.For("session"), "table", "json", "yaml"),
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Bootstrap auth context
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}

			// Create client and get user info
			authClient := auth.NewClient(ctx.ServerURL, ctx.Verbosity)
			userInfo, err := authClient.GetMe(ctx.Credential.AccessToken, ctx.OrganizationID)
			if err != nil {
				return fmt.Errorf("failed to get user info: %w", err)
			}

			// Print output based on format
			outputFormat, _ := cmd.Flags().GetString("output")
			if outputFormat == "table" || outputFormat == "" {
				printMeTable(cmd.OutOrStdout(), ctx.Config, userInfo, ctx.Verbosity)
			} else {
				return printMeStructured(cmd, userInfo)
			}

			return nil
		},
	}

	return cmd
}

// printMeTable prints user info in table format with context.
//
// Every value here is rendered through terminal.Sanitize. The profile fields
// are the server's, and the context fields are only as trustworthy: server,
// organization and organizationName are written into the config by `auth
// login` from what the server answered. printMeStructured is deliberately NOT
// sanitized -- JSON and YAML must carry the server's exact bytes.
func printMeTable(out io.Writer, cfg *config.Config, userInfo *auth.UserMeResponse, verbosity int) {
	// Display context and server info
	if cfg.CurrentContext != "" {
		fmt.Fprintf(out, "Context:                %s\n", terminal.Sanitize(cfg.CurrentContext))
	}

	ctx, _ := cfg.GetCurrentContext()
	if ctx != nil && ctx.Server != "" {
		fmt.Fprintf(out, "Server:                 %s\n", terminal.Sanitize(ctx.Server))
	}

	fmt.Fprintf(out, "ID:                     %s\n", terminal.Sanitize(userInfo.ID))
	fmt.Fprintf(out, "Email:                  %s\n", terminal.Sanitize(userInfo.Email))
	fmt.Fprintf(out, "Role:                   %s\n", terminal.Sanitize(userInfo.Role))

	// Display configured organization from context
	printConfiguredOrg(out, cfg, verbosity > 0)

	// Display active organizations (show IDs at verbosity > 0)
	printActiveOrgs(out, userInfo, verbosity > 0)

	fmt.Fprintf(out, "Is Active:              %v\n", userInfo.IsActive)

	if len(userInfo.WithheldPermissions) > 0 {
		fmt.Fprintf(out, "Withheld Permissions:   %s (your role grants them; the organization turned them off)\n",
			terminal.Sanitize(strings.Join(userInfo.WithheldPermissions, ", ")))
	}
}

// printConfiguredOrg prints the configured organization from the context.
func printConfiguredOrg(out io.Writer, cfg *config.Config, verbose bool) {
	ctx, err := cfg.GetCurrentContext()
	if err != nil {
		fmt.Fprintf(out, "Configured Org:         (none)\n")
		return
	}

	if ctx.Organization != "" {
		if verbose {
			if ctx.OrganizationName != "" {
				fmt.Fprintf(out, "Configured Org:         %s (%s)\n", terminal.Sanitize(ctx.OrganizationName), terminal.Sanitize(ctx.Organization))
			} else {
				fmt.Fprintf(out, "Configured Org:         %s\n", terminal.Sanitize(ctx.Organization))
			}
		} else {
			if ctx.OrganizationName != "" {
				fmt.Fprintf(out, "Configured Org:         %s\n", terminal.Sanitize(ctx.OrganizationName))
			} else {
				fmt.Fprintf(out, "Configured Org:         %s\n", terminal.Sanitize(ctx.Organization))
			}
		}
	} else {
		fmt.Fprintf(out, "Configured Org:         (none)\n")
	}
}

// printActiveOrgs prints the list of active organizations.
func printActiveOrgs(out io.Writer, userInfo *auth.UserMeResponse, verbose bool) {
	if len(userInfo.ActiveOrganizationNames) > 0 {
		orgList := formatOrgList(userInfo.ActiveOrganizationIDs, userInfo.ActiveOrganizationNames, verbose)
		fmt.Fprintf(out, "Active Organizations:   %s\n", terminal.Sanitize(orgList))
	} else {
		fmt.Fprintf(out, "Active Organizations:   []\n")
	}
}

// printMeStructured prints user info in json/yaml format.
func printMeStructured(cmd *cobra.Command, userInfo *auth.UserMeResponse) error {
	printer, err := output.NewPrinterFromCmd(cmd)
	if err != nil {
		return fmt.Errorf("failed to create output printer: %w", err)
	}

	if err := printer.Print(userInfo); err != nil {
		return fmt.Errorf("failed to print output: %w", err)
	}

	return nil
}

// formatOrgList formats the organization list for display
func formatOrgList(orgIDs []string, orgNames []string, verbose bool) string {
	if len(orgNames) == 0 {
		return "[]"
	}

	var parts []string
	for i, name := range orgNames {
		if verbose && i < len(orgIDs) {
			parts = append(parts, fmt.Sprintf("%s (%s)", name, orgIDs[i]))
		} else {
			parts = append(parts, name)
		}
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
