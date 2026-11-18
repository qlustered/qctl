//nolint:revive // Tests intentionally match the command package directory.
package comment_thread

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

// --row x --field is a cross-product: every named field on every named row.
func TestCreateBuildsCrossProductTargets(t *testing.T) {
	mock := newThreadTestEnv(t)
	var gotBody map[string]interface{}
	registerCreate(mock, &gotBody, sampleCreateResult(nil))
	registerTableResolve(mock, "orders", 42)

	if _, err := runThreadCommand(t, "", "comment-thread", "create", "--table", "orders",
		"--row", "1042", "--row", "1043", "--field", "amount", "--field", "currency",
		"--body", "why negative?"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	targets := gotBody["targets"].([]interface{})
	if len(targets) != 2 {
		t.Fatalf("sent %d targets, want 2 (one per row)", len(targets))
	}
	for _, raw := range targets {
		fields := raw.(map[string]interface{})["field_names"].([]interface{})
		if len(fields) != 2 {
			t.Errorf("target %v has %d fields, want 2", raw, len(fields))
		}
	}
}

// No --field means whole-row targets. field_names must be null on the wire, not
// an empty list: the backend reads absent/empty as "the whole row", and an
// empty list would be indistinguishable from a bug.
func TestCreateWithNoFieldsTargetsWholeRows(t *testing.T) {
	mock := newThreadTestEnv(t)
	var gotBody map[string]interface{}
	registerCreate(mock, &gotBody, sampleCreateResult(nil))
	registerTableResolve(mock, "orders", 42)

	if _, err := runThreadCommand(t, "", "comment-thread", "create", "--table", "orders",
		"--row", "1042", "--body", "dupes?"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	target := gotBody["targets"].([]interface{})[0].(map[string]interface{})
	if target["field_names"] != nil {
		t.Errorf("field_names = %v, want null", target["field_names"])
	}
}

// A 201 carrying dropped_targets is a PARTIAL success: the thread exists, and
// the dropped cells must be reported. Printing a bare "created" would silently
// swallow them. Exit stays 0, matching apply -f's 202 staging path.
func TestCreateReportsDroppedTargetsOnStderrAtExitZero(t *testing.T) {
	mock := newThreadTestEnv(t)
	var gotBody map[string]interface{}
	registerCreate(mock, &gotBody, sampleCreateResult([]api.DroppedTargetSchema{
		{RowID: 1043, FieldName: nil, Reason: "row_not_found"},
		{RowID: 1042, FieldName: ptr("bogus"), Reason: "unknown_field"},
	}))
	registerTableResolve(mock, "orders", 42)

	stdout, stderr, err := runThreadCommandSplit(t, "comment-thread", "create", "--table", "orders",
		"--row", "1042", "--row", "1043", "--field", "bogus", "--body", "?")
	if err != nil {
		t.Fatalf("dropped targets must not fail the command: %v", err)
	}
	if !strings.Contains(stdout, "comment-thread/"+testThreadUUID.String()+" created") {
		t.Errorf("stdout missing the created line:\n%s", stdout)
	}
	for _, want := range []string{"row 1043", "row_not_found", "bogus", "unknown_field"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q:\n%s", want, stderr)
		}
	}
}

// The row cap is the backend's (500 targets); refusing client-side keeps a
// mis-typed loop from becoming a 422 the user has to decode.
func TestCreateRefusesTooManyRows(t *testing.T) {
	mock := newThreadTestEnv(t)
	resolves := registerTableResolve(mock, "orders", 42)
	args := []string{"comment-thread", "create", "--table", "orders", "--body", "x"}
	for i := 0; i < 501; i++ {
		args = append(args, "--row", strconv.Itoa(i+1))
	}
	_, err := runThreadCommand(t, "", args...)
	if err == nil {
		t.Fatal("expected an error above the 500-row cap")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
	// The cap is checked before Bootstrap and the table lookup, so an over-cap
	// invocation costs nothing. Registering the resolver is what makes this an
	// assertion rather than a reliance on an unregistered handler.
	if *resolves != 0 {
		t.Errorf("an over-cap request cost %d table lookups, want 0", *resolves)
	}
}

// 500 legal rows x 5 fields is 2500 cells, over the aggregate MAX_THREAD_TARGET_CELLS.
func TestCreateRefusesTooManyCells(t *testing.T) {
	mock := newThreadTestEnv(t)
	resolves := registerTableResolve(mock, "orders", 42)
	args := []string{"comment-thread", "create", "--table", "orders", "--body", "x"}
	for i := 0; i < 500; i++ {
		args = append(args, "--row", strconv.Itoa(i+1))
	}
	for _, field := range []string{"a", "b", "c", "d", "e"} {
		args = append(args, "--field", field)
	}
	_, err := runThreadCommand(t, "", args...)
	if err == nil {
		t.Fatal("expected an error above the 2000-cell cap")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
	if *resolves != 0 {
		t.Errorf("an over-cap request cost %d table lookups, want 0", *resolves)
	}
}

// --body-file - reads the first comment from stdin, so an agent can pipe a
// heredoc instead of quoting a markdown block on the command line.
func TestCreateReadsTheBodyFromStdin(t *testing.T) {
	mock := newThreadTestEnv(t)
	var gotBody map[string]interface{}
	registerCreate(mock, &gotBody, sampleCreateResult(nil))
	registerTableResolve(mock, "orders", 42)

	out, err := runThreadCommand(t, "piped body\n", "comment-thread", "create", "--table", "orders",
		"--row", "1042", "--body-file", "-")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotBody["body"] != "piped body\n" {
		t.Errorf("body = %q, want the piped text", gotBody["body"])
	}
	if !strings.Contains(out, "comment-thread/"+testThreadUUID.String()+" created (1 target(s))") {
		t.Errorf("output missing the created line:\n%s", out)
	}
}

// Every locally-decidable refusal is bad usage (exit 2) and must cost no API
// call: the table resolver is registered precisely so "it never ran" is an
// assertion rather than an accident of an unregistered handler.
//
// The --body/--body-file pair's own branches belong to cmdutil.ReadBodyFlags
// and are tested there; the "neither body flag" case stays here because it is
// the wiring proof that create routes through it before spending a request.
func TestCreateRefusesBadFlagCombinationsBeforeAnyRequest(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"neither body flag", []string{"--row", "1042"}},
		{"whitespace-only body", []string{"--row", "1042", "--body", "   \n\t "}},
		{"body over the cap", []string{"--row", "1042", "--body", strings.Repeat("x", 10001)}},
		{"title over the cap", []string{"--row", "1042", "--body", "hi", "--title", strings.Repeat("t", 501)}},
		{"no rows", []string{"--body", "hi"}},
		{"zero row id", []string{"--row", "0", "--body", "hi"}},
		{"negative row id", []string{"--row", "1042", "--row", "-1", "--body", "hi"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := newThreadTestEnv(t)
			resolves := registerTableResolve(mock, "orders", 42)

			_, err := runThreadCommand(t, "",
				append([]string{"comment-thread", "create", "--table", "orders"}, tc.args...)...)
			if err == nil {
				t.Fatal("expected a usage error")
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d (err: %v)", got, qerrors.ExitBadUsage, err)
			}
			if *resolves != 0 {
				t.Errorf("a usage error cost %d table lookups, want 0", *resolves)
			}
		})
	}
}

// A body exactly at the cap is legal — the bound is inclusive, so an
// off-by-one in the check would refuse a request the backend accepts.
func TestCreateAcceptsABodyExactlyAtTheCap(t *testing.T) {
	mock := newThreadTestEnv(t)
	var gotBody map[string]interface{}
	registerCreate(mock, &gotBody, sampleCreateResult(nil))
	registerTableResolve(mock, "orders", 42)

	if _, err := runThreadCommand(t, "", "comment-thread", "create", "--table", "orders",
		"--row", "1042", "--body", strings.Repeat("x", 10000)); err != nil {
		t.Fatalf("a 10000-character body must be accepted: %v", err)
	}
}

// The count in the created line is the RESPONSE's target list, which counts
// row-field pairs — not the request's, which counts rows carrying field lists.
func TestCreateReportsTheServersTargetCount(t *testing.T) {
	mock := newThreadTestEnv(t)
	var gotBody map[string]interface{}
	result := sampleCreateResult(nil)
	result.Thread.Targets = &[]api.CommentThreadTargetResponseSchema{
		{RowID: 1042, FieldName: ptr("amount")},
		{RowID: 1042, FieldName: ptr("currency")},
		{RowID: 1043, FieldName: ptr("amount")},
	}
	registerCreate(mock, &gotBody, result)
	registerTableResolve(mock, "orders", 42)

	stdout, _, err := runThreadCommandSplit(t, "comment-thread", "create", "--table", "orders",
		"--row", "1042", "--row", "1043", "--field", "amount", "--field", "currency", "--body", "?")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Two request targets, three response targets: printing the request's
	// count would say "(2 target(s))" about a thread holding three.
	if !strings.Contains(stdout, "created (3 target(s))") {
		t.Errorf("stdout does not report the server's target count:\n%s", stdout)
	}
}

// targets is omitempty. A response that withholds it must not be reported as
// "(0 target(s))" — that is a false statement about a thread that was just
// created with targets.
func TestCreateOmitsTheCountWhenTheServerWithholdsTargets(t *testing.T) {
	mock := newThreadTestEnv(t)
	var gotBody map[string]interface{}
	result := sampleCreateResult(nil)
	result.Thread.Targets = nil
	registerCreate(mock, &gotBody, result)
	registerTableResolve(mock, "orders", 42)

	stdout, _, err := runThreadCommandSplit(t, "comment-thread", "create", "--table", "orders",
		"--row", "1042", "--body", "?")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(stdout, "target(s)") {
		t.Errorf("an unreported target list must not be counted:\n%s", stdout)
	}
	if !strings.Contains(stdout, "comment-thread/"+testThreadUUID.String()+" created") {
		t.Errorf("stdout missing the created line:\n%s", stdout)
	}
}

// A dropped target's reason and field name are both the server's, and a field
// name echoes a column of customer data. They land on stderr under a "warning:"
// heading the user reads as qctl's own, so neither may carry a live control.
func TestCreateSanitizesDroppedTargetText(t *testing.T) {
	mock := newThreadTestEnv(t)
	var gotBody map[string]interface{}
	registerCreate(mock, &gotBody, sampleCreateResult([]api.DroppedTargetSchema{
		{RowID: 1043, FieldName: nil, Reason: "row_gone\x1b[31mred"},
		{RowID: 1042, FieldName: ptr("amount\x1b]0;pwned\x07"), Reason: "unknown_field\x00"},
	}))
	registerTableResolve(mock, "orders", 42)

	_, stderr, err := runThreadCommandSplit(t, "comment-thread", "create", "--table", "orders",
		"--row", "1042", "--row", "1043", "--field", "amount", "--body", "?")
	if err != nil {
		t.Fatalf("dropped targets must not fail the command: %v", err)
	}
	if strings.ContainsRune(stderr, '\x1b') || strings.ContainsRune(stderr, '\x00') || strings.ContainsRune(stderr, '\x07') {
		t.Fatalf("dropped-target text reached the terminal with a live control byte: %q", stderr)
	}
	// The reason is printed with %s, so it carries Sanitize's form verbatim.
	if !strings.Contains(stderr, `row_gone\x1b[31mred`) {
		t.Errorf("missing the visible escape for the reason: %q", stderr)
	}
	// The field name goes through %q, which escapes Sanitize's own backslash.
	if !strings.Contains(stderr, `amount\\x1b]0;pwned\\x07`) {
		t.Errorf("missing the visible escape for the field name: %q", stderr)
	}
}

// Under -o json stdout is the server's response and nothing else: the result
// line and the dropped-target warning both go to stderr, and the thread id an
// agent replies to next is in the document.
func TestCreateJSONPrintsTheResponseAndMovesProseToStderr(t *testing.T) {
	mock := newThreadTestEnv(t)
	var gotBody map[string]interface{}
	registerCreate(mock, &gotBody, sampleCreateResult([]api.DroppedTargetSchema{
		{RowID: 1043, FieldName: nil, Reason: "row_not_found"},
	}))
	registerTableResolve(mock, "orders", 42)

	stdout, stderr, err := runThreadCommandSplit(t, "comment-thread", "create", "--table", "orders",
		"--row", "1042", "--row", "1043", "--field", "amount", "--body", "?", "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
		DroppedTargets []struct {
			RowID int `json:"row_id"`
		} `json:"dropped_targets"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
	}
	if got.Thread.ID != testThreadUUID.String() {
		t.Errorf("thread.id = %q, want %s", got.Thread.ID, testThreadUUID)
	}
	if len(got.DroppedTargets) != 1 || got.DroppedTargets[0].RowID != 1043 {
		t.Errorf("dropped_targets = %+v, want row 1043", got.DroppedTargets)
	}
	for _, want := range []string{"comment-thread/" + testThreadUUID.String() + " created", "row_not_found"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q:\n%s", want, stderr)
		}
	}
}

func TestCreateNamePrintsTheThread(t *testing.T) {
	mock := newThreadTestEnv(t)
	var gotBody map[string]interface{}
	registerCreate(mock, &gotBody, sampleCreateResult(nil))
	registerTableResolve(mock, "orders", 42)

	stdout, _, err := runThreadCommandSplit(t, "comment-thread", "create", "--table", "orders",
		"--row", "1042", "--body", "?", "-o", "name")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "comment-thread/" + testThreadUUID.String() + "\n"; stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
}

// delete-comment has no body to return, so it declares table alone and -o
// json exits 2 rather than printing prose.
func TestDeleteCommentRefusesJSON(t *testing.T) {
	newThreadTestEnv(t)
	_, err := runThreadCommand(t, "", "comment-thread", "delete-comment", testThreadUUID.String(),
		testCommentUUID.String(), "--table-id", "42", "--yes", "-o", "json")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("delete-comment -o json: exit %d (%v), want %d", code, err, qerrors.ExitBadUsage)
	}
}

// reply and edit-comment write the comment the server returned; its id is
// what --reply-to and edit-comment take next. No comment kind exists, so
// neither declares -o name.
func TestReplyAndEditCommentJSONPrintTheComment(t *testing.T) {
	for _, tc := range []struct {
		verb, method, path string
		status             int
		args               []string
	}{
		{"reply", "POST", threadCommentsPath, http.StatusCreated, []string{testThreadUUID.String()}},
		{"edit-comment", "PATCH", threadCommentsPath + "/" + testCommentUUID.String(), http.StatusOK,
			[]string{testThreadUUID.String(), testCommentUUID.String()}},
	} {
		t.Run(tc.verb, func(t *testing.T) {
			mock := newThreadTestEnv(t)
			mock.RegisterHandler(tc.method, tc.path, func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, tc.status, sampleThreadComment())
			})
			args := append(append([]string{"comment-thread", tc.verb}, tc.args...), "--table-id", "42", "--body", "x", "-o", "json")
			stdout, stderr, err := runThreadCommandSplit(t, args...)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			var got struct {
				ID       string `json:"id"`
				ThreadID string `json:"thread_id"`
			}
			if err := json.Unmarshal([]byte(stdout), &got); err != nil {
				t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
			}
			if got.ID != testCommentUUID.String() || got.ThreadID != testThreadUUID.String() {
				t.Errorf("document = %+v, want comment %s on thread %s", got, testCommentUUID, testThreadUUID)
			}
			if !strings.Contains(stderr, "comment-thread/"+testThreadUUID.String()) {
				t.Errorf("stderr missing the result line:\n%s", stderr)
			}
			_, err = runThreadCommand(t, "", append(args[:len(args)-1], "name")...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("-o name: exit %d (%v), want %d", code, err, qerrors.ExitBadUsage)
			}
		})
	}
}

// resolve and unresolve write the thread; -o name names it.
func TestResolutionVerbsWriteTheThread(t *testing.T) {
	for _, verb := range []string{"resolve", "unresolve"} {
		t.Run(verb, func(t *testing.T) {
			mock := newThreadTestEnv(t)
			mock.RegisterHandler("POST", resolutionPath(verb), func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, api.CommentThreadResponseSchema{ID: testThreadUUID})
			})
			stdout, stderr, err := runThreadCommandSplit(t, "comment-thread", verb, testThreadUUID.String(),
				"--table-id", "42", "-o", "yaml")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !strings.Contains(stdout, "id: "+testThreadUUID.String()) {
				t.Errorf("stdout is not the thread:\n%s", stdout)
			}
			if !strings.Contains(stderr, "comment-thread/"+testThreadUUID.String()) {
				t.Errorf("stderr missing the result line:\n%s", stderr)
			}
			stdout, _, err = runThreadCommandSplit(t, "comment-thread", verb, testThreadUUID.String(),
				"--table-id", "42", "-o", "name")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if want := "comment-thread/" + testThreadUUID.String() + "\n"; stdout != want {
				t.Errorf("-o name stdout = %q, want %q", stdout, want)
			}
		})
	}
}
