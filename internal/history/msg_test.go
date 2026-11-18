package history

import (
	"strings"
	"testing"
)

// The backend marks values with **bold** and links them to frontend routes,
// and on its ERROR paths it joins per-item messages with a literal <br>:
// `"<br>".join(msgs)` in atlas history_controller.py HistoryController.undo and
// HistoryController.redo, plus the concurrency refusal's
// `...needs to be undone instead:<br>{...}` in undo. All three are frontend
// syntax that must not reach a terminal.
//
// This is NOT the shape of a STEP message -- that one is newline-bulleted; see
// TestPlainMessageRendersTheStepMessagesBulletedShape below and PlainMessage's
// own doc comment. PlainMessage also renders the action's result message.
// The renderer is shared: apierror.ToPlainText is a thin
// export of the same unexported toPlainText that apierror runs every error
// body through (both arms of OperationalError.Format in parser.go), which is
// how the conflict message's <br> gets rendered in
// TestRunFlowKeepsTheConflictMessageAndItsExitCode (flow_test.go). A regression
// here is a regression there.
func TestPlainMessageRendersTheBackendsMarkdown(t *testing.T) {
	raw := `**max** changed from **10** to **20** in [orders rule freshness](/orgs/abc/datasets/7).` +
		`<br>**"category"** changed to **"vehicles"** in [orders data](/orgs/abc/datasets/7).`

	got := PlainMessage(raw)

	if strings.Contains(got, "<br>") {
		t.Errorf("literal <br> survived:\n%s", got)
	}
	if strings.Contains(got, "**") {
		t.Errorf("markdown bold survived:\n%s", got)
	}
	if strings.Contains(got, "/orgs/abc/datasets/7") {
		t.Errorf("frontend route survived; there is no browser here:\n%s", got)
	}
	lines := strings.Split(got, "\n")
	if len(lines) != 2 {
		t.Fatalf("want one line per item, got %d:\n%s", len(lines), got)
	}
	if !strings.HasPrefix(lines[0], "max changed from 10 to 20 in orders rule freshness") {
		t.Errorf("first line = %q", lines[0])
	}
}

// The shape a step message ACTUALLY arrives in, which nothing here covered
// before: newline-joined and bulleted. The last line of
// get_history_items_human_readable_msg (atlas
// cettings/cettings/controllers/history_human_readable_mixin.py) is
//
//	return "\n".join([f"- {item}" for item in result])
//
// and the single-item case returns result[0] bare, with no bullet.
// Both halves are pinned, because the bullets are the backend's own text and
// must survive to the terminal exactly as sent -- ToPlainText must not mistake
// a leading "- " for markdown of its own and eat it.
func TestPlainMessageRendersTheStepMessagesBulletedShape(t *testing.T) {
	multi := "- **max** changed from **10** to **20** in [orders rule freshness](/orgs/abc/datasets/7).\n" +
		`- **"category"** changed to **"vehicles"** in [orders data](/orgs/abc/datasets/7).`
	wantMulti := "- max changed from 10 to 20 in orders rule freshness.\n" +
		`- "category" changed to "vehicles" in orders data.`
	if got := PlainMessage(multi); got != wantMulti {
		t.Errorf("multi-item:\n got %q\nwant %q", got, wantMulti)
	}

	single := "**max** changed from **10** to **20** in [orders rule freshness](/orgs/abc/datasets/7)."
	wantSingle := "max changed from 10 to 20 in orders rule freshness."
	if got := PlainMessage(single); got != wantSingle {
		t.Errorf("single item (no bullet):\n got %q\nwant %q", got, wantSingle)
	}
}

func TestPlainMessageLeavesPlainTextAlone(t *testing.T) {
	if got := PlainMessage("orders initialized."); got != "orders initialized." {
		t.Errorf("PlainMessage = %q, want it unchanged", got)
	}
}

func TestPlainMessageOfEmptyIsEmpty(t *testing.T) {
	if got := PlainMessage(""); got != "" {
		t.Errorf("PlainMessage(\"\") = %q, want \"\"", got)
	}
}

// PlainMessage emits no ANSI and consults no terminal, so there is nothing for
// a TTY variant of these tests to vary. That is a consequence of choosing
// apierror.ToPlainText over glamour, not an untested case: glamour branches on
// TTY (WithAutoStyle vs the "notty" style) and would have needed both paths
// covered. Asserted here so the absence reads as a decision.
func TestPlainMessageIsTerminalIndependent(t *testing.T) {
	const raw = "**bold**<br>[link](/orgs/1)"
	if strings.ContainsRune(PlainMessage(raw), '\x1b') {
		t.Errorf("PlainMessage emitted an ANSI escape: %q", PlainMessage(raw))
	}
}

// A value too long for the backend to show whole arrives complete inside a
// ~*value*~ marker, and a step message is a detail view, so it prints whole.
// All four shapes the mixin writes are here, derived rather than invented:
// `orders_daily_snapshot_v2` is 24 characters, the first length
// get_value_formatted_for_msg marks (len > max_length + 3 with max_length=20,
// shorten_str_and_say_modified in common/common/misc.py), inside Style.bold's
// production quotes ("**“", "”**"), and so is the 31-character path;
// `policy_effective_date_utc` is 25, past get_column_name_formatted_for_msg's
// 16 + 3, bold and unquoted; and the instance name is 60 characters, past the
// mixin's max_length=40 (_get_human_readable_name_of_instance,
// history_human_readable_mixin.py), in its link text -- and bare, as it arrives
// for a model with no frontend route (a DatasetRule).
//
// The underscores are the point. Before atlas-d702 a shortened value arrived
// markdown-escaped, `orders\_daily\_snap...2`, and qctl printed the
// backslashes. A payload escapes only a backslash and a star, so the value comes
// back byte for byte -- and nothing between the markers is taken for Markdown.
func TestPlainMessagePrintsAMarkedValueWhole(t *testing.T) {
	name := "orders rule freshness for the daily snapshot table in region"
	raw := "**~*policy_effective_date_utc*~** changed from **“~*orders_daily_snapshot_v2*~”** to **“~*C:\\\\exports\\\\\\*_daily_snapshot.csv*~”** in [~*" + name + "*~](/orgs/abc/datasets/7)."
	want := "policy_effective_date_utc changed from “orders_daily_snapshot_v2” to “C:\\exports\\*_daily_snapshot.csv” in " + name + "."
	if got := PlainMessage(raw); got != want {
		t.Errorf("PlainMessage = %q, want %q", got, want)
	}
	bare := "**~*policy_effective_date_utc*~** changed in ~*" + name + "*~."
	if got, want := PlainMessage(bare), "policy_effective_date_utc changed in "+name+"."; got != want {
		t.Errorf("PlainMessage = %q, want %q", got, want)
	}
}

// A viewer who sees the column masked gets each PII value as a value marker
// holding its mask, and one who does not gets the value in a ~{…}~ wrapper.
// Both messages are atlas's own, from capi/tests/test_history_msg_pii_masking.py.
func TestPlainMessagePrintsAMaskedValueVerbatim(t *testing.T) {
	for _, c := range []struct{ name, raw, want string }{
		{"masked", `**"~*•••••tson*~"** changed to **"~*•••••hers*~"** in the **asking_price** field of row 7.`,
			`"•••••tson" changed to "•••••hers" in the asking_price field of row 7.`},
		{"short mask", `**"~*•••••*~"** changed to **"~*•••••*~"** in the **asking_price** field of row 7.`,
			`"•••••" changed to "•••••" in the asking_price field of row 7.`},
		{"unmasked", `**"~{Jane Doe-Watson}~"** changed to **"~{Jonathan Smithers}~"** in the **asking_price** field of row 7.`,
			`"Jane Doe-Watson" changed to "Jonathan Smithers" in the asking_price field of row 7.`},
	} {
		if got := PlainMessage(c.raw); got != c.want {
			t.Errorf("%s: PlainMessage = %q, want %q", c.name, got, c.want)
		}
	}
}

// RunFlow prints this preview straight to its writer, so the control-character
// pass has to have happened by the time PlainMessage returns -- and it has:
// markdown.PlainText, which apierror.toPlainText delegates to, sanitizes both
// the prose and each restored ~*value*~ marker.
//
// This is pinned HERE rather than added to RunFlow because sanitizing there
// too would be a second pass: the escapes below would come out as `\\x1b`. If
// markdown.PlainText ever stops sanitizing, this test is what says so before
// the undo/redo preview becomes a terminal-injection vector again.
func TestPlainMessageLeavesNoLiveControlCharacterForTheFlowPreview(t *testing.T) {
	got := PlainMessage("Undo **~*orders\x1b[31m*~** on \x1b]0;pwned\x07row 5\x00")

	if strings.ContainsAny(got, "\x1b\x07\x00") {
		t.Fatalf("PlainMessage left a live control byte for RunFlow to print: %q", got)
	}
	for _, want := range []string{`orders\x1b[31m`, `\x1b]0;pwned\x07`, `row 5\x00`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing the visible escape %q: %q", want, got)
		}
	}
	// One pass, not two. A `\\x1b` here means someone added a second
	// terminal.Sanitize on this path.
	if strings.Contains(got, `\\x1b`) {
		t.Errorf("the message was sanitized twice: %q", got)
	}
}
