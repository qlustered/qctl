package apierror

import (
	"strings"
	"testing"
)

const rebuildHint = "Run `qctl run composite-run-job <table>` to rebuild the table."

// compositeBody is the 404 get_data raises for an empty composite table
// (atlas capi/capi/views/data_view_core.py), carrying the actions
// actions_for_error_code builds (capi/capi/controllers/composite_status_controller.py).
func compositeBody(code, title, msg, suggestions, actions string) []byte {
	return []byte(`{"detail":{"msg":` + msg + `,"error_code":"` + code + `","title":"` + title +
		`","severity":"error","error_id":"E-1","suggestions":` + suggestions + `,"actions":` + actions + `}}`)
}

// The hint is appended, never substituted: BLOCKED's message carries the
// build's own failure text (composite_last_failure_message), and every line
// the server sent must survive next to qctl's.
func TestFormatAppendsTheRebuildCommandToTheServersCopy(t *testing.T) {
	body := compositeBody("COMPOSITE_BUILD_BLOCKED", "Build blocked",
		`"Building **orders_enriched** is blocked after repeated failures: column amount is missing. Fix the issue, then retry the build."`,
		`["Review the composite run details for the failure.","Retry the build once the underlying issue is resolved."]`,
		`[{"code":"RETRY_BUILD","text":"Retry the build."},{"code":"OPEN_RUN_DETAILS","text":"Review the run details."}]`)
	opErr, err := ParseErrorResponse(body, 404)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "Build blocked\n" +
		"Building orders_enriched is blocked after repeated failures: column amount is missing. Fix the issue, then retry the build.\n" +
		"Suggestions:\n" +
		"  - Review the composite run details for the failure.\n" +
		"  - Retry the build once the underlying issue is resolved.\n" +
		"(Code: COMPOSITE_BUILD_BLOCKED, ID: E-1)\n" +
		rebuildHint
	if got := opErr.Format(); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// The hint is keyed on the action, not the error code: the same code without
// RETRY_BUILD in its actions gets no hint.
func TestFormatHintsOnlyWhenTheServerOffersTheAction(t *testing.T) {
	body := compositeBody("COMPOSITE_BUILD_BLOCKED", "Build blocked", `"Blocked."`, `null`,
		`[{"code":"OPEN_RUN_DETAILS","text":"Review the run details."}]`)
	opErr, err := ParseErrorResponse(body, 404)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := opErr.Format(); strings.Contains(got, "composite-run-job") {
		t.Errorf("no RETRY_BUILD action, yet the render names the rebuild:\n%s", got)
	}
}

// The hint is qctl's copy, so a Replace outcome keeps it: Replace hides only
// what the server wrote.
func TestFormatKeepsTheHintUnderReplace(t *testing.T) {
	t.Cleanup(func() { SetVerbosity(0) })
	body := compositeBody("COMPOSITE_NO_ROWS", "No rows produced",
		`"**orders_enriched** ran successfully, but none of the rows matched. Review the composite setup."`,
		`["Confirm the primary table contains rows."]`,
		`[{"code":"RETRY_BUILD","text":"Rebuild this dataset."},{"code":"EDIT_SPEC","text":"Edit the composite spec."}]`)
	opErr, err := ParseErrorResponse(body, 404)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if Classify("COMPOSITE_NO_ROWS").Outcome != Replace {
		t.Fatal("fixture assumes COMPOSITE_NO_ROWS is Replace")
	}

	for _, v := range []int{0, 1} {
		SetVerbosity(v)
		got := opErr.Format()
		if !strings.HasSuffix(got, "(Code: COMPOSITE_NO_ROWS, ID: E-1)\n"+rebuildHint) {
			t.Errorf("-v=%d: want the hint right after the code line:\n%s", v, got)
		}
	}
}

func TestFormatPrintsEachHintOnce(t *testing.T) {
	opErr := &OperationalError{
		Message: "Build failed.",
		Actions: []Action{{Code: "RETRY_BUILD"}, {Code: "EDIT_SPEC"}, {Code: "RETRY_BUILD"}},
	}
	if got := strings.Count(opErr.Format(), rebuildHint); got != 1 {
		t.Errorf("hint printed %d times, want 1:\n%s", got, opErr.Format())
	}
}

// actions is one peripheral field, like suggestions. ERR_FIELD_IN_USE_BY_COMPOSITE
// sends {label,url} entries with no code (atlas
// cettings/cettings/controllers/composite_controller/composite_field_references.py);
// that and any other shape must cost only the hints, never the message.
func TestParseErrorResponseToleratesAnUnexpectedActionsShape(t *testing.T) {
	for _, actions := range []string{`null`, `[{"label":"Edit setup: x","url":"/orgs/1"}]`, `"RETRY_BUILD"`, `[1,2]`, `{"code":"RETRY_BUILD"}`} {
		body := []byte(`{"detail":{"msg":"Refused.","error_code":"SOME_CODE","actions":` + actions + `}}`)
		opErr, err := ParseErrorResponse(body, 409)
		if err != nil {
			t.Fatalf("actions=%s: unexpected error: %v", actions, err)
		}
		if want := "Refused.\n(Code: SOME_CODE)"; opErr.Format() != want {
			t.Errorf("actions=%s: Format() = %q, want %q", actions, opErr.Format(), want)
		}
	}
}

func TestActionHintsReturnsACopy(t *testing.T) {
	ActionHints()["RETRY_BUILD"] = "changed"
	if hintByAction["RETRY_BUILD"] != rebuildHint {
		t.Errorf("mutating ActionHints() changed the table: %q", hintByAction["RETRY_BUILD"])
	}
}
