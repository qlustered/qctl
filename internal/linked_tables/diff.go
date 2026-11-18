package linked_tables //nolint:revive // Public package name matches the established resource domain.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strings"
)

// Change is one manifest field whose value after an apply would differ from
// the live one. A nil Old or New is absent.
type Change struct {
	Path string
	Old  json.RawMessage
	New  json.RawMessage
}

// stateEnabled is the state of an enabled revision and the desired_state
// that asks for one.
const stateEnabled = "enabled"

// codeDeletedFieldPruned is the issue code for an output column the server
// drops from the spec because the field it reads is deleted.
const codeDeletedFieldPruned = "deleted_field_pruned"

// PrunedOutputColumns returns the output columns that issues report as
// deleted_field_pruned.
func PrunedOutputColumns(issues []Issue) map[string]bool {
	pruned := map[string]bool{}
	for _, issue := range issues {
		if issue.Code == nil || *issue.Code != codeDeletedFieldPruned {
			continue
		}
		loc := issue.Loc
		if len(loc) == 4 && loc[0] == "revision" && loc[1] == "output" && loc[3] == "field" {
			pruned[loc[2]] = true
		}
	}
	return pruned
}

// CreateChanges lists every spec field m sets as an addition, leaving out
// spec.dry_run and the output columns in pruned.
func CreateChanges(m *LinkedTableManifest, pruned map[string]bool) ([]Change, error) {
	spec := m.Spec
	wanted, err := manifestSources(spec.Sources, nil)
	if err != nil {
		return nil, err
	}

	var c changeList
	c.add("spec.destination", spec.Destination)
	if spec.BackupSettings != nil {
		c.add("spec.backup_settings", *spec.BackupSettings)
	}
	c.add("spec.table.database", spec.Table.Database)
	if spec.Table.Schema != nil {
		c.add("spec.table.schema", *spec.Table.Schema)
	}
	c.add("spec.table.name", spec.Table.Name)
	c.add("spec.spine.source", spec.Spine.Source)
	c.add("spec.spine.natural_key_columns", spec.Spine.NaturalKeyColumns)
	if len(spec.Spine.NaturalKeyNormalization) > 0 {
		c.add("spec.spine.natural_key_normalization", spec.Spine.NaturalKeyNormalization)
	}
	if spec.Spine.IdentityHashVersion != nil {
		c.add("spec.spine.identity_hash_version", *spec.Spine.IdentityHashVersion)
	}
	c.sources(nil, wanted)
	c.output(nil, manifestOutput(spec.Output, pruned))
	if spec.DesiredState != nil {
		c.add("spec.desired_state", *spec.DesiredState)
	}
	if spec.Release != nil {
		c.add("spec.release", *spec.Release)
	}
	if spec.Description != nil {
		c.add("spec.description", *spec.Description)
	}
	return c.changes, nil
}

// UpdateChanges compares m with live, the spec of a revision in state
// liveState: sources and output as the server would store them, the
// description, and, when the two specs match, whether the revision must still
// be enabled. datasetIDs maps each source dataset name m names to its id;
// datasetNames names live source dataset ids, and a live id it lacks shows as
// the id. The output columns in pruned are left out of m.
func UpdateChanges(m *LinkedTableManifest, live *RevisionSpec, liveState string, datasetIDs map[string]int, datasetNames map[int]string, pruned map[string]bool) ([]Change, error) {
	wanted, err := manifestSources(m.Spec.Sources, datasetIDs)
	if err != nil {
		return nil, err
	}
	have, err := liveSources(live.Sources, datasetNames)
	if err != nil {
		return nil, err
	}

	var c changeList
	c.sources(have, wanted)
	c.output(liveOutput(live.Output), manifestOutput(m.Spec.Output, pruned))
	specChanged := len(c.changes) > 0

	liveDescription := optionalJSON(live.Description)
	switch {
	case m.Spec.Description != nil:
		c.compare("spec.description", liveDescription, plainJSON(*m.Spec.Description))
	case specChanged:
		c.compare("spec.description", liveDescription, nil)
	}

	desired := stateEnabled
	if m.Spec.DesiredState != nil {
		desired = *m.Spec.DesiredState
	}
	if !specChanged && desired == stateEnabled && liveState != stateEnabled {
		c.compare("spec.desired_state", plainJSON(liveState), plainJSON(desired))
	}
	return c.changes, nil
}

// changeList accumulates changes in the order they are found.
type changeList struct {
	changes []Change
}

// add records value as an addition.
func (c *changeList) add(path string, value any) {
	c.changes = append(c.changes, Change{Path: path, New: plainJSON(value)})
}

// compare records a change when before and after differ.
func (c *changeList) compare(path string, before, after json.RawMessage) {
	if bytes.Equal(before, after) && (before == nil) == (after == nil) {
		return
	}
	c.changes = append(c.changes, Change{Path: path, Old: before, New: after})
}

// sources compares two alias-keyed source sets; a nil set is absent.
func (c *changeList) sources(live, wanted map[string]sourceView) {
	for _, alias := range unionKeys(live, wanted) {
		prefix := "spec.sources." + alias
		l, inLive := live[alias]
		w, inWanted := wanted[alias]
		switch {
		case !inLive:
			c.compare(prefix+".dataset", nil, w.dataset)
		case !inWanted:
			c.compare(prefix+".dataset", l.dataset, nil)
		case l.datasetID != w.datasetID:
			c.compare(prefix+".dataset", l.dataset, w.dataset)
		}
		c.compare(prefix+".arity", l.arity, w.arity)
		for _, column := range unionKeys(l.joins, w.joins) {
			c.compare(prefix+".join_fields."+column, l.joins[column], w.joins[column])
		}
	}
}

// output compares two column-keyed output sets; a nil set is absent.
func (c *changeList) output(live, wanted map[string]json.RawMessage) {
	for _, column := range unionKeys(live, wanted) {
		c.compare("spec.output."+column, live[column], wanted[column])
	}
}

// sourceView is one source as the comparison reads it: datasetID decides
// whether the dataset changed and dataset is how it prints.
type sourceView struct {
	datasetID int
	dataset   json.RawMessage
	arity     json.RawMessage
	joins     map[string]json.RawMessage
}

func manifestSources(sources map[string]SourceSpec, datasetIDs map[string]int) (map[string]sourceView, error) {
	views := make(map[string]sourceView, len(sources))
	for alias, src := range sources {
		view := sourceView{
			datasetID: datasetIDs[src.Dataset],
			dataset:   plainJSON(src.Dataset),
			joins:     make(map[string]json.RawMessage, len(src.JoinFields)),
		}
		if src.Arity != nil {
			view.arity = plainJSON(*src.Arity)
		}
		for column, value := range src.JoinFields {
			canonical, err := value.canonicalJSON()
			if err != nil {
				return nil, fmt.Errorf("spec.sources.%s.join_fields.%s: %w", alias, column, err)
			}
			view.joins[column] = canonical
		}
		views[alias] = view
	}
	return views, nil
}

func liveSources(sources map[string]RevisionSource, datasetNames map[int]string) (map[string]sourceView, error) {
	views := make(map[string]sourceView, len(sources))
	for alias, src := range sources {
		view := sourceView{
			datasetID: src.DatasetID,
			dataset:   plainJSON(src.DatasetID),
			arity:     optionalJSON(src.Arity),
			joins:     make(map[string]json.RawMessage, len(src.JoinFields)),
		}
		if name, ok := datasetNames[src.DatasetID]; ok {
			view.dataset = plainJSON(name)
		}
		for column, raw := range src.JoinFields {
			canonical, err := canonicalJoin(raw)
			if err != nil {
				return nil, fmt.Errorf("live spec sources.%s.join_fields.%s: %w", alias, column, err)
			}
			view.joins[column] = canonical
		}
		views[alias] = view
	}
	return views, nil
}

func manifestOutput(output map[string]OutputMapping, pruned map[string]bool) map[string]json.RawMessage {
	views := make(map[string]json.RawMessage, len(output))
	for column, mapping := range output {
		if !pruned[column] {
			views[column] = plainJSON(mapping)
		}
	}
	return views
}

func liveOutput(output map[string]OutputMapping) map[string]json.RawMessage {
	return manifestOutput(output, nil)
}

// canonicalJoinClause is a join clause in the form the server persists it:
// spine_fields always present, the optional keys only when set.
type canonicalJoinClause struct {
	SpineFields  []string    `json:"spine_fields"`
	SourceFields []string    `json:"source_fields"`
	SpineExpr    *string     `json:"spine_expr,omitempty"`
	SourceExpr   *string     `json:"source_expr,omitempty"`
	Constant     interface{} `json:"constant,omitempty"`
}

func (clause canonicalJoinClause) marshal() (json.RawMessage, error) {
	if clause.SpineFields == nil {
		clause.SpineFields = []string{}
	}
	return json.Marshal(clause)
}

// canonicalJSON returns j in the form the server persists it.
func (j JoinField) canonicalJSON() (json.RawMessage, error) {
	if j.Clause == nil {
		return plainJSON(j.Shorthand), nil
	}
	encoded, err := json.Marshal(canonicalJoinClause{
		SpineFields:  j.Clause.SpineFields,
		SourceFields: j.Clause.SourceFields,
		SpineExpr:    j.Clause.SpineExpr,
		SourceExpr:   j.Clause.SourceExpr,
		Constant:     j.Clause.Constant,
	})
	if err != nil {
		return nil, err
	}
	return canonicalJoin(encoded)
}

// canonicalJoin re-encodes a JSON join_fields value, a shorthand string or a
// clause, in the form the server persists it, each number of a constant
// spelled by canonicalNumber.
func canonicalJoin(raw json.RawMessage) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var shorthand string
		if err := json.Unmarshal(trimmed, &shorthand); err != nil {
			return nil, err
		}
		return plainJSON(shorthand), nil
	}
	var clause canonicalJoinClause
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.UseNumber()
	if err := decoder.Decode(&clause); err != nil {
		return nil, err
	}
	clause.Constant = canonicalNumbers(clause.Constant)
	return clause.marshal()
}

// canonicalNumbers replaces each json.Number in value, a value decoded with
// UseNumber, with its canonicalNumber spelling.
func canonicalNumbers(value any) any {
	return replaceLeaves(value, func(leaf any) any {
		if n, ok := leaf.(json.Number); ok {
			return canonicalNumber(n)
		}
		return leaf
	})
}

// replaceLeaves replaces, in place, each value under value that is neither a
// map[string]any nor a []any with replace's result for it, and returns value
// or, when value is itself such a leaf, replace's result.
func replaceLeaves(value any, replace func(any) any) any {
	switch v := value.(type) {
	case map[string]any:
		for key, item := range v {
			v[key] = replaceLeaves(item, replace)
		}
	case []any:
		for i, item := range v {
			v[i] = replaceLeaves(item, replace)
		}
	default:
		return replace(value)
	}
	return value
}

// canonicalNumber spells n as a decimal integer when it has no fraction or
// exponent, and otherwise as the float64 it parses to, always with a '.' or
// an exponent.
func canonicalNumber(n json.Number) json.Number {
	literal := n.String()
	if !strings.ContainsAny(literal, ".eE") {
		if integer, ok := new(big.Int).SetString(literal, 10); ok {
			return json.Number(integer.String())
		}
		return n
	}
	parsed, err := n.Float64()
	if err != nil {
		return n
	}
	return floatNumber(parsed)
}

// floatNumber spells the finite f as encoding/json does, with ".0" added when
// that spelling has neither a '.' nor an exponent.
func floatNumber(f float64) json.Number {
	spelled := string(plainJSON(f))
	if !strings.ContainsAny(spelled, ".eE") {
		spelled += ".0"
	}
	return json.Number(spelled)
}

// plainJSON encodes a string, number, []string, map[string]string or
// OutputMapping, none of which json.Marshal can fail on.
func plainJSON(value any) json.RawMessage {
	encoded, _ := json.Marshal(value)
	return encoded
}

// optionalJSON encodes *value, or returns nil when value is nil.
func optionalJSON[T any](value *T) json.RawMessage {
	if value == nil {
		return nil
	}
	return plainJSON(*value)
}

func unionKeys[V any](a, b map[string]V) []string {
	keys := make([]string, 0, len(a)+len(b))
	for key := range a {
		keys = append(keys, key)
	}
	for key := range b {
		if _, dup := a[key]; !dup {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}
