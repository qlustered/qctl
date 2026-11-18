package data_review //nolint:revive // Package name mirrors the hyphenated command.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"gopkg.in/yaml.v3"
)

const (
	amountHistoryID = 811
	emailHistoryID  = 812
)

var cellVerbs = []string{"accept-cell", "clear-cell", "withdraw-cell"}

func reviewRoute(id string) string {
	return "/api/orgs/" + testOrgID + "/data-reviews/" + id
}

func actionRoute(id string, historyID int) string {
	return fmt.Sprintf("%s/actions/%d", reviewRoute(id), historyID)
}

func aChange(field string, historyID int) map[string]any {
	return map[string]any{"field_name": field, "history_id": historyID, "edited_value": "x"}
}

// aReview is reviewA's detail response; changes == nil leaves the key out,
// as the backend does for a GET without include=changes.
func aReview(changes []map[string]any) map[string]any {
	r := map[string]any{
		"id": reviewA, "dataset_id": 42, "row_id": 1001, "status": "open", "required_approvals": 1,
		"created_at": "2026-09-01T00:00:00Z", "updated_at": "2026-09-01T00:00:00Z",
	}
	if changes != nil {
		r["changes"] = changes
	}
	return r
}

func anActionResult(decision string, statuses map[string]string, affected []string, rows, released []int) map[string]any {
	if released == nil {
		released = []int{}
	}
	return map[string]any{
		"ledger_id":                "0190ffff-1111-4222-8333-444444444444",
		"decision":                 decision,
		"affected_data_review_ids": affected,
		"affected_row_ids":         rows,
		"released_row_ids":         released,
		"affected_review_statuses": statuses,
	}
}

func ownResult(decision, status string) map[string]any {
	return anActionResult(decision, map[string]string{reviewA: status}, []string{reviewA}, []int{1001}, nil)
}

// respondReview serves reviewA's detail GET and records each query's
// include value.
func (e *testEnv) respondReview(status int, body any) *[]string {
	includes := &[]string{}
	e.mock.RegisterHandler("GET", reviewRoute(reviewA), func(w http.ResponseWriter, r *http.Request) {
		*includes = append(*includes, r.URL.Query().Get("include"))
		testutil.RespondJSON(w, status, body)
	})
	return includes
}

// respondAction serves one of reviewA's record-action routes and records every
// raw body it receives.
func (e *testEnv) respondAction(historyID, status int, body any) *[]map[string]json.RawMessage {
	bodies := &[]map[string]json.RawMessage{}
	e.mock.RegisterHandler("POST", actionRoute(reviewA, historyID), func(w http.ResponseWriter, r *http.Request) {
		var got map[string]json.RawMessage
		_ = json.NewDecoder(r.Body).Decode(&got)
		*bodies = append(*bodies, got)
		testutil.RespondJSON(w, status, body)
	})
	return bodies
}

func (e *testEnv) posts() int {
	n := 0
	for _, r := range e.mock.Requests() {
		if strings.HasPrefix(r, "POST ") {
			n++
		}
	}
	return n
}

func TestCellVerdict_FieldResolvesThroughOneGet(t *testing.T) {
	e := newTestEnv(t)
	includes := e.respondReview(http.StatusOK, aReview([]map[string]any{
		aChange("amount", amountHistoryID), aChange("email", emailHistoryID),
	}))
	bodies := e.respondAction(emailHistoryID, http.StatusOK, ownResult("comment_only", "open"))

	if _, _, err := e.exec(t, "clear-cell", reviewA, "--field", "email"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"GET " + reviewRoute(reviewA), "POST " + actionRoute(reviewA, emailHistoryID)}
	if got := e.mock.Requests(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("requests = %v, want %v", got, want)
	}
	if len(*includes) != 1 || (*includes)[0] != "changes" {
		t.Errorf("include = %v, want [changes]", *includes)
	}
	if len(*bodies) != 1 {
		t.Errorf("POST bodies = %d, want 1", len(*bodies))
	}
}

func TestCellVerdict_HistoryIDSkipsTheGet(t *testing.T) {
	e := newTestEnv(t)
	e.respondAction(amountHistoryID, http.StatusOK, ownResult("approve", "approved"))

	if _, _, err := e.exec(t, "accept-cell", reviewA, "--history-id", "811"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"POST " + actionRoute(reviewA, amountHistoryID)}
	if got := e.mock.Requests(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("requests = %v, want %v", got, want)
	}
}

func TestCellVerdict_FieldMatchIsExact(t *testing.T) {
	for _, field := range []string{"Amount", "amoun", "amount "} {
		t.Run(field, func(t *testing.T) {
			e := newTestEnv(t)
			e.respondReview(http.StatusOK, aReview([]map[string]any{aChange("amount", amountHistoryID)}))
			e.respondAction(amountHistoryID, http.StatusOK, ownResult("approve", "approved"))

			_, _, err := e.exec(t, "accept-cell", reviewA, "--field", field)
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want 2 (%v)", got, err)
			}
			if got := e.posts(); got != 0 {
				t.Errorf("POSTs = %d, want 0", got)
			}
		})
	}
}

func TestCellVerdict_EmptyChangeSetPointsAtHistoryID(t *testing.T) {
	cases := map[string][]map[string]any{
		"empty list":  {},
		"absent list": nil,
	}
	for name, changes := range cases {
		t.Run(name, func(t *testing.T) {
			e := newTestEnv(t)
			e.respondReview(http.StatusOK, aReview(changes))

			_, _, err := e.exec(t, "clear-cell", reviewA, "--field", "amount")
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want 2 (%v)", got, err)
			}
			if err == nil || !strings.Contains(err.Error(), "--history-id") {
				t.Errorf("err = %v, want the --history-id hint", err)
			}
			if got := e.posts(); got != 0 {
				t.Errorf("POSTs = %d, want 0", got)
			}
		})
	}
}

func TestCellVerdict_BodyPerVerb(t *testing.T) {
	cases := []struct {
		verb     string
		extra    []string
		decision string
		want     string
	}{
		{"accept-cell", nil, "approve", `{"comment":"","decision":"approve","suggested_value":""}`},
		{"clear-cell", nil, "comment_only", `{"comment":"","decision":"comment_only","suggested_value":""}`},
		{"withdraw-cell", nil, "withdraw", `{"comment":"","decision":"withdraw","suggested_value":""}`},
		{"accept-cell", []string{"--comment", "matches"}, "approve", `{"comment":"matches","decision":"approve","suggested_value":""}`},
		{"clear-cell", []string{"--comment", "fixed"}, "comment_only", `{"comment":"fixed","decision":"comment_only","suggested_value":""}`},
	}
	for _, tc := range cases {
		t.Run(tc.verb+" "+strings.Join(tc.extra, " "), func(t *testing.T) {
			e := newTestEnv(t)
			bodies := e.respondAction(amountHistoryID, http.StatusOK, ownResult(tc.decision, "open"))

			args := append([]string{tc.verb, reviewA, "--history-id", "811"}, tc.extra...)
			if _, _, err := e.exec(t, args...); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(*bodies) != 1 {
				t.Fatalf("POST bodies = %d, want 1", len(*bodies))
			}
			// Re-marshalling a map sorts its keys, so the comparison is exact
			// about the key set and every value's JSON type.
			got, _ := json.Marshal((*bodies)[0])
			if string(got) != tc.want {
				t.Errorf("body = %s\nwant   %s", got, tc.want)
			}
		})
	}
}

func TestCellVerdict_LocalRefusalsCostNoRequest(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"neither selector", []string{reviewA}},
		{"both selectors", []string{reviewA, "--field", "amount", "--history-id", "811"}},
		{"zero history id", []string{reviewA, "--history-id", "0"}},
		{"negative history id", []string{reviewA, "--history-id", "-3"}},
		{"non-numeric history id", []string{reviewA, "--history-id", "h811"}},
		{"empty field", []string{reviewA, "--field", ""}},
		{"malformed review id", []string{"0190aaaa", "--field", "amount"}},
		{"no review id", []string{"--field", "amount"}},
		{"two review ids", []string{reviewA, reviewB, "--field", "amount"}},
	}
	for _, verb := range cellVerbs {
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

func TestCellVerdict_ForbiddenNamesThePermissionByVerb(t *testing.T) {
	want := map[string]string{
		"accept-cell": "DATA_REVIEW_APPROVE",
		"clear-cell":  "DATA_REVIEW_APPROVE",
	}
	for verb, permission := range want {
		t.Run(verb, func(t *testing.T) {
			e := newTestEnv(t)
			// The real body: no error_code, so nothing but the verb can pick
			// the explanation.
			e.respondAction(amountHistoryID, http.StatusForbidden, map[string]any{"detail": map[string]any{
				"msg": "Recording an `approve` decision requires the data-review approve permission."}})

			_, _, err := e.exec(t, verb, reviewA, "--history-id", "811")
			if got := qerrors.GetExitCode(err); got != qerrors.ExitUnauthorized {
				t.Errorf("exit code = %d, want 4 (%v)", got, err)
			}
			if err == nil || !strings.Contains(err.Error(), permission) {
				t.Errorf("err = %v, want %s named", err, permission)
			}
		})
	}
}

func TestWithdrawCell_RefusalsKeepTheCauseAndExitCode(t *testing.T) {
	cases := []struct {
		name, message, code, hint string
		status, exit              int
	}{
		{"another reviewer", "You can only withdraw your own cell decision.", "", "own cell decision", http.StatusForbidden, qerrors.ExitUnauthorized},
		{"blocking veto", "Clearing a blocking cell decision requires the data-review approve permission.", "", "DATA_REVIEW_APPROVE", http.StatusForbidden, qerrors.ExitUnauthorized},
		{"no decision", "You have no standing cell decision to withdraw.", "", "", http.StatusUnprocessableEntity, qerrors.ExitBadUsage},
		{"wrong table", "history_ids [811] do not belong to dataset 42.", errorCodePayloadMismatch, "history id 811", http.StatusUnprocessableEntity, qerrors.ExitBadUsage},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestEnv(t)
			e.respondAction(amountHistoryID, tc.status, map[string]any{"detail": map[string]any{
				"msg": tc.message, "error_code": tc.code,
			}})
			_, _, err := e.exec(t, "withdraw-cell", reviewA, "--history-id", "811")
			if got := qerrors.GetExitCode(err); got != tc.exit {
				t.Errorf("exit code = %d, want %d (%v)", got, tc.exit, err)
			}
			if err == nil || !strings.Contains(err.Error(), tc.message) || !strings.Contains(err.Error(), tc.hint) {
				t.Errorf("err = %v, want cause %q and hint %q", err, tc.message, tc.hint)
			}
		})
	}
}

func TestWithdrawCell_TakesNoComment(t *testing.T) {
	e := newTestEnv(t)
	_, _, err := e.exec(t, "withdraw-cell", reviewA, "--history-id", "811", "--comment", "ignored")
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want 2 (%v)", got, err)
	}
	if got := len(e.mock.Requests()); got != 0 {
		t.Errorf("requests = %d, want 0", got)
	}
}

func TestCellVerdict_ForbiddenLookupNamesTheListPermission(t *testing.T) {
	e := newTestEnv(t)
	e.respondReview(http.StatusForbidden, map[string]any{"detail": map[string]any{"msg": "permission denied"}})

	_, _, err := e.exec(t, "accept-cell", reviewA, "--field", "amount")
	if got := qerrors.GetExitCode(err); got != qerrors.ExitUnauthorized {
		t.Errorf("exit code = %d, want 4 (%v)", got, err)
	}
	if err == nil || !strings.Contains(err.Error(), "DATA_REVIEW_LIST") {
		t.Errorf("err = %v, want DATA_REVIEW_LIST named", err)
	}
	if got := e.posts(); got != 0 {
		t.Errorf("POSTs = %d, want 0", got)
	}
}

func TestCellVerdict_PayloadMismatchIsBadUsage(t *testing.T) {
	e := newTestEnv(t)
	e.respondAction(amountHistoryID, http.StatusUnprocessableEntity, map[string]any{"detail": map[string]any{
		"msg": "history_ids [811] do not belong to dataset 42.", "error_code": "REVIEW_PAYLOAD_MISMATCH"}})

	_, _, err := e.exec(t, "clear-cell", reviewA, "--history-id", "811")
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want 2 (%v)", got, err)
	}
}

func TestCellVerdict_NotFoundIsExit3(t *testing.T) {
	notFound := map[string]any{"detail": map[string]any{"msg": "DataReview " + reviewA + " not found"}}

	t.Run("on the lookup", func(t *testing.T) {
		e := newTestEnv(t)
		e.respondReview(http.StatusNotFound, notFound)
		_, _, err := e.exec(t, "accept-cell", reviewA, "--field", "amount")
		if got := qerrors.GetExitCode(err); got != qerrors.ExitNotFound {
			t.Errorf("exit code = %d, want 3 (%v)", got, err)
		}
		if got := e.posts(); got != 0 {
			t.Errorf("POSTs = %d, want 0", got)
		}
	})
	t.Run("on the write", func(t *testing.T) {
		e := newTestEnv(t)
		e.respondAction(amountHistoryID, http.StatusNotFound, notFound)
		_, _, err := e.exec(t, "clear-cell", reviewA, "--history-id", "811")
		if got := qerrors.GetExitCode(err); got != qerrors.ExitNotFound {
			t.Errorf("exit code = %d, want 3 (%v)", got, err)
		}
	})
}

func TestCellVerdict_ResultLines(t *testing.T) {
	cases := []struct {
		name       string
		verb       string
		selector   []string
		result     map[string]any
		wantStdout string
		wantStderr string
	}{
		{
			name: "by field, own review only", verb: "accept-cell",
			selector:   []string{"--field", "amount"},
			result:     anActionResult("approve", map[string]string{reviewA: "approved"}, []string{reviewA}, []int{1001}, []int{1001}),
			wantStdout: "data-review/" + reviewA + " cell amount [h: 811] accepted (status: approved, affected: 1 review(s) / 1 row(s), released: 1 row(s))\n",
		},
		{
			name: "by history id, shared change", verb: "clear-cell",
			selector: []string{"--history-id", "811"},
			result: anActionResult("comment_only",
				map[string]string{reviewA: "open", reviewB: "open", reviewC: "changes_requested"},
				[]string{reviewB, reviewA, reviewC}, []int{1002, 1001, 1003}, nil),
			wantStdout: "data-review/" + reviewA + " cell [h: 811] cleared (status: open, affected: 3 review(s) / 3 row(s), released: 0 row(s))\n",
			wantStderr: "this change is shared: the verdict also applied to 2 other review(s)\n",
		},
		{
			// The route's review drops out of the recompute when it is closed
			// or its row has left quarantine; the write still happens.
			name: "own review not recomputed", verb: "clear-cell",
			selector: []string{"--field", "amount"},
			result: anActionResult("comment_only", map[string]string{reviewB: "open"},
				[]string{reviewB}, []int{1002}, nil),
			wantStdout: "data-review/" + reviewA + " cell amount [h: 811] cleared (affected: 1 review(s) / 1 row(s), released: 0 row(s))\n",
			wantStderr: "note: data-review/" + reviewA + " was not recomputed: it is closed or its row is not held" +
				" in quarantine; the verdict is still recorded on the change\n" +
				"this change is shared: the verdict also applied to 1 other review(s)\n",
		},
		{
			name: "withdraw by field, shared change", verb: "withdraw-cell",
			selector: []string{"--field", "amount"},
			result: anActionResult("withdraw", map[string]string{reviewA: "open", reviewB: "open"},
				[]string{reviewA, reviewB}, []int{1001, 1002}, nil),
			wantStdout: "data-review/" + reviewA + " cell amount [h: 811] withdrawn (status: open, affected: 2 review(s) / 2 row(s), released: 0 row(s))\n",
			wantStderr: "this change is shared: the withdrawal also applied to 1 other review(s)\n",
		},
		{
			name: "withdraw on a review not recomputed", verb: "withdraw-cell",
			selector:   []string{"--history-id", "811"},
			result:     anActionResult("withdraw", map[string]string{}, []string{}, []int{}, nil),
			wantStdout: "data-review/" + reviewA + " cell [h: 811] withdrawn (affected: 0 review(s) / 0 row(s), released: 0 row(s))\n",
			wantStderr: "note: data-review/" + reviewA + " was not recomputed: it is closed or its row is not held" +
				" in quarantine; your decision was still withdrawn from the change\n",
		},
		{
			name: "upper-case status key still matches", verb: "accept-cell",
			selector: []string{"--history-id", "811"},
			result: anActionResult("approve", map[string]string{strings.ToUpper(reviewA): "open"},
				[]string{reviewA}, []int{1001}, nil),
			wantStdout: "data-review/" + reviewA + " cell [h: 811] accepted (status: open, affected: 1 review(s) / 1 row(s), released: 0 row(s))\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestEnv(t)
			e.respondReview(http.StatusOK, aReview([]map[string]any{
				aChange("amount", amountHistoryID), aChange("email", emailHistoryID),
			}))
			e.respondAction(amountHistoryID, http.StatusOK, tc.result)

			// An upper-case id on the command line is normalised on output.
			args := append([]string{tc.verb, strings.ToUpper(reviewA)}, tc.selector...)
			stdout, stderr, err := e.exec(t, args...)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			wantStdout := tc.wantStdout + "Next: qctl describe data-review " + reviewA + "\n"
			if stdout != wantStdout {
				t.Errorf("stdout = %q\nwant   = %q", stdout, wantStdout)
			}
			if stderr != tc.wantStderr {
				t.Errorf("stderr = %q\nwant   = %q", stderr, tc.wantStderr)
			}
		})
	}
}

// Under -o json the verdict's ledger result is the only thing on stdout; the
// result line and the Next: recipe move to stderr. There is no -o name: the
// result is a ledger entry, not a review.
func TestCellVerdict_JSONPrintsTheLedgerResult(t *testing.T) {
	for verb, decision := range map[string]string{"accept-cell": "approve", "clear-cell": "comment_only", "withdraw-cell": "withdraw"} {
		e := newTestEnv(t)
		e.respondAction(amountHistoryID, http.StatusOK, ownResult(decision, "open"))
		stdout, stderr, err := e.exec(t, verb, reviewA, "--history-id", "811", "-o", "json")
		if err != nil {
			t.Fatalf("%s -o json: %v", verb, err)
		}
		var got struct {
			AffectedDataReviewIDs []string `json:"affected_data_review_ids"`
			AffectedRowIDs        []int    `json:"affected_row_ids"`
		}
		if err := json.Unmarshal([]byte(stdout), &got); err != nil {
			t.Fatalf("%s: stdout is not one JSON document: %v\n%s", verb, err, stdout)
		}
		if len(got.AffectedDataReviewIDs) != 1 || got.AffectedDataReviewIDs[0] != reviewA ||
			len(got.AffectedRowIDs) != 1 || got.AffectedRowIDs[0] != 1001 {
			t.Errorf("%s: document = %+v, want review %s and row 1001", verb, got, reviewA)
		}
		if !strings.Contains(stderr, "Next: qctl describe data-review "+reviewA) {
			t.Errorf("%s: stderr missing the Next: recipe:\n%s", verb, stderr)
		}
		_, _, err = e.exec(t, verb, reviewA, "--history-id", "811", "-o", "name")
		if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
			t.Errorf("%s -o name: exit %d (%v), want %d", verb, code, err, qerrors.ExitBadUsage)
		}
	}
}

// The response's decision is a generated union whose only field is an
// unexported json.RawMessage, so a printer that walks fields instead of
// calling MarshalJSON renders it as {}. Both documents must carry the bare
// string the server sent.
func TestCellVerdict_StructuredOutputPrintsTheDecisionAsAString(t *testing.T) {
	decisions := map[string]string{"accept-cell": "approve", "clear-cell": "comment_only", "withdraw-cell": "withdraw"}
	for verb, decision := range decisions {
		for _, format := range []string{"json", "yaml"} {
			t.Run(verb+"/"+format, func(t *testing.T) {
				e := newTestEnv(t)
				e.respondAction(amountHistoryID, http.StatusOK, ownResult(decision, "open"))

				stdout, _, err := e.exec(t, verb, reviewA, "--history-id", "811", "-o", format)
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				var doc map[string]any
				if format == "json" {
					err = json.Unmarshal([]byte(stdout), &doc)
				} else {
					err = yaml.Unmarshal([]byte(stdout), &doc)
				}
				if err != nil {
					t.Fatalf("stdout is not one %s document: %v\n%s", format, err, stdout)
				}
				if got, ok := doc["decision"].(string); !ok || got != decision {
					t.Errorf("decision = %#v, want the string %q\n%s", doc["decision"], decision, stdout)
				}
				if got := doc["ledger_id"]; got != "0190ffff-1111-4222-8333-444444444444" {
					t.Errorf("ledger_id = %#v, want the server's decision id", got)
				}
				statuses, ok := doc["affected_review_statuses"].(map[string]any)
				if !ok || statuses[reviewA] != "open" {
					t.Errorf("affected_review_statuses = %#v, want the server's open status for %s", doc["affected_review_statuses"], reviewA)
				}
			})
		}
	}
}
