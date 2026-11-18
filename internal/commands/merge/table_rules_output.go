package merge

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/dataset_rules"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/rule_merges"
	"github.com/qlustered/qctl/internal/rule_versions"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// mergeOutput is where 'merge table-rules' writes: human is stdout for the
// table view and stderr under -o json, yaml or name, whose stdout is one
// document.
type mergeOutput struct {
	format output.Format
	human  io.Writer
	stdout io.Writer
}

func newMergeOutput(cmd *cobra.Command) mergeOutput {
	format, _ := cmd.Flags().GetString("output")
	o := mergeOutput{format: output.Format(format), human: cmd.OutOrStdout(), stdout: cmd.OutOrStdout()}
	switch o.format {
	case output.FormatJSON, output.FormatYAML, output.FormatName:
		o.human = cmd.ErrOrStderr()
	}
	return o
}

// writeDocument writes stdout's document: body as received under -o json or
// yaml, rule-merge/<id> under -o name, and nothing for the table view.
func (o mergeOutput) writeDocument(id int, body []byte) error {
	switch o.format {
	case output.FormatJSON:
		return output.WriteIndentedJSON(o.stdout, body)
	case output.FormatYAML:
		return output.WriteJSONAsYAML(o.stdout, body)
	case output.FormatName:
		_, err := fmt.Fprintf(o.stdout, "rule-merge/%d\n", id)
		return err
	}
	return nil
}

// mergeRuleNamer returns the instance name of a rule of rules by UUID, "" for
// a UUID not among them.
func mergeRuleNamer(rules []dataset_rules.DatasetRuleTiny) func(string) string {
	names := make(map[string]string, len(rules))
	for _, rule := range rules {
		names[strings.ToLower(rule.ID.String())] = rule.InstanceName
	}
	return func(id string) string { return names[strings.ToLower(id)] }
}

// writeMergeResolutions writes the RESOLUTIONS section: one row per
// resolution, in the order sent, with its item, source rule, choice and the
// target rule it pairs with.
func writeMergeResolutions(w io.Writer, items []rule_merges.Item, resolutions []rule_merges.Resolution, names rule_merges.RenderNames) error {
	byID := make(map[int]rule_merges.Item, len(items))
	for _, item := range items {
		byID[item.ID] = item
	}
	var b bytes.Buffer
	b.WriteString("\nRESOLUTIONS\n")
	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ITEM\tSOURCE RULE\tCHOICE\tTARGET RULE")
	for _, r := range resolutions {
		item := byID[r.ItemID]
		target := item.TargetObjectID
		if r.TargetRuleID != nil {
			id := r.TargetRuleID.String()
			target = &id
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\n", r.ItemID,
			mergeRuleCell(names.SourceRule, item.SourceObjectID),
			mergeChoiceCell(r),
			mergeRuleCell(names.TargetRule, target))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	_, err := w.Write(b.Bytes())
	return err
}

// mergeChoiceCell is the resolution's choice, followed for edit_source by the
// keys it edits; a row filter sent as null reads "row_filter removed".
func mergeChoiceCell(r rule_merges.Resolution) string {
	var edits []string
	if r.ColumnMapping != nil {
		edits = append(edits, "column_mapping")
	}
	if r.Params != nil {
		edits = append(edits, "params")
	}
	if r.Severity != nil {
		edits = append(edits, "severity")
	}
	switch {
	case r.RowFilter != nil:
		edits = append(edits, "row_filter")
	case r.RowFilterSet:
		edits = append(edits, "row_filter removed")
	}
	choice := mergeCell(string(r.Choice))
	if len(edits) == 0 {
		return choice
	}
	return choice + " (" + strings.Join(edits, ", ") + ")"
}

// mergeRuleCell names the rule id by namer, falling back to its SHORT-ID; "-"
// without an id.
func mergeRuleCell(namer func(string) string, id *string) string {
	if id == nil || *id == "" {
		return "-"
	}
	if namer != nil {
		if name := namer(*id); name != "" {
			return mergeCell(name)
		}
	}
	return mergeCell(rule_versions.ShortID(*id))
}

// mergeCellBreaks are the characters mergeCell shows as a space.
var mergeCellBreaks = strings.NewReplacer("\n", " ", "\t", " ", " ", " ", " ", " ")

// mergeCell sanitizes s for a terminal and keeps it to one line.
func mergeCell(s string) string {
	return mergeCellBreaks.Replace(terminal.Sanitize(s))
}

// writeMergeSkeleton writes the UNRESOLVED CONFLICTS section: the conflicts
// with no resolution, and a fill-in RuleMergeResolution document with one
// entry for each. extending says the command was given a document already.
func writeMergeSkeleton(w io.Writer, unresolved []rule_merges.Item, names rule_merges.RenderNames, extending bool) {
	fmt.Fprintf(w, "\nUNRESOLVED CONFLICTS\n%s no resolution: %s\n", mergeConflictCount(len(unresolved)),
		mergeConflictLabels(unresolved, names))
	if extending {
		fmt.Fprint(w, "Add the entries below to the -f document's spec.resolutions and set each choice\n"+
			"(and each target_rule).\n\n")
	} else {
		fmt.Fprint(w, "Save the document below, set each choice (and each target_rule), and pass it\n"+
			"with -f.\n\n")
	}
	fmt.Fprint(w, terminal.Sanitize(rule_merges.Skeleton(unresolved, names.SourceRule, names.TargetRule)))
}

// unresolvedConflictsError is the exit-2 refusal to apply with conflicts that
// have no resolution.
func unresolvedConflictsError(unresolved []rule_merges.Item, names rule_merges.RenderNames) error {
	return qerrors.NewBadUsagef("%s no resolution: %s\n\n"+
		"Fill in the RuleMergeResolution document printed after the plan and pass it with -f.",
		mergeConflictCount(len(unresolved)), mergeConflictLabels(unresolved, names))
}

// mergeConflictCount is "1 conflict has" or "N conflicts have".
func mergeConflictCount(n int) string {
	if n == 1 {
		return "1 conflict has"
	}
	return fmt.Sprintf("%d conflicts have", n)
}

// mergeConflictLabels names each item by its source rule and item id.
func mergeConflictLabels(items []rule_merges.Item, names rule_merges.RenderNames) string {
	labels := make([]string, len(items))
	for i, item := range items {
		labels[i] = fmt.Sprintf("%s (item %d)", mergeRuleCell(names.SourceRule, item.SourceObjectID), item.ID)
	}
	return strings.Join(labels, ", ")
}

// mergeApplyPhrase says what applying merge with resolutions does:
// "apply rule merge 7 into orders (id 42): 1 rule created, 0 replaced, 2
// conflicts resolved", with the created and replaced items it declines last.
func mergeApplyPhrase(merge *rule_merges.RuleMerge, resolutions []rule_merges.Resolution, tables mergeTables) string {
	declines := map[int]bool{}
	for _, r := range resolutions {
		if r.Choice == api.DatasetCopyConflictChoiceKeepTarget {
			declines[r.ItemID] = true
		}
	}
	var created, replaced, conflicts, declined int
	for _, item := range merge.Items {
		switch item.Action {
		case api.DatasetCopyItemActionCreated, api.DatasetCopyItemActionReplaced:
			switch {
			case declines[item.ID]:
				declined++
			case item.Action == api.DatasetCopyItemActionCreated:
				created++
			default:
				replaced++
			}
		case api.DatasetCopyItemActionConflict:
			conflicts++
		}
	}
	phrase := fmt.Sprintf("apply rule merge %d into %s: %s created, %d replaced, %s resolved",
		merge.ID, mergeCell(tables.targetLabel()), mergePlural(created, "rule", "rules"), replaced,
		mergePlural(conflicts, "conflict", "conflicts"))
	if declined > 0 {
		phrase += fmt.Sprintf(", %d declined", declined)
	}
	return phrase
}

// mergePlural is "n singular" for 1 and "n plural" otherwise.
func mergePlural(n int, singular, plural string) string {
	if n == 1 {
		return "1 " + singular
	}
	return fmt.Sprintf("%d %s", n, plural)
}

// capitalize upper-cases the first byte of an ASCII phrase.
func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// writeMergeAccepted writes the result line of an accepted apply, then, unless
// the command waits, the line that reads the merge back.
func writeMergeAccepted(w io.Writer, applied *rule_merges.RuleMerge, describe string, waiting bool) {
	fmt.Fprintf(w, "\nrule-merge/%d %s\n", applied.ID, mergeCell(string(applied.State)))
	if !waiting {
		fmt.Fprintf(w, "Next: %s\n", describe)
	}
}
