package rule_templates //nolint:revive // Public package name matches the established resource domain.

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
	"github.com/qlustered/qctl/internal/terminal"
	"gopkg.in/yaml.v3"
)

// MappingsToAdd is one template entry's rule fields that need a column_mapping
// key: the required fields a merge item left unbound or ambiguous.
type MappingsToAdd struct {
	// Index is the entry's index in spec.rules.
	Index        int
	Rule         string
	InstanceName *string
	Fields       []FieldToMap
}

// FieldToMap is one rule field to add under an entry's column_mapping.
type FieldToMap struct {
	Name string
	// Comment is CandidateComment of the field's binding, followed by
	// producedHint when the item's rule revision enriches the field.
	Comment string
}

// Enriches reports whether the rule revision revisionID enriches the rule
// field field; a nil Enriches reports none.
type Enriches func(revisionID, field string) bool

// producedHint follows the candidates of a field the rule enriches: the entry
// can declare it under produced_fields instead, which adds a column.
const producedHint = "; or drop this key and list the field under produced_fields to add it as a new column " +
	"(dataset_field_name names the column)"

// entryMapping is what matching merge items to a template entry needs.
type entryMapping struct {
	rule         string
	instanceName *string
	mapped       map[string]bool
}

// key is the entry's key: its instance_name when set, else its rule.
func (e entryMapping) key() string {
	if e.instanceName != nil {
		return *e.instanceName
	}
	return e.rule
}

// Mappings returns, in spec.rules order, the fields to add to each entry of f
// that an item of items with reason unmapped names by its template_key: its
// required field bindings that are not bound, less the fields the entry's
// column_mapping already has. A field the item's rule revision enriches, by
// enriches, gets producedHint in its comment. An entry with nothing to add is
// left out, so an item made unmapped only by a mapped column the table lacks
// or by a produced field yields nothing.
func Mappings(f *File, items []api.RuleMergeItemSchema, enriches Enriches) []MappingsToAdd {
	entries := make([]entryMapping, len(f.Template.Spec.Rules))
	for i, entry := range f.Template.Spec.Rules {
		entries[i] = entryMapping{rule: entry.Rule, instanceName: entry.InstanceName, mapped: map[string]bool{}}
		if entry.ColumnMapping != nil {
			for field := range *entry.ColumnMapping {
				entries[i].mapped[field] = true
			}
		}
	}
	return mappingsFor(entries, items, enriches)
}

// mappingsFor is Mappings over entries.
func mappingsFor(entries []entryMapping, items []api.RuleMergeItemSchema, enriches Enriches) []MappingsToAdd {
	byKey := map[string][]api.RuleMergeItemSchema{}
	for _, item := range items {
		if item.TemplateKey == nil || item.Reason == nil || *item.Reason != api.RuleMergeItemReasonUnmapped {
			continue
		}
		byKey[*item.TemplateKey] = append(byKey[*item.TemplateKey], item)
	}
	var out []MappingsToAdd
	for i, entry := range entries {
		add := MappingsToAdd{Index: i, Rule: entry.rule, InstanceName: entry.instanceName}
		seen := map[string]bool{}
		for _, item := range byKey[entry.key()] {
			for _, binding := range item.FieldBindings {
				if !binding.Required || binding.Status == api.Bound ||
					entry.mapped[binding.RuleFieldName] || seen[binding.RuleFieldName] {
					continue
				}
				seen[binding.RuleFieldName] = true
				comment := CandidateComment(binding)
				if enriches != nil && enriches(item.RuleSnapshot.RuleRevisionID, binding.RuleFieldName) {
					comment += producedHint
				}
				add.Fields = append(add.Fields, FieldToMap{Name: binding.RuleFieldName, Comment: comment})
			}
		}
		if len(add.Fields) > 0 {
			out = append(out, add)
		}
	}
	return out
}

// CandidateComment is the comment written beside a column_mapping key for
// binding: its candidate columns with their reasons, best first, or a request
// to name a column when there are none. It is one line, safe for a terminal.
func CandidateComment(binding api.RuleTemplateFieldBindingSchema) string {
	if len(binding.Candidates) == 0 {
		return "no free type-compatible column; name one"
	}
	parts := make([]string, len(binding.Candidates))
	for i, candidate := range binding.Candidates {
		reason := strings.TrimSuffix(candidate.Reason, " with '"+binding.RuleFieldName+"'")
		parts[i] = candidate.Column
		if reason != "" {
			parts[i] += " (" + reason + ")"
		}
	}
	comment := "choose one: " + strings.Join(parts, ", ")
	if binding.Status == api.Ambiguous {
		comment = "matches several columns; " + comment
	}
	return commentText(comment)
}

// commentText is text with its line breaks turned into spaces and its
// terminal controls escaped.
func commentText(text string) string {
	text = strings.Map(func(r rune) rune {
		switch r {
		case '\r', '\n', '\u0085', '\u2028', '\u2029':
			return ' '
		}
		return r
	}, text)
	return terminal.Sanitize(text)
}

// RenderMappings is the YAML to add to the template for each of adds: per
// entry, its rule, its instance_name when it has one, and under column_mapping
// each field as a key with an empty value and its comment. The text is safe
// for a terminal; it is empty when adds is.
func RenderMappings(adds []MappingsToAdd) string {
	var b bytes.Buffer
	for _, add := range adds {
		fmt.Fprintf(&b, "- rule: %s\n", yamlScalar(add.Rule))
		if add.InstanceName != nil {
			fmt.Fprintf(&b, "  instance_name: %s\n", yamlScalar(*add.InstanceName))
		}
		b.WriteString("  column_mapping:\n")
		for _, field := range add.Fields {
			b.WriteString("    " + fieldLine(field) + "\n")
		}
	}
	return terminal.Sanitize(b.String())
}

// fieldLine is field as a column_mapping key with an empty value and its
// comment.
func fieldLine(field FieldToMap) string {
	return yamlKey(field.Name) + ": # " + commentText(field.Comment)
}

// yamlKey is name as a one-line double-quoted YAML scalar.
func yamlKey(name string) string {
	return encodeScalar(name, yaml.DoubleQuotedStyle)
}

// yamlScalar is value as a one-line YAML string scalar, plain when YAML
// allows it.
func yamlScalar(value string) string {
	if out := encodeScalar(value, 0); !strings.Contains(out, "\n") {
		return out
	}
	return yamlKey(value)
}

// encodeScalar is value encoded as a YAML string scalar in style, without the
// encoder's final newline.
func encodeScalar(value string, style yaml.Style) string {
	out, err := jsonnum.MarshalYAML(&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value, Style: style})
	if err != nil {
		// A string scalar node always encodes; this is unreachable.
		return fmt.Sprintf("%q", value)
	}
	return strings.TrimSuffix(string(out), "\n")
}
