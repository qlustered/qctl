package rule_merges //nolint:revive // Matches the backend resource name.

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/rule_versions"
	"github.com/qlustered/qctl/internal/terminal"
)

// RenderTemplateMerge writes m, a rule template merge, as text: a header with
// the merge's state and a count of its entries' items per action, its apply
// blockers, one block per template entry's item, and the table rules the
// template does not name, less those whose revision names.Builtin reports.
// Every backend string is sanitized for a terminal and kept to one line.
func RenderTemplateMerge(w io.Writer, m *api.RuleMergeSchema, names RenderNames) error {
	if m == nil {
		return errors.New("no rule merge to render")
	}
	entries, unmanaged := splitTemplateItems(m.Items)
	if names.Builtin != nil {
		unmanaged = slices.DeleteFunc(unmanaged, func(item api.RuleMergeItemSchema) bool {
			return names.Builtin(item.RuleSnapshot.RuleRevisionID)
		})
	}
	counts := mergeActionCounts(entries)
	if n := len(unmanaged); n > 0 {
		counts += fmt.Sprintf("; %d %s not in the template", n, plural(n, "table rule", "table rules"))
	}
	var b bytes.Buffer
	writeMergeHeaderCounts(&b, m, names, counts)
	if len(m.ApplyBlockers) > 0 {
		b.WriteString("\nAPPLY BLOCKERS\n")
		for _, blocker := range m.ApplyBlockers {
			fmt.Fprintf(&b, "  %s: %s\n", mergeText(string(blocker.Code)), mergeOneLine(terminal.Sanitize(blocker.Message)))
		}
	}
	if len(entries) > 0 {
		b.WriteString("\nITEMS\n")
		for i := range entries {
			writeTemplateItem(&b, &entries[i], names)
		}
	}
	if len(unmanaged) > 0 {
		b.WriteString("\nTABLE RULES NOT IN THE TEMPLATE (left alone)\n")
		for i := range unmanaged {
			fmt.Fprintf(&b, "  %s\n", templateRuleLabel(names.TargetRule, unmanaged[i].TargetObjectID))
		}
	}
	_, err := io.WriteString(w, terminal.Sanitize(b.String()))
	return err
}

// splitTemplateItems splits items into those of template entries and those of
// reason not_in_template, keeping their order.
func splitTemplateItems(items []api.RuleMergeItemSchema) (entries, unmanaged []api.RuleMergeItemSchema) {
	for _, item := range items {
		if itemReason(item) == api.RuleMergeItemReasonNotInTemplate {
			unmanaged = append(unmanaged, item)
		} else {
			entries = append(entries, item)
		}
	}
	return entries, unmanaged
}

// itemReason is the item's reason, "" when it has none.
func itemReason(item api.RuleMergeItemSchema) api.RuleMergeItemReason {
	if item.Reason == nil {
		return ""
	}
	return *item.Reason
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// templateLabel is the width of an item block's labels.
const templateLabel = "  %-12s %s\n"

// writeTemplateItem writes one entry's item block.
func writeTemplateItem(b *bytes.Buffer, item *api.RuleMergeItemSchema, names RenderNames) {
	key := "-"
	if item.TemplateKey != nil {
		key = mergeOrDash(mergeText(*item.TemplateKey))
	}
	fmt.Fprintf(b, "\nItem %d: %s\n", item.ID, key)
	action := mergeOrDash(mergeText(string(item.Action)))
	if reason := itemReason(*item); reason != "" {
		action += " (" + mergeText(string(reason)) + ")"
	}
	fmt.Fprintf(b, templateLabel, "Action:", action)
	if item.ConflictChoice != nil {
		fmt.Fprintf(b, templateLabel, "Choice:", mergeText(string(*item.ConflictChoice)))
	}
	switch {
	case item.TargetObjectID != nil && *item.TargetObjectID != "":
		fmt.Fprintf(b, templateLabel, "Table rule:", templateRuleLabel(names.TargetRule, item.TargetObjectID))
	case len(item.CandidateObjectIds) > 0:
		candidates := make([]string, len(item.CandidateObjectIds))
		for k, id := range item.CandidateObjectIds {
			candidates[k] = mergeRuleLabel(names.TargetRule, id)
		}
		fmt.Fprintf(b, templateLabel, "Table rule:", "ambiguous")
		fmt.Fprintf(b, templateLabel, "Candidates:", strings.Join(candidates, ", "))
	}
	fmt.Fprintf(b, templateLabel, "Release:", templateReleaseChange(item, names.Release))
	writeTemplateBindings(b, item.FieldBindings)
	if produced := templateProduced(item); produced != "" {
		fmt.Fprintf(b, templateLabel, "Produces:", produced)
	}
	if len(item.Effects) > 0 {
		b.WriteString("  Effects:\n")
		for _, effect := range item.Effects {
			fmt.Fprintf(b, "    - %s\n", mergeNoteText(&effect))
		}
	}
	if note := mergeNoteText(item.Note); note != "" {
		fmt.Fprintf(b, templateLabel, "Note:", note)
	}
	writeTemplateDifferences(b, item)
}

// templateRuleLabel is mergeRuleLabel for id, "-" without one.
func templateRuleLabel(name func(string) string, id *string) string {
	if id == nil || *id == "" {
		return "-"
	}
	return mergeRuleLabel(name, *id)
}

// templateReleaseChange is the release the item plans, preceded by the table
// rule's release and "->" when the two revisions differ.
func templateReleaseChange(item *api.RuleMergeItemSchema, release func(string) string) string {
	next := templateRelease(release, item.RuleSnapshot.RuleRevisionID)
	if item.TargetSnapshot == nil || strings.EqualFold(item.TargetSnapshot.RuleRevisionID, item.RuleSnapshot.RuleRevisionID) {
		return next
	}
	return templateRelease(release, item.TargetSnapshot.RuleRevisionID) + " -> " + next
}

// templateRelease is release's name for a rule revision id, else "revision"
// and the id's SHORT-ID; "-" without an id.
func templateRelease(release func(string) string, revisionID string) string {
	if revisionID == "" {
		return "-"
	}
	if release != nil {
		if name := release(revisionID); name != "" {
			return mergeText(name)
		}
	}
	return "revision " + mergeText(rule_versions.ShortID(revisionID))
}

// writeTemplateBindings writes one line per rule field: "field -> column
// (source)" when bound, else its status and whether it is required, followed
// by its candidate columns with their reasons.
func writeTemplateBindings(b *bytes.Buffer, bindings []api.RuleTemplateFieldBindingSchema) {
	if len(bindings) == 0 {
		return
	}
	b.WriteString("  Fields:\n")
	for _, binding := range bindings {
		field := mergeOrDash(mergeText(binding.RuleFieldName))
		if binding.Status == api.Bound {
			column := "-"
			if binding.BoundTo != nil {
				column = mergeOrDash(mergeText(*binding.BoundTo))
			}
			line := field + " -> " + column
			if binding.Source != nil {
				line += " (" + mergeText(string(*binding.Source)) + ")"
			}
			fmt.Fprintf(b, "    %s\n", line)
			continue
		}
		need := "optional"
		if binding.Required {
			need = "required"
		}
		fmt.Fprintf(b, "    %s: %s, %s\n", field, mergeText(string(binding.Status)), need)
		fmt.Fprintf(b, "      candidates: %s\n", templateCandidates(binding.Candidates))
	}
}

// templateCandidates is "column (reason)" per candidate, best first; "none"
// without any.
func templateCandidates(candidates []api.RuleFieldColumnCandidateSchema) string {
	if len(candidates) == 0 {
		return "none"
	}
	parts := make([]string, len(candidates))
	for i, c := range candidates {
		parts[i] = mergeText(c.Column)
		if reason := mergeText(c.Reason); reason != "" {
			parts[i] += " (" + reason + ")"
		}
	}
	return strings.Join(parts, ", ")
}

// templateProduced lists the columns the item's planned rule produces, each
// the item adds to the table marked "(new)"; "" when there are none.
func templateProduced(item *api.RuleMergeItemSchema) string {
	var parts []string
	seen := map[string]bool{}
	for _, spec := range item.RuleSnapshot.ProducedFields {
		column := spec.DatasetFieldName
		seen[column] = true
		part := mergeText(column)
		if slices.Contains(item.AddedFieldNames, column) {
			part += " (new)"
		}
		parts = append(parts, part)
	}
	for _, column := range item.AddedFieldNames {
		if !seen[column] {
			parts = append(parts, mergeText(column)+" (new)")
		}
	}
	return strings.Join(parts, ", ")
}

// writeTemplateDifferences writes, for a conflict with a table rule other than
// a family_mismatch, each configuration key whose planned value differs from
// the table rule's.
func writeTemplateDifferences(b *bytes.Buffer, item *api.RuleMergeItemSchema) {
	if item.Action != api.DatasetCopyItemActionConflict || item.TargetSnapshot == nil ||
		itemReason(*item) == api.RuleMergeItemReasonFamilyMismatch {
		return
	}
	var keys []string
	for _, key := range mergeConfigKeys {
		if !mergeConfigEqual(key, &item.RuleSnapshot, item.TargetSnapshot) {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return
	}
	b.WriteString("  Differs from the table rule (params as stored):\n")
	for _, key := range keys {
		fmt.Fprintf(b, "    %s:\n", key)
		fmt.Fprintf(b, "      %-9s %s\n", "template:", mergeConfigValue(key, &item.RuleSnapshot))
		fmt.Fprintf(b, "      %-9s %s\n", "table:", mergeConfigValue(key, item.TargetSnapshot))
	}
}
