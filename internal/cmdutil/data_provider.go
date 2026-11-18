package cmdutil

import (
	"github.com/qlustered/qctl/internal/datasets"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/spf13/cobra"
)

// AddSharedDataFlag registers shared-table selection for a data-plane command.
func AddSharedDataFlag(cmd *cobra.Command) {
	cmd.Flags().Bool("shared", false, "Use a table shared from another organization")
}

// SharedData validates unsupported options for shared-table operations.
func SharedData(cmd *cobra.Command, unsupported ...string) (bool, error) {
	shared, _ := cmd.Flags().GetBool("shared")
	if shared {
		for _, name := range unsupported {
			if cmd.Flags().Changed(name) {
				return false, qerrors.NewBadUsagef("--%s is not supported with --shared", name)
			}
		}
	}
	return shared, nil
}

// ResolveDataTableID resolves the table within the selected provider.
func ResolveDataTableID(ctx *CommandContext, cmd *cobra.Command, positional string, thirdParty bool) (int, error) {
	if !thirdParty {
		return ResolveTableID(ctx, cmd, positional)
	}
	selector, err := requiredTableSelector(cmd, positional, true)
	if err != nil {
		return 0, err
	}
	if selector.id != 0 {
		return selector.id, nil
	}
	c := datasets.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity).WithThirdParty(true)
	if selector.name != "" {
		table, err := c.ResolveByName(ctx.Credential.AccessToken, selector.name, "--table-id")
		if err != nil {
			return 0, err
		}
		return table.ID, nil
	}
	return c.ResolveID(ctx.Credential.AccessToken, selector.positional)
}
