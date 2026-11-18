package data_review //nolint:revive // Package name mirrors the hyphenated command.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"gopkg.in/yaml.v3"
)

const (
	reviewA = "0190aaaa-1111-4222-8333-444444444444"
	reviewB = "0190bbbb-1111-4222-8333-444444444444"
	reviewC = "0190cccc-1111-4222-8333-444444444444"
	groupID = "11111111-2222-4333-8444-555555555555"
)

func bulkRoute(verb string) string {
	return "/api/orgs/" + testOrgID + "/data-reviews/" + verb
}

func processed(id, status string, approvals, required, released int) map[string]any {
	rows := make([]int, released)
	for i := range rows {
		rows[i] = 100 + i
	}
	return map[string]any{
		"data_review_id": id, "status": status,
		"current_approvals": approvals, "required_approvals": required,
		"released_row_ids": rows,
	}
}

func skipped(id, code, reason string) map[string]any {
	return map[string]any{"data_review_id": id, "error_code": code, "reason": reason}
}

func batchResult(p []map[string]any, s []map[string]any) map[string]any {
	if p == nil {
		p = []map[string]any{}
	}
	if s == nil {
		s = []map[string]any{}
	}
	return map[string]any{"processed": p, "skipped": s}
}

// respondBatch serves one bulk route and records every raw body it receives.
func (e *testEnv) respondBatch(verb string, status int, body any) *[]map[string]json.RawMessage {
	bodies := &[]map[string]json.RawMessage{}
	e.mock.RegisterHandler("POST", bulkRoute(verb), func(w http.ResponseWriter, r *http.Request) {
		var got map[string]json.RawMessage
		_ = json.NewDecoder(r.Body).Decode(&got)
		*bodies = append(*bodies, got)
		testutil.RespondJSON(w, status, body)
	})
	return bodies
}

// requireNonInteractive skips when the test binary's stdin is a terminal:
// the gate under test is the one a script hits.
func requireNonInteractive(t *testing.T) {
	t.Helper()
	if cmdutil.CanPrompt() {
		t.Skip("stdin is interactive; the non-interactive gate cannot be exercised")
	}
}

func TestBulkDecision_ExitCodeFollowsThePartition(t *testing.T) {
	cases := []struct {
		name     string
		verb     string
		result   map[string]any
		wantExit int
	}{
		{"all processed", "approve", batchResult([]map[string]any{
			processed(reviewA, "approved", 1, 1, 1), processed(reviewB, "open", 0, 2, 0),
		}, nil), qerrors.ExitSuccess},
		{"single NOT_FOUND", "approve", batchResult(nil, []map[string]any{
			skipped(reviewA, "NOT_FOUND", "DataReview "+reviewA+" not found"),
		}), qerrors.ExitNotFound},
		{"single APPROVER_NOT_DESIGNATED", "approve", batchResult(nil, []map[string]any{
			skipped(reviewA, "APPROVER_NOT_DESIGNATED", "Reviewer(s) not in the designated reviewer set"),
		}), qerrors.ExitUnauthorized},
		{"two VIEW_ONLY", "approve", batchResult(nil, []map[string]any{
			skipped(reviewA, "VIEW_ONLY", "You have view-only access to table 7. Ask a table owner for edit access."),
			skipped(reviewB, "VIEW_ONLY", "You have view-only access to table 7. Ask a table owner for edit access."),
		}), qerrors.ExitUnauthorized},
		{"single VIEW_ONLY on withdraw", "withdraw", batchResult(nil, []map[string]any{
			skipped(reviewA, "VIEW_ONLY", "You have view-only access to table 7. Ask a table owner for edit access."),
		}), qerrors.ExitUnauthorized},
		{"VIEW_ONLY beside a processed review", "approve", batchResult(
			[]map[string]any{processed(reviewA, "open", 1, 2, 0)},
			[]map[string]any{skipped(reviewB, "VIEW_ONLY", "You have view-only access to table 7. Ask a table owner for edit access.")},
		), qerrors.ExitGenericError},
		{"two REVIEW_NOT_OPEN", "withdraw", batchResult(nil, []map[string]any{
			skipped(reviewA, "REVIEW_NOT_OPEN", "DataReview is not open for decision withdrawal (status: closed)"),
			skipped(reviewB, "REVIEW_NOT_OPEN", "You have no standing decision to withdraw on this review."),
		}), qerrors.ExitBadUsage},
		{"one processed one skipped", "approve", batchResult(
			[]map[string]any{processed(reviewA, "open", 1, 2, 0)},
			[]map[string]any{skipped(reviewB, "REVIEW_NOT_OPEN", "DataReview is not open (status: approved)")},
		), qerrors.ExitGenericError},
		{"two skipped with different codes", "approve", batchResult(nil, []map[string]any{
			skipped(reviewA, "NOT_FOUND", "DataReview "+reviewA+" not found"),
			skipped(reviewB, "REVIEW_NOT_OPEN", "DataReview is not open (status: closed)"),
		}), qerrors.ExitGenericError},
		{"unknown shared code", "approve", batchResult(nil, []map[string]any{
			skipped(reviewA, "SOMETHING_NEW", "a reason from a newer backend"),
		}), qerrors.ExitGenericError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestEnv(t)
			e.respondBatch(tc.verb, http.StatusOK, tc.result)

			_, _, err := e.exec(t, tc.verb, reviewA, reviewB)
			if got := qerrors.GetExitCode(err); got != tc.wantExit {
				t.Errorf("exit code = %d, want %d (err: %v)", got, tc.wantExit, err)
			}
		})
	}
}

func TestBulkDecision_PartialNamesTheSplit(t *testing.T) {
	e := newTestEnv(t)
	e.respondBatch("approve", http.StatusOK, batchResult(
		[]map[string]any{processed(reviewA, "open", 1, 2, 0), processed(reviewC, "open", 1, 2, 0)},
		[]map[string]any{skipped(reviewB, "REVIEW_NOT_OPEN", "DataReview is not open (status: closed)")},
	))

	_, _, err := e.exec(t, "approve", reviewA, reviewB, reviewC)
	if err == nil || err.Error() != "1 of 3 review(s) skipped; the other 2 processed" {
		t.Errorf("err = %v, want the split", err)
	}
}

func TestBulkDecision_SharedSkipQuotesDistinctReasonsCapped(t *testing.T) {
	e := newTestEnv(t)
	var skips []map[string]any
	for i := range 5 {
		id := fmt.Sprintf("0190dddd-1111-4222-8333-%012d", i)
		skips = append(skips, skipped(id, "NOT_FOUND", "DataReview "+id+" not found"))
	}
	skips = append(skips, skips[0]) // a repeated reason is quoted once
	e.respondBatch("approve", http.StatusOK, batchResult(nil, skips))

	_, _, err := e.exec(t, "approve", reviewA)
	if got := qerrors.GetExitCode(err); got != qerrors.ExitNotFound {
		t.Fatalf("exit code = %d, want 3 (%v)", got, err)
	}
	first := strings.SplitN(err.Error(), "\n", 2)[0]
	if strings.Count(first, "not found") != 3 || !strings.HasSuffix(first, "; and 2 more") {
		t.Errorf("first line = %q, want three reasons and a remainder count", first)
	}
}

func TestBulkDecision_BodyCarriesExactlyOneSelector(t *testing.T) {
	cases := []struct {
		name        string
		verb        string
		args        []string
		wantKeys    []string
		wantAbsent  []string
		wantComment string // raw JSON; "" = key must be absent
	}{
		{"approve ids, no comment", "approve", []string{reviewA},
			[]string{"data_review_ids"}, []string{"review_group_id"}, "null"},
		{"approve group with comment", "approve", []string{"--group", groupID, "--yes", "--comment", "spot-checked"},
			[]string{"review_group_id"}, []string{"data_review_ids"}, `"spot-checked"`},
		{"withdraw ids", "withdraw", []string{reviewA},
			[]string{"data_review_ids"}, []string{"review_group_id", "comment"}, ""},
		{"withdraw group", "withdraw", []string{"--group", groupID, "--yes"},
			[]string{"review_group_id"}, []string{"data_review_ids", "comment"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestEnv(t)
			bodies := e.respondBatch(tc.verb, http.StatusOK, batchResult(nil, nil))

			if _, _, err := e.exec(t, append([]string{tc.verb}, tc.args...)...); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(*bodies) != 1 {
				t.Fatalf("requests = %d, want 1", len(*bodies))
			}
			body := (*bodies)[0]
			for _, key := range tc.wantKeys {
				if _, ok := body[key]; !ok {
					t.Errorf("body %v lacks %q", body, key)
				}
			}
			for _, key := range tc.wantAbsent {
				if _, ok := body[key]; ok {
					t.Errorf("body %v carries %q", body, key)
				}
			}
			if tc.wantComment != "" && string(body["comment"]) != tc.wantComment {
				t.Errorf("comment = %s, want %s", body["comment"], tc.wantComment)
			}
			if raw, ok := body["review_group_id"]; ok && string(raw) != `"`+groupID+`"` {
				t.Errorf("review_group_id = %s, want %s", raw, groupID)
			}
		})
	}
}

func TestBulkDecision_WithdrawHasNoCommentFlag(t *testing.T) {
	e := newTestEnv(t)
	_, _, err := e.exec(t, "withdraw", reviewA, "--comment", "why")
	if err == nil {
		t.Fatal("withdraw accepted --comment")
	}
	if got := len(e.mock.Requests()); got != 0 {
		t.Errorf("requests = %d, want 0", got)
	}
}

func TestBulkDecision_IDsAreDedupedInOrder(t *testing.T) {
	e := newTestEnv(t)
	bodies := e.respondBatch("approve", http.StatusOK, batchResult(nil, nil))

	if _, _, err := e.exec(t, "approve", reviewB, reviewA, strings.ToUpper(reviewB), reviewC, reviewA); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var ids []string
	if err := json.Unmarshal((*bodies)[0]["data_review_ids"], &ids); err != nil {
		t.Fatalf("data_review_ids: %v", err)
	}
	if want := []string{reviewB, reviewA, reviewC}; strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Errorf("data_review_ids = %v, want %v", ids, want)
	}
}

func TestBulkDecision_LocalRefusalsCostNoRequest(t *testing.T) {
	tooMany := make([]string, 0, maxBatchReviewIDs+1)
	for i := range maxBatchReviewIDs + 1 {
		tooMany = append(tooMany, fmt.Sprintf("0190eeee-1111-4222-8333-%012d", i))
	}
	cases := []struct {
		name string
		args []string
	}{
		{"501 ids", tooMany},
		{"both selectors", []string{reviewA, "--group", groupID, "--yes"}},
		{"neither selector", nil},
		{"malformed id", []string{reviewA, "not-a-uuid"}},
		{"malformed group", []string{"--group", "not-a-uuid", "--yes"}},
		{"empty group", []string{"--group", "", "--yes"}},
	}
	for _, verb := range []string{"approve", "withdraw"} {
		for _, tc := range cases {
			t.Run(verb+"/"+tc.name, func(t *testing.T) {
				e := newTestEnv(t)
				_, _, err := e.exec(t, append([]string{verb}, tc.args...)...)
				if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
					t.Errorf("exit code = %d, want 2 (%v)", got, err)
				}
				if got := len(e.mock.Requests()); got != 0 {
					t.Errorf("requests = %d, want 0: %v", got, e.mock.Requests())
				}
			})
		}
	}
}

func TestBulkDecision_ExactlyTheCapIsAccepted(t *testing.T) {
	e := newTestEnv(t)
	bodies := e.respondBatch("approve", http.StatusOK, batchResult(nil, nil))
	args := []string{"approve"}
	for i := range maxBatchReviewIDs {
		args = append(args, fmt.Sprintf("0190eeee-1111-4222-8333-%012d", i))
	}
	if _, _, err := e.exec(t, args...); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(*bodies) != 1 {
		t.Errorf("requests = %d, want 1", len(*bodies))
	}
}

func TestBulkDecision_GroupWithoutYesIsBadUsageWhenNonInteractive(t *testing.T) {
	requireNonInteractive(t)
	e := newTestEnv(t)
	e.respondBatch("approve", http.StatusOK, batchResult(nil, nil))

	_, _, err := e.exec(t, "approve", "--group", groupID)
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want 2 (%v)", got, err)
	}
	if got := len(e.mock.Requests()); got != 0 {
		t.Errorf("requests = %d, want 0", got)
	}
}

func TestBulkDecision_GroupWithYesPostsOnce(t *testing.T) {
	requireNonInteractive(t)
	e := newTestEnv(t)
	bodies := e.respondBatch("withdraw", http.StatusOK, batchResult(
		[]map[string]any{processed(reviewA, "open", 0, 1, 0)}, nil))

	stdout, _, err := e.exec(t, "withdraw", "--group", groupID, "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := len(e.mock.Requests()); got != 1 || len(*bodies) != 1 {
		t.Errorf("requests = %d, want exactly one POST", got)
	}
	if strings.Contains(stdout, "Next:") {
		t.Errorf("stdout = %q, a --group call names no single review to describe", stdout)
	}
}

func TestBulkDecision_ExplicitIDsNeverPrompt(t *testing.T) {
	requireNonInteractive(t)
	e := newTestEnv(t)
	e.respondBatch("withdraw", http.StatusOK, batchResult(
		[]map[string]any{processed(reviewA, "open", 0, 1, 0)}, nil))

	if _, _, err := e.exec(t, "withdraw", reviewA); err != nil {
		t.Fatalf("an id list hit a confirmation gate: %v", err)
	}
}

func TestBulkDecision_GroupPromptDeclineSendsNothing(t *testing.T) {
	e := newTestEnv(t)
	e.respondBatch("approve", http.StatusOK, batchResult(nil, nil))
	cmdutil.SetReader(strings.NewReader("n\n"))
	defer cmdutil.ResetReader()

	_, _, err := e.exec(t, "approve", "--group", groupID)
	if !errors.Is(err, cmdutil.ErrDeclined) {
		t.Errorf("err = %v, want ErrDeclined", err)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitGenericError {
		t.Errorf("exit code = %d, want 1", got)
	}
	if got := len(e.mock.Requests()); got != 0 {
		t.Errorf("requests = %d, want 0", got)
	}
}

func TestBulkDecision_GroupPromptAcceptPostsOnce(t *testing.T) {
	e := newTestEnv(t)
	bodies := e.respondBatch("approve", http.StatusOK, batchResult(nil, nil))
	cmdutil.SetReader(strings.NewReader("y\n"))
	defer cmdutil.ResetReader()

	_, stderr, err := e.exec(t, "approve", "--group", groupID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(*bodies) != 1 {
		t.Errorf("requests = %d, want 1", len(*bodies))
	}
	if want := groupPrompt(bulkDecision{verb: "approve", ask: "Approve"}, groupID) + " [y/N]: " +
		"submission " + groupID + " has no open reviews; nothing to approve\n"; stderr != want {
		t.Errorf("stderr = %q, want %q", stderr, want)
	}
}

func TestBulkDecision_GroupPromptNamesTheSubmissionAndPreview(t *testing.T) {
	d := bulkDecision{verb: "withdraw", ask: "Withdraw your decision on"}
	lines := strings.Split(groupPrompt(d, groupID), "\n")
	if len(lines) != 3 ||
		lines[0] != "Withdraw your decision on every open or changes_requested review in submission "+groupID+" (up to 500 rows)?" ||
		!strings.HasPrefix(lines[1], "Preview: qctl get data-reviews --table <name> --group "+groupID) {
		t.Errorf("prompt lines = %q", lines)
	}
}

func TestBulkDecision_DefaultOutputLines(t *testing.T) {
	e := newTestEnv(t)
	e.respondBatch("approve", http.StatusOK, batchResult(
		[]map[string]any{processed(reviewA, "approved", 2, 2, 1)},
		[]map[string]any{skipped(reviewB, "REVIEW_NOT_OPEN", "DataReview is not open (status: closed)")},
	))

	stdout, _, err := e.exec(t, "approve", reviewA, reviewB)
	if got := qerrors.GetExitCode(err); got != qerrors.ExitGenericError {
		t.Errorf("exit code = %d, want 1", got)
	}
	want := "data-review/" + reviewA + " approved (status: approved, approvals: 2/2, released: 1 row(s))\n" +
		"data-review/" + reviewB + " skipped: DataReview is not open (status: closed) [REVIEW_NOT_OPEN]\n"
	if stdout != want {
		t.Errorf("stdout = %q\nwant   = %q", stdout, want)
	}
}

func TestBulkDecision_SingleProcessedIDEndsWithNext(t *testing.T) {
	cases := []struct{ verb, label, status string }{
		{"approve", "approved", "open"},
		{"withdraw", "decision withdrawn", "open"},
	}
	for _, tc := range cases {
		t.Run(tc.verb, func(t *testing.T) {
			e := newTestEnv(t)
			e.respondBatch(tc.verb, http.StatusOK, batchResult(
				[]map[string]any{processed(reviewA, tc.status, 0, 1, 0)}, nil))

			stdout, _, err := e.exec(t, tc.verb, reviewA, reviewA)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			want := "data-review/" + reviewA + " " + tc.label + " (status: " + tc.status +
				", approvals: 0/1, released: 0 row(s))\n" +
				"Next: qctl describe data-review " + reviewA + "\n"
			if stdout != want {
				t.Errorf("stdout = %q\nwant   = %q", stdout, want)
			}
		})
	}
}

func TestBulkDecision_HeldWarningOnlyForProcessedChangesRequestedApprovals(t *testing.T) {
	result := batchResult(
		[]map[string]any{
			processed(reviewA, "changes_requested", 1, 1, 0),
			processed(reviewB, "open", 1, 2, 0),
			processed(reviewC, "approved", 1, 1, 1),
		},
		nil,
	)

	t.Run("approve", func(t *testing.T) {
		e := newTestEnv(t)
		e.respondBatch("approve", http.StatusOK, result)
		_, stderr, err := e.exec(t, "approve", reviewA, reviewB, reviewC)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "warning: data-review/" + reviewA + " approval recorded, but the row is still held" +
			" — a cell needs a fix or the row was rejected\n"
		if stderr != want {
			t.Errorf("stderr = %q\nwant   = %q", stderr, want)
		}
	})

	t.Run("approve skip is not held", func(t *testing.T) {
		e := newTestEnv(t)
		e.respondBatch("approve", http.StatusOK, batchResult(nil, []map[string]any{
			skipped(reviewA, "REVIEW_NOT_OPEN", "DataReview is not open (status: changes_requested)"),
		}))
		_, stderr, _ := e.exec(t, "approve", reviewA)
		if stderr != "" {
			t.Errorf("stderr = %q, want empty", stderr)
		}
	})

	t.Run("withdraw", func(t *testing.T) {
		e := newTestEnv(t)
		e.respondBatch("withdraw", http.StatusOK, result)
		_, stderr, err := e.exec(t, "withdraw", reviewA, reviewB, reviewC)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if stderr != "" {
			t.Errorf("stderr = %q, want empty", stderr)
		}
	})
}

func TestBulkDecision_StructuredOutputIsTheRawResult(t *testing.T) {
	result := batchResult(
		[]map[string]any{processed(reviewA, "changes_requested", 1, 1, 0)},
		[]map[string]any{skipped(reviewB, "NOT_FOUND", "DataReview "+reviewB+" not found")},
	)
	for _, format := range []string{"json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			e := newTestEnv(t)
			e.respondBatch("approve", http.StatusOK, result)

			stdout, stderr, err := e.exec(t, "approve", reviewA, reviewB, "-o", format)
			if got := qerrors.GetExitCode(err); got != qerrors.ExitGenericError {
				t.Errorf("exit code = %d, want 1: a partial stays non-zero under -o %s", got, format)
			}
			var got map[string][]map[string]any
			if format == "json" {
				err = json.Unmarshal([]byte(stdout), &got)
			} else {
				err = yaml.Unmarshal([]byte(stdout), &got)
			}
			if err != nil {
				t.Fatalf("stdout is not %s: %v\n%s", format, err, stdout)
			}
			if len(got["processed"]) != 1 || got["processed"][0]["data_review_id"] != reviewA ||
				len(got["skipped"]) != 1 || got["skipped"][0]["error_code"] != "NOT_FOUND" {
				t.Errorf("payload = %#v", got)
			}
			if stderr != "" {
				t.Errorf("stderr = %q, want empty under -o %s", stderr, format)
			}
		})
	}
}

func TestBulkDecision_ForbiddenExplainsThePermission(t *testing.T) {
	for _, verb := range []string{"approve", "withdraw"} {
		t.Run(verb, func(t *testing.T) {
			e := newTestEnv(t)
			e.respondBatch(verb, http.StatusForbidden,
				map[string]any{"detail": map[string]any{"msg": "permission denied"}})

			_, _, err := e.exec(t, verb, reviewA)
			if got := qerrors.GetExitCode(err); got != qerrors.ExitUnauthorized {
				t.Errorf("exit code = %d, want 4 (%v)", got, err)
			}
			if err == nil || !strings.Contains(err.Error(), "DATA_REVIEW_APPROVE") {
				t.Errorf("err = %v, want the permission named", err)
			}
		})
	}
}

func TestBulkDecision_ServerRefusalKeepsItsExitCode(t *testing.T) {
	e := newTestEnv(t)
	e.respondBatch("approve", http.StatusUnprocessableEntity, map[string]any{"detail": map[string]any{
		"msg": "review_group_id selects 612 open reviews; the bulk decision cap is 500."}})

	_, _, err := e.exec(t, "approve", "--group", groupID, "--yes")
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want 2 (%v)", got, err)
	}
}
