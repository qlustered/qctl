package data_review //nolint:revive // Package name mirrors the hyphenated command.

import (
	"bytes"
	"strings"
	"testing"
)

// TestDataReviewHelpNamesTheRuleGroupAndTheGuide: qctl review is the other
// review group, and each help names the other so a reader who picked the
// wrong noun finds the right one.
func TestDataReviewHelpNamesTheRuleGroupAndTheGuide(t *testing.T) {
	root := testRoot()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"data-review", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"qctl review", "qctl guide review-data"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("data-review --help missing %q\n%s", want, buf.String())
		}
	}
}
