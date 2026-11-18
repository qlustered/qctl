//nolint:revive // Tests intentionally match the command package directory.
package comment_thread

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestReplyPostsTheComment(t *testing.T) {
	mock := newThreadTestEnv(t)
	var gotBody map[string]interface{}
	registerAddComment(mock, &gotBody)
	registerTableResolve(mock, "orders", 42)

	out, err := runThreadCommand(t, "", "comment-thread", "reply", testThreadUUID.String(),
		"--table", "orders", "--body", "please look")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotBody["body"] != "please look" {
		t.Errorf("wire body = %v, want %q", gotBody["body"], "please look")
	}
	// A top-level comment goes out with an explicit null, never the empty UUID.
	if gotBody["parent_id"] != nil {
		t.Errorf("parent_id = %v, want null", gotBody["parent_id"])
	}
	// The backend discards it here, so qctl must not pretend it linked one.
	if gotBody["linked_dry_run_job_id"] != nil {
		t.Errorf("linked_dry_run_job_id = %v, want null", gotBody["linked_dry_run_job_id"])
	}
	want := "comment-thread/" + testThreadUUID.String() + " comment added [c: " + testCommentUUID.String() + "]"
	if !strings.Contains(out, want) {
		t.Errorf("output missing %q\n%s", want, out)
	}
}

func TestReplyToSetsParentID(t *testing.T) {
	mock := newThreadTestEnv(t)
	var gotBody map[string]interface{}
	registerAddComment(mock, &gotBody)
	registerTableResolve(mock, "orders", 42)

	if _, err := runThreadCommand(t, "", "comment-thread", "reply", testThreadUUID.String(),
		"--table-id", "42", "--body", "PTAL", "--reply-to", testCommentUUID.String()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotBody["parent_id"] != testCommentUUID.String() {
		t.Errorf("parent_id = %v, want %s", gotBody["parent_id"], testCommentUUID)
	}
}

// --mention resolves users and appends structured links: a bare @name is not a
// mention, only the link form notifies the person it names.
func TestReplyMentionAppendsCcLine(t *testing.T) {
	mock := newThreadTestEnv(t)
	var gotBody map[string]interface{}
	registerAddComment(mock, &gotBody)
	registerUsersList(mock)
	registerTableResolve(mock, "orders", 42)

	if _, err := runThreadCommand(t, "", "comment-thread", "reply", testThreadUUID.String(),
		"--table", "orders", "--body", "please look", "--mention", "alice@corp.com"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "please look\n\ncc [@Alice Chen](mention:" + testUserUUID.String() + ")"
	if gotBody["body"] != want {
		t.Errorf("wire body = %q, want %q", gotBody["body"], want)
	}
}

// An unresolvable --mention is a flag validation failure, not a server error:
// exit 2, and nothing is posted.
func TestReplyUnknownMentionIsBadUsage(t *testing.T) {
	mock := newThreadTestEnv(t)
	posted := false
	mock.RegisterHandler("POST", threadCommentsPath, func(w http.ResponseWriter, _ *http.Request) {
		posted = true
		testutil.RespondJSON(w, http.StatusCreated, sampleThreadComment())
	})
	registerUsersList(mock)
	registerTableResolve(mock, "orders", 42)

	_, err := runThreadCommand(t, "", "comment-thread", "reply", testThreadUUID.String(),
		"--table", "orders", "--body", "x", "--mention", "nobody@corp.com")
	if err == nil {
		t.Fatal("expected an unresolved-mention error")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (%v)", code, qerrors.ExitBadUsage, err)
	}
	if posted {
		t.Error("comment must not be posted when a mention does not resolve")
	}
}

// Depth IS capped at 3 on this arc — the same MAX_THREAD_DEPTH the rule arc
// uses. The 422 carries error_code COMMENT_DEPTH_EXCEEDED; the message must say
// what to do about it.
func TestReplyMapsDepthExceeded(t *testing.T) {
	mock := newThreadTestEnv(t)
	registerTableResolve(mock, "orders", 42)
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/42/comment-threads/"+testThreadUUID.String()+"/comments",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusUnprocessableEntity, map[string]interface{}{
				"detail": map[string]interface{}{
					"msg":        "Maximum thread depth (3) exceeded",
					"error_code": "COMMENT_DEPTH_EXCEEDED",
					"context":    map[string]interface{}{"code": "comment_depth_exceeded"},
				},
			})
		})

	_, err := runThreadCommand(t, "", "comment-thread", "reply", testThreadUUID.String(),
		"--table", "orders", "--body", "x", "--reply-to", testCommentUUID.String())
	if err == nil {
		t.Fatal("expected a depth error")
	}
	if !strings.Contains(err.Error(), "max thread depth is 3") {
		t.Errorf("message gives no way forward: %v", err)
	}
	// The hint must ride a %w chain or the 422's exit code degrades to 1.
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
}

// There is no --dry-run-job here: the backend accepts linked_dry_run_job_id on
// this arc and silently discards it (rule arc only). Offering the flag would
// promise something that never happens.
func TestReplyHasNoDryRunJobFlag(t *testing.T) {
	cmd := setupThreadTestCommand()
	reply, _, err := cmd.Find([]string{"comment-thread", "reply"})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	// Find falls back to the parent for an unknown name, which would make the
	// assertion below pass against a tree with no reply command at all.
	if reply.Name() != "reply" {
		t.Fatalf("Find landed on %q, want the reply command", reply.Name())
	}
	if reply.Flags().Lookup("dry-run-job") != nil {
		t.Error("--dry-run-job is registered; the backend discards it on this arc")
	}
}

// A thread 404 includes advice about source visibility and rule-review threads.
func TestReplyNotFoundHintsRuleReviewThreads(t *testing.T) {
	mock := newThreadTestEnv(t)
	registerTableResolve(mock, "orders", 42)
	mock.RegisterHandler("POST", threadCommentsPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusNotFound, map[string]interface{}{
			"detail": map[string]interface{}{"msg": "Comment thread not found"},
		})
	})

	_, err := runThreadCommand(t, "", "comment-thread", "reply", testThreadUUID.String(),
		"--table", "orders", "--body", "x")
	if err == nil {
		t.Fatal("expected a not-found error")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
		t.Errorf("exit code = %d, want %d (%v)", code, qerrors.ExitNotFound, err)
	}
	if !strings.Contains(err.Error(), "qctl describe rule-review") {
		t.Errorf("404 gives no way forward: %v", err)
	}
	if !strings.Contains(err.Error(), "hidden by your source access") {
		t.Errorf("404 drops the source visibility cause: %v", err)
	}
}

// Every id is a full UUID and is checked before Bootstrap: a typo must not cost
// the table lookup, and it must exit 2 rather than 1.
func TestCommentVerbsRejectBadIDsWithoutAnyRequest(t *testing.T) {
	bad := "not-a-uuid"
	cases := map[string][]string{
		"reply thread id":         {"comment-thread", "reply", bad, "--table", "orders", "--body", "x"},
		"reply --reply-to":        {"comment-thread", "reply", testThreadUUID.String(), "--table", "orders", "--body", "x", "--reply-to", bad},
		"edit-comment thread id":  {"comment-thread", "edit-comment", bad, testCommentUUID.String(), "--table", "orders", "--body", "x"},
		"edit-comment comment id": {"comment-thread", "edit-comment", testThreadUUID.String(), bad, "--table", "orders", "--body", "x"},
		"delete-comment thread id": {
			"comment-thread", "delete-comment", bad, testCommentUUID.String(),
			"--table", "orders", "--yes",
		},
		"delete-comment comment id": {
			"comment-thread", "delete-comment", testThreadUUID.String(), bad,
			"--table", "orders", "--yes",
		},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			mock := newThreadTestEnv(t)
			calls := registerTableResolve(mock, "orders", 42)

			_, err := runThreadCommand(t, "", args...)
			if err == nil {
				t.Fatalf("expected a usage error for %q", bad)
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d (%v)", code, qerrors.ExitBadUsage, err)
			}
			if *calls != 0 {
				t.Errorf("the table was resolved %d time(s) before the id was validated", *calls)
			}
		})
	}
}

func TestReplyAndEditRejectAnEmptyBody(t *testing.T) {
	cases := map[string][]string{
		"reply":        {"comment-thread", "reply", testThreadUUID.String(), "--table", "orders", "--body", "   "},
		"edit-comment": {"comment-thread", "edit-comment", testThreadUUID.String(), testCommentUUID.String(), "--table", "orders", "--body", "   "},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			mock := newThreadTestEnv(t)
			calls := registerTableResolve(mock, "orders", 42)

			_, err := runThreadCommand(t, "", args...)
			if err == nil || !strings.Contains(err.Error(), "comment body is empty") {
				t.Fatalf("want empty-body error, got %v", err)
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
			}
			if *calls != 0 {
				t.Errorf("the table was resolved %d time(s) for a body that was never sendable", *calls)
			}
		})
	}
}

func TestEditCommentReplacesTheBody(t *testing.T) {
	mock := newThreadTestEnv(t)
	registerTableResolve(mock, "orders", 42)
	var gotBody map[string]interface{}
	mock.RegisterHandler("PATCH", threadCommentsPath+"/"+testCommentUUID.String(),
		func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			edited := sampleThreadComment()
			edited.Body, edited.IsEdited = "fixed", true
			testutil.RespondJSON(w, http.StatusOK, edited)
		})

	out, err := runThreadCommand(t, "", "comment-thread", "edit-comment",
		testThreadUUID.String(), testCommentUUID.String(), "--table", "orders", "--body", "fixed")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotBody["body"] != "fixed" {
		t.Errorf("wire body = %v, want %q", gotBody["body"], "fixed")
	}
	want := "comment-thread/" + testThreadUUID.String() + " comment " + testCommentUUID.String() + " edited"
	if !strings.Contains(out, want) {
		t.Errorf("output missing %q\n%s", want, out)
	}
}

// An ambiguous edit 403 includes authorship among its requirements.
func TestEditCommentForbiddenExplainsAuthorOnly(t *testing.T) {
	mock := newThreadTestEnv(t)
	registerTableResolve(mock, "orders", 42)
	mock.RegisterHandler("PATCH", threadCommentsPath+"/"+testCommentUUID.String(),
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusForbidden, map[string]interface{}{
				"detail": map[string]interface{}{"msg": "Forbidden"},
			})
		})

	_, err := runThreadCommand(t, "", "comment-thread", "edit-comment",
		testThreadUUID.String(), testCommentUUID.String(), "--table", "orders", "--body", "x")
	if err == nil {
		t.Fatal("expected a forbidden error")
	}
	if !strings.Contains(err.Error(), "only the comment's author") {
		t.Errorf("403 gives no way forward: %v", err)
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitUnauthorized {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitUnauthorized)
	}
}

func TestDeleteCommentWithYes(t *testing.T) {
	mock := newThreadTestEnv(t)
	registerTableResolve(mock, "orders", 42)
	called := false
	mock.RegisterHandler("DELETE", threadCommentsPath+"/"+testCommentUUID.String(),
		func(w http.ResponseWriter, _ *http.Request) {
			called = true
			testutil.RespondJSON(w, http.StatusOK, map[string]string{"result": "ok"})
		})

	out, err := runThreadCommand(t, "", "comment-thread", "delete-comment",
		testThreadUUID.String(), testCommentUUID.String(), "--table", "orders", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Error("DELETE endpoint was never hit")
	}
	want := "comment-thread/" + testThreadUUID.String() + " comment " + testCommentUUID.String() + " deleted"
	if !strings.Contains(out, want) {
		t.Errorf("output missing %q\n%s", want, out)
	}
}

// Delete prompts unless --yes, matching review delete-comment: a declined
// prompt makes no request, says so, and is a success.
func TestDeleteRequiresConfirmation(t *testing.T) {
	mock := newThreadTestEnv(t)
	registerTableResolve(mock, "orders", 42)
	called := false
	mock.RegisterHandler("DELETE", threadCommentsPath+"/"+testCommentUUID.String(),
		func(w http.ResponseWriter, _ *http.Request) {
			called = true
			testutil.RespondJSON(w, http.StatusOK, map[string]string{"result": "ok"})
		})
	cmdutil.SetReader(strings.NewReader("n\n"))
	defer cmdutil.ResetReader()

	out, err := runThreadCommand(t, "", "comment-thread", "delete-comment",
		testThreadUUID.String(), testCommentUUID.String(), "--table", "orders")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called {
		t.Error("declined confirmation must not delete")
	}
	if !strings.Contains(out, "Aborted") {
		t.Errorf("output missing Aborted\n%s", out)
	}
}

// A reply 403 includes permission advice.
func TestReplyForbiddenNamesThePermission(t *testing.T) {
	mock := newThreadTestEnv(t)
	registerTableResolve(mock, "orders", 42)
	mock.RegisterHandler("POST", threadCommentsPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusForbidden, map[string]interface{}{
			"detail": map[string]interface{}{"msg": "Forbidden"},
		})
	})

	_, err := runThreadCommand(t, "", "comment-thread", "reply", testThreadUUID.String(),
		"--table", "orders", "--body", "x")
	if err == nil {
		t.Fatal("expected a forbidden error")
	}
	if strings.Contains(err.Error(), "only the comment's author") {
		t.Errorf("reply's 403 is never about authorship: %v", err)
	}
	if !strings.Contains(err.Error(), "comment:write") {
		t.Errorf("403 gives no way forward: %v", err)
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitUnauthorized {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitUnauthorized)
	}
}

func TestCommentVerbsForbiddenIncludeAccessRequirements(t *testing.T) {
	for _, tc := range []struct {
		verb   string
		method string
		path   string
		extra  []string
	}{
		{"reply", "POST", threadCommentsPath, []string{"--body", "x"}},
		{
			"edit-comment", "PATCH", threadCommentsPath + "/" + testCommentUUID.String(),
			[]string{testCommentUUID.String(), "--body", "x"},
		},
		{
			"delete-comment", "DELETE", threadCommentsPath + "/" + testCommentUUID.String(),
			[]string{testCommentUUID.String(), "--yes"},
		},
		{"resolve", "POST", resolutionPath("resolve"), nil},
		{"unresolve", "POST", resolutionPath("unresolve"), nil},
	} {
		t.Run(tc.verb, func(t *testing.T) {
			messages := []string{
				"Forbidden", "User is not authorized to access table 42",
				"You do not have access to this table's sources.",
			}
			if tc.verb == "resolve" || tc.verb == "unresolve" {
				messages = append(messages, "This discussion also covers rows outside your sources.")
			}
			for _, message := range messages {
				t.Run(message, func(t *testing.T) {
					mock := newThreadTestEnv(t)
					mock.RegisterHandler(tc.method, tc.path, func(w http.ResponseWriter, _ *http.Request) {
						testutil.RespondJSON(w, http.StatusForbidden, map[string]any{"detail": map[string]any{"msg": message}})
					})
					args := []string{"comment-thread", tc.verb, testThreadUUID.String(), "--table-id", "42"}
					_, err := runThreadCommand(t, "", append(args, tc.extra...)...)
					if code := qerrors.GetExitCode(err); code != qerrors.ExitUnauthorized {
						t.Fatalf("exit code = %d, want 4 (%v)", code, err)
					}
					for _, want := range []string{message, "comment:write", "access to this table or its assigned sources"} {
						if !strings.Contains(err.Error(), want) {
							t.Errorf("error missing %q: %v", want, err)
						}
					}
					if (tc.verb == "resolve" || tc.verb == "unresolve") &&
						!strings.Contains(err.Error(), "every target row on their assigned sources") {
						t.Errorf("error omits complete source coverage: %v", err)
					}
				})
			}
		})
	}
}

func TestReplyParentHintMatchesTheServerRefusal(t *testing.T) {
	parentID := testCommentUUID.String()
	for _, tc := range []struct {
		name     string
		detail   any
		replyTo  string
		wantHint bool
	}{
		{"missing parent", map[string]any{"msg": "Comment " + parentID + " not found"}, parentID, true},
		{"uppercase parent", map[string]any{"msg": "Comment " + parentID + " not found"}, strings.ToUpper(parentID), true},
		{"missing other comment", map[string]any{"msg": "Comment " + testThreadUUID.String() + " not found"}, parentID, false},
		{"mention refused", map[string]any{
			"msg":     "You can only mention people who flagged or discussed your rows in this table.",
			"context": map[string]any{"user_ids": []string{testUserUUID.String()}},
		}, parentID, false},
		{"body validation", []map[string]any{{"loc": []string{"body", "body"}, "msg": "too long", "type": "string_too_long"}}, parentID, false},
		{"unknown refusal", map[string]any{"msg": "Another validation failure"}, parentID, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := newThreadTestEnv(t)
			mock.RegisterHandler("POST", threadCommentsPath, func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusUnprocessableEntity, map[string]any{"detail": tc.detail})
			})
			_, err := runThreadCommand(t, "", "comment-thread", "reply", testThreadUUID.String(),
				"--table-id", "42", "--body", "x", "--reply-to", tc.replyTo)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit code = %d, want 2 (%v)", code, err)
			}
			if got := strings.Contains(err.Error(), "--reply-to must name"); got != tc.wantHint {
				t.Errorf("parent hint = %v, want %v: %v", got, tc.wantHint, err)
			}
		})
	}
}

// A missing parent 422 carries advice about --reply-to.
func TestReplyBadParentHintsReplyTo(t *testing.T) {
	newEnv := func(t *testing.T) {
		t.Helper()
		mock := newThreadTestEnv(t)
		registerTableResolve(mock, "orders", 42)
		mock.RegisterHandler("POST", threadCommentsPath, func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusUnprocessableEntity, map[string]interface{}{
				"detail": map[string]interface{}{"msg": "Comment " + testCommentUUID.String() + " not found"},
			})
		})
	}

	t.Run("with --reply-to", func(t *testing.T) {
		newEnv(t)
		_, err := runThreadCommand(t, "", "comment-thread", "reply", testThreadUUID.String(),
			"--table", "orders", "--body", "x", "--reply-to", testCommentUUID.String())
		if err == nil {
			t.Fatal("expected a 422")
		}
		if !strings.Contains(err.Error(), "--reply-to") {
			t.Errorf("422 gives no way forward: %v", err)
		}
		if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
			t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
		}
	})

	// Without the flag the clause would point at something the user never
	// typed, which is worse than no hint at all.
	t.Run("without --reply-to", func(t *testing.T) {
		newEnv(t)
		_, err := runThreadCommand(t, "", "comment-thread", "reply", testThreadUUID.String(),
			"--table", "orders", "--body", "x")
		if err == nil {
			t.Fatal("expected a 422")
		}
		if strings.Contains(err.Error(), "--reply-to") {
			t.Errorf("hint names a flag the user never passed: %v", err)
		}
	})
}

func TestEditAndDeleteNotFoundNamesCommentAndThreadCauses(t *testing.T) {
	cases := map[string]struct {
		method string
		args   []string
	}{
		"edit-comment": {"PATCH", []string{
			"comment-thread", "edit-comment",
			testThreadUUID.String(), testCommentUUID.String(), "--table", "orders", "--body", "x",
		}},
		"delete-comment": {"DELETE", []string{
			"comment-thread", "delete-comment",
			testThreadUUID.String(), testCommentUUID.String(), "--table", "orders", "--yes",
		}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			mock := newThreadTestEnv(t)
			registerTableResolve(mock, "orders", 42)
			mock.RegisterHandler(tc.method, threadCommentsPath+"/"+testCommentUUID.String(),
				func(w http.ResponseWriter, _ *http.Request) {
					testutil.RespondJSON(w, http.StatusNotFound, map[string]interface{}{
						"detail": map[string]interface{}{"msg": "Comment not found"},
					})
				})

			_, err := runThreadCommand(t, "", tc.args...)
			if err == nil {
				t.Fatal("expected a not-found error")
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
				t.Errorf("exit code = %d, want %d (%v)", code, qerrors.ExitNotFound, err)
			}
			// Cause one: the comment id itself.
			if !strings.Contains(err.Error(), testCommentUUID.String()) {
				t.Errorf("404 never names the comment id: %v", err)
			}
			// Cause two: the thread belongs to the rule-review arc.
			if !strings.Contains(err.Error(), "qctl describe rule-review") {
				t.Errorf("404 drops the rule-review cause: %v", err)
			}
			if !strings.Contains(err.Error(), "hidden by your source access") {
				t.Errorf("404 drops the source visibility cause: %v", err)
			}
		})
	}
}

// The cc line --mention appends is part of what is sent, so the body cap must
// be measured on the payload rather than on what the user typed.
func TestReplyMentionsCountTowardTheBodyCap(t *testing.T) {
	mock := newThreadTestEnv(t)
	registerTableResolve(mock, "orders", 42)
	registerUsersList(mock)
	posted := false
	mock.RegisterHandler("POST", threadCommentsPath, func(w http.ResponseWriter, _ *http.Request) {
		posted = true
		testutil.RespondJSON(w, http.StatusCreated, sampleThreadComment())
	})

	// Under the cap on its own; over it once the cc line is appended.
	body := strings.Repeat("a", cmdutil.MaxCommentBodyChars-10)
	_, err := runThreadCommand(t, "", "comment-thread", "reply", testThreadUUID.String(),
		"--table", "orders", "--body", body, "--mention", "alice@corp.com")
	if err == nil {
		t.Fatal("expected the appended cc line to push the body over the cap")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (%v)", code, qerrors.ExitBadUsage, err)
	}
	if posted {
		t.Error("an over-cap body must not be sent")
	}
}

// Adding a thread comment needs comment:write, which the external role holds
// without org:users:list. A --mention given as a UUID therefore has to post
// without a usable directory; with the mentionable list unreadable too, the cc
// label falls back to the id that notifies.
func TestReplyUUIDMentionPostsWithoutTheDirectory(t *testing.T) {
	mock := newThreadTestEnv(t)
	var gotBody map[string]interface{}
	registerAddComment(mock, &gotBody)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/users", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusForbidden,
			map[string]any{"detail": map[string]any{"msg": "no access to the user list"}})
	})
	registerTableResolve(mock, "orders", 42)

	_, stderr, err := runThreadCommandSplit(t, "comment-thread", "reply", testThreadUUID.String(),
		"--table", "orders", "--body", "please look", "--mention", testUserUUID.String())
	if err != nil {
		t.Fatalf("the caller may comment: %v", err)
	}
	want := "please look\n\ncc [@" + testUserUUID.String() + "](mention:" + testUserUUID.String() + ")"
	if gotBody["body"] != want {
		t.Errorf("wire body = %q, want %q", gotBody["body"], want)
	}
	if !strings.Contains(stderr, "Warning: user directory unavailable") {
		t.Errorf("stderr = %q, want the directory warning", stderr)
	}
}

// registerForbiddenUsersList refuses the org membership sweep, as it is
// refused to the editor and external roles.
func registerForbiddenUsersList(mock *testutil.MockAPIServer) {
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/users", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusForbidden,
			map[string]any{"detail": map[string]any{"msg": "no access to the user list"}})
	})
}

// registerMentionable serves table 42's mentionable list: Alice only.
func registerMentionable(mock *testutil.MockAPIServer, calls *int) {
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/mentionable-users",
		func(w http.ResponseWriter, _ *http.Request) {
			*calls++
			testutil.RespondJSON(w, http.StatusOK, []map[string]any{
				{"id": testUserUUID.String(), "email": "alice@corp.com", "first_name": "Alice", "last_name": "Chen"},
			})
		})
}

// A caller refused the directory resolves a name against the people the table
// lets a comment mention, and labels the cc link from the same list.
func TestReplyMentionFallsBackToTheMentionableList(t *testing.T) {
	for _, mention := range []string{"Alice Chen", testUserUUID.String()} {
		t.Run(mention, func(t *testing.T) {
			mock := newThreadTestEnv(t)
			var gotBody map[string]interface{}
			registerAddComment(mock, &gotBody)
			registerForbiddenUsersList(mock)
			calls := 0
			registerMentionable(mock, &calls)
			registerTableResolve(mock, "orders", 42)

			_, stderr, err := runThreadCommandSplit(t, "comment-thread", "reply", testThreadUUID.String(),
				"--table", "orders", "--body", "please look", "--mention", mention)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			want := "please look\n\ncc [@Alice Chen](mention:" + testUserUUID.String() + ")"
			if gotBody["body"] != want {
				t.Errorf("wire body = %q, want %q", gotBody["body"], want)
			}
			if calls != 1 {
				t.Errorf("mentionable reads = %d, want 1", calls)
			}
			if strings.Contains(stderr, "Warning") {
				t.Errorf("stderr = %q, want no warning once the fallback answered", stderr)
			}
		})
	}
}

// A name outside the mentionable list exits 2 before anything is posted — for
// an external author the backend would refuse it with a 422 anyway.
func TestReplyMentionOutsideTheMentionableListIsBadUsage(t *testing.T) {
	mock := newThreadTestEnv(t)
	posted := false
	mock.RegisterHandler("POST", threadCommentsPath, func(w http.ResponseWriter, _ *http.Request) {
		posted = true
		testutil.RespondJSON(w, http.StatusCreated, sampleThreadComment())
	})
	registerForbiddenUsersList(mock)
	calls := 0
	registerMentionable(mock, &calls)
	registerTableResolve(mock, "orders", 42)

	_, err := runThreadCommand(t, "", "comment-thread", "reply", testThreadUUID.String(),
		"--table", "orders", "--body", "x", "--mention", "bob@corp.com")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (%v)", code, qerrors.ExitBadUsage, err)
	}
	if err == nil || !strings.Contains(err.Error(), "flagged rows of this table") {
		t.Errorf("error = %v, want it to say which people resolve", err)
	}
	if posted {
		t.Error("comment must not be posted when a mention does not resolve")
	}
}

// A readable directory is never second-guessed by the narrower list.
func TestReplyMentionSkipsTheMentionableListWhenTheDirectoryAnswers(t *testing.T) {
	mock := newThreadTestEnv(t)
	var gotBody map[string]interface{}
	registerAddComment(mock, &gotBody)
	registerUsersList(mock)
	calls := 0
	registerMentionable(mock, &calls)
	registerTableResolve(mock, "orders", 42)

	if _, err := runThreadCommand(t, "", "comment-thread", "reply", testThreadUUID.String(),
		"--table", "orders", "--body", "x", "--mention", "alice@corp.com"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 0 {
		t.Errorf("mentionable reads = %d, want 0", calls)
	}
}
