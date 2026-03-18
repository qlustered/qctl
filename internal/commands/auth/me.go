package auth

import (
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/auth"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/output"
	"github.com/spf13/cobra"
)

// NewMeCommand creates the auth me command
func NewMeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "me",
		Short: "Display information about the current user",
		Long:  `Display information about the currently authenticated user, including their active organization.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Bootstrap auth context
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}

			// Get organization ID from context for the GetMe call
			configCtx, _ := ctx.Config.GetCurrentContext()
			orgID := ""
			if configCtx != nil {
				orgID = configCtx.Organization
				// Fall back to first cached org if no default set
				if orgID == "" && len(configCtx.Organizations) > 0 {
					orgID = configCtx.Organizations[0].ID
				}
			}

			if orgID == "" {
				return fmt.Errorf("no organization configured. Please login again to set up organization context")
			}

			// Create client and get user info
			authClient := auth.NewClient(ctx.ServerURL, ctx.Verbosity)
			userInfo, err := authClient.GetMe(ctx.Credential.AccessToken, orgID)
			if err != nil {
				return fmt.Errorf("failed to get user info: %w", err)
			}

			// Print output based on format
			outputFormat, _ := cmd.Flags().GetString("output")
			if outputFormat == "table" || outputFormat == "" {
				printMeTable(ctx.Config, userInfo, ctx.Verbosity)
			} else {
				return printMeStructured(cmd, userInfo)
			}

			return nil
		},
	}

	return cmd
}

// printMeTable prints user info in table format with context.
func printMeTable(cfg *config.Config, userInfo *auth.UserMeResponse, verbosity int) {
	// Display context and server info
	if cfg.CurrentContext != "" {
		fmt.Printf("Context:                %s\n", cfg.CurrentContext)
	}

	ctx, _ := cfg.GetCurrentContext()
	if ctx != nil && ctx.Server != "" {
		fmt.Printf("Server:                 %s\n", ctx.Server)
	}

	fmt.Printf("ID:                     %s\n", userInfo.ID)
	fmt.Printf("Email:                  %s\n", userInfo.Email)
	fmt.Printf("Role:                   %s\n", userInfo.Role)

	// Display configured organization from context
	printConfiguredOrg(cfg, verbosity > 0)

	// Display active organizations (show IDs at verbosity > 0)
	printActiveOrgs(userInfo, verbosity > 0)

	// Display cached organizations count
	if ctx != nil && len(ctx.Organizations) > 0 {
		fmt.Printf("Cached Orgs:            %d\n", len(ctx.Organizations))
	}

	fmt.Printf("Is Active:              %v\n", userInfo.IsActive)
	fmt.Printf("Show Advanced UI:       %v\n", userInfo.ShowAdvancedUI)
}

// printConfiguredOrg prints the configured organization from the context.
func printConfiguredOrg(cfg *config.Config, verbose bool) {
	ctx, err := cfg.GetCurrentContext()
	if err != nil {
		fmt.Printf("Configured Org:         (none)\n")
		return
	}

	if ctx.Organization != "" {
		if verbose {
			if ctx.OrganizationName != "" {
				fmt.Printf("Configured Org:         %s (%s)\n", ctx.OrganizationName, ctx.Organization)
			} else {
				fmt.Printf("Configured Org:         %s\n", ctx.Organization)
			}
		} else {
			if ctx.OrganizationName != "" {
				fmt.Printf("Configured Org:         %s\n", ctx.OrganizationName)
			} else {
				fmt.Printf("Configured Org:         %s\n", ctx.Organization)
			}
		}
	} else {
		fmt.Printf("Configured Org:         (none)\n")
	}
}

// printActiveOrgs prints the list of active organizations.
func printActiveOrgs(userInfo *auth.UserMeResponse, verbose bool) {
	if len(userInfo.ActiveOrganizationNames) > 0 {
		orgList := formatOrgList(userInfo.ActiveOrganizationIDs, userInfo.ActiveOrganizationNames, verbose)
		fmt.Printf("Active Organizations:   %s\n", orgList)
	} else {
		fmt.Printf("Active Organizations:   []\n")
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
