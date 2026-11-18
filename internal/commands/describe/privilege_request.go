package describe

import (
	"fmt"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/privilege_requests"
	"github.com/spf13/cobra"
)

// NewPrivilegeRequestCommand creates the describe privilege-request command.
func NewPrivilegeRequestCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "privilege-request <request-id>",
		Short: "Show one ops privilege request, its people and its audit rows",
		Long: `Show one break-glass request to grant (escalate) or revoke (demote) a
user's ops eligibility: its status, the target (with their is_ops value now),
the requestor, the decision, the justification, the audit rows its approval
wrote, and -- while it is Pending -- the commands that decide it.

This is an operations surface: the route requires the ops role, so an admin or
editor gets a 403 (exit 4). The request id is the full UUID printed by
'qctl get privilege-requests'; an unknown id exits 3.

Pending is what the server says, not a promise: the server evaluates expiry
only when someone tries to approve, and qctl never computes it.

The default output is a self-describing text payload designed for both humans
and AI agents. Use -o json|yaml for the raw API response.

Examples:
  qctl describe privilege-request 0190bbbb-1111-2222-3333-444444444444
  qctl describe privilege-request 0190bbbb-1111-2222-3333-444444444444 -o json`,
		Annotations: cmdutil.WithFormats(catalog.For("privilege-request"), "table", "json", "yaml"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Before Bootstrap: a malformed id is decidable locally, is bad
			// usage (exit 2), and must cost nothing.
			id, err := cmdutil.ParseUUIDArg(args[0], "privilege request id")
			if err != nil {
				return err
			}
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}
			client := privilege_requests.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			detail, err := client.Get(ctx.Credential.AccessToken, id)
			if err != nil {
				return fmt.Errorf("failed to get privilege request: %w", err)
			}

			if cmd.Flags().Changed("output") {
				outputFormat, _ := cmd.Flags().GetString("output")
				switch outputFormat {
				case outputFormatJSON, outputFormatYAML:
					return output.WriteRaw(cmd.OutOrStdout(), outputFormat, detail)
				}
			}

			_, err = fmt.Fprint(cmd.OutOrStdout(), privilege_requests.RenderDetail(detail))
			return err
		},
	}
	return cmd
}
