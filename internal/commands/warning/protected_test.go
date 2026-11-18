package warning

import (
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
)

// TestNoWarningActionIsDestructive fails when a spec regen adds a warning
// action that cmdutil.IsDestructiveAction classifies. alert resolve gates such
// actions on a protected context in its confirmResolution; give this package's
// twin the same gate.
func TestNoWarningActionIsDestructive(t *testing.T) {
	actions := api.StringEnumValues["WarningAction"]
	if len(actions) == 0 {
		t.Fatal(`api.StringEnumValues["WarningAction"] is empty; the enum moved`)
	}
	for _, a := range actions {
		if cmdutil.IsDestructiveAction(a) {
			t.Errorf("warning action %s is destructive but warning resolve does not gate it on a protected context", a)
		}
	}
}
