package root

import (
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
)

// sheetsInventoryTree is sheetsDraftTree with the inventory of template 8:
// sheets Data and Notes.
var sheetsInventoryTree = strings.Replace(sheetsDraftTree, `"skeleton_id":8,`, `"skeleton_id":8,"workbook_inventory":{"has_vba":false,"sheets":[`+
	`{"name":"Data","max_row":2,"max_column":2,"merged_ranges":[],"header_rows":{"1":["Policy","Premium"]}},`+
	`{"name":"Notes","max_row":1,"max_column":1,"merged_ranges":[],"header_rows":{}}]},`, 1)

// A misspelled sheet key plans the stored sheet's DELETE before the POST the
// server would refuse; diff and apply exit 2 before either is sent.
func TestDeliverableSheetTheTemplateLacksExitsTwoBeforeAnyWrite(t *testing.T) {
	if !strings.Contains(sheetsInventoryTree, `"workbook_inventory"`) {
		t.Fatal("sheetsDraftTree no longer has the skeleton_id the inventory is spliced after")
	}
	manifest := sheetsManifest(strings.Replace(sheetsData, "Data:", "Daat:", 1), sheetsDataColumns, sheetsNotes)
	for _, args := range [][]string{{"diff"}, {"apply", "--yes"}} {
		t.Run(args[0], func(t *testing.T) {
			s := sheetsServer(t)
			answerRevisionTree(t, s.MockAPIServer, draftRevisionID, sheetsInventoryTree)
			path := writeManifest(t, t.TempDir(), "deliverable.yaml", manifest)
			out, _, err := runManifest(t, append([]string{args[0], "-f", path}, args[1:]...)...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit = %d, want %d; error = %v", code, qerrors.ExitBadUsage, err)
			}
			if !strings.Contains(err.Error(), `has no sheet "Daat"`) {
				t.Errorf("error = %v, want the refusal of sheet Daat", err)
			}
			if got := s.writes(); len(got) != 0 || out != "" {
				t.Errorf("writes = %v and stdout = %q, want neither", got, out)
			}
		})
	}
}
