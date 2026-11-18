package cache

import (
	"errors"
	"fmt"
	"os"

	"github.com/qlustered/qctl/internal/aliasstore"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/spf13/cobra"
)

func newResolveCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "resolve <alias>",
		Short: "Resolve a persisted UUID alias to its UUID",
		Long: `Resolve a persisted UUID alias (such as r-1) to its full UUID for the current server.

Only persisted UUID aliases are resolvable; integer IDs are never aliased.`,
		Args:        cobra.ExactArgs(1),
		Annotations: cmdutil.WithFormats(catalog.For("alias"), "table"),
		RunE: func(cmd *cobra.Command, args []string) error {
			alias := args[0]
			if _, _, err := aliasstore.ParseAlias(alias); err != nil {
				return qerrors.Wrap(qerrors.ExitBadUsage, err, "")
			}

			ctx, err := cmdutil.BootstrapWithoutAuth(cmd)
			if err != nil {
				return err
			}
			serverKey, err := config.NormalizeEndpointKey(ctx.ServerURL)
			if err != nil {
				return err
			}

			store, err := aliasstore.OpenReadOnly()
			if err != nil {
				// If the DB file doesn't exist yet (fresh install), treat it as
				// "alias not known" rather than surfacing an internal file error.
				if errors.Is(err, os.ErrNotExist) {
					return qerrors.NewNotFound(fmt.Sprintf("unknown alias %q for the current server", alias))
				}
				return err
			}
			defer store.Close()

			uuid, err := store.Resolve(serverKey, alias)
			if err != nil {
				if errors.Is(err, aliasstore.ErrAliasNotFound) {
					return qerrors.Wrap(qerrors.ExitNotFound, err, "")
				}
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), uuid)
			return nil
		},
	}
}
