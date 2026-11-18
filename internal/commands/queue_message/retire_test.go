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

func registerRetire(mock *testutil.MockAPIServer, ids []int) *[]api.QueueRetireOldRequestSchema {
	bodies := new([]api.QueueRetireOldRequestSchema)
	mock.RegisterHandler("POST", queuePath+"/retire-old", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body api.QueueRetireOldRequestSchema
		if err := json.Unmarshal(raw, &body); err != nil {
			panic(err)
		}
		*bodies = append(*bodies, body)
		testutil.RespondJSON(w, http.StatusOK, api.QueueRetireOldResponseSchema{
			RetiredIds: ids, Count: len(ids),
		})
	})
	return bodies
}

func TestRetireSendsTheBodyAndReportsTheServerCount(t *testing.T) {
	mock := newQueueMessageTestEnv(t)
	bodies := registerRetire(mock, []int{11, 12})

	out, _, err := runQueueMessage(t, "retire", "--older-than", "90 days", "--limit", "100", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(*bodies) != 1 {
		t.Fatalf("made %d retire calls, want 1", len(*bodies))
	}
	body := (*bodies)[0]
	if body.OlderThan == nil || *body.OlderThan != "90 days" {
		t.Errorf("older_than = %v, want \"90 days\"", body.OlderThan)
	}
	if body.Limit == nil || *body.Limit != 100 {
		t.Errorf("limit = %v, want 100", body.Limit)
	}
	if !strings.Contains(out, "Retired 2 queue message(s)") {
		t.Errorf("output %q does not report the server's count", out)
	}
	// The route runs UPDATE ... SET acknowledged = true and deletes nothing.
	// Reporting a deletion here would describe an effect the call does not have.
	if !strings.Contains(out, "acknowledged, not deleted") {
		t.Errorf("output %q does not say the messages were acknowledged rather than deleted", out)
	}
}

// --older-than is refused rather than defaulted. The server would silently
// apply 30 days, which is a window nobody chose, on the one verb here that
// cannot be undone.
func TestRetireRequiresOlderThan(t *testing.T) {
	mock := newQueueMessageTestEnv(t)
	bodies := registerRetire(mock, []int{11})

	_, _, err := runQueueMessage(t, "retire", "--yes")
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(err.Error(), "--older-than is required") {
		t.Errorf("error %q does not name the missing flag", err)
	}
	if len(*bodies) != 0 {
		t.Error("the refused run still called the retire route")
	}
}

// Under --yes there is no prompt, so the consequence lines are the only warning
// a script gets. They print either way, as rows/confirmAction does.
func TestRetirePrintsItsConsequencesUnderYes(t *testing.T) {
	mock := newQueueMessageTestEnv(t)
	registerRetire(mock, []int{11})

	_, errOut, err := runQueueMessage(t, "retire", "--older-than", "90 days", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{
		"It does not delete them",
		"Nothing in the API un-acknowledges a message",
		"not performed by this call",
		"never deletes a row whose dequeued_at is empty",
	} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr %q is missing the consequence %q", errOut, want)
		}
	}
	// The retention window has three different defaults in cueue's config
	// (7 days / 5 minutes / 5 hours), so naming one would be wrong on two of
	// the three deployments.
	for _, forbidden := range []string{"7 days", "5 min", "5 hours"} {
		if strings.Contains(errOut, forbidden) {
			t.Errorf("stderr %q quotes a retention window that is not the same on every deployment", errOut)
		}
	}
}

// The count rule with no honest number available at all: no read route applies
// the enqueued_at age condition, so the prompt explains that and names the
// closest recipe rather than borrowing a number from a different population.
func TestRetirePromptIsAlwaysUnsizedAndSaysWhy(t *testing.T) {
	answerYes(t)
	mock := newQueueMessageTestEnv(t)
	registerRetire(mock, []int{11})

	_, prompt, err := runQueueMessage(t, "retire", "--older-than", "90 days", "--limit", "100")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(prompt, "No count is shown") {
		t.Errorf("stderr %q does not explain the missing count", prompt)
	}
	if !strings.Contains(prompt, "--order-by enqueued_at --reverse") {
		t.Errorf("stderr %q does not name the recipe that gets closest", prompt)
	}
	if !strings.Contains(prompt, "Retire up to 100 queue message(s) enqueued more than 90 days ago?") {
		t.Errorf("prompt %q does not name the limit and the window it will send", prompt)
	}
}

func TestRetireUsageErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"zero limit", []string{"retire", "--older-than", "90 days", "--limit", "0", "--yes"}},
		{"empty older-than", []string{"retire", "--older-than", "", "--yes"}},
		{"unexpected argument", []string{"retire", "--older-than", "90 days", "42", "--yes"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := newQueueMessageTestEnv(t)
			bodies := registerRetire(mock, []int{11})

			_, _, err := runQueueMessage(t, tt.args...)
			if err == nil {
				t.Fatal("expected an error")
			}
			if code := qerrors.GetExitCode(err); code != 2 {
				t.Errorf("exit code = %d, want 2", code)
			}
			if len(*bodies) != 0 {
				t.Error("a client-side usage error still reached the server")
			}
		})
	}
}

func TestRetireDeclineChangesNothing(t *testing.T) {
	cmdutil.SetReader(strings.NewReader("n\n"))
	t.Cleanup(cmdutil.ResetReader)
	mock := newQueueMessageTestEnv(t)
	bodies := registerRetire(mock, []int{11})

	_, _, err := runQueueMessage(t, "retire", "--older-than", "90 days")
	if err == nil {
		t.Fatal("expected the decline to be an error")
	}
	if code := qerrors.GetExitCode(err); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if len(*bodies) != 0 {
		t.Error("a declined run still called the retire route")
	}
}
