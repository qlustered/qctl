package warnings

import (
	"net/http"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/testutil"
)

const testOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

// Test fixtures

func sampleWarningTiny() WarningTiny {
	createdAt := time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC)
	dataSourceID := 5
	dataSourceName := "customer_data"

	return WarningTiny{
		ID:                  1,
		DatasetName:         "user_data",
		DatasetID:           "10",
		DataSourceModelID:   &dataSourceID,
		DataSourceModelName: &dataSourceName,
		IssueType:           "DATA_QUALITY_ISSUE",
		Count:               5,
		Msg:                 "Data quality issue detected in 5 rows",
		CreatedAt:           &createdAt,
		AssignedUser:        nil,
	}
}

func sampleWarningsList() []WarningTiny {
	return []WarningTiny{
		sampleWarningTiny(),
		{
			ID:           2,
			DatasetName:  "product_catalog",
			DatasetID:    "15",
			IssueType:    "SCHEMA_DRIFT",
			Count:        2,
			Msg:          "Schema drift detected",
			AssignedUser: nil,
		},
		{
			ID:           3,
			DatasetName:  "user_analytics",
			DatasetID:    "20",
			IssueType:    "PERFORMANCE_WARNING",
			Count:        1,
			Msg:          "Query performance degraded",
			AssignedUser: nil,
		},
	}
}

// sampleAPIWarningFull returns the API format (with int dataset_id)
func sampleAPIWarningFull() map[string]interface{} {
	return map[string]interface{}{
		"id":                     1,
		"dataset_name":           "user_data",
		"dataset_id":             10,
		"data_source_model_id":   5,
		"data_source_model_name": "customer_data",
		"issue_type":             "DATA_QUALITY_ISSUE",
		"count":                  5,
		"msg":                    "Data quality issue detected in 5 rows",
		"created_at":             "2025-01-15T10:00:00Z",
		"settings_model_id":      100,
		"field_name":             "revenue",
		"field_value":            "0.00",
		"resolved":               false,
		"warning_actions_list":   []interface{}{},
		"redirect_url":           "/datasets/10/warnings/1",
		"publisher":              "data_validator",
	}
}

func TestClient_GetWarnings(t *testing.T) {
	tests := []struct {
		name         string
		params       GetWarningsParams
		mockResponse WarningsListResponse
		mockStatus   int
		wantErr      bool
		wantCount    int
	}{
		{
			name: "successful fetch with results",
			params: GetWarningsParams{
				Limit: 100,
			},
			mockResponse: WarningsListResponse{
				Results:   []WarningTiny{sampleWarningTiny()},
				TotalRows: intPtr(1),
				Page:      intPtr(1),
			},
			mockStatus: http.StatusOK,
			wantErr:    false,
			wantCount:  1,
		},
		{
			name:   "empty results",
			params: GetWarningsParams{},
			mockResponse: WarningsListResponse{
				Results:   []WarningTiny{},
				TotalRows: intPtr(0),
				Page:      intPtr(1),
			},
			mockStatus: http.StatusOK,
			wantErr:    false,
			wantCount:  0,
		},
		{
			name:       "unauthorized error",
			params:     GetWarningsParams{},
			mockStatus: http.StatusUnauthorized,
			wantErr:    true,
		},
		{
			name:       "internal server error",
			params:     GetWarningsParams{},
			mockStatus: http.StatusInternalServerError,
			wantErr:    true,
		},
		{
			name: "with resolved filter",
			params: GetWarningsParams{
				Resolved: boolPtr(false),
			},
			mockResponse: WarningsListResponse{
				Results:   []WarningTiny{sampleWarningTiny()},
				TotalRows: intPtr(1),
				Page:      intPtr(1),
			},
			mockStatus: http.StatusOK,
			wantErr:    false,
			wantCount:  1,
		},
		{
			name: "with dataset filter",
			params: GetWarningsParams{
				DatasetID: intPtr(10),
			},
			mockResponse: WarningsListResponse{
				Results:   []WarningTiny{sampleWarningTiny()},
				TotalRows: intPtr(1),
				Page:      intPtr(1),
			},
			mockStatus: http.StatusOK,
			wantErr:    false,
			wantCount:  1,
		},
		{
			name: "with data source filter",
			params: GetWarningsParams{
				DataSourceModelID: intPtr(5),
			},
			mockResponse: WarningsListResponse{
				Results:   []WarningTiny{sampleWarningTiny()},
				TotalRows: intPtr(1),
				Page:      intPtr(1),
			},
			mockStatus: http.StatusOK,
			wantErr:    false,
			wantCount:  1,
		},
		{
			name: "with search query",
			params: GetWarningsParams{
				SearchQuery: stringPtr("quality"),
			},
			mockResponse: WarningsListResponse{
				Results:   []WarningTiny{sampleWarningTiny()},
				TotalRows: intPtr(1),
				Page:      intPtr(1),
			},
			mockStatus: http.StatusOK,
			wantErr:    false,
			wantCount:  1,
		},
		{
			name: "with pagination params",
			params: GetWarningsParams{
				Page:    2,
				Limit:   50,
				OrderBy: "created_at",
				Reverse: true,
			},
			mockResponse: WarningsListResponse{
				Results:   []WarningTiny{sampleWarningTiny()},
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
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/warnings", func(w http.ResponseWriter, r *http.Request) {
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

			// Call GetWarnings
			resp, err := client.GetWarnings("test-token", tt.params)

			// Check error
			if (err != nil) != tt.wantErr {
				t.Errorf("GetWarnings() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				if len(resp.Results) != tt.wantCount {
					t.Errorf("GetWarnings() count = %d, want %d", len(resp.Results), tt.wantCount)
				}
			}
		})
	}
}

func TestClient_GetWarning(t *testing.T) {
	tests := []struct {
		name       string
		warningID  int
		mockStatus int
		wantErr    bool
	}{
		{
			name:       "successful fetch",
			warningID:  1,
			mockStatus: http.StatusOK,
			wantErr:    false,
		},
		{
			name:       "warning not found",
			warningID:  999,
			mockStatus: http.StatusNotFound,
			wantErr:    true,
		},
		{
			name:       "unauthorized",
			warningID:  1,
			mockStatus: http.StatusUnauthorized,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/warnings/1", func(w http.ResponseWriter, r *http.Request) {
				if tt.mockStatus != http.StatusOK {
					testutil.RespondError(w, tt.mockStatus, "error")
					return
				}
				testutil.RespondJSON(w, tt.mockStatus, sampleAPIWarningFull())
			})

			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/warnings/999", func(w http.ResponseWriter, r *http.Request) {
				testutil.RespondError(w, http.StatusNotFound, "Warning not found")
			})

			client := NewClient(mock.Server.URL, testOrgID, 0)

			warning, err := client.GetWarning("test-token", tt.warningID)

			if (err != nil) != tt.wantErr {
				t.Errorf("GetWarning() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				if warning == nil {
					t.Fatal("Expected warning, got nil")
				}
				if warning.ID != 1 {
					t.Errorf("Expected warning ID 1, got %d", warning.ID)
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
