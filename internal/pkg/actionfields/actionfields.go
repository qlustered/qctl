// Package actionfields validates the request fields a manifest supplies for one
// alert or warning resolve action against the backend's published contract.
//
// The contract is api.AlertActionFields / api.WarningActionFields, generated
// from the spec's x-alert-action-fields / x-warning-action-fields extensions,
// which atlas builds from ACTION_REQUEST_FIELDS
// (capi/capi/controllers/alert_action_fields.py). Nothing here is written down
// per action: a regenerated spec changes what qctl accepts with no code change.
//
// This package exists so the alert and warning sides share ONE implementation
// of the refusal, the way internal/comments is the one implementation of the
// object-shaped-422 handling both domains need.
package actionfields

import (
	"fmt"
	"sort"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

// Present is the set of wire field names a manifest actually supplied.
//
// "Supplied" means non-nil for a scalar pointer and NON-EMPTY for a list or a
// map. That one definition reproduces three of the backend's documented 400s
// without transcribing any of its prose: `ids: []` selects no rows, so it must
// not satisfy the overwrite_value one-of group (_handle_the_overwrite,
// capi/capi/controllers/alert_controller.py), nor the `ids` requirement of
// discard_cell_override, reapply_cell_override and ignore_rule_for_row.
type Present map[string]bool

// Set records name as supplied when ok. It is a method rather than a plain map
// write so every caller spells the emptiness decision the same way.
func (p Present) Set(name string, ok bool) {
	if ok {
		p[name] = true
	}
}

// Validate checks one action's supplied fields against fields.
//
// Every rejection is bad usage (exit 2): each refuses something the user wrote,
// before any request is sent. It reports EVERY problem in one error rather than
// the first, so a wrong manifest is fixed in one pass.
//
// Notes are deliberately not enforced. api.ActionFields.Note is prose the
// backend's own schema description marks "not machine-consumable", and a
// hand-transcribed conditional would become a FALSE refusal the day the backend
// loosens it, with nothing in qctl able to notice. Callers print the note
// alongside a 4xx instead.
func Validate(action string, fields api.ActionFields, present Present) error {
	var problems []string

	var unread []string
	for name := range present {
		if !fields.Accepts(name) {
			unread = append(unread, name)
		}
	}
	sort.Strings(unread) // map iteration order must not reorder the message
	for _, name := range unread {
		problems = append(problems, fmt.Sprintf("spec.%s is not read by this action", name))
	}

	for _, name := range fields.MissingRequired(present) {
		problems = append(problems, fmt.Sprintf("spec.%s is required and not set", name))
	}
	for _, group := range fields.UnsatisfiedOneOf(present) {
		problems = append(problems, fmt.Sprintf("at least one of %s must be set", strings.Join(group, "|")))
	}

	if len(problems) == 0 {
		return nil
	}

	reads := "this action reads no fields"
	if names := fields.AllNames(); len(names) > 0 {
		reads = "this action reads: " + strings.Join(names, ", ")
	}
	return qerrors.NewBadUsagef("action %q: %s\n  %s",
		action, strings.Join(problems, "; "), reads)
}
