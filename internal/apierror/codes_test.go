package apierror

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata/error-code-outcomes.txt")

const goldenPath = "testdata/error-code-outcomes.txt"

// TestEveryErrorCodeHasARecordedOutcome is the ticket's first acceptance
// criterion. Passthrough is the default, so a new backend code needs no code
// change to render sensibly — but it must not slip in unnoticed either. The
// golden file is the record: regenerating it puts the new code in a reviewable
// diff next to the outcome it silently received. A third column, "hint", marks
// the codes whose Classification carries a Hint, so adding or dropping one is
// a reviewed diff too.
//
// Regenerate with:
//
//	go test ./internal/apierror -run TestEveryErrorCodeHasARecordedOutcome -update
func TestEveryErrorCodeHasARecordedOutcome(t *testing.T) {
	var b strings.Builder
	for _, code := range api.ErrorCodes {
		class := Classify(code)
		fmt.Fprintf(&b, "%s\t%s", code, class.Outcome)
		if class.Hint != nil {
			b.WriteString("\thint")
		}
		b.WriteString("\n")
	}
	got := b.String()

	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(goldenPath, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		return
	}

	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden (regenerate with -update): %v", err)
	}
	if got != string(want) {
		t.Errorf("error-code outcomes changed.\n--- want (%s)\n%s\n--- got\n%s\n"+
			"If the backend added or renamed a code, confirm the new outcome is "+
			"right, then regenerate with:\n"+
			"  go test ./internal/apierror -run TestEveryErrorCodeHasARecordedOutcome -update",
			goldenPath, want, got)
	}
}

// TestClassifiedCodesExistInTheCatalog catches the other direction: a
// hand-written entry for a code the backend no longer sends is dead weight
// that the golden file cannot see, because the golden is built by walking the
// catalog.
func TestClassifiedCodesExistInTheCatalog(t *testing.T) {
	catalog := make(map[string]bool, len(api.ErrorCodes))
	for _, c := range api.ErrorCodes {
		catalog[c] = true
	}
	for code := range classifications {
		if !catalog[code] {
			t.Errorf("classifications has %q, which is not in api.ErrorCodes — "+
				"the backend renamed or dropped it", code)
		}
	}
}

// TestEveryReplaceCarriesText guards the one way a Replace entry can be worse
// than Passthrough: suppressing the server's message and printing nothing.
//
// An EmptyState entry that sets Text follows the exact same Format() branch
// as Replace (see parser.go), so it is exposed to the identical defect: Text
// present but blank after trimming would suppress the server's message and
// render nothing in its place. An EmptyState entry that leaves Text empty is
// fine -- Format() falls back to Passthrough for it -- so only a
// non-empty-but-blank Text is an error.
func TestEveryReplaceCarriesText(t *testing.T) {
	for code, c := range classifications {
		if c.Outcome == Replace && strings.TrimSpace(c.Text) == "" {
			t.Errorf("%s is Replace with no replacement text", code)
		}
		if c.Outcome == EmptyState && c.Text != "" && strings.TrimSpace(c.Text) == "" {
			t.Errorf("%s is EmptyState with whitespace-only text", code)
		}
	}
}

func TestClassifyUnknownCodeIsPassthrough(t *testing.T) {
	for _, code := range []string{"", "SOME_CODE_INVENTED_LATER"} {
		if got := Classify(code); got.Outcome != Passthrough {
			t.Errorf("Classify(%q).Outcome = %v, want Passthrough", code, got.Outcome)
		}
	}
}

// DRY_RUN_RATE_LIMIT is Passthrough on purpose, not by accident of the
// default. Replace is reserved for copy written for the React frontend (see
// Outcome's doc comment), and this message is written for a person: "You have
// 3 dry run(s) in progress (limit: 3). Please wait for an existing dry run to
// complete before starting a new one." A Replace could not improve on that
// anyway -- Classification.Text is a fixed string and Format() does not
// interpolate, so it would drop the very counts that make the sentence useful.
//
// This test exists because the golden file cannot carry that reasoning: it is
// regenerated wholesale by -update, so a future regen could flip this code to
// Replace and produce a green, reviewed-looking diff.
func TestDryRunRateLimitIsDeliberatelyPassthrough(t *testing.T) {
	class := Classify("DRY_RUN_RATE_LIMIT")
	if class.Outcome != Passthrough {
		t.Errorf("Classify(DRY_RUN_RATE_LIMIT).Outcome = %s, want passthrough — the backend's own "+
			"sentence carries current_count and max_allowed, and Text cannot "+
			"interpolate them back in", class.Outcome)
	}
	if class.Text != "" {
		t.Errorf("Classify(DRY_RUN_RATE_LIMIT).Text = %q, want empty", class.Text)
	}
}

// Atlas's COMPOSITE_BUILD_STALLED message already tells a terminal user what
// happened and what to do: retry, then contact support if it recurs. Unlike the
// composite codes classified above, it names no frontend tab, menu, or route,
// so replacing it would throw away useful backend context. The command that
// retries comes from its RETRY_BUILD action (actions.go), appended rather than
// substituted.
func TestCompositeBuildStalledIsDeliberatelyPassthrough(t *testing.T) {
	class := Classify("COMPOSITE_BUILD_STALLED")
	if class.Outcome != Passthrough {
		t.Errorf("outcome = %s, want passthrough", class.Outcome)
	}
	if class.Text != "" {
		t.Errorf("replacement text = %q, want none", class.Text)
	}
}

// seriesConflictBody is a 409 detail as atlas's middleware sends it: title
// "Error", the message as msg, and context only when contextJSON is non-empty.
func seriesConflictBody(code, msg, contextJSON string) []byte {
	ctx := ""
	if contextJSON != "" {
		ctx = `,"context":` + contextJSON
	}
	return []byte(fmt.Sprintf(`{"detail":{"msg":%q,"title":"Error","severity":"error","error_code":%q,"error_id":"req-1"%s}}`,
		msg, code, ctx))
}

func formatBody(t *testing.T, body []byte) string {
	t.Helper()
	opErr, err := ParseErrorResponse(body, 409)
	if err != nil {
		t.Fatalf("ParseErrorResponse: %v", err)
	}
	return opErr.Format()
}

func TestPeriodLockedHintNamesUnlockCommand(t *testing.T) {
	msg := "Period '2026-07' is locked. Unlock it before changing its data."
	got := formatBody(t, seriesConflictBody(CodePeriodLocked, msg, ""))
	want := msg + "\n" +
		"(Code: PERIOD_LOCKED, ID: req-1)\n" +
		"Hint: An account with deliverable:admin can run `qctl table-series unlock <series> --period <key> --yes`. " +
		"`qctl get table-series` shows series and period keys; " +
		"`qctl get table-series <series-uuid> --periods` shows lock state (is_locked)."
	if got != want {
		t.Errorf("Format() =\n%s\nwant\n%s", got, want)
	}
}

func TestSubmissionDeclarationHintsNameTheCommandThatHelps(t *testing.T) {
	for _, tt := range []struct{ code, msg, hint string }{
		{"SUBMISSION_PERIOD_NOT_EXPECTED", "The intake is not expected to submit for this period.",
			"`qctl get submission-statuses --cloud-source-id <id>` lists the periods the cloud source is expected for."},
		{"SUBMISSION_ALREADY_DECLARED", "A waiver is already in effect for this period.",
			"A waiver is already in effect for the period; " +
				"`qctl submission-status unwaive --cloud-source-id <id> --period <key>` withdraws it."},
	} {
		t.Run(tt.code, func(t *testing.T) {
			got := formatBody(t, seriesConflictBody(tt.code, tt.msg, ""))
			want := tt.msg + "\n(Code: " + tt.code + ", ID: req-1)\nHint: " + tt.hint
			if got != want {
				t.Errorf("Format() =\n%s\nwant\n%s", got, want)
			}
		})
	}
}

func TestSeriesDeadlineScheduleInvalidHintNamesWhereTheDaysAreShown(t *testing.T) {
	msg := "The broker due date (60 days after the period ends) falls after the MGA deadline (30 days after the period ends)."
	body := seriesConflictBody("SERIES_DEADLINE_SCHEDULE_INVALID", msg, "")
	got := formatBody(t, body)
	want := msg + "\n(Code: SERIES_DEADLINE_SCHEDULE_INVALID, ID: req-1)\nHint: " + seriesDeadlineScheduleInvalidHint
	if got != want {
		t.Errorf("Format() =\n%s\nwant\n%s", got, want)
	}
	err := HandleHTTPErrorFromBytes(422, body, "patch table series deadline schedule failed")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.HasSuffix(err.Error(), "\nHint: "+seriesDeadlineScheduleInvalidHint) {
		t.Errorf("error lacks the hint line:\n%s", err.Error())
	}
}

func TestSeriesReferencedBySourceHintNamesEachSourceFromContext(t *testing.T) {
	msg := "Series 'Monthly' is the series of data source(s) 'Feed A' (id 12), 'Feed B' (id 15). " +
		"A series an undeleted data source references can not be deleted."
	got := formatBody(t, seriesConflictBody(CodeSeriesReferencedBySource, msg,
		`{"data_sources":[{"id":12,"name":"Feed A"},{"id":15,"name":"Feed B"}]}`))
	want := msg + "\n" +
		"(Code: SERIES_REFERENCED_BY_SOURCE, ID: req-1)\n" +
		"Hint: Delete the cloud sources that use this series, then delete the series again:\n" +
		"  qctl delete cloud-source 12 # \"Feed A\"\n" +
		"  qctl delete cloud-source 15 # \"Feed B\""
	if got != want {
		t.Errorf("Format() =\n%s\nwant\n%s", got, want)
	}
}

func TestSeriesReferencedBySourceHintFallsBackWithoutUsableContext(t *testing.T) {
	msg := "Series 'Monthly' is the series of data source(s) 'Feed A' (id 12)."
	want := msg + "\n" +
		"(Code: SERIES_REFERENCED_BY_SOURCE, ID: req-1)\n" +
		"Hint: Delete each cloud source the message names with `qctl delete cloud-source <id>`, " +
		"then delete the series again."
	for name, ctx := range map[string]string{
		"absent":          "",
		"null":            "null",
		"empty object":    `{}`,
		"empty list":      `{"data_sources":[]}`,
		"no ids":          `{"data_sources":[{"name":"Feed A"}]}`,
		"unexpected type": `{"data_sources":"Feed A"}`,
		"not an object":   `"data_sources"`,
	} {
		t.Run(name, func(t *testing.T) {
			if got := formatBody(t, seriesConflictBody(CodeSeriesReferencedBySource, msg, ctx)); got != want {
				t.Errorf("Format() =\n%s\nwant\n%s", got, want)
			}
		})
	}
}

func TestSeriesSourceFilesHintHandlesMissingAndUnsafeContext(t *testing.T) {
	for name, ctx := range map[string]string{
		"absent": "", "null": "null", "empty": `{}`, "wrong shape": `{"data_sources":"feed"}`,
		"invalid ids": `{"data_sources":[{"id":0,"name":"zero"},{"id":-1,"name":"negative"},{"name":"missing"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			got := formatBody(t, seriesConflictBody("SERIES_SOURCE_FILES_IN_TABLE", "Refused", ctx))
			if !strings.Contains(got, "`qctl describe cloud-source <id>`") || strings.Contains(got, "qctl describe cloud-source 0") {
				t.Errorf("fallback = %s", got)
			}
		})
	}
	got := formatBody(t, seriesConflictBody("SERIES_SOURCE_FILES_IN_TABLE", "Refused",
		`{"data_sources":[{"id":12,"name":"Feed\n\u001b[31m"}]}`))
	if !strings.Contains(got, `qctl describe cloud-source 12 # "Feed\n\x1b[31m"`) || strings.ContainsAny(got, "\x1b\r") {
		t.Errorf("unsafe source name: %q", got)
	}
}

func TestSeriesReferencedByDeliverableHintNamesTheDraftOnlyWayOut(t *testing.T) {
	msg := "Series 'Monthly' is referenced by bindings of Deliverable revision(s): 'Board pack' (release 'r1'). " +
		"A series a deliverable binding references can not be deleted."
	got := formatBody(t, seriesConflictBody("SERIES_REFERENCED_BY_DELIVERABLE", msg, ""))
	want := msg + "\n" +
		"(Code: SERIES_REFERENCED_BY_DELIVERABLE, ID: req-1)\n" +
		"Hint: Only a draft revision can change: remove the series from its sheet and cell bindings, " +
		"or delete the draft with `qctl delete deliverable-revision <deliverable> --release <release>`. " +
		"An enabled or disabled revision's bindings cannot change, so a series it binds cannot be deleted."
	if got != want {
		t.Errorf("Format() =\n%s\nwant\n%s", got, want)
	}
}

func TestCompositeRowDeleteRefusedHintNamesTheUpstreamTable(t *testing.T) {
	msg := "Rows of a composite table cannot be deleted: they are built from its upstream tables. " +
		"Delete the row in the upstream table it comes from; a clean row there is moved to quarantine first."
	body := seriesConflictBody("COMPOSITE_ROW_DELETE_REFUSED", msg, "")
	err := HandleHTTPErrorFromBytes(422, body, "delete rows failed")
	if got := qerrors.GetExitCode(err); got != 2 {
		t.Errorf("exit code = %d, want 2", got)
	}
	want := msg + "\n(Code: COMPOSITE_ROW_DELETE_REFUSED, ID: req-1)\n" +
		"Hint: For an organization-owned linked table, inspect its sources with `qctl get linked-table <table> -o yaml`. " +
		"In the upstream table that supplies the row, move it to quarantine first if it is clean with " +
		"`qctl rows send-to-quarantine <upstream-table> --row-id <upstream-row-id>`, then delete it with " +
		"`qctl rows delete-quarantined <upstream-table> --signature <upstream-signature> --reason <reason>`."
	if got := formatBody(t, body); got != want {
		t.Errorf("Format() =\n%s\nwant\n%s", got, want)
	}
}

func TestRowDeletedHintNamesEachDelete(t *testing.T) {
	msg := "Row ids [12, 13, 14] were deleted."
	body := seriesConflictBody(CodeRowDeleted, msg,
		`{"deleted_row_ids":[{"id":12,"row_delete_id":17},{"id":13,"row_delete_id":null},{"id":14,"row_delete_id":17}]}`)
	err := HandleHTTPErrorFromBytes(409, body, "set cell failed")
	if got := qerrors.GetExitCode(err); got != 2 {
		t.Errorf("exit code = %d, want 2", got)
	}
	want := msg + "\n(Code: ROW_DELETED, ID: req-1)\n" +
		"Hint: The rows are deleted. See who deleted them and why:\n" +
		"  qctl get deleted-rows <table> --delete-id 17  # rows 12, 14\n" +
		"  qctl get data <table> --deleted-rows all  # rows 13, no delete id"
	if got := formatBody(t, body); got != want {
		t.Errorf("Format() =\n%s\nwant\n%s", got, want)
	}
}

func TestRowDeletedHintWithoutContextFallsBack(t *testing.T) {
	msg := "Row ids [12] were deleted."
	want := msg + "\n(Code: ROW_DELETED, ID: req-1)\n" +
		"Hint: The rows are deleted. `qctl get deleted-rows <table>` lists who deleted them and why; " +
		"`qctl get data <table> --deleted-rows all` shows them with their Row State."
	for _, ctx := range []string{"", `{"deleted_row_ids":[]}`} {
		if got := formatBody(t, seriesConflictBody(CodeRowDeleted, msg, ctx)); got != want {
			t.Errorf("context %q: Format() =\n%s\nwant\n%s", ctx, got, want)
		}
	}
}

func TestCleanRowsImmutableHintNamesTheQuarantineRoute(t *testing.T) {
	msg := "Rows in clean data cannot be deleted. Move them to quarantine first " +
		"(POST datasets/42/move-rows-to-quarantine), then delete them there."
	body := seriesConflictBody("CLEAN_ROWS_IMMUTABLE", msg, "")
	err := HandleHTTPErrorFromBytes(422, body, "delete rows failed")
	if got := qerrors.GetExitCode(err); got != 2 {
		t.Errorf("exit code = %d, want 2", got)
	}
	want := msg + "\n" +
		"(Code: CLEAN_ROWS_IMMUTABLE, ID: req-1)\n" +
		"Hint: Send the rows to quarantine with `qctl rows send-to-quarantine <table> --row-id <id>`, " +
		"then delete them with `qctl rows delete-quarantined <table> --signature <signature> --reason <reason>`."
	if got := formatBody(t, body); got != want {
		t.Errorf("Format() =\n%s\nwant\n%s", got, want)
	}
}

// A code with no hint renders exactly as before context was kept, even when
// it carries the same data_sources context SERIES_REFERENCED_BY_SOURCE reads.
func TestCodeWithoutAHintRendersUnchanged(t *testing.T) {
	msg := "The table is the template of data source 'Feed A' (id 12)."
	got := formatBody(t, seriesConflictBody("SERIES_SOURCE_TEMPLATE_TABLE", msg,
		`{"data_sources":[{"id":12,"name":"Feed A"}]}`))
	want := msg + "\n(Code: SERIES_SOURCE_TEMPLATE_TABLE, ID: req-1)"
	if got != want {
		t.Errorf("Format() =\n%s\nwant\n%s", got, want)
	}
}

func TestParseKeepsContextAndDropsANullOne(t *testing.T) {
	for name, tc := range map[string]struct {
		ctx  string
		want string
	}{
		"absent": {"", ""},
		"null":   {"null", ""},
		"object": {`{"data_sources":[{"id":12,"name":"Feed A"}]}`, `{"data_sources":[{"id":12,"name":"Feed A"}]}`},
	} {
		t.Run(name, func(t *testing.T) {
			opErr, err := ParseErrorResponse(seriesConflictBody("SERIES_SOURCE_TEMPLATE_TABLE", "m", tc.ctx), 409)
			if err != nil {
				t.Fatalf("ParseErrorResponse: %v", err)
			}
			if got := string(opErr.Context); got != tc.want {
				t.Errorf("Context = %q, want %q", got, tc.want)
			}
			if tc.want == "" && opErr.Context != nil {
				t.Errorf("Context = %#v, want nil", opErr.Context)
			}
		})
	}
}

// Every hint must say something with no context at all, since a hint is
// rendered whatever the server sent alongside the code.
func TestEveryHintSaysSomethingWithoutContext(t *testing.T) {
	for code, c := range classifications {
		if c.Hint == nil {
			continue
		}
		if got := c.Hint(&OperationalError{ErrorCode: code}); strings.TrimSpace(got) == "" {
			t.Errorf("%s: Hint with no context = %q, want advice", code, got)
		}
	}
}

// The hint rides the same *OperationalError the HTTP layer wraps, so a 409
// carrying one still exits with the contract's code for a 409.
func TestAHintedConflictStillExitsTwo(t *testing.T) {
	body := seriesConflictBody(CodePeriodLocked, "Period '2026-07' is locked. Unlock it before changing its data.", "")
	err := HandleHTTPErrorFromBytes(409, body, "move rows to quarantine failed")
	if got := qerrors.GetExitCode(err); got != 2 {
		t.Errorf("exit code = %d, want 2", got)
	}
	if !strings.Contains(err.Error(), "\nHint: An account with deliverable:admin can run `qctl table-series unlock ") {
		t.Errorf("error lacks the hint line:\n%s", err.Error())
	}
}
