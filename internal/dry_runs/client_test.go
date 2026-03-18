package dry_runs

import (
	"net/http"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
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
			Sampling: api.SamplingMetadata{},
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
				TotalRowsProcessed: 100,
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
