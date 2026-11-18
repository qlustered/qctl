package cache

import (
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/spf13/cobra"
)

// NewCommand creates the `cache` parent command.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cache",
		Short: "Manage qctl's local cache",
		Long: `Manage qctl's local cache: persistent UUID aliases and cached rule-revision
short IDs under ~/.qctl/cache ('cache aliases'), and the qctl releases built
for your servers under ~/.qctl/versions ('cache releases').`,
		Annotations: catalog.Parent(),
		RunE:        cmdutil.SubcommandRequired,
	}
	cmd.AddCommand(newAliasesCommand())
	cmd.AddCommand(newReleasesCommand())
	return cmd
}

// newAliasesCommand creates the `cache aliases` parent command.
func newAliasesCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "aliases",
		Short: "Manage persistent UUID aliases",
		Long: `Manage persistent UUID aliases (such as r-1 for a table rule).

Aliases are permanent, scoped to the current server, and are allocated automatically
the first time qctl prints a UUID-keyed resource (for example via 'qctl get data').
Integer IDs are never aliased.

The same underlying store also caches rule-revision SHORT-ID -> full-UUID
mappings, scoped to the server and organization; those are not aliases (no
r-N is minted for them and these commands never list or resolve them), but
'qctl cache aliases clear' discards them too.`,
		Annotations: catalog.Parent(),
		RunE:        cmdutil.SubcommandRequired,
	}
	cmd.AddCommand(newListCommand())
	cmd.AddCommand(newResolveCommand())
	cmd.AddCommand(newClearCommand())
	return cmd
}
