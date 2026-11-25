package rule_templates //nolint:revive // Public package name matches the established resource domain.

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/pkg/privatefile"
	"gopkg.in/yaml.v3"
)

const columnMappingKey = "column_mapping"

// defaultIndent is the indentation step used when the file shows none.
const defaultIndent = 2

// WriteMappings adds to the TableRuleTemplate file at path the column_mapping
// keys Mappings finds for items and enriches, each with an empty value and its
// comment, and returns what it added. It inserts lines, and removes an empty
// column_mapping value written as ~, null or {} with the spaces before it.
// The file keeps its mode and is replaced atomically, and is not written when
// there is nothing to add. Stdin, a file that is not one block-style YAML
// document, an entry it cannot add to in place, and a result that would read
// back as anything but the keys added exit 2, with nothing written.
func WriteMappings(path string, items []api.RuleMergeItemSchema, enriches Enriches) ([]MappingsToAdd, error) {
	if path == "" || path == "-" {
		return nil, stdinRefusal()
	}
	target, err := privatefile.ResolveTarget(path, "template")
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(target)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", path, err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", path, err)
	}
	updated, adds, err := insertMappings(data, path, items, enriches)
	if err != nil || len(adds) == 0 {
		return nil, err
	}
	if err := writeKeepingMode(target, updated, info.Mode().Perm()); err != nil {
		return nil, err
	}
	return adds, nil
}

// insertMappings returns data with the column_mapping keys for items and
// enriches added, and what it added; source names the file in error messages.
func insertMappings(data []byte, source string, items []api.RuleMergeItemSchema, enriches Enriches) ([]byte, []MappingsToAdd, error) {
	doc, err := singleBlockDocument(data, source)
	if err != nil {
		return nil, nil, err
	}
	rules := mappingValue(doc, "spec")
	rules = mappingValue(rules, "rules")
	if rules == nil || rules.Kind != yaml.SequenceNode {
		return nil, nil, qerrors.NewBadUsagef("%s has no spec.rules list to add column_mapping keys to", source)
	}
	entries := make([]entryMapping, len(rules.Content))
	for i, node := range rules.Content {
		entries[i] = nodeEntryMapping(resolveAlias(node))
	}
	adds := mappingsFor(entries, items, enriches)
	if len(adds) == 0 {
		return data, nil, nil
	}

	text := newLines(data)
	width := indentWidth(doc)
	order := preorder(doc)
	for _, add := range adds {
		if err := text.addEntry(rules.Content[add.Index], add, width, order, source); err != nil {
			return nil, nil, err
		}
	}
	updated := text.bytes()
	if err := checkOnlyKeysAdded(data, updated, adds); err != nil {
		return nil, nil, qerrors.Wrap(qerrors.ExitBadUsage, fmt.Errorf(
			"could not add the column_mapping keys to %s without changing its other content (%w); "+
				"add these lines to it by hand:\n%s", source, err, RenderMappings(adds)), "")
	}
	return updated, adds, nil
}

// singleBlockDocument returns the document node of data, refusing (exit 2)
// anything but one YAML document whose top level is a block mapping.
func singleBlockDocument(data []byte, source string) (*yaml.Node, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var docs []*yaml.Node
	for {
		var node yaml.Node
		err := decoder.Decode(&node)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, qerrors.Wrap(qerrors.ExitBadUsage, err, fmt.Sprintf("failed to parse %s", source))
		}
		docs = append(docs, &node)
	}
	if len(docs) != 1 {
		return nil, qerrors.NewBadUsagef("%s holds %d YAML documents: column_mapping keys are only written into a file "+
			"holding one TableRuleTemplate document", source, len(docs))
	}
	doc := docs[0]
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, qerrors.NewBadUsagef("%s is not a %s document", source, Kind)
	}
	if doc.Content[0].Style&yaml.FlowStyle != 0 {
		return nil, flowStyleRefusal(source)
	}
	return doc, nil
}

// CheckMappingsWritable refuses, exit 2, a template file WriteMappings refuses
// whatever it is asked to add: stdin, and a file whose document is not a
// block-style YAML mapping, JSON included.
func (f *File) CheckMappingsWritable() error {
	if f.Source == "" || f.Source == "-" {
		return stdinRefusal()
	}
	if f.Node == nil || len(f.Node.Content) != 1 || f.Node.Content[0].Style&yaml.FlowStyle != 0 {
		return flowStyleRefusal(f.Source)
	}
	return nil
}

func stdinRefusal() error {
	return qerrors.NewBadUsage("column_mapping keys are written into the template file: name the file, not stdin")
}

func flowStyleRefusal(source string) error {
	return qerrors.NewBadUsagef("%s is JSON or flow-style YAML, which column_mapping keys are not written into: "+
		"add them by hand", source)
}

// nodeEntryMapping reads an entry's rule, instance_name and column_mapping
// keys from its node.
func nodeEntryMapping(node *yaml.Node) entryMapping {
	entry := entryMapping{mapped: map[string]bool{}}
	if rule := mappingValue(node, "rule"); rule != nil && rule.Kind == yaml.ScalarNode {
		entry.rule = rule.Value
	}
	if name := mappingValue(node, "instance_name"); name != nil && name.Kind == yaml.ScalarNode && name.ShortTag() != nullTag {
		value := name.Value
		entry.instanceName = &value
	}
	if mapping := mappingValue(node, columnMappingKey); mapping != nil && mapping.Kind == yaml.MappingNode {
		for i := 0; i < len(mapping.Content); i += 2 {
			entry.mapped[mapping.Content[i].Value] = true
		}
	}
	return entry
}

// mappingPair returns the key and value nodes of key in the mapping node, or
// nils.
func mappingPair(node *yaml.Node, key string) (*yaml.Node, *yaml.Node) {
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i], node.Content[i+1]
		}
	}
	return nil, nil
}

// indentWidth is the indentation step of the first block mapping nested in
// another in doc, or defaultIndent.
func indentWidth(doc *yaml.Node) int {
	for _, node := range preorder(doc) {
		if node.Kind != yaml.MappingNode || node.Style&yaml.FlowStyle != 0 {
			continue
		}
		for i := 0; i+1 < len(node.Content); i += 2 {
			key, value := node.Content[i], node.Content[i+1]
			if value.Kind == yaml.MappingNode && value.Style&yaml.FlowStyle == 0 && len(value.Content) > 0 &&
				value.Line > key.Line && value.Content[0].Column > key.Column {
				return value.Content[0].Column - key.Column
			}
		}
	}
	return defaultIndent
}

// preorder lists node and its descendants in document order, not following
// aliases.
func preorder(node *yaml.Node) []*yaml.Node {
	out := []*yaml.Node{node}
	for _, child := range node.Content {
		out = append(out, preorder(child)...)
	}
	return out
}

// lines is a file's text as lines, without their line feeds.
type lines struct {
	text            []string
	crlf            bool
	trailingNewline bool
	// after holds the lines to insert after each line number, 1-based.
	after map[int][]string
}

// newLines splits data into lines.
func newLines(data []byte) *lines {
	text := strings.Split(string(data), "\n")
	l := &lines{after: map[int][]string{}}
	if len(text) > 1 && text[len(text)-1] == "" {
		l.trailingNewline = true
		text = text[:len(text)-1]
	}
	l.text = text
	l.crlf = len(text) > 1 && strings.HasSuffix(text[0], "\r")
	return l
}

// insert queues added after line n, 1-based; 0 inserts before the first.
func (l *lines) insert(n int, added ...string) {
	for _, line := range added {
		if l.crlf {
			line += "\r"
		}
		l.after[n] = append(l.after[n], line)
	}
}

// bytes joins the lines and the queued insertions.
func (l *lines) bytes() []byte {
	var out []string
	out = append(out, l.after[0]...)
	for i, line := range l.text {
		out = append(out, line)
		out = append(out, l.after[i+1]...)
	}
	joined := strings.Join(out, "\n")
	if l.trailingNewline {
		joined += "\n"
	}
	return []byte(joined)
}

// addEntry queues the lines that add add's fields to the entry node; width
// is the file's indentation step and order the document's nodes in order.
func (l *lines) addEntry(node *yaml.Node, add MappingsToAdd, width int, order []*yaml.Node, source string) error {
	label := fmt.Sprintf("spec.rules[%d] in %s", add.Index, source)
	if node.Kind != yaml.MappingNode || node.Style&yaml.FlowStyle != 0 || len(node.Content) == 0 {
		return qerrors.NewBadUsagef("%s is not a block mapping, which column_mapping keys are not written into: "+
			"add them by hand", label)
	}
	fieldLines := func(indent int) []string {
		out := make([]string, len(add.Fields))
		for i, field := range add.Fields {
			out[i] = strings.Repeat(" ", indent) + fieldLine(field)
		}
		return out
	}
	key, mapping := mappingPair(node, columnMappingKey)
	switch {
	case key == nil:
		indent := node.Content[0].Column - 1
		l.insert(l.endLine(node, order), append([]string{strings.Repeat(" ", indent) + columnMappingKey + ":"},
			fieldLines(indent+width)...)...)
	case mapping.Kind == yaml.MappingNode && mapping.Style&yaml.FlowStyle == 0 && len(mapping.Content) > 0:
		l.insert(l.endLine(mapping, order), fieldLines(mapping.Content[0].Column-1)...)
	case isEmptyValue(mapping):
		if err := l.removeEmptyValue(mapping, label); err != nil {
			return err
		}
		l.insert(key.Line, fieldLines(key.Column-1+width)...)
	default:
		return qerrors.NewBadUsagef("%s: column_mapping is not a block mapping, which keys are not written into: "+
			"add them by hand", label)
	}
	return nil
}

// isEmptyValue reports whether node is a null scalar or an empty flow
// mapping, with no comment of its own.
func isEmptyValue(node *yaml.Node) bool {
	if node.HeadComment != "" || node.FootComment != "" || node.Anchor != "" {
		return false
	}
	if node.Kind == yaml.ScalarNode && node.ShortTag() == nullTag {
		return true
	}
	return node.Kind == yaml.MappingNode && node.Style&yaml.FlowStyle != 0 && len(node.Content) == 0
}

// removeEmptyValue deletes the text of an empty value isEmptyValue accepts
// from its line; label names the entry.
func (l *lines) removeEmptyValue(node *yaml.Node, label string) error {
	if node.Kind == yaml.ScalarNode && node.Value == "" {
		return nil
	}
	refuse := qerrors.NewBadUsagef("%s: column_mapping's empty value could not be replaced in place: "+
		"delete it and run again", label)
	if node.Line < 1 || node.Line > len(l.text) {
		return refuse
	}
	line := []rune(l.text[node.Line-1])
	start := node.Column - 1
	if start < 0 || start > len(line) {
		return refuse
	}
	var end int
	if node.Kind == yaml.ScalarNode {
		end = start + len([]rune(node.Value))
		if end > len(line) || string(line[start:end]) != node.Value {
			return refuse
		}
	} else {
		segment := string(line[start:])
		closing := strings.IndexRune(segment, '}')
		if !strings.HasPrefix(segment, "{") || closing < 0 || strings.TrimSpace(segment[1:closing]) != "" {
			return refuse
		}
		end = start + utf8.RuneCountInString(segment[:closing+1])
	}
	rest := string(line[end:])
	if strings.TrimSpace(rest) == "" {
		rest = strings.TrimLeft(rest, " \t")
	}
	l.text[node.Line-1] = strings.TrimRight(string(line[:start]), " ") + rest
	return nil
}

// endLine is the last line of node's content: the line before the next node
// in order, less the blank and comment lines that lead into it.
func (l *lines) endLine(node *yaml.Node, order []*yaml.Node) int {
	sub := preorder(node)
	last := node.Line
	for _, n := range sub {
		last = max(last, n.Line)
	}
	next := len(l.text) + 1
	for i, n := range order {
		if n == sub[len(sub)-1] {
			for _, following := range order[i+1:] {
				if following.Line > 0 {
					next = following.Line
					break
				}
			}
			break
		}
	}
	end := next - 1
	for end > last {
		trimmed := strings.TrimSpace(l.text[end-1])
		if trimmed != "" && trimmed != "-" && !strings.HasPrefix(trimmed, "#") {
			break
		}
		end--
	}
	return end
}

// checkOnlyKeysAdded checks that updated decodes to original with each of
// adds' fields added as an empty column_mapping value.
func checkOnlyKeysAdded(original, updated []byte, adds []MappingsToAdd) error {
	var want, got any
	if err := yaml.Unmarshal(original, &want); err != nil {
		return err
	}
	if err := yaml.Unmarshal(updated, &got); err != nil {
		return err
	}
	if _, err := singleBlockDocument(updated, "the result"); err != nil {
		return err
	}
	root, _ := want.(map[string]any)
	spec, _ := root["spec"].(map[string]any)
	rules, _ := spec["rules"].([]any)
	for _, add := range adds {
		if add.Index >= len(rules) {
			return errors.New("an entry moved")
		}
		entry, _ := rules[add.Index].(map[string]any)
		if entry == nil {
			return errors.New("an entry is not a mapping")
		}
		mapping, _ := entry[columnMappingKey].(map[string]any)
		if mapping == nil {
			mapping = map[string]any{}
			entry[columnMappingKey] = mapping
		}
		for _, field := range add.Fields {
			mapping[field.Name] = nil
		}
	}
	if !reflect.DeepEqual(want, got) {
		return errors.New("the result does not read back as the template with only those keys added")
	}
	return nil
}

// writeKeepingMode replaces target with data atomically, through a temporary
// file in its directory, and gives it mode perm.
func writeKeepingMode(target string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+"-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to write %s: %w", target, err)
	}
	tmpName := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}()
	if err := tmp.Chmod(perm); err != nil {
		return fmt.Errorf("failed to write %s: %w", target, err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("failed to write %s: %w", target, err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("failed to write %s: %w", target, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to write %s: %w", target, err)
	}
	if err := os.Rename(tmpName, target); err != nil {
		return fmt.Errorf("failed to write %s: %w", target, err)
	}
	return nil
}
