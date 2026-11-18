package manifest

import (
	"bytes"
	"io"

	"gopkg.in/yaml.v3"
)

// SplitDocuments cuts a YAML stream into one byte slice per document. A valid
// JSON object is one document and keeps its original bytes.
//
// It parses with yaml.v3 rather than splitting on "---" lines, so a "---" at
// column 0 inside a block scalar stays inside its document. Empty documents (a
// bare "---", or one holding only comments) are dropped, matching what the old
// map-decoding loop in apply's rules.go did when a document decoded to a nil
// map.
//
// A raw stream holding exactly one non-empty document returns the original
// bytes untouched; a stream whose documents are all empty returns none at all,
// whatever its raw count; a stream with multiple raw documents always
// re-marshals non-empty ones. Re-rendering a node is lossless for values but
// not for layout, and StrictUnmarshal's unknown-field errors quote a line
// number, so every single-document manifest keeps producing byte-identical
// errors.
//
// It lives here, rather than in internal/commands/apply where it was born,
// because `qctl diff -f` has to count a file's documents exactly the way
// `qctl apply -f` splits them. Two independent counters would disagree on a
// trailing "---" or a comment-only document, and diff would refuse a file
// apply treats as one document.
func SplitDocuments(data []byte) ([][]byte, error) {
	if isJSONDocument(data) {
		return [][]byte{data}, nil
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var nodes []*yaml.Node
	rawCount := 0
	for {
		var node yaml.Node
		err := dec.Decode(&node)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		rawCount++
		if isEmptyDocument(&node) {
			continue
		}
		nodes = append(nodes, &node)
	}
	switch {
	case len(nodes) == 0:
		return nil, nil
	case rawCount == 1:
		// The raw stream held exactly one document; return it unchanged to
		// preserve layout and error line numbers.
		return [][]byte{data}, nil
	}
	docs := make([][]byte, 0, len(nodes))
	for _, n := range nodes {
		b, err := yaml.Marshal(n)
		if err != nil {
			return nil, err
		}
		docs = append(docs, b)
	}
	return docs, nil
}

// isEmptyDocument reports whether a decoded document carries no content:
// yaml.v3 hands back a DocumentNode wrapping a null scalar both for "---\n---"
// and for a document that holds only comments.
func isEmptyDocument(node *yaml.Node) bool {
	if node.Kind != yaml.DocumentNode || len(node.Content) == 0 {
		return true
	}
	inner := node.Content[0]
	return inner.Kind == yaml.ScalarNode && inner.Tag == "!!null"
}
