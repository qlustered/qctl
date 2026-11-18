//nolint:revive // Tests intentionally match the command package directory.
package comment_thread

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

// resolvedAt is the stamp a resolved thread comes back with. A var so the
// pointer fields of the generated schema can take its address.
var resolvedAt = time.Date(2026, 8, 26, 11, 0, 0, 0, time.UTC)

func resolutionPath(verb string) string {
	return "/api/orgs/" + testOrgID + "/datasets/42/comment-threads/" + testThreadUUID.String() + "/" + verb
}

// resolve/unresolve answer with STATE, not transcript: comments comes back
// empty by design and only comment_count is populated. A renderer that reuses
// the describe payload would announce "0 comments" on a thread with twelve.
func TestResolvePrintsCountNotTranscript(t *testing.T) {
	mock := newThreadTestEnv(t)
	registerTableResolve(mock, "orders", 42)
	twelve := 12
	mock.RegisterHandler("POST", resolutionPath("resolve"),
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.CommentThreadResponseSchema{
				ID:           testThreadUUID,
				CommentCount: &twelve,
				Comments:     &[]api.CommentResponseSchema{}, // deliberately empty
				ResolvedAt:   &resolvedAt,
			})
		})

	out, err := runThreadCommand(t, "", "comment-thread", "resolve",
		testThreadUUID.String(), "--table", "orders")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "12 comment") {
		t.Errorf("output must report the real count from comment_count:\n%s", out)
	}
	for _, forbidden := range []string{"0 comments", "## COMMENTS"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("output reused the transcript renderer (%q):\n%s", forbidden, out)
		}
	}
	if !strings.Contains(out, "resolved") {
		t.Errorf("output never says what happened:\n%s", out)
	}
}

// unresolve reopens: resolved_at comes back null, and the line has to say
// "reopened" rather than borrowing resolve's verb.
func TestUnresolveReportsReopened(t *testing.T) {
	mock := newThreadTestEnv(t)
	registerTableResolve(mock, "orders", 42)
	one := 1
	mock.RegisterHandler("POST", resolutionPath("unresolve"),
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.CommentThreadResponseSchema{
				ID:           testThreadUUID,
				CommentCount: &one,
				Comments:     &[]api.CommentResponseSchema{},
				ResolvedAt:   nil, // reopened
			})
		})

	out, err := runThreadCommand(t, "", "comment-thread", "unresolve",
		testThreadUUID.String(), "--table-id", "42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "reopened") {
		t.Errorf("output must say the thread was reopened:\n%s", out)
	}
	if strings.Contains(out, "resolved") {
		t.Errorf("unresolve must not borrow resolve's verb:\n%s", out)
	}
	// One comment, not "1 comments": the count is prose, not a field dump.
	if !strings.Contains(out, "1 comment)") {
		t.Errorf("singular count is mis-pluralised:\n%s", out)
	}
}

// Both verbs take a full thread UUID, checked before Bootstrap: a typo must
// exit 2 and cost no request at all.
func TestResolutionVerbsRejectBadIDsWithoutAnyRequest(t *testing.T) {
	for _, verb := range []string{"resolve", "unresolve"} {
		t.Run(verb, func(t *testing.T) {
			mock := newThreadTestEnv(t)
			calls := registerTableResolve(mock, "orders", 42)
			hit := false
			mock.RegisterHandler("POST", resolutionPath(verb), func(w http.ResponseWriter, _ *http.Request) {
				hit = true
				testutil.RespondJSON(w, http.StatusOK, api.CommentThreadResponseSchema{ID: testThreadUUID})
			})

			_, err := runThreadCommand(t, "", "comment-thread", verb, "not-a-uuid", "--table", "orders")
			if err == nil {
				t.Fatal("expected a usage error for a malformed thread id")
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d (%v)", code, qerrors.ExitBadUsage, err)
			}
			if *calls != 0 {
				t.Errorf("the table was resolved %d time(s) before the id was validated", *calls)
			}
			if hit {
				t.Error("a malformed id must not reach the backend")
			}
		})
	}
}

// A resolution 404 includes advice about source visibility and rule-review threads.
func TestResolutionNotFoundHintsRuleReviewThreads(t *testing.T) {
	for _, verb := range []string{"resolve", "unresolve"} {
		t.Run(verb, func(t *testing.T) {
			mock := newThreadTestEnv(t)
			registerTableResolve(mock, "orders", 42)
			mock.RegisterHandler("POST", resolutionPath(verb), func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusNotFound, map[string]interface{}{
					"detail": map[string]interface{}{"msg": "Comment thread not found"},
				})
			})

			_, err := runThreadCommand(t, "", "comment-thread", verb, testThreadUUID.String(), "--table", "orders")
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
		})
	}
}

// Neither route checks authorship — both are comment:write plus the dataset
// authorization — so the edit/delete wording would send the user looking for a
// problem that does not exist.
func TestResolutionForbiddenNamesThePermission(t *testing.T) {
	for _, verb := range []string{"resolve", "unresolve"} {
		t.Run(verb, func(t *testing.T) {
			mock := newThreadTestEnv(t)
			registerTableResolve(mock, "orders", 42)
			mock.RegisterHandler("POST", resolutionPath(verb), func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusForbidden, map[string]interface{}{
					"detail": map[string]interface{}{"msg": "Forbidden"},
				})
			})

			_, err := runThreadCommand(t, "", "comment-thread", verb, testThreadUUID.String(), "--table", "orders")
			if err == nil {
				t.Fatal("expected a forbidden error")
			}
			if strings.Contains(err.Error(), "author") {
				t.Errorf("%s's 403 is never about authorship: %v", verb, err)
			}
			if !strings.Contains(err.Error(), "comment:write") {
				t.Errorf("403 gives no way forward: %v", err)
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitUnauthorized {
				t.Errorf("exit code = %d, want %d", code, qerrors.ExitUnauthorized)
			}
		})
	}
}
