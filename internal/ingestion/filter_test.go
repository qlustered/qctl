package ingestion

import (
	"slices"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

func TestParseFilter(t *testing.T) {
	params, err := ParseFilter("kind=dry_run,stored-item-id=7")
	if err != nil {
		t.Fatalf("ParseFilter: %v", err)
	}
	if want := []IngestionJobKind{api.IngestionJobKindDryRun}; !slices.Equal(params.Kinds, want) {
		t.Errorf("Kinds = %v, want %v", params.Kinds, want)
	}
	if params.StoredItemID == nil || *params.StoredItemID != 7 {
		t.Errorf("StoredItemID = %v, want 7", params.StoredItemID)
	}

	params, err = ParseFilter("table-id=5")
	if err != nil {
		t.Fatalf("ParseFilter: %v", err)
	}
	if !slices.Equal(params.Kinds, RunnableKinds()) {
		t.Errorf("a filter without a kind has Kinds %v, want RunnableKinds %v", params.Kinds, RunnableKinds())
	}

	for _, filter := range []string{
		"kind=file|dry_run",
		"kind=composite_carrier",
		"kinds=file|quarantine_reprocess",
		"file-id=x",
	} {
		if _, err := ParseFilter(filter); qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
			t.Errorf("ParseFilter(%q): exit code %d, want %d (%v)", filter, qerrors.GetExitCode(err), qerrors.ExitBadUsage, err)
		}
	}
}
