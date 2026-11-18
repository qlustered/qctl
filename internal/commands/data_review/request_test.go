package data_review //nolint:revive // Package name mirrors the hyphenated command.

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const (
	userAda   = "0190a0a0-1111-4222-8333-444444444444"
	userGrace = "0190b0b0-1111-4222-8333-444444444444"
	newGroup  = "0190ee00-1111-4222-8333-444444444444"
	oldGroup  = "0190ee11-1111-4222-8333-444444444444"
)

var (
	submitRoute = "/api/orgs/" + testOrgID + "/data-reviews/batch"
	usersRoute  = "/api/orgs/" + testOrgID + "/users"
	tablesRoute = "/api/orgs/" + testOrgID + "/datasets"
)

// respondUsers serves the directory: Ada Lovelace and Grace Hopper.
func (e *testEnv) respondUsers(status int) {
	e.mock.RegisterHandler("GET", usersRoute, func(w http.ResponseWriter, _ *http.Request) {
		if status != http.StatusOK {
			testutil.RespondJSON(w, status, map[string]any{"detail": map[string]any{"msg": "directory unavailable"}})
			return
		}
		testutil.RespondJSON(w, status, map[string]any{"next": nil, "results": []map[string]any{
			{"id": userAda, "email": "ada@example.com", "first_name": "Ada", "last_name": "Lovelace"},
			{"id": userGrace, "email": "grace@example.com", "first_name": "Grace", "last_name": "Hopper"},
		}})
	})
}

func (e *testEnv) respondTable(name string, id int) {
	e.mock.RegisterHandler("GET", tablesRoute, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(
			`{"total_rows":1,"page":1,"results":[{"id":`+strconv.Itoa(id)+`,"name":"`+name+`","state":"active"}]}`))
	})
}

// respondSubmit serves the batch route and records every raw body it receives.
func (e *testEnv) respondSubmit(status int, body any) *[]map[string]json.RawMessage {
	bodies := &[]map[string]json.RawMessage{}
	e.mock.RegisterHandler("POST", submitRoute, func(w http.ResponseWriter, r *http.Request) {
		var got map[string]json.RawMessage
		_ = json.NewDecoder(r.Body).Decode(&got)
		*bodies = append(*bodies, got)
		testutil.RespondJSON(w, status, body)
	})
	return bodies
}

func groupRef(id, title, createdAt string) map[string]any {
	return map[string]any{"id": id, "title": title, "description": nil, "created_at": createdAt}
}

// submitted is one element of the batch response; groups are its
// review_groups, in the order given.
func submitted(id string, row int, status, createdAt string, groups ...map[string]any) map[string]any {
	return map[string]any{
		"id": id, "dataset_id": 42, "row_id": row, "status": status, "required_approvals": 2,
		"created_at": createdAt, "updated_at": createdAt, "review_groups": groups,
	}
}

func freshSubmission() []map[string]any {
	g := groupRef(newGroup, "Fix Q3 amounts", "2026-09-16T10:00:00Z")
	return []map[string]any{
		submitted(reviewA, 1001, "open", "2026-09-16T10:00:00Z", g),
		submitted(reviewB, 1002, "open", "2026-09-16T10:00:00Z", g),
	}
}

func TestRequest_BodyCarriesTheSubmissionVerbatim(t *testing.T) {
	e := newTestEnv(t)
	bodies := e.respondSubmit(http.StatusCreated, freshSubmission())

	_, _, err := e.exec(t, "request", "--table-id", "42",
		"--row-id", "1002,1001", "--row-id", "7", "--title", "  Fix Q3 amounts ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := e.mock.Requests(), []string{"POST " + submitRoute}; strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("requests = %v, want %v (--table-id and no --reviewer cost one request)", got, want)
	}
	if len(*bodies) != 1 {
		t.Fatalf("POST bodies = %d, want 1", len(*bodies))
	}
	body := (*bodies)[0]
	want := map[string]string{
		"dataset_id":            `42`,
		"row_ids":               `[1002,1001,7]`,
		"title":                 `"Fix Q3 amounts"`,
		"description":           `null`,
		"assigned_reviewer_ids": `null`,
	}
	for key, value := range want {
		if got := string(body[key]); got != value {
			t.Errorf("%s = %s, want %s", key, got, value)
		}
	}
	if len(body) != len(want) {
		t.Errorf("body keys = %d, want %d: %v", len(body), len(want), body)
	}
}

func TestRequest_ReviewersAndTableNameStayWithinThreeRequests(t *testing.T) {
	e := newTestEnv(t)
	e.respondTable("orders", 42)
	e.respondUsers(http.StatusOK)
	bodies := e.respondSubmit(http.StatusCreated, freshSubmission())

	_, _, err := e.exec(t, "request", "--table", "orders", "--row-id", "1001",
		"--title", "t", "--description", "why", "--reviewer", "grace@example.com", "--reviewer", "Ada Lovelace")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"GET " + tablesRoute, "GET " + usersRoute, "POST " + submitRoute}
	if got := e.mock.Requests(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("requests = %v, want %v", got, want)
	}
	body := (*bodies)[0]
	if got, want := string(body["assigned_reviewer_ids"]), `["`+userGrace+`","`+userAda+`"]`; got != want {
		t.Errorf("assigned_reviewer_ids = %s, want %s", got, want)
	}
	if got := string(body["description"]); got != `"why"` {
		t.Errorf("description = %s, want \"why\"", got)
	}
	if got := string(body["dataset_id"]); got != "42" {
		t.Errorf("dataset_id = %s, want 42", got)
	}
}

func TestRequest_ReportsDroppedReviewersForEachReturnedReview(t *testing.T) {
	for _, format := range []string{"table", "json", "yaml", "name"} {
		t.Run(format, func(t *testing.T) {
			e := newTestEnv(t)
			e.respondUsers(http.StatusOK)
			response := freshSubmission()
			response[0]["assigned_reviewers"] = []map[string]any{{"id": userAda}}
			response[1]["assigned_reviewers"] = []map[string]any{{"id": userGrace}}
			g := groupRef(newGroup, "Fix Q3 amounts", "2026-09-16T10:00:00Z")
			allAssigned := submitted(reviewC, 1003, "open", "2026-09-16T10:00:00Z", g)
			allAssigned["assigned_reviewers"] = []map[string]any{{"id": userGrace}, {"id": userAda}}
			e.respondSubmit(http.StatusCreated, append(response, allAssigned))

			stdout, stderr, err := e.exec(t, "request", "--table-id", "42", "--row-id", "1001,1002,1003", "--title", "t",
				"--reviewer", "ada@example.com", "--reviewer", "Grace Hopper", "--reviewer", userGrace, "-o", format)
			if code := qerrors.GetExitCode(err); code != 0 {
				t.Fatalf("exit = %d, want 0 (err: %v)", code, err)
			}
			want := "data-review/" + reviewA + " not assigned (not an active member of this organization): Grace Hopper\n" +
				"data-review/" + reviewB + " not assigned (not an active member of this organization): Ada Lovelace\n"
			if !strings.HasSuffix(stderr, want) || strings.Count(stderr, "not assigned") != 2 {
				t.Errorf("stderr = %q, want only these two warnings: %q", stderr, want)
			}
			if strings.Contains(stdout, "not assigned") {
				t.Errorf("warning leaked to stdout: %q", stdout)
			}
			if got := e.mock.Requests(); len(got) != 2 {
				t.Errorf("requests = %v, want one directory read and one POST", got)
			}
			if format == "json" && !json.Valid([]byte(stdout)) {
				t.Errorf("stdout is not one JSON document: %q", stdout)
			}
			if format == "name" && stdout != "data-review/"+reviewA+"\ndata-review/"+reviewB+"\ndata-review/"+reviewC+"\n" {
				t.Errorf("unexpected name output: %q", stdout)
			}
		})
	}
}

func TestRequest_DroppedUUIDReviewersNeedNoDirectory(t *testing.T) {
	e := newTestEnv(t)
	e.respondUsers(http.StatusForbidden)
	response := freshSubmission()
	response[0]["assigned_reviewers"] = nil
	response[1]["assigned_reviewers"] = []map[string]any{}
	e.respondSubmit(http.StatusCreated, response)

	_, stderr, err := e.exec(t, "request", "--table-id", "42", "--row-id", "1001,1002", "--title", "t",
		"--reviewer", userGrace, "--reviewer", userAda, "--reviewer", userGrace)
	if code := qerrors.GetExitCode(err); code != 0 {
		t.Fatalf("exit = %d, want 0 (err: %v)", code, err)
	}
	want := "data-review/" + reviewA + " not assigned (not an active member of this organization): " + userGrace + ", " + userAda + "\n" +
		"data-review/" + reviewB + " not assigned (not an active member of this organization): " + userGrace + ", " + userAda + "\n"
	if stderr != want {
		t.Errorf("stderr = %q, want %q", stderr, want)
	}
	if got, want := e.mock.Requests(), []string{"POST " + submitRoute}; strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("requests = %v, want %v (no directory sweep)", got, want)
	}
}

func TestRequest_RefusesBadInputBeforeAnyRequest(t *testing.T) {
	tooMany := make([]string, maxSubmitRowIDs+1)
	for i := range tooMany {
		tooMany[i] = strconv.Itoa(i + 1)
	}
	cases := []struct {
		name string
		args []string
	}{
		{"501 row ids", []string{"--table-id", "42", "--row-id", strings.Join(tooMany, ","), "--title", "t"}},
		{"duplicate row id", []string{"--table-id", "42", "--row-id", "7,8", "--row-id", "7", "--title", "t"}},
		{"zero row id", []string{"--table-id", "42", "--row-id", "0", "--title", "t"}},
		{"negative row id", []string{"--table-id", "42", "--row-id=-3", "--title", "t"}},
		{"non-numeric row id", []string{"--table-id", "42", "--row-id", "x", "--title", "t"}},
		{"no row id", []string{"--table-id", "42", "--title", "t"}},
		{"blank title", []string{"--table-id", "42", "--row-id", "7", "--title", " \t "}},
		{"empty title", []string{"--table-id", "42", "--row-id", "7", "--title", ""}},
		{"no title", []string{"--table-id", "42", "--row-id", "7"}},
		{"no table", []string{"--row-id", "7", "--title", "t"}},
		{"both table selectors", []string{"--table", "orders", "--table-id", "42", "--row-id", "7", "--title", "t"}},
		{"positional argument", []string{"orders", "--row-id", "7", "--title", "t"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestEnv(t)
			e.respondTable("orders", 42)
			e.respondSubmit(http.StatusCreated, freshSubmission())

			_, _, err := e.exec(t, append([]string{"request"}, tc.args...)...)
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want 2 (err: %v)", got, err)
			}
			if got := e.mock.Requests(); len(got) != 0 {
				t.Errorf("requests = %v, want none", got)
			}
		})
	}
}

func TestRequest_UnresolvableReviewerPostsNothing(t *testing.T) {
	for _, reviewer := range []string{"nobody@example.com", "Ada"} {
		t.Run(reviewer, func(t *testing.T) {
			e := newTestEnv(t)
			e.respondUsers(http.StatusOK)
			e.respondSubmit(http.StatusCreated, freshSubmission())

			_, _, err := e.exec(t, "request", "--table-id", "42", "--row-id", "7", "--title", "t",
				"--reviewer", "ada@example.com", "--reviewer", reviewer)
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want 2 (err: %v)", got, err)
			}
			if got := e.posts(); got != 0 {
				t.Errorf("POSTs = %d, want 0", got)
			}
		})
	}
}

func TestRequest_PrintsTheNewestGroupAndMarksReusedReviews(t *testing.T) {
	e := newTestEnv(t)
	e.respondTable("q3 orders", 42)
	previous := groupRef(oldGroup, "Earlier pass", "2026-09-01T09:00:00Z")
	current := groupRef(newGroup, "Fix Q3 amounts", "2026-09-16T10:00:00Z")
	e.respondSubmit(http.StatusCreated, []map[string]any{
		// The first review is reused: it predates the new group, and its
		// review_groups lists the older submission AFTER the newer one.
		submitted(reviewA, 1001, "changes_requested", "2026-09-01T09:00:00Z", current, previous),
		// Created in the same transaction as the group: not a re-submission.
		submitted(reviewB, 1002, "open", "2026-09-16T10:00:00Z", current),
	})

	stdout, _, err := e.exec(t, "request", "--table", "q3 orders", "--row-id", "1001,1002", "--title", "Fix Q3 amounts")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := strings.Join([]string{
		`data-review-group/` + newGroup + ` created "Fix Q3 amounts": 2 review(s)`,
		`data-review/` + reviewA + ` row 1001 (status: changes_requested, approvals required: 2) (re-submitted)`,
		`data-review/` + reviewB + ` row 1002 (status: open, approvals required: 2)`,
		`Next: qctl get data-reviews --table 'q3 orders' --group ` + newGroup,
	}, "\n") + "\n"
	if stdout != want {
		t.Errorf("stdout =\n%s\nwant\n%s", stdout, want)
	}
}

func TestRequest_NextLineRepeatsTableID(t *testing.T) {
	e := newTestEnv(t)
	e.respondSubmit(http.StatusCreated, freshSubmission())

	stdout, _, err := e.exec(t, "request", "--table-id", "42", "--row-id", "1001", "--title", "t")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "Next: qctl get data-reviews --table-id 42 --group " + newGroup + "\n"; !strings.HasSuffix(stdout, want) {
		t.Errorf("stdout = %q, want suffix %q", stdout, want)
	}
}

func TestRequest_ResponseWithoutAGroupStillSucceeds(t *testing.T) {
	e := newTestEnv(t)
	e.respondSubmit(http.StatusCreated, []map[string]any{submitted(reviewA, 1001, "open", "2026-09-16T10:00:00Z")})

	stdout, stderr, err := e.exec(t, "request", "--table-id", "42", "--row-id", "1001", "--title", "t")
	if err != nil {
		t.Fatalf("the submission was made; err = %v", err)
	}
	if strings.Contains(stdout, "(re-submitted)") || strings.Contains(stdout, "Next:") {
		t.Errorf("stdout = %q, want neither a re-submitted mark nor a group recipe", stdout)
	}
	if !strings.Contains(stderr, "qctl get data-review-groups --table-id 42") {
		t.Errorf("stderr = %q, want the groups recipe", stderr)
	}
}

func TestRequest_ForbiddenExitsFour(t *testing.T) {
	e := newTestEnv(t)
	e.respondSubmit(http.StatusForbidden, map[string]any{"detail": map[string]any{"msg": "Forbidden"}})

	_, _, err := e.exec(t, "request", "--table-id", "42", "--row-id", "7", "--title", "t")
	if got := qerrors.GetExitCode(err); got != qerrors.ExitUnauthorized {
		t.Errorf("exit code = %d, want 4 (err: %v)", got, err)
	}
	if err == nil || !strings.Contains(err.Error(), "data_review:request") ||
		!strings.Contains(err.Error(), "held by admin, agent, editor, external, member, ops") ||
		!strings.Contains(err.Error(), "access to the table or assigned sources") {
		t.Errorf("err = %v, want the submission permission, granting roles and resource access", err)
	}
}

func TestRequest_ForbiddenUsesGeneratedPermissionMaps(t *testing.T) {
	const operation = "submit_data_review_batch"
	original := api.OperationPermissions[operation]
	api.OperationPermissions[operation] = "test:submission:write"
	originalRoles := api.RolePermissions
	api.RolePermissions = map[string][]string{"test_role": {"test:submission:write"}}
	t.Cleanup(func() {
		api.OperationPermissions[operation] = original
		api.RolePermissions = originalRoles
	})
	e := newTestEnv(t)
	e.respondSubmit(http.StatusForbidden, map[string]any{"detail": map[string]any{"msg": "Source not assigned"}})
	_, _, err := e.exec(t, "request", "--table-id", "42", "--row-id", "7", "--title", "t")
	if qerrors.GetExitCode(err) != qerrors.ExitUnauthorized {
		t.Fatalf("exit code lost: %v", err)
	}
	if !strings.Contains(err.Error(), "test:submission:write, held by test_role") ||
		!strings.Contains(err.Error(), "Source not assigned") {
		t.Errorf("permission maps or server cause lost: %v", err)
	}
}

// The editor role may request a data review but cannot list users, so naming
// every reviewer by UUID has to reach the submission without a directory read.
func TestRequest_UUIDReviewersNeedNoDirectory(t *testing.T) {
	e := newTestEnv(t)
	e.respondUsers(http.StatusForbidden)
	bodies := e.respondSubmit(http.StatusCreated, freshSubmission())

	_, _, err := e.exec(t, "request", "--table-id", "42", "--row-id", "1001", "--title", "t",
		"--reviewer", userGrace, "--reviewer", "  "+userAda+"  ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := e.mock.Requests(), []string{"POST " + submitRoute}; strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("requests = %v, want %v (no directory sweep)", got, want)
	}
	if got, want := string((*bodies)[0]["assigned_reviewer_ids"]), `["`+userGrace+`","`+userAda+`"]`; got != want {
		t.Errorf("assigned_reviewer_ids = %s, want %s", got, want)
	}
}

// A name still needs the directory, and its 403 is still the caller's missing
// permission: exit 4, naming the permission and the way around it.
func TestRequest_ANamedReviewerStillNeedsTheDirectory(t *testing.T) {
	e := newTestEnv(t)
	e.respondUsers(http.StatusForbidden)
	e.respondSubmit(http.StatusCreated, freshSubmission())

	_, _, err := e.exec(t, "request", "--table-id", "42", "--row-id", "1001", "--title", "t",
		"--reviewer", userGrace, "--reviewer", "Ada Lovelace")
	if got := qerrors.GetExitCode(err); got != qerrors.ExitUnauthorized {
		t.Errorf("exit code = %d, want 4 (err: %v)", got, err)
	}
	if got := e.posts(); got != 0 {
		t.Errorf("POSTs = %d, want 0", got)
	}
	for _, want := range []string{`"Ada Lovelace"`, "org:users:list", "full UUID"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}
}

// Under -o json stdout is {group, reviews}: the group id is what
// `get data-reviews --group` takes next, and each review id what every other
// data-review verb takes. The prose lines move to stderr.
func TestRequest_JSONPrintsTheGroupAndReviews(t *testing.T) {
	e := newTestEnv(t)
	e.respondSubmit(http.StatusCreated, freshSubmission())

	stdout, stderr, err := e.exec(t, "request", "--table-id", "42", "--row-id", "1001,1002", "--title", "t", "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got struct {
		Group *struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"group"`
		Reviews []struct {
			ID    string `json:"id"`
			RowID int    `json:"row_id"`
		} `json:"reviews"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
	}
	if got.Group == nil || got.Group.ID != newGroup || got.Group.Title != "Fix Q3 amounts" {
		t.Errorf("group = %+v, want %s %q", got.Group, newGroup, "Fix Q3 amounts")
	}
	if len(got.Reviews) != 2 || got.Reviews[0].ID != reviewA || got.Reviews[1].RowID != 1002 {
		t.Errorf("reviews = %+v, want %s and row 1002", got.Reviews, reviewA)
	}
	for _, want := range []string{"data-review-group/" + newGroup + " created", "Next: qctl get data-reviews"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q:\n%s", want, stderr)
		}
	}
}

// A response that names no group still prints a document, with a null group.
func TestRequest_YAMLWithoutAGroupCarriesANullGroup(t *testing.T) {
	e := newTestEnv(t)
	e.respondSubmit(http.StatusCreated, []map[string]any{submitted(reviewA, 1001, "open", "2026-09-16T10:00:00Z")})

	stdout, _, err := e.exec(t, "request", "--table-id", "42", "--row-id", "1001", "--title", "t", "-o", "yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(stdout, "group: null\n") || !strings.Contains(stdout, "id: "+reviewA) {
		t.Errorf("stdout =\n%s\nwant a null group and the review", stdout)
	}
}

func TestRequest_NamePrintsOneLinePerReview(t *testing.T) {
	e := newTestEnv(t)
	e.respondSubmit(http.StatusCreated, freshSubmission())

	stdout, _, err := e.exec(t, "request", "--table-id", "42", "--row-id", "1001,1002", "--title", "t", "-o", "name")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "data-review/" + reviewA + "\ndata-review/" + reviewB + "\n"; stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
}
