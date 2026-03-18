package datasets

import (
	"strings"
	"testing"
)

func TestResolveDataset(t *testing.T) {
	sampleDatasets := []DatasetTiny{
		{ID: 1, Name: "user_data"},
		{ID: 2, Name: "orders"},
		{ID: 3, Name: "My Table With Spaces"},
	}

	tests := []struct {
		name     string
		datasets []DatasetTiny
		input    string
		wantID   int
		wantName string
		wantErr  string
	}{
		{
			name:   "integer ID",
			input:  "42",
			wantID: 42,
		},
		{
			name:     "exact name match",
			datasets: sampleDatasets,
			input:    "user_data",
			wantID:   1,
			wantName: "user_data",
		},
		{
			name:     "exact name with spaces",
			datasets: sampleDatasets,
			input:    "My Table With Spaces",
			wantID:   3,
			wantName: "My Table With Spaces",
		},
		{
			name:    "no match",
			datasets: sampleDatasets,
			input:   "nonexistent",
			wantErr: "no table found matching 'nonexistent'",
		},
		{
			name:    "empty input",
			input:   "",
			wantErr: "table identifier cannot be empty",
		},
		{
			name:     "empty list with name input",
			datasets: []DatasetTiny{},
			input:    "anything",
			wantErr:  "no tables found",
		},
		{
			name:    "case sensitive - no match",
			datasets: sampleDatasets,
			input:   "User_Data",
			wantErr: "no table found matching 'User_Data'",
		},
		{
			name:   "negative integer treated as ID",
			input:  "-1",
			wantID: -1,
		},
		{
			name:   "zero treated as ID",
			input:  "0",
			wantID: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ResolveDataset(tt.datasets, tt.input)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got: %v", tt.wantErr, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if result.ID != tt.wantID {
				t.Errorf("expected ID %d, got %d", tt.wantID, result.ID)
			}
			if tt.wantName != "" && result.Name != tt.wantName {
				t.Errorf("expected Name %q, got %q", tt.wantName, result.Name)
			}
		})
	}
}

func TestResolveDataset_NoMatchShowsAvailable(t *testing.T) {
	datasets := []DatasetTiny{
		{ID: 1, Name: "orders"},
		{ID: 2, Name: "users"},
	}

	_, err := ResolveDataset(datasets, "nonexistent")
	if err == nil {
		t.Fatal("expected error")
	}

	errMsg := err.Error()
	if !strings.Contains(errMsg, "[1] orders") {
		t.Errorf("expected available tables to list 'orders', got: %s", errMsg)
	}
	if !strings.Contains(errMsg, "[2] users") {
		t.Errorf("expected available tables to list 'users', got: %s", errMsg)
	}
	if !strings.Contains(errMsg, "qctl get tables") {
		t.Errorf("expected hint about 'qctl get tables', got: %s", errMsg)
	}
}
