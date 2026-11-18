package delete

import (
	"fmt"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/table_series"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// NewTableSeriesCommand creates the delete table-series command.
func NewTableSeriesCommand() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:         "table-series <name-or-id>",
		Short:       "Delete an empty, unreferenced table series",
		Annotations: cmdutil.WithFormats(catalog.For("table-series"), "table"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := table_series.ValidateReference(args[0]); err != nil {
				return err
			}
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}
			c := table_series.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			token := ctx.Credential.AccessToken
			id, err := c.ResolveID(token, args[0])
			if err != nil {
				return err
			}
			item, err := c.Get(token, id)
			if err != nil {
				return err
			}
			name := terminal.Sanitize(item.Name)
			if err := cmdutil.Confirm(cmd.ErrOrStderr(), yes,
				"confirmation required to delete table series; re-run with --yes",
				fmt.Sprintf("Delete table series %q (%s)? This cannot be undone.", name, id)); err != nil {
				return err
			}
			if err := c.Delete(token, id); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "table-series/%s deleted\n", name)
			return nil
		},
	}
	addDeleteFlags(cmd, &yes)
	return cmd
}
