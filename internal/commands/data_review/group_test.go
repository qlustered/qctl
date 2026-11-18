package data_review //nolint:revive // Package name mirrors the hyphenated command.

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

func groupRoute(verb string) string {
	return "/api/orgs/" + testOrgID + "/data-reviews/groups/" + groupID + "/" + verb
}

// respondGroupPOST serves one of the request routes and records every raw
// body it receives; re-request and cancel send none, recorded as nil.
func (e *testEnv) respondGroupPOST(verb string, status int, body any) *[]map[string]json.RawMessage {
	bodies := &[]map[string]json.RawMessage{}
	e.mock.RegisterHandler("POST", groupRoute(verb), func(w http.ResponseWriter, r *http.Request) {
		var got map[string]json.RawMessage
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&got)
		}
		*bodies = append(*bodies, got)
		testutil.RespondJSON(w, status, body)
	})
	return bodies
}

func groupAssigned(ids ...string) map[string]any {
	return map[string]any{"review_group_id": groupID, "assigned_reviewer_ids": ids}
}

func groupRerequested(ids ...string) map[string]any {
	return map[string]any{"review_group_id": groupID, "rerequested_reviewer_ids": ids}
}

func groupCancelled(removed, closed []string) map[string]any {
	return map[string]any{"review_group_id": groupID, "removed_data_review_ids": removed, "closed_data_review_ids": closed}
}

// groupNotOpen is the request routes' real 422 for a request with no open
// review.
func groupNotOpen() map[string]any {
	return map[string]any{"detail": map[string]any{
		"msg": "This request has no open reviews.", "error_code": "REVIEW_NOT_OPEN", "error_id": "",
	}}
}

func joined(lines []string) string {
	return strings.Join(lines, "\n")
}

func TestGroupAssign_SendsTheResolvedReviewersAndListsTheAssignees(t *testing.T) {
	e := newTestEnv(t)
	e.respondUsers(http.StatusOK)
	bodies := e.respondGroupPOST("assign", http.StatusOK, groupAssigned(userAda, userGrace))

	stdout, stderr, err := e.execGroup(t, "assign", groupID, "--reviewer", "Grace Hopper")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"GET " + usersRoute, "POST " + groupRoute("assign")}
	if got := e.mock.Requests(); joined(got) != joined(want) {
		t.Errorf("requests = %v, want %v", got, want)
	}
	if got, want := string((*bodies)[0]["reviewer_ids"]), `["`+userGrace+`"]`; got != want {
		t.Errorf("reviewer_ids = %s, want %s", got, want)
	}
	if want := "data-review-group/" + groupID + " reviewers assigned (now: Ada Lovelace, Grace Hopper)\n"; stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want none", stderr)
	}
}

func TestGroupAssign_NamesReviewersTheServerDropped(t *testing.T) {
	e := newTestEnv(t)
	e.respondUsers(http.StatusOK)
	e.respondGroupPOST("assign", http.StatusOK, groupAssigned(userAda))

	// UUIDs resolve without a sweep, so the names cost one read after the POST.
	stdout, stderr, err := e.execGroup(t, "assign", groupID, "--reviewer", userAda, "--reviewer", userGrace)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"POST " + groupRoute("assign"), "GET " + usersRoute}
	if got := e.mock.Requests(); joined(got) != joined(want) {
		t.Errorf("requests = %v, want %v", got, want)
	}
	if want := "data-review-group/" + groupID +
		" not assigned (not an active member of this organization): Grace Hopper\n"; stderr != want {
		t.Errorf("stderr = %q, want %q", stderr, want)
	}
	if !strings.HasSuffix(stdout, "(now: Ada Lovelace)\n") {
		t.Errorf("stdout = %q, want the assignee list", stdout)
	}
}

func TestGroupAssign_StructuredOutputIsTheResponse(t *testing.T) {
	e := newTestEnv(t)
	e.respondGroupPOST("assign", http.StatusOK, groupAssigned(userAda))
	e.respondUsers(http.StatusOK)

	stdout, stderr, err := e.execGroup(t, "assign", groupID, "--reviewer", userAda, "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
	}
	if got["review_group_id"] != groupID {
		t.Errorf("review_group_id = %v, want %s", got["review_group_id"], groupID)
	}
	if !strings.Contains(stderr, "reviewers assigned (now: Ada Lovelace)") {
		t.Errorf("stderr = %q, want the result line", stderr)
	}

	e2 := newTestEnv(t)
	e2.respondGroupPOST("assign", http.StatusOK, groupAssigned(userAda))
	e2.respondUsers(http.StatusOK)
	stdout, _, err = e2.execGroup(t, "assign", groupID, "--reviewer", userAda, "-o", "name")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "data-review-group/" + groupID + "\n"; stdout != want {
		t.Errorf("-o name stdout = %q, want %q", stdout, want)
	}
}

func TestGroupVerbs_RefusalsKeepTheirExitCodes(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		body     map[string]any
		want     int
		wantText string
	}{
		{"no open review", http.StatusUnprocessableEntity, groupNotOpen(), qerrors.ExitBadUsage,
			"has no open or changes_requested review left"},
		{"forbidden", http.StatusForbidden, map[string]any{"detail": map[string]any{"msg": "Forbidden"}},
			qerrors.ExitUnauthorized, "requires DATA_REVIEW_EDIT"},
		{"not found", http.StatusNotFound, map[string]any{"detail": map[string]any{"msg": "Review request not found"}},
			qerrors.ExitNotFound, "Review request not found"},
	}
	verbs := map[string][]string{
		"assign":     {"assign", groupID, "--reviewer", userAda},
		"re-request": {"re-request", groupID},
		"cancel":     {"cancel", groupID, "--yes"},
	}
	for verb, args := range verbs {
		for _, tc := range cases {
			if verb == "cancel" && tc.status == http.StatusUnprocessableEntity {
				continue // cancel has no status guard
			}
			t.Run(verb+"/"+tc.name, func(t *testing.T) {
				e := newTestEnv(t)
				e.respondGroupPOST(verb, tc.status, tc.body)

				_, _, err := e.execGroup(t, args...)
				if got := qerrors.GetExitCode(err); got != tc.want {
					t.Errorf("exit code = %d, want %d (err: %v)", got, tc.want, err)
				}
				if err == nil || !strings.Contains(err.Error(), tc.wantText) {
					t.Errorf("err = %v, want it to mention %q", err, tc.wantText)
				}
			})
		}
	}
}

func TestGroupRerequest_NamesWhoWasNotified(t *testing.T) {
	e := newTestEnv(t)
	e.respondUsers(http.StatusOK)
	bodies := e.respondGroupPOST("re-request", http.StatusOK, groupRerequested(userGrace))

	stdout, _, err := e.execGroup(t, "re-request", groupID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"POST " + groupRoute("re-request"), "GET " + usersRoute}
	if got := e.mock.Requests(); joined(got) != joined(want) {
		t.Errorf("requests = %v, want %v", got, want)
	}
	if got := len((*bodies)[0]); got != 0 {
		t.Errorf("body = %v, want none", (*bodies)[0])
	}
	if want := "data-review-group/" + groupID + " re-requested: Grace Hopper\n"; stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
}

func TestGroupRerequest_NobodyNotifiedIsASuccessWithoutADirectoryRead(t *testing.T) {
	e := newTestEnv(t)
	e.respondGroupPOST("re-request", http.StatusOK, groupRerequested())

	stdout, _, err := e.execGroup(t, "re-request", groupID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := e.mock.Requests(); len(got) != 1 {
		t.Errorf("requests = %v, want the POST alone", got)
	}
	if want := "data-review-group/" + groupID + " re-requested: nobody"; !strings.HasPrefix(stdout, want) {
		t.Errorf("stdout = %q, want prefix %q", stdout, want)
	}
}

func TestGroupCancel_NonInteractiveWithoutYesSendsNothing(t *testing.T) {
	requireNonInteractive(t)
	e := newTestEnv(t)
	e.respondGroupPOST("cancel", http.StatusOK, groupCancelled(nil, nil))

	_, _, err := e.execGroup(t, "cancel", groupID)
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want 2 (err: %v)", got, err)
	}
	if got := e.mock.Requests(); len(got) != 0 {
		t.Errorf("requests = %v, want none", got)
	}
}

func TestGroupCancel_ReportsRemovedVersusClosed(t *testing.T) {
	e := newTestEnv(t)
	e.respondGroupPOST("cancel", http.StatusOK,
		groupCancelled([]string{reviewA, reviewB, reviewC}, []string{reviewA, reviewC}))

	stdout, _, err := e.execGroup(t, "cancel", groupID, "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := e.mock.Requests(), []string{"POST " + groupRoute("cancel")}; joined(got) != joined(want) {
		t.Errorf("requests = %v, want %v", got, want)
	}
	want := joined([]string{
		"data-review-group/" + groupID + " cancelled: 3 review(s) removed, 2 closed",
		"data-review/" + reviewA + " closed",
		"data-review/" + reviewB + " not closed (in another request, or no longer open)",
		"data-review/" + reviewC + " closed",
	}) + "\n"
	if stdout != want {
		t.Errorf("stdout =\n%s\nwant\n%s", stdout, want)
	}
}

func TestGroupCancel_RepeatIsANoOpThatSucceeds(t *testing.T) {
	e := newTestEnv(t)
	e.respondGroupPOST("cancel", http.StatusOK, groupCancelled([]string{}, []string{}))

	stdout, _, err := e.execGroup(t, "cancel", groupID, "--yes")
	if got := qerrors.GetExitCode(err); got != qerrors.ExitSuccess {
		t.Fatalf("exit code = %d, want 0 (err: %v)", got, err)
	}
	if want := "data-review-group/" + groupID + " cancelled: it had no reviews left, so nothing changed\n"; stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
}

func TestGroupCancel_StructuredOutputIsTheResponse(t *testing.T) {
	e := newTestEnv(t)
	e.respondGroupPOST("cancel", http.StatusOK, groupCancelled([]string{reviewA}, []string{reviewA}))

	stdout, stderr, err := e.execGroup(t, "cancel", groupID, "--yes", "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got struct {
		Removed []string `json:"removed_data_review_ids"`
		Closed  []string `json:"closed_data_review_ids"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
	}
	if len(got.Removed) != 1 || len(got.Closed) != 1 {
		t.Errorf("response = %+v, want one removed and one closed", got)
	}
	if !strings.Contains(stderr, "data-review/"+reviewA+" closed") {
		t.Errorf("stderr = %q, want the result lines", stderr)
	}
}

func TestGroupCancel_PromptDeclineSendsNothing(t *testing.T) {
	e := newTestEnv(t)
	e.respondGroupPOST("cancel", http.StatusOK, groupCancelled(nil, nil))
	cmdutil.SetReader(strings.NewReader("n\n"))
	defer cmdutil.ResetReader()

	_, _, err := e.execGroup(t, "cancel", groupID)
	if !errors.Is(err, cmdutil.ErrDeclined) {
		t.Errorf("err = %v, want ErrDeclined", err)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitGenericError {
		t.Errorf("exit code = %d, want 1", got)
	}
	if got := e.mock.Requests(); len(got) != 0 {
		t.Errorf("requests = %v, want none", got)
	}
}

func TestGroupCancel_PromptListsTheConsequencesThenAsks(t *testing.T) {
	want := joined([]string{
		"Its rows stay in quarantine — cancelling approves and releases nothing.",
		"Its open reviews are closed and leave the review queue. Nobody is notified.",
		"A review that is also in another request stays open there, with its decisions.",
		"Sending the rows again starts a new request.",
		"This can't be undone.",
		"Cancel request " + groupID + "?",
	})
	if got := cancelPrompt(groupID); got != want {
		t.Errorf("prompt =\n%s\nwant\n%s", got, want)
	}
}

func TestGroupVerbs_TakeAKindReference(t *testing.T) {
	e := newTestEnv(t)
	e.respondGroupPOST("re-request", http.StatusOK, groupRerequested())

	if _, _, err := e.execGroup(t, "re-request", "data-review-group/"+groupID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := e.mock.Requests(); len(got) != 1 {
		t.Errorf("requests = %v, want the POST", got)
	}
}

func TestGroupVerbs_MalformedIDSendsNothing(t *testing.T) {
	for _, arg := range []string{"11111111", "data-review/" + reviewA} {
		t.Run(arg, func(t *testing.T) {
			e := newTestEnv(t)
			_, _, err := e.execGroup(t, "cancel", arg, "--yes")
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want 2 (err: %v)", got, err)
			}
			if got := e.mock.Requests(); len(got) != 0 {
				t.Errorf("requests = %v, want none", got)
			}
		})
	}
}
