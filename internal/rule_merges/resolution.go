package rule_merges //nolint:revive // Public package name matches the established resource domain.

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
	"github.com/qlustered/qctl/internal/pkg/piimask"
	"github.com/qlustered/qctl/internal/row_filters"
	"github.com/qlustered/qctl/internal/terminal"
	"gopkg.in/yaml.v3"
)

// RuleMergeResolutionKind is the kind ParseResolution accepts.
const RuleMergeResolutionKind = "RuleMergeResolution"

// RuleMergeResolutionEntry says what to do with the merge item of one source
// table rule. Rule and TargetRule are table-rule references: a name, SHORT-ID,
// r- alias or UUID. ColumnMapping, Params, Severity and RowFilter go with
// edit_source only; an empty map counts as absent. RowFilterSet records a
// row_filter key, so an explicit null is told from no key.
type RuleMergeResolutionEntry struct {
	Rule          string                 `yaml:"rule" json:"rule"`
	Choice        string                 `yaml:"choice" json:"choice"`
	TargetRule    string                 `yaml:"target_rule,omitempty" json:"target_rule,omitempty"`
	ColumnMapping map[string]string      `yaml:"column_mapping,omitempty" json:"column_mapping,omitempty"`
	Params        map[string]interface{} `yaml:"params,omitempty" json:"params,omitempty"`
	Severity      string                 `yaml:"severity,omitempty" json:"severity,omitempty"`
	RowFilter     *row_filters.Filter    `yaml:"row_filter,omitempty" json:"row_filter,omitempty"`
	RowFilterSet  bool                   `yaml:"-" json:"-"`
}

// UnmarshalYAML strictly decodes an entry and records whether it has a
// row_filter key, null included.
func (e *RuleMergeResolutionEntry) UnmarshalYAML(node *yaml.Node) error {
	type plain RuleMergeResolutionEntry
	if err := pkgmanifest.StrictUnmarshalNode(node, (*plain)(e)); err != nil {
		return err
	}
	e.RowFilterSet = row_filters.HasYAMLField(node, "row_filter")
	return nil
}

// UnmarshalJSON strictly decodes an entry and records whether it has a
// row_filter key, null included.
func (e *RuleMergeResolutionEntry) UnmarshalJSON(data []byte) error {
	type plain RuleMergeResolutionEntry
	if err := pkgmanifest.StrictUnmarshal(data, (*plain)(e)); err != nil {
		return err
	}
	e.RowFilterSet = row_filters.HasJSONField(data, "row_filter")
	return nil
}

// RuleMergeResolutionSpec is the spec of a RuleMergeResolution document.
type RuleMergeResolutionSpec struct {
	Resolutions []RuleMergeResolutionEntry `yaml:"resolutions" json:"resolutions"`
}

// RuleMergeResolution is one `-f` document.
type RuleMergeResolution struct {
	APIVersion string                  `yaml:"apiVersion" json:"apiVersion"`
	Kind       string                  `yaml:"kind" json:"kind"`
	Metadata   pkgmanifest.Metadata    `yaml:"metadata" json:"metadata"`
	Spec       RuleMergeResolutionSpec `yaml:"spec" json:"spec"`
}

// RuleResolver maps a table-rule reference to that rule's UUID.
type RuleResolver func(ref string) (id string, err error)

// ParseResolution strict-parses data as exactly one RuleMergeResolution
// document, checks its apiVersion and kind, and validates its spec. Every
// failure exits 2.
func ParseResolution(data []byte) (*RuleMergeResolution, error) {
	docs, err := pkgmanifest.SplitDocuments(data)
	if err != nil {
		return nil, qerrors.Wrap(qerrors.ExitBadUsage, err, "failed to parse RuleMergeResolution manifest")
	}
	if len(docs) != 1 {
		return nil, qerrors.NewBadUsagef("expected one %s document, found %d", RuleMergeResolutionKind, len(docs))
	}
	var doc RuleMergeResolution
	if err := pkgmanifest.StrictUnmarshal(docs[0], &doc); err != nil {
		return nil, qerrors.Wrap(qerrors.ExitBadUsage, err, "failed to parse RuleMergeResolution manifest")
	}
	if doc.APIVersion != pkgmanifest.APIVersionV1 {
		return nil, qerrors.NewBadUsagef("expected apiVersion '%s', got '%s'", pkgmanifest.APIVersionV1, doc.APIVersion)
	}
	if doc.Kind != RuleMergeResolutionKind {
		return nil, qerrors.NewBadUsagef(
			"kind %q is not accepted here: 'qctl merge table-rules -f' takes %s", doc.Kind, RuleMergeResolutionKind)
	}
	if err := doc.Spec.Validate(); err != nil {
		return nil, err
	}
	return &doc, nil
}

// Validate checks the spec without the merge: at least one entry, each with a
// rule and a choice from the generated DatasetCopyConflictChoice enum, edit
// fields only with edit_source, a severity from the generated RuleSeverity
// enum, params holding no masked text, a row filter as a saved one, and no
// rule reference twice. Every failure exits 2.
func (s RuleMergeResolutionSpec) Validate() error {
	if len(s.Resolutions) == 0 {
		return qerrors.NewBadUsage("spec.resolutions is empty: give one entry per rule to resolve")
	}
	first := map[string]int{}
	for i, entry := range s.Resolutions {
		if err := entry.validate(i); err != nil {
			return err
		}
		if j, ok := first[entry.Rule]; ok {
			return qerrors.NewBadUsagef("spec.resolutions[%d] and spec.resolutions[%d] both name rule %q", j, i, entry.Rule)
		}
		first[entry.Rule] = i
	}
	return nil
}

// validate checks one entry; i is its index in spec.resolutions.
func (e RuleMergeResolutionEntry) validate(i int) error {
	if e.Rule == "" {
		return qerrors.NewBadUsagef("spec.resolutions[%d].rule is required: name the source table rule", i)
	}
	choices := api.EnumValues[api.DatasetCopyConflictChoice]()
	if e.Choice == "" {
		return qerrors.NewBadUsagef("spec.resolutions[%d] (rule %q): choice is required (one of: %s)",
			i, e.Rule, strings.Join(choices, ", "))
	}
	if !slices.Contains(choices, e.Choice) {
		return qerrors.NewBadUsagef("spec.resolutions[%d] (rule %q): choice %q is not one of: %s",
			i, e.Rule, e.Choice, strings.Join(choices, ", "))
	}
	if fields := e.editFields(); len(fields) > 0 && e.Choice != string(api.DatasetCopyConflictChoiceEditSource) {
		return qerrors.NewBadUsagef("spec.resolutions[%d] (rule %q): only choice %s takes %s, and this entry's choice is %s",
			i, e.Rule, api.DatasetCopyConflictChoiceEditSource, strings.Join(fields, ", "), e.Choice)
	}
	if severities := api.EnumValues[api.RuleSeverity](); e.Severity != "" && !slices.Contains(severities, e.Severity) {
		return qerrors.NewBadUsagef("spec.resolutions[%d] (rule %q): severity %q is not one of: %s",
			i, e.Rule, e.Severity, strings.Join(severities, ", "))
	}
	if _, err := json.Marshal(e.Params); err != nil {
		return qerrors.Wrap(qerrors.ExitBadUsage, err,
			fmt.Sprintf("spec.resolutions[%d] (rule %q): params cannot be sent as JSON", i, e.Rule))
	}
	if paths := piimask.Paths(fmt.Sprintf("spec.resolutions[%d].params", i), e.Params); len(paths) > 0 {
		return qerrors.NewBadUsagef("spec.resolutions[%d] (rule %q) holds masked text at %s: %s stands in for a value "+
			"the server masks for you, not the value itself, and merging it would store that text. Write the real values",
			i, e.Rule, terminal.Sanitize(strings.Join(paths, ", ")), piimask.Mask)
	}
	return e.RowFilter.Validate(true, fmt.Sprintf("spec.resolutions[%d].row_filter", i))
}

// hasRowFilter reports whether the entry sets row_filter, null included.
func (e RuleMergeResolutionEntry) hasRowFilter() bool {
	return e.RowFilterSet || e.RowFilter != nil
}

// editFields names the edit_source-only keys the entry sets.
func (e RuleMergeResolutionEntry) editFields() []string {
	var fields []string
	if len(e.ColumnMapping) > 0 {
		fields = append(fields, "column_mapping")
	}
	if len(e.Params) > 0 {
		fields = append(fields, "params")
	}
	if e.Severity != "" {
		fields = append(fields, "severity")
	}
	if e.hasRowFilter() {
		fields = append(fields, "row_filter")
	}
	return fields
}

// ToRequest validates the spec and maps each entry, in order, onto the item of
// items whose source rule sourceRule resolves its rule to. It refuses, exit 2,
// an entry whose rule has no item, two entries on one item, and a choice or
// target_rule the item's action does not take; target_rule is resolved with
// targetRule. A resolver's error is returned wrapped, keeping its exit code.
func (s RuleMergeResolutionSpec) ToRequest(items []api.RuleMergeItemSchema, sourceRule, targetRule RuleResolver) ([]Resolution, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	out := make([]Resolution, 0, len(s.Resolutions))
	entryForItem := map[int]int{}
	for i, entry := range s.Resolutions {
		sourceID, err := sourceRule(entry.Rule)
		if err != nil {
			return nil, fmt.Errorf("spec.resolutions[%d] (rule %q): %w", i, entry.Rule, err)
		}
		item, ok := itemForSourceRule(items, sourceID)
		if !ok {
			return nil, qerrors.NewBadUsagef("spec.resolutions[%d]: rule %q has no item in this merge", i, entry.Rule)
		}
		if j, ok := entryForItem[item.ID]; ok {
			return nil, qerrors.NewBadUsagef("spec.resolutions[%d] and spec.resolutions[%d] both resolve rule %q (item %d)",
				j, i, entry.Rule, item.ID)
		}
		entryForItem[item.ID] = i
		if err := entry.checkChoiceFits(i, item); err != nil {
			return nil, err
		}
		resolution, err := entry.toResolution(i, item, targetRule)
		if err != nil {
			return nil, err
		}
		out = append(out, resolution)
	}
	return out, nil
}

// itemForSourceRule finds the item whose source rule is sourceID.
func itemForSourceRule(items []api.RuleMergeItemSchema, sourceID string) (api.RuleMergeItemSchema, bool) {
	for _, item := range items {
		if item.SourceObjectID != nil && strings.EqualFold(*item.SourceObjectID, sourceID) {
			return item, true
		}
	}
	return api.RuleMergeItemSchema{}, false
}

// isAmbiguousConflict reports whether item is a conflict matched to no single
// target rule but to candidates.
func isAmbiguousConflict(item api.RuleMergeItemSchema) bool {
	return item.Action == api.DatasetCopyItemActionConflict && item.TargetObjectID == nil && len(item.CandidateObjectIds) > 0
}

// checkChoiceFits refuses, exit 2, a choice or target_rule the item's action
// does not take.
func (e RuleMergeResolutionEntry) checkChoiceFits(i int, item api.RuleMergeItemSchema) error {
	switch item.Action {
	case api.DatasetCopyItemActionConflict:
	case api.DatasetCopyItemActionCreated, api.DatasetCopyItemActionReplaced:
		if e.Choice != string(api.DatasetCopyConflictChoiceKeepTarget) {
			return qerrors.NewBadUsagef("spec.resolutions[%d]: rule %q is %s in this merge; its only choice is %s, which declines it",
				i, e.Rule, item.Action, api.DatasetCopyConflictChoiceKeepTarget)
		}
	default:
		return qerrors.NewBadUsagef("spec.resolutions[%d]: rule %q is %s in this merge and takes no resolution; remove the entry",
			i, e.Rule, item.Action)
	}
	ambiguous := isAmbiguousConflict(item)
	switch {
	case ambiguous && e.Choice != string(api.DatasetCopyConflictChoiceKeepBoth) && e.TargetRule == "":
		return qerrors.NewBadUsagef("spec.resolutions[%d]: rule %q is an ambiguous conflict; %s needs target_rule naming one of its candidates",
			i, e.Rule, e.Choice)
	case ambiguous && e.Choice == string(api.DatasetCopyConflictChoiceKeepBoth) && e.TargetRule != "":
		return qerrors.NewBadUsagef("spec.resolutions[%d]: rule %q: target_rule does not go with %s",
			i, e.Rule, api.DatasetCopyConflictChoiceKeepBoth)
	case !ambiguous && e.TargetRule != "":
		return qerrors.NewBadUsagef("spec.resolutions[%d]: rule %q: target_rule is only for an ambiguous conflict",
			i, e.Rule)
	}
	return nil
}

// toResolution builds the wire resolution for item, resolving target_rule
// with targetRule and refusing, exit 2, one that is not among the item's
// candidates. Maps are copied.
func (e RuleMergeResolutionEntry) toResolution(i int, item api.RuleMergeItemSchema, targetRule RuleResolver) (Resolution, error) {
	out := Resolution{RuleMergeResolutionSchema: api.RuleMergeResolutionSchema{
		ItemID: item.ID, Choice: api.DatasetCopyConflictChoice(e.Choice),
	}}
	if e.TargetRule != "" {
		targetID, err := targetRule(e.TargetRule)
		if err != nil {
			return out, fmt.Errorf("spec.resolutions[%d] (rule %q) target_rule %q: %w", i, e.Rule, e.TargetRule, err)
		}
		k := slices.IndexFunc(item.CandidateObjectIds, func(c string) bool { return strings.EqualFold(c, targetID) })
		if k < 0 {
			return out, qerrors.NewBadUsagef("spec.resolutions[%d]: target_rule %q is not among the candidates of rule %q",
				i, e.TargetRule, e.Rule)
		}
		parsed, err := uuid.Parse(item.CandidateObjectIds[k])
		if err != nil {
			return out, fmt.Errorf("spec.resolutions[%d]: candidate %q of rule %q is not a UUID: %w",
				i, item.CandidateObjectIds[k], e.Rule, err)
		}
		out.TargetRuleID = &parsed
	}
	if e.Choice != string(api.DatasetCopyConflictChoiceEditSource) {
		return out, nil
	}
	if len(e.ColumnMapping) > 0 {
		mapping := maps.Clone(e.ColumnMapping)
		out.ColumnMapping = &mapping
	}
	if len(e.Params) > 0 {
		params := maps.Clone(e.Params)
		out.Params = &params
	}
	if e.Severity != "" {
		severity := api.RuleSeverity(e.Severity)
		out.Severity = &severity
	}
	if e.hasRowFilter() {
		out.RowFilter = row_filters.Saved(e.RowFilter)
		out.RowFilterSet = true
	}
	return out, nil
}

// UnresolvedConflicts returns, in order, the conflict items of items that no
// resolution names.
func UnresolvedConflicts(items []api.RuleMergeItemSchema, resolutions []Resolution) []api.RuleMergeItemSchema {
	resolved := map[int]bool{}
	for _, r := range resolutions {
		resolved[r.ItemID] = true
	}
	var out []api.RuleMergeItemSchema
	for _, item := range items {
		if item.Action == api.DatasetCopyItemActionConflict && !resolved[item.ID] {
			out = append(out, item)
		}
	}
	return out
}

// Skeleton renders a fill-in RuleMergeResolution document with one entry per
// conflict item of items: rule set to the source rule's name, choice left
// empty, and for an ambiguous conflict an empty target_rule. Comments above
// an entry carry the item's note, its matched target rule and the choices; a
// target_rule's comment lists the candidates.
// sourceRuleName and targetRuleName name a rule by UUID; nil or an empty name
// falls back to the UUID. Rule names are written as YAML scalars, and every
// server string inside a comment goes through pkgmanifest.CommentSafe.
func Skeleton(items []api.RuleMergeItemSchema, sourceRuleName, targetRuleName func(id string) string) string {
	choices := strings.Join(api.EnumValues[api.DatasetCopyConflictChoice](), ", ")
	var b strings.Builder
	b.WriteString("# Set choice on every entry (and target_rule where there is one), then run\n")
	b.WriteString("# the same qctl merge table-rules command again with -f <this file>.\n")
	fmt.Fprintf(&b, "# %s also takes column_mapping, params (display units), severity and\n", api.DatasetCopyConflictChoiceEditSource)
	b.WriteString("# row_filter (null removes the source rule's).\n")
	fmt.Fprintf(&b, "apiVersion: %s\nkind: %s\nspec:\n", pkgmanifest.APIVersionV1, RuleMergeResolutionKind)

	var conflicts []api.RuleMergeItemSchema
	for _, item := range items {
		if item.Action == api.DatasetCopyItemActionConflict {
			conflicts = append(conflicts, item)
		}
	}
	if len(conflicts) == 0 {
		b.WriteString("  resolutions: []\n")
		return b.String()
	}
	b.WriteString("  resolutions:\n")
	for _, item := range conflicts {
		source := ""
		if item.SourceObjectID != nil {
			source = *item.SourceObjectID
		}
		name := skeletonRuleName(sourceRuleName, source)
		if item.Note != nil && *item.Note != "" {
			fmt.Fprintf(&b, "    # note: %s\n", pkgmanifest.CommentSafe(apierror.ToPlainText(*item.Note)))
		}
		if item.TargetObjectID != nil {
			fmt.Fprintf(&b, "    # matched target rule: %s\n",
				pkgmanifest.CommentSafe(skeletonRuleName(targetRuleName, *item.TargetObjectID)))
		}
		fmt.Fprintf(&b, "    # choice: one of %s\n", choices)
		fmt.Fprintf(&b, "    - rule:%s\n", skeletonValue(name))
		b.WriteString("      choice:\n")
		if isAmbiguousConflict(item) {
			candidates := make([]string, len(item.CandidateObjectIds))
			for k, id := range item.CandidateObjectIds {
				candidates[k] = pkgmanifest.CommentSafe(skeletonScalar(skeletonRuleName(targetRuleName, id)))
			}
			fmt.Fprintf(&b, "      # target_rule: one of %s (not with %s)\n", strings.Join(candidates, ", "), api.DatasetCopyConflictChoiceKeepBoth)
			b.WriteString("      target_rule:\n")
		}
	}
	return b.String()
}

// skeletonRuleName is namer's name for the rule id, or id when namer is nil or
// returns "".
func skeletonRuleName(namer func(id string) string, id string) string {
	if namer != nil {
		if name := namer(id); name != "" {
			return name
		}
	}
	return id
}

// skeletonValue is s as the value after a "key:" on one skeleton line: a
// space and the YAML scalar, or nothing for "" so the key reads as null.
func skeletonValue(s string) string {
	if s == "" {
		return ""
	}
	return " " + skeletonScalar(s)
}

// skeletonScalar renders s as a single-line YAML scalar that reads back as s.
func skeletonScalar(s string) string {
	if out, err := jsonnum.MarshalYAML(s); err == nil {
		v := strings.TrimSuffix(string(out), "\n")
		if !strings.ContainsAny(v, "\n\r\u0085\u2028\u2029") && terminal.Sanitize(v) == v {
			return v
		}
	}
	node := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Style: yaml.DoubleQuotedStyle, Value: strings.ToValidUTF8(s, "\uFFFD")}
	out, err := jsonnum.MarshalYAML(node)
	if err != nil {
		return ""
	}
	return strings.TrimSuffix(string(out), "\n")
}
