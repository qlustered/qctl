package paramflags

import (
	"fmt"

	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/spf13/cobra"
)

// AlertItemIDFlag is the flag name both data commands register.
const AlertItemIDFlag = "alert-item-id"

// isBadDataFlag is the derived flag for the get_data `is_bad_data` parameter,
// which --alert-item-id is useless without. Derived, not hand-registered, on
// both commands — Derive maps the wire name through FlagNameForWire.
const isBadDataFlag = "is-bad-data"

// alertItemIDHelp is one line so it reads the same in both commands' --help.
const alertItemIDHelp = "Filter quarantine rows to those held by this alert (requires --is-bad-data)"

// RegisterAlertItemIDFlag hand-registers --alert-item-id.
//
// It is the one get_data query parameter that cannot be derived: the spec
// declares alert_item_id as an `int | str` union, which oapi-codegen emits as
// an inline anonymous struct, so KindForField classifies it KindUnsupported
// and both commands keep it on their denylist. The value reaches the wire
// through datasets.WithAlertItemID instead — see internal/datasets/
// alert_item_filter.go for why the generated field cannot carry it.
func RegisterAlertItemIDFlag(cmd *cobra.Command) {
	cmdutil.RefInt(cmd.Flags(), AlertItemIDFlag, "alert", alertItemIDHelp)
}

// AlertItemIDFilter reads --alert-item-id back, returning (id, true, nil) when
// the user set it. Both failure modes exit 2.
//
// The --is-bad-data requirement is enforced here rather than left to the
// backend, because the backend does not enforce it and does not report it.
// The filter is applied under `if alert_item_id is not None and is_bad_data`
// (atlas cettings/.../data_controller/core.py), and alert_item_id is absent
// from get_data_core's bad-data-only 400 list — which does refuse
// dataset_rule_ids_, review_status and five others the same way. So a
// clean-rows request carrying --alert-item-id is accepted, has the filter
// dropped on the floor, and answers HTTP 200 with the whole unfiltered page:
// a silent wrong answer, and every row on it reads as one this alert holds.
//
// Alerts live on quarantine rows, so there is no clean-rows meaning to
// recover here — the pairing is a real constraint, not a qctl restriction.
func AlertItemIDFilter(cmd *cobra.Command) (int, bool, error) {
	id, ok, err := cmdutil.PositiveIDFlag(cmd, AlertItemIDFlag)
	if err != nil || !ok {
		return 0, false, err
	}
	if cmd.Flags().Lookup(isBadDataFlag) == nil {
		// Same guard PositiveIDFlag carries: without it a command that never
		// registered --is-bad-data reads back false and every --alert-item-id
		// is refused with advice the user cannot act on.
		return 0, false, fmt.Errorf("internal error: flag --%s is not defined on %q",
			isBadDataFlag, cmd.CommandPath())
	}
	isBadData, err := cmd.Flags().GetBool(isBadDataFlag)
	if err != nil {
		return 0, false, fmt.Errorf("failed to read --%s: %w", isBadDataFlag, err)
	}
	if !isBadData {
		return 0, false, qerrors.NewBadUsagef(
			"--%s requires --%s: the alert filter is only defined over quarantine rows, and the "+
				"backend drops it on a clean-rows request instead of refusing it",
			AlertItemIDFlag, isBadDataFlag)
	}
	return id, true, nil
}
