package printer

import (
	"bytes"
	"strings"
	"testing"
)

func TestTruncateValue(t *testing.T) {
	tests := []struct {
		name           string
		maxColWidth    int
		value          string
		expectedOutput string
	}{
		{
			name:           "short value not truncated",
			maxColWidth:    80,
			value:          "short.txt",
			expectedOutput: "short.txt",
		},
		{
			name:           "long value truncated with ellipsis",
			maxColWidth:    20,
			value:          "very-long-filename-that-exceeds-the-limit.csv",
			expectedOutput: "very-long-filenam...",
		},
		{
			name:           "exact length not truncated",
			maxColWidth:    10,
			value:          "exactly10c",
			expectedOutput: "exactly10c",
		},
		{
			name:           "one over limit gets truncated",
			maxColWidth:    10,
			value:          "exactly11ch",
			expectedOutput: "exactly...",
		},
		{
			name:           "zero max width disables truncation",
			maxColWidth:    0,
			value:          "very-long-filename-that-should-not-be-truncated.csv",
			expectedOutput: "very-long-filename-that-should-not-be-truncated.csv",
		},
		{
			name:           "negative max width disables truncation",
			maxColWidth:    -1,
			value:          "very-long-filename-that-should-not-be-truncated.csv",
			expectedOutput: "very-long-filename-that-should-not-be-truncated.csv",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewPrinter(Options{
				MaxColumnWidth: tt.maxColWidth,
			})

			result := p.truncateValue(tt.value)
			if result != tt.expectedOutput {
				t.Errorf("truncateValue() = %q, want %q", result, tt.expectedOutput)
			}
		})
	}
}

func TestTableWithLongFilenames(t *testing.T) {
	type Job struct {
		ID       int    `json:"id"`
		FileName string `json:"file_name"`
		State    string `json:"state"`
	}

	jobs := []Job{
		{ID: 1, FileName: "short.csv", State: "finished"},
		{ID: 2, FileName: "this-is-a-very-very-very-very-very-very-very-very-very-long-filename-that-should-be-truncated.csv", State: "running"},
		{ID: 3, FileName: "medium-length-name.csv", State: "pending"},
	}

	t.Run("with default truncation at 80 chars", func(t *testing.T) {
		var buf bytes.Buffer
		p := NewPrinter(Options{
			Format:         FormatTable,
			Columns:        []string{"id", "file_name", "state"},
			MaxColumnWidth: 80, // default
			Writer:         &buf,
		})

		err := p.Print(jobs)
		if err != nil {
			t.Fatalf("Print() error = %v", err)
		}

		output := buf.String()

		// Should contain the short filename as-is
		if !strings.Contains(output, "short.csv") {
			t.Error("Expected to find 'short.csv' in output")
		}

		// Should contain truncated version of long filename
		if !strings.Contains(output, "...") {
			t.Error("Expected to find '...' (ellipsis) in output for truncated filename")
		}

		// Should NOT contain the full long filename
		fullLongName := "this-is-a-very-very-very-very-very-very-very-very-very-long-filename-that-should-be-truncated.csv"
		if strings.Contains(output, fullLongName) {
			t.Error("Did not expect to find full long filename in output")
		}
	})

	t.Run("with no truncation", func(t *testing.T) {
		var buf bytes.Buffer
		p := NewPrinter(Options{
			Format:         FormatTable,
			Columns:        []string{"id", "file_name", "state"},
			MaxColumnWidth: 0, // disable truncation
			Writer:         &buf,
		})

		err := p.Print(jobs)
		if err != nil {
			t.Fatalf("Print() error = %v", err)
		}

		output := buf.String()

		// Should contain the full long filename
		fullLongName := "this-is-a-very-very-very-very-very-very-very-very-very-long-filename-that-should-be-truncated.csv"
		if !strings.Contains(output, fullLongName) {
			t.Error("Expected to find full long filename when truncation is disabled")
		}
	})

	t.Run("with custom truncation at 30 chars", func(t *testing.T) {
		var buf bytes.Buffer
		p := NewPrinter(Options{
			Format:         FormatTable,
			Columns:        []string{"id", "file_name", "state"},
			MaxColumnWidth: 30,
			Writer:         &buf,
		})

		err := p.Print(jobs)
		if err != nil {
			t.Fatalf("Print() error = %v", err)
		}

		output := buf.String()

		// Medium length name should also be truncated
		if !strings.Contains(output, "...") {
			t.Error("Expected to find '...' (ellipsis) in output")
		}

		// Check that no line in the table is excessively long
		lines := strings.Split(output, "\n")
		for _, line := range lines {
			if len(line) > 200 { // reasonable limit for a 3-column table
				t.Errorf("Found excessively long line (%d chars): %s", len(line), line)
			}
		}
	})
}
