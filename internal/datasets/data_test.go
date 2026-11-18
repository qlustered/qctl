package datasets

import (
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/testutil"
)

func sampleDataSchema() api.DataWithCellHistorySchema {
	clean := 1500
	bad := 24
	return api.DataWithCellHistorySchema{
		DatasetName:    "users_quarantine_data",
		Fields:         []string{"id", "email"},
		Labels:         []string{"User ID", "Email"},
		FieldTypes:     []api.FieldType{"integer", "text"},
		Results:        [][]interface{}{{float64(8943), "a@b.com"}},
		TotalPageCount: 2,
		TotalRows:      24,
		CleanRowsCount: &clean,
		BadRowsCount:   &bad,
	}
}

func TestClient_GetData(t *testing.T) {
	tests := []struct {
		name       string
		datasetID  int
		mockStatus int
		wantErr    bool
	}{
		{"successful fetch", 1, http.StatusOK, false},
		{"unauthorized", 1, http.StatusUnauthorized, true},
		{"validation error", 1, http.StatusUnprocessableEntity, true},
		{"server error", 1, http.StatusInternalServerError, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1/data", func(w http.ResponseWriter, _ *http.Request) {
				if tt.mockStatus != http.StatusOK {
					testutil.RespondError(w, tt.mockStatus, "error")
					return
				}
				testutil.RespondJSON(w, http.StatusOK, sampleDataSchema())
			})

			client := NewClient(mock.Server.URL, testOrgID, 0)
			page, err := client.GetData("test-token", tt.datasetID, &api.GetDataParams{})

			if (err != nil) != tt.wantErr {
				t.Fatalf("GetData() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr {
				if page == nil || page.DatasetName != "users_quarantine_data" {
					t.Errorf("GetData() unexpected page: %+v", page)
				}
			}
		})
	}
}

func TestClient_GetData_EmptyJSON200(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	// Return HTTP 200 with a non-JSON content-type so ParseGetDataResponse
	// never enters the JSON200 branch, leaving resp.JSON200 == nil.
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1/data", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondText(w, http.StatusOK, "ok")
	})

	client := NewClient(mock.Server.URL, testOrgID, 0)
	page, err := client.GetData("test-token", 1, &api.GetDataParams{})

	if err == nil {
		t.Fatalf("GetData() expected error for nil JSON200, got nil")
	}
	if page != nil {
		t.Errorf("GetData() page = %+v, want nil", page)
	}
	if msg := err.Error(); !strings.Contains(msg, "unexpected empty response") {
		t.Errorf("GetData() error = %q, want substring %q", msg, "unexpected empty response")
	}
}

func TestClient_GetData_NilParams(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1/data", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleDataSchema())
	})

	client := NewClient(mock.Server.URL, testOrgID, 0)
	if _, err := client.GetData("test-token", 1, nil); err != nil {
		t.Errorf("GetData(nil params) error = %v, want nil", err)
	}
}
