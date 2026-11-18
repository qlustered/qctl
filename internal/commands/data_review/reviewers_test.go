package data_review //nolint:revive // Package name mirrors the hyphenated command.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

func reviewerRoute(verb string) string {
	return reviewRoute(reviewA) + "/" + verb
}

// reviewWith is reviewA's detail with the given assignees.
func reviewWith(status string, assignees ...string) map[string]any {
	r := aReview(nil)
	r["status"] = status
	list := make([]map[string]any, 0, len(assignees))
	for _, id := range assignees {
		list = append(list, map[string]any{"id": id, "first_name": "x", "last_name": "y"})
	}
	r["assigned_reviewers"] = list
	return r
}

// respondReviewerPOST serves one of reviewA's reviewer routes and records
// every raw body it receives; close sends none, recorded as nil.
func (e *testEnv) respondReviewerPOST(verb string, status int, body any) *[]map[string]json.RawMessage {
	bodies := &[]map[string]json.RawMessage{}
	e.mock.RegisterHandler("POST", reviewerRoute(verb), func(w http.ResponseWriter, r *http.Request) {
		var got map[string]json.RawMessage
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&got)
		}
		*bodies = append(*bodies, got)
		testutil.RespondJSON(w, status, body)
	})
	return bodies
}

// notOpen is a not-active refusal carrying context.status, as re-request's
// 409 does.
func notOpen(code, status string) map[string]any {
	return map[string]any{"detail": map[string]any{
		"msg": "Review is not active", "error_code": code,
		"context": map[string]any{"code": code, "data_review_id": reviewA, "status": status},
	}}
}

// notOpenBare is assign's and unassign's real 422: the status is only in msg.
func notOpenBare(status string) map[string]any {
	return map[string]any{"detail": map[string]any{
		"msg": "DataReview is not open (status: " + status + ")", "error_code": "REVIEW_NOT_OPEN", "error_id": "",
	}}
}

func TestAssign_SendsOnlyReviewersNotYetAssigned(t *testing.T) {
	e := newTestEnv(t)
	e.respondUsers(http.StatusOK)
	e.respondReview(http.StatusOK, reviewWith("open", userAda))
	bodies := e.respondReviewerPOST("assign", http.StatusOK, reviewWith("open", userAda, userGrace))

	stdout, stderr, err := e.exec(t, "assign", reviewA,
		"--reviewer", "ada@example.com", "--reviewer", "Grace Hopper", "--reviewer", userGrace)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"GET " + usersRoute, "GET " + reviewRoute(reviewA), "POST " + reviewerRoute("assign")}
	if got := e.mock.Requests(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("requests = %v, want %v", got, want)
	}
	if got, want := string((*bodies)[0]["reviewer_ids"]), `["`+userGrace+`"]`; got != want {
		t.Errorf("reviewer_ids = %s, want %s", got, want)
	}
	if want := "already assigned: Ada Lovelace\n"; stderr != want {
		t.Errorf("stderr = %q, want %q", stderr, want)
	}
	if want := "data-review/" + reviewA + " reviewers assigned (status: open)\n" +
		"Next: qctl describe data-review " + reviewA + "\n"; stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
}

func TestAssign_EveryoneAlreadyAssignedPostsNothing(t *testing.T) {
	e := newTestEnv(t)
	e.respondUsers(http.StatusOK)
	e.respondReview(http.StatusOK, reviewWith("changes_requested", userAda, userGrace))
	e.respondReviewerPOST("assign", http.StatusOK, reviewWith("changes_requested", userAda, userGrace))

	stdout, stderr, err := e.exec(t, "assign", reviewA, "--reviewer", "grace@example.com", "--reviewer", userAda)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := e.posts(); got != 0 {
		t.Errorf("POSTs = %d, want 0", got)
	}
	if want := "already assigned: Grace Hopper, Ada Lovelace\n"; stderr != want {
		t.Errorf("stderr = %q, want %q", stderr, want)
	}
	if want := "data-review/" + reviewA + " reviewers unchanged (status: changes_requested)\n"; !strings.HasPrefix(stdout, want) {
		t.Errorf("stdout = %q, want prefix %q", stdout, want)
	}
}

func TestAssign_ReportsReviewersDroppedByTheServer(t *testing.T) {
	cases := []struct {
		name      string
		response  map[string]any
		wantNames string
	}{
		{"all assigned", reviewWith("open", userGrace, userAda), ""},
		{"one dropped", reviewWith("open", userAda), "Grace Hopper"},
		{"all dropped", reviewWith("open"), "Grace Hopper, Ada Lovelace"},
		{"assignees omitted", aReview(nil), "Grace Hopper, Ada Lovelace"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestEnv(t)
			e.respondUsers(http.StatusOK)
			e.respondReview(http.StatusOK, reviewWith("open"))
			e.respondReviewerPOST("assign", http.StatusOK, tc.response)

			stdout, stderr, err := e.exec(t, "assign", reviewA,
				"--reviewer", "grace@example.com", "--reviewer", "Ada Lovelace", "--reviewer", userGrace)
			if code := qerrors.GetExitCode(err); code != 0 {
				t.Fatalf("exit = %d, want 0 (err: %v)", code, err)
			}
			want := ""
			if tc.wantNames != "" {
				want = "data-review/" + reviewA + " not assigned (not an active member of this organization): " + tc.wantNames + "\n"
			}
			if stderr != want {
				t.Errorf("stderr = %q, want %q", stderr, want)
			}
			if strings.Contains(stdout, "not assigned") {
				t.Errorf("warning leaked to stdout: %q", stdout)
			}
			if got := e.mock.Requests(); len(got) != 3 {
				t.Errorf("requests = %v, want one directory read, one review read and one POST", got)
			}
		})
	}
}

func TestAssign_DroppedUUIDReviewerNeedsNoDirectory(t *testing.T) {
	for _, format := range []string{"table", "json", "yaml", "name"} {
		t.Run(format, func(t *testing.T) {
			e := newTestEnv(t)
			e.respondUsers(http.StatusForbidden)
			e.respondReview(http.StatusOK, reviewWith("open", userAda))
			e.respondReviewerPOST("assign", http.StatusOK, reviewWith("open", userAda))

			stdout, stderr, err := e.exec(t, "assign", reviewA,
				"--reviewer", userGrace, "--reviewer", userGrace, "-o", format)
			if code := qerrors.GetExitCode(err); code != 0 {
				t.Fatalf("exit = %d, want 0 (err: %v)", code, err)
			}
			warning := "data-review/" + reviewA + " not assigned (not an active member of this organization): " + userGrace + "\n"
			if strings.Count(stderr, warning) != 1 || strings.Contains(stdout, "not assigned") {
				t.Errorf("want one stderr warning %q; stdout = %q, stderr = %q", warning, stdout, stderr)
			}
			want := []string{"GET " + reviewRoute(reviewA), "POST " + reviewerRoute("assign")}
			if got := e.mock.Requests(); strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Errorf("requests = %v, want %v (no directory sweep)", got, want)
			}
			if format == "json" && !json.Valid([]byte(stdout)) {
				t.Errorf("stdout is not one JSON document: %q", stdout)
			}
			if format == "name" && stdout != "data-review/"+reviewA+"\n" {
				t.Errorf("unexpected name output: %q", stdout)
			}
		})
	}
}

func TestReviewerVerbs_RefuseBadInputBeforeAnyRequest(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"assign without --reviewer", []string{"assign", reviewA}},
		{"unassign without --reviewer", []string{"unassign", reviewA}},
		{"assign with a short id", []string{"assign", "0190aaaa", "--reviewer", "ada@example.com"}},
		{"unassign with a short id", []string{"unassign", "0190aaaa", "--reviewer", "ada@example.com"}},
		{"re-request with a short id", []string{"re-request", "0190aaaa"}},
		{"assign without an id", []string{"assign", "--reviewer", "ada@example.com"}},
		{"re-request with two ids", []string{"re-request", reviewA, reviewB}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestEnv(t)
			e.respondUsers(http.StatusOK)

			_, _, err := e.exec(t, tc.args...)
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want 2 (err: %v)", got, err)
			}
			if got := e.mock.Requests(); len(got) != 0 {
				t.Errorf("requests = %v, want none", got)
			}
		})
	}
}

func TestReviewerVerbs_UnresolvableReviewerPostsNothing(t *testing.T) {
	for _, verb := range []string{"assign", "unassign", "re-request"} {
		t.Run(verb, func(t *testing.T) {
			e := newTestEnv(t)
			e.respondUsers(http.StatusOK)
			e.respondReview(http.StatusOK, reviewWith("open"))
			e.respondReviewerPOST(verb, http.StatusOK, reviewWith("open"))

			_, _, err := e.exec(t, verb, reviewA, "--reviewer", "nobody@example.com")
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want 2 (err: %v)", got, err)
			}
			if got := e.mock.Requests(); strings.Join(got, "\n") != "GET "+usersRoute {
				t.Errorf("requests = %v, want only the directory", got)
			}
		})
	}
}

func TestReviewerVerbs_NotActiveNamesTheStatus(t *testing.T) {
	cases := []struct {
		verb   string
		status int
		code   string
	}{
		{"assign", http.StatusUnprocessableEntity, "REVIEW_NOT_OPEN"},
		{"unassign", http.StatusUnprocessableEntity, "REVIEW_NOT_OPEN"},
		{"re-request", http.StatusConflict, "review_not_active"},
	}
	for _, tc := range cases {
		t.Run(tc.verb, func(t *testing.T) {
			e := newTestEnv(t)
			e.respondUsers(http.StatusOK)
			e.respondReview(http.StatusOK, reviewWith("open"))
			e.respondReviewerPOST(tc.verb, tc.status, notOpen(tc.code, "approved"))

			_, _, err := e.exec(t, tc.verb, reviewA, "--reviewer", "ada@example.com")
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want 2 (err: %v)", got, err)
			}
			// The one message check the ticket allows: the bespoke mapping
			// exists to put the server's status in front of the user.
			if err == nil || !strings.HasPrefix(err.Error(), "review is approved; ") {
				t.Errorf("err = %v, want it to lead with the review's status", err)
			}
		})
	}
}

func TestReviewerVerbs_NotOpenWithoutContextStillExitsTwo(t *testing.T) {
	cases := []struct {
		verb       string
		wantPrefix string
	}{
		// assign already read the review, so it can name the status.
		{"assign", "review is approved; "},
		{"unassign", "review is no longer active; "},
	}
	for _, tc := range cases {
		t.Run(tc.verb, func(t *testing.T) {
			e := newTestEnv(t)
			e.respondUsers(http.StatusOK)
			e.respondReview(http.StatusOK, reviewWith("approved"))
			e.respondReviewerPOST(tc.verb, http.StatusUnprocessableEntity, notOpenBare("approved"))

			_, _, err := e.exec(t, tc.verb, reviewA, "--reviewer", "ada@example.com")
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want 2 (err: %v)", got, err)
			}
			if err == nil || !strings.HasPrefix(err.Error(), tc.wantPrefix) {
				t.Errorf("err = %v, want prefix %q", err, tc.wantPrefix)
			}
		})
	}
}

func TestReviewerVerbs_ForbiddenExitsFour(t *testing.T) {
	forbidden := map[string]any{"detail": map[string]any{"msg": "Forbidden"}}
	for _, verb := range []string{"assign", "unassign", "re-request"} {
		t.Run(verb, func(t *testing.T) {
			e := newTestEnv(t)
			e.respondUsers(http.StatusOK)
			e.respondReview(http.StatusOK, reviewWith("open"))
			e.respondReviewerPOST(verb, http.StatusForbidden, forbidden)

			_, _, err := e.exec(t, verb, reviewA, "--reviewer", "ada@example.com")
			if got := qerrors.GetExitCode(err); got != qerrors.ExitUnauthorized {
				t.Errorf("exit code = %d, want 4 (err: %v)", got, err)
			}
		})
	}
}

// TestAssign_AlreadyAssignedOnAnInactiveReviewIsRefused: sending nothing
// skips the route's REVIEW_NOT_OPEN guard, so assign applies it itself rather
// than reporting "unchanged" on a review nobody can assign to.
func TestAssign_AlreadyAssignedOnAnInactiveReviewIsRefused(t *testing.T) {
	for _, status := range []string{"approved", "closed"} {
		t.Run(status, func(t *testing.T) {
			e := newTestEnv(t)
			e.respondUsers(http.StatusOK)
			e.respondReview(http.StatusOK, reviewWith(status, userAda))
			e.respondReviewerPOST("assign", http.StatusOK, reviewWith(status, userAda))

			stdout, _, err := e.exec(t, "assign", reviewA, "--reviewer", userAda)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit = %d (err %v), want %d", code, err, qerrors.ExitBadUsage)
			}
			if got := e.posts(); got != 0 {
				t.Errorf("POSTs = %d, want 0", got)
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want nothing", stdout)
			}
		})
	}
}

func TestAssign_ForbiddenReadExitsFour(t *testing.T) {
	e := newTestEnv(t)
	e.respondUsers(http.StatusOK)
	e.respondReview(http.StatusForbidden, map[string]any{"detail": map[string]any{"msg": "Forbidden"}})

	_, _, err := e.exec(t, "assign", reviewA, "--reviewer", "ada@example.com")
	if got := qerrors.GetExitCode(err); got != qerrors.ExitUnauthorized {
		t.Errorf("exit code = %d, want 4 (err: %v)", got, err)
	}
	if got := e.posts(); got != 0 {
		t.Errorf("POSTs = %d, want 0", got)
	}
}

func TestUnassign_PostsDirectlyAndReportsRetainedVerdicts(t *testing.T) {
	// A vet by someone never assigned: unassigned_vets lists it too, but this
	// call did not cause it.
	bystander := map[string]any{"history_id": 813, "decided_by_id": "0190c0c0-1111-4222-8333-444444444444", "decision": "approve"}
	cases := []struct {
		name       string
		vets       []map[string]any
		wantStderr string
	}{
		{"no retained verdicts", []map[string]any{}, ""},
		{"only someone else's verdicts", []map[string]any{bystander}, ""},
		{"retained verdicts", []map[string]any{
			{"history_id": 811, "decided_by_id": userAda, "decision": "reject"},
			{"history_id": 812, "decided_by_id": userAda, "decision": "approve"},
			// A row in two submissions can list one vet twice.
			{"history_id": 811, "decided_by_id": userAda, "decision": "reject"},
			bystander,
		}, "retained: 2 cell verdict(s) by the removed reviewer(s) still count on this row" +
			" — see qctl describe data-review " + reviewA + "\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestEnv(t)
			e.respondUsers(http.StatusOK)
			response := reviewWith("changes_requested", userGrace)
			response["unassigned_vets"] = tc.vets
			bodies := e.respondReviewerPOST("unassign", http.StatusOK, response)

			stdout, stderr, err := e.exec(t, "unassign", reviewA, "--reviewer", "ada@example.com")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			want := []string{"GET " + usersRoute, "POST " + reviewerRoute("unassign")}
			if got := e.mock.Requests(); strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Errorf("requests = %v, want %v", got, want)
			}
			if got, want := string((*bodies)[0]["reviewer_ids"]), `["`+userAda+`"]`; got != want {
				t.Errorf("reviewer_ids = %s, want %s", got, want)
			}
			if stderr != tc.wantStderr {
				t.Errorf("stderr = %q, want %q", stderr, tc.wantStderr)
			}
			if want := "data-review/" + reviewA + " reviewers unassigned (status: changes_requested)\n"; !strings.HasPrefix(stdout, want) {
				t.Errorf("stdout = %q, want prefix %q", stdout, want)
			}
		})
	}
}

func rerequested(status string, ids ...string) map[string]any {
	return map[string]any{"data_review_id": reviewA, "status": status, "rerequested_reviewer_ids": ids}
}

func TestRerequest_NobodyNotifiedIsASuccess(t *testing.T) {
	e := newTestEnv(t)
	bodies := e.respondReviewerPOST("re-request", http.StatusOK, rerequested("open"))

	stdout, _, err := e.exec(t, "re-request", reviewA)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := e.mock.Requests(), []string{"POST " + reviewerRoute("re-request")}; strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("requests = %v, want %v (no names to resolve or display)", got, want)
	}
	if got := len((*bodies)[0]); got != 0 {
		t.Errorf("body = %v, want {}", (*bodies)[0])
	}
	want := "data-review/" + reviewA + " re-requested: nobody — no undecided assignee can be notified (status: open)\n"
	if !strings.HasPrefix(stdout, want) {
		t.Errorf("stdout = %q, want prefix %q", stdout, want)
	}
}

func TestRerequest_NamedReviewersNobodyNotifiedSaysWhy(t *testing.T) {
	e := newTestEnv(t)
	e.respondUsers(http.StatusOK)
	e.respondReviewerPOST("re-request", http.StatusOK, rerequested("open"))

	stdout, _, err := e.exec(t, "re-request", reviewA, "--reviewer", "grace@example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "data-review/" + reviewA + " re-requested: nobody — none of the named reviewers is an assignee" +
		" who can be notified (status: open)\n"
	if !strings.HasPrefix(stdout, want) {
		t.Errorf("stdout = %q, want prefix %q", stdout, want)
	}
}

func TestRerequest_NamesWhoWasNotified(t *testing.T) {
	e := newTestEnv(t)
	e.respondUsers(http.StatusOK)
	bodies := e.respondReviewerPOST("re-request", http.StatusOK, rerequested("changes_requested", userGrace))

	stdout, _, err := e.exec(t, "re-request", reviewA, "--reviewer", "grace@example.com", "--reviewer", "Ada Lovelace")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"GET " + usersRoute, "POST " + reviewerRoute("re-request")}
	if got := e.mock.Requests(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("requests = %v, want %v (one directory sweep)", got, want)
	}
	if got, want := string((*bodies)[0]["reviewer_ids"]), `["`+userGrace+`","`+userAda+`"]`; got != want {
		t.Errorf("reviewer_ids = %s, want %s", got, want)
	}
	if want := "data-review/" + reviewA + " re-requested: Grace Hopper (status: changes_requested)\n"; !strings.HasPrefix(stdout, want) {
		t.Errorf("stdout = %q, want prefix %q", stdout, want)
	}
}

func TestRerequest_DirectoryFailureAfterTheNotificationStillSucceeds(t *testing.T) {
	e := newTestEnv(t)
	e.respondUsers(http.StatusForbidden)
	e.respondReviewerPOST("re-request", http.StatusOK, rerequested("open", userAda))

	stdout, stderr, err := e.exec(t, "re-request", reviewA)
	if err != nil {
		t.Fatalf("the notifications went out; err = %v", err)
	}
	want := []string{"POST " + reviewerRoute("re-request"), "GET " + usersRoute}
	if got := e.mock.Requests(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("requests = %v, want %v", got, want)
	}
	if !strings.Contains(stdout, "re-requested: "+userAda+" (status: open)") {
		t.Errorf("stdout = %q, want the raw UUID", stdout)
	}
	if !strings.HasPrefix(stderr, "Warning: user directory unavailable") {
		t.Errorf("stderr = %q, want the directory warning", stderr)
	}
}

func TestRerequest_NotActiveWithoutAStatusStillExitsTwo(t *testing.T) {
	e := newTestEnv(t)
	e.respondReviewerPOST("re-request", http.StatusConflict, map[string]any{"detail": map[string]any{
		"msg": "not active", "context": map[string]any{"code": "review_not_active"},
	}})

	_, _, err := e.exec(t, "re-request", reviewA)
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want 2 (err: %v)", got, err)
	}
}
