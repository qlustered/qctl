package alerts

import (
	"net/http"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/testutil"
)

const testOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

// Test fixtures using generated types

func sampleAlertTiny() AlertTiny {
	createdAt := time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC)
	dataSourceID := 5
	dataSourceName := "customer_data"
	redirectURL := "/datasets/1/alerts/1"
	resolvableByUser := true
	resolveAfterMigration := false

	return AlertTiny{
		ID:                    1,
		DatasetName:           "user_data",
		DatasetID:             10,
		DataSourceModelID:     &dataSourceID,
		DataSourceModelName:   &dataSourceName,
		IssueType:             api.AlertTypeValidation,
		Count:                 3,
		Msg:                   "Validation error in 3 files",
		CreatedAt:             &createdAt,
		ResolvedAt:            nil,
		ResolvableByUser:      &resolvableByUser,
		ResolveAfterMigration: &resolveAfterMigration,
		RedirectURL:           &redirectURL,
		AffectedStoredItems:   nil,
		Whodunit:              nil,
		AssignedUser:          nil,
	}
}

func sampleAlertsList() []AlertTiny {
	resolvableByUserTrue := true
	resolvableByUserFalse := false
	resolveAfterMigrationFalse := false
	resolveAfterMigrationTrue := true

	return []AlertTiny{
		sampleAlertTiny(),
		{
			ID:                    2,
			DatasetName:           "product_catalog",
			DatasetID:             15,
			IssueType:             api.AlertTypeRuleValidation,
			Count:                 10,
			Msg:                   "Price field validation failed for 10 rows",
			ResolvableByUser:      &resolvableByUserTrue,
			ResolveAfterMigration: &resolveAfterMigrationFalse,
			AffectedStoredItems:   nil,
		},
		{
			ID:                    3,
			DatasetName:           "user_analytics",
			DatasetID:             20,
			IssueType:             api.AlertTypeBadJSONSchema,
			Count:                 1,
			Msg:                   "Schema has changed unexpectedly",
			ResolvableByUser:      &resolvableByUserFalse,
			ResolveAfterMigration: &resolveAfterMigrationTrue,
			AffectedStoredItems:   nil,
		},
	}
}

func sampleAlertFull() AlertFull {
	createdAt := time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC)
	dataSourceID := 5
	dataSourceName := "customer_data"
	redirectURL := "/datasets/1/alerts/1"
	fieldName := "email"
	fieldValue := "invalid@"
	stackTrace := "Error at line 42..."
	resolvableByUser := true
	resolveAfterMigration := false
	resolved := false
	blocksProfiling := false
	blocksIngestionForDataset := true
	blocksIngestionForDataSource := false
	blocksStoredItem := false
	ingestionJobIDs := []int{100, 101}
	alertActions := []api.AlertAction{}

	return AlertFull{
		ID:                           1,
		DatasetName:                  "user_data",
		DatasetID:                    10,
		DataSourceModelID:            &dataSourceID,
		DataSourceModelName:          &dataSourceName,
		IssueType:                    api.AlertTypeValidation,
		Count:                        3,
		Msg:                          "Validation error in 3 files",
		CreatedAt:                    &createdAt,
		ResolvedAt:                   nil,
		ResolvableByUser:             &resolvableByUser,
		ResolveAfterMigration:        &resolveAfterMigration,
		RedirectURL:                  &redirectURL,
		Whodunit:                     nil,
		AssignedUser:                 nil,
		SettingsModelID:              100,
		IsRowLevel:                   false,
		FieldName:                    &fieldName,
		FieldValue:                   &fieldValue,
		Resolved:                     &resolved,
		BlocksProfiling:              &blocksProfiling,
		BlocksIngestionForDataset:    &blocksIngestionForDataset,
		BlocksIngestionForDataSource: &blocksIngestionForDataSource,
		BlocksStoredItem:             &blocksStoredItem,
		AlertActionsList:             &alertActions,
		StoredItemsToAlerts:          nil,
		StackTrace:                   &stackTrace,
		IngestionJobIds:              &ingestionJobIDs,
	}
}

func TestClient_GetAlerts(t *testing.T) {
	tests := []struct {
		name         string
		params       GetAlertsParams
		mockResponse AlertsPage
		mockStatus   int
		wantErr      bool
		wantCount    int
	}{
		{
			name: "successful fetch with results",
			params: GetAlertsParams{
				Limit: 100,
			},
			mockResponse: AlertsPage{
				Results:   []AlertTiny{sampleAlertTiny()},
				TotalRows: intPtr(1),
				Page:      intPtr(1),
			},
			mockStatus: http.StatusOK,
			wantErr:    false,
			wantCount:  1,
		},
		{
			name:   "empty results",
			params: GetAlertsParams{},
			mockResponse: AlertsPage{
				Results:   []AlertTiny{},
				TotalRows: intPtr(0),
				Page:      intPtr(1),
			},
			mockStatus: http.StatusOK,
			wantErr:    false,
			wantCount:  0,
		},
		{
			name:       "unauthorized error",
			params:     GetAlertsParams{},
			mockStatus: http.StatusUnauthorized,
			wantErr:    true,
		},
		{
			name:       "internal server error",
			params:     GetAlertsParams{},
			mockStatus: http.StatusInternalServerError,
			wantErr:    true,
		},
		{
			name: "with resolved filter",
			params: GetAlertsParams{
				Resolved: boolPtr(false),
			},
			mockResponse: AlertsPage{
				Results:   []AlertTiny{sampleAlertTiny()},
				TotalRows: intPtr(1),
				Page:      intPtr(1),
			},
			mockStatus: http.StatusOK,
			wantErr:    false,
			wantCount:  1,
		},
		{
			name: "with dataset filter",
			params: GetAlertsParams{
				DatasetID: intPtr(10),
			},
			mockResponse: AlertsPage{
				Results:   []AlertTiny{sampleAlertTiny()},
				TotalRows: intPtr(1),
				Page:      intPtr(1),
			},
			mockStatus: http.StatusOK,
			wantErr:    false,
			wantCount:  1,
		},
		{
			name: "with is_row_level filter",
			params: GetAlertsParams{
				IsRowLevel: boolPtr(false),
			},
			mockResponse: AlertsPage{
				Results:   []AlertTiny{sampleAlertTiny()},
				TotalRows: intPtr(1),
				Page:      intPtr(1),
			},
			mockStatus: http.StatusOK,
			wantErr:    false,
			wantCount:  1,
		},
		{
			name: "with search query",
			params: GetAlertsParams{
				SearchQuery: stringPtr("email"),
			},
			mockResponse: AlertsPage{
				Results:   []AlertTiny{sampleAlertTiny()},
				TotalRows: intPtr(1),
				Page:      intPtr(1),
			},
			mockStatus: http.StatusOK,
			wantErr:    false,
			wantCount:  1,
		},
		{
			name: "with pagination params",
			params: GetAlertsParams{
				Page:    2,
				Limit:   50,
				OrderBy: "created_at",
				Reverse: true,
			},
			mockResponse: AlertsPage{
				Results:   []AlertTiny{sampleAlertTiny()},
				TotalRows: intPtr(1),
				Page:      intPtr(2),
			},
			mockStatus: http.StatusOK,
			wantErr:    false,
			wantCount:  1,
		},
		{
			name: "with resolvable_by_user filter",
			params: GetAlertsParams{
				ResolvableByUser: boolPtr(true),
			},
			mockResponse: AlertsPage{
				Results:   []AlertTiny{sampleAlertTiny()},
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
			// Create mock server
			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			// Register handler
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/alerts", func(w http.ResponseWriter, r *http.Request) {
				// Verify query parameters
				if tt.params.DatasetID != nil {
					if r.URL.Query().Get("dataset_id") == "" {
						t.Error("Expected dataset_id parameter to be set")
					}
				}

				if tt.params.SearchQuery != nil {
					if r.URL.Query().Get("search_query") != *tt.params.SearchQuery {
						t.Errorf("Expected search_query=%s, got %s", *tt.params.SearchQuery, r.URL.Query().Get("search_query"))
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

			// Call GetAlerts
			resp, err := client.GetAlerts("test-token", tt.params)

			// Check error
			if (err != nil) != tt.wantErr {
				t.Errorf("GetAlerts() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				if len(resp.Results) != tt.wantCount {
					t.Errorf("GetAlerts() count = %d, want %d", len(resp.Results), tt.wantCount)
				}
			}
		})
	}
}

func TestClient_GetAlert(t *testing.T) {
	tests := []struct {
		name       string
		alertID    int
		mockStatus int
		wantErr    bool
	}{
		{
			name:       "successful fetch",
			alertID:    1,
			mockStatus: http.StatusOK,
			wantErr:    false,
		},
		{
			name:       "alert not found",
			alertID:    999,
			mockStatus: http.StatusNotFound,
			wantErr:    true,
		},
		{
			name:       "unauthorized",
			alertID:    1,
			mockStatus: http.StatusUnauthorized,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/alerts/1", func(w http.ResponseWriter, r *http.Request) {
				if tt.mockStatus != http.StatusOK {
					testutil.RespondError(w, tt.mockStatus, "error")
					return
				}
				testutil.RespondJSON(w, tt.mockStatus, sampleAlertFull())
			})

			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/alerts/999", func(w http.ResponseWriter, r *http.Request) {
				testutil.RespondError(w, http.StatusNotFound, "Alert not found")
			})

			client := NewClient(mock.Server.URL, testOrgID, 0)

			alert, err := client.GetAlert("test-token", tt.alertID)

			if (err != nil) != tt.wantErr {
				t.Errorf("GetAlert() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				if alert == nil {
					t.Fatal("Expected alert, got nil")
				}
				if alert.ID != 1 {
					t.Errorf("Expected alert ID 1, got %d", alert.ID)
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

func boolPtr(b bool) *bool {
	return &b
}
