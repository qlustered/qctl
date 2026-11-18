package deliverables

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const (
	revV1 = "aaaaaaaa-0000-4000-8000-000000000001"
	revV2 = "aaaaaaaa-0000-4000-8000-000000000002"
	revV3 = "aaaaaaaa-0000-4000-8000-000000000003"
	revV4 = "aaaaaaaa-0000-4000-8000-000000000004"
)

const revisionsPath = testPath + "/" + monthlyID + "/revisions"

var (
	disabledV1 = map[string]any{"id": revV1, "release": "v1", "state": "disabled"}
	enabledV2  = map[string]any{"id": revV2, "release": "v2", "state": "enabled"}
	draftV3    = map[string]any{"id": revV3, "release": "v3", "state": "draft"}
	draftV4    = map[string]any{"id": revV4, "release": "v4 beta", "state": "draft"}
)

func serveRevisions(t *testing.T, items ...map[string]any) *testutil.MockAPIServer {
	t.Helper()
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	if items == nil {
		items = []map[string]any{}
	}
	mock.RegisterHandler("GET", revisionsPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, items)
	})
	return mock
}

func TestResolveRevisionSelectsByReleaseUUIDOrEnabled(t *testing.T) {
	for _, tc := range []struct {
		name              string
		items             []map[string]any
		release, revision string
		want              string
		code              int
		requests          int
	}{
		{name: "enabled by default", items: []map[string]any{disabledV1, enabledV2, draftV3}, want: revV2, requests: 1},
		{name: "exact release", items: []map[string]any{disabledV1, enabledV2}, release: "v1", want: revV1, requests: 1},
		{name: "release as a UUID", items: []map[string]any{disabledV1, draftV3}, release: revV3, want: revV3, requests: 1},
		{name: "revision UUID", items: []map[string]any{disabledV1, enabledV2}, revision: revV1, want: revV1, requests: 1},
		{name: "unknown release", items: []map[string]any{disabledV1}, release: "v9", code: qerrors.ExitNotFound, requests: 1},
		{name: "unknown revision", items: []map[string]any{disabledV1}, revision: revV4, code: qerrors.ExitNotFound, requests: 1},
		{name: "only revision", items: []map[string]any{draftV3}, want: revV3, requests: 1},
		{name: "no revisions", code: qerrors.ExitNotFound, requests: 1},
		{name: "none enabled", items: []map[string]any{disabledV1, draftV3}, code: qerrors.ExitBadUsage, requests: 1},
		{name: "both selectors", release: "v1", revision: revV1, code: qerrors.ExitBadUsage},
		{name: "short revision", revision: "aaaaaaaa", code: qerrors.ExitBadUsage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := serveRevisions(t, tc.items...)
			got, err := NewClient(mock.URL(), testOrgID, 0).ResolveRevision("token", uuid.MustParse(monthlyID), tc.release, tc.revision)
			if code := qerrors.GetExitCode(err); code != tc.code {
				t.Fatalf("exit = %d, want %d; error = %v", code, tc.code, err)
			}
			if tc.code == 0 && got.String() != tc.want {
				t.Errorf("revision = %s, want %s", got, tc.want)
			}
			if tc.code != 0 && got != uuid.Nil {
				t.Errorf("revision = %s on error, want the nil UUID", got)
			}
			if n := len(mock.Requests()); n != tc.requests {
				t.Errorf("requests = %d, want %d", n, tc.requests)
			}
			if tc.code == qerrors.ExitNotFound && tc.items != nil && !strings.Contains(err.Error(), "qctl get deliverable-revisions "+monthlyID) {
				t.Errorf("not-found error does not say how to list revisions: %v", err)
			}
		})
	}
}

func TestResolveEnableTargetDefaultsToTheOnlyDraft(t *testing.T) {
	for _, tc := range []struct {
		name              string
		items             []map[string]any
		release, revision string
		want              string
		code              int
		requests          int
		says              []string
	}{
		{name: "only draft", items: []map[string]any{disabledV1, enabledV2, draftV3}, want: revV3, requests: 1},
		{name: "no draft", items: []map[string]any{disabledV1, enabledV2}, code: qerrors.ExitBadUsage, requests: 1,
			says: []string{"no draft revision", "--release", "a disabled release can be enabled again", "qctl get deliverable-revisions " + monthlyID}},
		{name: "several drafts", items: []map[string]any{enabledV2, draftV3, draftV4}, code: qerrors.ExitBadUsage, requests: 1,
			says: []string{"2 draft revisions", `"v3"`, `"v4 beta"`, "--release", "qctl get deliverable-revisions " + monthlyID}},
		{name: "disabled release", items: []map[string]any{disabledV1, enabledV2, draftV3}, release: "v1", want: revV1, requests: 1},
		{name: "revision UUID", items: []map[string]any{disabledV1, enabledV2, draftV3, draftV4}, revision: revV2, want: revV2, requests: 1},
		{name: "unknown release", items: []map[string]any{disabledV1, draftV3}, release: "v9", code: qerrors.ExitNotFound, requests: 1,
			says: []string{`"v9"`, "qctl get deliverable-revisions " + monthlyID}},
		{name: "no revisions", code: qerrors.ExitNotFound, requests: 1, says: []string{"has no revisions"}},
		{name: "no revisions with a release", release: "v1", code: qerrors.ExitNotFound, requests: 1},
		{name: "both selectors", release: "v1", revision: revV1, code: qerrors.ExitBadUsage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := serveRevisions(t, tc.items...)
			target, all, err := NewClient(mock.URL(), testOrgID, 0).ResolveEnableTarget("token", uuid.MustParse(monthlyID), tc.release, tc.revision)
			if code := qerrors.GetExitCode(err); code != tc.code {
				t.Fatalf("exit = %d, want %d; error = %v", code, tc.code, err)
			}
			if n := len(mock.Requests()); n != tc.requests {
				t.Errorf("requests = %d, want %d", n, tc.requests)
			}
			for _, want := range tc.says {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error lacks %q: %v", want, err)
				}
			}
			if tc.code != 0 {
				return
			}
			if target.ID.String() != tc.want {
				t.Errorf("target = %s, want %s", target.ID, tc.want)
			}
			if len(all) != len(tc.items) {
				t.Errorf("all = %d revisions, want %d", len(all), len(tc.items))
			}
		})
	}
}

func TestFormatIssueJoinsLocByDots(t *testing.T) {
	loc := []string{"sheet_bindings", "Claims", "column_bindings", "amount"}
	empty := []string{}
	for _, tc := range []struct {
		name  string
		issue Issue
		want  string
	}{
		{name: "loc", issue: Issue{Loc: &loc, Msg: "Column amount is not on table Claims."},
			want: "sheet_bindings.Claims.column_bindings.amount: Column amount is not on table Claims."},
		{name: "nil loc", issue: Issue{Msg: "The revision has no template."}, want: "The revision has no template."},
		{name: "empty loc", issue: Issue{Loc: &empty, Msg: "The revision has no template."}, want: "The revision has no template."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := FormatIssue(tc.issue); got != tc.want {
				t.Errorf("FormatIssue = %q, want %q", got, tc.want)
			}
		})
	}
}

const revisionPath = "/api/orgs/" + testOrgID + "/deliverable-revisions/" + revV3

func TestValidateReturnsAnInvalidRevisionWithoutAnError(t *testing.T) {
	const body = `{"valid":false,` +
		`"errors":[{"binding_kind":"cell_binding","binding_id":"` + revV4 + `","loc":["cell_bindings","Summary!C4"],"msg":"Sheet Summary is not in the template."}],` +
		`"warnings":[{"binding_kind":"revision","binding_id":null,"loc":[],"msg":"Formulas in the data region of sheet Claims."}]}`
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	mock.RegisterHandler("POST", revisionPath+"/validate", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	})
	result, err := NewClient(mock.URL(), testOrgID, 0).Validate("token", uuid.MustParse(revV3))
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if result.Valid {
		t.Error("Valid = true, want false")
	}
	if result.Errors == nil || len(*result.Errors) != 1 || FormatIssue((*result.Errors)[0]) != "cell_bindings.Summary!C4: Sheet Summary is not in the template." {
		t.Errorf("errors = %+v", result.Errors)
	}
	if result.Warnings == nil || len(*result.Warnings) != 1 || FormatIssue((*result.Warnings)[0]) != "Formulas in the data region of sheet Claims." {
		t.Errorf("warnings = %+v", result.Warnings)
	}
	if string(result.Body) != body {
		t.Errorf("Body = %s, want the response body", result.Body)
	}
}

func TestValidateRefusalKeepsTheAPIError(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	mock.RegisterHandler("POST", revisionPath+"/validate", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusConflict, map[string]any{"detail": map[string]any{
			"msg": "The organization has no backup settings.", "error_id": ""}})
	})
	_, err := NewClient(mock.URL(), testOrgID, 0).Validate("token", uuid.MustParse(revV3))
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Fatalf("exit = %d, want %d; error = %v", code, qerrors.ExitBadUsage, err)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusConflict {
		t.Errorf("error metadata lost: %#v", err)
	}
}

func TestEnableReturnsTheRevisionTree(t *testing.T) {
	const body = `{"id":"` + revV3 + `","deliverable_id":"` + monthlyID + `","release":"v3","state":"enabled","sheet_bindings":[],"cell_bindings":[]}`
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	mock.RegisterHandler("POST", revisionPath+"/enable", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	})
	detail, err := NewClient(mock.URL(), testOrgID, 0).Enable("token", uuid.MustParse(revV3))
	if err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if detail.ID.String() != revV3 || detail.State != api.DeliverableRevisionStateEnabled || detail.Release != "v3" {
		t.Errorf("revision = %+v", detail.DeliverableRevisionResponse)
	}
	if string(detail.Body) != body {
		t.Errorf("Body = %s, want the response body", detail.Body)
	}
	if got := strings.Join(mock.Requests(), ","); got != "POST "+revisionPath+"/enable" {
		t.Errorf("requests = %s", got)
	}
}

// serveEnable answers the enable route with status and the APIError envelope
// atlas's middleware renders: {"detail": APIError.to_dict()}.
func serveEnable(t *testing.T, status int, detail map[string]any) (*testutil.MockAPIServer, []byte) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"detail": detail})
	if err != nil {
		t.Fatal(err)
	}
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	mock.RegisterHandler("POST", revisionPath+"/enable", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(body)
	})
	return mock, body
}

func TestEnableListsTheIssuesOfAnInvalidRevision(t *testing.T) {
	mock, _ := serveEnable(t, http.StatusUnprocessableEntity, map[string]any{
		"msg":        "Revision " + revV3 + " failed validation with 2 error(s).",
		"error_code": "REVISION_INVALID",
		"error_id":   "3F9A0C1B",
		"context": map[string]any{"errors": []map[string]any{
			{"binding_kind": "column_binding", "binding_id": revV4, "loc": []string{"sheet_bindings", "Claims", "column_bindings", "amount"},
				"msg": "Column amount is not on table Claims."},
			{"binding_kind": "revision", "binding_id": nil, "loc": []string{}, "msg": "The revision has no template."},
		}},
	})
	_, err := NewClient(mock.URL(), testOrgID, 0).Enable("token", uuid.MustParse(revV3))
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Fatalf("exit = %d, want %d; error = %v", code, qerrors.ExitBadUsage, err)
	}
	var invalid *RevisionInvalidError
	if !errors.As(err, &invalid) || len(invalid.Issues) != 2 || invalid.Issues[0].BindingKind != "column_binding" {
		t.Fatalf("RevisionInvalidError = %#v; error = %v", invalid, err)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusUnprocessableEntity || apiErr.Code != "REVISION_INVALID" {
		t.Errorf("error metadata lost: %#v", apiErr)
	}
	text := err.Error()
	if !strings.Contains(text, "failed validation with 2 error(s).") {
		t.Errorf("server message lost: %s", text)
	}
	want := "\n  - sheet_bindings.Claims.column_bindings.amount: Column amount is not on table Claims.\n  - The revision has no template."
	if !strings.HasSuffix(text, want) {
		t.Errorf("error = %q, want it to end with %q", text, want)
	}
}

func TestEnableListsTheRunsThatCouldNotMove(t *testing.T) {
	first := "Sheet 'Claims' has an overridden table, and revision 'v3' has no sheet 'Claims'."
	second := "No table matches period 2026-09."
	mock, _ := serveEnable(t, http.StatusConflict, map[string]any{
		"msg":      "Run 41: " + first + " Run 42: " + second,
		"error_id": "",
		"context":  map[string]any{"runs": []map[string]any{{"run_id": 41, "reason": first}, {"run_id": 42, "reason": second}}},
	})
	_, err := NewClient(mock.URL(), testOrgID, 0).Enable("token", uuid.MustParse(revV3))
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Fatalf("exit = %d, want %d; error = %v", code, qerrors.ExitBadUsage, err)
	}
	var blocked *RunsBlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("not a RunsBlockedError: %v", err)
	}
	if want := []RunProblem{{RunID: 41, Reason: first}, {RunID: 42, Reason: second}}; !reflect.DeepEqual(blocked.Runs, want) {
		t.Errorf("Runs = %+v, want %+v", blocked.Runs, want)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusConflict || apiErr.Code != "" {
		t.Errorf("error metadata lost: %#v", apiErr)
	}
	want := "\n  - run 41: " + first + "\n  - run 42: " + second
	if !strings.HasSuffix(err.Error(), want) {
		t.Errorf("error = %q, want it to end with %q", err, want)
	}
	if n := strings.Count(err.Error(), first); n != 1 {
		t.Errorf("error repeats a reason %d times: %q", n, err)
	}
}

func TestEnableLeavesOtherRefusalsUnchanged(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		detail map[string]any
	}{
		{name: "already enabled", status: http.StatusConflict,
			detail: map[string]any{"msg": "Revision " + revV3 + " is already enabled.", "error_id": ""}},
		{name: "archived", status: http.StatusConflict,
			detail: map[string]any{"msg": "Deliverable " + monthlyID + " is archived; archived deliverables can not be modified.", "error_id": ""}},
		{name: "no runs listed", status: http.StatusConflict,
			detail: map[string]any{"msg": "conflict", "error_id": "", "context": map[string]any{"runs": []any{}}}},
		{name: "another 422 code", status: http.StatusUnprocessableEntity,
			detail: map[string]any{"msg": "bad binding", "error_code": "BINDING_INVALID", "error_id": "0D1E2F3A",
				"context": map[string]any{"errors": []map[string]any{{"binding_kind": "revision", "loc": []string{}, "msg": "bad binding"}}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock, body := serveEnable(t, tc.status, tc.detail)
			_, err := NewClient(mock.URL(), testOrgID, 0).Enable("token", uuid.MustParse(revV3))
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit = %d, want %d; error = %v", code, qerrors.ExitBadUsage, err)
			}
			if _, ok := err.(*APIError); !ok {
				t.Errorf("error is %T, want the *APIError itself", err)
			}
			if want := handleError(tc.status, body, enableFailed, "enable_deliverable_revision").Error(); err.Error() != want {
				t.Errorf("error = %q, want %q", err, want)
			}
		})
	}
}

func TestFollowingRunsPagesEveryStateAndKeepsTheRunsThatMove(t *testing.T) {
	target := uuid.MustParse(revV3)
	run := func(id int, state, mode, revision string) map[string]any {
		return map[string]any{"id": id, "state": state, "revision_mode": mode, "revision_id": revision,
			"deliverable_id": monthlyID, "sequence": 1, "run_params": map[string]any{}}
	}
	modes := []string{"new_period", "reissue_enabled", "reissue_as_delivered", "new_period"}
	byState := map[string][]map[string]any{"rendered": {}}
	var want []int
	for id := 11; id <= 111; id++ {
		mode, revision := modes[id%4], revV2
		if id%4 == 3 {
			revision = revV3
		}
		byState["open"] = append(byState["open"], run(id, "open", mode, revision))
		if id%4 <= 1 {
			want = append(want, id)
		}
	}
	byState["stale"] = []map[string]any{run(5, "stale", "reissue_enabled", revV1), run(200, "stale", "reissue_as_delivered", revV1)}
	want = append([]int{5}, want...)

	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	var queries []string
	mock.RegisterHandler("GET", testPath+"/"+monthlyID+"/runs", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		queries = append(queries, fmt.Sprintf("state=%s page=%s limit=%s order_by=%s reverse=%s",
			q.Get("state"), q.Get("page"), q.Get("limit"), q.Get("order_by"), q.Get("reverse")))
		page, _ := strconv.Atoi(q.Get("page"))
		limit, _ := strconv.Atoi(q.Get("limit"))
		all := byState[q.Get("state")]
		lo, hi := min((page-1)*limit, len(all)), min(page*limit, len(all))
		testutil.RespondJSON(w, http.StatusOK, map[string]any{"results": all[lo:hi], "total_rows": len(all), "page": page})
	})
	runs, err := NewClient(mock.URL(), testOrgID, 0).FollowingRuns("token", uuid.MustParse(monthlyID), target)
	if err != nil {
		t.Fatalf("FollowingRuns: %v", err)
	}
	wantQueries := []string{
		"state=open page=1 limit=100 order_by=id reverse=false",
		"state=open page=2 limit=100 order_by=id reverse=false",
		"state=rendered page=1 limit=100 order_by=id reverse=false",
		"state=stale page=1 limit=100 order_by=id reverse=false",
	}
	if !reflect.DeepEqual(queries, wantQueries) {
		t.Errorf("queries =\n%s\nwant\n%s", strings.Join(queries, "\n"), strings.Join(wantQueries, "\n"))
	}
	got := make([]int, len(runs))
	for i, r := range runs {
		got[i] = r.ID
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("run ids = %v, want %v", got, want)
	}
}
