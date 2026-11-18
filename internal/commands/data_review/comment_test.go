package data_review //nolint:revive // Package name mirrors the hyphenated command.

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const (
	threadT  = "0190dddd-1111-4222-8333-444444444444"
	commentC = "0190cccc-1111-4222-8333-444444444444"
)

var commentRoute = reviewRoute(reviewA) + "/comments"

func aComment(body string) map[string]any {
	return map[string]any{
		"id": commentC, "thread_id": threadT, "author_id": userAda, "body": body,
		"created_at": "2026-09-16T10:00:00Z", "edited_at": nil, "is_edited": false,
		"linked_dry_run_job_id": nil, "mentioned_user_ids": []string{}, "parent_id": nil,
	}
}

// respondComment serves reviewA's comment route and records every raw body it
// receives.
func (e *testEnv) respondComment(status int, body any) *[]map[string]json.RawMessage {
	bodies := &[]map[string]json.RawMessage{}
	e.mock.RegisterHandler("POST", commentRoute, func(w http.ResponseWriter, r *http.Request) {
		var got map[string]json.RawMessage
		_ = json.NewDecoder(r.Body).Decode(&got)
		*bodies = append(*bodies, got)
		testutil.RespondJSON(w, status, body)
	})
	return bodies
}

// execStdin is exec with the given text on the command's stdin; only the
// error is kept.
func (e *testEnv) execStdin(t *testing.T, stdin string, args ...string) error {
	t.Helper()
	root := testRoot()
	root.SetIn(strings.NewReader(stdin))
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs(append([]string{"data-review"}, args...))
	return root.Execute()
}

func sentBody(t *testing.T, bodies *[]map[string]json.RawMessage) string {
	t.Helper()
	if len(*bodies) != 1 {
		t.Fatalf("POST bodies = %d, want 1", len(*bodies))
	}
	var body string
	if err := json.Unmarshal((*bodies)[0]["body"], &body); err != nil {
		t.Fatalf("body key: %v", err)
	}
	return body
}

func TestComment_PostsAndPrintsTheThreadRecipes(t *testing.T) {
	e := newTestEnv(t)
	e.respondReview(http.StatusOK, aReview(nil))
	bodies := e.respondComment(http.StatusCreated, aComment("looks off"))

	stdout, stderr, err := e.exec(t, "comment", reviewA, "--body", "looks off")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"GET " + reviewRoute(reviewA), "POST " + commentRoute}
	if got := e.mock.Requests(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("requests = %v, want %v", got, want)
	}
	if got := sentBody(t, bodies); got != "looks off" {
		t.Errorf("body = %q, want %q", got, "looks off")
	}
	// No parent, and never the dry-run key the route ignores.
	for _, key := range []string{"parent_id", "linked_dry_run_job_id"} {
		if raw, ok := (*bodies)[0][key]; ok && string(raw) != "null" {
			t.Errorf("%s = %s, want absent or null", key, raw)
		}
	}
	wantOut := "data-review/" + reviewA + " comment added [c: " + commentC + "] (thread: " + threadT + ")\n" +
		"Next: qctl comment-thread reply " + threadT + " --table-id 42 --body \"...\"\n" +
		"      qctl comment-thread edit-comment " + threadT + " " + commentC + " --table-id 42 --body \"...\"\n" +
		"      qctl comment-thread delete-comment " + threadT + " " + commentC + " --table-id 42 --yes\n"
	if stdout != wantOut {
		t.Errorf("stdout =\n%s\nwant\n%s", stdout, wantOut)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
}

func TestComment_BodyFileDashPostsStdinVerbatim(t *testing.T) {
	e := newTestEnv(t)
	e.respondReview(http.StatusOK, aReview(nil))
	bodies := e.respondComment(http.StatusCreated, aComment("x"))
	stdin := "  line one\n\n- a *list*\n"

	if err := e.execStdin(t, stdin, "comment", reviewA, "--body-file", "-"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := sentBody(t, bodies); got != stdin {
		t.Errorf("body = %q, want %q", got, stdin)
	}
}

func TestComment_ReplyToSendsTheParent(t *testing.T) {
	e := newTestEnv(t)
	e.respondReview(http.StatusOK, aReview(nil))
	bodies := e.respondComment(http.StatusCreated, aComment("fixed"))

	if _, _, err := e.exec(t, "comment", reviewA, "--body", "fixed", "--reply-to", commentC); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := string((*bodies)[0]["parent_id"]), `"`+commentC+`"`; got != want {
		t.Errorf("parent_id = %s, want %s", got, want)
	}
}

func TestComment_RefusesBadInputBeforeAnyRequest(t *testing.T) {
	cases := []struct {
		name  string
		stdin string
		args  []string
	}{
		{"body and body-file", "", []string{reviewA, "--body", "x", "--body-file", "notes.md"}},
		{"no body at all", "", []string{reviewA}},
		{"empty body", "", []string{reviewA, "--body", ""}},
		{"blank body", "", []string{reviewA, "--body", "  \n\t"}},
		{"empty stdin", "", []string{reviewA, "--body-file", "-"}},
		{"blank stdin", "\n\n", []string{reviewA, "--body-file", "-"}},
		{"over the cap", "", []string{reviewA, "--body", strings.Repeat("é", cmdutil.MaxCommentBodyChars+1)}},
		{"short review id", "", []string{"0190aaaa", "--body", "x"}},
		{"short reply-to", "", []string{reviewA, "--body", "x", "--reply-to", "0190cccc"}},
		{"no review id", "", []string{"--body", "x"}},
		{"two review ids", "", []string{reviewA, reviewB, "--body", "x"}},
		{"dry-run-job is not a flag here", "", []string{reviewA, "--body", "x", "--dry-run-job", "5"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestEnv(t)
			e.respondReview(http.StatusOK, aReview(nil))
			e.respondUsers(http.StatusOK)
			e.respondComment(http.StatusCreated, aComment("x"))

			err := e.execStdin(t, tc.stdin, append([]string{"comment"}, tc.args...)...)
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want 2 (err: %v)", got, err)
			}
			if got := e.mock.Requests(); len(got) != 0 {
				t.Errorf("requests = %v, want none", got)
			}
		})
	}
}

func TestComment_MentionAppendsTheCCLine(t *testing.T) {
	e := newTestEnv(t)
	e.respondReview(http.StatusOK, aReview(nil))
	e.respondUsers(http.StatusOK)
	bodies := e.respondComment(http.StatusCreated, aComment("x"))

	_, _, err := e.exec(t, "comment", reviewA, "--body", "please look",
		"--mention", "ada@example.com", "--mention", "Grace Hopper")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"GET " + reviewRoute(reviewA), "GET " + usersRoute, "POST " + commentRoute}
	if got := e.mock.Requests(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("requests = %v, want %v", got, want)
	}
	wantBody := "please look\n\ncc [@Ada Lovelace](mention:" + userAda + ") [@Grace Hopper](mention:" + userGrace + ")"
	if got := sentBody(t, bodies); got != wantBody {
		t.Errorf("body = %q, want %q", got, wantBody)
	}
}

func TestComment_UnresolvableMentionPostsNothing(t *testing.T) {
	e := newTestEnv(t)
	e.respondReview(http.StatusOK, aReview(nil))
	e.respondUsers(http.StatusOK)
	e.respondComment(http.StatusCreated, aComment("x"))

	_, _, err := e.exec(t, "comment", reviewA, "--body", "x", "--mention", "nobody@example.com")
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want 2 (err: %v)", got, err)
	}
	if got := e.posts(); got != 0 {
		t.Errorf("POSTs = %d, want 0", got)
	}
}

func TestComment_CCLinePushingPastTheCapPostsNothing(t *testing.T) {
	e := newTestEnv(t)
	e.respondReview(http.StatusOK, aReview(nil))
	e.respondUsers(http.StatusOK)
	e.respondComment(http.StatusCreated, aComment("x"))

	_, _, err := e.exec(t, "comment", reviewA, "--body", strings.Repeat("a", cmdutil.MaxCommentBodyChars-10),
		"--mention", "ada@example.com")
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want 2 (err: %v)", got, err)
	}
	if got := e.posts(); got != 0 {
		t.Errorf("POSTs = %d, want 0", got)
	}
}

func TestComment_RefusalsKeepTheirExitCodes(t *testing.T) {
	depth := map[string]any{"detail": map[string]any{
		"msg": "Maximum thread depth (3) exceeded", "error_code": "COMMENT_DEPTH_EXCEEDED",
		"context": map[string]any{"code": "COMMENT_DEPTH_EXCEEDED"},
	}}
	badParent := map[string]any{"detail": map[string]any{"msg": "Comment " + commentC + " not found", "error_id": ""}}
	cases := []struct {
		name   string
		status int
		body   map[string]any
		want   int
	}{
		{"depth exceeded", http.StatusUnprocessableEntity, depth, qerrors.ExitBadUsage},
		{"reply-to not in this thread", http.StatusUnprocessableEntity, badParent, qerrors.ExitBadUsage},
		{
			"mention refused", http.StatusUnprocessableEntity,
			map[string]any{"detail": map[string]any{
				"msg":     "You can only mention people who flagged or discussed your rows in this table.",
				"context": map[string]any{"user_ids": []string{userAda}},
			}},
			qerrors.ExitBadUsage,
		},
		{
			"unknown refusal", http.StatusUnprocessableEntity,
			map[string]any{"detail": map[string]any{"msg": "Another validation failure"}},
			qerrors.ExitBadUsage,
		},
		{
			"forbidden", http.StatusForbidden,
			map[string]any{"detail": map[string]any{"msg": "User is not authorized to access table 42"}},
			qerrors.ExitUnauthorized,
		},
		{
			"not found", http.StatusNotFound,
			map[string]any{"detail": map[string]any{"msg": "DataReview " + reviewA + " not found"}},
			qerrors.ExitNotFound,
		},
		{
			"body refused by validation", http.StatusUnprocessableEntity,
			map[string]any{"detail": []map[string]any{{"loc": []string{"body", "body"}, "msg": "too long", "type": "string_too_long"}}},
			qerrors.ExitBadUsage,
		},
		{
			"server error", http.StatusInternalServerError,
			map[string]any{"detail": "boom"},
			qerrors.ExitGenericError,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestEnv(t)
			e.respondReview(http.StatusOK, aReview(nil))
			e.respondComment(tc.status, tc.body)

			_, _, err := e.exec(t, "comment", reviewA, "--body", "x", "--reply-to", commentC)
			if got := qerrors.GetExitCode(err); got != tc.want {
				t.Fatalf("exit code = %d, want %d (err: %v)", got, tc.want, err)
			}
			wantParentHint := tc.name == "reply-to not in this thread"
			if got := strings.Contains(err.Error(), "--reply-to must name"); got != wantParentHint {
				t.Errorf("parent hint = %v, want %v: %v", got, wantParentHint, err)
			}
			if tc.name == "mention refused" && !strings.Contains(err.Error(), "You can only mention people") {
				t.Errorf("mention refusal lost its server message: %v", err)
			}
		})
	}
}

func TestComment_ReviewNotFoundPostsNothing(t *testing.T) {
	e := newTestEnv(t)
	e.respondReview(http.StatusNotFound, map[string]any{"detail": map[string]any{"msg": "DataReview not found"}})
	e.respondUsers(http.StatusOK)
	e.respondComment(http.StatusCreated, aComment("x"))

	_, _, err := e.exec(t, "comment", reviewA, "--body", "x", "--mention", "ada@example.com")
	if got := qerrors.GetExitCode(err); got != qerrors.ExitNotFound {
		t.Errorf("exit code = %d, want 3 (err: %v)", got, err)
	}
	if got := e.mock.Requests(); strings.Join(got, "\n") != "GET "+reviewRoute(reviewA) {
		t.Errorf("requests = %v, want only the review read", got)
	}
}

// A caller who may comment but not read reviews still posts; only the table
// id in the recipes is lost.
func TestComment_UnreadableReviewStillPosts(t *testing.T) {
	e := newTestEnv(t)
	e.respondReview(http.StatusForbidden, map[string]any{"detail": map[string]any{"msg": "Forbidden"}})
	e.respondComment(http.StatusCreated, aComment("x"))

	stdout, stderr, err := e.exec(t, "comment", reviewA, "--body", "x")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := e.posts(); got != 1 {
		t.Errorf("POSTs = %d, want 1", got)
	}
	if !strings.Contains(stdout, "Next: qctl comment-thread reply "+threadT+" --table-id <table-id> ") {
		t.Errorf("stdout = %q, want the reply recipe with a table-id placeholder", stdout)
	}
	if !strings.HasPrefix(stderr, "warning: ") {
		t.Errorf("stderr = %q, want a warning", stderr)
	}
}

// Under -o json the created comment is the only thing on stdout: its
// thread_id is what comment-thread reply takes next. No comment kind exists,
// so -o name exits 2 before the write.
func TestComment_JSONPrintsTheComment(t *testing.T) {
	e := newTestEnv(t)
	e.respondReview(http.StatusOK, aReview(nil))
	bodies := e.respondComment(http.StatusCreated, aComment("x"))

	stdout, stderr, err := e.exec(t, "comment", reviewA, "--body", "x", "-o", "json")
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
	if got.ThreadID != threadT || got.ID == "" {
		t.Errorf("document = %+v, want a comment on thread %s", got, threadT)
	}
	if !strings.Contains(stderr, "Next: qctl comment-thread reply "+threadT) {
		t.Errorf("stderr missing the reply recipe:\n%s", stderr)
	}

	_, _, err = e.exec(t, "comment", reviewA, "--body", "x", "-o", "name")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("-o name: exit %d (%v), want %d", code, err, qerrors.ExitBadUsage)
	}
	if len(*bodies) != 1 {
		t.Errorf("POST bodies = %d, want 1: the -o name refusal must come before the write", len(*bodies))
	}
}

// The external role may comment but cannot list users. A --mention given as a
// UUID resolves locally, so the comment posts; the cc label is presentation
// and, with the mentionable list unreadable too, falls back to the id after
// the directory warning.
func TestComment_UUIDMentionsPostThroughADirectory403(t *testing.T) {
	e := newTestEnv(t)
	e.respondReview(http.StatusOK, aReview(nil))
	e.respondUsers(http.StatusForbidden)
	bodies := e.respondComment(http.StatusCreated, aComment("x"))

	_, stderr, err := e.exec(t, "comment", reviewA, "--body", "please look", "--mention", userAda)
	if err != nil {
		t.Fatalf("the caller may comment: %v", err)
	}
	wantBody := "please look\n\ncc [@" + userAda + "](mention:" + userAda + ")"
	if got := sentBody(t, bodies); got != wantBody {
		t.Errorf("body = %q, want %q", got, wantBody)
	}
	if !strings.Contains(stderr, "Warning: user directory unavailable") {
		t.Errorf("stderr = %q, want the directory warning", stderr)
	}
}

// A mention by email needs the directory or the table's mentionable list: when
// both fail, the directory's 403 exits 4 and says which permission is missing,
// rather than posting a comment that mentions nobody.
func TestComment_ANamedMentionStillNeedsTheDirectory(t *testing.T) {
	e := newTestEnv(t)
	e.respondReview(http.StatusOK, aReview(nil))
	e.respondUsers(http.StatusForbidden)
	e.respondComment(http.StatusCreated, aComment("x"))

	_, _, err := e.exec(t, "comment", reviewA, "--body", "x", "--mention", "ada@example.com")
	if got := qerrors.GetExitCode(err); got != qerrors.ExitUnauthorized {
		t.Errorf("exit code = %d, want 4 (err: %v)", got, err)
	}
	if got := e.posts(); got != 0 {
		t.Errorf("POSTs = %d, want 0", got)
	}
	if !strings.Contains(err.Error(), "org:users:list") {
		t.Errorf("error = %q, want it to name the permission", err)
	}
}

// respondMentionable serves the review's table's mentionable list: Ada only.
func (e *testEnv) respondMentionable() {
	e.mock.RegisterHandler("GET", mentionableRoute, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, []map[string]any{
			{"id": userAda, "email": "ada@example.com", "first_name": "Ada", "last_name": "Lovelace"},
		})
	})
}

var mentionableRoute = "/api/orgs/" + testOrgID + "/datasets/42/mentionable-users"

// The editor role may read the review but not the directory: a name resolves
// against the people the review's table lets a comment mention.
func TestComment_NamedMentionFallsBackToTheMentionableList(t *testing.T) {
	e := newTestEnv(t)
	e.respondReview(http.StatusOK, aReview(nil))
	e.respondUsers(http.StatusForbidden)
	e.respondMentionable()
	bodies := e.respondComment(http.StatusCreated, aComment("x"))

	_, stderr, err := e.exec(t, "comment", reviewA, "--body", "please look", "--mention", "Ada Lovelace")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"GET " + reviewRoute(reviewA), "GET " + usersRoute, "GET " + mentionableRoute, "POST " + commentRoute}
	if got := e.mock.Requests(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("requests = %v, want %v", got, want)
	}
	wantBody := "please look\n\ncc [@Ada Lovelace](mention:" + userAda + ")"
	if got := sentBody(t, bodies); got != wantBody {
		t.Errorf("body = %q, want %q", got, wantBody)
	}
	if strings.Contains(stderr, "Warning") {
		t.Errorf("stderr = %q, want no warning once the fallback answered", stderr)
	}
}

// A name the mentionable list does not hold is a usage error naming the way
// past it, and nothing is posted.
func TestComment_NameOutsideTheMentionableListPostsNothing(t *testing.T) {
	e := newTestEnv(t)
	e.respondReview(http.StatusOK, aReview(nil))
	e.respondUsers(http.StatusForbidden)
	e.respondMentionable()
	e.respondComment(http.StatusCreated, aComment("x"))

	_, _, err := e.exec(t, "comment", reviewA, "--body", "x", "--mention", "grace@example.com")
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want 2 (err: %v)", got, err)
	}
	if err == nil || !strings.Contains(err.Error(), "full UUID") {
		t.Errorf("error = %v, want it to name the full-UUID way past", err)
	}
	if got := e.posts(); got != 0 {
		t.Errorf("POSTs = %d, want 0", got)
	}
}

// Without the review there is no table to ask, so a refused directory read is
// final.
func TestComment_UnreadableReviewSkipsTheMentionableFallback(t *testing.T) {
	e := newTestEnv(t)
	e.respondReview(http.StatusForbidden, map[string]any{"detail": map[string]any{"msg": "Forbidden"}})
	e.respondUsers(http.StatusForbidden)
	e.respondMentionable()
	e.respondComment(http.StatusCreated, aComment("x"))

	_, _, err := e.exec(t, "comment", reviewA, "--body", "x", "--mention", "ada@example.com")
	if got := qerrors.GetExitCode(err); got != qerrors.ExitUnauthorized {
		t.Errorf("exit code = %d, want 4 (err: %v)", got, err)
	}
	for _, r := range e.mock.Requests() {
		if r == "GET "+mentionableRoute {
			t.Error("the mentionable list was read without a table from the review")
		}
	}
}
