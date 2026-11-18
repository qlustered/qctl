package rule_merges //nolint:revive // Public package name matches the established resource domain.

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/pkg/pollwait"
)

const (
	draftMigrationNote = "Waiting for review of the draft migration that adds its columns."
	noDataSourceNote   = "It waits for validation until the target has an active data source."
	describeMerge41    = "qctl describe rule-merge 41 --table orders"
)

// waitMerge is rule merge 41 in state, carrying items.
func waitMerge(state api.DatasetCopyState, items ...api.RuleMergeItemSchema) *api.RuleMergeSchema {
	return &api.RuleMergeSchema{
		ID:              41,
		CreatedAt:       time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC),
		SourceDatasetID: 7,
		TargetDatasetID: 12,
		State:           state,
		Items:           items,
	}
}

// withSummary is m carrying summary.
func withSummary(m *api.RuleMergeSchema, summary string) *api.RuleMergeSchema {
	m.Summary = &summary
	return m
}

// waitItem is item id with action, carrying note unless note is "".
func waitItem(id int, action api.DatasetCopyItemAction, note string) api.RuleMergeItemSchema {
	item := api.RuleMergeItemSchema{ID: id, Action: action}
	if note != "" {
		item.Note = &note
	}
	return item
}

// serveSequence returns a poll that answers with the next response, repeating
// the last one, and the poll count. A response is a merge or an error.
func serveSequence(responses ...any) (poll func(context.Context) (*api.RuleMergeSchema, error), polls func() int) {
	n := 0
	poll = func(context.Context) (*api.RuleMergeSchema, error) {
		response := responses[min(n, len(responses)-1)]
		n++
		if err, ok := response.(error); ok {
			return nil, err
		}
		return response.(*api.RuleMergeSchema), nil
	}
	return poll, func() int { return n }
}

// stubClock replaces Wait's clock with one that only moves when Wait sleeps,
// and returns the sleeps it was asked for.
func stubClock(t *testing.T) *[]time.Duration {
	t.Helper()
	now := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	var sleeps []time.Duration
	SetWaitClock(func() time.Time { return now }, func(d time.Duration) {
		sleeps = append(sleeps, d)
		now = now.Add(d)
	})
	t.Cleanup(ResetWaitClock)
	return &sleeps
}

// An applying merge's wait ends on each final state and returns that poll's
// merge; only applied passes the Verdict.
func TestWait_EndsOnEachFinalState(t *testing.T) {
	tests := []struct {
		name  string
		final *api.RuleMergeSchema
		want  []string // nil: the verdict passes
	}{
		{
			name:  "applied",
			final: withSummary(waitMerge(api.DatasetCopyStateApplied), "Rules: 2 created."),
		},
		{
			name:  "partially applied",
			final: withSummary(waitMerge(api.DatasetCopyStatePartiallyApplied), "Rules: 1 created, 1 failed."),
			want: []string{
				"rule merge 41 was not applied in full (state: partially_applied)",
				"\n\nRules: 1 created, 1 failed.",
			},
		},
		{
			name:  "failed",
			final: withSummary(waitMerge(api.DatasetCopyStateFailed), "Rules: 2 failed."),
			want:  []string{"rule merge 41 was not applied in full (state: failed)", "\n\nRules: 2 failed."},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stubClock(t)
			poll, polls := serveSequence(
				waitMerge(api.DatasetCopyStateApplying, waitItem(1, api.DatasetCopyItemActionCreated, draftMigrationNote)),
				tt.final,
			)
			progress := 0

			m, err := Wait(context.Background(), poll, 41, WaitOptions{
				Timeout:  time.Minute,
				Progress: func(*api.RuleMergeSchema) { progress++ },
			})
			if err != nil {
				t.Fatal(err)
			}
			if m != tt.final {
				t.Errorf("Wait returned %+v, want the settled poll's merge", m)
			}
			if polls() != 2 {
				t.Errorf("polls = %d, want 2", polls())
			}
			if progress != 1 {
				t.Errorf("progress calls = %d, want 1: the applying poll, never the settled one", progress)
			}

			verdict := Verdict(m)
			if tt.want == nil {
				if verdict != nil {
					t.Fatalf("Verdict = %v, want nil", verdict)
				}
				return
			}
			if code := qerrors.GetExitCode(verdict); code != qerrors.ExitGenericError {
				t.Errorf("exit code = %d, want %d (verdict: %v)", code, qerrors.ExitGenericError, verdict)
			}
			for _, want := range tt.want {
				if !strings.Contains(verdict.Error(), want) {
					t.Errorf("verdict lacks %q:\n%s", want, verdict)
				}
			}
		})
	}
}

func TestVerdict_WithoutASummary(t *testing.T) {
	err := Verdict(waitMerge(api.DatasetCopyStateFailed))
	if code := qerrors.GetExitCode(err); code != qerrors.ExitGenericError {
		t.Fatalf("exit code = %d, want %d (err: %v)", code, qerrors.ExitGenericError, err)
	}
	if want := "rule merge 41 was not applied in full (state: failed)"; err.Error() != want {
		t.Errorf("Verdict = %q, want %q", err, want)
	}
}

// Progress hears the first poll and each change of state, item action or item
// note, once, and never a repeat.
func TestWait_ReportsTheFirstPollAndEachChangeOnce(t *testing.T) {
	stubClock(t)
	poll, _ := serveSequence(
		waitMerge(api.DatasetCopyStateApplying,
			waitItem(1, api.DatasetCopyItemActionCreated, ""),
			waitItem(2, api.DatasetCopyItemActionKept, "It matches the target rule not_null by rule family and columns.")),
		waitMerge(api.DatasetCopyStateApplying,
			waitItem(1, api.DatasetCopyItemActionCreated, ""),
			waitItem(2, api.DatasetCopyItemActionKept, "It matches the target rule not_null by rule family and columns.")),
		waitMerge(api.DatasetCopyStateApplying,
			waitItem(1, api.DatasetCopyItemActionCreated, noDataSourceNote),
			waitItem(2, api.DatasetCopyItemActionKept, "It matches the target rule not_null by rule family and columns.")),
		waitMerge(api.DatasetCopyStateApplying,
			waitItem(1, api.DatasetCopyItemActionCreated, noDataSourceNote),
			waitItem(2, api.DatasetCopyItemActionKept, "It matches the target rule not_null by rule family and columns.")),
		waitMerge(api.DatasetCopyStateApplying,
			waitItem(1, api.DatasetCopyItemActionCreated, draftMigrationNote),
			waitItem(2, api.DatasetCopyItemActionKept, "It matches the target rule not_null by rule family and columns.")),
		waitMerge(api.DatasetCopyStateApplying,
			waitItem(1, api.DatasetCopyItemActionFailed, draftMigrationNote),
			waitItem(2, api.DatasetCopyItemActionKept, "It matches the target rule not_null by rule family and columns.")),
		waitMerge(api.DatasetCopyStatePartiallyApplied,
			waitItem(1, api.DatasetCopyItemActionFailed, "The new rule failed validation."),
			waitItem(2, api.DatasetCopyItemActionKept, "It matches the target rule not_null by rule family and columns.")),
	)
	var progress bytes.Buffer
	calls := 0
	_, err := Wait(context.Background(), poll, 41, WaitOptions{
		Timeout: time.Minute,
		Progress: func(m *api.RuleMergeSchema) {
			calls++
			WriteProgress(&progress, m)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 4 {
		t.Errorf("progress calls = %d, want 4: the first poll, two note changes and one action change", calls)
	}
	want := `rule merge 41: applying
rule merge 41: applying
  item 1: It waits for validation until the target has an active data source.
rule merge 41: applying
  item 1: Waiting for review of the draft migration that adds its columns.
rule merge 41: applying
`
	if progress.String() != want {
		t.Errorf("progress:\n--- got ---\n%s--- want ---\n%s", progress.String(), want)
	}
}

// A state change alone is a change.
func TestWaitProgressChanged_State(t *testing.T) {
	a := waitMerge(api.DatasetCopyStateApplying)
	b := waitMerge(api.DatasetCopyStatePreviewed)
	if !waitProgressChanged(a, b) {
		t.Error("a state change was not a change")
	}
	if waitProgressChanged(a, waitMerge(api.DatasetCopyStateApplying)) {
		t.Error("an identical poll was a change")
	}
}

func TestWriteProgress(t *testing.T) {
	tests := []struct {
		name  string
		merge *api.RuleMergeSchema
		want  string
	}{
		{
			name: "lists created and replaced items with a note",
			merge: waitMerge(api.DatasetCopyStateApplying,
				waitItem(1, api.DatasetCopyItemActionCreated, draftMigrationNote),
				waitItem(2, api.DatasetCopyItemActionCreated, ""),
				waitItem(3, api.DatasetCopyItemActionReplaced, "The rule **not_null** is disabled. "+noDataSourceNote),
				waitItem(4, api.DatasetCopyItemActionKept, "It matches the target rule not_null by rule family and columns."),
				waitItem(5, api.DatasetCopyItemActionSkipped, "The source rule is disabled."),
				waitItem(6, api.DatasetCopyItemActionFailed, "The new rule was deleted."),
				waitItem(7, api.DatasetCopyItemActionConflict, "It changed in both tables."),
			),
			want: `rule merge 41: applying
  item 1: Waiting for review of the draft migration that adds its columns.
  item 3: The rule not_null is disabled. It waits for validation until the target has an active data source.
`,
		},
		{
			name: "sanitizes the note and indents its further lines",
			merge: waitMerge(api.DatasetCopyStateApplying,
				waitItem(1, api.DatasetCopyItemActionCreated, "Columns \x1b[31mred\x1b[0m.\nWaiting for validation."),
			),
			want: "rule merge 41: applying\n  item 1: Columns \\x1b[31mred\\x1b[0m.\n    Waiting for validation.\n",
		},
		{
			name: "lists no item unless the merge is applying",
			merge: waitMerge(api.DatasetCopyStatePreviewed,
				waitItem(1, api.DatasetCopyItemActionCreated, draftMigrationNote),
			),
			want: "rule merge 41: previewed\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			WriteProgress(&out, tt.merge)
			if out.String() != tt.want {
				t.Errorf("--- got ---\n%s--- want ---\n%s", out.String(), tt.want)
			}
		})
	}
}

// A failed poll whose error can change on a retry is retried.
func TestWait_RetriesATransientPollError(t *testing.T) {
	stubClock(t)
	poll, polls := serveSequence(
		errors.New("connection reset by peer"),
		qerrors.FromHTTPStatusWithCause(503, errors.New("service unavailable"), "failed to get rule merge"),
		waitMerge(api.DatasetCopyStateApplied),
	)

	m, err := Wait(context.Background(), poll, 41, WaitOptions{Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if m.State != api.DatasetCopyStateApplied {
		t.Errorf("state = %s, want applied", m.State)
	}
	if polls() != 3 {
		t.Errorf("polls = %d, want 3", polls())
	}
}

// A failed poll whose error no retry can change ends the wait with that error.
func TestWait_APermanentPollErrorEndsTheWait(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code int
	}{
		{name: "bad usage", err: qerrors.NewBadUsage("rule merge 41 is not a merge into orders"), code: qerrors.ExitBadUsage},
		{name: "not found", err: qerrors.FromHTTPStatusWithCause(404, errors.New("not found"), "failed to get rule merge"), code: qerrors.ExitNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sleeps := stubClock(t)
			poll, polls := serveSequence(waitMerge(api.DatasetCopyStateApplying), tt.err)

			m, err := Wait(context.Background(), poll, 41, WaitOptions{Timeout: time.Minute, Describe: "qctl describe rule-merge 41 --table-id 42"})
			if m != nil {
				t.Errorf("Wait returned %+v, want nil", m)
			}
			if !errors.Is(err, tt.err) {
				t.Errorf("err = %v, want the poll's error", err)
			}
			if code := qerrors.GetExitCode(err); code != tt.code {
				t.Errorf("exit code = %d, want %d", code, tt.code)
			}
			// The merge goes on server-side, so the error still says where to read it.
			if !strings.Contains(err.Error(), "Next: qctl describe rule-merge 41 --table-id 42") {
				t.Errorf("err lacks the Next: line: %v", err)
			}
			if polls() != 2 || len(*sleeps) != 1 {
				t.Errorf("polls = %d, sleeps = %v, want two polls and one sleep", polls(), *sleeps)
			}
		})
	}
}

// A merge that stays applying runs the wait to its timeout, which exits 124
// and says the merge goes on.
func TestWait_TimeoutSaysOnlyTheWaitStopped(t *testing.T) {
	applying := waitMerge(api.DatasetCopyStateApplying,
		waitItem(1, api.DatasetCopyItemActionCreated, draftMigrationNote),
		waitItem(2, api.DatasetCopyItemActionReplaced, noDataSourceNote),
		waitItem(3, api.DatasetCopyItemActionKept, "It matches the target rule not_null by rule family and columns."),
		waitItem(4, api.DatasetCopyItemActionCreated, ""),
	)
	pollErr := qerrors.FromHTTPStatusWithCause(502, errors.New("bad gateway"), "failed to get rule merge")
	waiting := "rule merge 41 is still applying; only the wait stopped\n" +
		"  item 1: " + draftMigrationNote + "\n" +
		"  item 2: " + noDataSourceNote

	tests := []struct {
		name      string
		responses []any
		describe  string
		want      []string
		wantErr   error // wrapped besides ErrWaitTimeout
	}{
		{
			name:      "still applying",
			responses: []any{applying},
			describe:  describeMerge41,
			want:      []string{"after 5s: " + waiting + "\n\nNext: " + describeMerge41},
		},
		{
			name:      "the last poll failed",
			responses: []any{applying, pollErr},
			describe:  describeMerge41,
			want:      []string{waiting, "\n\nlast poll error: failed to get rule merge", "\n\nNext: " + describeMerge41},
			wantErr:   pollErr,
		},
		{
			name:      "no poll succeeded",
			responses: []any{pollErr},
			describe:  describeMerge41,
			want:      []string{"no poll of rule merge 41 succeeded", "last poll error:"},
			wantErr:   pollErr,
		},
		{
			name:      "no describe command",
			responses: []any{applying},
			want:      []string{waiting},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stubClock(t)
			poll, _ := serveSequence(tt.responses...)

			m, err := Wait(context.Background(), poll, 41, WaitOptions{Timeout: 5 * time.Second, Describe: tt.describe})
			if m != nil {
				t.Errorf("Wait returned %+v, want nil", m)
			}
			if !errors.Is(err, ErrWaitTimeout) {
				t.Fatalf("err = %v, want ErrWaitTimeout", err)
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Errorf("err does not wrap the last poll error:\n%s", err)
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitTimeout {
				t.Errorf("exit code = %d, want %d", code, qerrors.ExitTimeout)
			}
			for _, want := range tt.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error lacks %q:\n%s", want, err)
				}
			}
			if strings.Contains(err.Error(), "item 3") || strings.Contains(err.Error(), "item 4") {
				t.Errorf("error lists an item that is not waiting:\n%s", err)
			}
			switch {
			case tt.describe == "" && strings.Contains(err.Error(), "Next:"):
				t.Errorf("error names a Next: line with no describe command:\n%s", err)
			case tt.describe != "" && !strings.HasSuffix(err.Error(), "\n\nNext: "+tt.describe):
				t.Errorf("error does not end with the Next: line:\n%s", err)
			}
		})
	}
}

// A closed Done ends the wait with pollwait.ErrStopped, unwrapped, before the
// next poll.
func TestWait_DoneStopsTheWait(t *testing.T) {
	stubClock(t)
	poll, polls := serveSequence(waitMerge(api.DatasetCopyStateApplying))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cancel()
	m, err := Wait(ctx, poll, 41, WaitOptions{Timeout: time.Minute, Describe: describeMerge41})
	if m != nil || err != pollwait.ErrStopped {
		t.Fatalf("Wait = (%v, %v), want pollwait.ErrStopped", m, err)
	}
	if polls() != 0 {
		t.Errorf("polls = %d, want 0", polls())
	}
}

// The cadence is 2 s for the first 30 s and 5 s after, and the last sleep
// stops at the deadline, where one final poll runs.
func TestWait_FollowsThePollCadence(t *testing.T) {
	tests := []struct {
		name      string
		timeout   time.Duration
		wantSleep []time.Duration
	}{
		{
			name:    "eases off after 30s",
			timeout: 40 * time.Second,
			wantSleep: []time.Duration{
				2 * time.Second, 2 * time.Second, 2 * time.Second, 2 * time.Second, 2 * time.Second,
				2 * time.Second, 2 * time.Second, 2 * time.Second, 2 * time.Second, 2 * time.Second,
				2 * time.Second, 2 * time.Second, 2 * time.Second, 2 * time.Second, 2 * time.Second,
				5 * time.Second, 5 * time.Second,
			},
		},
		{
			name:      "the last sleep is clipped to the deadline",
			timeout:   3 * time.Second,
			wantSleep: []time.Duration{2 * time.Second, time.Second},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sleeps := stubClock(t)
			poll, polls := serveSequence(waitMerge(api.DatasetCopyStateApplying))

			_, err := Wait(context.Background(), poll, 41, WaitOptions{Timeout: tt.timeout})
			if !errors.Is(err, ErrWaitTimeout) {
				t.Fatalf("err = %v, want ErrWaitTimeout", err)
			}
			if got := *sleeps; !equalWaitDurations(got, tt.wantSleep) {
				t.Errorf("sleeps = %v, want %v", got, tt.wantSleep)
			}
			if polls() != len(tt.wantSleep)+1 {
				t.Errorf("polls = %d, want %d: one before each sleep and one at the deadline", polls(), len(tt.wantSleep)+1)
			}
		})
	}
}

func equalWaitDurations(a, b []time.Duration) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
