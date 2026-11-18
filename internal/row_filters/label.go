package row_filters //nolint:revive // Matches the resource domain.

import (
	"strings"

	"github.com/qlustered/qctl/internal/api"
)

// Label returns a filter's description or a fallback, or blank for no filter.
func Label(f *api.RowFilter) string {
	if f == nil {
		return ""
	}
	if f.Description != nil {
		if description := strings.TrimSpace(*f.Description); description != "" {
			return description
		}
	}
	return "Checks only some rows"
}
