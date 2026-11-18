package get

import (
	"unicode/utf8"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/spf13/cobra"
)

// searchMinLength is the backend's floor, not qctl's. Every list endpoint qctl
// calls drops a search of two characters or fewer and returns the unfiltered
// set: atlas get_search_filters in capi/capi/views/pagination.py (repeated in
// get_paginated_results_by_primary_key and get_paginated_results_order_by,
// and inline for rule families in get_rule_families_calculated,
// capi/capi/controllers/rule_view_controller.py). The server records the drop as
// SearchFilterResult(applied=False), but no response schema carries that field,
// so qctl cannot detect it after the fact and has to enforce the minimum before
// the request.
const searchMinLength = 3

// parseSearchFlag reads --search and rejects a value the server would silently
// ignore. Forwarding one prints a full listing that looks filtered: real rows,
// a filter that was never applied, exit 0 -- the one outcome a caller cannot
// tell from a genuine match. Bad usage, so exit 2, matching validateSortField,
// which already refuses an --order-by the backend would not honor.
//
// The count is in runes because the server's guard is Python len() over a str,
// which counts code points: two CJK characters are six bytes and would pass a
// byte-length check while the server still dropped them.
//
// An absent flag and an explicit --search "" both return ("", nil): neither
// sends a filter, so neither can misreport one. The value is not trimmed --
// the backend measures the raw string too.
func parseSearchFlag(cmd *cobra.Command) (string, error) {
	search, _ := cmd.Flags().GetString("search")
	if search == "" {
		return "", nil
	}
	if utf8.RuneCountInString(search) < searchMinLength {
		return "", qerrors.NewBadUsagef(
			"--search %q is too short (minimum %d characters); the server ignores shorter queries "+
				"and would return unfiltered results. Widen the search or drop --search.",
			search, searchMinLength)
	}
	return search, nil
}
