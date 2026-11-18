package dataview

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
)

func historyFixture(t *testing.T) *api.DataWithCellHistorySchema {
	t.Helper()
	data, err := os.ReadFile("testdata/cell_history.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema api.DataWithCellHistorySchema
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&schema); err != nil {
		t.Fatal(err)
	}
	return &schema
}

func TestCellHistoryAlignmentAndRendering(t *testing.T) {
	page, err := Normalize(historyFixture(t), NormalizeOptions{ShowRowLogs: true, RequestedPage: 1})
	if err != nil {
		t.Fatal(err)
	}
	out := render(t, page, nil)
	for _, want := range []string{
		"| 22 | new@example.com [H] [F] |",
		"| 11 | flag-only@example.com [F] |",
		"| 33 | untouched@example.com |",
		"row_logs_ -> the CELL HISTORY section",
		"### Row 22 · email",
		`"baseline":{"upstream":9007199254740993}`,
		`"history_id":9007199254740995`,
		"Checked by Bea (admin) · 2026-10-20T11:00:00Z — Checked the source",
		"Row approved by cam@example.com (member) · 2026-10-20T12:00:00Z",
		"Row approved by (unattributed) · 2026-10-20T13:00:00Z — Former reviewer",
		"Sent for approval: October fixes · 2026-10-20T10:30:00Z · by Ada (external)",
		"review-group dddddddd-1111-2222-3333-444444444444 · data-review eeeeeeee-1111-2222-3333-444444444444",
		"Sent for approval: Resubmitted fixes · 2026-10-20T10:45:00Z · by (unattributed)",
		"Flag alert raised · 2026-10-20T10:05:00Z · by Ada (external)",
		"Flag alert cleared · 2026-10-20T10:10:00Z · by (unattributed)",
		"detach_reason=value_changed · thread 22222222-1111-2222-3333-444444444444",
		`observed="old@example.com"`,
		"Flag warning resolved · 2026-10-20T11:10:00Z · by Bea (admin)",
		"observed=null — Confirmed",
		"### Row 11 · email\n- Flag warning retracted",
		"### Row 11 · hidden_field\n- Flag alert raised",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "### Row 33") || strings.Contains(out, "Checked by cam") {
		t.Errorf("misattributed history:\n%s", out)
	}
}

func TestCellHistoryRequiresShowRowLogs(t *testing.T) {
	page, err := Normalize(historyFixture(t), NormalizeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	out := render(t, page, nil)
	if strings.Contains(out, "CELL HISTORY") || strings.Contains(out, "[F]") || strings.Contains(out, "Checked by") {
		t.Fatalf("history appeared without --show-row-logs:\n%s", out)
	}
}

func TestCellHistoryRejectsMisalignedEvents(t *testing.T) {
	for _, n := range []int{0, 2, 4} {
		schema := historyFixture(t)
		events := make([]map[string][]api.CellFlagEventSchema, n)
		schema.CellFlagEvents = &events
		if _, err := Normalize(schema, NormalizeOptions{ShowRowLogs: true}); err == nil || !strings.Contains(err.Error(), "cell_flag_events") {
			t.Errorf("%d entries for %d rows: %v", n, len(schema.Results), err)
		}
	}
}

func TestCellHistoryAbsentAndEmpty(t *testing.T) {
	for _, empty := range []bool{false, true} {
		schema := historyFixture(t)
		schema.CellFlagEvents = nil
		schema.Results = nil
		if empty {
			events := []map[string][]api.CellFlagEventSchema{}
			schema.CellFlagEvents = &events
		}
		page, err := Normalize(schema, NormalizeOptions{ShowRowLogs: true})
		if err != nil {
			t.Fatal(err)
		}
		if out := render(t, page, nil); !strings.Contains(out, "## CELL HISTORY\n- none returned on this page") {
			t.Errorf("empty history:\n%s", out)
		}
	}
}

func TestCellHistoryPreservesUnknownLogsAndEscapesText(t *testing.T) {
	schema := historyFixture(t)
	entry := schema.Results[0][1].(map[string]interface{})["email"].([]interface{})[1].(map[string]interface{})
	entry["future_key"] = []interface{}{json.Number("9007199254740997"), false}
	entry["approvals"].([]interface{})[0].(map[string]interface{})["comment"] = "line\npipe|" + string(rune(0x2028)) + "end\x1b[31m"
	entry["review_requests"] = "unexpected shape"
	page, err := Normalize(schema, NormalizeOptions{ShowRowLogs: true})
	if err != nil {
		t.Fatal(err)
	}
	out := render(t, page, nil)
	for _, want := range []string{`"future_key":[9007199254740997,false]`, `line\npipe\|\nend\x1b[31m`, `review_requests: "unexpected shape"`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}
