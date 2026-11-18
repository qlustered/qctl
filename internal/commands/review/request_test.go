package review

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/rule_versions"
	"github.com/qlustered/qctl/internal/testutil"
)

// registerRuleResolution serves the rule-revisions list the resolver needs.
func registerRuleResolution(mock *testutil.MockAPIServer) {
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions",
		func(w http.ResponseWriter, r *http.Request) {
			totalRows, page := 1, 1
			testutil.RespondJSON(w, http.StatusOK, rule_versions.RuleRevisionList{
				Results: []rule_versions.RuleRevisionTiny{{
					ID: testRevisionUUID, Name: "dedupe-emails", Release: "2.1.0", State: "draft",
				}},
				TotalRows: &totalRows,
				Page:      &page,
			})
		})
}

func TestRequestByNameDefaultTitleOrgWide(t *testing.T) {
	mock := newReviewTestEnv(t)
	registerRuleResolution(mock)

	var gotBody map[string]interface{}
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRevisionUUID.String()+"/rule-reviews",
		func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			testutil.RespondJSON(w, http.StatusCreated, sampleReview(api.ReviewRequestStatusOpen))
		})

	cmd := setupReviewTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"review", "request", "dedupe-emails"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotBody["title"] != "Review dedupe-emails@2.1.0" {
		t.Errorf("default title = %v", gotBody["title"])
	}
	if v, present := gotBody["dataset_id"]; !present || v != nil {
		t.Errorf("dataset_id = %v (present=%t), want explicit null (org-wide)", v, present)
	}
	out := buf.String()
	for _, want := range []string{
		"rule-review/" + testReviewUUID.String() + " created (status: open, approvals: 0/2)",
		"rule dedupe-emails@2.1.0 is now in_review",
		"Next: qctl describe rule-review " + testReviewUUID.String(),
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n%s", want, out)
		}
	}
}

func TestRequestWithTableAndReviewer(t *testing.T) {
	mock := newReviewTestEnv(t)
	registerRuleResolution(mock)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/users",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, sampleUsersList())
		})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
				"results":    []map[string]interface{}{{"id": 42, "name": "orders"}},
				"total_rows": 1,
				"page":       1,
			})
		})

	var gotBody map[string]interface{}
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRevisionUUID.String()+"/rule-reviews",
		func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			testutil.RespondJSON(w, http.StatusCreated, sampleReview(api.ReviewRequestStatusOpen))
		})

	cmd := setupReviewTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"review", "request", "dedupe-emails",
		"--table", "orders", "--reviewer", "alice@corp.com", "--title", "Custom title"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotBody["title"] != "Custom title" {
		t.Errorf("title = %v", gotBody["title"])
	}
	if gotBody["dataset_id"] != float64(42) {
		t.Errorf("dataset_id = %v, want 42", gotBody["dataset_id"])
	}
	ids, _ := gotBody["assigned_reviewer_ids"].([]interface{})
	if len(ids) != 1 || ids[0] != testReviewerUUID.String() {
		t.Errorf("assigned_reviewer_ids = %v", gotBody["assigned_reviewer_ids"])
	}
}

// --table is a NAME. The fixture is named "42" and has ID 7 — the one case the
// old name-or-id --table could never serve, because datasets.ResolveID tried
// strconv.Atoi first and scoped the review to table 42 instead.
func TestRequestTableIsAlwaysAName(t *testing.T) {
	mock := newReviewTestEnv(t)
	registerRuleResolution(mock)

	var lookedUp string
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets",
		func(w http.ResponseWriter, r *http.Request) {
			lookedUp = r.URL.Query().Get("name")
			testutil.RespondJSON(w, http.StatusOK, api.DatasetsListSchema{
				Results: []api.DataSetTinySchema{{ID: 7, Name: "42"}},
			})
		})
	var gotBody map[string]interface{}
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRevisionUUID.String()+"/rule-reviews",
		func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			testutil.RespondJSON(w, http.StatusCreated, sampleReview(api.ReviewRequestStatusOpen))
		})

	cmd := setupReviewTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"review", "request", "dedupe-emails", "--table", "42"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("--table must be looked up as a name: %v", err)
	}
	if lookedUp != "42" {
		t.Errorf("--table 42 must query name=42, queried %q", lookedUp)
	}
	if gotBody["dataset_id"] != float64(7) {
		t.Errorf("--table 42 must scope the review to dataset_id 7, got %v", gotBody["dataset_id"])
	}
}

// The other half of the pair: --table-id is an ID and triggers no name lookup.
func TestRequestAcceptsTableID(t *testing.T) {
	mock := newReviewTestEnv(t)
	registerRuleResolution(mock)

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets",
		func(_ http.ResponseWriter, _ *http.Request) {
			t.Error("--table-id must not trigger a name lookup")
		})
	var gotBody map[string]interface{}
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRevisionUUID.String()+"/rule-reviews",
		func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			testutil.RespondJSON(w, http.StatusCreated, sampleReview(api.ReviewRequestStatusOpen))
		})

	cmd := setupReviewTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"review", "request", "dedupe-emails", "--table-id", "42"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotBody["dataset_id"] != float64(42) {
		t.Errorf("--table-id 42 must scope the review to dataset_id 42, got %v", gotBody["dataset_id"])
	}
}

// The rule argument is not a table, so --table/--table-id are the only way to
// scope one — and giving both is bad usage (exit 2), not a precedence question.
//
// The table selector's own conflict check is purely local, so it is resolved
// before the rule lookup below: trapping /rule-revisions (rather than merely
// registering it) is the regression fence for that ordering — reverting the
// hoist in runRequest makes resolveRequestRule run first and this test fail.
func TestRequestRejectsBothTableSelectors(t *testing.T) {
	mock := newReviewTestEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions",
		func(_ http.ResponseWriter, _ *http.Request) {
			t.Error("a contradictory table selector must fail before the rule is resolved")
		})
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRevisionUUID.String()+"/rule-reviews",
		func(_ http.ResponseWriter, _ *http.Request) {
			t.Error("a contradictory request must fail before the review is created")
		})

	cmd := setupReviewTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"review", "request", "dedupe-emails", "--table", "orders", "--table-id", "7"})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "table specified more than once") {
		t.Fatalf("expected the conflicting-selector error, got: %v", err)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (ExitBadUsage) for %v", got, qerrors.ExitBadUsage, err)
	}
}

// The dry-run-job lookup is the other GET a contradictory selector must not
// pay for (up to two GETs before the conflict was caught, per the review).
func TestRequestRejectsBothTableSelectorsSkipsDryRunLookup(t *testing.T) {
	mock := newReviewTestEnv(t)
	mock.RegisterHandler("GET", fmt.Sprintf("/api/orgs/%s/dry-runs/999", testOrgID),
		func(_ http.ResponseWriter, _ *http.Request) {
			t.Error("a contradictory table selector must fail before the dry-run job is looked up")
		})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions",
		func(_ http.ResponseWriter, _ *http.Request) {
			t.Error("a contradictory table selector must fail before the rule is resolved")
		})

	cmd := setupReviewTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"review", "request", "dedupe-emails",
		"--table", "orders", "--table-id", "7", "--dry-run-job", "999"})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "table specified more than once") {
		t.Fatalf("expected the conflicting-selector error, got: %v", err)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (ExitBadUsage) for %v", got, qerrors.ExitBadUsage, err)
	}
}

func TestRequestByUUIDFetchesNameForDefaultTitle(t *testing.T) {
	mock := newReviewTestEnv(t)
	// No list handler: UUID input must not hit the resolver list. The default
	// title fetches revision detail via GetOneRuleRevisionDetails (/details).
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRevisionUUID.String()+"/details",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
				"id": testRevisionUUID.String(), "name": "dedupe-emails", "release": "2.1.0",
			})
		})
	var gotBody map[string]interface{}
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRevisionUUID.String()+"/rule-reviews",
		func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			testutil.RespondJSON(w, http.StatusCreated, sampleReview(api.ReviewRequestStatusOpen))
		})

	cmd := setupReviewTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"review", "request", testRevisionUUID.String()})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotBody["title"] != "Review dedupe-emails@2.1.0" {
		t.Errorf("default title = %v (UUID input must fetch name/release)", gotBody["title"])
	}
}

func TestRequestConflictShowsExistingReview(t *testing.T) {
	mock := newReviewTestEnv(t)
	registerRuleResolution(mock)
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRevisionUUID.String()+"/rule-reviews",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusConflict, map[string]interface{}{
				"detail": map[string]interface{}{
					"msg": "An active review already exists",
					"context": map[string]interface{}{
						"code":      "rule_review_already_open",
						"review_id": testReviewUUID.String(),
						"status":    "open",
						"title":     "Review dedupe-emails@2.1.0",
					},
				},
			})
		})

	cmd := setupReviewTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"review", "request", "dedupe-emails"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error on 409")
	}
	id := testReviewUUID.String()
	want := "a review is already open for this revision and scope:\n" +
		"  rule-review/" + id + " (status: open) \"Review dedupe-emails@2.1.0\"\n" +
		"Next: qctl describe rule-review " + id + "  (or close it: qctl review close " + id + ")"
	if got := err.Error(); got != want {
		t.Errorf("message changed (guide request-review quotes it)\n got: %q\nwant: %q", got, want)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (a 409 is a 4xx)", got, qerrors.ExitBadUsage)
	}
}

func TestRequestWithDryRunJobLinks(t *testing.T) {
	t.Run("id", func(t *testing.T) { testRequestWithDryRunJobLinks(t, "512") })
	t.Run("kind ref", func(t *testing.T) { testRequestWithDryRunJobLinks(t, "dry-run-job/512") })
}

func testRequestWithDryRunJobLinks(t *testing.T, ref string) {
	mock := newReviewTestEnv(t)
	registerRuleResolution(mock)
	registerDryRunJob(mock, api.IngestionJobStateFinished)
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRevisionUUID.String()+"/rule-reviews",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusCreated, sampleReview(api.ReviewRequestStatusOpen))
		})
	var linkBody map[string]interface{}
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-reviews/"+testReviewUUID.String()+"/link-dry-run",
		func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&linkBody)
			testutil.RespondJSON(w, http.StatusOK, api.ReviewResponseSchema{
				ID: testReviewUUID, Status: api.ReviewRequestStatusOpen, DryRunJobID: testutil.IntPtr(512),
			})
		})

	cmd := setupReviewTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"review", "request", "dedupe-emails", "--dry-run-job", ref})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"rule-review/" + testReviewUUID.String() + " created",
		"rule-review/" + testReviewUUID.String() + " dry run linked: job 512",
		"Next: qctl describe rule-review " + testReviewUUID.String(),
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n%s", want, out)
		}
	}
	if linkBody["dry_run_job_id"] != float64(512) {
		t.Errorf("link body dry_run_job_id = %v, want 512", linkBody["dry_run_job_id"])
	}
}

func TestRequestDryRunJobLinkFailurePrintsRecovery(t *testing.T) {
	mock := newReviewTestEnv(t)
	registerRuleResolution(mock)
	registerDryRunJob(mock, api.IngestionJobStateFinished)
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRevisionUUID.String()+"/rule-reviews",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusCreated, sampleReview(api.ReviewRequestStatusOpen))
		})
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-reviews/"+testReviewUUID.String()+"/link-dry-run",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusInternalServerError, map[string]interface{}{
				"detail": map[string]interface{}{"msg": "boom"},
			})
		})

	cmd := setupReviewTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"review", "request", "dedupe-emails", "--dry-run-job", "512"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected a non-zero exit when the link step fails after create")
	}
	// No rollback: the created review line is still shown.
	if !strings.Contains(buf.String(), "rule-review/"+testReviewUUID.String()+" created") {
		t.Errorf("created line must still be printed after a link failure\n%s", buf.String())
	}
	if !strings.Contains(err.Error(), "qctl review link-dry-run "+testReviewUUID.String()+" 512") {
		t.Errorf("error must include the recovery command, got: %v", err)
	}
}

func TestRequestDryRunJobNotFoundBeforeCreate(t *testing.T) {
	mock := newReviewTestEnv(t)
	registerRuleResolution(mock)
	mock.RegisterHandler("GET", fmt.Sprintf("/api/orgs/%s/dry-runs/999", testOrgID),
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusNotFound, map[string]interface{}{
				"detail": map[string]interface{}{"msg": "Not found"},
			})
		})
	createCalled := false
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRevisionUUID.String()+"/rule-reviews",
		func(w http.ResponseWriter, r *http.Request) { createCalled = true })

	cmd := setupReviewTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"review", "request", "dedupe-emails", "--dry-run-job", "999"})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "dry-run job 999 not found in this org") {
		t.Fatalf("expected job-not-found before create, got: %v", err)
	}
	if createCalled {
		t.Error("review must not be created when the job pre-validation fails")
	}
}

func TestRequestReviewerNotDesignated(t *testing.T) {
	mock := newReviewTestEnv(t)
	registerRuleResolution(mock)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/users",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, sampleUsersList())
		})
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRevisionUUID.String()+"/rule-reviews",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusUnprocessableEntity, map[string]interface{}{
				"detail": map[string]interface{}{
					"msg":        "Reviewer is not designated",
					"error_code": "REVIEWER_NOT_DESIGNATED",
				},
			})
		})

	cmd := setupReviewTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"review", "request", "dedupe-emails", "--reviewer", "alice@corp.com"})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "designat") {
		t.Fatalf("expected designated-reviewer explanation, got: %v", err)
	}
}

// The rule name and release printed on the second line come from the RESPONSE's
// rule_revision when it carries one, so a crafted name reaches stdout without
// the caller ever typing it.
func TestRequestSanitizesTheServerNameReleaseAndStatus(t *testing.T) {
	mock := newReviewTestEnv(t)
	registerRuleResolution(mock)

	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRevisionUUID.String()+"/rule-reviews",
		func(w http.ResponseWriter, _ *http.Request) {
			review := sampleReview("open\x1b[31m")
			review.RuleRevision.Name = "dedupe\x1b]0;pwned\x07"
			review.RuleRevision.Release = "2.1.0\x00"
			testutil.RespondJSON(w, http.StatusCreated, review)
		})

	out, err := runReviewCommand(t, "", "review", "request", "dedupe-emails")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.ContainsRune(out, '\x1b') || strings.ContainsRune(out, '\x00') || strings.ContainsRune(out, '\x07') {
		t.Fatalf("server text reached the terminal with a live control byte: %q", out)
	}
	for _, want := range []string{`open\x1b[31m`, `dedupe\x1b]0;pwned\x07`, `2.1.0\x00`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing the visible escape %q: %q", want, out)
		}
	}
}

// runRequestSplit runs `review request` with stdout and stderr kept apart.
func runRequestSplit(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := setupReviewTestCommand()
	// As on the real root: a usage dump would land on stdout.
	cmd.SilenceUsage = true
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(append([]string{"review", "request"}, args...))
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

// Under -o json stdout is the review the link answered with, so dry_run_job_id
// is already set, and the result lines move to stderr.
func TestRequestJSONPrintsTheLinkedReview(t *testing.T) {
	mock := newReviewTestEnv(t)
	registerRuleResolution(mock)
	registerDryRunJob(mock, api.IngestionJobStateFinished)
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRevisionUUID.String()+"/rule-reviews",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusCreated, sampleReview(api.ReviewRequestStatusOpen))
		})
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-reviews/"+testReviewUUID.String()+"/link-dry-run",
		func(w http.ResponseWriter, r *http.Request) {
			linked := sampleReview(api.ReviewRequestStatusOpen)
			linked.DryRunJobID = testutil.IntPtr(512)
			testutil.RespondJSON(w, http.StatusOK, linked)
		})

	stdout, stderr, err := runRequestSplit(t, "dedupe-emails", "--dry-run-job", "512", "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got struct {
		ID          string `json:"id"`
		DryRunJobID *int   `json:"dry_run_job_id"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
	}
	if got.ID != testReviewUUID.String() || got.DryRunJobID == nil || *got.DryRunJobID != 512 {
		t.Errorf("review = %+v, want id %s with dry_run_job_id 512", got, testReviewUUID)
	}
	for _, want := range []string{" created (status: open", "dry run linked: job 512", "Next: qctl describe rule-review"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q\n%s", want, stderr)
		}
	}
}

// A link that fails after the create still writes the created review: its id
// is what the recovery command needs.
func TestRequestNameStillPrintsTheReviewWhenTheLinkFails(t *testing.T) {
	mock := newReviewTestEnv(t)
	registerRuleResolution(mock)
	registerDryRunJob(mock, api.IngestionJobStateFinished)
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRevisionUUID.String()+"/rule-reviews",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusCreated, sampleReview(api.ReviewRequestStatusOpen))
		})
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-reviews/"+testReviewUUID.String()+"/link-dry-run",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusInternalServerError, map[string]interface{}{
				"detail": map[string]interface{}{"msg": "boom"},
			})
		})

	stdout, _, err := runRequestSplit(t, "dedupe-emails", "--dry-run-job", "512", "-o", "name")
	if err == nil {
		t.Fatal("expected a non-zero exit when the link step fails after create")
	}
	if want := "rule-review/" + testReviewUUID.String() + "\n"; stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
}

// delete-comment has no body to return, and link-dry-run's --comment path
// returns none either, so both declare table alone and -o json exits 2
// before any request.
func TestReviewVerbsWithNoResultRefuseJSON(t *testing.T) {
	newReviewTestEnv(t)
	id := testReviewUUID.String()
	for _, args := range [][]string{
		{"delete-comment", id, id, "--yes"}, {"link-dry-run", id, "5"},
	} {
		cmd := setupReviewTestCommand()
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetArgs(append(append([]string{"review"}, args...), "-o", "json"))
		err := cmd.Execute()
		if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage ||
			!strings.Contains(err.Error(), `unknown output format "json"`) {
			t.Errorf("review %s -o json: exit %d (%v), want the format refusal", args[0], code, err)
		}
	}
}
