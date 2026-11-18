package row_filters //nolint:revive // Matches the resource domain.

import (
	"testing"

	"github.com/qlustered/qctl/internal/api"
)

func TestLabel(t *testing.T) {
	for _, tt := range []struct {
		name   string
		filter *api.RowFilter
		want   string
	}{
		{"no filter", nil, ""},
		{"missing description", &api.RowFilter{}, "Checks only some rows"},
		{"empty description", &api.RowFilter{Description: strPtr("")}, "Checks only some rows"},
		{"blank description", &api.RowFilter{Description: strPtr(" \t\n")}, "Checks only some rows"},
		{"description", &api.RowFilter{Description: strPtr("Completed bookings")}, "Completed bookings"},
		{"padded description", &api.RowFilter{Description: strPtr(" \tCompleted bookings\n ")}, "Completed bookings"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := Label(tt.filter); got != tt.want {
				t.Errorf("Label() = %q, want %q", got, tt.want)
			}
		})
	}
}
