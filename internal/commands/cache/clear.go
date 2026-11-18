package cache

import (
	"fmt"

	"github.com/qlustered/qctl/internal/aliasstore"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	"github.com/spf13/cobra"
)

func newClearCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "clear",
		Short: "Remove the current server's cached UUID aliases and short IDs",
		Long: `Remove the current server's cached UUID aliases and short IDs.

Use --all-servers to clear every server. Clearing resets the allocation
counter, so the next UUID printed is aliased r-1 again: an alias you noted
before the clear can name a different table rule afterwards.

The same store also holds the rule-revision SHORT-IDs qctl has seen, and the
clear discards those too. Nothing breaks -- the next 'qctl get rule <short-id>'
re-learns them from a catalog listing -- but that costs a request, and in an
org with more rule revisions than one listing returns a SHORT-ID stops
resolving until 'qctl get rules' reseeds it.

Nothing on the server knows about either, so clearing can never affect a table
rule or a rule revision.`,
		Args:        cobra.NoArgs,
		Annotations: cmdutil.WithFormats(catalog.For("alias"), "table"),
		RunE: func(cmd *cobra.Command, _ []string) error {
			allServers, _ := cmd.Flags().GetBool("all-servers")

			store, err := aliasstore.Open()
			if err != nil {
				return err
			}
			defer store.Close()

			if allServers {
				if err := store.ClearAll(); err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), "Cleared aliases for all servers.")
				return nil
			}

			ctx, err := cmdutil.BootstrapWithoutAuth(cmd)
			if err != nil {
				return err
			}
			serverKey, err := config.NormalizeEndpointKey(ctx.ServerURL)
			if err != nil {
				return err
			}
			if err := store.Clear(serverKey); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Cleared aliases for %s.\n", serverKey)
			return nil
		},
	}
	cmd.Flags().Bool("all-servers", false, "Clear every server's cached aliases and short IDs")
	return cmd
}
