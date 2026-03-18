package tableui

import (
	"bytes"
	"strings"
	"testing"
)

func TestRender(t *testing.T) {
	tests := []struct {
		name        string
		columns     []string
		rows        []map[string]string
		noHeaders   bool
		maxColWidth int
		wantContain []string
		wantMissing []string
	}{
		{
			name:    "basic table with headers",
			columns: []string{"name", "release", "tags"},
			rows: []map[string]string{
				{"name": "email_validator", "release": "1.0.0", "tags": "Default"},
			},
			noHeaders:   false,
			maxColWidth: 80,
			wantContain: []string{"NAME", "RELEASE", "TAGS", "email_validator", "1.0.0", "Default"},
		},
		{
			name:    "no headers",
			columns: []string{"name", "release"},
			rows: []map[string]string{
				{"name": "email_validator", "release": "1.0.0"},
			},
			noHeaders:   true,
			maxColWidth: 80,
			wantContain: []string{"email_validator", "1.0.0"},
			wantMissing: []string{"NAME", "RELEASE"},
		},
		{
			name:    "underscore to dash in headers",
			columns: []string{"short_id", "is_default"},
			rows: []map[string]string{
				{"short_id": "abc12345", "is_default": "true"},
			},
			noHeaders:   false,
			maxColWidth: 80,
			wantContain: []string{"SHORT-ID", "IS-DEFAULT"},
		},
		{
			name:    "data_source_type maps to TYPE",
			columns: []string{"data_source_type"},
			rows: []map[string]string{
				{"data_source_type": "s3"},
			},
			noHeaders:   false,
			maxColWidth: 80,
			wantContain: []string{"TYPE", "s3"},
		},
		{
			name:    "truncation with max column width",
			columns: []string{"name"},
			rows: []map[string]string{
				{"name": "this_is_a_very_long_value_that_exceeds_the_limit"},
			},
			noHeaders:   false,
			maxColWidth: 20,
			wantContain: []string{"this_is_a_very_lo..."},
		},
		{
			name:        "empty rows returns nil error",
			columns:     []string{"name"},
			rows:        nil,
			noHeaders:   false,
			maxColWidth: 80,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			err := Render(&buf, tt.columns, tt.rows, tt.noHeaders, tt.maxColWidth)
			if err != nil {
				t.Fatalf("Render() error = %v", err)
			}

			output := buf.String()
			for _, want := range tt.wantContain {
				if !strings.Contains(output, want) {
					t.Errorf("Output should contain %q, got: %s", want, output)
				}
			}
			for _, missing := range tt.wantMissing {
				if strings.Contains(output, missing) {
					t.Errorf("Output should not contain %q, got: %s", missing, output)
				}
			}
		})
	}
}
