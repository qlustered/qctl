package catalog_test

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
)

const (
	parameterlessClaim = "Verified parameterless today, including:"
	needsInputClaim    = "Verified to need input today, including:"
)

var backticked = regexp.MustCompile("`([a-z][a-z0-9_]*)`")

// TestAlertPageActionClaimsMatchSpec pins pages/alert.md's two lists of action
// names to x-alert-action-fields.
//
// It exists because the page got one wrong and nothing caught it: gotcha 4
// offered "an Excel sheet choice" as an example of an action --action cannot
// drive, but choose_excel_sheet takes no fields at all — it is a redirect to
// the dataset's multiple_sheets_found alert, where the sheet is chosen with
// proceed_with_sheet_name, which *is* parameterised. A [backend] gotcha can
// rot silently; this one now cannot.
func TestAlertPageActionClaimsMatchSpec(t *testing.T) {
	raw, err := os.ReadFile("../resources/pages/alert.md")
	if err != nil {
		t.Fatalf("read alert.md: %v", err)
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
			t.Errorf("alert.md has no line containing %q with backticked action names after it", c.claim)
			continue
		}
		for _, name := range names {
			fields, ok := api.AlertActionFields[name]
			if !ok {
				t.Errorf("alert.md names %q after %q, but the spec defines no such action", name, c.claim)
				continue
			}
			if fields.NeedsInput() != c.wantNeedsInput {
				t.Errorf("alert.md lists %q after %q, but NeedsInput() = %v (required %v, one_of %v)",
					name, c.claim, fields.NeedsInput(), fields.Required, fields.OneOf)
			}
		}
	}
}

// claimedActions returns the backticked identifiers that follow the claim on
// the line that carries it. Page gotchas are one bullet per line, so the line
// is the claim's whole scope.
func claimedActions(t *testing.T, page, claim string) []string {
	t.Helper()
	for _, line := range strings.Split(page, "\n") {
		i := strings.Index(line, claim)
		if i < 0 {
			continue
		}
		var names []string
		for _, m := range backticked.FindAllStringSubmatch(line[i+len(claim):], -1) {
			names = append(names, m[1])
		}
		return names
	}
	return nil
}
