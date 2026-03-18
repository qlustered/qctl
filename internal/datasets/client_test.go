package datasets

import (
	"net/http"
	"testing"

	"github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/testutil"
)

const testOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

// Test fixtures using generated types

func sampleDatasetTiny() DatasetTiny {
	cleanRows := 10000
	badRows := 5
	progress := 100
	orgID := types.UUID{}
	orgID.UnmarshalText([]byte("550e8400-e29b-41d4-a716-446655440000"))

	return DatasetTiny{
		ID:               1,
		VersionID:        10,
		Name:             "user_data",
		State:            api.DataSetStateActive,
		DestinationName:  "postgres_main",
		UnresolvedAlerts: 0,
		OrganizationID:   orgID,
		CleanRowsCount:   &cleanRows,
		BadRowsCount:     &badRows,
		ProgressPercent:  &progress,
		Users:            []api.UserInfoTinyDictSchema{},
	}
}

func sampleDatasetsList() []DatasetTiny {
	orgID := types.UUID{}
	orgID.UnmarshalText([]byte("550e8400-e29b-41d4-a716-446655440000"))

	return []DatasetTiny{
		sampleDatasetTiny(),
		{
			ID:               2,
			VersionID:        5,
			Name:             "product_catalog",
			State:            api.DataSetStateDisabled,
			DestinationName:  "snowflake_prod",
			UnresolvedAlerts: 2,
			OrganizationID:   orgID,
			Users:            []api.UserInfoTinyDictSchema{},
		},
		{
			ID:               3,
			VersionID:        15,
			Name:             "user_analytics",
			State:            api.DataSetStateDeleted,
			DestinationName:  "postgres_main",
			UnresolvedAlerts: 0,
			OrganizationID:   orgID,
			Users:            []api.UserInfoTinyDictSchema{},
		},
	}
}

func sampleDatasetFull() DatasetFull {
	orgID := types.UUID{}
	orgID.UnmarshalText([]byte("550e8400-e29b-41d4-a716-446655440000"))

	encryptBackup := true
	quarantine := false
	removeOutliers := false
	shouldReprocess := false
	detectAnomalies := false
	enableCellMove := false
	strictDatetime := false
	guessDatetime := false
	enableRowLogs := false

	return DatasetFull{
		ID:                          1,
		VersionID:                   10,
		Name:                        "user_data",
		SchemaName:                  "public",
		DatabaseName:                "analytics",
		TableName:                   "users",
		MigrationPolicy:             api.ApplyAsap,
		State:                       api.DataSetStateActive,
		OrganizationID:              orgID,
		EncryptRawDataDuringBackup:  &encryptBackup,
		QuarantineRowsUntilApproved: &quarantine,
		RemoveOutliersWhenRecommendingNumericValidators: &removeOutliers,
		ShouldReprocess:                    &shouldReprocess,
		DetectAnomalies:                    &detectAnomalies,
		EnableCellMoveSuggestions:          &enableCellMove,
		StrictlyOneDatetimeFormatInAColumn: &strictDatetime,
		GuessDatetimeFormatInIngestion:     &guessDatetime,
		EnableRowLogs:                      &enableRowLogs,
		AnomalyThreshold:                   50,
		MaxRetryCount:                      3,
		MaxTriesToFixJSON:                  3,
		BackupKeyFormat:                    "{dataset_id}/{data_source_id}/{datetime}",
		BackupSettingsID:                   1,
		DestinationID:                      1,
		DestinationName:                    "postgres_main",
		DataLoadingProcess:                 api.Snapshot,
		SettingsModel:                      api.SettingsSchema{},
		BackupSettingsList:                 []api.OptionTypeSchema{},
		DestinationsList:                   []api.OptionTypeSchema{},
	}
}

func sampleDatasetStatsResponse() DatasetStatsResponse {
	badRows := 5
	return DatasetStatsResponse{
		BadRowsCount: &badRows,
	}
}

func sampleJobRunningCountResponse() JobRunningCountResponse {
	return JobRunningCountResponse{
		IngestionJobCount:        2,
		WaitingIngestionJobCount: 1,
		TrainingJobCount:         0,
		ServerTime:               1705320000,
	}
}

func TestClient_GetDatasets(t *testing.T) {
	tests := []struct {
		name         string
		params       GetDatasetsParams
		mockResponse DatasetsListResponse
		mockStatus   int
		wantErr      bool
		wantCount    int
	}{
		{
			name: "successful fetch with results",
			params: GetDatasetsParams{
				Limit: 100,
			},
			mockResponse: DatasetsListResponse{
				Results:   []DatasetTiny{sampleDatasetTiny()},
				TotalRows: intPtr(1),
				Page:      intPtr(1),
			},
			mockStatus: http.StatusOK,
			wantErr:    false,
			wantCount:  1,
		},
		{
			name:   "empty results",
			params: GetDatasetsParams{},
			mockResponse: DatasetsListResponse{
				Results:   []DatasetTiny{},
				TotalRows: intPtr(0),
				Page:      intPtr(1),
			},
			mockStatus: http.StatusOK,
			wantErr:    false,
			wantCount:  0,
		},
		{
			name:       "unauthorized error",
			params:     GetDatasetsParams{},
			mockStatus: http.StatusUnauthorized,
			wantErr:    true,
		},
		{
			name:       "internal server error",
			params:     GetDatasetsParams{},
			mockStatus: http.StatusInternalServerError,
			wantErr:    true,
		},
		{
			name: "with state filter",
			params: GetDatasetsParams{
				States: []string{"active", "disabled"},
			},
			mockResponse: DatasetsListResponse{
				Results:   []DatasetTiny{sampleDatasetTiny()},
				TotalRows: intPtr(1),
				Page:      intPtr(1),
			},
			mockStatus: http.StatusOK,
			wantErr:    false,
			wantCount:  1,
		},
		{
			name: "with name filter",
			params: GetDatasetsParams{
				Name: stringPtr("user_data"),
			},
			mockResponse: DatasetsListResponse{
				Results:   []DatasetTiny{sampleDatasetTiny()},
				TotalRows: intPtr(1),
				Page:      intPtr(1),
			},
			mockStatus: http.StatusOK,
			wantErr:    false,
			wantCount:  1,
		},
		{
			name: "with destination filter",
			params: GetDatasetsParams{
				DestinationID: intPtr(1),
			},
			mockResponse: DatasetsListResponse{
				Results:   []DatasetTiny{sampleDatasetTiny()},
				TotalRows: intPtr(1),
				Page:      intPtr(1),
			},
			mockStatus: http.StatusOK,
			wantErr:    false,
			wantCount:  1,
		},
		{
			name: "with pagination params",
			params: GetDatasetsParams{
				Page:    2,
				Limit:   50,
				OrderBy: "name",
				Reverse: true,
			},
			mockResponse: DatasetsListResponse{
				Results:   []DatasetTiny{sampleDatasetTiny()},
				TotalRows: intPtr(1),
				Page:      intPtr(2),
			},
			mockStatus: http.StatusOK,
			wantErr:    false,
			wantCount:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock server
			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			// Register handler
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
				// Verify query parameters
				if tt.params.Name != nil {
					if r.URL.Query().Get("name") != *tt.params.Name {
						t.Errorf("Expected name=%s, got %s", *tt.params.Name, r.URL.Query().Get("name"))
					}
				}

				if len(tt.params.States) > 0 {
					states := r.URL.Query()["states"]
					if len(states) != len(tt.params.States) {
						t.Errorf("Expected %d states, got %d", len(tt.params.States), len(states))
					}
				}

				if tt.params.Page > 0 {
					if r.URL.Query().Get("page") == "" {
						t.Error("Expected page parameter to be set")
					}
				}

				if tt.mockStatus != http.StatusOK {
					testutil.RespondError(w, tt.mockStatus, "error")
					return
				}

				testutil.RespondJSON(w, tt.mockStatus, tt.mockResponse)
			})

			// Create client
			client := NewClient(mock.Server.URL, testOrgID, 0)

			// Call GetDatasets
			resp, err := client.GetDatasets("test-token", tt.params)

			// Check error
			if (err != nil) != tt.wantErr {
				t.Errorf("GetDatasets() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				if len(resp.Results) != tt.wantCount {
					t.Errorf("GetDatasets() count = %d, want %d", len(resp.Results), tt.wantCount)
				}
			}
		})
	}
}

func TestClient_GetDataset(t *testing.T) {
	tests := []struct {
		name       string
		datasetID  int
		mockStatus int
		wantErr    bool
	}{
		{
			name:       "successful fetch",
			datasetID:  1,
			mockStatus: http.StatusOK,
			wantErr:    false,
		},
		{
			name:       "dataset not found",
			datasetID:  999,
			mockStatus: http.StatusNotFound,
			wantErr:    true,
		},
		{
			name:       "unauthorized",
			datasetID:  1,
			mockStatus: http.StatusUnauthorized,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1", func(w http.ResponseWriter, r *http.Request) {
				if tt.mockStatus != http.StatusOK {
					testutil.RespondError(w, tt.mockStatus, "error")
					return
				}
				testutil.RespondJSON(w, tt.mockStatus, sampleDatasetFull())
			})

			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/999", func(w http.ResponseWriter, r *http.Request) {
				testutil.RespondError(w, http.StatusNotFound, "Dataset not found")
			})

			client := NewClient(mock.Server.URL, testOrgID, 0)

			dataset, err := client.GetDataset("test-token", tt.datasetID)

			if (err != nil) != tt.wantErr {
				t.Errorf("GetDataset() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				if dataset == nil {
					t.Fatal("Expected dataset, got nil")
				}
				if dataset.ID != 1 {
					t.Errorf("Expected dataset ID 1, got %d", dataset.ID)
				}
			}
		})
	}
}

func TestClient_GetDatasetStats(t *testing.T) {
	tests := []struct {
		name       string
		datasetID  int
		mockStatus int
		wantErr    bool
	}{
		{
			name:       "successful fetch",
			datasetID:  1,
			mockStatus: http.StatusOK,
			wantErr:    false,
		},
		{
			name:       "dataset not found",
			datasetID:  999,
			mockStatus: http.StatusNotFound,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1/stats", func(w http.ResponseWriter, r *http.Request) {
				if tt.mockStatus != http.StatusOK {
					testutil.RespondError(w, tt.mockStatus, "error")
					return
				}
				testutil.RespondJSON(w, tt.mockStatus, sampleDatasetStatsResponse())
			})

			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/999/stats", func(w http.ResponseWriter, r *http.Request) {
				testutil.RespondError(w, http.StatusNotFound, "Dataset not found")
			})

			client := NewClient(mock.Server.URL, testOrgID, 0)

			stats, err := client.GetDatasetStats("test-token", tt.datasetID)

			if (err != nil) != tt.wantErr {
				t.Errorf("GetDatasetStats() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				if stats == nil {
					t.Fatal("Expected stats, got nil")
				}
				if stats.BadRowsCount == nil {
					t.Error("Expected BadRowsCount to be set")
				}
			}
		})
	}
}

func TestClient_GetDatasetJobActivity(t *testing.T) {
	tests := []struct {
		name       string
		datasetID  int
		lastUpdate *int
		mockStatus int
		wantErr    bool
	}{
		{
			name:       "successful fetch without lastUpdate",
			datasetID:  1,
			lastUpdate: nil,
			mockStatus: http.StatusOK,
			wantErr:    false,
		},
		{
			name:       "successful fetch with lastUpdate",
			datasetID:  1,
			lastUpdate: intPtr(1705320000),
			mockStatus: http.StatusOK,
			wantErr:    false,
		},
		{
			name:       "unauthorized",
			datasetID:  1,
			lastUpdate: nil,
			mockStatus: http.StatusUnauthorized,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/1/job-activity", func(w http.ResponseWriter, r *http.Request) {
				if tt.mockStatus != http.StatusOK {
					testutil.RespondError(w, tt.mockStatus, "error")
					return
				}
				testutil.RespondJSON(w, tt.mockStatus, sampleJobRunningCountResponse())
			})

			client := NewClient(mock.Server.URL, testOrgID, 0)

			response, err := client.GetDatasetJobActivity("test-token", tt.datasetID, tt.lastUpdate)

			if (err != nil) != tt.wantErr {
				t.Errorf("GetDatasetJobActivity() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				if response == nil {
					t.Fatal("Expected response, got nil")
				}
				if response.ServerTime == 0 {
					t.Error("Expected ServerTime to be set")
				}
			}
		})
	}
}

func TestClient_GetJobActivity(t *testing.T) {
	tests := []struct {
		name       string
		lastUpdate *int
		mockStatus int
		wantErr    bool
	}{
		{
			name:       "successful fetch",
			lastUpdate: nil,
			mockStatus: http.StatusOK,
			wantErr:    false,
		},
		{
			name:       "with last update",
			lastUpdate: intPtr(1705320000),
			mockStatus: http.StatusOK,
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/job-activity", func(w http.ResponseWriter, r *http.Request) {
				if tt.mockStatus != http.StatusOK {
					testutil.RespondError(w, tt.mockStatus, "error")
					return
				}
				testutil.RespondJSON(w, tt.mockStatus, sampleJobRunningCountResponse())
			})

			client := NewClient(mock.Server.URL, testOrgID, 0)

			response, err := client.GetJobActivity("test-token", tt.lastUpdate)

			if (err != nil) != tt.wantErr {
				t.Errorf("GetJobActivity() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				if response == nil {
					t.Error("Expected response, got nil")
				}
			}
		})
	}
}

// Helper functions
func intPtr(i int) *int {
	return &i
}

func stringPtr(s string) *string {
	return &s
}
