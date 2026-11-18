package data_review //nolint:revive // Package name mirrors the hyphenated command.

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const (
	progressGroupID    = "11111111-2222-4333-8444-555555555555"
	countRoute         = "/api/orgs/" + testOrgID + "/datasets/42/review/count-by-state"
	signoffRoute       = "/api/orgs/" + testOrgID + "/datasets/42/data-reviews/signoff-progress"
	requestStatesRoute = "/api/orgs/" + testOrgID + "/datasets/42/review/request-state-counts"
)

func (e *testEnv) run(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	return e.exec(t, append([]string{"progress"}, args...)...)
}

func (e *testEnv) registerSuccess(myTotal int) (*url.Values, *url.Values) {
	countQuery := &url.Values{}
	signoffQuery := &url.Values{}
	e.mock.RegisterHandler("GET", countRoute, func(w http.ResponseWriter, r *http.Request) {
		*countQuery = r.URL.Query()
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(
			`{"awaiting_decision":7,"rejected":2,"accepted":11}`,
		))
	})
	e.mock.RegisterHandler("GET", signoffRoute, func(w http.ResponseWriter, r *http.Request) {
		*signoffQuery = r.URL.Query()
		myDone, myRemaining := 2, 1
		if myTotal == 0 {
			myDone, myRemaining = 0, 0
		}
		testutil.RespondJSON(w, http.StatusOK, map[string]int{
			"done": 14, "total": 20, "remaining": 6,
			"my_done": myDone, "my_total": myTotal, "my_remaining": myRemaining,
		})
	})
	return countQuery, signoffQuery
}

func (e *testEnv) registerRequestStates() *url.Values {
	query := &url.Values{}
	e.mock.RegisterHandler("GET", requestStatesRoute, func(w http.ResponseWriter, r *http.Request) {
		*query = r.URL.Query()
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(
			`{"approver":{"to_review":7,"flagged_by_me":2,"approved":8,"on_hold":3},"sender":{"waiting":9,"flagged":4,"approved":4,"on_hold":3}}`,
		))
	})
	return query
}

func TestProgress_GroupScopesRowAndRequestCounts(t *testing.T) {
	e := newTestEnv(t)
	countQuery, signoffQuery := e.registerSuccess(3)
	requestQuery := e.registerRequestStates()

	stdout, stderr, err := e.run(t, "--table-id", "42", "--group", progressGroupID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "rows: awaiting_decision 7 · rejected 2 · accepted 11  (scope: submission " + progressGroupID + ")\n" +
		"approver (you): to_review 7 · flagged_by_me 2 · approved 8 · on_hold 3\n" +
		"sender: waiting 9 · flagged 4 · approved 4 · on_hold 3\n" +
		"sign-off: done 14 of 20 (remaining 6) · you: 2 of 3 (remaining 1)\n"
	if stdout != want {
		t.Errorf("stdout = %q\nwant   = %q", stdout, want)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
	if got := countQuery.Get("review_group_id"); got != progressGroupID {
		t.Errorf("count review_group_id = %q, want %q", got, progressGroupID)
	}
	if got := signoffQuery.Get("review_group_id"); got != "" {
		t.Errorf("signoff review_group_id = %q, want omitted", got)
	}
	if got := requestQuery.Get("review_group_id"); got != progressGroupID {
		t.Errorf("request states review_group_id = %q, want %q", got, progressGroupID)
	}
	if got := len(e.mock.Requests()); got != 3 {
		t.Errorf("requests = %d, want count + request states + signoff: %v", got, e.mock.Requests())
	}
}

func TestProgress_NoPersonalAssignmentsHasExplicitWording(t *testing.T) {
	e := newTestEnv(t)
	e.registerSuccess(0)

	stdout, _, err := e.run(t, "--table-id", "42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout, "(scope: whole table)") {
		t.Errorf("stdout = %q, want whole-table scope", stdout)
	}
	if !strings.Contains(stdout, "you: no reviews assigned to you") {
		t.Errorf("stdout = %q, want no-assignment wording", stdout)
	}
	if strings.Contains(stdout, "approver (you):") || strings.Contains(stdout, "sender:") || len(e.mock.Requests()) != 2 {
		t.Errorf("ungrouped progress added request states: %q, requests %v", stdout, e.mock.Requests())
	}
}

func TestProgress_GroupStructuredOutputIncludesRequestCounts(t *testing.T) {
	var jsonOutput string
	for _, format := range []string{"json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			e := newTestEnv(t)
			e.registerSuccess(3)
			e.registerRequestStates()
			stdout, stderr, err := e.run(t, "--table-id", "42", "--group", progressGroupID, "-o", format)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if stderr != "" {
				t.Errorf("stderr = %q, want empty", stderr)
			}
			if format == "yaml" {
				testutil.AssertYAMLReadsAsJSON(t, stdout, jsonOutput)
				return
			}
			jsonOutput = stdout
			var got map[string]json.RawMessage
			if err := json.Unmarshal([]byte(stdout), &got); err != nil {
				t.Fatalf("invalid JSON: %v", err)
			}
			var counts map[string]map[string]int
			if err := json.Unmarshal(got["request_state_counts"], &counts); err != nil {
				t.Fatalf("invalid request counts: %v", err)
			}
			if len(got) != 3 || counts["approver"]["approved"] != 8 || counts["sender"]["approved"] != 4 ||
				counts["approver"]["flagged_by_me"] != 2 || counts["sender"]["flagged"] != 4 {
				t.Errorf("payload = %s", stdout)
			}
		})
	}
}

func TestProgress_RequestCountsErrorsKeepExitCode(t *testing.T) {
	for _, tc := range []struct {
		status int
		code   int
	}{
		{http.StatusForbidden, qerrors.ExitUnauthorized},
		{http.StatusNotFound, qerrors.ExitNotFound},
		{http.StatusUnprocessableEntity, qerrors.ExitBadUsage},
		{http.StatusServiceUnavailable, qerrors.ExitGenericError},
	} {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			e := newTestEnv(t)
			e.registerSuccess(3)
			e.mock.RegisterHandler("GET", requestStatesRoute, func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, tc.status, json.RawMessage(`{"detail":{"msg":"request counts unavailable","error_code":"REQUEST_COUNTS_UNAVAILABLE"}}`))
			})
			stdout, _, err := e.run(t, "--table-id", "42", "--group", progressGroupID)
			if err == nil || qerrors.GetExitCode(err) != tc.code {
				t.Fatalf("error = %v, want exit %d", err, tc.code)
			}
			if !strings.Contains(err.Error(), "request-state-counts endpoint") {
				t.Errorf("error = %v, want endpoint name", err)
			}
			if stdout != "" || len(e.mock.Requests()) != 2 {
				t.Errorf("stdout = %q, requests = %v; want no partial success and no signoff read", stdout, e.mock.Requests())
			}
		})
	}
}

func TestProgress_JSONHasBothWireKeyedObjects(t *testing.T) {
	e := newTestEnv(t)
	e.registerSuccess(3)

	stdout, stderr, err := e.run(t, "--table-id", "42", "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got map[string]map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
	}
	if len(got) != 2 || got["count_by_state"]["accepted"] != float64(11) ||
		got["signoff_progress"]["my_total"] != float64(3) {
		t.Errorf("payload = %#v", got)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
}

func TestProgress_LocalRefusalsCostNoRequest(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"missing table", nil},
		{"both table selectors", []string{"--table", "orders", "--table-id", "42"}},
		{"empty group", []string{"--table-id", "42", "--group", ""}},
		{"malformed group", []string{"--table-id", "42", "--group", "not-a-uuid"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestEnv(t)
			_, _, err := e.run(t, tc.args...)
			if err == nil {
				t.Fatal("expected usage error")
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want 2 (%v)", got, err)
			}
			if got := len(e.mock.Requests()); got != 0 {
				t.Errorf("requests = %d, want 0: %v", got, e.mock.Requests())
			}
		})
	}
}

func TestProgress_ForbiddenNamesTheEndpointAndPreservesExitFour(t *testing.T) {
	for _, endpoint := range []string{"count-by-state", "signoff-progress"} {
		t.Run(endpoint, func(t *testing.T) {
			e := newTestEnv(t)
			if endpoint == "signoff-progress" {
				e.mock.RegisterHandler("GET", countRoute, func(w http.ResponseWriter, _ *http.Request) {
					testutil.RespondJSON(w, http.StatusOK, json.RawMessage(
						`{"awaiting_decision":7,"rejected":2,"accepted":11}`,
					))
				})
			}
			forbiddenRoute := countRoute
			if endpoint == "signoff-progress" {
				forbiddenRoute = signoffRoute
			}
			e.mock.RegisterHandler("GET", forbiddenRoute, func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusForbidden,
					map[string]any{"detail": map[string]any{"msg": "permission denied"}})
			})

			_, _, err := e.run(t, "--table-id", "42")
			if err == nil {
				t.Fatal("expected forbidden error")
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitUnauthorized {
				t.Errorf("exit code = %d, want 4 (%v)", got, err)
			}
			if !strings.Contains(err.Error(), endpoint+" endpoint") {
				t.Errorf("error = %q, want endpoint name", err)
			}
		})
	}
}
