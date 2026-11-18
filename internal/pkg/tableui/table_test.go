package tableui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
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
			name:    "wide characters truncate by display width",
			columns: []string{"name"},
			rows: []map[string]string{
				{"name": "東東東東"},
			},
			noHeaders:   true,
			maxColWidth: 7,
			wantContain: []string{"東東..."},
			wantMissing: []string{"東東東東"},
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

func TestRenderSanitizesTerminalControls(t *testing.T) {
	var buf bytes.Buffer
	rows := []map[string]string{{"name": "before\x1b[?25l\x00\x7f\u009bafter"}}
	if err := Render(&buf, []string{"name"}, rows, true, 80); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsRune(buf.String(), '\x1b') || !strings.Contains(buf.String(), `before\x1b[?25l\x00\x7f\x9bafter`) {
		t.Errorf("Render did not sanitize its cell: %q", buf.String())
	}
}

func TestSetDefaultColumnsReturnsFlagError(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("output", "table", "")

	if err := setDefaultColumns(cmd, "name,release"); err == nil {
		t.Fatal("setDefaultColumns() error = nil, want missing columns flag error")
	}
}

type tableTestRow struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// TestPrintFromCmdRejectsUnknownColumnOnEmptyResult mirrors
// TestPrintTableRejectsUnknownColumnOnEmptyResult from
// internal/output/columns_test.go and internal/pkg/printer/printer_test.go:
// an unknown --columns name must fail the same way on an empty result set as
// on a full one, because AvailableColumns reflects the display struct's type
// rather than reading rows[0]. PrintFromCmd — one of the brief's two named
// call sites — had no direct test of this at all; this pins it end to end.
func TestPrintFromCmdRejectsUnknownColumnOnEmptyResult(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("output", "table", "")
	cmd.Flags().String("columns", "bogus_column", "")
	cmd.Flags().Bool("no-headers", false, "")
	cmd.Flags().Bool("allow-plaintext-secrets", false, "")
	cmd.Flags().Int("max-column-width", 80, "")

	var buf bytes.Buffer
	cmd.SetOut(&buf)

	err := PrintFromCmd(cmd, []tableTestRow{}, "id,name")
	if err == nil || !strings.Contains(err.Error(), `unknown column "bogus_column"`) {
		t.Fatalf("expected an unknown-column error on an empty result, got %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("nothing should have been printed, got %q", buf.String())
	}
}

type messageTestRow struct {
	ID  int    `json:"id"`
	Msg string `json:"msg"`
}

const longMarkedFile = "quarterly_bordereau_2026_Q3_final_revised_after_review_by_underwriting_team_v12 (copy).csv"

// newMessageTestCmd is a table command writing to buf, with
// --max-column-width registered as the root registers it.
func newMessageTestCmd(buf *bytes.Buffer) *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Flags().String("output", "table", "")
	cmd.Flags().String("columns", "", "")
	cmd.Flags().Bool("no-headers", true, "")
	cmd.Flags().Bool("allow-plaintext-secrets", false, "")
	cmd.Flags().Int("max-column-width", 0, "")
	cmd.SetOut(buf)
	return cmd
}

// This message fits --max-column-width by shortening its marked value alone:
// the prose stays whole, nothing is clipped, and the cell stays on one line.
func TestPrintMessagesFromCmdFitsMarkedValuesToTheColumnWidth(t *testing.T) {
	file := longMarkedFile
	var buf bytes.Buffer
	cmd := newMessageTestCmd(&buf)
	if err := cmd.Flags().Set("max-column-width", "50"); err != nil {
		t.Fatal(err)
	}

	rows := []messageTestRow{{1, `The file "~*` + file + `*~" could not be read.`}}
	if err := PrintMessagesFromCmd(cmd, rows, "id,msg", []string{"msg"}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, file) || !strings.Contains(out, "…") {
		t.Errorf("the marked value was not shortened:\n%s", out)
	}
	if !strings.Contains(out, `The file "quarterly_`) || !strings.Contains(out, `.csv" could not be read.`) {
		t.Errorf("the prose or the value's head and tail were lost:\n%s", out)
	}
	start := strings.Index(out, "The file")
	end := strings.Index(out, "read.") + len("read.")
	if start < 0 || end < start {
		t.Fatalf("message not found:\n%s", out)
	}
	if w := len([]rune(out[start:end])); w != 50 {
		t.Errorf("message is %d cells, want exactly the 50-cell cap: %q", w, out[start:end])
	}
}

// Piped, with no --max-column-width passed, the message is not a preview: the
// marked value prints whole, so the identifier in it can be copied.
func TestPrintMessagesFromCmdKeepsAMarkedValueWholeOnAPipe(t *testing.T) {
	var buf bytes.Buffer
	cmd := newMessageTestCmd(&buf)

	rows := []messageTestRow{{1, `The file "~*` + longMarkedFile + `*~" could not be read.`}}
	if err := PrintMessagesFromCmd(cmd, rows, "id,msg", []string{"msg"}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if want := `The file "` + longMarkedFile + `" could not be read.`; !strings.Contains(out, want) {
		t.Errorf("the message was cut on a pipe; want %q in:\n%s", want, out)
	}
	if strings.Contains(out, "…") || strings.Contains(out, "...") {
		t.Errorf("the message was shortened on a pipe:\n%s", out)
	}
}
