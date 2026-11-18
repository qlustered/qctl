package review

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

// Every review verb that answers with a body writes it under -o json, moves
// its result lines to stderr, and names the review under -o name; the two
// comment verbs answer with a comment, which has no kind, so they refuse
// -o name.
func TestReviewVerbsWriteTheResponse(t *testing.T) {
	base := "/api/orgs/" + testOrgID + "/rule-reviews/" + testReviewUUID.String()
	id := testReviewUUID.String()
	decision := sampleDecisionResult(api.ReviewDecisionEnumApprove, false, api.ReviewRequestStatusOpen)
	cases := []struct {
		name, method, route string
		status              int
		reply               interface{}
		args                []string
		key                 string // the document key that carries the review id
		named               bool
	}{
		{"approve", "POST", "/decisions", http.StatusCreated, decision, []string{"approve", id}, "", true},
		{"reject", "POST", "/decisions", http.StatusCreated, decision, []string{"reject", id}, "", true},
		{"request-changes", "POST", "/decisions", http.StatusCreated, decision, []string{"request-changes", id}, "", true},
		{"withdraw", "POST", "/withdraw-decision", http.StatusOK, sampleReviewDetail(api.ReviewRequestStatusOpen),
			[]string{"withdraw", id}, "id", true},
		{"close", "POST", "/close", http.StatusOK, sampleReview(api.ReviewRequestStatusClosed),
			[]string{"close", id}, "id", true},
		{"reopen", "POST", "/reopen", http.StatusOK, sampleReviewDetail(api.ReviewRequestStatusOpen),
			[]string{"reopen", id}, "id", true},
		{"assign", "POST", "/assign", http.StatusOK, sampleReviewDetail(api.ReviewRequestStatusOpen),
			[]string{"assign", id, "--reviewer", testReviewerUUID.String()}, "id", true},
		{"unassign", "POST", "/unassign", http.StatusOK, sampleReviewDetail(api.ReviewRequestStatusOpen),
			[]string{"unassign", id, "--reviewer", testReviewerUUID.String()}, "id", true},
		{"re-request", "POST", "/re-request", http.StatusOK,
			api.RerequestReviewersResponseSchema{ReviewID: testReviewUUID, Status: api.ReviewRequestStatusOpen},
			[]string{"re-request", id}, "review_id", true},
		{"comment", "POST", "/comments", http.StatusCreated, api.CommentResponseSchema{ID: testCommentUUID},
			[]string{"comment", id, "--body", "x"}, "", false},
		{"edit-comment", "PATCH", "/comments/" + testCommentUUID.String(), http.StatusOK,
			api.CommentResponseSchema{ID: testCommentUUID},
			[]string{"edit-comment", id, testCommentUUID.String(), "--body", "x"}, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := newReviewTestEnv(t)
			mock.RegisterHandler(tc.method, base+tc.route, func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, tc.status, tc.reply)
			})
			// The decision verbs' best-effort approvals read.
			mock.RegisterHandler("GET", base, func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, sampleReviewDetail(api.ReviewRequestStatusOpen))
			})

			stdout, stderr, err := runReviewSplit(t, append(append([]string{"review"}, tc.args...), "-o", "json")...)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			var got map[string]interface{}
			if err := json.Unmarshal([]byte(stdout), &got); err != nil {
				t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
			}
			switch {
			case tc.key != "":
				if got[tc.key] != id {
					t.Errorf("%s = %v, want %s", tc.key, got[tc.key], id)
				}
			case tc.named:
				if _, ok := got["review_status"]; !ok {
					t.Errorf("decision document has no review_status:\n%s", stdout)
				}
			default:
				if got["id"] != testCommentUUID.String() {
					t.Errorf("comment id = %v, want %s", got["id"], testCommentUUID)
				}
			}
			if !strings.Contains(stderr, "rule-review/"+id) {
				t.Errorf("stderr missing the result line:\n%s", stderr)
			}

			stdout, _, err = runReviewSplit(t, append(append([]string{"review"}, tc.args...), "-o", "name")...)
			if !tc.named {
				if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
					t.Errorf("-o name: exit %d (%v), want %d", code, err, qerrors.ExitBadUsage)
				}
				return
			}
			if err != nil {
				t.Fatalf("-o name: unexpected error: %v", err)
			}
			if want := "rule-review/" + id + "\n"; stdout != want {
				t.Errorf("-o name stdout = %q, want %q", stdout, want)
			}
		})
	}
}

func runReviewSplit(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := setupReviewTestCommand()
	cmd.SilenceUsage = true
	var out, errOut strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}
