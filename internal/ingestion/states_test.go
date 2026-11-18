package ingestion

import (
	"slices"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

// TestJobStatesIsTheGeneratedEnum pins JobStates to the generated constants,
// so a generator that stops keying api.StringEnumValues by the Go type name
// fails here instead of leaving every state refused.
func TestJobStatesIsTheGeneratedEnum(t *testing.T) {
	want := []string{
		string(api.IngestionJobStateBlocked),
		string(api.IngestionJobStateCreated),
		string(api.IngestionJobStateError),
		string(api.IngestionJobStateFileMissing),
		string(api.IngestionJobStateFinished),
		string(api.IngestionJobStateKilled),
		string(api.IngestionJobStatePartitioned),
		string(api.IngestionJobStatePending),
		string(api.IngestionJobStateRequeued),
		string(api.IngestionJobStateRunning),
		string(api.IngestionJobStateStaged),
		string(api.IngestionJobStateStopped),
		string(api.IngestionJobStateVersionIDMismatch),
	}
	if got := JobStates(); !slices.Equal(got, want) {
		t.Errorf("JobStates() = %v, want %v", got, want)
	}
}

func TestJobStatesReturnsACopy(t *testing.T) {
	states := JobStates()
	states[0] = "mutated"
	if slices.Contains(JobStates(), "mutated") {
		t.Fatal("mutating JobStates' result changed the next call's")
	}
}

func TestValidateJobStateAcceptsEveryState(t *testing.T) {
	for _, state := range JobStates() {
		if err := ValidateJobState("state", state); err != nil {
			t.Errorf("ValidateJobState(%q) = %v, want nil", state, err)
		}
	}
}

func TestValidateJobStateRefusesWithBadUsage(t *testing.T) {
	for _, state := range []string{"failed", "", "Running", " running"} {
		t.Run(state, func(t *testing.T) {
			err := ValidateJobState("--states", state)
			if err == nil {
				t.Fatalf("ValidateJobState(%q) = nil, want an error", state)
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
			}
			for _, part := range []string{"--states", `"` + state + `"`, "version_id_mismatch"} {
				if !strings.Contains(err.Error(), part) {
					t.Errorf("error %q does not contain %q", err, part)
				}
			}
		})
	}
}
