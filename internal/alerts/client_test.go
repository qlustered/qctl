package alerts

import (
	"net/http"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const testOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

// Test fixtures using generated types

func sampleAlertTiny() AlertTiny {
	createdAt := time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC)
	dataSourceID := 5
	dataSourceName := "customer_data"
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
		AffectedStoredItems:   nil,
		Whodunit:              nil,
		AssignedUser:          nil,
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

func TestClient_GetAlertStats(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/alerts/1/stats", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{"affecting_rows_count": 1234})
	})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/alerts/999/stats", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondError(w, http.StatusNotFound, "Alert 999 can not be found.")
	})
	client := NewClient(mock.Server.URL, testOrgID, 0)

	stats, err := client.GetAlertStats("test-token", 1)
	if err != nil {
		t.Fatalf("GetAlertStats() error = %v", err)
	}
	if stats.AffectingRowsCount == nil || *stats.AffectingRowsCount != 1234 {
		t.Errorf("affecting_rows_count = %v, want 1234", stats.AffectingRowsCount)
	}

	if _, err := client.GetAlertStats("test-token", 999); qerrors.GetExitCode(err) != qerrors.ExitNotFound {
		t.Errorf("a 404 should exit %d, got %d (%v)", qerrors.ExitNotFound, qerrors.GetExitCode(err), err)
	}
}

// The severe_rule_bug lookup in `describe table` is the only caller, and the
// filter is the whole reason that lookup is affordable: without it the command
// would have to page the table's entire alert list to find one alert.
func TestClient_GetAlertsSendsIssueType(t *testing.T) {
	var got string
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/alerts", func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query().Get("issue_type")
		testutil.RespondJSON(w, http.StatusOK, AlertsPage{Results: []AlertTiny{}, TotalRows: intPtr(0), Page: intPtr(1)})
	})

	client := NewClient(mock.Server.URL, testOrgID, 0)
	issueType := api.AlertTypeSevereRuleBug
	if _, err := client.GetAlerts("test-token", GetAlertsParams{IssueType: &issueType}); err != nil {
		t.Fatalf("GetAlerts() error = %v", err)
	}
	if got != "severe_rule_bug" {
		t.Errorf("issue_type = %q, want %q", got, "severe_rule_bug")
	}
}

// An absent filter must stay absent. Sending issue_type="" would be a filter
// the caller never asked for, and the same class of bug as the --resolved
// default this package already documents.
func TestClient_GetAlertsOmitsIssueTypeWhenUnset(t *testing.T) {
	var present bool
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/alerts", func(w http.ResponseWriter, r *http.Request) {
		_, present = r.URL.Query()["issue_type"]
		testutil.RespondJSON(w, http.StatusOK, AlertsPage{Results: []AlertTiny{}, TotalRows: intPtr(0), Page: intPtr(1)})
	})

	client := NewClient(mock.Server.URL, testOrgID, 0)
	if _, err := client.GetAlerts("test-token", GetAlertsParams{}); err != nil {
		t.Fatalf("GetAlerts() error = %v", err)
	}
	if present {
		t.Error("issue_type must not be sent when the caller did not set it")
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
