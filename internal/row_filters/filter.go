// Package row_filters defines row filters in manifests.
package row_filters //nolint:revive // Matches the resource domain.

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/pkg/manifest"
	"github.com/qlustered/qctl/internal/pkg/piimask"
	"github.com/qlustered/qctl/internal/terminal"
	"gopkg.in/yaml.v3"
)

// Filter selects rows using conditions and an optional predicate.
type Filter struct {
	Description *string     `json:"description,omitempty" yaml:"description,omitempty"`
	Conditions  []Condition `json:"conditions,omitempty" yaml:"conditions,omitempty"`
	Predicate   *Predicate  `json:"predicate,omitempty" yaml:"predicate,omitempty"`
}

// Condition compares a column with string operands.
type Condition struct {
	ColumnName    string                `json:"column_name" yaml:"column_name"`
	Operator      api.RowFilterOperator `json:"operator" yaml:"operator"`
	Value         *string               `json:"value,omitempty" yaml:"value,omitempty"`
	ValueHigh     *string               `json:"value_high,omitempty" yaml:"value_high,omitempty"`
	ValueList     *[]string             `json:"value_list,omitempty" yaml:"value_list,omitempty"`
	CaseSensitive bool                  `json:"case_sensitive" yaml:"case_sensitive"`
}

// Predicate is a Starlark module defining applies(row).
type Predicate struct {
	Source  string   `json:"source" yaml:"source"`
	Columns []string `json:"columns" yaml:"columns"`
}

// UnmarshalYAML strictly decodes a filter.
func (f *Filter) UnmarshalYAML(node *yaml.Node) error {
	type plain Filter
	return manifest.StrictUnmarshalNode(node, (*plain)(f))
}

// UnmarshalJSON strictly decodes a filter.
func (f *Filter) UnmarshalJSON(data []byte) error {
	type plain Filter
	return manifest.StrictUnmarshal(data, (*plain)(f))
}

// Validate checks the filter's required fields; saved filters need a
// description and may hold no masked text.
func (f *Filter) Validate(saved bool, field string) error {
	if f == nil {
		return nil
	}
	if len(f.Conditions) == 0 && f.Predicate == nil {
		return qerrors.NewBadUsagef("%s needs at least one condition or a predicate", field)
	}
	if saved && f.Description == nil {
		return qerrors.NewBadUsagef("%s needs a description: describe which rows the filter keeps", field)
	}
	if f.Description != nil && strings.TrimSpace(*f.Description) == "" {
		if saved {
			return qerrors.NewBadUsagef("%s has a blank description: describe which rows the filter keeps", field)
		}
		return qerrors.NewBadUsagef("%s has a blank description: describe which rows the filter keeps, or leave description out", field)
	}
	if f.Description != nil && utf8.RuneCountInString(strings.TrimSpace(*f.Description)) > 200 {
		return qerrors.NewBadUsagef("%s has a description over 200 characters", field)
	}
	if !saved {
		return nil
	}
	if paths := piimask.Paths("", f); len(paths) > 0 {
		return qerrors.NewBadUsagef("%s holds masked text at %s: %s stands in for a value the server masks for you, "+
			"not the value itself, and saving it would store that text. Write the real values",
			field, terminal.Sanitize(strings.Join(paths, ", ")), piimask.Mask)
	}
	return nil
}

// FromAPI converts a response filter to its manifest form.
func FromAPI(f *api.RowFilter) *Filter {
	if f == nil {
		return nil
	}
	result := &Filter{}
	if f.Description != nil {
		result.Description = f.Description
	}
	if f.Predicate != nil {
		result.Predicate = &Predicate{Source: f.Predicate.Source, Columns: f.Predicate.Columns}
	}
	if f.Conditions != nil {
		for _, c := range *f.Conditions {
			condition := Condition{ColumnName: c.ColumnName, Operator: c.Operator, Value: c.Value, ValueHigh: c.ValueHigh, ValueList: c.ValueList}
			if c.CaseSensitive != nil {
				condition.CaseSensitive = *c.CaseSensitive
			}
			result.Conditions = append(result.Conditions, condition)
		}
	}
	return result
}

// Saved converts a manifest filter to the instantiate request shape.
func Saved(f *Filter) *api.DescribedRowFilter {
	if f == nil {
		return nil
	}
	return &api.DescribedRowFilter{Description: f.DescriptionText(), Conditions: f.apiConditions(), Predicate: f.apiPredicate()}
}

// Draft converts a manifest filter to the count request shape, without its description.
func Draft(f *Filter) api.RowFilter {
	if f == nil {
		return api.RowFilter{}
	}
	return api.RowFilter{Conditions: f.apiConditions(), Predicate: f.apiPredicate()}
}

func (f *Filter) apiConditions() *[]api.RowFilterCondition {
	if len(f.Conditions) == 0 {
		return nil
	}
	conditions := make([]api.RowFilterCondition, len(f.Conditions))
	for i, c := range f.Conditions {
		conditions[i] = api.RowFilterCondition{ColumnName: c.ColumnName, Operator: c.Operator, Value: c.Value, ValueHigh: c.ValueHigh, ValueList: c.ValueList, CaseSensitive: &c.CaseSensitive}
	}
	return &conditions
}

func (f *Filter) apiPredicate() *api.RowFilterPredicate {
	if f.Predicate == nil {
		return nil
	}
	return &api.RowFilterPredicate{Source: f.Predicate.Source, Columns: f.Predicate.Columns}
}

// HasYAMLField reports whether a mapping contains the path, including null values.
func HasYAMLField(node *yaml.Node, path ...string) bool {
	if len(path) == 0 {
		return true
	}
	child := yamlField(node, path[0])
	return child != nil && HasYAMLField(child, path[1:]...)
}

func yamlField(node *yaml.Node, key string) *yaml.Node {
	if node == nil {
		return nil
	}
	if node.Kind == yaml.AliasNode {
		return yamlField(node.Alias, key)
	}
	if node.Kind == yaml.DocumentNode && len(node.Content) == 1 {
		return yamlField(node.Content[0], key)
	}
	if node.Kind == yaml.SequenceNode {
		for _, item := range node.Content {
			if child := yamlField(item, key); child != nil {
				return child
			}
		}
		return nil
	}
	if node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Tag == "!!merge" {
			return yamlField(node.Content[i+1], key)
		}
	}
	return nil
}

// HasJSONField reports whether an object contains the path, including null values.
func HasJSONField(data []byte, path ...string) bool {
	if len(path) == 0 {
		return true
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(data, &object) != nil {
		return false
	}
	child, ok := object[path[0]]
	return ok && HasJSONField(child, path[1:]...)
}

// SameRows reports whether two filters have the same conditions and predicate.
func SameRows(left, right *Filter) bool {
	material := func(f *Filter) []byte {
		if f == nil {
			return []byte("null")
		}
		selection := *f
		selection.Description = nil
		data, _ := json.Marshal(selection)
		return data
	}
	return string(material(left)) == string(material(right))
}

// DescriptionText returns the description, or an empty string when absent.
func (f *Filter) DescriptionText() string {
	if f == nil || f.Description == nil {
		return ""
	}
	return *f.Description
}
