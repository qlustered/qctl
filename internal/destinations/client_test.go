package destinations

import (
	"net/http"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/testutil"
)

const testOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

func strPtr(s string) *string {
	return &s
}

func destTypePtr(t DestinationType) *DestinationType {
	return &t
}

// Test fixtures using generated types

func sampleDestinationTiny() DestinationTiny {
	return DestinationTiny{
		ID:              1,
		Name:            "postgres-prod",
		DestinationType: api.DestinationType("postgresql"),
		UpdatedAt:       time.Now(),
	}
}

func sampleDestinationTiny2() DestinationTiny {
	return DestinationTiny{
		ID:              2,
		Name:            "postgres-staging",
		DestinationType: api.DestinationType("postgresql"),
		UpdatedAt:       time.Now(),
	}
}

func sampleDestinationFull() DestinationFull {
	return DestinationFull{
		ID:              1,
		Name:            "postgres-prod",
		DestinationType: api.DestinationType("postgresql"),
		Host:            "db.example.com",
		Port:            5432,
		DatabaseName:    "production",
		User:            "admin",
		ConnectTimeout:  30,
		CreatedAt:       time.Now().Add(-24 * time.Hour),
		UpdatedAt:       time.Now(),
	}
}

func intPtr(i int) *int {
	return &i
}

func TestClient_GetDestinations(t *testing.T) {
	tests := []struct {
		name         string
		params       GetDestinationsParams
		mockResponse ListPage
		mockStatus   int
		wantErr      bool
		wantCount    int
	}{
		{
			name: "successful fetch with results",
			params: GetDestinationsParams{
				Limit: 100,
			},
			mockResponse: ListPage{
				Results:   []DestinationTiny{sampleDestinationTiny()},
				TotalRows: intPtr(1),
				Page:      intPtr(1),
			},
			mockStatus: http.StatusOK,
			wantErr:    false,
			wantCount:  1,
		},
		{
			name: "successful fetch with empty results",
			params: GetDestinationsParams{
				Limit: 100,
			},
			mockResponse: ListPage{
				Results:   []DestinationTiny{},
				TotalRows: intPtr(0),
				Page:      intPtr(1),
			},
			mockStatus: http.StatusOK,
			wantErr:    false,
			wantCount:  0,
		},
		{
			name: "fetch with name filter",
			params: GetDestinationsParams{
				Name: strPtr("postgres-prod"),
			},
			mockResponse: ListPage{
				Results:   []DestinationTiny{sampleDestinationTiny()},
				TotalRows: intPtr(1),
				Page:      intPtr(1),
			},
			mockStatus: http.StatusOK,
			wantErr:    false,
			wantCount:  1,
		},
		{
			name: "fetch with sorting",
			params: GetDestinationsParams{
				OrderBy: "name",
				Reverse: true,
			},
			mockResponse: ListPage{
				Results:   []DestinationTiny{sampleDestinationTiny2(), sampleDestinationTiny()},
				TotalRows: intPtr(2),
				Page:      intPtr(1),
			},
			mockStatus: http.StatusOK,
			wantErr:    false,
			wantCount:  2,
		},
		{
			name:       "unauthorized error",
			params:     GetDestinationsParams{},
			mockStatus: http.StatusUnauthorized,
			wantErr:    true,
		},
		{
			name:       "server error",
			params:     GetDestinationsParams{},
			mockStatus: http.StatusInternalServerError,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup mock server
			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/destinations", func(w http.ResponseWriter, r *http.Request) {
				if tt.mockStatus != http.StatusOK {
					testutil.RespondError(w, tt.mockStatus, "error")
					return
				}
				testutil.RespondJSON(w, tt.mockStatus, tt.mockResponse)
			})

			// Create client
			client, err := NewClient(mock.URL(), testOrgID, 0)
			if err != nil {
				t.Fatalf("failed to create client: %v", err)
			}

			// Execute
			result, err := client.GetDestinations("test-token", tt.params)

			// Assert
			if tt.wantErr {
				if err == nil {
					t.Error("expected error but got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if len(result.Results) != tt.wantCount {
				t.Errorf("got %d results, want %d", len(result.Results), tt.wantCount)
			}
		})
	}
}

func TestClient_GetDestination(t *testing.T) {
	tests := []struct {
		name          string
		destinationID int
		mockResponse  DestinationFull
		mockStatus    int
		wantErr       bool
	}{
		{
			name:          "successful fetch",
			destinationID: 1,
			mockResponse:  sampleDestinationFull(),
			mockStatus:    http.StatusOK,
			wantErr:       false,
		},
		{
			name:          "not found",
			destinationID: 999,
			mockStatus:    http.StatusNotFound,
			wantErr:       true,
		},
		{
			name:          "unauthorized",
			destinationID: 1,
			mockStatus:    http.StatusUnauthorized,
			wantErr:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/destinations/1", func(w http.ResponseWriter, r *http.Request) {
				if tt.mockStatus != http.StatusOK {
					testutil.RespondError(w, tt.mockStatus, "error")
					return
				}
				testutil.RespondJSON(w, tt.mockStatus, tt.mockResponse)
			})

			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/destinations/999", func(w http.ResponseWriter, r *http.Request) {
				testutil.RespondError(w, http.StatusNotFound, "not found")
			})

			client, err := NewClient(mock.URL(), testOrgID, 0)
			if err != nil {
				t.Fatalf("failed to create client: %v", err)
			}

			result, err := client.GetDestination("test-token", tt.destinationID)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error but got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if result.ID != tt.mockResponse.ID {
				t.Errorf("got ID %d, want %d", result.ID, tt.mockResponse.ID)
			}
			if result.Name != tt.mockResponse.Name {
				t.Errorf("got Name %q, want %q", result.Name, tt.mockResponse.Name)
			}
		})
	}
}

func TestClient_GetDestinationByName(t *testing.T) {
	tests := []struct {
		name         string
		searchName   string
		listResponse ListPage
		fullResponse DestinationFull
		wantFound    bool
		wantErr      bool
	}{
		{
			name:       "found exact match",
			searchName: "postgres-prod",
			listResponse: ListPage{
				Results:   []DestinationTiny{sampleDestinationTiny()},
				TotalRows: intPtr(1),
			},
			fullResponse: sampleDestinationFull(),
			wantFound:    true,
			wantErr:      false,
		},
		{
			name:       "not found",
			searchName: "nonexistent",
			listResponse: ListPage{
				Results:   []DestinationTiny{},
				TotalRows: intPtr(0),
			},
			wantFound: false,
			wantErr:   false,
		},
		{
			name:       "partial match not returned",
			searchName: "postgres",
			listResponse: ListPage{
				Results:   []DestinationTiny{sampleDestinationTiny()}, // name is postgres-prod
				TotalRows: intPtr(1),
			},
			wantFound: false, // exact match required
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/destinations", func(w http.ResponseWriter, r *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, tt.listResponse)
			})

			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/destinations/1", func(w http.ResponseWriter, r *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, tt.fullResponse)
			})

			client, err := NewClient(mock.URL(), testOrgID, 0)
			if err != nil {
				t.Fatalf("failed to create client: %v", err)
			}

			result, err := client.GetDestinationByName("test-token", tt.searchName)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error but got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if tt.wantFound && result == nil {
				t.Error("expected to find destination but got nil")
			}
			if !tt.wantFound && result != nil {
				t.Errorf("expected nil but got destination: %+v", result)
			}
		})
	}
}

func TestClient_GetDestinationDatabaseNames(t *testing.T) {
	tests := []struct {
		name          string
		destinationID int
		mockResponse  []string
		mockStatus    int
		wantErr       bool
		wantCount     int
	}{
		{
			name:          "successful fetch",
			destinationID: 1,
			mockResponse:  []string{"db1", "db2", "db3"},
			mockStatus:    http.StatusOK,
			wantErr:       false,
			wantCount:     3,
		},
		{
			name:          "empty databases",
			destinationID: 1,
			mockResponse:  []string{},
			mockStatus:    http.StatusOK,
			wantErr:       false,
			wantCount:     0,
		},
		{
			name:          "error response",
			destinationID: 1,
			mockStatus:    http.StatusInternalServerError,
			wantErr:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/destinations/1/database-names", func(w http.ResponseWriter, r *http.Request) {
				if tt.mockStatus != http.StatusOK {
					testutil.RespondError(w, tt.mockStatus, "error")
					return
				}
				testutil.RespondJSON(w, tt.mockStatus, map[string][]string{
					"results": tt.mockResponse,
				})
			})

			client, err := NewClient(mock.URL(), testOrgID, 0)
			if err != nil {
				t.Fatalf("failed to create client: %v", err)
			}

			result, err := client.GetDestinationDatabaseNames("test-token", tt.destinationID)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error but got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if len(result) != tt.wantCount {
				t.Errorf("got %d databases, want %d", len(result), tt.wantCount)
			}
		})
	}
}

func TestClient_CreateDestination(t *testing.T) {
	password := "secret"
	timeout := 30

	tests := []struct {
		name         string
		manifest     *DestinationManifest
		mockResponse DestinationFull
		mockStatus   int
		wantErr      bool
	}{
		{
			name: "successful create",
			manifest: &DestinationManifest{
				APIVersion: "qluster.ai/v1",
				Kind:       "Destination",
				Metadata: DestinationMetadata{
					Name: "new-destination",
				},
				Spec: DestinationSpec{
					Type:           destTypePtr(DestinationTypePostgresql),
					Host:           strPtr("db.example.com"),
					Port:           5432,
					DatabaseName:   strPtr("mydb"),
					User:           strPtr("admin"),
					Password:       &password,
					ConnectTimeout: &timeout,
				},
			},
			mockResponse: DestinationFull{
				ID:              10,
				Name:            "new-destination",
				DestinationType: api.DestinationType("postgresql"),
				Host:            "db.example.com",
				Port:            5432,
				DatabaseName:    "mydb",
				User:            "admin",
				ConnectTimeout:  30,
			},
			mockStatus: http.StatusOK,
			wantErr:    false,
		},
		{
			name: "create error",
			manifest: &DestinationManifest{
				APIVersion: "qluster.ai/v1",
				Kind:       "Destination",
				Metadata: DestinationMetadata{
					Name: "new-destination",
				},
				Spec: DestinationSpec{
					Type:         destTypePtr(DestinationTypePostgresql),
					Host:         strPtr("db.example.com"),
					Port:         5432,
					DatabaseName: strPtr("mydb"),
					User:         strPtr("admin"),
				},
			},
			mockStatus: http.StatusBadRequest,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/destinations", func(w http.ResponseWriter, r *http.Request) {
				if tt.mockStatus != http.StatusOK {
					testutil.RespondError(w, tt.mockStatus, "error")
					return
				}
				testutil.RespondJSON(w, tt.mockStatus, tt.mockResponse)
			})

			client, err := NewClient(mock.URL(), testOrgID, 0)
			if err != nil {
				t.Fatalf("failed to create client: %v", err)
			}

			result, err := client.CreateDestination("test-token", tt.manifest)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error but got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if result.Name != tt.manifest.Metadata.Name {
				t.Errorf("got Name %q, want %q", result.Name, tt.manifest.Metadata.Name)
			}
		})
	}
}

func TestClient_UpdateDestination(t *testing.T) {
	password := "newsecret"

	tests := []struct {
		name          string
		destinationID int
		manifest      *DestinationManifest
		mockResponse  DestinationFull
		mockStatus    int
		wantErr       bool
	}{
		{
			name:          "successful update",
			destinationID: 1,
			manifest: &DestinationManifest{
				APIVersion: "qluster.ai/v1",
				Kind:       "Destination",
				Metadata: DestinationMetadata{
					Name: "updated-destination",
				},
				Spec: DestinationSpec{
					Type:         destTypePtr(DestinationTypePostgresql),
					Host:         strPtr("newdb.example.com"),
					Port:         5432,
					DatabaseName: strPtr("newdb"),
					User:         strPtr("newadmin"),
					Password:     &password,
				},
			},
			mockResponse: DestinationFull{
				ID:              1,
				Name:            "updated-destination",
				DestinationType: api.DestinationType("postgresql"),
				Host:            "newdb.example.com",
				Port:            5432,
				DatabaseName:    "newdb",
				User:            "newadmin",
			},
			mockStatus: http.StatusOK,
			wantErr:    false,
		},
		{
			name:          "update error",
			destinationID: 1,
			manifest: &DestinationManifest{
				APIVersion: "qluster.ai/v1",
				Kind:       "Destination",
				Metadata: DestinationMetadata{
					Name: "updated-destination",
				},
				Spec: DestinationSpec{
					Type:         destTypePtr(DestinationTypePostgresql),
					Host:         strPtr("newdb.example.com"),
					Port:         5432,
					DatabaseName: strPtr("newdb"),
					User:         strPtr("newadmin"),
				},
			},
			mockStatus: http.StatusBadRequest,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/destinations", func(w http.ResponseWriter, r *http.Request) {
				if tt.mockStatus != http.StatusOK {
					testutil.RespondError(w, tt.mockStatus, "error")
					return
				}
				testutil.RespondJSON(w, tt.mockStatus, tt.mockResponse)
			})

			client, err := NewClient(mock.URL(), testOrgID, 0)
			if err != nil {
				t.Fatalf("failed to create client: %v", err)
			}

			result, err := client.UpdateDestination("test-token", tt.destinationID, tt.manifest)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error but got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if result.Name != tt.manifest.Metadata.Name {
				t.Errorf("got Name %q, want %q", result.Name, tt.manifest.Metadata.Name)
			}
		})
	}
}

func TestClient_Apply(t *testing.T) {
	tests := []struct {
		name           string
		manifest       *DestinationManifest
		existingDest   *DestinationFull // nil means not found
		createResponse DestinationFull
		updateResponse DestinationFull
		wantAction     string
		wantErr        bool
	}{
		{
			name: "create new destination",
			manifest: &DestinationManifest{
				APIVersion: "qluster.ai/v1",
				Kind:       "Destination",
				Metadata: DestinationMetadata{
					Name: "new-destination",
				},
				Spec: DestinationSpec{
					Type:         destTypePtr(DestinationTypePostgresql),
					Host:         strPtr("db.example.com"),
					Port:         5432,
					DatabaseName: strPtr("mydb"),
					User:         strPtr("admin"),
				},
			},
			existingDest: nil,
			createResponse: DestinationFull{
				ID:   10,
				Name: "new-destination",
			},
			wantAction: "created",
			wantErr:    false,
		},
		{
			name: "update existing destination",
			manifest: &DestinationManifest{
				APIVersion: "qluster.ai/v1",
				Kind:       "Destination",
				Metadata: DestinationMetadata{
					Name: "existing-destination",
				},
				Spec: DestinationSpec{
					Type:         destTypePtr(DestinationTypePostgresql),
					Host:         strPtr("db.example.com"),
					Port:         5432,
					DatabaseName: strPtr("mydb"),
					User:         strPtr("admin"),
				},
			},
			existingDest: &DestinationFull{
				ID:   5,
				Name: "existing-destination",
			},
			updateResponse: DestinationFull{
				ID:   5,
				Name: "existing-destination",
			},
			wantAction: "updated",
			wantErr:    false,
		},
		{
			name: "invalid manifest",
			manifest: &DestinationManifest{
				// Missing required fields
				Kind: "Destination",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			// Mock list endpoint for lookup
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/destinations", func(w http.ResponseWriter, r *http.Request) {
				if tt.existingDest != nil {
					testutil.RespondJSON(w, http.StatusOK, ListPage{
						Results: []DestinationTiny{
							{
								ID:              tt.existingDest.ID,
								Name:            tt.existingDest.Name,
								DestinationType: api.DestinationType("postgresql"),
							},
						},
						TotalRows: intPtr(1),
					})
				} else {
					testutil.RespondJSON(w, http.StatusOK, ListPage{
						Results:   []DestinationTiny{},
						TotalRows: intPtr(0),
					})
				}
			})

			// Mock get single endpoint
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/destinations/5", func(w http.ResponseWriter, r *http.Request) {
				if tt.existingDest != nil {
					testutil.RespondJSON(w, http.StatusOK, tt.existingDest)
				} else {
					testutil.RespondError(w, http.StatusNotFound, "not found")
				}
			})

			// Mock create endpoint
			mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/destinations", func(w http.ResponseWriter, r *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, tt.createResponse)
			})

			// Mock update endpoint
			mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/destinations", func(w http.ResponseWriter, r *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, tt.updateResponse)
			})

			client, err := NewClient(mock.URL(), testOrgID, 0)
			if err != nil {
				t.Fatalf("failed to create client: %v", err)
			}

			result, err := client.Apply("test-token", tt.manifest)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error but got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if result.Action != tt.wantAction {
				t.Errorf("got action %q, want %q", result.Action, tt.wantAction)
			}

			if result.Status != "applied" {
				t.Errorf("got status %q, want %q", result.Status, "applied")
			}
		})
	}
}

func TestClient_CountDestinationsByName(t *testing.T) {
	tests := []struct {
		name         string
		searchName   string
		listResponse ListPage
		wantCount    int
		wantErr      bool
	}{
		{
			name:       "one match",
			searchName: "postgres-prod",
			listResponse: ListPage{
				Results:   []DestinationTiny{sampleDestinationTiny()},
				TotalRows: intPtr(1),
			},
			wantCount: 1,
			wantErr:   false,
		},
		{
			name:       "no matches",
			searchName: "nonexistent",
			listResponse: ListPage{
				Results:   []DestinationTiny{},
				TotalRows: intPtr(0),
			},
			wantCount: 0,
			wantErr:   false,
		},
		{
			name:       "multiple matches (integrity violation)",
			searchName: "duplicate-name",
			listResponse: ListPage{
				Results: []DestinationTiny{
					{ID: 1, Name: "duplicate-name", DestinationType: api.DestinationType("postgresql")},
					{ID: 2, Name: "duplicate-name", DestinationType: api.DestinationType("postgresql")},
				},
				TotalRows: intPtr(2),
			},
			wantCount: 2,
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/destinations", func(w http.ResponseWriter, r *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, tt.listResponse)
			})

			client, err := NewClient(mock.URL(), testOrgID, 0)
			if err != nil {
				t.Fatalf("failed to create client: %v", err)
			}

			count, err := client.CountDestinationsByName("test-token", tt.searchName)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error but got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if count != tt.wantCount {
				t.Errorf("got count %d, want %d", count, tt.wantCount)
			}
		})
	}
}
