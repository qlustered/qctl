package apply

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
)

func TestSplitYAMLDocumentsSeparatesEachDocument(t *testing.T) {
	src := []byte(`apiVersion: qluster.ai/v1
kind: TableRule
metadata: {}
spec:
  dataset_id: 8
---
apiVersion: qluster.ai/v1
kind: TableRule
metadata:
  name: two
spec:
  dataset_id: 8
`)
	docs, err := splitYAMLDocuments(src)
	if err != nil {
		t.Fatalf("splitYAMLDocuments: %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("expected 2 documents, got %d", len(docs))
	}
	if strings.Contains(string(docs[0]), "name: two") {
		t.Errorf("document 1 leaked document 2's content:\n%s", docs[0])
	}
	if !strings.Contains(string(docs[1]), "name: two") {
		t.Errorf("document 2 lost its content:\n%s", docs[1])
	}
}

// A "---" at column 0 inside a block scalar is text, not a separator. This is
// the case a line-based split gets wrong.
func TestSplitYAMLDocumentsKeepsSeparatorInsideBlockScalar(t *testing.T) {
	src := []byte(`kind: Table
metadata:
  name: one
spec:
  note: |
    before
    ---
    after
`)
	docs, err := splitYAMLDocuments(src)
	if err != nil {
		t.Fatalf("splitYAMLDocuments: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 document, got %d: %q", len(docs), docs)
	}
}

func TestSplitYAMLDocumentsDropsEmptyDocuments(t *testing.T) {
	src := []byte("kind: Table\nmetadata:\n  name: one\n---\n\n---\n# only a comment\n---\nkind: Table\nmetadata:\n  name: two\n")
	docs, err := splitYAMLDocuments(src)
	if err != nil {
		t.Fatalf("splitYAMLDocuments: %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("expected 2 non-empty documents, got %d: %q", len(docs), docs)
	}
}

func TestSplitYAMLDocumentsPassesASingleDocumentThroughUnchanged(t *testing.T) {
	src := []byte("kind: Table\nmetadata:\n  name: one\nspec:\n  dataset_id: 1\n")
	docs, err := splitYAMLDocuments(src)
	if err != nil {
		t.Fatalf("splitYAMLDocuments: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 document, got %d", len(docs))
	}
	if !bytes.Equal(docs[0], src) {
		t.Errorf("single document was re-rendered; want the original bytes\n got: %q\nwant: %q", docs[0], src)
	}
}

func TestSplitYAMLDocumentsReturnsNoneForAnEmptyStream(t *testing.T) {
	for _, src := range []string{"", "# just a comment\n", "---\n---\n"} {
		docs, err := splitYAMLDocuments([]byte(src))
		if err != nil {
			t.Fatalf("splitYAMLDocuments(%q): %v", src, err)
		}
		if len(docs) != 0 {
			t.Errorf("splitYAMLDocuments(%q) = %d documents, want 0", src, len(docs))
		}
	}
}

func TestSplitYAMLDocumentsRejectsASyntaxError(t *testing.T) {
	if _, err := splitYAMLDocuments([]byte("kind: Table\n  bad: [indent\n")); err == nil {
		t.Fatal("expected a parse error for malformed YAML, got nil")
	}
}

// When empty/comment-only documents precede the sole real one, the real
// document must be isolated, not bundled with the empties.
func TestSplitYAMLDocumentsIsolatesARealDocumentAfterAnEmptyOne(t *testing.T) {
	src := []byte("---\n# just a header comment\n---\nkind: Table\nmetadata:\n  name: one\nspec:\n  dataset_id: 1\n")
	docs, err := splitYAMLDocuments(src)
	if err != nil {
		t.Fatalf("splitYAMLDocuments: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 document, got %d", len(docs))
	}
	// Verify the returned document does not contain the comment-only preamble.
	if strings.Contains(string(docs[0]), "header comment") {
		t.Errorf("returned document leaked the empty preamble:\n%s", docs[0])
	}
	// Verify it contains the real content.
	if !strings.Contains(string(docs[0]), "kind: Table") {
		t.Errorf("returned document lost the real content:\n%s", docs[0])
	}
}

// readManifestDocuments tests

func TestReadManifestDocumentsWithOneDocument(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "single.yaml")
	content := []byte("apiVersion: qluster.ai/v1\nkind: Table\nmetadata:\n  name: test-table\nspec:\n  dataset_id: 1\n")
	if err := os.WriteFile(filePath, content, 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	docs, err := readManifestDocuments(filePath)
	if err != nil {
		t.Fatalf("readManifestDocuments: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 document, got %d", len(docs))
	}

	doc := docs[0]
	if doc.Kind != "Table" {
		t.Errorf("expected Kind=Table, got %q", doc.Kind)
	}
	if doc.Name != "test-table" {
		t.Errorf("expected Name=test-table, got %q", doc.Name)
	}
	if doc.Source != filePath {
		t.Errorf("expected Source=%q (no [n] suffix), got %q", filePath, doc.Source)
	}
	if doc.Index != 1 || doc.Total != 1 {
		t.Errorf("expected Index=1 Total=1, got Index=%d Total=%d", doc.Index, doc.Total)
	}
	if doc.Err != nil {
		t.Errorf("expected Err=nil, got %v", doc.Err)
	}
}

func TestReadManifestDocumentsWithMultipleDocuments(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "multi.yaml")
	content := []byte("apiVersion: qluster.ai/v1\nkind: Table\nmetadata:\n  name: table-one\nspec:\n  dataset_id: 1\n---\napiVersion: qluster.ai/v1\nkind: Table\nmetadata:\n  name: table-two\nspec:\n  dataset_id: 2\n")
	if err := os.WriteFile(filePath, content, 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	docs, err := readManifestDocuments(filePath)
	if err != nil {
		t.Fatalf("readManifestDocuments: %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("expected 2 documents, got %d", len(docs))
	}

	// Check first document
	if docs[0].Kind != "Table" {
		t.Errorf("doc 0: expected Kind=Table, got %q", docs[0].Kind)
	}
	if docs[0].Name != "table-one" {
		t.Errorf("doc 0: expected Name=table-one, got %q", docs[0].Name)
	}
	if !strings.HasSuffix(docs[0].Source, "[1]") {
		t.Errorf("doc 0: expected Source with [1] suffix, got %q", docs[0].Source)
	}
	if docs[0].Index != 1 || docs[0].Total != 2 {
		t.Errorf("doc 0: expected Index=1 Total=2, got Index=%d Total=%d", docs[0].Index, docs[0].Total)
	}

	// Check second document
	if docs[1].Kind != "Table" {
		t.Errorf("doc 1: expected Kind=Table, got %q", docs[1].Kind)
	}
	if docs[1].Name != "table-two" {
		t.Errorf("doc 1: expected Name=table-two, got %q", docs[1].Name)
	}
	if !strings.HasSuffix(docs[1].Source, "[2]") {
		t.Errorf("doc 1: expected Source with [2] suffix, got %q", docs[1].Source)
	}
	if docs[1].Index != 2 || docs[1].Total != 2 {
		t.Errorf("doc 1: expected Index=2 Total=2, got Index=%d Total=%d", docs[1].Index, docs[1].Total)
	}
}

func TestReadManifestDocumentsNonexistentFile(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "nonexistent.yaml")

	_, err := readManifestDocuments(filePath)
	if err == nil {
		t.Fatal("expected error for nonexistent file, got nil")
	}
	// Verify it's a proper error with unwrap chain.
	if qerrors.GetExitCode(err) != qerrors.ExitGenericError {
		t.Errorf("expected exit code %d, got %d", qerrors.ExitGenericError, qerrors.GetExitCode(err))
	}
	// Verify it's still an os.IsNotExist error in the chain.
	var pathErr *os.PathError
	if !errors.As(err, &pathErr) {
		t.Errorf("expected error chain to contain *os.PathError, got %T", err)
	}
}

func TestReadManifestDocumentsEnvelopeParseError(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "bad-envelope.yaml")
	// Missing required apiVersion and kind; has a good second document.
	content := []byte("metadata:\n  name: broken\n---\napiVersion: qluster.ai/v1\nkind: Table\nmetadata:\n  name: good\nspec:\n  dataset_id: 1\n")
	if err := os.WriteFile(filePath, content, 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	docs, err := readManifestDocuments(filePath)
	if err != nil {
		t.Fatalf("readManifestDocuments: %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("expected 2 documents, got %d", len(docs))
	}

	// First document should have an error.
	if docs[0].Err == nil {
		t.Error("expected doc 0 to have an error, got nil")
	}
	// The broken envelope is the user's file being wrong: exit 2, and the chain
	// survives to carry it through applyStream's aggregate.
	if got := qerrors.GetExitCode(docs[0].Err); got != qerrors.ExitBadUsage {
		t.Errorf("doc 0 exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
	if docs[0].Kind != "" {
		t.Errorf("expected doc 0 Kind to be empty (parse failed), got %q", docs[0].Kind)
	}

	// Second document should be fine and not affected by the first's error.
	if docs[1].Err != nil {
		t.Errorf("expected doc 1 to have no error, got %v", docs[1].Err)
	}
	if docs[1].Kind != "Table" {
		t.Errorf("expected doc 1 Kind=Table, got %q", docs[1].Kind)
	}
	if docs[1].Name != "good" {
		t.Errorf("expected doc 1 Name=good, got %q", docs[1].Name)
	}
}

func TestReadManifestDocumentsNameFallback(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "name-fallback.yaml")
	// Document with metadata.id but no name; second with neither (instantiate TableRule).
	content := []byte("apiVersion: qluster.ai/v1\nkind: Destination\nmetadata:\n  id: dest-uuid-123\nspec:\n  type: bigquery\n---\napiVersion: qluster.ai/v1\nkind: TableRule\nmetadata: {}\nspec:\n  rule_revision_id: 42\n  dataset_id: 1\n")
	if err := os.WriteFile(filePath, content, 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	docs, err := readManifestDocuments(filePath)
	if err != nil {
		t.Fatalf("readManifestDocuments: %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("expected 2 documents, got %d", len(docs))
	}

	// First: name is empty, should fall back to id.
	if docs[0].Name != "dest-uuid-123" {
		t.Errorf("expected doc 0 Name to fall back to id (dest-uuid-123), got %q", docs[0].Name)
	}

	// Second: both name and id are empty, Name should be "".
	if docs[1].Name != "" {
		t.Errorf("expected doc 1 Name to be empty for instantiate TableRule, got %q", docs[1].Name)
	}

	// A named document carries no identity; the nameless one carries its spec's.
	if docs[0].Identity != (docIdentity{}) {
		t.Errorf("expected doc 0 (named by id) to carry no identity, got %+v", docs[0].Identity)
	}
	if want := (docIdentity{Kind: "TableRule", Rule: "42", DatasetID: "1"}); docs[1].Identity != want {
		t.Errorf("doc 1 Identity = %+v, want %+v", docs[1].Identity, want)
	}
}

func TestReadManifestDocumentsLabelsSlugsAndInvalidEnvelopes(t *testing.T) {
	tests := []struct {
		name    string
		content string
		label   string
		invalid bool
	}{
		{"standard field slug", "apiVersion: qluster.ai/v1\nkind: StandardField\nmetadata: {slug: policy-number}\nspec: {name: Policy Number}\n", "standard-field/policy-number", false},
		{"table-kind slug", "apiVersion: qluster.ai/v1\nkind: TableKind\nmetadata: {slug: policy}\nspec: {name: Policy, concepts: []}\n", "table-kind/policy", false},
		{"name precedes id and slug", "apiVersion: qluster.ai/v1\nkind: StandardField\nmetadata: {name: named, id: recorded, slug: policy-number}\n", "standard-field/named", false},
		{"id precedes slug", "apiVersion: qluster.ai/v1\nkind: StandardField\nmetadata: {id: recorded, slug: policy-number}\n", "standard-field/recorded", false},
		{"missing apiVersion", "kind: StandardField\nmetadata: {slug: policy-number}\n", "standard-field/policy-number", true},
		{"unknown metadata key", "apiVersion: qluster.ai/v1\nkind: TableKind\nmetadata: {slug: policy, typo: true}\n", "table-kind/policy", true},
		{"invalid metadata name", "apiVersion: qluster.ai/v1\nkind: StandardField\nmetadata: {name: [], slug: policy-number}\n", "standard-field/policy-number", true},
		{"invalid envelope with name", "kind: Table\nmetadata: {name: orders}\n", "table/orders", true},
		{"invalid envelope with id", "kind: TableRule\nmetadata: {id: recorded}\n", "table-rule/recorded", true},
		{"invalid envelope with kind alias", "kind: CompositeDataset\nmetadata: {name: linked}\n", "linked-table/linked", true},
		{"invalid envelope with instance name", "kind: TableRule\nmetadata: {}\nspec: {instance_name: Check, dataset_id: 8}\n", `table-rule "Check" (document 1 of 1)`, true},
		{"invalid envelope with rule", "kind: TableRule\nmetadata: {}\nspec: {rule: check, dataset_id: 8}\n", "table-rule for rule check on table 8 (document 1 of 1)", true},
		{"invalid JSON envelope", `{"kind":"StandardField","metadata":{"slug":"policy-number","typo":true}}`, "standard-field/policy-number", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filePath := filepath.Join(t.TempDir(), "manifest.yaml")
			if err := os.WriteFile(filePath, []byte(tt.content), 0600); err != nil {
				t.Fatal(err)
			}
			docs, err := readManifestDocuments(filePath)
			if err != nil {
				t.Fatal(err)
			}
			if len(docs) != 1 {
				t.Fatalf("got %d documents, want 1", len(docs))
			}
			if got := docLabel(docs[0]); got != tt.label {
				t.Errorf("label = %q, want %q", got, tt.label)
			}
			if tt.invalid {
				if got := qerrors.GetExitCode(docs[0].Err); got != qerrors.ExitBadUsage {
					t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
				}
			} else if docs[0].Err != nil {
				t.Errorf("valid envelope failed: %v", docs[0].Err)
			}
		})
	}
}

// A named document keeps today's label, so readManifestDocuments leaves its
// identity unread even when the spec carries one.
func TestReadManifestDocumentsSkipsIdentityForANamedDocument(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "named.yaml")
	content := []byte("apiVersion: qluster.ai/v1\nkind: TableRule\nmetadata:\n  id: 550e8400-e29b-41d4-a716-446655440000\nspec:\n  instance_name: renamed\n  rule: vehicle_aging\n  dataset_id: 8\n")
	if err := os.WriteFile(filePath, content, 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	docs, err := readManifestDocuments(filePath)
	if err != nil {
		t.Fatalf("readManifestDocuments: %v", err)
	}
	if docs[0].Identity != (docIdentity{}) {
		t.Errorf("expected no identity for a document with metadata.id, got %+v", docs[0].Identity)
	}
}

// Labelling never fails a document: a spec no handler could decode still
// leaves the document's Err nil.
func TestReadManifestDocumentsIdentityNeverAddsAnError(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "odd-spec.yaml")
	content := []byte("apiVersion: qluster.ai/v1\nkind: TableRule\nmetadata: {}\nspec: [rule, dataset_id]\n---\napiVersion: qluster.ai/v1\nkind: TableRule\nmetadata: {}\nspec:\n  rule: {name: vehicle_aging}\n  dataset_id: [8]\n  instance_name: null\n")
	if err := os.WriteFile(filePath, content, 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	docs, err := readManifestDocuments(filePath)
	if err != nil {
		t.Fatalf("readManifestDocuments: %v", err)
	}
	for i, doc := range docs {
		if doc.Err != nil {
			t.Errorf("doc %d: labelling must not fail a document, got Err=%v", i, doc.Err)
		}
		if doc.Identity != (docIdentity{Kind: "TableRule"}) {
			t.Errorf("doc %d: expected only the kind from non-scalar identity keys, got %+v", i, doc.Identity)
		}
	}
}

func TestIdentityOf(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		want docIdentity
	}{
		{
			name: "spec instance name, rule and table",
			doc:  "metadata: {}\nspec:\n  instance_name: Vehicle Aging\n  rule: vehicle_aging\n  dataset_id: 8\n",
			want: docIdentity{InstanceName: "Vehicle Aging", Rule: "vehicle_aging", DatasetID: "8"},
		},
		{
			name: "metadata instance name",
			doc:  "metadata:\n  instance_name: Vehicle Aging\nspec:\n  rule: vehicle_aging\n",
			want: docIdentity{InstanceName: "Vehicle Aging", Rule: "vehicle_aging"},
		},
		{
			name: "metadata instance name wins over spec",
			doc:  "metadata:\n  instance_name: from metadata\nspec:\n  instance_name: from spec\n",
			want: docIdentity{InstanceName: "from metadata"},
		},
		{
			name: "empty metadata instance name falls back to spec",
			doc:  "metadata:\n  instance_name: \"\"\nspec:\n  instance_name: from spec\n",
			want: docIdentity{InstanceName: "from spec"},
		},
		{
			name: "rule wins over rule_revision_id",
			doc:  "spec:\n  rule: vehicle_aging\n  rule_revision_id: aaaa0000-bbbb-cccc-dddd-eeeeeeee0001\n",
			want: docIdentity{Rule: "vehicle_aging"},
		},
		{
			name: "rule_revision_id when there is no rule",
			doc:  "spec:\n  rule_revision_id: aaaa0000-bbbb-cccc-dddd-eeeeeeee0001\n  dataset_id: \"8\"\n",
			want: docIdentity{Rule: "aaaa0000-bbbb-cccc-dddd-eeeeeeee0001", DatasetID: "8"},
		},
		{
			name: "JSON document",
			doc:  `{"metadata": {}, "spec": {"rule": "vehicle_aging", "dataset_id": 8}}`,
			want: docIdentity{Rule: "vehicle_aging", DatasetID: "8"},
		},
		{
			name: "aliased values",
			doc:  "defaults: &rule vehicle_aging\nspec:\n  rule: *rule\n",
			want: docIdentity{Rule: "vehicle_aging"},
		},
		{
			name: "null and non-scalar values",
			doc:  "metadata: []\nspec:\n  instance_name: ~\n  rule: [vehicle_aging]\n  dataset_id: {id: 8}\n",
			want: docIdentity{},
		},
		{
			name: "no spec",
			doc:  "metadata: {}\n",
			want: docIdentity{},
		},
		{
			name: "not a mapping",
			doc:  "- spec\n- rule\n",
			want: docIdentity{},
		},
		{
			name: "not YAML",
			doc:  "spec: [unterminated\n",
			want: docIdentity{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := identityOf([]byte(tt.doc)); got != tt.want {
				t.Errorf("identityOf() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestReadManifestDocumentsEmptyFile(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "empty.yaml")
	if err := os.WriteFile(filePath, []byte(""), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := readManifestDocuments(filePath)
	if err == nil {
		t.Fatal("expected error for empty file, got nil")
	}
	// Must be a NewBadUsagef (exit 2).
	if qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
		t.Errorf("expected exit code %d (BadUsage), got %d", qerrors.ExitBadUsage, qerrors.GetExitCode(err))
	}
	if !strings.Contains(err.Error(), "no YAML documents found") {
		t.Errorf("expected error message to mention no YAML documents, got: %v", err)
	}
}

func TestReadManifestDocumentsCommentOnlyFile(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "comments-only.yaml")
	if err := os.WriteFile(filePath, []byte("# This file has only comments\n# and no actual documents\n"), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := readManifestDocuments(filePath)
	if err == nil {
		t.Fatal("expected error for comment-only file, got nil")
	}
	// Must be a NewBadUsagef (exit 2).
	if qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
		t.Errorf("expected exit code %d (BadUsage), got %d", qerrors.ExitBadUsage, qerrors.GetExitCode(err))
	}
}
