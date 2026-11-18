package alerts

import (
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/commands/guide/guidetest"
)

// Pins the NEXT ACTIONS footer quoted under triage-alerts "## Read the NEXT
// ACTIONS footer".
// The fixture offers every action the spec lists for cant_sniff_header, so a
// regen that changes that list changes the footer the topic must quote.
func TestGuideQuoteAlertNextActions(t *testing.T) {
	resolved := false
	offered := api.IssueTypeAlertActions[string(api.AlertTypeCantSniffHeader)]
	if len(offered) == 0 {
		t.Fatal("the spec no longer lists actions for cant_sniff_header; pick another issue type for the topic's example")
	}
	actions := make([]api.AlertAction, 0, len(offered))
	for _, name := range offered {
		actions = append(actions, api.AlertAction(name))
	}
	guidetest.AssertQuoted(t, "triage-alerts", RenderNextActions(&AlertFull{
		ID:               9,
		IssueType:        api.AlertTypeCantSniffHeader,
		Resolved:         &resolved,
		AlertActionsList: &actions,
	}, atTerminal))
}

// Pins the skeleton quoted under triage-alerts "## Actions that need input".
// The topic elides the backend's note around one sentence, so the pin checks
// that sentence is still inside the rendered note, with text on both sides.
func TestGuideQuoteAlertSkeleton(t *testing.T) {
	const notePrefix = "# note from the backend: "
	const quotedNote = "map_value_once records a one-off mapping instead of a learned one, " +
		"and is REQUIRED to map a blank value"
	fieldName, fieldValue := "amount", "1,420.00"
	out := Skeleton(&AlertFull{
		ID:          9,
		DatasetID:   42,
		DatasetName: "orders",
		FieldName:   &fieldName,
		FieldValue:  &fieldValue,
	}, string(api.AlertActionMapValue), nil)
	head, note, ok := strings.Cut(out, notePrefix)
	note = strings.TrimSuffix(note, "\n")
	if !ok || head == "" || strings.Contains(note, "\n") {
		t.Fatalf("skeleton no longer is manifest lines followed by the one-line backend note the topic quotes:\n%s", out)
	}
	if before, after, found := strings.Cut(note, quotedNote); !found || before == "" || after == "" {
		t.Fatalf("backend note no longer holds %q with text on both sides:\n%s", quotedNote, note)
	}
	guidetest.AssertQuoted(t, "triage-alerts", head+notePrefix+"... "+quotedNote+" ...")
}
