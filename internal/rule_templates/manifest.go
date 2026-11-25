// Package rule_templates parses and validates TableRuleTemplate manifests and
// builds the rule template a rule merge preview sends.
package rule_templates //nolint:revive // Public package name matches the established resource domain.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/dataset_rules"
	qerrors "github.com/qlustered/qctl/internal/errors"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
	"github.com/qlustered/qctl/internal/pkg/piimask"
	"github.com/qlustered/qctl/internal/row_filters"
	"github.com/qlustered/qctl/internal/rule_merges"
	"github.com/qlustered/qctl/internal/terminal"
	"gopkg.in/yaml.v3"
)

// Kind is the manifest kind Parse accepts.
const Kind = "TableRuleTemplate"

// nullTag is the short tag of a YAML null.
const nullTag = "!!null"

// maxNameLength is the longest metadata.name or instance_name the server
// takes, in characters.
const maxNameLength = 200

// TableRuleTemplate is one TableRuleTemplate document.
type TableRuleTemplate struct {
	APIVersion string   `yaml:"apiVersion" json:"apiVersion"`
	Kind       string   `yaml:"kind" json:"kind"`
	Metadata   Metadata `yaml:"metadata" json:"metadata"`
	Spec       Spec     `yaml:"spec" json:"spec"`
}

// Metadata is a TableRuleTemplate's metadata block.
type Metadata struct {
	Name string `yaml:"name" json:"name"`
}

// Spec is a TableRuleTemplate's spec block.
type Spec struct {
	Rules []Entry `yaml:"rules" json:"rules"`
}

// Entry is one rule of a template. A nil optional field is left out of the
// request. RowFilterSet records a row_filter key, so an explicit null is told
// from no key.
type Entry struct {
	Rule           string                  `yaml:"rule" json:"rule"`
	Release        string                  `yaml:"release" json:"release"`
	InstanceName   *string                 `yaml:"instance_name,omitempty" json:"instance_name,omitempty"`
	Severity       *string                 `yaml:"severity,omitempty" json:"severity,omitempty"`
	Params         *map[string]interface{} `yaml:"params,omitempty" json:"params,omitempty"`
	RowFilter      *row_filters.Filter     `yaml:"row_filter,omitempty" json:"row_filter,omitempty"`
	ColumnMapping  *map[string]string      `yaml:"column_mapping,omitempty" json:"column_mapping,omitempty"`
	ProducedFields *[]ProducedField        `yaml:"produced_fields,omitempty" json:"produced_fields,omitempty"`
	RowFilterSet   bool                    `yaml:"-" json:"-"`
}

// ProducedField is one entry of an Entry's produced_fields.
type ProducedField struct {
	RuleFieldName    string  `yaml:"rule_field_name" json:"rule_field_name"`
	DatasetFieldName *string `yaml:"dataset_field_name,omitempty" json:"dataset_field_name,omitempty"`
}

// UnmarshalYAML strictly decodes an entry and records whether it has a
// row_filter key, null included.
func (e *Entry) UnmarshalYAML(node *yaml.Node) error {
	type plain Entry
	if err := pkgmanifest.StrictUnmarshalNode(node, (*plain)(e)); err != nil {
		return err
	}
	e.RowFilterSet = row_filters.HasYAMLField(node, "row_filter")
	return nil
}

// UnmarshalJSON strictly decodes an entry and records whether it has a
// row_filter key, null included.
func (e *Entry) UnmarshalJSON(data []byte) error {
	type plain Entry
	if err := pkgmanifest.StrictUnmarshal(data, (*plain)(e)); err != nil {
		return err
	}
	e.RowFilterSet = row_filters.HasJSONField(data, "row_filter")
	return nil
}

// Key is the entry's key in its template: its instance_name when set, else
// its rule. It is what a merge item's template_key holds.
func (e Entry) Key() string {
	if e.InstanceName != nil {
		return *e.InstanceName
	}
	return e.Rule
}

// File is a parsed TableRuleTemplate file.
type File struct {
	Template TableRuleTemplate
	// Source names the file in error messages.
	Source string
	// Node is the file's document node as yaml.v3 parsed it, comments
	// included; nil for a JSON file yaml.v3 cannot read.
	Node *yaml.Node
}

// Parse strict-parses data, YAML or JSON, as exactly one TableRuleTemplate
// document, checks its apiVersion and kind, and validates it. source names
// the file in error messages. Every failure exits 2.
func Parse(data []byte, source string) (*File, error) {
	docs, err := pkgmanifest.SplitDocuments(data)
	if err != nil {
		return nil, qerrors.Wrap(qerrors.ExitBadUsage, err, fmt.Sprintf("failed to parse %s manifest %s", Kind, source))
	}
	if len(docs) != 1 {
		return nil, qerrors.NewBadUsagef("%s: expected one %s document, found %d", source, Kind, len(docs))
	}
	var template TableRuleTemplate
	if err := pkgmanifest.StrictUnmarshal(docs[0], &template); err != nil {
		return nil, qerrors.Wrap(qerrors.ExitBadUsage, err, fmt.Sprintf("failed to parse %s manifest %s", Kind, source))
	}
	if err := dataset_rules.ValidateManifestAPIVersion(template.APIVersion, source); err != nil {
		return nil, err
	}
	if template.Kind != Kind {
		return nil, qerrors.NewBadUsagef("kind %q in %s is not %s", template.Kind, source, Kind)
	}
	if err := template.Validate(source); err != nil {
		return nil, err
	}
	node, _ := documentNode(data)
	return &File{Template: template, Source: source, Node: node}, nil
}

// documentNode returns the first non-empty document node of data.
func documentNode(data []byte) (*yaml.Node, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	for {
		var node yaml.Node
		err := decoder.Decode(&node)
		if errors.Is(err, io.EOF) {
			return nil, errors.New("no document")
		}
		if err != nil {
			return nil, err
		}
		if len(node.Content) == 1 && node.Content[0].ShortTag() != nullTag {
			return &node, nil
		}
	}
}

// EntryNode returns the mapping node of spec.rules[i] in f.Node, or nil when
// there is none.
func (f *File) EntryNode(i int) *yaml.Node {
	rules := mappingValue(mappingValue(f.Node, "spec"), "rules")
	if rules == nil || rules.Kind != yaml.SequenceNode || i < 0 || i >= len(rules.Content) {
		return nil
	}
	return resolveAlias(rules.Content[i])
}

// mappingValue returns the value node of key in the mapping node, which may
// be a document node wrapping one, or nil.
func mappingValue(node *yaml.Node, key string) *yaml.Node {
	node = resolveAlias(node)
	if node != nil && node.Kind == yaml.DocumentNode && len(node.Content) == 1 {
		node = resolveAlias(node.Content[0])
	}
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return resolveAlias(node.Content[i+1])
		}
	}
	return nil
}

// resolveAlias follows alias nodes to the node they name.
func resolveAlias(node *yaml.Node) *yaml.Node {
	for node != nil && node.Kind == yaml.AliasNode {
		node = node.Alias
	}
	return node
}

// Validate makes every check that needs no server: a metadata.name, at least
// one entry, each with a rule and a release, a non-blank instance_name when
// set, a severity from the generated RuleSeverity enum, params that encode to
// JSON and hold no masked text, a row filter as a saved one, no empty
// column_mapping value, a rule_field_name on every produced field, an
// instance_name on every entry of a rule listed twice, and unique entry keys.
// source names the file in error messages. Every failure exits 2.
func (t *TableRuleTemplate) Validate(source string) error {
	name := strings.TrimSpace(t.Metadata.Name)
	if name == "" {
		return qerrors.NewBadUsagef("metadata.name is required in %s: name the template", source)
	}
	if utf8.RuneCountInString(name) > maxNameLength {
		return qerrors.NewBadUsagef("metadata.name in %s is over %d characters", source, maxNameLength)
	}
	if len(t.Spec.Rules) == 0 {
		return qerrors.NewBadUsagef("spec.rules is empty in %s: list at least one rule", source)
	}
	for i, entry := range t.Spec.Rules {
		if err := entry.validate(i, source); err != nil {
			return err
		}
	}
	if err := t.checkRepeatedFamilies(source); err != nil {
		return err
	}
	return t.checkUniqueKeys(source)
}

// validate checks one entry; i is its index in spec.rules.
func (e Entry) validate(i int, source string) error {
	if e.Rule == "" {
		return qerrors.NewBadUsagef("spec.rules[%d].rule is required in %s: name the rule family", i, source)
	}
	if e.Release == "" {
		return qerrors.NewBadUsagef("spec.rules[%d] (rule %q) in %s: release is required: name a published release of the rule",
			i, e.Rule, source)
	}
	if e.InstanceName != nil {
		instanceName := strings.TrimSpace(*e.InstanceName)
		if instanceName == "" {
			return qerrors.NewBadUsagef("spec.rules[%d] (rule %q) in %s: instance_name is blank: name the rule on the table, or leave instance_name out",
				i, e.Rule, source)
		}
		if utf8.RuneCountInString(instanceName) > maxNameLength {
			return qerrors.NewBadUsagef("spec.rules[%d] (rule %q) in %s: instance_name is over %d characters",
				i, e.Rule, source, maxNameLength)
		}
	}
	label := fmt.Sprintf("spec.rules[%d] (%q) in %s", i, e.Key(), source)
	if severities := api.EnumValues[api.RuleSeverity](); e.Severity != nil && !slices.Contains(severities, *e.Severity) {
		return qerrors.NewBadUsagef("%s: severity %q is not one of: %s", label, *e.Severity, strings.Join(severities, ", "))
	}
	if err := e.validateParams(i, label); err != nil {
		return err
	}
	if err := e.RowFilter.Validate(true, fmt.Sprintf("spec.rules[%d].row_filter in %s", i, source)); err != nil {
		return err
	}
	return e.validateFields(label)
}

// validateParams refuses params that do not encode to JSON or hold masked
// text; label names the entry.
func (e Entry) validateParams(i int, label string) error {
	if e.Params == nil {
		return nil
	}
	if _, err := json.Marshal(*e.Params); err != nil {
		return qerrors.Wrap(qerrors.ExitBadUsage, err, label+": params cannot be sent as JSON")
	}
	if paths := piimask.Paths(fmt.Sprintf("spec.rules[%d].params", i), *e.Params); len(paths) > 0 {
		return qerrors.NewBadUsagef("%s holds masked text at %s: %s stands in for a value the server masks for you, "+
			"not the value itself, and sending it would store that text. Write the real values",
			label, terminal.Sanitize(strings.Join(paths, ", ")), piimask.Mask)
	}
	return nil
}

// validateFields refuses an empty column_mapping value and a produced field
// without a rule_field_name; label names the entry.
func (e Entry) validateFields(label string) error {
	if e.ColumnMapping != nil {
		for _, field := range slices.Sorted(maps.Keys(*e.ColumnMapping)) {
			if strings.TrimSpace((*e.ColumnMapping)[field]) == "" {
				return qerrors.NewBadUsagef("%s: column_mapping[%q] is empty: fill in the table column the rule field %q binds to",
					label, field, field)
			}
		}
	}
	if e.ProducedFields != nil {
		for k, produced := range *e.ProducedFields {
			if produced.RuleFieldName == "" {
				return qerrors.NewBadUsagef("%s: produced_fields[%d].rule_field_name is required", label, k)
			}
		}
	}
	return nil
}

// checkRepeatedFamilies refuses a rule listed more than once with an entry
// that has no instance_name.
func (t *TableRuleTemplate) checkRepeatedFamilies(source string) error {
	indexes := map[string][]int{}
	var order []string
	for i, entry := range t.Spec.Rules {
		if _, seen := indexes[entry.Rule]; !seen {
			order = append(order, entry.Rule)
		}
		indexes[entry.Rule] = append(indexes[entry.Rule], i)
	}
	for _, rule := range order {
		entries := indexes[rule]
		if len(entries) < 2 {
			continue
		}
		var unnamed []string
		for _, i := range entries {
			if t.Spec.Rules[i].InstanceName == nil {
				unnamed = append(unnamed, fmt.Sprintf("spec.rules[%d]", i))
			}
		}
		if len(unnamed) > 0 {
			verb := "has"
			if len(unnamed) > 1 {
				verb = "have"
			}
			return qerrors.NewBadUsagef("rule %q is listed %d times in %s, so every one of its entries needs an instance_name; %s %s none",
				rule, len(entries), source, strings.Join(unnamed, ", "), verb)
		}
	}
	return nil
}

// checkUniqueKeys refuses two entries with the same key.
func (t *TableRuleTemplate) checkUniqueKeys(source string) error {
	first := map[string]int{}
	for i, entry := range t.Spec.Rules {
		key := entry.Key()
		if j, ok := first[key]; ok {
			return qerrors.NewBadUsagef("spec.rules[%d] and spec.rules[%d] in %s have the same key %q: "+
				"an entry's key is its instance_name, else its rule, and keys must be unique", j, i, source, key)
		}
		first[key] = i
	}
	return nil
}

// ToRequest builds the template PreviewTemplate sends from t as written,
// without validating it. params and column_mapping are copied one level
// deep.
func (t *TableRuleTemplate) ToRequest() rule_merges.Template {
	out := rule_merges.Template{Name: t.Metadata.Name, Entries: make([]rule_merges.TemplateEntry, len(t.Spec.Rules))}
	for i, e := range t.Spec.Rules {
		entry := api.RuleTemplateEntrySchema{Rule: e.Rule, Release: e.Release}
		if e.InstanceName != nil {
			instanceName := *e.InstanceName
			entry.InstanceName = &instanceName
		}
		if e.Severity != nil {
			severity := api.RuleSeverity(*e.Severity)
			entry.Severity = &severity
		}
		if e.Params != nil {
			params := maps.Clone(*e.Params)
			if params == nil {
				params = map[string]interface{}{}
			}
			entry.Params = &params
		}
		if e.ColumnMapping != nil {
			mapping := maps.Clone(*e.ColumnMapping)
			if mapping == nil {
				mapping = map[string]string{}
			}
			entry.ColumnMapping = &mapping
		}
		if e.ProducedFields != nil {
			produced := make([]api.RuleTemplateProducedFieldSchema, len(*e.ProducedFields))
			for k, field := range *e.ProducedFields {
				produced[k] = api.RuleTemplateProducedFieldSchema{RuleFieldName: field.RuleFieldName}
				if field.DatasetFieldName != nil {
					datasetFieldName := *field.DatasetFieldName
					produced[k].DatasetFieldName = &datasetFieldName
				}
			}
			entry.ProducedFields = &produced
		}
		rowFilterSet := e.RowFilterSet || e.RowFilter != nil
		if rowFilterSet {
			entry.RowFilter = row_filters.Saved(e.RowFilter)
		}
		out.Entries[i] = rule_merges.TemplateEntry{RuleTemplateEntrySchema: entry, RowFilterSet: rowFilterSet}
	}
	return out
}

// Keys returns each entry's Key, in spec.rules order.
func (t *TableRuleTemplate) Keys() []string {
	keys := make([]string, len(t.Spec.Rules))
	for i, entry := range t.Spec.Rules {
		keys[i] = entry.Key()
	}
	return keys
}
