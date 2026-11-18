package manifest

import (
	"bytes"

	"github.com/qlustered/qctl/internal/pkg/jsonnum"
	"gopkg.in/yaml.v3"
)

// StrictUnmarshalNode decodes a YAML node and rejects unknown fields.
func StrictUnmarshalNode(node *yaml.Node, v interface{}) error {
	// Validate alias expansion before copying references into the subtree.
	var value interface{}
	if err := node.Decode(&value); err != nil {
		return formatYAMLError(err)
	}
	var data bytes.Buffer
	encoder := jsonnum.NewYAMLEncoder(&data)
	encoder.SetIndent(2)
	if err := encoder.Encode(inlineYAMLAliases(node)); err != nil {
		return err
	}
	if err := encoder.Close(); err != nil {
		return err
	}
	return StrictUnmarshal(data.Bytes(), v)
}

func inlineYAMLAliases(node *yaml.Node) *yaml.Node {
	if node.Kind == yaml.AliasNode {
		return inlineYAMLAliases(node.Alias)
	}
	cloned := *node
	cloned.Anchor = ""
	cloned.Content = make([]*yaml.Node, len(node.Content))
	for i, child := range node.Content {
		cloned.Content[i] = inlineYAMLAliases(child)
	}
	return &cloned
}
