package suggestion

import (
	"fmt"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/rule_suggestions"
	"github.com/spf13/cobra"
)

func newDismissCommand() *cobra.Command {
	var family bool
	cmd := &cobra.Command{
		Use:   "dismiss <suggestion-id>... | --source <s> | --action <a>",
		Short: "Dismiss proposals for new rules and rule changes",
		Long: `Dismiss one or more proposals for new rules or rule changes.
The table rules keep their current configuration.

With --family, the backend never re-suggests any revision of the affected
rule families for this table. On a multi-id or filtered dismiss it applies
to every suggestion in the batch.

Several ids may be given at once. Every id is checked before anything is
written: if any one of them is unknown or already resolved, the whole
command is refused and nothing is dismissed.

Instead of ids you may select a set with --source and/or --action, which
dismisses every PENDING suggestion matching. That form shows what it
matched and asks for confirmation; --yes skips the prompt.

The table the suggestion belongs to is required: pass it with
--table <name> or --table-id <id>.

Examples:
  qctl suggestion dismiss 550e8400-e29b-41d4-a716-446655440000 --table orders --family
  qctl suggestion dismiss 550e8400-... 6f1c2d3e-... --table-id 42
  qctl suggestion dismiss --source periodic --table orders --yes`,
		Annotations: cmdutil.WithFormats(catalog.For("rule-suggestion"), "table"),
		Args:        cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runResolve(cmd, args, resolveVerb{
				name: verbDismiss,
				single: func(c *rule_suggestions.Client, token string, tableID int, id string) (*rule_suggestions.Suggestion, string, error) {
					result, err := c.Dismiss(token, tableID, id, family)
					if err != nil {
						return nil, "", fmt.Errorf("failed to dismiss suggestion: %w", err)
					}
					line := fmt.Sprintf("suggestion %s dismissed — tablerule %q unchanged\n",
						result.ID, result.InstanceName)
					if family {
						line += "rule family suppressed for this table (no future re-suggestions)\n"
					}
					return result, line, nil
				},
				bulk: func(c *rule_suggestions.Client, token string, tableID int, sel rule_suggestions.BulkSelector) (*rule_suggestions.BulkResult, error) {
					return c.BulkDismiss(token, tableID, sel, family)
				},
			})
		},
	}
	cmdutil.AddTableFlags(cmd, "Table (dataset)")
	addSelectorFlags(cmd)
	cmd.Flags().BoolVar(&family, "family", false,
		"Also suppress future suggestions of any revision of the affected rule families for this table")
	cmd.Flags().Bool("yes", false, "Skip the confirmation prompt shown when selecting by --source/--action")
	return cmd
}
