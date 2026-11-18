package review

import (
	"bytes"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

// registerDryRunJob mocks GET /dry-runs/<id> with the given state.
func registerDryRunJob(mock *testutil.MockAPIServer, state api.IngestionJobState) {
	mock.RegisterHandler("GET", fmt.Sprintf("/api/orgs/%s/dry-runs/%d", testOrgID, 512),
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.DryRunJobFullSchema{ID: 512, State: state})
		})
}

// registerReviewDetailWithGate mocks GET review with a policy + optional prior
// linked job.
func registerReviewDetailWithGate(mock *testutil.MockAPIServer, priorJob *int) {
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-reviews/"+testReviewUUID.String(),
		func(w http.ResponseWriter, r *http.Request) {
			d := sampleReviewDetail(api.ReviewRequestStatusOpen)
			d.DryRunJobID = priorJob
			d.EffectivePolicy = &api.EffectivePolicyResponseSchema{
				RequiresReview: true, MinReviewers: 2, RequireDryRun: true,
				RequireDryRunSuccess: true, Source: "dataset",
			}
			testutil.RespondJSON(w, http.StatusOK, d)
		})
}

func TestLinkDryRunReviewLevel(t *testing.T) {
	mock := newReviewTestEnv(t)
	registerDryRunJob(mock, api.IngestionJobStateFinished)
	registerReviewDetailWithGate(mock, nil)
	var gotBody map[string]interface{}
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-reviews/"+testReviewUUID.String()+"/link-dry-run",
		func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			testutil.RespondJSON(w, http.StatusOK, api.ReviewResponseSchema{
				ID: testReviewUUID, Status: api.ReviewRequestStatusOpen, DryRunJobID: testutil.IntPtr(512),
			})
		})

	cmd := setupReviewTestCommand()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"review", "link-dry-run", testReviewUUID.String(), "512"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "rule-review/"+testReviewUUID.String()+" dry run linked: job 512") {
		t.Errorf("missing linked line\n%s", out.String())
	}
	if !strings.Contains(out.String(), "Next: qctl describe rule-review "+testReviewUUID.String()) {
		t.Errorf("missing next hint\n%s", out.String())
	}
	if gotBody["dry_run_job_id"] != float64(512) {
		t.Errorf("body dry_run_job_id = %v, want 512", gotBody["dry_run_job_id"])
	}
	// Finished job + require_success → no warning.
	if strings.Contains(errOut.String(), "warning:") {
		t.Errorf("no warning expected for a finished job\n%s", errOut.String())
	}
}

func TestLinkDryRunToCommentRouting(t *testing.T) {
	mock := newReviewTestEnv(t)
	commentUUID := uuid.MustParse("cccccccc-1111-2222-3333-444444444444")
	registerDryRunJob(mock, api.IngestionJobStateFinished)
	registerReviewDetailWithGate(mock, nil)

	reviewLevelCalled := false
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-reviews/"+testReviewUUID.String()+"/link-dry-run",
		func(w http.ResponseWriter, r *http.Request) { reviewLevelCalled = true })
	commentCalled := false
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-reviews/"+testReviewUUID.String()+"/comments/"+commentUUID.String()+"/link-dry-run",
		func(w http.ResponseWriter, r *http.Request) {
			commentCalled = true
			testutil.RespondJSON(w, http.StatusOK, api.ReviewMessageSchema{Message: "linked", Success: true})
		})

	cmd := setupReviewTestCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"review", "link-dry-run", testReviewUUID.String(), "512", "--comment", commentUUID.String()})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reviewLevelCalled {
		t.Error("--comment must not hit the review-level link endpoint")
	}
	if !commentCalled {
		t.Error("comment link endpoint was not called")
	}
	if !strings.Contains(out.String(), "linked to comment "+commentUUID.String()+": job 512") {
		t.Errorf("missing comment-linked line\n%s", out.String())
	}
}

func TestLinkDryRunJobNotFoundShortCircuits(t *testing.T) {
	mock := newReviewTestEnv(t)
	mock.RegisterHandler("GET", fmt.Sprintf("/api/orgs/%s/dry-runs/999", testOrgID),
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusNotFound, map[string]interface{}{
				"detail": map[string]interface{}{"msg": "Not found"},
			})
		})
	linkCalled := false
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-reviews/"+testReviewUUID.String()+"/link-dry-run",
		func(w http.ResponseWriter, r *http.Request) { linkCalled = true })

	cmd := setupReviewTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"review", "link-dry-run", testReviewUUID.String(), "999"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected an error for a missing job")
	}
	if !strings.Contains(err.Error(), "dry-run job 999 not found in this org") {
		t.Errorf("error = %q, want it to name the missing job", err.Error())
	}
	var exitErr *qerrors.Error
	if !stderrors.As(err, &exitErr) || exitErr.ExitCode() != qerrors.ExitNotFound {
		t.Errorf("expected exit code %d (not found), got %v", qerrors.ExitNotFound, err)
	}
	if linkCalled {
		t.Error("link endpoint must not be called when pre-validation fails")
	}
}

func TestLinkDryRunWarnsWhenNotFinishedAndSuccessRequired(t *testing.T) {
	mock := newReviewTestEnv(t)
	registerDryRunJob(mock, api.IngestionJobStateRunning)
	registerReviewDetailWithGate(mock, nil)
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-reviews/"+testReviewUUID.String()+"/link-dry-run",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.ReviewResponseSchema{
				ID: testReviewUUID, Status: api.ReviewRequestStatusOpen, DryRunJobID: testutil.IntPtr(512),
			})
		})

	cmd := setupReviewTestCommand()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"review", "link-dry-run", testReviewUUID.String(), "512"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "warning: job 512 state is running; the enable gate requires state=finished with no rule load errors"
	if !strings.Contains(errOut.String(), want) {
		t.Errorf("stderr missing warning %q\n%s", want, errOut.String())
	}
	// Link still proceeds.
	if !strings.Contains(out.String(), "dry run linked: job 512") {
		t.Errorf("link must proceed after the warning\n%s", out.String())
	}
}

func TestLinkDryRunReportsReplacement(t *testing.T) {
	mock := newReviewTestEnv(t)
	registerDryRunJob(mock, api.IngestionJobStateFinished)
	registerReviewDetailWithGate(mock, testutil.IntPtr(431))
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-reviews/"+testReviewUUID.String()+"/link-dry-run",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.ReviewResponseSchema{
				ID: testReviewUUID, Status: api.ReviewRequestStatusOpen, DryRunJobID: testutil.IntPtr(512),
			})
		})

	cmd := setupReviewTestCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"review", "link-dry-run", testReviewUUID.String(), "512"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "dry run linked: job 512 (replaced job 431)") {
		t.Errorf("missing replacement note\n%s", out.String())
	}
}

func TestLinkDryRunInvalidJobID(t *testing.T) {
	_ = newReviewTestEnv(t)
	cmd := setupReviewTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"review", "link-dry-run", testReviewUUID.String(), "not-an-int"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected an error for a non-integer job id")
	}
}

// The job state in the gate warning is the server's text, echoed to stderr.
func TestLinkDryRunWarningSanitizesTheJobState(t *testing.T) {
	mock := newReviewTestEnv(t)
	registerDryRunJob(mock, "running\x1b[31m")
	registerReviewDetailWithGate(mock, nil)
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-reviews/"+testReviewUUID.String()+"/link-dry-run",
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.ReviewResponseSchema{
				ID: testReviewUUID, Status: api.ReviewRequestStatusOpen, DryRunJobID: testutil.IntPtr(512),
			})
		})

	out, err := runReviewCommand(t, "", "review", "link-dry-run", testReviewUUID.String(), "512")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.ContainsRune(out, '\x1b') || !strings.Contains(out, `state is running\x1b[31m;`) {
		t.Errorf("job state not sanitized: %q", out)
	}
}
