package row_filters //nolint:revive // Matches the resource domain.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
)

const fileField = "--filter-file"

func writeFilterFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadFileReadsYAML(t *testing.T) {
	path := writeFilterFile(t, "filter.yaml", `description: EU orders
conditions:
  - column_name: region
    operator: eq
    value: EU
    case_sensitive: true
predicate:
  source: |
    def applies(row):
        return True
  columns: [amount]
`)
	f, err := ReadFile(path, fileField, true)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if f.DescriptionText() != "EU orders" {
		t.Errorf("description = %q", f.DescriptionText())
	}
	if len(f.Conditions) != 1 || f.Conditions[0].ColumnName != "region" || f.Conditions[0].Operator != "eq" ||
		f.Conditions[0].Value == nil || *f.Conditions[0].Value != "EU" || !f.Conditions[0].CaseSensitive {
		t.Errorf("conditions = %+v", f.Conditions)
	}
	if f.Predicate == nil || f.Predicate.Source != "def applies(row):\n    return True\n" || len(f.Predicate.Columns) != 1 {
		t.Errorf("predicate = %+v", f.Predicate)
	}
}

func TestReadFileReadsJSON(t *testing.T) {
	path := writeFilterFile(t, "filter.json", `{"conditions": [{"column_name": "status", "operator": "in", "value_list": ["open", "held"]}]}`)
	f, err := ReadFile(path, fileField, false)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if f.Description != nil {
		t.Errorf("description = %q, want none", *f.Description)
	}
	if len(f.Conditions) != 1 || f.Conditions[0].ValueList == nil || strings.Join(*f.Conditions[0].ValueList, ",") != "open,held" {
		t.Errorf("conditions = %+v", f.Conditions)
	}
}

func TestReadFileRefusals(t *testing.T) {
	for _, tt := range []struct {
		name    string
		content string
		saved   bool
		want    string
	}{
		{"multi-document", "conditions: [{column_name: a, operator: is_blank}]\n---\nconditions: [{column_name: b, operator: is_blank}]\n", false, "2 documents"},
		{"empty", "", false, "is empty"},
		{"comments only", "# nothing here\n", false, "is empty"},
		{"null", "null\n", false, "is empty"},
		{"unknown key", "conditions: [{column_name: a, operator: is_blank}]\nlimit: 5\n", false, "limit"},
		{"unknown nested key", "conditions: [{column: a, operator: is_blank}]\n", false, "column"},
		{"unknown JSON key", `{"conditions": [{"column_name": "a", "operator": "is_blank"}], "limit": 5}`, false, "limit"},
		{"not a mapping", "- column_name: a\n", false, fileField},
		{"invalid YAML", "conditions: [\n", false, "not valid"},
		{"no condition or predicate", "description: nothing\n", false, "at least one condition or a predicate"},
		{"empty mapping", "{}\n", false, "at least one condition or a predicate"},
		{"saved without description", "conditions: [{column_name: a, operator: is_blank}]\n", true, fileField + " needs a description: describe which rows the filter keeps"},
		{"blank description", "description: '  '\nconditions: [{column_name: a, operator: is_blank}]\n", false, fileField + " has a blank description: describe which rows the filter keeps, or leave description out"},
		{"saved blank description", "description: '  '\nconditions: [{column_name: a, operator: is_blank}]\n", true, fileField + " has a blank description: describe which rows the filter keeps"},
		{"long description", "description: " + strings.Repeat("x", 201) + "\nconditions: [{column_name: a, operator: is_blank}]\n", false, fileField + " has a description over 200 characters"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := writeFilterFile(t, "filter.yaml", tt.content)
			f, err := ReadFile(path, fileField, tt.saved)
			if err == nil {
				t.Fatalf("ReadFile accepted it: %+v", f)
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d: %v", code, qerrors.ExitBadUsage, err)
			}
			if !strings.Contains(err.Error(), fileField) {
				t.Errorf("error does not name %s: %v", fileField, err)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to mention %q", err, tt.want)
			}
		})
	}
}

// An unreadable path is an I/O failure, not a filter qctl parsed and refused,
// so it exits 1 as an unreadable `apply -f` file does.
func TestReadFileUnreadablePathExitsOne(t *testing.T) {
	for name, path := range map[string]string{
		"missing":   filepath.Join(t.TempDir(), "nope.yaml"),
		"directory": t.TempDir(),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ReadFile(path, fileField, false)
			if err == nil {
				t.Fatal("ReadFile accepted an unreadable path")
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitGenericError {
				t.Errorf("exit code = %d, want %d: %v", code, qerrors.ExitGenericError, err)
			}
			if !strings.Contains(err.Error(), fileField) {
				t.Errorf("error does not name %s: %v", fileField, err)
			}
		})
	}
}

func TestReadFileRefusesAnEmptyPath(t *testing.T) {
	_, err := ReadFile("", fileField, false)
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d: %v", code, qerrors.ExitBadUsage, err)
	}
}
