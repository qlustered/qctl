package history

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/output"
)

// FlowOptions is everything RunFlow needs from the command layer.
//
// Confirm is a callback rather than a bool because internal/history must not
// import internal/cmdutil: no domain package does, and the dependency would run
// the wrong way. The command layer closes over cmdutil.Confirm; a nil Confirm
// means the caller already authorized the action (--yes).
type FlowOptions struct {
	Out     io.Writer
	ErrOut  io.Writer
	Confirm func(prompt string) error
	// Format is the resolved -o value. "json" and "yaml" switch stdout to a
	// single machine-readable document; anything else is the human path.
	Format string
}

// FlowResult is the whole exchange, emitted as ONE document under -o json and
// -o yaml.
//
// One document, not two: the spec asked for "the step response and, after the
// action, the UndoRedoResponseSchema", and two JSON values on one stream is not
// something jq reads without stream flags.
//
// should_refresh is kept here and omitted from the human line on purpose. It is
// a frontend cache hint, and its one CLI-legible meaning -- under DataView it
// is set only when a data overwrite was reverted rather than a config change --
// is not worth a line of terminal output that would need explaining.
type FlowResult struct {
	Step   *StepResult `json:"step"`
	Result *ActResult  `json:"result"`
}

// RunFlow previews a step, confirms it, and acts with the previewed history ids.
func (c *Client) RunFlow(accessToken string, dir Direction, target Target, opts FlowOptions) error {
	step, err := c.Step(accessToken, dir, target)
	if err != nil {
		return err
	}

	structured := opts.Format == "json" || opts.Format == "yaml"

	// The step call above is unconditional. There is no --dry-run: the step
	// call is not an opt-in preview, it is the only way the command can work
	// safely, so it always runs. Only the PRINT is conditional -- a preview
	// that renders empty is skipped rather than emitted as a blank line, and
	// the test is on PlainMessage's output rather than on step.Msg, because a
	// message that renders to nothing is as absent as one the backend never
	// sent. Under -o json and -o yaml the preview goes to stderr so stdout
	// stays one parseable document.
	preview := PlainMessage(step.Msg)
	if preview != "" {
		if structured {
			fmt.Fprintln(opts.ErrOut, preview)
		} else {
			fmt.Fprintln(opts.Out, preview)
		}
	}

	if opts.Confirm != nil {
		if err := opts.Confirm(promptFor(dir)); err != nil {
			return err
		}
	}

	result, err := c.Act(accessToken, dir, target, step.HistoryItemsIds)
	if err != nil {
		return annotatePartial(dir, err)
	}

	if structured {
		return output.WriteRaw(opts.Out, opts.Format, FlowResult{Step: step, Result: result})
	}
	fmt.Fprintf(opts.Out, "%s — %s applied\n", target.Label, dir)
	if result.Msg != nil {
		if msg := PlainMessage(*result.Msg); msg != "" {
			fmt.Fprintln(opts.Out, msg)
		}
	}
	return nil
}

func promptFor(dir Direction) string {
	if dir == Redo {
		return "Redo this change?"
	}
	return "Undo this change?"
}

// partialTitleMarker is how the backend flags an undo or redo that did not
// fully apply -- which covers a half-applied one AND one where nothing moved
// at all; see annotatePartial for why the two are indistinguishable here.
//
// The sentence rides on the error's TITLE, not its message: the raise is
// APIError(400, "<br>".join(msgs), title="Undo was not fully done. Please try
// again.") (atlas history_controller.py HistoryController.undo), and redo's
// twin (HistoryController.redo) says "Redo". The message body is the per-item
// detail. apierror's Format() already prints the title as a leading line, so
// all this adds is the pointer.
//
// Matching on the title text is the only signal available -- the raise passes
// no error_code -- so it is deliberately narrowed to the fragment both
// directions share.
//
// NOTHING AUTOMATED GUARDS THIS STRING, and an earlier version of this comment
// claimed two guards that do not exist. `make gotchas-sweep` cannot fire on it:
// the title is a Python literal that never reaches the spec, and grepping
// `not fully done` across internal/api/client.gen.go, internal/api/
// metadata.gen.go and tools/gotchassweep/ (baseline.yaml included) returns
// nothing, so no claim can even depend on it. No test can fire either: every
// test here supplies the title in its own fixture, so a backend rewording
// leaves the whole suite green while annotatePartial silently stops matching.
// It has to be re-read against ../atlas by hand.
const partialTitleMarker = "not fully done"

// annotatePartial appends the pointer that turns a bare error into an
// actionable one: an undo that half-applies leaves is_deleted NULL, which is
// the `partial` state `qctl get history --undo-status partial` lists, and
// without the pointer the error reads as "nothing happened" when some rows did
// move.
//
// The wording is CONDITIONAL on purpose, and this is the part to leave alone.
// The same title -- and so the same annotation -- is raised on two outcomes the
// backend does not tell apart on the wire. The tri-state writer is
// _update_history_depending_on_full_success (atlas
// history_controller.py), whose branching is its `if result.is_full_success:`
// chain: it sets is_deleted = NULL only in its `elif result.success_row_count:`
// branch; when NOTHING moved it adds the same message and RETURNS before the
// UPDATE (the `else:` arm), leaving the item exactly as it was. Both paths then
// hit the one `if msgs: raise` in HistoryController.undo with the
// identical title. So "some items moved" would be a false sentence on the
// zero-row path, pointing at a listing that comes back empty. The only signal
// that would separate them is the prose of OverwrittenFieldResult.get_msg
// (common/common/schema.py) -- matching on it would be a second,
// finer-grained string dependency on backend wording for no gain, since the
// user has that message in front of them either way.
//
// TestRunFlowNeverAssertsItemsMovedOnAFailedUndo pins this.
//
// %w, not %v: the exit code has to keep riding the Unwrap chain.
func annotatePartial(dir Direction, err error) error {
	var opErr *apierror.OperationalError
	if !errors.As(err, &opErr) || !strings.Contains(opErr.Title, partialTitleMarker) {
		return err
	}
	return fmt.Errorf("%w\n\nThis %s did not complete; the server's own report is above.\n"+
		"Anything that did move is left in the partial state; list it with the command\n"+
		"below, where an empty result means nothing moved and the item is unchanged:\n"+
		"  qctl get history --undo-status partial", err, dir)
}
