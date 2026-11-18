// Package warning implements the `qctl warning` command group — the mutating
// verbs on a warning, in the shape of kubectl's `certificate approve|deny` and
// qctl's own `alert submit|retract`. Reads live under `qctl get warnings` and
// `qctl describe warning`.
//
// submit and retract act on a USER-SUBMITTED warning only. resolve is the
// exception and the mirror of `alert resolve`: it drives the actions a warning
// offers, which for a user-submitted warning is nothing --action can supply
// (all five need new_value), so in practice resolve is the rule-raised half
// and retract is the user-submitted half.
package warning

import (
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/spf13/cobra"
)

// NewCommand creates the `warning` parent command.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "warning",
		Short: "Raise or withdraw an ad-hoc warning on a table's cells",
		Long: `Raise or withdraw an ad-hoc, hand-written warning on cells of quarantined rows.

A warning is ADVISORY: unlike an alert it never holds the row. Once the row's
other issues clear it goes clean carrying the warning, and from then on the
note can go stale — editing the flagged cell on a clean row does not update or
clear it, because no producer revisits clean rows. If the objection has to
survive that, raise an alert instead ('qctl alert submit').

  qctl warning resolve 55 --action not_anomaly
  qctl warning submit  --table orders --row 1042 --field amount --comment "looks off"
  qctl warning retract 8817 --row 1042

'retract' is not 'resolve': it means "I flagged the wrong cell", not "this is
fixed".

'resolve' is the verb for a warning a RULE raised: it drives one of the actions
the warning itself offers.

'submit' and 'retract' stay user-submitted-only, and 'retract' still refuses a
rule-raised warning with exit 2. To reclassify or silence FUTURE findings,
patch the table rule ('qctl explain table-rule'), not the warning.

Reads live under:
  qctl get warnings
  qctl describe warning <id>`,
		Annotations: catalog.Parent(),
		RunE:        cmdutil.SubcommandRequired,
	}
	cmd.AddCommand(newSubmitCommand())
	cmd.AddCommand(newRetractCommand())
	cmd.AddCommand(newResolveCommand())
	cmd.AddCommand(newProposeCommand())
	return cmd
}
