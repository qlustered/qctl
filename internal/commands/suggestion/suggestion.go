// Package suggestion implements rule suggestion accept, dismiss and edit commands.
package suggestion

import (
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/spf13/cobra"
)

// NewCommand creates the `suggestion` parent command.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "suggestion",
		Short: "Resolve proposals for new rules and rule changes",
		Long: `Resolve proposals to create, update or delete a table's rules.

Profiling proposes new rules. Your own rule instantiations and edits can also
be staged for schema review or validation. Accepting applies the proposal;
dismissing discards it. Revalidation candidates are promoted by their own
validation run and cannot be accepted manually.

Typical flow:
  1. qctl get rule-suggestions --table-id <id> --show-params  # inspect pending proposals
  2. qctl describe rule-suggestion <id> --table-id <id>   # inspect one
  3. qctl suggestion edit <id> --table-id <id>            # optionally tune it first
  4. qctl suggestion accept|dismiss <id> --table-id <id>  # resolve it

accept and dismiss also take SEVERAL ids at once, or no id at all plus
--source/--action, which selects every pending suggestion matching. The
multi-id form checks every id before writing anything and refuses the whole
command if one is unusable; the --source/--action form shows what it matched
and asks, and --yes skips that prompt. Ids and --source/--action cannot be
combined. edit always takes exactly one id.

The table can also be given by name: --table <name>.

Suggestion ids are full UUIDs — copy them verbatim from command output.

How staging works: qctl guide instantiate-rule`,
		Annotations: catalog.Parent(),
		RunE:        cmdutil.SubcommandRequired,
	}
	cmd.AddCommand(newAcceptCommand())
	cmd.AddCommand(newDismissCommand())
	cmd.AddCommand(newEditCommand())
	return cmd
}
