package merge

import (
	"bytes"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/catalog"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

func TestMergeCommand_IsADispatchParentOfTableRules(t *testing.T) {
	cmd := NewCommand()
	if got := cmd.Annotations[catalog.AnnotationResource]; got != catalog.Dispatch {
		t.Errorf("merge resource annotation = %q, want %q", got, catalog.Dispatch)
	}
	sub, _, err := cmd.Find([]string{"table-rules"})
	if err != nil || sub.Name() != "table-rules" {
		t.Fatalf("merge has no table-rules subcommand: %v", err)
	}
	if got := sub.Annotations[catalog.AnnotationResource]; got != "rule-merge" {
		t.Errorf("table-rules resource annotation = %q, want rule-merge", got)
	}
}

func TestMergeCommand_UnknownSubcommandExitsTwo(t *testing.T) {
	root := setupTestCommand()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"merge", "table-rule"})
	err := root.Execute()
	wantExit(t, err, qerrors.ExitBadUsage)
	if !strings.Contains(err.Error(), "table-rules") {
		t.Errorf("no suggestion of table-rules: %v", err)
	}
}
