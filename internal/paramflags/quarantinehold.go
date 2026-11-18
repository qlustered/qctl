package paramflags

import (
	"fmt"
	"strings"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/spf13/cobra"
)

// QuarantineHoldFlag is the flag both data commands carry for the
// quarantine_hold query parameter. It is DERIVED from the wire name, not
// hand-registered the way --alert-item-id is, so this constant is only the
// name to refer to it by; TestQuarantineHoldFlagMatchesTheWireName pins the
// two together.
const QuarantineHoldFlag = "quarantine-hold"

// QuarantineHoldWire is the parameter's wire name, which is what both
// commands' help maps are keyed on.
const QuarantineHoldWire = "quarantine_hold"

// QuarantineHoldHelp is one line so it reads the same in both commands'
// --help.
//
// Both commands fall back to a generic "passed through to the backend" line
// for a parameter their own map does not name, which is the wrong answer for a
// flag whose values are a closed set: nothing about the flag itself says what
// the three are. The exactness is here rather than only in the resource
// page because `release` is the trap -- it is `keep_in_quarantine_ IS FALSE`,
// not "anything but held", so it excludes undecided rows.
const QuarantineHoldHelp = "Filter quarantine rows by hold decision: hold, release or undecided " +
	"(requires --is-bad-data); each is exact, so release excludes undecided rows"

// DeletedRowsWire is the deleted_rows query parameter's wire name.
const DeletedRowsWire = "deleted_rows"

// DeletedRowsHelp is the --deleted-rows help on both data commands.
const DeletedRowsHelp = "Rows by deletion state: live (default), live_and_person_deleted " +
	"(adds quarantine rows a person deleted or an earlier delete excluded; same as live without --is-bad-data) " +
	"or all (every row, deleted or not)"

// badDataOnlyHelp is both the validation inventory and the shared help for
// query parameters that have no meaning on the clean-records table. Keep it
// keyed by the wire name: each data command derives its own available flags
// from a different generated params struct, then asks this package for the
// subset it carries.
//
// review_change_history_id arrived after the original bad-data guard was
// introduced and has its own 422 in get_data_core rather than living in
// _reject_bad_data_only_params. It belongs here for the semantic reason, not
// because of which backend helper implements the refusal.
var badDataOnlyHelp = map[string]string{
	"quarantine_hold": QuarantineHoldHelp,
	"held_for_approval": "Filter quarantine rows held for approval (undecided outside a submitted data review, " +
		"with an approval hold reason); " +
		"false selects every other quarantine row (requires --is-bad-data)",
	"waiting_for_approval": "Filter quarantine rows undecided in a non-closed submitted data review; " +
		"false selects every other quarantine row (requires --is-bad-data)",
	"approval_hold_reason": "Filter quarantine rows by why the system would hold them: possible_duplicate, " +
		"review_policy or table_setting, whatever the hold decision (requires --is-bad-data)",
	"passes_validation_": "Filter quarantine rows by validation result " +
		"(requires --is-bad-data)",
	"awaiting_processing_": "Filter quarantine rows by whether processing is queued " +
		"(requires --is-bad-data)",
	"dataset_rule_ids_": "Filter quarantine rows whose rule IDs overlap these UUIDs " +
		"(requires --is-bad-data)",
	"include_dataset_rule_ids": "Show triggered dataset rules per quarantine row " +
		"(requires --is-bad-data)",
	"review_status": "Filter quarantine rows by review status: accepted, rejected or awaiting_decision " +
		"(requires --is-bad-data)",
	"review_group_id": "Scope quarantine rows to one submission's non-closed reviews " +
		"(requires --is-bad-data)",
	"review_change_history_id": "Filter quarantine rows still gated by this submitted change " +
		"(requires --is-bad-data)",
	"request_state": "Filter one submission's quarantine rows: approver (default) uses to_review, flagged_by_me, approved, on_hold; " +
		"sender uses waiting, flagged, approved, on_hold (requires --is-bad-data and --review-group-id; view set by --request-view)",
	"problem_kind": "Filter quarantine rows to one problem group's unresolved issues: alert or warning; " +
		"needs --problem-issue-type and --problem-dataset-rule-id or --problem-field-names (requires --is-bad-data)",
	"problem_issue_type": "The problem group's alert or warning type; must be a type of --problem-kind " +
		"(requires --is-bad-data)",
	"problem_dataset_rule_id": "The table rule UUID of a rule's problem group; not with --problem-field-names " +
		"(requires --is-bad-data)",
	"problem_field_names": "A field name of a problem group with no rule (repeatable); not with --problem-dataset-rule-id " +
		"(requires --is-bad-data)",
}

// BadDataOnlyHelp returns curated help for a quarantine-only wire parameter.
// The bool says whether the wire parameter is in the shared inventory.
func BadDataOnlyHelp(wireName string) (string, bool) {
	help, ok := badDataOnlyHelp[wireName]
	return help, ok
}

// ValidateDataFilterFlags runs the client-side checks over the derived query
// flags of `get data` and `download data`: a value outside an enum parameter's
// declared set, and every active quarantine-only option without
// --is-bad-data.
//
// Call it BEFORE paramflags.Build and before anything reaches the network --
// Build's override may mark flags Changed, and `get data` resolves a table
// name with a request of its own.
func ValidateDataFilterFlags(cmd *cobra.Command, flags []Flag) error {
	if err := ValidateEnums(cmd, flags); err != nil {
		return err
	}
	return validateBadDataOnlyPairings(cmd, flags)
}

// validateBadDataOnlyPairings refuses every active generated quarantine-only
// flag without --is-bad-data. The caller's derived flag slice determines the
// route-specific subset: get data carries the review filters, while download
// data does not.
//
// These parameters filter or expose columns and review state that exist only
// for quarantine rows, so there is no clean-rows reading to recover: the
// pairing is a real constraint, not a qctl restriction. Atlas refuses the same
// combinations in _reject_bad_data_only_params, except
// review_change_history_id, whose get_data_core guard returns 422 directly.
//
// Validation is still useful even though Atlas is authoritative: it names CLI
// flags rather than wire parameters, aggregates every bad generated option on
// the command line, and runs before get data spends a request resolving a
// table name or download data enqueues an export.
//
// --include-dataset-rule-ids=false is not active: unlike false-valued row
// filters such as --passes-validation=false, it asks for no extra response
// data and Atlas does not reject it.
func validateBadDataOnlyPairings(cmd *cobra.Command, flags []Flag) error {
	var active []string
	for _, flag := range flags {
		if _, ok := badDataOnlyHelp[flag.WireName]; !ok || !cmd.Flags().Changed(flag.FlagName) {
			continue
		}
		if flag.WireName == "include_dataset_rule_ids" {
			enabled, err := cmd.Flags().GetBool(flag.FlagName)
			if err != nil {
				return fmt.Errorf("failed to read --%s: %w", flag.FlagName, err)
			}
			if !enabled {
				continue
			}
		}
		active = append(active, "--"+flag.FlagName)
	}
	if len(active) == 0 {
		return nil
	}
	if cmd.Flags().Lookup(isBadDataFlag) == nil {
		// Same guard AlertItemIDFilter carries: without it the advice names a
		// flag the user cannot pass, which turns an internal wiring defect into
		// an apparent command-line mistake.
		return fmt.Errorf("internal error: flag --%s is not defined on %q",
			isBadDataFlag, cmd.CommandPath())
	}
	isBadData, err := cmd.Flags().GetBool(isBadDataFlag)
	if err != nil {
		return fmt.Errorf("failed to read --%s: %w", isBadDataFlag, err)
	}
	if !isBadData {
		verb, explanation := "requires", "this option is"
		if len(active) > 1 {
			verb, explanation = "require", "these options are"
		}
		return qerrors.NewBadUsagef(
			"%s %s --%s: %s only valid for quarantine rows",
			strings.Join(active, ", "), verb, isBadDataFlag, explanation,
		)
	}
	return nil
}
