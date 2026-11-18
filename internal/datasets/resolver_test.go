package datasets

import (
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
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
			name:     "no match",
			datasets: sampleDatasets,
			input:    "nonexistent",
			wantErr:  `no table named "nonexistent"`,
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
			wantErr:  `no table named "anything"`,
		},
		{
			name:     "case sensitive - no match",
			datasets: sampleDatasets,
			input:    "User_Data",
			wantErr:  `no table named "User_Data"`,
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

func TestResolveDataset_NoMatchSuggestsNames(t *testing.T) {
	datasets := []DatasetTiny{
		{ID: 1, Name: "orders"},
		{ID: 2, Name: "users"},
	}

	_, err := ResolveDataset(datasets, "Orders")
	if err == nil {
		t.Fatal("expected error")
	}

	errMsg := err.Error()
	if !strings.Contains(errMsg, `Did you mean "orders" (id 1)?`) {
		t.Errorf("expected suggestion for 'orders', got: %s", errMsg)
	}
	if strings.Contains(errMsg, "users") {
		t.Errorf("unrelated table should not be suggested, got: %s", errMsg)
	}
	if !strings.Contains(errMsg, "qctl get tables") {
		t.Errorf("expected hint about 'qctl get tables', got: %s", errMsg)
	}
}

// Every way ResolveDataset can fail carries an exit code, and the codes are
// asserted rather than the messages: a bare fmt.Errorf produces an error with
// no Unwrap, so GetExitCode falls through to 1 while the text still reads
// perfectly. That silent 1 is what this pins.
//
// It matters most to `qctl rows check-unique`, which spends exit 1 on its
// VERDICT -- "these columns are not a key". A mistyped table name reported as
// 1 is indistinguishable from an answer, so `check-unique nosuch --column id`
// would tell a script the columns are not unique.
func TestResolveDatasetFailuresCarryTheirExitCode(t *testing.T) {
	sample := []DatasetTiny{{ID: 1, Name: "orders"}}

	cases := []struct {
		name     string
		datasets []DatasetTiny
		input    string
		want     int
	}{
		{"empty identifier is bad usage", sample, "", qerrors.ExitBadUsage},
		{"no tables at all is not found", nil, "orders", qerrors.ExitNotFound},
		{"a name that matches nothing is not found", sample, "nosuch", qerrors.ExitNotFound},
		{"duplicate names are bad usage", []DatasetTiny{{ID: 1, Name: "orders"}, {ID: 2, Name: "orders"}}, "orders", qerrors.ExitBadUsage},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ResolveDataset(tc.datasets, tc.input)
			if err == nil {
				t.Fatalf("expected an error")
			}
			if got := qerrors.GetExitCode(err); got != tc.want {
				t.Errorf("exit code = %d, want %d (message: %s)", got, tc.want, err)
			}
		})
	}
}
