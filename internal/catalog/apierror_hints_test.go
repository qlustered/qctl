package catalog_test

import (
	"regexp"
	"testing"

	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/catalog"
)

// backtickedQctl matches a backticked qctl command inside prose.
var backtickedQctl = regexp.MustCompile("`(qctl [^`]+)`")

// TestAPIErrorActionHintsResolve resolves the command in every hint
// apierror prints for a backend action against the real tree, so a renamed
// verb cannot leave an error recommending a command that fails.
func TestAPIErrorActionHintsResolve(t *testing.T) {
	root := realRoot(t)
	hints := apierror.ActionHints()
	if len(hints) == 0 {
		t.Fatal("apierror.ActionHints() is empty; nothing to check")
	}
	for code, hint := range hints {
		m := backtickedQctl.FindStringSubmatch(hint)
		if m == nil {
			t.Errorf("%s: hint %q names no backticked qctl command", code, hint)
			continue
		}
		cmd, err := catalog.ResolveCommandLine(root, m[1])
		if err != nil {
			t.Errorf("%s: hint prints %v", code, err)
			continue
		}
		checkRecipeArgs(t, cmd, m[1])
	}
}
