package apierror

import (
	"testing"

	"github.com/qlustered/qctl/internal/commands/guide/guidetest"
	"github.com/qlustered/qctl/internal/config"
)

// Pins the scope note quoted under authenticate "## The scope is fixed, and a
// failure is not a reason to try another org".
func TestGuideQuoteScopeNote(t *testing.T) {
	setScopeForTest(t, "Qluster-dev", string(config.OrgSourceContext))
	guidetest.AssertQuoted(t, "authenticate", ScopeNote())
}
