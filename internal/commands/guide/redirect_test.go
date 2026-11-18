// Package guide_test is external because internal/commands/root imports
// internal/commands/guide — package guide itself cannot import root without
// an import cycle. Driving the real root command tree also matches the
// convention Task 3's external test packages (e.g. explain, api_resources)
// already set.
package guide_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/commands/root"
)

// runGuideCmd executes qctl in-process through the real root command. Unlike
// guide_test.go's runGuide (which builds a standalone guide command), this
// drives root.GetRootCmd() — a package singleton — so every flag it sets must
// be reset in t.Cleanup.
func runGuideCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	r := root.GetRootCmd()
	var buf bytes.Buffer
	r.SetOut(&buf)
	r.SetErr(&buf)
	r.SetArgs(args)
	t.Cleanup(func() {
		r.SetArgs(nil)
		r.SetOut(nil)
		r.SetErr(nil)
		_ = r.PersistentFlags().Set("output", "table")
	})
	err := r.Execute()
	return buf.String(), err
}

// A resource name is the single most likely wrong guess at a guide topic.
// Erroring on it sends the reader nowhere; pointing costs one line.
// "backup-settings"'s page (internal/resources/pages/backup-settings.md) declares no
// guide, so this also pins the other half of the conditional: when a
// resource has no guide topic, the "Its workflow guide is:" block must not
// appear.
func TestGuideResourceNamePointsAtExplain(t *testing.T) {
	out, err := runGuideCmd(t, "guide", "backup-settings")
	if err != nil {
		t.Fatalf("qctl guide backup-settings must not error: %v", err)
	}
	if !strings.Contains(out, "qctl explain backup-settings") {
		t.Errorf("expected a pointer to explain, got:\n%s", out)
	}
	if strings.Contains(out, "Its workflow guide is:") {
		t.Errorf("backup-settings has no guide topic; the workflow-guide block must not appear, got:\n%s", out)
	}
}

// TestGuideResourceWithGuideTopicPointsAtBoth exercises the other side of the
// same conditional: "table" (internal/resources/pages/table.md) declares
// guide: onboard-table, so the redirect must surface both the explain
// pointer and the workflow guide pointer. Without this test, deleting or
// inverting the `if r.Guide != ""` in printTopic would leave every other
// guide test green.
func TestGuideResourceWithGuideTopicPointsAtBoth(t *testing.T) {
	out, err := runGuideCmd(t, "guide", "table")
	if err != nil {
		t.Fatalf("qctl guide table must not error: %v", err)
	}
	if !strings.Contains(out, "qctl explain table") {
		t.Errorf("expected a pointer to explain, got:\n%s", out)
	}
	if !strings.Contains(out, "Its workflow guide is:") {
		t.Errorf("table declares a guide topic; expected the workflow-guide block, got:\n%s", out)
	}
	if !strings.Contains(out, "qctl guide onboard-table") {
		t.Errorf("expected a pointer to table's guide topic (onboard-table), got:\n%s", out)
	}
}

func TestGuideNonResourceStillErrors(t *testing.T) {
	if _, err := runGuideCmd(t, "guide", "bogus"); err == nil {
		t.Fatal("an unknown, non-resource topic must still error")
	}
}
