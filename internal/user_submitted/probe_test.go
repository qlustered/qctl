package user_submitted //nolint:revive // Matches the backend's own name for this feature.

import (
	"bytes"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/prompt"
)

// notFound builds one row_not_found result carrying the backend's own detail —
// one status for three causes, which is the sentence the probe exists to
// disambiguate.
func notFound(rowID int) Result {
	return Result{
		RowID:      rowID,
		Status:     api.RowNotFound,
		FieldNames: []string{"amount"},
		Detail: ptr("Not a live quarantined row of this dataset, or its flagged values changed," +
			" or it is being reprocessed."),
	}
}

// fixedProbe answers the same state for every row.
func fixedProbe(state RowState, calls *[]int) RowProber {
	return func(rowID int) RowState {
		if calls != nil {
			*calls = append(*calls, rowID)
		}
		return state
	}
}

func TestSubmitFailureNamesWhatTheProbeFound(t *testing.T) {
	tests := []struct {
		name  string
		state RowState
		want  []string
		// absent must not be reported as retryable, and awaiting must not be
		// reported as a mistyped id — so each case also names what it is NOT.
		reject string
	}{
		{
			name:  "awaiting processing is retryable, and is not blamed on one verb",
			state: RowStateAwaitingProcessing,
			want: []string{
				"row 1042 IS in quarantine, but is awaiting processing",
				"Retry once it settles",
				// send-to-quarantine is ONE writer of awaiting_processing_ —
				// the reprocess UPDATE, a row edit and the ALTER evacuation set
				// it too — so the note must not read as that verb's signature.
				"a row edit",
			},
			reject: "no live quarantined row",
		},
		{
			// The degraded arm: the quarantine read succeeded and said no, the
			// clean read that would say more did not answer. It must offer the
			// check that CAN, and explains that a missing id makes the move
			// fail with not found rather than silently changing nothing.
			name:  "absent from quarantine offers the id check before the move it cannot justify",
			state: RowStateAbsentFromQuarantine,
			want: []string{
				"still on the clean side, or this table has no such row",
				"clean-side read that would tell you more did not answer",
				"--filter-by-field-name id --filter-by-field-value 1042",
				"qctl rows send-to-quarantine <table> --row-id 1042",
				"reports not found (exit 3) and changes nothing",
			},
			reject: "awaiting processing",
		},
		{
			// The probe READ the row it is naming, so it prescribes: the move
			// is justified and the id it takes is the one already typed. It
			// must not hedge with the ambiguity the second read removed.
			name:  "clean prescribes the move instead of offering a check",
			state: RowStateClean,
			want: []string{
				"live row on the CLEAN side",
				"qctl rows send-to-quarantine <table> --row-id 1042",
				"the move lands the row awaiting processing",
			},
			reject: "or this table has no such row",
		},
		{
			// The opposite fix. Suggesting the move here would send the caller
			// to write against an id with nothing behind it — the exact thing
			// the pre-split note could only warn about.
			name:  "absent from the table sends nobody to the move",
			state: RowStateAbsentFromTable,
			want: []string{
				"no live row of this table carries id 1042 on either side",
				"the id is wrong, or the row has been deleted",
				"has nothing to move here",
			},
			reject: "qctl rows send-to-quarantine <table> --row-id 1042",
		},
		{
			// The read happens AFTER the refusal, so a reprocess that settled
			// in between lands here — the one case a plain retry fixes. The
			// guard's other two conjuncts are invisible to get_data, so this
			// arm names three causes and picks none.
			name:  "settled keeps retry on the table and still names both invisible causes",
			state: RowStateQuarantined,
			want: []string{
				"NOT awaiting processing as of this read",
				"a retry is worth one attempt",
				"its flagged values changed under you or an ingest job holds it",
				"rule out none of the three",
			},
			reject: "no live quarantined row",
		},
		{
			name:   "unknown adds nothing at all",
			state:  RowStateUnknown,
			want:   []string{"row 1042 (amount): row_not_found"},
			reject: "re-read just now",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			result := &CreateResult{Results: []Result{notFound(1042)}}
			err := ReportSubmit(&out, &errOut, KindAlert, 42, result, fixedProbe(tt.state, nil))
			if err == nil {
				t.Fatal("ReportSubmit() error = nil, want the all-failed error")
			}
			// The probe explains the failure; it never re-grades it.
			if code := qerrors.GetExitCode(err); code != 3 {
				t.Errorf("GetExitCode() = %d, want 3", code)
			}
			for _, want := range tt.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error missing %q:\n%s", want, err.Error())
				}
			}
			if strings.Contains(err.Error(), tt.reject) {
				t.Errorf("error should not contain %q:\n%s", tt.reject, err.Error())
			}
			// The backend's own detail is never replaced by the probe's note.
			if !strings.Contains(err.Error(), "or it is being reprocessed.") {
				t.Errorf("the server's detail was dropped:\n%s", err.Error())
			}
		})
	}
}

// The partial-success path renders through the same function, so a row that
// failed alongside successes is explained identically — on stderr, at exit 0.
func TestProbeNoteReachesThePartialSuccessBlock(t *testing.T) {
	var out, errOut bytes.Buffer
	result := &CreateResult{
		IssueItemIds: []int{8817},
		Results: []Result{
			{RowID: 1042, Status: api.Created, FieldNames: []string{"amount"}, IssueItemID: ptr(8817)},
			notFound(1043),
		},
	}
	if err := ReportSubmit(&out, &errOut, KindAlert, 42, result,
		fixedProbe(RowStateAwaitingProcessing, nil)); err != nil {
		t.Fatalf("a submit that flagged one row is still a success, got: %v", err)
	}
	if !strings.Contains(errOut.String(), "row 1043 IS in quarantine, but is awaiting processing") {
		t.Errorf("stderr did not carry the probe note:\n%s", errOut.String())
	}
	if strings.Contains(out.String(), "re-read just now") {
		t.Errorf("the probe note belongs on stderr, not stdout:\n%s", out.String())
	}
}

// TestSuccessesAreWrittenBeforeTheProbeRuns: the probe is up to MaxProbedRows
// sequential HTTP reads with no shared deadline, so a slow data endpoint must
// not be able to withhold a success the caller already earned. The prober
// asserts from inside itself that the aggregate line is already on stdout —
// probing ahead of it (as this code first did) fails here, where a test that
// only inspected the final buffers would pass either way.
func TestSuccessesAreWrittenBeforeTheProbeRuns(t *testing.T) {
	var out, errOut bytes.Buffer
	probed := false
	probe := RowProber(func(int) RowState {
		probed = true
		if !strings.Contains(out.String(), "flagged row 1042") {
			t.Errorf("the probe ran before the success reached stdout; stdout so far:\n%q", out.String())
		}
		return RowStateAwaitingProcessing
	})
	result := &CreateResult{
		IssueItemIds: []int{8817},
		Results: []Result{
			{RowID: 1042, Status: api.Created, FieldNames: []string{"amount"}, IssueItemID: ptr(8817)},
			notFound(1043),
		},
	}
	if err := ReportSubmit(&out, &errOut, KindAlert, 42, result, probe); err != nil {
		t.Fatalf("a submit that flagged one row is still a success, got: %v", err)
	}
	if !probed {
		t.Fatal("the probe never ran, so this test proved nothing")
	}
}

// field_invalid says the FIELD is wrong. Probing the row would answer a
// question nobody asked and could point the reader at the wrong mistake.
func TestFieldInvalidIsNeverProbed(t *testing.T) {
	var probed []int
	var out, errOut bytes.Buffer
	result := &CreateResult{
		Results: []Result{
			{RowID: 1042, Status: api.FieldInvalid, FieldNames: nil},
			notFound(1043),
		},
	}
	err := ReportSubmit(&out, &errOut, KindAlert, 42, result, fixedProbe(RowStateAbsentFromQuarantine, &probed))
	if err == nil {
		t.Fatal("ReportSubmit() error = nil, want the all-failed error")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("GetExitCode() = %d, want 2 — a field_invalid anywhere still decides the code", code)
	}
	if len(probed) != 1 || probed[0] != 1043 {
		t.Errorf("probed rows = %v, want only the row_not_found row 1043", probed)
	}
}

// A nil prober is the pre-probe behaviour, byte for byte.
func TestNilProberChangesNothing(t *testing.T) {
	build := func() *CreateResult {
		return &CreateResult{Results: []Result{notFound(1042), notFound(1043)}}
	}
	var out, errOut bytes.Buffer
	err := ReportSubmit(&out, &errOut, KindAlert, 42, build(), nil)
	if err == nil {
		t.Fatal("ReportSubmit() error = nil, want the all-failed error")
	}
	if strings.Contains(err.Error(), "re-read just now") || strings.Contains(err.Error(), "were re-read") {
		t.Errorf("a nil prober must add nothing:\n%s", err.Error())
	}
	if out.Len() != 0 || errOut.Len() != 0 {
		t.Errorf("an all-failed submit prints nothing; got stdout %q stderr %q", out.String(), errOut.String())
	}
}

// Over the cap the probe stops, and SAYS it stopped: a partial diagnosis that
// went unannounced would read as "the rows below are fine".
func TestProbeStopsAtTheCapAndSaysSo(t *testing.T) {
	var probed []int
	results := make([]Result, 0, MaxProbedRows+3)
	for i := 0; i < MaxProbedRows+3; i++ {
		results = append(results, notFound(1000+i))
	}
	var out, errOut bytes.Buffer
	err := ReportSubmit(&out, &errOut, KindAlert, 42, &CreateResult{Results: results},
		fixedProbe(RowStateAwaitingProcessing, &probed))
	if err == nil {
		t.Fatal("ReportSubmit() error = nil, want the all-failed error")
	}
	if len(probed) != MaxProbedRows {
		t.Errorf("probed %d rows, want the cap of %d", len(probed), MaxProbedRows)
	}
	if want := "(only the first 10 of 13 row_not_found rows were re-read)"; !strings.Contains(err.Error(), want) {
		t.Errorf("error missing %q:\n%s", want, err.Error())
	}
	// The rows past the cap keep their line; none of them is silently dropped.
	for _, r := range results {
		if !strings.Contains(err.Error(), describeRow(r.RowID, r.FieldNames)) {
			t.Errorf("row %d vanished from the report:\n%s", r.RowID, err.Error())
		}
	}
}

// At or under the cap there is nothing to announce.
func TestNoCapNoteWhenEveryRowWasProbed(t *testing.T) {
	var out, errOut bytes.Buffer
	err := ReportSubmit(&out, &errOut, KindAlert, 42,
		&CreateResult{Results: []Result{notFound(1042)}}, fixedProbe(RowStateAbsentFromQuarantine, nil))
	if err == nil {
		t.Fatal("ReportSubmit() error = nil, want the all-failed error")
	}
	if strings.Contains(err.Error(), "were re-read") {
		t.Errorf("no cap note is owed when nothing was skipped:\n%s", err.Error())
	}
}

// The states are positional, so a server that answered for one row twice gets
// two verdicts rather than one collapsed onto both lines.
func TestProbeVerdictsAreNotKeyedByRowID(t *testing.T) {
	states := map[int]RowState{}
	probe := RowProber(func(rowID int) RowState {
		if _, seen := states[rowID]; seen {
			return RowStateAwaitingProcessing
		}
		states[rowID] = RowStateAbsentFromQuarantine
		return RowStateAbsentFromQuarantine
	})
	var out, errOut bytes.Buffer
	err := ReportSubmit(&out, &errOut, KindAlert, 42,
		&CreateResult{Results: []Result{notFound(1042), notFound(1042)}}, probe)
	if err == nil {
		t.Fatal("ReportSubmit() error = nil, want the all-failed error")
	}
	if !strings.Contains(err.Error(), "no live quarantined row") ||
		!strings.Contains(err.Error(), "awaiting processing") {
		t.Errorf("both verdicts should survive, got:\n%s", err.Error())
	}
}

func TestProbeMoveSuggestionsConfirmationSuffix(t *testing.T) {
	for _, interactive := range []bool{false, true} {
		prompt.ResetReader()
		if interactive {
			prompt.SetReader(strings.NewReader("y\n"))
		}
		t.Cleanup(prompt.ResetReader)
		want := "qctl rows send-to-quarantine <table> --row-id 7"
		if !interactive {
			want += " --yes"
		}
		for _, state := range []RowState{RowStateClean, RowStateAbsentFromQuarantine} {
			got := probeNote(7, state)
			if !strings.Contains(got, want+",") && !strings.Contains(got, want+".") {
				t.Errorf("interactive=%t state=%d: missing %q in %s", interactive, state, want, got)
			}
		}
	}
}
