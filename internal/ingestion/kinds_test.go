package ingestion

import (
	"slices"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

// A kind added to the enum fails here until someone decides whether atlas
// lets it be run again and stopped (rerun_refusal and stop_refusal in atlas
// capi/capi/controllers/ingestion_controller.py) and files it on one side.
func TestEveryJobKindIsRunnableOrNot(t *testing.T) {
	runnable := []IngestionJobKind{
		api.IngestionJobKindCompositeDryRun,
		api.IngestionJobKindDryRun,
		api.IngestionJobKindFile,
	}
	if got := RunnableKinds(); !slices.Equal(got, runnable) {
		t.Errorf("RunnableKinds() = %v, want %v", got, runnable)
	}
	var all []string
	for _, k := range append(slices.Clone(runnable), unactionableKinds...) {
		all = append(all, string(k))
	}
	slices.Sort(all)
	if got := JobKinds(); !slices.Equal(got, all) {
		t.Errorf("JobKinds() = %v, want exactly the runnable and unactionable kinds %v", got, all)
	}
}

func TestParseJobKindsDropsRepeatsAndRefusesAnUnknownKind(t *testing.T) {
	got, err := ParseJobKinds("--kind", []string{"file", "dry_run", "file"})
	if err != nil {
		t.Fatalf("ParseJobKinds: %v", err)
	}
	if want := []IngestionJobKind{api.IngestionJobKindFile, api.IngestionJobKindDryRun}; !slices.Equal(got, want) {
		t.Errorf("ParseJobKinds = %v, want %v", got, want)
	}
	if _, err := ParseJobKinds("--kind", []string{"carrier"}); qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
		t.Errorf("unknown kind: exit code %d, want %d (%v)", qerrors.GetExitCode(err), qerrors.ExitBadUsage, err)
	}
}
