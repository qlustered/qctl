package datasets

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/commands/guide/guidetest"
)

// guideSchemaDetail is the orders table the inspect-schema topic shows: a
// draft pending on top of the applied schema that modifies name and adds
// amount.
func guideSchemaDetail(t *testing.T) *TableSchemaDetail {
	t.Helper()
	detail := schemaDetailFromJSON(t, `{
	  "dataset_id": 42,
	  "dataset_name": "orders",
	  "migration_state": "draft_with_current",
	  "state": "draft",
	  "confirmed": false,
	  "diff_summary": {"total_fields": 3, "added_count": 1, "modified_count": 1, "removed_count": 0},
	  "fields": {
	    "id": {
	      "current": {"type": "Integer", "is_nullable": false},
	      "new": {"type": "Integer", "is_nullable": false}
	    },
	    "name": {
	      "action": "modified",
	      "current": {"type": "String", "args": [100], "is_nullable": false},
	      "new": {"type": "String", "args": [255], "is_nullable": false}
	    },
	    "amount": {
	      "action": "added",
	      "new": {"type": "Decimal", "args": [10, 2], "is_nullable": true}
	    }
	  }
	}`)
	detail.CreatedAt = time.Now().Add(-60 * time.Hour)
	detail.UpdatedAt = time.Now().Add(-3*time.Hour - 30*time.Minute)
	return detail
}

// guideSchemaGrid renders the default-verbosity grid.
func guideSchemaGrid(t *testing.T, detail *TableSchemaDetail) string {
	t.Helper()
	cmd := summaryRenderCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := RenderSchemaGrid(cmd, detail, 0); err != nil {
		t.Fatalf("RenderSchemaGrid: %v", err)
	}
	return buf.String()
}

// afterCommand joins a topic's command line to the output quoted under it.
// The topic separates the two with one blank line, which takes the place of
// any blank lines the output starts with. It fails t when out is blank.
func afterCommand(t *testing.T, command, out string) string {
	t.Helper()
	lines := strings.Split(out, "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	if len(lines) == 0 {
		t.Fatalf("%s rendered no output to quote", command)
	}
	return command + "\n\n" + strings.Join(lines, "\n")
}

// Pins the grid quoted under inspect-schema "## See the fields of a table".
func TestGuideQuoteSchemaGrid(t *testing.T) {
	grid := guideSchemaGrid(t, guideSchemaDetail(t))
	guidetest.AssertQuoted(t, "inspect-schema", afterCommand(t, "qctl get table-schema orders", grid))
}

// Pins the header quoted under inspect-schema "## Understand the schema
// state"; the topic cuts the grid that follows it.
func TestGuideQuoteSchemaHeader(t *testing.T) {
	header := FormatSchemaHeader(guideSchemaDetail(t))
	header += fmt.Sprintf("%-13s %s\n", "Suggestions:", FormatSuggestionsSection(SuggestionsSection{Pending: 3, Blocking: 1}))
	guidetest.AssertQuoted(t, "inspect-schema", afterCommand(t, "qctl describe table-schema orders", header))
}
