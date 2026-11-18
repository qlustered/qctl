package manifest

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestStrictUnmarshalNodeResolvesExternalAliasesWithoutLosingValues(t *testing.T) {
	var node yaml.Node
	doc := "number: &number 18446744073709551615\nvalue:\n  count: *number\n  text: |\n    first\n      second\n"
	if err := yaml.Unmarshal([]byte(doc), &node); err != nil {
		t.Fatal(err)
	}
	var value struct {
		Count uint64 `yaml:"count"`
		Text  string `yaml:"text"`
	}
	if err := StrictUnmarshalNode(node.Content[0].Content[3], &value); err != nil {
		t.Fatal(err)
	}
	if value.Count != 18446744073709551615 || value.Text != "first\n  second\n" {
		t.Fatalf("values changed: %+v", value)
	}
}

func TestStrictUnmarshalNodeRejectsUnknownAliasedFieldsAndCycles(t *testing.T) {
	for _, doc := range []string{
		"base: &base {typo: 1}\nvalue: {<<: *base}\n",
		"base: &base {known: *base}\nvalue: *base\n",
	} {
		var node yaml.Node
		if err := yaml.Unmarshal([]byte(doc), &node); err != nil {
			t.Fatal(err)
		}
		var value struct {
			Known string `yaml:"known"`
		}
		if err := StrictUnmarshalNode(node.Content[0].Content[3], &value); err == nil {
			t.Fatalf("invalid subtree accepted: %s", doc)
		}
	}
}
