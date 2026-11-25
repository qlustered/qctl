package rule_templates //nolint:revive // Public package name matches the established resource domain.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

// writeTemplate writes content to a template file in a fresh directory at
// mode perm and returns its path.
func writeTemplate(t *testing.T, content string, perm os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "monthly.yaml")
	if err := os.WriteFile(path, []byte(content), perm); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, perm); err != nil {
		t.Fatal(err)
	}
	return path
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// beforeWrite has comments, blank lines, a four-space indent with compact
// sequences, and every column_mapping shape WriteMappings adds to: none, a
// partial block mapping, {} with a comment, and a bare key.
const beforeWrite = `# Monthly orders template.
apiVersion: qluster.ai/v1
kind: TableRuleTemplate
metadata:
    name: monthly-orders   # lineage key

spec:
    rules:
    # totals
    - rule: order-total-rule
      release: 2.0.0
      instance_name: 'Order Total Check'
      params: {threshold: 12.5}   # display units

    - rule: customer-name-split-rule
      release: 1.3.0
      column_mapping:
          # the full name
          full_name: customer_name
      # end of split rule

    - rule: date-check
      release: 1.0.0
      column_mapping: {}  # filled below

    - rule: amount-check
      release: 1.0.0
      column_mapping:
    - rule: untouched-rule
      release: 1.0.0
# end of file
`

const afterWrite = `# Monthly orders template.
apiVersion: qluster.ai/v1
kind: TableRuleTemplate
metadata:
    name: monthly-orders   # lineage key

spec:
    rules:
    # totals
    - rule: order-total-rule
      release: 2.0.0
      instance_name: 'Order Total Check'
      params: {threshold: 12.5}   # display units
      column_mapping:
          "Amount: \"Net\"": # no free type-compatible column; name one
          "Purchase Time": # choose one: purchased_at (type ok; shares 'purchase'), created_at (type ok)

    - rule: customer-name-split-rule
      release: 1.3.0
      column_mapping:
          # the full name
          full_name: customer_name
          "first_name": # matches several columns; choose one: first_name_1 (type ok), given_name (type ok)
      # end of split rule

    - rule: date-check
      release: 1.0.0
      column_mapping:  # filled below
          "order_date": # choose one: ordered_on (type ok)

    - rule: amount-check
      release: 1.0.0
      column_mapping:
          "amount": # choose one: total (type ok)
    - rule: untouched-rule
      release: 1.0.0
# end of file
`

// writeItems are the merge items that turn beforeWrite into afterWrite; the
// last two add nothing.
func writeItems() []api.RuleMergeItemSchema {
	ambiguous := unbound("first_name", "first_name_1", "type ok", "given_name", "type ok")
	ambiguous.Status = api.Ambiguous
	return []api.RuleMergeItemSchema{
		unmappedItem("Order Total Check",
			unbound(`Amount: "Net"`),
			unbound("Purchase Time",
				"purchased_at", "type ok; shares 'purchase' with 'Purchase Time'",
				"created_at", "type ok")),
		unmappedItem("customer-name-split-rule", ambiguous, bound("full_name", "customer_name")),
		unmappedItem("date-check", unbound("order_date", "ordered_on", "type ok")),
		unmappedItem("amount-check", unbound("amount", "total", "type ok")),
		unmappedItem("untouched-rule", bound("value", "missing_column")),
	}
}

// TestWriteMappings_AddsOnlyTheKeys: the keys land under each entry's
// column_mapping, which is created where missing, and every other byte of the
// file, comments and blank lines included, stays; a second run adds nothing
// and leaves the file alone.
func TestWriteMappings_AddsOnlyTheKeys(t *testing.T) {
	path := writeTemplate(t, beforeWrite, 0o640)
	adds, err := WriteMappings(path, writeItems(), nil)
	if err != nil {
		t.Fatalf("WriteMappings: %v", err)
	}
	if len(adds) != 4 {
		t.Errorf("WriteMappings added to %d entries, want 4: %+v", len(adds), adds)
	}
	if got := readFile(t, path); got != afterWrite {
		t.Errorf("file after WriteMappings =\n%s\nwant\n%s", got, afterWrite)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Errorf("mode = %v, want 0640", info.Mode().Perm())
	}

	again, err := WriteMappings(path, writeItems(), nil)
	if err != nil || len(again) != 0 {
		t.Fatalf("second WriteMappings = %+v, %v; want nothing added", again, err)
	}
	if got := readFile(t, path); got != afterWrite {
		t.Errorf("second WriteMappings changed the file:\n%s", got)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("directory holds %d files, want only the template", len(entries))
	}
}

// TestWriteMappings_ParseRefusesTheEmptyValueUntilFilled: what WriteMappings
// writes fails Parse with exit 2 naming the entry and the field, and parses
// once the value is filled in.
func TestWriteMappings_ParseRefusesTheEmptyValueUntilFilled(t *testing.T) {
	const template = `apiVersion: qluster.ai/v1
kind: TableRuleTemplate
metadata:
  name: monthly-orders
spec:
  rules:
    - rule: order-total-rule
      release: 2.0.0
      instance_name: Order Total Check
`
	path := writeTemplate(t, template, 0o644)
	if _, err := WriteMappings(path, []api.RuleMergeItemSchema{
		unmappedItem("Order Total Check", unbound("Purchase Time", "purchased_at", "type ok")),
	}, nil); err != nil {
		t.Fatalf("WriteMappings: %v", err)
	}
	written := readFile(t, path)

	_, err := Parse([]byte(written), path)
	if got := qerrors.GetExitCode(err); err == nil || got != qerrors.ExitBadUsage {
		t.Fatalf("Parse of the written file: err = %v (exit %d), want exit %d", err, got, qerrors.ExitBadUsage)
	}
	for _, want := range []string{`spec.rules[0] ("Order Total Check")`, `column_mapping["Purchase Time"] is empty`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Parse error %q does not contain %q", err, want)
		}
	}

	filled := strings.Replace(written, `"Purchase Time": #`, `"Purchase Time": purchased_at #`, 1)
	f, err := Parse([]byte(filled), path)
	if err != nil {
		t.Fatalf("Parse after filling the value: %v", err)
	}
	if got := (*f.Template.Spec.Rules[0].ColumnMapping)["Purchase Time"]; got != "purchased_at" {
		t.Errorf("column_mapping[Purchase Time] = %q, want purchased_at", got)
	}
}

// TestWriteMappings_Refuses: input WriteMappings will not rewrite exits 2
// and leaves the file as it was.
func TestWriteMappings_Refuses(t *testing.T) {
	items := []api.RuleMergeItemSchema{unmappedItem("a", unbound("f"))}
	tests := []struct {
		name    string
		content string
	}{
		{
			name: "JSON",
			content: `{"apiVersion": "qluster.ai/v1", "kind": "TableRuleTemplate", "metadata": {"name": "t"},
"spec": {"rules": [{"rule": "a", "release": "1.0.0"}]}}`,
		},
		{
			name:    "two documents",
			content: "apiVersion: qluster.ai/v1\nkind: TableRuleTemplate\nmetadata:\n  name: t\nspec:\n  rules:\n    - rule: a\n      release: 1.0.0\n---\nkind: Other\n",
		},
		{
			name:    "a comment-only second document",
			content: "apiVersion: qluster.ai/v1\nkind: TableRuleTemplate\nmetadata:\n  name: t\nspec:\n  rules:\n    - rule: a\n      release: 1.0.0\n---\n# notes\n",
		},
		{
			name:    "a flow-style entry",
			content: "apiVersion: qluster.ai/v1\nkind: TableRuleTemplate\nmetadata:\n  name: t\nspec:\n  rules:\n    - {rule: a, release: 1.0.0}\n",
		},
		{
			name:    "a flow-style column_mapping with keys",
			content: "apiVersion: qluster.ai/v1\nkind: TableRuleTemplate\nmetadata:\n  name: t\nspec:\n  rules:\n    - rule: a\n      release: 1.0.0\n      column_mapping: {g: h}\n",
		},
		{
			name:    "no spec.rules",
			content: "apiVersion: qluster.ai/v1\nkind: TableRuleTemplate\nmetadata:\n  name: t\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeTemplate(t, tt.content, 0o644)
			_, err := WriteMappings(path, items, nil)
			if got := qerrors.GetExitCode(err); err == nil || got != qerrors.ExitBadUsage {
				t.Fatalf("err = %v (exit %d), want exit %d", err, got, qerrors.ExitBadUsage)
			}
			if got := readFile(t, path); got != tt.content {
				t.Errorf("the file changed:\n%s", got)
			}
		})
	}
}

// TestWriteMappings_RefusesStdin: "-" names no file to write into.
func TestWriteMappings_RefusesStdin(t *testing.T) {
	_, err := WriteMappings("-", nil, nil)
	if got := qerrors.GetExitCode(err); err == nil || got != qerrors.ExitBadUsage {
		t.Fatalf("err = %v (exit %d), want exit %d", err, got, qerrors.ExitBadUsage)
	}
}

// TestWriteMappings_RefusesAnInsertionThatChangesOtherContent: when the lines
// would land inside a block scalar, nothing is written and the error carries
// the lines to add by hand.
func TestWriteMappings_RefusesAnInsertionThatChangesOtherContent(t *testing.T) {
	const content = `apiVersion: qluster.ai/v1
kind: TableRuleTemplate
metadata:
  name: t
spec:
  rules:
    - rule: a
      release: 1.0.0
      params:
        note: |
          text
          # not a comment
`
	path := writeTemplate(t, content, 0o644)
	_, err := WriteMappings(path, []api.RuleMergeItemSchema{unmappedItem("a", unbound("f", "c", "type ok"))}, nil)
	if got := qerrors.GetExitCode(err); err == nil || got != qerrors.ExitBadUsage {
		t.Fatalf("err = %v (exit %d), want exit %d", err, got, qerrors.ExitBadUsage)
	}
	if !strings.Contains(err.Error(), `"f": # choose one: c (type ok)`) {
		t.Errorf("error %q does not carry the lines to add", err)
	}
	if got := readFile(t, path); got != content {
		t.Errorf("the file changed:\n%s", got)
	}
}

// TestWriteMappings_KeepsCRLF: a CRLF file gets CRLF lines.
func TestWriteMappings_KeepsCRLF(t *testing.T) {
	content := strings.ReplaceAll("apiVersion: qluster.ai/v1\nkind: TableRuleTemplate\nmetadata:\n  name: t\n"+
		"spec:\n  rules:\n    - rule: a\n      release: 1.0.0\n      column_mapping: ~\n", "\n", "\r\n")
	path := writeTemplate(t, content, 0o644)
	if _, err := WriteMappings(path, []api.RuleMergeItemSchema{unmappedItem("a", unbound("f"))}, nil); err != nil {
		t.Fatalf("WriteMappings: %v", err)
	}
	want := strings.ReplaceAll("apiVersion: qluster.ai/v1\nkind: TableRuleTemplate\nmetadata:\n  name: t\n"+
		"spec:\n  rules:\n    - rule: a\n      release: 1.0.0\n      column_mapping:\n"+
		"        \"f\": # no free type-compatible column; name one\n", "\n", "\r\n")
	if got := readFile(t, path); got != want {
		t.Errorf("file = %q, want %q", got, want)
	}
}

// TestWriteMappings_WritesThroughASymlink: the link's target gets the keys
// and the link stays a link.
func TestWriteMappings_WritesThroughASymlink(t *testing.T) {
	target := writeTemplate(t, "apiVersion: qluster.ai/v1\nkind: TableRuleTemplate\nmetadata:\n  name: t\n"+
		"spec:\n  rules:\n    - rule: a\n      release: 1.0.0\n", 0o600)
	link := filepath.Join(t.TempDir(), "link.yaml")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteMappings(link, []api.RuleMergeItemSchema{unmappedItem("a", unbound("f"))}, nil); err != nil {
		t.Fatalf("WriteMappings: %v", err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the link is no longer a symlink: %v", err)
	}
	if got := readFile(t, target); !bytes.Contains([]byte(got), []byte(`"f": #`)) {
		t.Errorf("the target did not get the key:\n%s", got)
	}
}

// TestCheckMappingsWritable: stdin, JSON and a flow-style top level are
// refused with exit 2 from the parsed file alone; a block-style file passes,
// flow-style entries included, since those are refused only when a key is
// added to one.
func TestCheckMappingsWritable(t *testing.T) {
	const block = "apiVersion: qluster.ai/v1\nkind: TableRuleTemplate\nmetadata:\n  name: t\nspec:\n  rules:\n    - {rule: a, release: 1.0.0}\n"
	tests := []struct {
		name, source, content string
		refused               bool
	}{
		{"block", "t.yaml", block, false},
		{"stdin", "-", block, true},
		{"JSON", "t.json", `{"apiVersion": "qluster.ai/v1", "kind": "TableRuleTemplate", "metadata": {"name": "t"},
"spec": {"rules": [{"rule": "a", "release": "1.0.0"}]}}`, true},
		{"flow-style YAML", "t.yaml", "{apiVersion: qluster.ai/v1, kind: TableRuleTemplate, metadata: {name: t}, " +
			"spec: {rules: [{rule: a, release: 1.0.0}]}}\n", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file, err := Parse([]byte(tt.content), tt.source)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			err = file.CheckMappingsWritable()
			if !tt.refused {
				if err != nil {
					t.Fatalf("CheckMappingsWritable = %v, want nil", err)
				}
				return
			}
			if got := qerrors.GetExitCode(err); err == nil || got != qerrors.ExitBadUsage {
				t.Fatalf("err = %v (exit %d), want exit %d", err, got, qerrors.ExitBadUsage)
			}
		})
	}
}
