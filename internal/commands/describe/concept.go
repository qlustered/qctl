package describe

import (
	"fmt"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/concepts"
	"github.com/qlustered/qctl/internal/output"
	"github.com/spf13/cobra"
)

// NewStandardFieldCommand creates the describe standard-field command.
func NewStandardFieldCommand() *cobra.Command {
	return &cobra.Command{
		Use: "standard-field <slug-or-name-or-id>", Short: "Describe one standard field",
		Annotations: cmdutil.WithFormats(catalog.For("standard-field"), "table", "json", "yaml"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}
			c := concepts.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			id, err := c.ResolveID(ctx.Credential.AccessToken, args[0])
			if err != nil {
				return err
			}
			item, err := c.Get(ctx.Credential.AccessToken, id)
			if err != nil {
				return err
			}
			format, _ := cmd.Flags().GetString("output")
			if format == outputFormatJSON || format == outputFormatYAML {
				return output.WriteRaw(cmd.OutOrStdout(), format, item)
			}
			_, err = fmt.Fprint(cmd.OutOrStdout(), concepts.FormatDetail(item))
			return err
		},
	}
}
