package rule_merges //nolint:revive // Matches the backend resource name.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/pkg/timeutil"
	"github.com/qlustered/qctl/internal/rule_versions"
	"github.com/qlustered/qctl/internal/terminal"
)

// RenderNames supplies the names RenderMerge shows for the ids a merge carries.
type RenderNames struct {
	SourceTable, TargetTable string                   // table display names ("" → show the id only)
	SourceRule, TargetRule   func(uuid string) string // instance_name for a rule UUID of that table; "" when unknown
	Release                  func(uuid string) string // release of a rule revision UUID; "" when unknown
	// Builtin reports a built-in rule revision UUID; nil reports none.
	// RenderTemplateMerge leaves the table rules of such revisions out of its
	// list of table rules not in the template.
	Builtin func(uuid string) bool
}

// mergeActionOrder is DatasetCopyItemAction in the backend's declaration order.
var mergeActionOrder = []api.DatasetCopyItemAction{
	api.DatasetCopyItemActionCreated,
	api.DatasetCopyItemActionReplaced,
	api.DatasetCopyItemActionSkipped,
	api.DatasetCopyItemActionKept,
	api.DatasetCopyItemActionConflict,
	api.DatasetCopyItemActionFailed,
}

// mergeConfigKeys are the DatasetRuleConfigSnapshot keys, in the order
// source_changes and target_changes list them.
var mergeConfigKeys = []string{"rule_revision_id", "params", "severity", "state", "column_mapping", "produced_fields"}

// mergeParamsUnitsLine opens the CONFLICTS section.
const mergeParamsUnitsLine = "Params shown here are stored values; an edit_source resolution takes params " +
	"in display units, which differ for a percent column."

// RenderMerge writes m as text: a header with the merge's state and a count of
// its items per action, one table row per item, and a CONFLICTS block per
// conflict item comparing the source rule's configuration with the target's.
// Every backend string is sanitized for a terminal and kept to one line. A
// rule template merge is rendered by RenderTemplateMerge.
func RenderMerge(w io.Writer, m *api.RuleMergeSchema, names RenderNames) error {
	if m == nil {
		return errors.New("no rule merge to render")
	}
	if m.TemplateName != nil {
		return RenderTemplateMerge(w, m, names)
	}
	var b bytes.Buffer
	writeMergeHeader(&b, m, names)
	if len(m.Items) > 0 {
		b.WriteString("\n")
		if err := writeMergeItems(&b, m.Items, names); err != nil {
			return err
		}
	}
	writeMergeConflicts(&b, m.Items, names)
	_, err := io.WriteString(w, terminal.Sanitize(b.String()))
	return err
}

// writeMergeHeader writes the merge's identity and label lines. The count line
// is "Plan:" while the merge is previewed and "Items:" after that.
func writeMergeHeader(b *bytes.Buffer, m *api.RuleMergeSchema, names RenderNames) {
	writeMergeHeaderCounts(b, m, names, mergeActionCounts(m.Items))
}

// writeMergeHeaderCounts is writeMergeHeader with counts on its count line.
func writeMergeHeaderCounts(b *bytes.Buffer, m *api.RuleMergeSchema, names RenderNames, counts string) {
	fmt.Fprintf(b, "Rule merge %d: %s -> %s\n", m.ID,
		mergeSourceLabel(m, names.SourceTable),
		mergeTableLabel(names.TargetTable, m.TargetDatasetID))
	fmt.Fprintf(b, "%-9s %s\n", "State:", mergeText(string(m.State)))
	if m.Summary != nil && *m.Summary != "" {
		fmt.Fprintf(b, "%-9s %s\n", "Summary:", mergeText(*m.Summary))
	}
	fmt.Fprintf(b, "%-9s %s\n", "Created:", mergeTimestamp(m.CreatedAt))
	label := "Items:"
	if m.State == api.DatasetCopyStatePreviewed {
		label = "Plan:"
	}
	fmt.Fprintf(b, "%-9s %s\n", label, counts)
}

// mergeSourceLabel names a merge's source: the rule template it merges, else
// the source table.
func mergeSourceLabel(m *api.RuleMergeSchema, tableName string) string {
	if m.SourceDatasetID == nil {
		name := ""
		if m.TemplateName != nil {
			name = *m.TemplateName
		}
		return "rule template " + mergeText(name)
	}
	return mergeTableLabel(tableName, *m.SourceDatasetID)
}

// mergeTableLabel is "name (id N)", or "id N" without a name.
func mergeTableLabel(name string, id int) string {
	if name == "" {
		return fmt.Sprintf("id %d", id)
	}
	return fmt.Sprintf("%s (id %d)", mergeText(name), id)
}

// mergeTimestamp renders an absolute UTC time followed by its relative age.
func mergeTimestamp(t time.Time) string {
	return fmt.Sprintf("%s (%s)", t.UTC().Format(time.RFC3339), timeutil.FormatRelative(t))
}

// mergeActionCounts counts items per action, in mergeActionOrder and then any
// other action by name, omitting zero counts; "no rules" without items.
func mergeActionCounts(items []api.RuleMergeItemSchema) string {
	if len(items) == 0 {
		return "no rules"
	}
	counts := map[api.DatasetCopyItemAction]int{}
	for i := range items {
		counts[items[i].Action]++
	}
	order := append([]api.DatasetCopyItemAction(nil), mergeActionOrder...)
	var others []string
	for action := range counts {
		if !mergeKnownAction(action) {
			others = append(others, string(action))
		}
	}
	sort.Strings(others)
	for _, action := range others {
		order = append(order, api.DatasetCopyItemAction(action))
	}
	parts := make([]string, 0, len(order))
	for _, action := range order {
		if counts[action] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[action], mergeText(string(action))))
		}
	}
	return strings.Join(parts, ", ")
}

func mergeKnownAction(action api.DatasetCopyItemAction) bool {
	for _, known := range mergeActionOrder {
		if action == known {
			return true
		}
	}
	return false
}

// writeMergeItems writes one aligned row per item.
func writeMergeItems(b *bytes.Buffer, items []api.RuleMergeItemSchema, names RenderNames) error {
	tw := tabwriter.NewWriter(b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ITEM\tACTION\tSOURCE RULE\tTARGET RULE\tCHOICE\tNOTE")
	for i := range items {
		item := &items[i]
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\n",
			item.ID,
			mergeOrDash(mergeText(string(item.Action))),
			mergeRuleName(names.SourceRule, item.SourceObjectID),
			mergeTargetCell(item, names),
			mergeChoice(item.ConflictChoice),
			mergeOrDash(mergeItemNote(item)))
	}
	return tw.Flush()
}

// mergeTargetCell names an item's target rule; an ambiguous conflict shows its
// candidate count instead.
func mergeTargetCell(item *api.RuleMergeItemSchema, names RenderNames) string {
	if item.TargetObjectID != nil {
		return mergeRuleName(names.TargetRule, item.TargetObjectID)
	}
	if mergeIsAmbiguous(item) {
		return fmt.Sprintf("ambiguous (%d candidates)", len(item.CandidateObjectIds))
	}
	return "-"
}

// mergeIsAmbiguous reports a conflict with candidates and no target rule.
func mergeIsAmbiguous(item *api.RuleMergeItemSchema) bool {
	return item.Action == api.DatasetCopyItemActionConflict &&
		item.TargetObjectID == nil && len(item.CandidateObjectIds) > 0
}

// mergeItemNote is the item's note on one line, followed for a created or
// replaced item by the dataset fields it adds.
func mergeItemNote(item *api.RuleMergeItemSchema) string {
	note := mergeNoteText(item.Note)
	adds := item.Action == api.DatasetCopyItemActionCreated || item.Action == api.DatasetCopyItemActionReplaced
	if !adds || len(item.AddedFieldNames) == 0 {
		return note
	}
	added := "adds fields: " + mergeJoinText(item.AddedFieldNames)
	if note == "" {
		return added
	}
	return note + " (" + added + ")"
}

// mergeRuleName is the rule's instance name from name, or its SHORT-ID when
// name has none; "-" without an id.
func mergeRuleName(name func(string) string, id *string) string {
	if id == nil || *id == "" {
		return "-"
	}
	if name != nil {
		if n := name(*id); n != "" {
			return mergeText(n)
		}
	}
	return mergeText(rule_versions.ShortID(*id))
}

// mergeRuleLabel is the rule's instance name and SHORT-ID, "name (SHORTID)",
// or the SHORT-ID alone when name has none.
func mergeRuleLabel(name func(string) string, id string) string {
	short := mergeText(rule_versions.ShortID(id))
	if name != nil {
		if n := name(id); n != "" {
			return fmt.Sprintf("%s (%s)", mergeText(n), short)
		}
	}
	return short
}

// writeMergeConflicts writes the CONFLICTS section, one block per conflict
// item, or nothing when there is none.
func writeMergeConflicts(b *bytes.Buffer, items []api.RuleMergeItemSchema, names RenderNames) {
	opened := false
	for i := range items {
		item := &items[i]
		if item.Action != api.DatasetCopyItemActionConflict {
			continue
		}
		if !opened {
			b.WriteString("\nCONFLICTS\n")
			b.WriteString(mergeParamsUnitsLine + "\n")
			opened = true
		}
		writeMergeConflict(b, item, names)
	}
}

// writeMergeConflict writes one conflict item's block: the rules it pairs, its
// note, choice and added fields, what changed since the base when there is
// one, and the value of each configuration key that differs.
func writeMergeConflict(b *bytes.Buffer, item *api.RuleMergeItemSchema, names RenderNames) {
	source := "-"
	if item.SourceObjectID != nil && *item.SourceObjectID != "" {
		source = mergeRuleLabel(names.SourceRule, *item.SourceObjectID)
	}
	switch {
	case item.TargetObjectID != nil && *item.TargetObjectID != "":
		fmt.Fprintf(b, "\nItem %d: %s vs %s\n", item.ID, source, mergeRuleLabel(names.TargetRule, *item.TargetObjectID))
	case len(item.CandidateObjectIds) > 0:
		fmt.Fprintf(b, "\nItem %d: %s vs one of %d candidates\n", item.ID, source, len(item.CandidateObjectIds))
		b.WriteString("  Candidates:\n")
		for _, id := range item.CandidateObjectIds {
			fmt.Fprintf(b, "    - %s\n", mergeRuleLabel(names.TargetRule, id))
		}
	default:
		fmt.Fprintf(b, "\nItem %d: %s vs no target rule\n", item.ID, source)
	}
	if note := mergeNoteText(item.Note); note != "" {
		fmt.Fprintf(b, "  %-16s %s\n", "Note:", note)
	}
	if item.ConflictChoice != nil {
		fmt.Fprintf(b, "  %-16s %s\n", "Choice:", mergeText(string(*item.ConflictChoice)))
	}
	if len(item.AddedFieldNames) > 0 {
		fmt.Fprintf(b, "  %-16s %s\n", "Adds fields:", mergeJoinText(item.AddedFieldNames))
	}
	writeMergeLineage(b, item)
	writeMergeDifferences(b, item)
}

// writeMergeLineage writes the item's source_changes and target_changes when
// it has a base, and "Base: none" otherwise.
func writeMergeLineage(b *bytes.Buffer, item *api.RuleMergeItemSchema) {
	if item.BaseSnapshot == nil {
		fmt.Fprintf(b, "  %-16s %s\n", "Base:", "none")
		return
	}
	fmt.Fprintf(b, "  %-16s %s\n", "Source changes:", mergeChangeList(item.SourceChanges, "-"))
	fmt.Fprintf(b, "  %-16s %s\n", "Target changes:", mergeChangeList(item.TargetChanges, "(no target rule)"))
}

// mergeChangeList joins a change list, "none" when empty and absent when nil.
func mergeChangeList(keys *[]string, absent string) string {
	if keys == nil {
		return absent
	}
	if len(*keys) == 0 {
		return "none"
	}
	return mergeJoinText(*keys)
}

// writeMergeDifferences writes each configuration key that differs between
// the source and target snapshots, with the base value when there is a base.
// Without a target snapshot it writes the keys that differ between the base
// and the source; without either, nothing.
func writeMergeDifferences(b *bytes.Buffer, item *api.RuleMergeItemSchema) {
	source := &item.RuleSnapshot
	base, target := item.BaseSnapshot, item.TargetSnapshot
	against := target
	if against == nil {
		against = base
	}
	if against == nil {
		return
	}
	var keys []string
	for _, key := range mergeConfigKeys {
		if !mergeConfigEqual(key, source, against) {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		fmt.Fprintf(b, "  %-16s %s\n", "Configuration:", "no difference")
		return
	}
	b.WriteString("  Configuration:\n")
	for _, key := range keys {
		fmt.Fprintf(b, "    %s:\n", key)
		if base != nil {
			fmt.Fprintf(b, "      %-8s %s\n", "base:", mergeConfigValue(key, base))
		}
		fmt.Fprintf(b, "      %-8s %s\n", "source:", mergeConfigValue(key, source))
		if target != nil {
			fmt.Fprintf(b, "      %-8s %s\n", "target:", mergeConfigValue(key, target))
		}
	}
}

func mergeChoice(choice *api.DatasetCopyConflictChoice) string {
	if choice == nil {
		return "-"
	}
	return mergeOrDash(mergeText(string(*choice)))
}

// mergeConfigEqual compares one configuration key of two snapshots; a state
// is compared as enabled or not.
func mergeConfigEqual(key string, a, b *api.DatasetRuleConfigSnapshot) bool {
	if key == "state" {
		return (a.State == api.DatasetRuleStateEnabled) == (b.State == api.DatasetRuleStateEnabled)
	}
	return mergeConfigValue(key, a) == mergeConfigValue(key, b)
}

// mergeConfigValue renders one configuration key of s on one line: strings as
// they are, and params, column_mapping and produced_fields as compact JSON
// with sorted keys.
func mergeConfigValue(key string, s *api.DatasetRuleConfigSnapshot) string {
	switch key {
	case "rule_revision_id":
		return mergeText(s.RuleRevisionID)
	case "severity":
		return mergeText(string(s.Severity))
	case "state":
		return mergeText(string(s.State))
	case "params":
		return mergeCompactJSON(s.Params)
	case "column_mapping":
		return mergeCompactJSON(s.ColumnMapping)
	case "produced_fields":
		return mergeCompactJSON(s.ProducedFields)
	}
	return ""
}

// mergeCompactJSON renders v as one-line JSON with sorted map keys and no
// HTML escaping ("<unencodable>" on error).
func mergeCompactJSON(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "<unencodable>"
	}
	return mergeText(strings.TrimSuffix(buf.String(), "\n"))
}

// mergeNoteText renders a backend note as plain text on one line; "" for nil.
func mergeNoteText(note *string) string {
	if note == nil {
		return ""
	}
	return mergeOneLine(apierror.ToPlainText(*note))
}

// mergeJoinText sanitizes each of values and joins them with ", ".
func mergeJoinText(values []string) string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = mergeText(v)
	}
	return strings.Join(out, ", ")
}

// mergeText sanitizes s for a terminal and keeps it to one line.
func mergeText(s string) string {
	return mergeOneLine(terminal.Sanitize(s))
}

// mergeLineBreak matches a run of line breaks and the blanks around it.
var mergeLineBreak = regexp.MustCompile(`[ \t]*[\n\x{2028}\x{2029}][ \t\n\x{2028}\x{2029}]*`)

// mergeOneLine shows each run of line breaks, and each tab, as one space.
func mergeOneLine(s string) string {
	return strings.ReplaceAll(mergeLineBreak.ReplaceAllString(s, " "), "\t", " ")
}

func mergeOrDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
