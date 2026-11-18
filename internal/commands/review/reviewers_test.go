package review

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

func registerUsers(mock *testutil.MockAPIServer) {
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/users",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, sampleUsersList())
		})
}

func TestAssignResolvesEmailAndPrintsStatus(t *testing.T) {
	mock := newReviewTestEnv(t)
	registerUsers(mock)
	var gotBody map[string]interface{}
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-reviews/"+testReviewUUID.String()+"/assign",
		func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			testutil.RespondJSON(w, http.StatusOK, sampleReviewDetail(api.ReviewRequestStatusOpen))
		})

	cmd := setupReviewTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"review", "assign", testReviewUUID.String(), "--reviewer", "alice@corp.com"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ids, _ := gotBody["reviewer_ids"].([]interface{})
	if len(ids) != 1 || ids[0] != testReviewerUUID.String() {
		t.Errorf("reviewer_ids = %v", gotBody["reviewer_ids"])
	}
	want := "rule-review/" + testReviewUUID.String() + " reviewers assigned (status: open)"
	if !strings.Contains(buf.String(), want) {
		t.Errorf("output missing %q\n%s", want, buf.String())
	}
}

func TestAssignRequiresReviewerFlag(t *testing.T) {
	_ = newReviewTestEnv(t)
	cmd := setupReviewTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"review", "assign", testReviewUUID.String()})

	if err := cmd.Execute(); err == nil {
		t.Fatal("assign without --reviewer must fail")
	}
}

func TestAssignReviewNotOpenReportsStatus(t *testing.T) {
	mock := newReviewTestEnv(t)
	registerUsers(mock)
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-reviews/"+testReviewUUID.String()+"/assign",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusUnprocessableEntity, map[string]interface{}{
				"detail": map[string]interface{}{
					"msg":        "Review is not open for reviewer changes (status: rejected)",
					"error_code": "REVIEW_NOT_OPEN",
				},
			})
		})

	cmd := setupReviewTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"review", "assign", testReviewUUID.String(), "--reviewer", "alice@corp.com"})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "(status: rejected)") {
		t.Fatalf("expected current status in error, got: %v", err)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (a 422 is a 4xx)", got, qerrors.ExitBadUsage)
	}
}

func TestUnassignPrintsBackendStatus(t *testing.T) {
	// Retracting an approver's vote can reopen an approved review.
	mock := newReviewTestEnv(t)
	registerUsers(mock)
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-reviews/"+testReviewUUID.String()+"/unassign",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, sampleReviewDetail(api.ReviewRequestStatusOpen))
		})

	cmd := setupReviewTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"review", "unassign", testReviewUUID.String(), "--reviewer", "alice@corp.com"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "rule-review/" + testReviewUUID.String() + " reviewers unassigned (status: open)"
	if !strings.Contains(buf.String(), want) {
		t.Errorf("output missing %q\n%s", want, buf.String())
	}
}

func TestRerequestNudgesEveryoneAndListsNames(t *testing.T) {
	mock := newReviewTestEnv(t)
	registerUsers(mock)
	var gotBody map[string]interface{}
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-reviews/"+testReviewUUID.String()+"/re-request",
		func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			ids := []openapi_types.UUID{testReviewerUUID}
			testutil.RespondJSON(w, http.StatusOK, api.RerequestReviewersResponseSchema{
				ReviewID: testReviewUUID, Status: api.ReviewRequestStatusOpen,
				RerequestedReviewerIds: &ids,
			})
		})

	cmd := setupReviewTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"review", "re-request", testReviewUUID.String()})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, present := gotBody["reviewer_ids"]; present {
		t.Errorf("empty nudge must omit reviewer_ids, got %v", gotBody["reviewer_ids"])
	}
	out := buf.String()
	if !strings.Contains(out, "rule-review/"+testReviewUUID.String()+" re-requested (status: open)") {
		t.Errorf("missing re-requested line\n%s", out)
	}
	if !strings.Contains(out, "Nudged: Alice Chen") {
		t.Errorf("missing nudged names\n%s", out)
	}
}

func TestRerequestNotActiveMapsError(t *testing.T) {
	mock := newReviewTestEnv(t)
	registerUsers(mock)
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-reviews/"+testReviewUUID.String()+"/re-request",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusConflict, map[string]interface{}{
				"detail": map[string]interface{}{
					"msg":        "Review is not active",
					"error_code": "review_not_active",
					"context":    map[string]interface{}{"status": "closed"},
				},
			})
		})

	cmd := setupReviewTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"review", "re-request", testReviewUUID.String()})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error")
	}
	if got, want := err.Error(), "review is closed; nudges only work on active reviews"; got != want {
		t.Errorf("message = %q, want %q", got, want)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (a 409 is a 4xx)", got, qerrors.ExitBadUsage)
	}
}

// registerUsersForbidden refuses the directory the way the backend refuses a
// caller without org:users:list.
func registerUsersForbidden(mock *testutil.MockAPIServer) {
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/users",
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusForbidden,
				map[string]any{"detail": map[string]any{"msg": "no access to the user list"}})
		})
}

// reviewerVerb is one command that takes --reviewer: its arguments, the POST
// it exists to send, and the body field that carries the resolved ids.
type reviewerVerb struct {
	args   []string
	route  string
	idsKey string
}

// reviewerVerbNames are the verbs registerReviewerVerbs serves.
var reviewerVerbNames = []string{"request", "assign", "unassign", "re-request"}

// registerReviewerVerbs serves every route a reviewer verb needs besides the
// directory, capturing the POST bodies by route.
func registerReviewerVerbs(mock *testutil.MockAPIServer) (map[string]reviewerVerb, map[string]map[string]interface{}) {
	reviewRoute := "/api/orgs/" + testOrgID + "/rule-reviews/" + testReviewUUID.String()
	verbs := map[string]reviewerVerb{
		"request": {[]string{"review", "request", "dedupe-emails"},
			"/api/orgs/" + testOrgID + "/rule-revisions/" + testRevisionUUID.String() + "/rule-reviews", "assigned_reviewer_ids"},
		"assign":     {[]string{"review", "assign", testReviewUUID.String()}, reviewRoute + "/assign", "reviewer_ids"},
		"unassign":   {[]string{"review", "unassign", testReviewUUID.String()}, reviewRoute + "/unassign", "reviewer_ids"},
		"re-request": {[]string{"review", "re-request", testReviewUUID.String()}, reviewRoute + "/re-request", "reviewer_ids"},
	}
	registerRuleResolution(mock)
	bodies := map[string]map[string]interface{}{}
	for name, v := range verbs {
		route, name := v.route, name
		mock.RegisterHandler("POST", route, func(w http.ResponseWriter, r *http.Request) {
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			bodies[route] = body
			switch name {
			case "request":
				testutil.RespondJSON(w, http.StatusCreated, sampleReview(api.ReviewRequestStatusOpen))
			case "re-request":
				testutil.RespondJSON(w, http.StatusOK, api.RerequestReviewersResponseSchema{
					ReviewID: testReviewUUID, Status: api.ReviewRequestStatusOpen,
				})
			default:
				testutil.RespondJSON(w, http.StatusOK, sampleReviewDetail(api.ReviewRequestStatusOpen))
			}
		})
	}
	return verbs, bodies
}

// A reviewer named by UUID resolves without the directory, so a caller who
// cannot list users still reaches every reviewer route.
func TestReviewerVerbsResolveAUUIDWithoutTheDirectory(t *testing.T) {
	for _, verb := range reviewerVerbNames {
		t.Run(verb, func(t *testing.T) {
			mock := newReviewTestEnv(t)
			registerUsersForbidden(mock)
			verbs, bodies := registerReviewerVerbs(mock)
			v := verbs[verb]

			out, err := runReviewCommand(t, "", append(v.args, "--reviewer", testReviewerUUID.String())...)
			if err != nil {
				t.Fatalf("unexpected error: %v\n%s", err, out)
			}
			for _, req := range mock.Requests() {
				if strings.HasSuffix(req, "/users") {
					t.Errorf("requests = %v, want no directory read", mock.Requests())
				}
			}
			ids, _ := bodies[v.route][v.idsKey].([]interface{})
			if len(ids) != 1 || ids[0] != testReviewerUUID.String() {
				t.Errorf("%s = %v, want [%s]", v.idsKey, bodies[v.route][v.idsKey], testReviewerUUID)
			}
		})
	}
}

// A name or an email still needs the directory, and its refusal is the
// caller's missing permission: exit 4, naming it, with nothing sent.
func TestReviewerVerbsStillNeedTheDirectoryForAName(t *testing.T) {
	for _, verb := range reviewerVerbNames {
		t.Run(verb, func(t *testing.T) {
			mock := newReviewTestEnv(t)
			registerUsersForbidden(mock)
			verbs, bodies := registerReviewerVerbs(mock)
			v := verbs[verb]

			_, err := runReviewCommand(t, "", append(v.args,
				"--reviewer", testReviewerUUID.String(), "--reviewer", "alice@corp.com")...)
			if got := qerrors.GetExitCode(err); got != qerrors.ExitUnauthorized {
				t.Errorf("exit code = %d, want 4 (err: %v)", got, err)
			}
			if len(bodies) != 0 {
				t.Errorf("POSTed %v, want nothing sent", bodies)
			}
			for _, want := range []string{`"alice@corp.com"`, "org:users:list", "full UUID"} {
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Errorf("error = %v, want it to contain %q", err, want)
				}
			}
		})
	}
}

// re-request names who it nudged, but only after the nudges went out: a
// refused directory costs the names, not the command.
func TestRerequestNamesTheNudgedAfterTheRouteAndSurvivesARefusedDirectory(t *testing.T) {
	mock := newReviewTestEnv(t)
	registerUsersForbidden(mock)
	route := "/api/orgs/" + testOrgID + "/rule-reviews/" + testReviewUUID.String() + "/re-request"
	mock.RegisterHandler("POST", route, func(w http.ResponseWriter, _ *http.Request) {
		ids := []openapi_types.UUID{testReviewerUUID}
		testutil.RespondJSON(w, http.StatusOK, api.RerequestReviewersResponseSchema{
			ReviewID: testReviewUUID, Status: api.ReviewRequestStatusOpen,
			RerequestedReviewerIds: &ids,
		})
	})

	out, err := runReviewCommand(t, "", "review", "re-request", testReviewUUID.String())
	if err != nil {
		t.Fatalf("the nudges went out, so the command succeeds: %v\n%s", err, out)
	}
	want := []string{"POST " + route, "GET /api/orgs/" + testOrgID + "/users"}
	if got := mock.Requests(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("requests = %v, want %v", got, want)
	}
	for _, want := range []string{"Warning: user directory unavailable", "Nudged: " + testReviewerUUID.String()} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n%s", want, out)
		}
	}
}

// The status and a reviewer's name are the server's text, echoed to stdout;
// an ESC in either would recolour the terminal from a line read as qctl's own.
func TestReviewerVerbsSanitizeTheServerText(t *testing.T) {
	const crafted = "open\x1b[31m\x00"
	reviewRoute := "/api/orgs/" + testOrgID + "/rule-reviews/" + testReviewUUID.String()
	for _, tc := range []struct {
		verb  string
		reply func() interface{}
		want  []string
	}{
		{"assign", func() interface{} { return sampleReviewDetail(crafted) }, []string{`open\x1b[31m\x00`}},
		{"unassign", func() interface{} { return sampleReviewDetail(crafted) }, []string{`open\x1b[31m\x00`}},
		{"re-request", func() interface{} {
			ids := []openapi_types.UUID{testReviewerUUID}
			return api.RerequestReviewersResponseSchema{
				ReviewID: testReviewUUID, Status: crafted, RerequestedReviewerIds: &ids,
			}
		}, []string{`open\x1b[31m\x00`, `Nudged: Alice\x1b]0;pwned\x07 Chen`}},
	} {
		t.Run(tc.verb, func(t *testing.T) {
			mock := newReviewTestEnv(t)
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/users",
				func(w http.ResponseWriter, _ *http.Request) {
					users := sampleUsersList()
					users.Results[0].FirstName = "Alice\x1b]0;pwned\x07"
					testutil.RespondJSON(w, http.StatusOK, users)
				})
			mock.RegisterHandler("POST", reviewRoute+"/"+tc.verb,
				func(w http.ResponseWriter, _ *http.Request) {
					testutil.RespondJSON(w, http.StatusOK, tc.reply())
				})

			out, err := runReviewCommand(t, "", "review", tc.verb, testReviewUUID.String(),
				"--reviewer", "alice@corp.com")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if strings.ContainsAny(out, "\x1b\x00\x07") {
				t.Fatalf("server text reached the terminal with a live control byte: %q", out)
			}
			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Errorf("missing the visible escape %q: %q", want, out)
				}
			}
		})
	}
}
