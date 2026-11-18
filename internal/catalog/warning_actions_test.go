package catalog_test

import (
	"os"
	"testing"

	"github.com/qlustered/qctl/internal/api"
)

// TestWarningPageActionClaimsMatchSpec is TestAlertPageActionClaimsMatchSpec
// for the warning half: it pins pages/warning.md's two lists of action names to
// x-warning-action-fields, so a regenerated spec that parameterises one of the
// six cannot leave the page claiming it is drivable.
//
// It reuses parameterlessClaim, needsInputClaim and claimedActions from
// alert_actions_test.go — same package, one definition each.
func TestWarningPageActionClaimsMatchSpec(t *testing.T) {
	raw, err := os.ReadFile("../resources/pages/warning.md")
	if err != nil {
		t.Fatalf("read warning.md: %v", err)
	}

	checks := []struct {
		claim          string
		wantNeedsInput bool
	}{
		{parameterlessClaim, false},
		{needsInputClaim, true},
	}
	for _, c := range checks {
		names := claimedActions(t, string(raw), c.claim)
		if len(names) == 0 {
			t.Errorf("warning.md has no line containing %q with backticked action names after it", c.claim)
			continue
		}
		for _, name := range names {
			fields, ok := api.WarningActionFields[name]
			if !ok {
				t.Errorf("warning.md names %q after %q, but the spec defines no such warning action", name, c.claim)
				continue
			}
			if fields.NeedsInput() != c.wantNeedsInput {
				t.Errorf("warning.md lists %q after %q, but NeedsInput() = %v (required %v, one_of %v)",
					name, c.claim, fields.NeedsInput(), fields.Required, fields.OneOf)
			}
		}
	}
}
