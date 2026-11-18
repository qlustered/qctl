package dry_runs //nolint:revive // Public package name matches the established resource domain.

import (
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

// The terminal set is atlas's _TERMINAL_STATES (capi/capi/views/
// dry_run_view_core.py, beside get_dry_run_stats_websocket).
func TestIsTerminal(t *testing.T) {
	for state, want := range map[IngestionJobState]bool{
		api.IngestionJobStateFinished:          true,
		api.IngestionJobStateError:             true,
		api.IngestionJobStateKilled:            true,
		api.IngestionJobStateStopped:           true,
		api.IngestionJobStateVersionIDMismatch: true,
		api.IngestionJobStateFileMissing:       true,
		api.IngestionJobStateBlocked:           true,
		api.IngestionJobStatePartitioned:       true,
		api.IngestionJobStateCreated:           false,
		api.IngestionJobStatePending:           false,
		api.IngestionJobStateRequeued:          false,
		api.IngestionJobStateRunning:           false,
		api.IngestionJobStateStaged:            false,
		"":                                     false,
		"some_new_state":                       false,
	} {
		if got := IsTerminal(state); got != want {
			t.Errorf("IsTerminal(%q) = %v, want %v", state, got, want)
		}
	}
}

func TestVerdict(t *testing.T) {
	ingestionJob := 9
	alert := 77
	errorMessage := "Dry run crashed on **orders**\nsecond line"
	loadErrors := []api.RuleLoadError{{Position: 1, RuleRevisionID: "r1", ErrorType: "NameError", Message: "name 'foo' is not defined"}}

	for _, tc := range []struct {
		name    string
		job     DryRunJobFull
		blocker *Blocker
		want    string
	}{
		{
			name: "killed",
			job:  DryRunJobFull{ID: 42, State: api.IngestionJobStateKilled},
			want: "dry-run-job 42 ended in state killed\n\nNext: qctl describe dry-run-job 42",
		},
		{
			name: "error with detail and an alert",
			job: DryRunJobFull{ID: 42, State: api.IngestionJobStateError, ErrorMessage: &errorMessage,
				RuleLoadErrors: &loadErrors, AlertItemID: &alert},
			want: "dry-run-job 42 ended in state error\n\n" +
				"rule load errors:\n  position 1 (NameError): name 'foo' is not defined\n\n" +
				"error message: Dry run crashed on orders\n  second line\n\n" +
				"alert: 77 (qctl describe alert 77)\n\n" +
				"Next: qctl describe dry-run-job 42",
		},
		{
			name:    "blocked by a known alert",
			job:     DryRunJobFull{ID: 42, State: api.IngestionJobStateBlocked, IngestionJobID: &ingestionJob},
			blocker: &Blocker{AlertID: 77, Message: "Tried 3 times, giving up."},
			want: "dry-run-job 42 ended in state blocked: alert 77 blocks it\n\n" +
				"alert message: Tried 3 times, giving up.\n\nNext: qctl describe alert 77",
		},
		{
			name: "blocked, the job naming the alert",
			job:  DryRunJobFull{ID: 42, State: api.IngestionJobStateBlocked, AlertItemID: &alert},
			want: "dry-run-job 42 ended in state blocked: alert 77 blocks it\n\nNext: qctl describe alert 77",
		},
		{
			name: "blocked by an unknown alert",
			job:  DryRunJobFull{ID: 42, State: api.IngestionJobStateBlocked, IngestionJobID: &ingestionJob},
			want: "dry-run-job 42 ended in state blocked\n\nNext: qctl describe ingestion-job 9",
		},
		{
			name: "blocked without an ingestion job",
			job:  DryRunJobFull{ID: 42, State: api.IngestionJobStateBlocked},
			want: "dry-run-job 42 ended in state blocked\n\nNext: qctl describe dry-run-job 42",
		},
		{
			name:    "a blocker for a job that is not blocked",
			job:     DryRunJobFull{ID: 42, State: api.IngestionJobStateStopped},
			blocker: &Blocker{AlertID: 77},
			want:    "dry-run-job 42 ended in state stopped\n\nNext: qctl describe dry-run-job 42",
		},
		{
			name: "a state is sanitized",
			job:  DryRunJobFull{ID: 42, State: "evil\x1b[2J"},
			want: "dry-run-job 42 ended in state evil\\x1b[2J\n\nNext: qctl describe dry-run-job 42",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := Verdict(&tc.job, tc.blocker)
			if err == nil {
				t.Fatal("Verdict = nil, want an error")
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitGenericError {
				t.Errorf("exit code = %d, want 1", got)
			}
			if err.Error() != tc.want {
				t.Errorf("Verdict =\n%s\nwant\n%s", err, tc.want)
			}
		})
	}

	if err := Verdict(&DryRunJobFull{ID: 42, State: api.IngestionJobStateFinished}, nil); err != nil {
		t.Errorf("Verdict(finished) = %v, want nil", err)
	}
}

// Client.Verdict reads a blocked job's alert from its ingestion job, and
// falls back to describing the ingestion job when that read fails.
func TestClientVerdictReadsTheBlockingAlert(t *testing.T) {
	ingestionJob := 9
	alert := 77
	for _, tc := range []struct {
		name   string
		job    DryRunJobFull
		status int
		body   map[string]any
		reads  int
		want   string
	}{
		{
			name:   "the ingestion job names it",
			job:    DryRunJobFull{ID: 42, State: api.IngestionJobStateBlocked, IngestionJobID: &ingestionJob},
			status: http.StatusOK,
			body:   map[string]any{"id": 9, "state": "blocked", "is_dry_run": true, "alert_item_id": 77, "alert_item_msg": "Tried 3 times"},
			reads:  1,
			want:   "alert 77 blocks it\n\nalert message: Tried 3 times\n\nNext: qctl describe alert 77",
		},
		{
			name:   "the read is refused",
			job:    DryRunJobFull{ID: 42, State: api.IngestionJobStateBlocked, IngestionJobID: &ingestionJob},
			status: http.StatusForbidden,
			body:   map[string]any{"detail": "forbidden"},
			reads:  1,
			want:   "ended in state blocked\n\nNext: qctl describe ingestion-job 9",
		},
		{
			name:  "the job names it",
			job:   DryRunJobFull{ID: 42, State: api.IngestionJobStateBlocked, IngestionJobID: &ingestionJob, AlertItemID: &alert},
			reads: 0,
			want:  "Next: qctl describe alert 77",
		},
		{
			name:  "not blocked",
			job:   DryRunJobFull{ID: 42, State: api.IngestionJobStateKilled, IngestionJobID: &ingestionJob},
			reads: 0,
			want:  "Next: qctl describe dry-run-job 42",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()
			reads := 0
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/ingestion-jobs/9", func(w http.ResponseWriter, _ *http.Request) {
				reads++
				testutil.RespondJSON(w, tc.status, tc.body)
			})
			err := NewClient(mock.Server.URL, testOrgID, 0).Verdict("test-token", &tc.job)
			if err == nil || !strings.HasSuffix(err.Error(), tc.want) {
				t.Errorf("Verdict = %v, want it to end with %q", err, tc.want)
			}
			if reads != tc.reads {
				t.Errorf("ingestion job reads = %d, want %d", reads, tc.reads)
			}
		})
	}
}
