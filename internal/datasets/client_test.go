package datasets

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/prompt"
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
		Users:            []api.DataSetUserInfoTinySchema{},
	}
}

func sampleDatasetFull() DatasetFull {
	orgID := types.UUID{}
	orgID.UnmarshalText([]byte("550e8400-e29b-41d4-a716-446655440000"))

	encryptBackup := true
	quarantine := false
	removeOutliers := false
	detectAnomalies := false
	enableCellMove := false
	strictDatetime := false
	guessDatetime := false

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
		DetectAnomalies:                    &detectAnomalies,
		EnableCellMoveSuggestions:          &enableCellMove,
		StrictlyOneDatetimeFormatInAColumn: &strictDatetime,
		GuessDatetimeFormatInIngestion:     &guessDatetime,
		AnomalyThreshold:                   50,
		MaxRetryCount:                      3,
		MaxTriesToFixJSON:                  3,
		BackupKeyFormat:                    "{dataset_id}/{data_source_id}/{datetime}",
		BackupSettingsID:                   1,
		DestinationID:                      1,
		DestinationName:                    "postgres_main",
		DataLoadingProcess:                 api.DataLoadingProcessAppend,
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

func TestResolveIDUsesBoundedExactNameLookup(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	requests := 0
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		requests++
		if got := r.URL.Query().Get("name"); got != "user_data" {
			t.Errorf("name = %q, want user_data", got)
		}
		if got := r.URL.Query().Get("limit"); got != "2" {
			t.Errorf("limit = %q, want 2 to detect ambiguous names", got)
		}
		total, page := 1, 1
		testutil.RespondJSON(w, http.StatusOK, DatasetsPage{
			Results: []DatasetTiny{sampleDatasetTiny()}, TotalRows: &total, Page: &page,
		})
	})

	c := NewClient(mock.URL(), testOrgID, 0)
	got, err := c.ResolveID("token", "user_data")
	if err != nil {
		t.Fatalf("ResolveID: %v", err)
	}
	if got != 1 {
		t.Errorf("resolved ID = %d, want 1", got)
	}
	if requests != 1 {
		t.Errorf("requests = %d, want 1", requests)
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

const schemaDetailDraftJSON = `{
  "confirmed": false,
  "created_at": "2026-07-09T10:00:00Z",
  "updated_at": "2026-07-11T07:00:00Z",
  "dataset_id": 42,
  "dataset_name": "orders",
  "diff_summary": {"added_count": 1, "modified_count": 1, "removed_count": 1, "total_fields": 4},
  "fields": {
    "id": {
      "current": {"type": "integer", "is_nullable": false, "indexed": true},
      "new": {"type": "integer", "is_nullable": false, "indexed": true}
    },
    "name": {
      "action": "modified",
      "current": {"type": "varchar", "args": [100], "is_nullable": false},
      "new": {"type": "varchar", "args": [255], "is_nullable": false},
      "diffs": [{"property": "args", "old_value": [100], "new_value": [255]}]
    },
    "amount": {
      "action": "added",
      "new": {"type": "numeric", "is_nullable": true, "is_dollar": true},
      "notable_values": ["1.50", "99.99"]
    },
    "legacy_code": {
      "action": "removed",
      "current": {"type": "varchar", "args": [10], "is_nullable": true, "original_name": "Legacy Code"}
    }
  },
  "migration_id": 7,
  "migration_model_version_id": 11,
  "settings_model_version_id": 13,
  "migration_state": "draft_with_current",
  "state": "draft"
}`

func TestClient_GetSchemaDetails(t *testing.T) {
	t.Run("successful fetch", func(t *testing.T) {
		mock := testutil.NewMockAPIServer()
		defer mock.Close()

		mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/migration-details", func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, json.RawMessage(schemaDetailDraftJSON))
		})

		client := NewClient(mock.Server.URL, testOrgID, 0)
		detail, err := client.GetSchemaDetails("test-token", 42)
		if err != nil {
			t.Fatalf("GetSchemaDetails() error = %v", err)
		}
		if detail.DatasetName != "orders" {
			t.Errorf("DatasetName = %q, want %q", detail.DatasetName, "orders")
		}
		if detail.MigrationState != api.DraftWithCurrent {
			t.Errorf("MigrationState = %q, want draft_with_current", detail.MigrationState)
		}
		if len(detail.Fields) != 4 {
			t.Errorf("len(Fields) = %d, want 4", len(detail.Fields))
		}
	})

	t.Run("server error surfaces", func(t *testing.T) {
		mock := testutil.NewMockAPIServer()
		defer mock.Close()

		mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/migration-details", func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondError(w, http.StatusInternalServerError, "boom")
		})

		client := NewClient(mock.Server.URL, testOrgID, 0)
		if _, err := client.GetSchemaDetails("test-token", 42); err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}

func TestClient_GetSchemaFields(t *testing.T) {
	t.Run("successful fetch", func(t *testing.T) {
		mock := testutil.NewMockAPIServer()
		defer mock.Close()

		mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/migration-fields", func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"fields": ["amount", "id", "name"]}`))
		})

		client := NewClient(mock.Server.URL, testOrgID, 0)
		fields, err := client.GetSchemaFields("test-token", 42)
		if err != nil {
			t.Fatalf("GetSchemaFields() error = %v", err)
		}
		want := []string{"amount", "id", "name"}
		if len(fields) != len(want) {
			t.Fatalf("fields = %v, want %v", fields, want)
		}
		for i := range want {
			if fields[i] != want[i] {
				t.Errorf("fields[%d] = %q, want %q", i, fields[i], want[i])
			}
		}
	})

	t.Run("not found surfaces", func(t *testing.T) {
		mock := testutil.NewMockAPIServer()
		defer mock.Close()

		mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/999/migration-fields", func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondError(w, http.StatusNotFound, "not found")
		})

		client := NewClient(mock.Server.URL, testOrgID, 0)
		if _, err := client.GetSchemaFields("test-token", 999); err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}

// The list endpoint is the ONLY view of a deleted table: GET
// /orgs/{org}/datasets/{id} answers 404 "dataset {id} is deleted."
// (_get_dataset_full, atlas capi/capi/views/settings_view_core.py) and so does
// .../small (get_dataset_small, settings_view.py). The default states filter is
// [active, disabled], so a lookup that forgets to send states=deleted finds
// nothing -- which is the bug this test exists to prevent.
func TestResolveDeletedByNameSendsStatesDeleted(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	var gotStates []string
	var gotName string
	var gotLimit string
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		gotStates = r.URL.Query()["states"]
		gotName = r.URL.Query().Get("name")
		gotLimit = r.URL.Query().Get("limit")
		testutil.RespondJSON(w, http.StatusOK, DatasetsPage{
			Results: []DatasetTiny{{ID: 42, Name: "orders", State: api.DataSetStateDeleted}},
		})
	})

	c := NewClient(mock.Server.URL, testOrgID, 0)
	got, err := c.ResolveDeletedByName("test-token", "orders", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ID != 42 {
		t.Errorf("ID = %d, want 42", got.ID)
	}
	if len(gotStates) != 1 || gotStates[0] != "deleted" {
		t.Errorf("states query = %v, want [deleted]", gotStates)
	}
	if gotName != "orders" {
		t.Errorf("name filter = %q, want orders", gotName)
	}
	if gotLimit != "2" {
		t.Errorf("limit = %q, want 2 so a second same-named deleted table cannot be hidden", gotLimit)
	}
}

func TestResolveDeletedByNameAmbiguousExitsTwoAndListsIDs(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, DatasetsPage{Results: []DatasetTiny{
			{ID: 77, Name: "orders", State: api.DataSetStateDeleted},
			{ID: 42, Name: "orders", State: api.DataSetStateDeleted},
		}})
	})

	c := NewClient(mock.Server.URL, testOrgID, 0)
	got, err := c.ResolveDeletedByName("test-token", "orders", false)
	if err == nil {
		t.Fatalf("expected an ambiguity error, got %+v", got)
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (bad usage)", code, qerrors.ExitBadUsage)
	}
	for _, want := range []string{"ambiguous", "77", "42", "qctl undelete table <id> --yes"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should contain %q, got: %v", want, err)
		}
	}
}

func TestResolveDeletedByNameNotFoundExitsThree(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, DatasetsPage{Results: []DatasetTiny{}})
	})

	c := NewClient(mock.Server.URL, testOrgID, 0)
	_, err := c.ResolveDeletedByName("test-token", "ghost", false)
	if err == nil {
		t.Fatal("expected an error for a name with no deleted match")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
		t.Errorf("exit code = %d, want %d (not found)", code, qerrors.ExitNotFound)
	}
	if !strings.Contains(err.Error(), "--state deleted") {
		t.Errorf("error should point at 'qctl get tables --state deleted', got: %v", err)
	}
}

// The server's filters are trusted for the request and re-checked on the
// answer. A row the filters should have excluded must not be handed to
// RecoverDataset: recovery is one POST away and there is no undo, so the
// failure mode of trusting Results[0] is "undelete table orders silently
// recovered something else". Both sub-cases are unreachable through today's
// atlas (name is exact equality, states is sent) -- they are what a filter
// regression on either axis would look like.
func TestResolveDeletedByNameRejectsARowTheFiltersShouldHaveExcluded(t *testing.T) {
	for _, tc := range []struct {
		name string
		row  DatasetTiny
	}{
		{"name is not an exact match", DatasetTiny{ID: 42, Name: "orders_v2", State: api.DataSetStateDeleted}},
		{"row is not deleted", DatasetTiny{ID: 42, Name: "orders", State: api.DataSetStateActive}},
		{"second row is not an exact match", DatasetTiny{ID: 42, Name: "orders", State: api.DataSetStateDeleted}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			row := tc.row
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
				results := []DatasetTiny{row}
				if tc.name == "second row is not an exact match" {
					results = append(results, DatasetTiny{ID: 43, Name: "orders_v2", State: api.DataSetStateDeleted})
				}
				testutil.RespondJSON(w, http.StatusOK, DatasetsPage{Results: results})
			})

			c := NewClient(mock.Server.URL, testOrgID, 0)
			got, err := c.ResolveDeletedByName("test-token", "orders", false)
			if err == nil {
				t.Fatalf("expected an error, got %+v", got)
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
				t.Errorf("exit code = %d, want %d (not found)", code, qerrors.ExitNotFound)
			}
		})
	}
}

func TestRecoverDatasetSendsNewNameAndReturnsBody(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	var gotBody map[string]any
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/42/recover", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		testutil.RespondJSON(w, http.StatusOK, DatasetRecoverResponse{
			DatasetID: 42, Name: "orders-restored", State: "active", DataSourcesRecovered: 3,
		})
	})

	c := NewClient(mock.Server.URL, testOrgID, 0)
	got, err := c.RecoverDataset("test-token", 42, "orders-restored")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.DataSourcesRecovered != 3 || got.Name != "orders-restored" {
		t.Errorf("response = %+v, want name orders-restored and 3 data sources", got)
	}
	if gotBody["new_name"] != "orders-restored" {
		t.Errorf("new_name in body = %v, want orders-restored", gotBody["new_name"])
	}
}

// --new-name is optional: omitting it must not send new_name:"", which the
// server would treat as a name to rename to.
func TestRecoverDatasetOmitsEmptyNewName(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	var gotBody map[string]any
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/42/recover", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		testutil.RespondJSON(w, http.StatusOK, DatasetRecoverResponse{DatasetID: 42, Name: "orders", State: "active"})
	})

	c := NewClient(mock.Server.URL, testOrgID, 0)
	if _, err := c.RecoverDataset("test-token", 42, ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v, ok := gotBody["new_name"]; ok && v != nil {
		t.Errorf("new_name = %v, want null or absent when --new-name was not given", v)
	}
}

// 409 is the name-collision the server raises when the recovered name is
// already taken. Its remedy is --new-name, and "other 4xx" maps to exit 2.
func TestRecoverDatasetConflictExitsTwo(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/42/recover", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondError(w, http.StatusConflict, `The name "orders" is already used by another active/disabled dataset. Provide a new_name.`)
	})

	c := NewClient(mock.Server.URL, testOrgID, 0)
	_, err := c.RecoverDataset("test-token", 42, "")
	if err == nil {
		t.Fatal("expected an error on 409")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
}

// 207 Multi-Status is a 2xx: the grants under `granted` ARE committed (atlas
// capi/capi/views/user_view.py, the add_user_to_datasets route, documents it
// explicitly). Treating it as an HTTP error would discard a body describing
// work the server actually did.
func TestShareDatasetsAccepts207AsSuccess(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/add-user", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusMultiStatus, map[string]any{
			"result":             "partial",
			"user_id":            "11111111-2222-3333-4444-555555555555",
			"granted":            []int{12},
			"already_had_access": []int{},
			"failed": []map[string]any{
				{"dataset_id": 19, "reason": "not_authorized", "msg": "You are not authorized to invite another person to access this table."},
			},
		})
	})

	c := NewClient(mock.Server.URL, testOrgID, 0)
	got, err := c.ShareDatasets("test-token", ShareDatasetsRequest{
		Email: "alice@example.com", DatasetIDs: []int{12, 19},
	})
	if err != nil {
		t.Fatalf("207 must not surface as an error: %v", err)
	}
	if len(got.Granted) != 1 || got.Granted[0] != 12 {
		t.Errorf("granted = %v, want [12]", got.Granted)
	}
	if len(got.Failed) != 1 || got.Failed[0].DatasetID != 19 {
		t.Errorf("failed = %+v, want one entry for 19", got.Failed)
	}
}

func TestShareDatasetsSendsEmailAndIDs(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	var gotBody map[string]any
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/add-user", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		testutil.RespondJSON(w, http.StatusOK, map[string]any{
			"result": "ok", "user_id": "11111111-2222-3333-4444-555555555555",
			"granted": []int{12}, "already_had_access": []int{}, "failed": []any{},
		})
	})

	c := NewClient(mock.Server.URL, testOrgID, 0)
	if _, err := c.ShareDatasets("test-token", ShareDatasetsRequest{
		Email: "alice@example.com", DatasetIDs: []int{12},
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotBody["email"] != "alice@example.com" {
		t.Errorf("email = %v, want alice@example.com", gotBody["email"])
	}
	if v, ok := gotBody["user_id"]; ok && v != nil {
		t.Errorf("user_id = %v, want null when only --to was given", v)
	}
	ids, _ := gotBody["dataset_ids"].([]any)
	if len(ids) != 1 || ids[0].(float64) != 12 {
		t.Errorf("dataset_ids = %v, want [12]", gotBody["dataset_ids"])
	}
}

func TestShareDatasetsRejectsAMalformedUserID(t *testing.T) {
	c := NewClient("http://example.invalid", testOrgID, 0)
	_, err := c.ShareDatasets("test-token", ShareDatasetsRequest{
		UserID: "not-a-uuid", DatasetIDs: []int{12},
	})
	if err == nil {
		t.Fatal("expected an error for a malformed user id")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
}

func TestShareDatasets403SurfacesAsUnauthorized(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/add-user", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondError(w, http.StatusForbidden, "You don't have access to invite users to this table.")
	})

	c := NewClient(mock.Server.URL, testOrgID, 0)
	_, err := c.ShareDatasets("test-token", ShareDatasetsRequest{
		Email: "alice@example.com", DatasetIDs: []int{12},
	})
	if err == nil {
		t.Fatal("expected an error on 403")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitUnauthorized {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitUnauthorized)
	}
}

func TestAmbiguousUndeleteRetainsConfirmationChoice(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, DatasetsPage{Results: []DatasetTiny{
			{ID: 77, Name: "orders", State: api.DataSetStateDeleted},
			{ID: 42, Name: "orders", State: api.DataSetStateDeleted},
		}})
	})
	c := NewClient(mock.Server.URL, testOrgID, 0)
	for _, tc := range []struct {
		interactive, yes bool
		suffix           string
	}{
		{false, false, " --yes"}, {false, true, " --yes"}, {true, false, ""}, {true, true, " --yes"},
	} {
		prompt.ResetReader()
		if tc.interactive {
			prompt.SetReader(strings.NewReader("y\n"))
		}
		t.Cleanup(prompt.ResetReader)
		_, err := c.ResolveDeletedByName("test-token", "orders", tc.yes)
		if qerrors.GetExitCode(err) != 2 || !strings.Contains(err.Error(), "qctl undelete table <id>"+tc.suffix+"'") {
			t.Errorf("interactive=%t yes=%t: %v", tc.interactive, tc.yes, err)
		}
	}
}
