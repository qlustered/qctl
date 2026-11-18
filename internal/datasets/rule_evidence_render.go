package datasets

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/dataview"
	"github.com/qlustered/qctl/internal/pkg/timeutil"
	"github.com/qlustered/qctl/internal/rule_versions"
	"github.com/qlustered/qctl/internal/terminal"
)

// RowEvidenceOptions controls how much of one row's evidence is rendered.
//
// ShowAllVerdicts is -v. Off, the verdict table lists only the manifest entries
// that did something to the row and counts the rest; on, it lists every entry
// the manifest carried, in position order.
type RowEvidenceOptions struct {
	ShowAllVerdicts bool
}

// quietVerdicts are the outcomes the default view summarises rather than lists:
// the rule ran and raised nothing, and the row was quarantined by cleaning
// before the validation gate so no rule saw it (atlas common/common/
// types_rule.py RuleVerdict).
//
// These are Python enum MEMBER NAMES arriving in a plain `str` field — the spec
// declares no enum for them, so there is no generated constant to key on. The
// default is therefore chosen to fail safe: an unrecognised verdict is LISTED,
// so a rename in atlas makes rows appear that used to be summarised, never the
// other way round.
var quietVerdicts = map[string]bool{
	"passed":        true,
	"not_evaluated": true,
}

// RenderRowEvidence writes the agent-oriented payload for one row's rule
// evidence, mirroring comment_threads.RenderThread.
//
// aliaser maps a dataset-rule UUID to its r-* alias and must not be nil: the
// alias is the identifier `qctl describe table-rule` takes, and rendering the
// raw UUID instead would make the same rule read differently here and in
// `get data --include-dataset-rule-ids`. An allocation failure fails the
// render rather than silently falling back, so an alias never means two rules.
func RenderRowEvidence(w io.Writer, ev *RowRuleEvidence, aliaser dataview.RuleAliaser, opts RowEvidenceOptions) error {
	if ev == nil {
		return fmt.Errorf("nil evidence")
	}
	if aliaser == nil {
		return fmt.Errorf("no rule aliaser provided")
	}

	var b strings.Builder
	writeEvidenceHeader(&b, ev)
	aliasCache := map[string]string{}
	for i := range ev.Results {
		if err := writeEvidenceRun(&b, &ev.Results[i], ev.RowID, aliaser, aliasCache, opts); err != nil {
			return err
		}
	}
	_, err := io.WriteString(w, terminal.Sanitize(b.String()))
	return err
}

func writeEvidenceHeader(b *strings.Builder, ev *RowRuleEvidence) {
	fmt.Fprintf(b, "# ROW %d RULE EVIDENCE\n", ev.RowID)
	fmt.Fprintf(b, "Table: %d\n", ev.DatasetID)
	fmt.Fprintf(b, "Initial signature: %s\n", stringOrDash(ev.InitialSignature))
	fmt.Fprintf(b, "Runs: %d", len(ev.Results))
	if len(ev.Results) > 0 {
		b.WriteString(" (newest first)")
	}
	b.WriteString("\n")
	if len(ev.Results) == 0 {
		b.WriteString("\nNo rule run has recorded a verdict for this row.\n")
	}
}

// writeEvidenceRun renders one run: its identity, then its verdicts.
//
// requestedRowID is the id the caller asked about. A run whose own row_id
// differs recorded its verdicts under a PREVIOUS incarnation of this row — a
// composite recompute tombstones and resurrects a row under a new id but keeps
// its initial_signature_, which is what widened the read to reach this run — so
// the older id is named rather than quietly folded into the new one.
func writeEvidenceRun(
	b *strings.Builder,
	run *api.RuleRunEvidenceSchema,
	requestedRowID int,
	aliaser dataview.RuleAliaser,
	aliasCache map[string]string,
	opts RowEvidenceOptions,
) error {
	fmt.Fprintf(b, "\n## RUN %d — %s, evaluated %s\n",
		run.RuleRunID, run.RunKind, evidenceTime(run.EvaluatedAt))
	fmt.Fprintf(b, "Started: %s\n", evidenceTime(run.StartedAt))
	fmt.Fprintf(b, "Jobs: %s\n", evidenceJobs(run))
	fmt.Fprintf(b, "Manifest: %s (sha256 %s)\n", run.ManifestID, run.ManifestSha256)
	if run.RowID != requestedRowID {
		fmt.Fprintf(b, "Recorded under row id %d (this row's earlier incarnation; same initial signature)\n", run.RowID)
	}

	listed, hidden := splitVerdicts(run.Verdicts, opts.ShowAllVerdicts)
	fmt.Fprintf(b, "Verdicts: %s\n", verdictSummary(run.Verdicts))
	if err := writeVerdictTable(b, listed, aliaser, aliasCache); err != nil {
		return err
	}
	if hidden > 0 {
		fmt.Fprintf(b, "\n%d rule(s) with no effect on this row are not listed; -v lists every manifest entry.\n", hidden)
	}
	writeUnattributed(b, run)
	return nil
}

// evidenceJobs names the pipeline job behind the run. The three id fields are
// mutually exclusive in practice but are three independent nullable columns on
// the wire, so all that are set are printed rather than the first.
func evidenceJobs(run *api.RuleRunEvidenceSchema) string {
	var parts []string
	for _, j := range []struct {
		label string
		id    *int
	}{
		{"ingestion", run.IngestionJobID},
		{"dry-run", run.DryRunJobID},
		{"composite-run", run.CompositeRunJobID},
	} {
		if j.id != nil {
			parts = append(parts, j.label+" "+strconv.Itoa(*j.id))
		}
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ", ")
}

// splitVerdicts partitions a run's entries into the ones the view lists and a
// count of the ones it summarises instead.
func splitVerdicts(verdicts []api.RuleVerdictEvidenceSchema, showAll bool) ([]api.RuleVerdictEvidenceSchema, int) {
	if showAll {
		return verdicts, 0
	}
	listed := make([]api.RuleVerdictEvidenceSchema, 0, len(verdicts))
	hidden := 0
	for _, v := range verdicts {
		if v.Verdict != nil && quietVerdicts[*v.Verdict] {
			hidden++
			continue
		}
		listed = append(listed, v)
	}
	return listed, hidden
}

// verdictSummary counts a run's entries by verdict. Names appear in the order
// they first occur in the manifest, so the same run always summarises the same
// way — Go's map iteration order would not.
func verdictSummary(verdicts []api.RuleVerdictEvidenceSchema) string {
	if len(verdicts) == 0 {
		return "0 configured rules"
	}
	counts := map[string]int{}
	var order []string
	for _, v := range verdicts {
		name := verdictName(v)
		if _, seen := counts[name]; !seen {
			order = append(order, name)
		}
		counts[name]++
	}
	parts := make([]string, 0, len(order))
	for _, name := range order {
		parts = append(parts, fmt.Sprintf("%d %s", counts[name], name))
	}
	return fmt.Sprintf("%d configured rules — %s", len(verdicts), strings.Join(parts, ", "))
}

// verdictName is the display name for one entry's outcome.
//
// The two null cases are different failures and are spelled differently. Both
// null: the run's stored verdict vector was shorter than the manifest, so
// nothing was recorded at that position. verdict null with a verdict_code:
// the code is one this build of the server has no name for, which means the
// vector was written by a newer build — the number is printed so the reader
// can look it up rather than losing it.
func verdictName(v api.RuleVerdictEvidenceSchema) string {
	if v.Verdict != nil && *v.Verdict != "" {
		return *v.Verdict
	}
	if v.VerdictCode != nil {
		return fmt.Sprintf("unnamed code %d", *v.VerdictCode)
	}
	return "unrecorded"
}

func writeVerdictTable(
	b *strings.Builder,
	verdicts []api.RuleVerdictEvidenceSchema,
	aliaser dataview.RuleAliaser,
	aliasCache map[string]string,
) error {
	if len(verdicts) == 0 {
		return nil
	}
	headers := []string{"Pos", "Rule", "Instance", "Family", "Revision", "Verdict", "Issues"}
	b.WriteString("\n| " + strings.Join(headers, " | ") + " |\n")
	seps := make([]string, len(headers))
	for i := range seps {
		seps[i] = "---"
	}
	b.WriteString("|" + strings.Join(seps, "|") + "|\n")

	for _, v := range verdicts {
		alias, err := ruleAlias(v.DatasetRuleID, aliaser, aliasCache)
		if err != nil {
			return err
		}
		cols := []string{
			strconv.Itoa(v.Position),
			alias,
			evidenceCell(v.InstanceName),
			evidenceCell(v.FamilySlug),
			revisionCell(v.RuleRevisionID),
			verdictCell(v),
			verdictIssues(v),
		}
		b.WriteString("| " + strings.Join(cols, " | ") + " |\n")
	}
	return nil
}

// ruleAlias maps one manifest entry's dataset_rule_id to its r-* alias.
//
// An entry with no dataset_rule_id is rendered "-" and never allocated: an
// alias is a permanent, ordinal-numbered handle, and burning one on an empty
// string would leave `qctl cache resolve r-N` pointing at nothing.
func ruleAlias(datasetRuleID string, aliaser dataview.RuleAliaser, cache map[string]string) (string, error) {
	if datasetRuleID == "" {
		return "-", nil
	}
	if alias, ok := cache[datasetRuleID]; ok {
		return alias, nil
	}
	alias, err := aliaser.Alias(datasetRuleID)
	if err != nil {
		return "", fmt.Errorf("failed to allocate alias for rule %s: %w", datasetRuleID, err)
	}
	cache[datasetRuleID] = alias
	return alias, nil
}

// revisionCell is the rule revision's SHORT ID — the same 8-hex form the
// short-ID column of `qctl get rule-revisions` shows and `qctl get rule
// <short>` takes.
func revisionCell(id string) string {
	if id == "" {
		return "-"
	}
	return rule_versions.ShortID(id)
}

// verdictCell names the outcome and, when a steward's row-rule ignore dropped
// what the rule raised, the suppression that did it.
func verdictCell(v api.RuleVerdictEvidenceSchema) string {
	name := verdictName(v)
	if v.SuppressionHistoryID != nil {
		name += fmt.Sprintf(" (ignore %d)", *v.SuppressionHistoryID)
	}
	return name
}

// verdictIssues renders the alert and warning tokens this entry raised, in the
// same [A<id>]/[W<id>] form `get data` puts on a cell.
func verdictIssues(v api.RuleVerdictEvidenceSchema) string {
	return dataview.RenderIssueRefs(evidenceRefs(v.AlertItemTokens, v.WarningItemTokens))
}

func evidenceRefs(alerts, warnings *[]string) []dataview.IssueRef {
	var refs []dataview.IssueRef
	if alerts != nil {
		refs = append(refs, dataview.ParseIssueTokens(dataview.IssueAlert, *alerts)...)
	}
	if warnings != nil {
		refs = append(refs, dataview.ParseIssueTokens(dataview.IssueWarning, *warnings)...)
	}
	return refs
}

// writeUnattributed names the row's tokens that no entry in this run's manifest
// claimed: the alert or warning is on the row, but the item it came from no
// longer names a dataset rule (or no longer exists at all). They are printed
// rather than dropped — a row can be quarantined by an issue this table cannot
// attribute, and a view that hid them would report fewer issues than the row
// has.
func writeUnattributed(b *strings.Builder, run *api.RuleRunEvidenceSchema) {
	refs := evidenceRefs(run.UnattributedAlertTokens, run.UnattributedWarningTokens)
	if len(refs) == 0 {
		return
	}
	fmt.Fprintf(b, "\nUnattributed issues (no manifest entry claimed them): %s\n",
		dataview.RenderIssueRefs(refs))
}

// evidenceCell is dataview.EscapeCell with an empty value spelled "-", so a
// blank manifest field cannot read as a missing table column.
func evidenceCell(s string) string {
	if s == "" {
		return "-"
	}
	return dataview.EscapeCell(s)
}

// evidenceTime prints an absolute timestamp with the relative form beside it.
// The relative form alone is what the rest of the CLI shows, but this view
// answers an audit question — "on this date, why was this row treated this
// way" — and "2 hours ago" is not a date.
func evidenceTime(t time.Time) string {
	abs := t.Format(time.RFC3339)
	if rel := timeutil.FormatRelative(t); rel != "" {
		return abs + " (" + rel + ")"
	}
	return abs
}

func stringOrDash(s *string) string {
	if s == nil || *s == "" {
		return "-"
	}
	return *s
}

// RevisionIDs is every distinct rule revision the evidence names, in the order
// they appear. `describe row` hands these to the short-ID cache so a revision
// the user was just shown resolves without listing the catalog.
func RevisionIDs(ev *RowRuleEvidence) []string {
	if ev == nil {
		return nil
	}
	seen := map[string]bool{}
	var ids []string
	for i := range ev.Results {
		for _, v := range ev.Results[i].Verdicts {
			if v.RuleRevisionID == "" || seen[v.RuleRevisionID] {
				continue
			}
			seen[v.RuleRevisionID] = true
			ids = append(ids, v.RuleRevisionID)
		}
	}
	return ids
}
