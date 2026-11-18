package dry_runs //nolint:revive // Public package name matches the established resource domain.

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const testOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

func TestGetDryRunJob(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dry-runs/42", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.DryRunJobFullSchema{
			ID:        42,
			DatasetID: 10,
			State:     "finished",
			RuleRunSpecs: []api.RuleRunSpec{
				{Position: 0},
			},
			Sampling: &api.SamplingMetadata{},
		})
	})

	client := NewClient(mock.Server.URL, testOrgID, 0)
	job, err := client.GetDryRunJob("test-token", 42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if job.ID != 42 {
		t.Errorf("expected ID=42, got %d", job.ID)
	}
	if job.DatasetID != 10 {
		t.Errorf("expected DatasetID=10, got %d", job.DatasetID)
	}
	if string(job.State) != "finished" {
		t.Errorf("expected State=finished, got %s", job.State)
	}
}

func TestGetDryRunJob_NotFound(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dry-runs/999", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondError(w, http.StatusNotFound, "Not found")
	})

	client := NewClient(mock.Server.URL, testOrgID, 0)
	_, err := client.GetDryRunJob("test-token", 999)
	if err == nil {
		t.Fatal("expected error for not found")
	}
}

func TestListDryRunJobs(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/10/dry-runs", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.DryRunJobsListSchema{
			Results: []api.DryRunJobTinySchema{
				{ID: 1, DatasetID: 10, DatasetName: "test_table", State: "finished"},
				{ID: 2, DatasetID: 10, DatasetName: "test_table", State: "running"},
			},
		})
	})

	client := NewClient(mock.Server.URL, testOrgID, 0)
	resp, err := client.ListDryRunJobs("test-token", 10, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(resp.Results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(resp.Results))
	}
	if resp.Results[0].ID != 1 {
		t.Errorf("expected first result ID=1, got %d", resp.Results[0].ID)
	}
}

func TestGetDryRunJobPreviewCompact(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dry-runs/42/preview/compact", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.CompactDryRunPreviewResponse{
			DryRunJobID: 42,
			State:       "finished",
			Stats: api.DryRunComparisonStats{
				TotalRowsProcessed:  100,
				RowsWithStateChange: 5,
			},
			Repro: api.AgentReproMetadata{
				SnapshotID: 1,
			},
		})
	})

	client := NewClient(mock.Server.URL, testOrgID, 0)
	preview, err := client.GetDryRunJobPreviewCompact("test-token", 42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if preview.DryRunJobID != 42 {
		t.Errorf("expected DryRunJobID=42, got %d", preview.DryRunJobID)
	}
	if preview.Stats.TotalRowsProcessed != 100 {
		t.Errorf("expected TotalRowsProcessed=100, got %d", preview.Stats.TotalRowsProcessed)
	}
}

func TestGetDryRunJobPreview(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	snapshotAt := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dry-runs/42/preview", func(w http.ResponseWriter, r *http.Request) {
		_ = snapshotAt // referenced for clarity but not used in preview response
		testutil.RespondJSON(w, http.StatusOK, api.DryRunJobPreviewResponse{
			DryRunJobID: 42,
			State:       "finished",
			AllFields:   []string{"name", "email"},
			Stats: api.DryRunComparisonStats{
				TotalRowsProcessed: 50,
			},
		})
	})

	client := NewClient(mock.Server.URL, testOrgID, 0)
	preview, err := client.GetDryRunJobPreview("test-token", 42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if preview.DryRunJobID != 42 {
		t.Errorf("expected DryRunJobID=42, got %d", preview.DryRunJobID)
	}
	if len(preview.AllFields) != 2 {
		t.Errorf("expected 2 fields, got %d", len(preview.AllFields))
	}
}

func TestLaunchDryRunJob(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("PUT", "/api/orgs/"+testOrgID+"/dry-runs", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.LaunchDryJobRunResponseSchema{
			DryRunJobID: 99,
		})
	})

	client := NewClient(mock.Server.URL, testOrgID, 0)

	req := LaunchRequest{
		DataSourceModelID: 5,
	}
	// Set rule_run_specs via the union type
	ruleSpecs := []interface{}{
		map[string]interface{}{"position": 0},
	}
	if err := req.RuleRunSpecs.FromLaunchDryJobRunRequestSchemaRuleRunSpecs0(ruleSpecs); err != nil {
		t.Fatalf("failed to set rule specs: %v", err)
	}

	resp, err := client.LaunchDryRunJob("test-token", req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.DryRunJobID != 99 {
		t.Errorf("expected DryRunJobID=99, got %d", resp.DryRunJobID)
	}
}

// newLaunchRequest builds the smallest launch body the generated client will
// marshal: the union field has no usable zero value, so it is set explicitly.
func newLaunchRequest(t *testing.T) LaunchRequest {
	t.Helper()
	req := LaunchRequest{DataSourceModelID: 456}
	if err := req.RuleRunSpecs.FromLaunchDryJobRunRequestSchemaRuleRunSpecs0([]interface{}{
		map[string]interface{}{"position": 0},
	}); err != nil {
		t.Fatalf("building rule_run_specs: %v", err)
	}
	return req
}

// The launch endpoint enforces the exploratory dry-run cap
// (MAX_CONCURRENT_DRY_RUNS_PER_USER = 3, checked and raised in
// create_and_run_dry_run, atlas capi/capi/controllers/dry_run_controller.py).
// That is a DIFFERENT budget from the one rule instantiation hits -- see
// internal/cmdutil/throttle.go. The backend answers with an object-shaped
// detail carrying DRY_RUN_RATE_LIMIT plus Retry-After: 15
// (dry_run_limit_api_error, cettings/cettings/controllers/dry_run_helpers.py);
// this test uses 7 so a value that happened to match the default cannot pass
// by accident. The header only reaches *apierror.ThrottleError if this client
// forwards resp.HTTPResponse.Header.
func TestLaunchDryRunJob_ThrottleCarriesRetryAfter(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("PUT", "/api/orgs/"+testOrgID+"/dry-runs", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "7")
		testutil.RespondJSON(w, http.StatusTooManyRequests, map[string]interface{}{
			"detail": map[string]interface{}{
				"msg":        "You have 3 dry run(s) in progress (limit: 3). Please wait for an existing dry run to complete before starting a new one.",
				"error_code": "DRY_RUN_RATE_LIMIT",
				"context": map[string]interface{}{
					"current_count":       3,
					"max_allowed":         3,
					"limit_kind":          "concurrent_execution",
					"retry_after_seconds": 15,
				},
			},
		})
	})

	client := NewClient(mock.Server.URL, testOrgID, 0)
	if _, err := client.LaunchDryRunJob("test-token", newLaunchRequest(t)); err == nil {
		t.Fatal("expected an error for a 429")
	} else {
		var throttled *apierror.ThrottleError
		if !errors.As(err, &throttled) {
			t.Fatalf("expected a *apierror.ThrottleError, got %T: %v", err, err)
		}
		if throttled.RetryAfter != 7*time.Second {
			t.Errorf("RetryAfter = %s, want 7s — the response header was dropped", throttled.RetryAfter)
		}
		if code := qerrors.GetExitCode(err); code != 1 {
			t.Errorf("exit code = %d, want 1: a throttle is not bad usage", code)
		}
	}
}

// No header and no context: the throttle still has to be detectable from the
// status, with RetryAfter left at 0 so the caller uses its own backoff.
func TestLaunchDryRunJob_ThrottleWithNoRetryAdviceAtAll(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("PUT", "/api/orgs/"+testOrgID+"/dry-runs", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusTooManyRequests, map[string]interface{}{
			"detail": map[string]interface{}{"msg": "You have 3 dry run(s) in progress (limit: 3)."},
		})
	})

	client := NewClient(mock.Server.URL, testOrgID, 0)
	_, err := client.LaunchDryRunJob("test-token", newLaunchRequest(t))
	var throttled *apierror.ThrottleError
	if !errors.As(err, &throttled) {
		t.Fatalf("expected a *apierror.ThrottleError, got %T: %v", err, err)
	}
	if throttled.RetryAfter != 0 {
		t.Errorf("RetryAfter = %s, want 0 when neither the header nor context carries one", throttled.RetryAfter)
	}
}

// The regression test for the whole ticket. A 429 whose body does not match the
// APIError object the spec declares -- an older server's bare string, or a
// gateway shedding load -- makes ParseLaunchDryRunJobResponse return
// (nil, unmarshal error) before the response is inspectable. It must still
// reach the caller as a throttle carrying the server's Retry-After, via the
// captured headers in internal/client. Without that, a throttle degrades to an
// opaque transport error on the availability path.
func TestLaunchDryRunJob_UnparseableThrottleBodyStillCarriesRetryAfter(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("PUT", "/api/orgs/"+testOrgID+"/dry-runs", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "9")
		testutil.RespondJSON(w, http.StatusTooManyRequests, map[string]interface{}{
			"detail": "You have 3 dry run(s) in progress (limit: 3).",
		})
	})

	client := NewClient(mock.Server.URL, testOrgID, 0)
	_, err := client.LaunchDryRunJob("test-token", newLaunchRequest(t))
	var throttled *apierror.ThrottleError
	if !errors.As(err, &throttled) {
		t.Fatalf("expected a *apierror.ThrottleError, got %T: %v", err, err)
	}
	if throttled.RetryAfter != 9*time.Second {
		t.Errorf("RetryAfter = %s, want 9s — the header was dropped when the body failed to parse", throttled.RetryAfter)
	}
	if code := qerrors.GetExitCode(err); code != 1 {
		t.Errorf("exit code = %d, want 1: a throttle is not bad usage", code)
	}
}
