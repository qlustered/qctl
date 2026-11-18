package undoredo

import (
	"strings"
	"testing"
)

func TestHelpDescribesOnlyTheCallersEligibleChanges(t *testing.T) {
	for _, verb := range []string{"undo", "redo"} {
		for _, resource := range []string{"", "table", "cloud-source", "destination"} {
			t.Run(strings.TrimSpace(verb+" "+resource), func(t *testing.T) {
				args := []string{verb}
				if resource != "" {
					args = append(args, resource)
				}
				out, err := run(t, append(args, "--help")...)
				if err != nil {
					t.Fatalf("help: %v\n%s", err, out)
				}
				for _, want := range []string{"only your own eligible", "whatever your role", "Read the preview"} {
					if !strings.Contains(out, want) {
						t.Errorf("help lacks %q:\n%s", want, out)
					}
				}
				for _, stale := range []string{"ANY user", "not just yours", "colleague's change", "WHOLE change chain", "if anyone changed"} {
					if strings.Contains(out, stale) {
						t.Errorf("help still contains %q:\n%s", stale, out)
					}
				}
				want := "reversing your most recent eligible change"
				if verb == "redo" {
					want = "re-applying your next eligible undone change"
				}
				if !strings.Contains(out, want) {
					t.Errorf("help lacks direction %q:\n%s", want, out)
				}
			})
		}
	}
}
