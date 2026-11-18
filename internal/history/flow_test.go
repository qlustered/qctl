package history

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"gopkg.in/yaml.v3"
)

func flowOpts(out, errOut *bytes.Buffer) FlowOptions {
	return FlowOptions{Out: out, ErrOut: errOut}
}

// Echoing the previewed ids lets the backend refuse a changed selection.
func TestRunFlowActsWithExactlyTheIdsTheStepReturned(t *testing.T) {
	env := newUndoEnv(t)
	var out, errOut bytes.Buffer

	if err := env.client().RunFlow("tok", Undo, TableTarget(7, "table orders"), flowOpts(&out, &errOut)); err != nil {
		t.Fatalf("RunFlow: %v", err)
	}

	got := env.mock.Requests()
	want := []string{
		"POST /api/orgs/" + testOrgID + "/undo-step",
		"POST /api/orgs/" + testOrgID + "/undo",
	}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("requests = %v, want %v (the step call must PRECEDE the act call)", got, want)
	}

	ids, _ := env.bodies["undo"]["history_items_ids"].([]interface{})
	if len(ids) != 2 || ids[0] != float64(12) || ids[1] != float64(13) {
		t.Errorf("act sent history_items_ids = %v, want the step's [12 13]", ids)
	}
}

func TestRunFlowPrintsThePreviewThenTheOutcome(t *testing.T) {
	env := newUndoEnv(t)
	var out, errOut bytes.Buffer

	if err := env.client().RunFlow("tok", Undo, TableTarget(7, "table orders"), flowOpts(&out, &errOut)); err != nil {
		t.Fatalf("RunFlow: %v", err)
	}

	text := out.String()
	if strings.Contains(text, "**") {
		t.Errorf("the preview was not rendered:\n%s", text)
	}
	// Positions, not two Contains calls: "preview THEN outcome" is the claim,
	// and a version that printed the outcome first would satisfy both
	// substrings.
	preview := strings.Index(text, "max changed from 10 to 20.")
	outcome := strings.Index(text, "table orders — undo applied")
	if preview < 0 {
		t.Errorf("the preview is missing:\n%s", text)
	}
	if outcome < 0 {
		t.Errorf("the outcome line is missing:\n%s", text)
	}
	if preview >= 0 && outcome >= 0 && preview > outcome {
		t.Errorf("the outcome line printed before the preview:\n%s", text)
	}
	if strings.Contains(text, "should_refresh") {
		t.Errorf("should_refresh is a frontend cache hint and must not be in the human output:\n%s", text)
	}
}

func TestRunFlowRunsTheStepCallBeforeAskingAndStopsIfDeclined(t *testing.T) {
	env := newUndoEnv(t)
	var out, errOut bytes.Buffer
	opts := flowOpts(&out, &errOut)

	var promptedAfter int
	sentinel := errors.New("aborted")
	opts.Confirm = func(prompt string) error {
		promptedAfter = len(env.mock.Requests())
		if !strings.Contains(prompt, "Undo") {
			t.Errorf("prompt = %q, want it to name the direction", prompt)
		}
		// The user confirms what they were SHOWN, so the prompt must not be
		// the first thing they see. Asserting on the rendered sentence rather
		// than on out being non-empty: a version that printed anything else
		// first would pass the weaker check while still asking the user to
		// approve a change they have not read.
		if !strings.Contains(out.String(), "max changed from 10 to 20.") {
			t.Errorf("the prompt came before the preview; stdout was:\n%s", out.String())
		}
		return sentinel
	}

	err := env.client().RunFlow("tok", Undo, TableTarget(7, "table orders"), opts)
	// Identity, not errors.Is: a %w-wrapped error satisfies errors.Is, so it
	// would not prove the callback's error came back untouched. The command
	// layer's cancellation sentinel has to arrive exactly as it left.
	if err != sentinel {
		t.Fatalf("RunFlow returned %v, want the confirm error unchanged", err)
	}
	if promptedAfter != 1 {
		t.Errorf("the prompt saw %d requests, want 1 (the step call must run first)", promptedAfter)
	}
	if len(env.mock.Requests()) != 1 {
		t.Errorf("requests = %v; a declined confirmation must not act", env.mock.Requests())
	}
}

func TestRunFlowPrintsNonEmptyResultMessagesAfterTheOutcome(t *testing.T) {
	for _, dir := range []Direction{Undo, Redo} {
		for _, tc := range []struct {
			name string
			msg  *string
			want string
		}{
			{name: "absent"},
			{name: "empty", msg: strPtr("")},
			{name: "empty markup", msg: strPtr("<br><p></p>")},
			{name: "changed cells", msg: strPtr("**2 cells** changed since, so we left them as they are."),
				want: "2 cells changed since, so we left them as they are.\n"},
		} {
			t.Run(string(dir)+"/"+tc.name, func(t *testing.T) {
				env := newUndoEnv(t)
				env.resultMsg = tc.msg
				var out, errOut bytes.Buffer
				if err := env.client().RunFlow("tok", dir, TableTarget(7, "table orders"), flowOpts(&out, &errOut)); err != nil {
					t.Fatalf("RunFlow: %v", err)
				}
				want := "max changed from 10 to 20.\ntable orders — " + string(dir) + " applied\n" + tc.want
				if got := out.String(); got != want {
					t.Errorf("stdout = %q, want %q", got, want)
				}
				if errOut.Len() != 0 {
					t.Errorf("stderr = %q, want nothing", errOut.String())
				}
			})
		}
	}
}

func TestRunFlowStructuredOutputIsOneDocumentWithBothHalves(t *testing.T) {
	env := newUndoEnv(t)
	env.resultMsg = strPtr("**2 cells** changed since, so we left them as they are.")
	var out, errOut bytes.Buffer
	opts := flowOpts(&out, &errOut)
	opts.Format = "json"

	if err := env.client().RunFlow("tok", Undo, TableTarget(7, "table orders"), opts); err != nil {
		t.Fatalf("RunFlow: %v", err)
	}

	var doc struct {
		Step struct {
			HistoryItemsIDs []int  `json:"history_items_ids"`
			Msg             string `json:"msg"`
		} `json:"step"`
		Result struct {
			ShouldRefresh *bool  `json:"should_refresh"`
			Msg           string `json:"msg"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("stdout is not a single JSON document: %v\n%s", err, out.String())
	}
	if len(doc.Step.HistoryItemsIDs) != 2 {
		t.Errorf("step.history_items_ids = %v", doc.Step.HistoryItemsIDs)
	}
	if doc.Result.ShouldRefresh == nil || !*doc.Result.ShouldRefresh {
		t.Errorf("result.should_refresh is missing; it is kept in structured output")
	}
	if doc.Result.Msg != *env.resultMsg {
		t.Errorf("result.msg = %q, want %q", doc.Result.Msg, *env.resultMsg)
	}
	// The preview still has to be readable before the prompt, so it goes to
	// stderr rather than corrupting the document on stdout.
	if !strings.Contains(errOut.String(), "max changed from 10 to 20.") {
		t.Errorf("the preview did not reach stderr:\n%s", errOut.String())
	}
}

// The YAML branch has to round-trip through JSON, because the generated wire
// types carry json tags and no yaml tags. A regression to a direct
// yaml.Encode(doc) still produces valid YAML -- with the key spelled
// `historyitemsids`. So the key SPELLING is the assertion; that is the whole
// thing the round-trip buys.
func TestRunFlowYAMLKeysComeFromTheJSONTags(t *testing.T) {
	env := newUndoEnv(t)
	env.resultMsg = strPtr("**2 cells** changed since, so we left them as they are.")
	var out, errOut bytes.Buffer
	opts := flowOpts(&out, &errOut)
	opts.Format = "yaml"

	if err := env.client().RunFlow("tok", Undo, TableTarget(7, "table orders"), opts); err != nil {
		t.Fatalf("RunFlow: %v", err)
	}

	text := out.String()
	if !strings.Contains(text, "history_items_ids:") {
		t.Errorf("stdout has no history_items_ids key; the json tags did not survive:\n%s", text)
	}
	if strings.Contains(text, "historyitemsids") {
		t.Errorf("the Go field name leaked into the YAML keys:\n%s", text)
	}
	if !strings.Contains(text, "should_refresh:") {
		t.Errorf("stdout has no should_refresh key:\n%s", text)
	}
	// Still a document, and still one: parse it back.
	var doc map[string]interface{}
	if err := yaml.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("stdout is not a single YAML document: %v\n%s", err, text)
	}
	if _, ok := doc["step"]; !ok {
		t.Errorf("the document has no step half: %v", doc)
	}
	if _, ok := doc["result"]; !ok {
		t.Errorf("the document has no result half: %v", doc)
	}
	if result, ok := doc["result"].(map[string]interface{}); !ok || result["msg"] != *env.resultMsg {
		t.Errorf("result.msg was not preserved: %v", doc["result"])
	}
	// Same split as -o json: the preview must stay off stdout.
	if !strings.Contains(errOut.String(), "max changed from 10 to 20.") {
		t.Errorf("the preview did not reach stderr:\n%s", errOut.String())
	}
}

// The undo conflict names what the server thinks should be undone instead.
// That message is the useful part and must survive intact, with the <br>
// rendered, and the exit code must stay 2 rather than degrading to 1.
func TestRunFlowKeepsTheConflictMessageAndItsExitCode(t *testing.T) {
	env := newUndoEnv(t)
	env.code["undo"] = http.StatusBadRequest
	env.raw["undo"] = `{"detail":{"msg":"Our logs show the following needs to be undone instead:` +
		`<br>**max** changed from **10** to **30**.","title":"Error"}}`

	var out, errOut bytes.Buffer
	err := env.client().RunFlow("tok", Undo, TableTarget(7, "table orders"), flowOpts(&out, &errOut))
	if err == nil {
		t.Fatal("expected the conflict to fail the command")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
	msg := err.Error()
	if strings.Contains(msg, "<br>") {
		t.Errorf("literal <br> reached the user:\n%s", msg)
	}
	if !strings.Contains(msg, "Our logs show the following needs to be undone instead:") {
		t.Errorf("the server's sentence did not survive:\n%s", msg)
	}
	if !strings.Contains(msg, "max changed from 10 to 30.") {
		t.Errorf("the named change did not survive on its own line:\n%s", msg)
	}
}

// A half-applied undo leaves is_deleted NULL, which is the `partial` state.
// A plain error would suggest nothing happened when some rows did move.
func TestRunFlowPointsAtPartialAfterAHalfAppliedUndo(t *testing.T) {
	env := newUndoEnv(t)
	env.code["undo"] = http.StatusBadRequest
	env.raw["undo"] = `{"detail":{"msg":"Could not revert 2 rows.",` +
		`"title":"Undo was not fully done. Please try again."}}`

	var out, errOut bytes.Buffer
	err := env.client().RunFlow("tok", Undo, TableTarget(7, "table orders"), flowOpts(&out, &errOut))
	if err == nil {
		t.Fatal("expected the partial undo to fail the command")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
	if !strings.Contains(err.Error(), "qctl get history --undo-status partial") {
		t.Errorf("the partial pointer is missing:\n%s", err.Error())
	}
}

// The SAME title is raised whether some rows moved or none did -- the tri-state
// writer, atlas history_controller.py
// _update_history_depending_on_full_success, only reaches its UPDATE when
// success_row_count is non-zero, and both paths fall through to the one raise
// in HistoryController.undo. So the annotation must never assert that items
// moved: on the zero-row body that sentence is false and the listing it points
// at comes back empty.
//
// Both bodies below are exact get_msg outputs (common/common/schema.py):
// the first is the two-line form -- get_msg joins its own lines with "\n",
// never with <br>; the <br> join happens in the raise, ACROSS the msgs
// set, one entry per history item -- and it carries the "- N rows were
// modified." line that only a non-zero success_row_count produces. Note that
// line can never stand alone: a single-line get_msg carrying only the success
// line implies is_full_success, which returns "" from get_msg's
// `if self.is_full_success:` guard. The second body is the single-message form
// the zero-row path rewrites to "All relevant rows ...". qctl must answer them
// identically.
func TestRunFlowNeverAssertsItemsMovedOnAFailedUndo(t *testing.T) {
	bodies := map[string]string{
		"some rows moved": `- 2 rows were modified.\n- Some relevant rows were being ` +
			`modified by another process, so we did not touch them.`,
		"no rows moved": "All relevant rows were being modified by another process, " +
			"so we did not touch them.",
	}
	for name, msg := range bodies {
		t.Run(name, func(t *testing.T) {
			env := newUndoEnv(t)
			env.code["undo"] = http.StatusBadRequest
			env.raw["undo"] = `{"detail":{"msg":"` + msg + `",` +
				`"title":"Undo was not fully done. Please try again."}}`

			var out, errOut bytes.Buffer
			err := env.client().RunFlow("tok", Undo, TableTarget(7, "table orders"), flowOpts(&out, &errOut))
			if err == nil {
				t.Fatal("expected the failed undo to fail the command")
			}
			got := err.Error()

			// The pointer is still offered on both paths -- it is the only way
			// to find an item that DID get stuck.
			if !strings.Contains(got, "qctl get history --undo-status partial") {
				t.Errorf("the partial pointer is missing:\n%s", got)
			}
			// ... but it must be qualified, not asserted. Both halves are
			// checked so that dropping either one fails: the conditional
			// offer, and the sentence that tells the user what an empty
			// listing means.
			if !strings.Contains(got, "Anything that did move is left in the partial state") {
				t.Errorf("the pointer is not qualified as conditional:\n%s", got)
			}
			if !strings.Contains(got, "an empty result means nothing moved") {
				t.Errorf("the empty-listing case is not explained:\n%s", got)
			}
			// The regression this test exists for: an unconditional claim of
			// movement, which is false whenever no row moved.
			for _, banned := range []string{"some items moved", "half-applied: some"} {
				if strings.Contains(strings.ToLower(got), banned) {
					t.Errorf("the annotation asserts items moved (%q), which is false on the zero-row path:\n%s", banned, got)
				}
			}
		})
	}
}

// The pointer must NOT be appended to an ordinary conflict, which is also a
// 400 and where nothing is stuck.
func TestRunFlowDoesNotPointAtPartialForAConflict(t *testing.T) {
	env := newUndoEnv(t)
	env.code["undo"] = http.StatusBadRequest
	env.raw["undo"] = `{"detail":{"msg":"Our logs show the following needs to be undone instead:<br>x.","title":"Error"}}`

	var out, errOut bytes.Buffer
	err := env.client().RunFlow("tok", Undo, TableTarget(7, "table orders"), flowOpts(&out, &errOut))
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "--undo-status partial") {
		t.Errorf("the partial pointer was appended to a conflict:\n%s", err.Error())
	}
}

// The preview PRINT is skipped when it renders empty (flow.go RunFlow). This pins
// the skip half of that guard, which nothing reached before: mutating
// `if preview != ""` to `if true` left both ./internal/history and
// ./internal/commands/undoredo green.
//
// "<br><p></p>" is markup that renders to nothing at all, which is the case the
// guard is for: the test is on PlainMessage's OUTPUT rather than on step.Msg,
// because a message that renders to nothing is as absent as one the backend
// never sent. Without the skip the outcome line is preceded by a blank line.
//
// Full-output equality, not a Contains: a leading "\n" is exactly the
// regression, and every Contains assertion in this file passes through it.
func TestRunFlowSkipsAPreviewThatRendersEmpty(t *testing.T) {
	env := newUndoEnv(t)
	env.code["undo-step"] = http.StatusOK
	env.raw["undo-step"] = `{"history_items_ids":[12,13],"msg":"<br><p></p>"}`

	var out, errOut bytes.Buffer
	if err := env.client().RunFlow("tok", Undo, TableTarget(7, "table orders"), flowOpts(&out, &errOut)); err != nil {
		t.Fatalf("RunFlow: %v", err)
	}

	const want = "table orders — undo applied\n"
	if got := out.String(); got != want {
		t.Errorf("stdout = %q, want %q (an empty preview must not print a blank line)", got, want)
	}
	if errOut.Len() != 0 {
		t.Errorf("stderr = %q, want nothing", errOut.String())
	}
}
