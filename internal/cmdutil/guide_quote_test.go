package cmdutil

import (
	"bytes"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/commands/guide/guidetest"
)

// Pins the retry line quoted under dry-run-jobs "## When the launch is
// refused", with the noun the topic quotes.
func TestGuideQuoteThrottleRetryLine(t *testing.T) {
	stubSleep(t)
	var out bytes.Buffer
	calls := 0
	if _, err := RetryOnThrottle(&out, "dry runs", func() (int, error) {
		calls++
		if calls == 1 {
			return 0, &apierror.ThrottleError{RetryAfter: 15 * time.Second}
		}
		return 0, nil
	}); err != nil {
		t.Fatalf("RetryOnThrottle: %v", err)
	}
	guidetest.AssertQuoted(t, "dry-run-jobs", out.String())
}
