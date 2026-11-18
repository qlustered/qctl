//nolint:revive // Tests intentionally match the command package directory.
package queue_message

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

// answerYes makes the confirmation prompt answerable, so a test can reach the
// code past it. Without it cmdutil.Confirm short-circuits on a non-interactive
// stdin and the run never gets that far.
func answerYes(t *testing.T) {
	t.Helper()
	cmdutil.SetReader(strings.NewReader("y\n"))
	t.Cleanup(cmdutil.ResetReader)
}

type deadProbe struct {
	calls  int
	limits []string
	// idleFor holds each call's idle_for, or noIdleFor when it was not sent.
	idleFor []string
}

const noIdleFor = "<absent>"

func registerDeadList(mock *testutil.MockAPIServer, ids []int) *deadProbe {
	probe := &deadProbe{}
	mock.RegisterHandler("GET", queuePath+"/dead", func(w http.ResponseWriter, r *http.Request) {
		probe.calls++
		probe.limits = append(probe.limits, r.URL.Query().Get("limit"))
		idleFor := noIdleFor
		if vals, ok := r.URL.Query()["idle_for"]; ok {
			idleFor = vals[0]
		}
		probe.idleFor = append(probe.idleFor, idleFor)
		testutil.RespondJSON(w, http.StatusOK, api.QueueDeadListResponseSchema{
			DeadIds: ids, TotalCount: len(ids),
		})
	})
	return probe
}

func registerResurrectBatch(mock *testutil.MockAPIServer, ids []int) *[]api.QueueResurrectBatchRequestSchema {
	bodies := new([]api.QueueResurrectBatchRequestSchema)
	mock.RegisterHandler("POST", queuePath+"/resurrect-batch", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body api.QueueResurrectBatchRequestSchema
		if err := json.Unmarshal(raw, &body); err != nil {
			panic(err)
		}
		*bodies = append(*bodies, body)
		testutil.RespondJSON(w, http.StatusOK, api.QueueResurrectBatchResponseSchema{
			ResurrectedIds: ids, Count: len(ids),
		})
	})
	return bodies
}

func TestResurrectDeadDryListChangesNothing(t *testing.T) {
	mock := newQueueMessageTestEnv(t)
	probe := registerDeadList(mock, []int{7, 8, 9})
	bodies := registerResurrectBatch(mock, nil)

	out, _, err := runQueueMessage(t, "resurrect-dead", "--dry-list", "--limit", "50")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(*bodies) != 0 {
		t.Fatal("--dry-list called the resurrect-batch route")
	}
	if probe.calls != 1 {
		t.Fatalf("made %d candidate-list calls, want 1", probe.calls)
	}
	// The preview and the repair must ask for the same number of rows. The two
	// routes' own defaults are 100 and 1000, so an unforwarded --limit would
	// make the preview describe a different slice than the run it previews.
	if probe.limits[0] != "50" {
		t.Errorf("candidate list asked for limit %q, want 50", probe.limits[0])
	}
	// No --older-than, so no idle_for: the server's default window is the one
	// resurrect-batch falls back to as well.
	if probe.idleFor[0] != noIdleFor {
		t.Errorf("candidate list sent idle_for %q with no --older-than", probe.idleFor[0])
	}
	if !strings.Contains(out, "7, 8, 9") {
		t.Errorf("output %q does not list the candidate ids", out)
	}
	if !strings.Contains(out, "as of this call") {
		t.Errorf("output %q does not say the list is a point-in-time read", out)
	}
}

// The preview previews the real call: the candidate route applies the same
// window the repair would, so --older-than has to reach it.
func TestResurrectDeadDryListForwardsOlderThan(t *testing.T) {
	mock := newQueueMessageTestEnv(t)
	probe := registerDeadList(mock, []int{7})
	registerResurrectBatch(mock, nil)

	if _, _, err := runQueueMessage(t, "resurrect-dead", "--dry-list", "--older-than", "2 hours"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if probe.calls != 1 {
		t.Fatalf("made %d candidate-list calls, want 1", probe.calls)
	}
	if probe.idleFor[0] != "2 hours" {
		t.Errorf("candidate list sent idle_for %q, want \"2 hours\"", probe.idleFor[0])
	}
}

// --dry-list changes nothing, so --yes has no prompt to skip. Honouring
// --dry-list silently would make a run that was meant to repair the queue exit
// 0 having done nothing.
func TestResurrectDeadRefusesDryListWithYes(t *testing.T) {
	mock := newQueueMessageTestEnv(t)
	registerDeadList(mock, []int{7})
	bodies := registerResurrectBatch(mock, []int{7})

	_, _, err := runQueueMessage(t, "resurrect-dead", "--dry-list", "--yes")
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if len(*bodies) != 0 {
		t.Error("the refused run still called the resurrect-batch route")
	}
}

func TestResurrectDeadSendsTheBodyAndReportsTheServerCount(t *testing.T) {
	mock := newQueueMessageTestEnv(t)
	probe := registerDeadList(mock, []int{7, 8})
	bodies := registerResurrectBatch(mock, []int{7, 8})

	out, _, err := runQueueMessage(t, "resurrect-dead",
		"--older-than", "2 hours", "--limit", "50", "--backoff", "60", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(*bodies) != 1 {
		t.Fatalf("made %d resurrect-batch calls, want 1", len(*bodies))
	}
	body := (*bodies)[0]
	if body.OlderThan == nil || *body.OlderThan != "2 hours" {
		t.Errorf("older_than = %v, want \"2 hours\"", body.OlderThan)
	}
	if body.Limit == nil || *body.Limit != 50 {
		t.Errorf("limit = %v, want 50", body.Limit)
	}
	if body.BackoffSeconds == nil || *body.BackoffSeconds != 60 {
		t.Errorf("backoff_seconds = %v, want 60", body.BackoffSeconds)
	}
	// Under --yes the prompt is never shown, so sizing it would spend a
	// request on a number nobody reads.
	if probe.calls != 0 {
		t.Errorf("made %d candidate-list calls under --yes, want 0", probe.calls)
	}
	if !strings.Contains(out, "Resurrected 2 queue message(s)") {
		t.Errorf("output %q does not report the server's count", out)
	}
	if !strings.Contains(out, "7, 8") {
		t.Errorf("output %q does not list the ids the server returned", out)
	}
}

// The count rule: a prompt carries a number only when one describes the rows
// about to change. The candidate route applies whatever window the repair
// will, so the prompt is sized with and without --older-than.
func TestResurrectDeadSizesThePromptForTheSameWindow(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantIdleFor string
	}{
		{
			name:        "default window",
			args:        []string{"resurrect-dead"},
			wantIdleFor: noIdleFor,
		},
		{
			name:        "an explicit interval",
			args:        []string{"resurrect-dead", "--older-than", "3 days"},
			wantIdleFor: "3 days",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			answerYes(t)
			mock := newQueueMessageTestEnv(t)
			probe := registerDeadList(mock, []int{7, 8, 9})
			registerResurrectBatch(mock, []int{7, 8, 9})

			_, errOut, err := runQueueMessage(t, tt.args...)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if probe.calls != 1 {
				t.Fatalf("made %d candidate-list calls, want 1", probe.calls)
			}
			if probe.idleFor[0] != tt.wantIdleFor {
				t.Errorf("candidate list sent idle_for %q, want %q", probe.idleFor[0], tt.wantIdleFor)
			}
			if !strings.Contains(errOut, "Resurrect 3 dead queue message(s)?") {
				t.Errorf("prompt %q does not carry the candidate count", errOut)
			}
			if !strings.Contains(errOut, "as of this call") {
				t.Errorf("stderr %q does not say the count is a point-in-time read", errOut)
			}
		})
	}
}

// The candidate route and resurrect-batch validate the interval the same way,
// so a probe refused as bad usage means the run would be too: fail before the
// prompt instead of asking the user to confirm it.
func TestResurrectDeadRefusedProbeFailsBeforeThePrompt(t *testing.T) {
	answerYes(t)
	mock := newQueueMessageTestEnv(t)
	mock.RegisterHandler("GET", queuePath+"/dead", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondError(w, http.StatusBadRequest, "Invalid idle_for value 'banana'.")
	})
	bodies := registerResurrectBatch(mock, []int{7})

	_, errOut, err := runQueueMessage(t, "resurrect-dead", "--older-than", "banana")
	if err == nil {
		t.Fatal("expected the refused probe to fail the command")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if len(*bodies) != 0 {
		t.Error("the run still called the resurrect-batch route")
	}
	if strings.Contains(errOut, "Resurrect") {
		t.Errorf("stderr %q shows a prompt for a run that cannot succeed", errOut)
	}
}

// A failed probe is decoration failing, not the command failing: it warns and
// falls through to the unsized prompt, as rows/confirm.go's sizedStatusCount
// does.
func TestResurrectDeadSurvivesAFailedProbe(t *testing.T) {
	answerYes(t)
	mock := newQueueMessageTestEnv(t)
	mock.RegisterHandler("GET", queuePath+"/dead", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusInternalServerError, map[string]string{"detail": "boom"})
	})
	bodies := registerResurrectBatch(mock, []int{7})

	_, errOut, err := runQueueMessage(t, "resurrect-dead")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(*bodies) != 1 {
		t.Fatalf("made %d resurrect-batch calls, want 1", len(*bodies))
	}
	if !strings.Contains(errOut, "could not count the messages") {
		t.Errorf("stderr %q does not warn that the count failed", errOut)
	}
}

func TestResurrectDeadUsageErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"zero limit", []string{"resurrect-dead", "--limit", "0", "--yes"}},
		{"negative backoff", []string{"resurrect-dead", "--backoff", "-1", "--yes"}},
		// An empty interval would be dropped by the domain client and the run
		// would silently use the server's default window instead.
		{"empty older-than", []string{"resurrect-dead", "--older-than", "", "--yes"}},
		{"unexpected argument", []string{"resurrect-dead", "42"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := newQueueMessageTestEnv(t)
			probe := registerDeadList(mock, []int{7})
			bodies := registerResurrectBatch(mock, []int{7})

			_, _, err := runQueueMessage(t, tt.args...)
			if err == nil {
				t.Fatal("expected an error")
			}
			if code := qerrors.GetExitCode(err); code != 2 {
				t.Errorf("exit code = %d, want 2", code)
			}
			if probe.calls != 0 || len(*bodies) != 0 {
				t.Error("a client-side usage error still reached the server")
			}
		})
	}
}

// Declining is a choice, not bad usage: exit 1, and nothing changed.
func TestResurrectDeadDeclineChangesNothing(t *testing.T) {
	cmdutil.SetReader(strings.NewReader("n\n"))
	t.Cleanup(cmdutil.ResetReader)
	mock := newQueueMessageTestEnv(t)
	registerDeadList(mock, []int{7})
	bodies := registerResurrectBatch(mock, []int{7})

	_, _, err := runQueueMessage(t, "resurrect-dead")
	if err == nil {
		t.Fatal("expected the decline to be an error")
	}
	if code := qerrors.GetExitCode(err); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if len(*bodies) != 0 {
		t.Error("a declined run still called the resurrect-batch route")
	}
}
