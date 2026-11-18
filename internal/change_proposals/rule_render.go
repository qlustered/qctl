package change_proposals //nolint:revive // Established package-path style in this repo (rule_reviews, privilege_requests).

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/qlustered/qctl/internal/dataset_rules"
)

// Impact keys writeRuleImpact renders as lines.
var (
	renderedRuleBound = map[string]bool{
		"operation": true, "target": true, "payload": true, "base_params": true, "params_diff": true,
		"series_rule_approval": true, "dry_run_job_id": true, "rule_errors": true,
		"addressed_alert_ids": true, "addressed_warning_ids": true,
	}
	renderedRuleContext = map[string]bool{"dry_run": true}
)

// writeRuleImpact writes a rule-param proposal's impact: the params change,
// the base params, the series rule approval, and the validation dry run.
func writeRuleImpact(b *strings.Builder, d *Detail) {
	impact := d.Review.Impact
	writeParamsDiff(b, impact.Bound["params_diff"])
	if base, ok := impact.Bound["base_params"].(map[string]interface{}); ok {
		fmt.Fprintf(b, "Base params (the rule's params when this version went to review): %s\n", compactJSON(base))
	} else {
		b.WriteString("Base params: (none recorded — this version has not been submitted)\n")
	}
	writeSeriesApproval(b, impact.Bound["series_rule_approval"], d.ApproveReapprovesSeriesRules)
	writeDryRun(b, d.ID.String(), impact.Context["dry_run"], impact.Bound["rule_errors"])
	rest := map[string]interface{}{}
	for k, v := range impact.Bound {
		if !renderedRuleBound[k] {
			rest[k] = v
		}
	}
	for k, v := range impact.Context {
		if !renderedRuleContext[k] {
			rest[k] = v
		}
	}
	if len(rest) > 0 {
		b.WriteString("Other impact fields:\n")
		WriteYAMLBlock(b, rest)
	}
}

// writeParamsDiff writes params_diff: each param whose value approving
// changes, from the rule's live value to the proposed one.
func writeParamsDiff(b *strings.Builder, v interface{}) {
	diff, ok := v.(map[string]interface{})
	switch {
	case v == nil:
		b.WriteString("Params change: (unknown — the rule is gone or the payload is not a valid rule patch)\n")
		return
	case !ok:
		fmt.Fprintf(b, "Params change: %s\n", compactJSON(v))
		return
	case len(diff) == 0:
		b.WriteString("Params change: (none — the proposed params equal the rule's live params)\n")
		return
	}
	WriteParamsDiff(b, "Params change (the rule's live value -> the proposed one):", diff)
}

// WriteParamsDiff writes header, then a "- key: before -> after" line per
// key of diff in key order, "(unset)" standing for a side the key lacks.
func WriteParamsDiff(b *strings.Builder, header string, diff map[string]interface{}) {
	b.WriteString(header + "\n")
	keys := make([]string, 0, len(diff))
	for k := range diff {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		change, _ := diff[k].(map[string]interface{})
		fmt.Fprintf(b, "- %s: %s -> %s\n", k, sideValue(change, "before"), sideValue(change, "after"))
	}
}

// ParamsDiff is each key whose value differs between before and after
// (dataset_rules.SameParamValue), mapped to its "before" and "after" values;
// a side without the key is left out.
func ParamsDiff(before, after map[string]interface{}) map[string]interface{} {
	diff := map[string]interface{}{}
	for k, v := range before {
		w, ok := after[k]
		if ok && dataset_rules.SameParamValue(v, w) {
			continue
		}
		change := map[string]interface{}{"before": v}
		if ok {
			change["after"] = w
		}
		diff[k] = change
	}
	for k, w := range after {
		if _, ok := before[k]; !ok {
			diff[k] = map[string]interface{}{"after": w}
		}
	}
	return diff
}

func sideValue(change map[string]interface{}, side string) string {
	v, ok := change[side]
	if !ok {
		return "(unset)"
	}
	return compactJSON(v)
}

// writeSeriesApproval writes the series rule approval status and whether
// approving re-records it.
func writeSeriesApproval(b *strings.Builder, v interface{}, reapproves *bool) {
	m, ok := v.(map[string]interface{})
	if !ok {
		b.WriteString("Series rule approval: (unknown — the rule is gone or the payload is not a valid rule patch)\n")
		return
	}
	status, _ := m["status"].(string)
	series, _ := m["series_id"].(string)
	switch status {
	case "not_in_series":
		b.WriteString("Series rule approval: the table is in no series\n")
		return
	case "current":
		fmt.Fprintf(b, "Series rule approval: series %s is approved for its live rule set\n", series)
	case "not_current":
		fmt.Fprintf(b, "Series rule approval: series %s is not approved for its live rule set\n", series)
	default:
		fmt.Fprintf(b, "Series rule approval: %s\n", flatJSON(m))
	}
	switch {
	case reapproves == nil:
	case *reapproves:
		b.WriteString("Approving by you also re-approves the series rule set with the new params\n")
	default:
		b.WriteString("Approving by you does not re-approve the series rule set\n")
	}
}

// writeDryRun writes the proposal's validation dry run and the rule errors
// it recorded.
func writeDryRun(b *strings.Builder, id string, v, ruleErrors interface{}) {
	run, ok := v.(map[string]interface{})
	if !ok {
		fmt.Fprintf(b, "Dry run: (none — approve is blocked until one finishes; stage one with: "+
			"qctl change-proposal rerun-dry-run %s)\n", id)
		return
	}
	line := fmt.Sprintf("Dry run: dry-run job %v — %v", run["id"], run["status"])
	if size, ok := run["sample_size"]; ok && size != nil {
		line += fmt.Sprintf("; sample size %v", size)
	}
	b.WriteString(line + "\n")
	if counts, ok := run["counts"].(map[string]interface{}); ok {
		fmt.Fprintf(b, "  counts (left = live params, right = proposed): %s\n", flatJSON(counts))
	}
	if rates, ok := run["rates"].(map[string]interface{}); ok && len(rates) > 0 {
		fmt.Fprintf(b, "  rates: %s\n", flatJSON(rates))
	}
	fmt.Fprintf(b, "  see it: qctl describe dry-run-job %v\n", run["id"])
	errs, _ := ruleErrors.(map[string]interface{})
	if len(errs) == 0 {
		b.WriteString("Rule errors in the dry run: (none)\n")
		return
	}
	fmt.Fprintf(b, "Rule errors in the dry run: %s\n", flatJSON(errs))
}

// flatJSON renders a map as "k: v, k: v" in key order, each value as JSON.
func flatJSON(m map[string]interface{}) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s: %s", k, compactJSON(m[k]))
	}
	return strings.Join(parts, ", ")
}

func compactJSON(v interface{}) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(raw)
}

// RuleOutcomeKeys are the execution_result keys RuleOutcome renders.
var RuleOutcomeKeys = map[string]bool{
	"outcome": true, "dataset_rule_suggestion_id": true, "application": true, "series_rule_approval": true, "staged_as": true,
}

// seriesRefusals explain each reason an approval left the series rule
// approval unrecorded.
var seriesRefusals = map[string]string{
	"staged":                 "the patch was staged, not applied",
	"not_in_series":          "the table is in no series",
	"approver_role":          "the approver lacks an admin or member role with review:approve",
	"approver_table_access":  "the approver lacks review:approve on every table of the series",
	"no_approval":            "the series has no rule set approval to re-record",
	"approved_params_differ": "the rule's live entry in the series rule set does not carry the proposed params",
	"rule_set_differs":       "the live series rule set differs from the last approved one by more than this patch",
}

// applicationText explains each execution_result.application value.
var applicationText = map[string]string{
	applicationPending: "pending — accept or dismiss the suggestion; until then the addressed alerts and warnings " +
		"stay linked to this proposal",
	applicationApplied:   "applied — the suggestion was accepted",
	applicationDismissed: "dismissed — the suggestion was dismissed or failed, so the patch was not applied",
}

// RuleOutcome is a rule-param proposal's execution result as lines: the
// outcome, the staged suggestion and its application, and the series rule
// approval; a key the result lacks writes no line. The text is not sanitized.
func RuleOutcome(p *Proposal) string {
	if p.ExecutionResult == nil {
		return ""
	}
	result := *p.ExecutionResult
	stagedAs := StagedAs(p)
	var b strings.Builder
	switch outcome := result["outcome"]; outcome {
	case nil:
	case applicationApplied:
		b.WriteString("Outcome: applied — the rule now has the proposed params\n")
	case "staged":
		b.WriteString("Outcome: staged — the patch became a rule suggestion instead of changing the rule")
		if stagedAs == "" {
			b.WriteString("; it applies when the suggestion is accepted")
		}
		b.WriteString("\n")
	default:
		fmt.Fprintf(&b, "Outcome: %v\n", outcome)
	}
	b.WriteString(stagedAs)
	if id := result["dataset_rule_suggestion_id"]; id != nil {
		table := "<table-id>"
		if p.DatasetID != nil {
			table = strconv.Itoa(*p.DatasetID)
		}
		fmt.Fprintf(&b, "Staged suggestion: %v — see it: qctl describe rule-suggestion %v --table-id %s\n", id, id, table)
	}
	if app, present := result["application"]; present {
		text, known := applicationText[fmt.Sprint(app)]
		if stagedAs != "" {
			text, known = fmt.Sprint(app), true
		}
		if !known {
			text = compactJSON(app)
		}
		fmt.Fprintf(&b, "Suggestion application: %s\n", text)
	}
	if v, present := result["series_rule_approval"]; present {
		fmt.Fprintf(&b, "Series rule approval: %s\n", seriesApprovalResult(v))
	}
	return b.String()
}

// seriesApprovalResult renders series_rule_approval: {id} when the approval
// re-recorded it, {id: null, reason} when it did not.
func seriesApprovalResult(v interface{}) string {
	m, ok := v.(map[string]interface{})
	if !ok {
		return compactJSON(v)
	}
	if id := m["id"]; id != nil {
		return fmt.Sprintf("re-recorded as approval %v, for the series rule set with the new params", id)
	}
	reason := fmt.Sprint(m["reason"])
	if text, known := seriesRefusals[reason]; known {
		return fmt.Sprintf("not re-recorded — %s (%s)", reason, text)
	}
	return "not re-recorded — " + compactJSON(m["reason"])
}

// RuleTargetState renders a rule target_state, {state, params}, or null
// when the rule is gone.
func RuleTargetState(v interface{}) string {
	m, ok := v.(map[string]interface{})
	switch {
	case v == nil:
		return "gone"
	case !ok:
		return compactJSON(v)
	}
	return fmt.Sprintf("%v, params %s", m["state"], compactJSON(m["params"]))
}
