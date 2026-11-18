// Package rule_upgrade implements the `qctl rule-upgrade` command group — the
// rollout of a new default rule revision to the tables still running an older
// one.
//
// The READ half is `qctl get rule-upgrades <rule>`; this group holds the
// actions. It is noun-first, like `comment-thread`, because `notify` is not a
// get and the alternatives are worse: the `notification` group means MY inbox
// and addresses notification ids, while a verb-first `notify` group would hold
// exactly one subcommand with no plausible second.
//
//nolint:revive // Package name intentionally matches the rule-upgrade command directory.
package rule_upgrade

import (
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/spf13/cobra"
)

// NewCommand creates the `rule-upgrade` parent command.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rule-upgrade",
		Short: "Act on the rollout of a new default rule revision",
		Long: `Act on the rollout of a new default rule revision.

See the rollout first:
  qctl get rule-upgrades <rule>    # which tables still run the old revision

Then re-send the notice to the people who dismissed it:
  qctl rule-upgrade notify <rule>`,
		Annotations: catalog.Parent(),
		RunE:        cmdutil.SubcommandRequired,
	}
	cmd.AddCommand(newNotifyCommand())
	return cmd
}
