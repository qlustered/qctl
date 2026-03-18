package ingestion

import (
	"net/http"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/testutil"
)

// testOrgID is a valid UUID used for testing
const testOrgID = "00000000-0000-0000-0000-000000000000"

// Test fixtures using generated types

func sampleIngestionJobTiny() IngestionJobTiny {
	cleanRows := 50000
	badRows := 5
	ignoredRows := 0
	storedItemID := 100
	alertItemID := 200
	isAlertResolved := false
	updatedAt := time.Date(2025, 1, 18, 10, 0, 0, 0, time.UTC)

	return IngestionJobTiny{
		ID:               101,
		DatasetID:        1,
		DatasetName:      "user_analytics",
		Key:              "s3://bucket/events_2025_01_18.csv",
		FileName:         "events_2025_01_18.csv",
		StoredItemID:     &storedItemID,
		State:            api.IngestionJobState("running"),
		AlertItemID:      &alertItemID,
		IsAlertResolved:  &isAlertResolved,
		CleanRowsCount:   &cleanRows,
		BadRowsCount:     &badRows,
		IgnoredRowsCount: &ignoredRows,
		Msg:              nil,
		TryCount:         1,
		IsDryRun:         false,
		UpdatedAt:        updatedAt,
	}
}

func sampleIngestionJobsList() []IngestionJobTiny {
	updatedAt1 := time.Date(2025, 1, 19, 8, 0, 0, 0, time.UTC)
	updatedAt2 := time.Date(2025, 1, 17, 14, 30, 0, 0, time.UTC)

	return []IngestionJobTiny{
		sampleIngestionJobTiny(),
		{
			ID:          102,
			DatasetID:   1,
			DatasetName: "user_analytics",
			Key:         "s3://bucket/events_2025_01_19.csv",
			FileName:    "events_2025_01_19.csv",
			State:       api.IngestionJobState("waiting"),
			TryCount:    0,
			IsDryRun:    false,
			UpdatedAt:   updatedAt1,
		},
		{
			ID:          103,
			DatasetID:   2,
			DatasetName: "product_catalog",
			Key:         "s3://bucket/products_2025_01_17.csv",
			FileName:    "products_2025_01_17.csv",
			State:       api.IngestionJobState("completed"),
			TryCount:    1,
			IsDryRun:    false,
			UpdatedAt:   updatedAt2,
		},
	}
}

func sampleIngestionJobFull() IngestionJobFull {
	cleanRows := 50000
	badRows := 5
	ignoredRows := 0
	storedItemID := 100
	alertItemID := 200
	isAlertResolved := false
	createdAt := time.Date(2025, 1, 18, 9, 55, 0, 0, time.UTC)
	updatedAt := time.Date(2025, 1, 18, 10, 15, 0, 0, time.UTC)
	startedAt := time.Date(2025, 1, 18, 10, 0, 0, 0, time.UTC)

	return IngestionJobFull{
		ID:                  101,
		DatasetID:           1,
		DatasetName:         "user_analytics",
		Key:                 "s3://bucket/events_2025_01_18.csv",
		FileName:            "events_2025_01_18.csv",
		StoredItemID:        &storedItemID,
		State:               api.IngestionJobState("running"),
		AlertItemID:         &alertItemID,
		IsAlertResolved:     &isAlertResolved,
		CleanRowsCount:      &cleanRows,
		BadRowsCount:        &badRows,
		IgnoredRowsCount:    &ignoredRows,
		Msg:                 nil,
		TryCount:            1,
		AttemptID:           0,
		IsDryRun:            false,
		UpdatedAt:           updatedAt,
		DataSourceModelID:   10,
		DataSourceModelName: "S3 Bucket - Events",
		SettingsModelID:     20,
		CreatedAt:           createdAt,
		StartedAt:           &startedAt,
		FinishedAt:          nil,
	}
}

// Test functions

func TestClient_GetIngestionJobs(t *testing.T) {
	tests := []struct {
		name         string
		params       GetIngestionJobsParams
		mockResponse IngestionJobsListResponse
		mockStatus   int
		wantErr      bool
		wantCount    int
	}{
		{
			name: "successful fetch with results",
			params: GetIngestionJobsParams{
				Limit: 100,
			},
			mockResponse: IngestionJobsListResponse{
				Results:   []IngestionJobTiny{sampleIngestionJobTiny()},
				TotalRows: intPtr(1),
				Page:      intPtr(1),
			},
			mockStatus: http.StatusOK,
			wantErr:    false,
			wantCount:  1,
		},
		{
			name:   "empty results",
			params: GetIngestionJobsParams{},
			mockResponse: IngestionJobsListResponse{
				Results:   []IngestionJobTiny{},
				TotalRows: intPtr(0),
				Page:      intPtr(1),
			},
			mockStatus: http.StatusOK,
			wantErr:    false,
			wantCount:  0,
		},
		{
			name:       "unauthorized error",
			params:     GetIngestionJobsParams{},
			mockStatus: http.StatusUnauthorized,
			wantErr:    true,
		},
		{
			name: "with state filter",
			params: GetIngestionJobsParams{
				States: []string{"running", "waiting"},
			},
			mockResponse: IngestionJobsListResponse{
				Results:   []IngestionJobTiny{sampleIngestionJobTiny()},
				TotalRows: intPtr(1),
				Page:      intPtr(1),
			},
			mockStatus: http.StatusOK,
			wantErr:    false,
			wantCount:  1,
		},
		{
			name: "with dataset_id filter",
			params: GetIngestionJobsParams{
				DatasetID: intPtr(1),
			},
			mockResponse: IngestionJobsListResponse{
				Results:   []IngestionJobTiny{sampleIngestionJobTiny()},
				TotalRows: intPtr(1),
				Page:      intPtr(1),
			},
			mockStatus: http.StatusOK,
			wantErr:    false,
			wantCount:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/ingestion-jobs", func(w http.ResponseWriter, r *http.Request) {
				if tt.mockStatus != http.StatusOK {
					testutil.RespondError(w, tt.mockStatus, "error")
					return
				}
				testutil.RespondJSON(w, tt.mockStatus, tt.mockResponse)
			})

			client, _ := NewClient(mock.Server.URL, testOrgID, 0)
			resp, err := client.GetIngestionJobs("test-token", tt.params)

			if (err != nil) != tt.wantErr {
				t.Errorf("GetIngestionJobs() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				if len(resp.Results) != tt.wantCount {
					t.Errorf("GetIngestionJobs() count = %d, want %d", len(resp.Results), tt.wantCount)
				}
			}
		})
	}
}

func TestClient_GetIngestionJob(t *testing.T) {
	tests := []struct {
		name       string
		jobID      int
		mockStatus int
		wantErr    bool
	}{
		{
			name:       "successful fetch",
			jobID:      101,
			mockStatus: http.StatusOK,
			wantErr:    false,
		},
		{
			name:       "job not found",
			jobID:      999,
			mockStatus: http.StatusNotFound,
			wantErr:    true,
		},
		{
			name:       "unauthorized",
			jobID:      101,
			mockStatus: http.StatusUnauthorized,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/ingestion-jobs/101", func(w http.ResponseWriter, r *http.Request) {
				if tt.mockStatus != http.StatusOK {
					testutil.RespondError(w, tt.mockStatus, "error")
					return
				}
				testutil.RespondJSON(w, tt.mockStatus, sampleIngestionJobFull())
			})

			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/ingestion-jobs/999", func(w http.ResponseWriter, r *http.Request) {
				testutil.RespondError(w, http.StatusNotFound, "Ingestion job not found")
			})

			client, _ := NewClient(mock.Server.URL, testOrgID, 0)
			job, err := client.GetIngestionJob("test-token", tt.jobID)

			if (err != nil) != tt.wantErr {
				t.Errorf("GetIngestionJob() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				if job == nil {
					t.Fatal("Expected job, got nil")
				}
				if job.ID != 101 {
					t.Errorf("Expected job ID 101, got %d", job.ID)
				}
			}
		})
	}
}

func TestClient_RunIngestionJob(t *testing.T) {
	tests := []struct {
		name       string
		jobID      int
		mockStatus int
		wantErr    bool
	}{
		{
			name:       "successful run",
			jobID:      101,
			mockStatus: http.StatusOK,
			wantErr:    false,
		},
		{
			name:       "job not found",
			jobID:      999,
			mockStatus: http.StatusNotFound,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/ingestion-jobs/101/run-ingestion-job", func(w http.ResponseWriter, r *http.Request) {
				if tt.mockStatus != http.StatusOK {
					testutil.RespondError(w, tt.mockStatus, "error")
					return
				}
				testutil.RespondJSON(w, tt.mockStatus, map[string]string{"result": "Job started"})
			})

			mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/ingestion-jobs/999/run-ingestion-job", func(w http.ResponseWriter, r *http.Request) {
				testutil.RespondError(w, http.StatusNotFound, "Job not found")
			})

			client, _ := NewClient(mock.Server.URL, testOrgID, 0)
			resp, err := client.RunIngestionJob("test-token", tt.jobID)

			if (err != nil) != tt.wantErr {
				t.Errorf("RunIngestionJob() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr && resp == nil {
				t.Error("Expected response, got nil")
			}
		})
	}
}

func TestClient_RunMultipleIngestionJobs(t *testing.T) {
	tests := []struct {
		name       string
		jobIDs     []int
		mockStatus int
		wantErr    bool
	}{
		{
			name:       "successful run multiple",
			jobIDs:     []int{101, 102},
			mockStatus: http.StatusOK,
			wantErr:    false,
		},
		{
			name:       "empty job list",
			jobIDs:     []int{},
			mockStatus: http.StatusOK,
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/ingestion-jobs/run-ingestion-jobs", func(w http.ResponseWriter, r *http.Request) {
				if tt.mockStatus != http.StatusOK {
					testutil.RespondError(w, tt.mockStatus, "error")
					return
				}
				testutil.RespondJSON(w, tt.mockStatus, map[string]string{"result": "Jobs started"})
			})

			client, _ := NewClient(mock.Server.URL, testOrgID, 0)
			resp, err := client.RunMultipleIngestionJobs("test-token", tt.jobIDs)

			if (err != nil) != tt.wantErr {
				t.Errorf("RunMultipleIngestionJobs() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr && resp == nil {
				t.Error("Expected response, got nil")
			}
		})
	}
}

func TestClient_KillIngestionJob(t *testing.T) {
	tests := []struct {
		name       string
		jobID      int
		mockStatus int
		wantErr    bool
	}{
		{
			name:       "successful kill",
			jobID:      101,
			mockStatus: http.StatusOK,
			wantErr:    false,
		},
		{
			name:       "job not found",
			jobID:      999,
			mockStatus: http.StatusNotFound,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/ingestion-jobs/101/kill-ingestion-job", func(w http.ResponseWriter, r *http.Request) {
				if tt.mockStatus != http.StatusOK {
					testutil.RespondError(w, tt.mockStatus, "error")
					return
				}
				testutil.RespondJSON(w, tt.mockStatus, map[string]string{"result": "Job killed"})
			})

			mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/ingestion-jobs/999/kill-ingestion-job", func(w http.ResponseWriter, r *http.Request) {
				testutil.RespondError(w, http.StatusNotFound, "Job not found")
			})

			client, _ := NewClient(mock.Server.URL, testOrgID, 0)
			resp, err := client.KillIngestionJob("test-token", tt.jobID)

			if (err != nil) != tt.wantErr {
				t.Errorf("KillIngestionJob() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr && resp == nil {
				t.Error("Expected response, got nil")
			}
		})
	}
}

func TestClient_KillMultipleIngestionJobs(t *testing.T) {
	tests := []struct {
		name       string
		jobIDs     []int
		mockStatus int
		wantErr    bool
	}{
		{
			name:       "successful kill multiple",
			jobIDs:     []int{101, 102},
			mockStatus: http.StatusOK,
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/ingestion-jobs/kill-ingestion-jobs", func(w http.ResponseWriter, r *http.Request) {
				if tt.mockStatus != http.StatusOK {
					testutil.RespondError(w, tt.mockStatus, "error")
					return
				}
				testutil.RespondJSON(w, tt.mockStatus, map[string]string{"result": "Jobs killed"})
			})

			client, _ := NewClient(mock.Server.URL, testOrgID, 0)
			resp, err := client.KillMultipleIngestionJobs("test-token", tt.jobIDs)

			if (err != nil) != tt.wantErr {
				t.Errorf("KillMultipleIngestionJobs() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr && resp == nil {
				t.Error("Expected response, got nil")
			}
		})
	}
}

// Helper functions
func intPtr(i int) *int {
	return &i
}
