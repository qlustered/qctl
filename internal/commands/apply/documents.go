package apply

import (
	"cmp"
	"fmt"
	"os"

	qerrors "github.com/qlustered/qctl/internal/errors"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
	"gopkg.in/yaml.v3"
)

// manifestDoc is one YAML document out of a -f file, carrying enough context
// for a handler to name itself in an error and for the runner to name it in a
// per-document outcome line.
type manifestDoc struct {
	// Data is this document's own YAML, ready to unmarshal.
	Data []byte
	// Kind is the manifest kind verbatim; "" when the envelope did not parse.
	Kind string
	// Name is metadata.name, falling back to metadata.id, then metadata.slug.
	Name string
	// Identity holds label keys recovered by a tolerant decode.
	Identity docIdentity
	// Source is what a message calls this document: "f.yaml" when the file
	// holds one, "f.yaml[2]" when it holds several. Single-document files keep
	// today's messages byte-identical; multi-document files disambiguate.
	Source string
	// Path is the -f file the document came from.
	Path string
	// Index is the 1-based position in the file; Total is how many there are.
	Index int
	Total int
	// Err is an envelope-level failure for this document alone. It is recorded
	// rather than returned so one bad document out of seven does not stop the
	// other six.
	Err error
}

// splitYAMLDocuments cuts a YAML stream into one byte slice per document.
//
// The implementation lives in internal/pkg/manifest so that `qctl diff -f`,
// which refuses a multi-document file, counts documents by exactly the rule
// apply splits them by. This wrapper stays because the split is apply's
// vocabulary and every caller and test in this package spells it this way.
func splitYAMLDocuments(data []byte) ([][]byte, error) {
	return pkgmanifest.SplitDocuments(data)
}

// readManifestDocuments reads filePath and returns one manifestDoc per YAML
// document, with the kind, the name and, for a nameless document, its
// identity already pulled off so the runner can dispatch and label without
// re-parsing.
func readManifestDocuments(filePath string) ([]manifestDoc, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		// Exit 1, not 2, and deliberately: an unreadable path is an I/O failure
		// (permissions, a broken mount, a path that never existed), not a
		// manifest qctl parsed and rejected. Exit 2 would tell a script its
		// arguments were malformed when they may be perfectly well-formed.
		return nil, fmt.Errorf("failed to read file %s: %w", filePath, err)
	}
	raw, err := splitYAMLDocuments(data)
	if err != nil {
		return nil, qerrors.Wrap(qerrors.ExitBadUsage, err, "failed to parse manifest")
	}
	if len(raw) == 0 {
		return nil, qerrors.NewBadUsagef("no YAML documents found in %s", filePath)
	}
	docs := make([]manifestDoc, 0, len(raw))
	for i, b := range raw {
		doc := manifestDoc{Data: b, Index: i + 1, Total: len(raw), Source: filePath, Path: filePath}
		if len(raw) > 1 {
			doc.Source = fmt.Sprintf("%s[%d]", filePath, i+1)
		}
		envelope, err := pkgmanifest.LoadBytes(b)
		if err != nil {
			// Same wording genericApply used, so a single-document file with a
			// broken envelope reports exactly what it reports today. Wrap, not
			// NewBadUsagef: NewBadUsagef goes through fmt.Sprintf, which renders
			// a %w as %!w(...) and drops the cause.
			doc.Err = qerrors.Wrap(qerrors.ExitBadUsage, err, "failed to parse manifest")
			doc.Identity = identityOf(b)
		} else {
			doc.Kind = envelope.Kind
			doc.Name = cmp.Or(envelope.Metadata.Name, envelope.Metadata.ID, envelope.Metadata.Slug)
			if doc.Name == "" {
				doc.Identity = identityOf(b)
			}
		}
		docs = append(docs, doc)
	}
	return docs, nil
}

// docIdentity holds a document's label keys.
type docIdentity struct {
	// Kind is the manifest kind.
	Kind string
	// Name is metadata.name, falling back to metadata.id, then metadata.slug.
	Name string
	// InstanceName is metadata.instance_name, falling back to
	// spec.instance_name.
	InstanceName string
	// Rule is spec.rule, falling back to spec.rule_revision_id.
	Rule string
	// DatasetID is spec.dataset_id.
	DatasetID string
}

// identityOf reads a document's identity keys. It never fails: a document
// that does not parse, or a key that is missing, null or not a scalar, leaves
// the field "".
func identityOf(data []byte) docIdentity {
	var doc struct {
		Kind     yaml.Node `yaml:"kind"`
		Metadata yaml.Node `yaml:"metadata"`
		Spec     yaml.Node `yaml:"spec"`
	}
	if yaml.Unmarshal(data, &doc) != nil {
		return docIdentity{}
	}
	return docIdentity{
		Kind:         scalarValue(&doc.Kind),
		Name:         cmp.Or(scalarAt(&doc.Metadata, "name"), scalarAt(&doc.Metadata, "id"), scalarAt(&doc.Metadata, "slug")),
		InstanceName: cmp.Or(scalarAt(&doc.Metadata, "instance_name"), scalarAt(&doc.Spec, "instance_name")),
		Rule:         cmp.Or(scalarAt(&doc.Spec, "rule"), scalarAt(&doc.Spec, "rule_revision_id")),
		DatasetID:    scalarAt(&doc.Spec, "dataset_id"),
	}
}

// scalarAt returns the non-null scalar value of key in the mapping node, or ""
// when node is not a mapping or key is missing, null or not a scalar.
func scalarAt(node *yaml.Node, key string) string {
	node = resolveAlias(node)
	if node.Kind != yaml.MappingNode {
		return ""
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value != key {
			continue
		}
		return scalarValue(node.Content[i+1])
	}
	return ""
}

// scalarValue returns a non-null scalar's value, or "".
func scalarValue(node *yaml.Node) string {
	node = resolveAlias(node)
	if node.Kind != yaml.ScalarNode || node.Tag == "!!null" {
		return ""
	}
	return node.Value
}

// resolveAlias returns the node an alias points at, or node itself.
func resolveAlias(node *yaml.Node) *yaml.Node {
	if node.Kind == yaml.AliasNode && node.Alias != nil {
		return node.Alias
	}
	return node
}
