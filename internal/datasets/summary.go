package datasets

import (
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/pkg/tableui"
	"github.com/qlustered/qctl/internal/pkg/timeutil"
	"github.com/qlustered/qctl/internal/rule_versions"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// DatasetRuleTiny is the rule list item BuildColumnRows joins against.
//
// It aliases the generated schema directly rather than reaching for
// internal/dataset_rules' alias of the same type: this package must not import
// the domain clients (cmdutil already imports it, and the fan-out that calls
// both lives in the command package). Because both are aliases of
// api.DatasetRuleTinySchema they are the same type, so a
// dataset_rules.GetDatasetRules result assigns straight across.
type DatasetRuleTiny = api.DatasetRuleTinySchema

// ColumnRow pairs one schema field with the table rules bound to it. It is the
// line that carries the most information per row in `describe table`: "what
// can I validate here" and "what already validates it", answered together.
//
// Rules stays a slice so -o json|yaml keeps it structured; the text grid joins
// it.
//
// RulesUnavailable marks a row whose Rules is unknown rather than known-empty:
// the rules fetch failed, so this row cannot claim "nothing validates this
// column". The slice itself cannot carry that difference — gopkg.in/yaml.v3
// encodes both a nil and an empty []string as `rules: []` — so the flag is an
// explicit field, and it is what the text grid renders "?" from.
//
// ProducedBy is set when the column's definition carries the server's
// rule_output stamp.
type ColumnRow struct {
	Column           string          `json:"column" yaml:"column"`
	Type             string          `json:"type" yaml:"type"`
	Nullable         string          `json:"nullable" yaml:"nullable"`
	Action           string          `json:"action,omitempty" yaml:"action,omitempty"`
	Rules            []string        `json:"rules" yaml:"rules"`
	RulesUnavailable bool            `json:"rules_unavailable,omitempty" yaml:"rules_unavailable,omitempty"`
	ProducedBy       *ColumnProducer `json:"produced_by,omitempty" yaml:"produced_by,omitempty"`
}

// ColumnProducer is the rule whose output a column is. Rule is the rule's
// instance name, empty when the fetched rule list does not contain RuleID.
type ColumnProducer struct {
	RuleID string `json:"rule_id" yaml:"rule_id"`
	Rule   string `json:"rule,omitempty" yaml:"rule,omitempty"`
}

// BuildColumnRows joins a table's schema to its rules.
//
// A rule binds to a column when its dataset_columns names it. Two cases must
// not vanish silently: the backend leaves dataset_columns nil for a rule that
// reads no column (a row-count check), and it can name a column the current
// schema no longer has (a removed field, or a rule staged against a draft).
// Both come back in unbound, to be listed under the grid, rather than being
// dropped — a table whose rule list is quietly shorter than `get table-rules`
// says is worse than a slightly busier view.
//
// action is left blank when no draft exists: SchemaFieldsToDisplay fills every
// row with "unchanged" regardless, which is a column of noise on a settled
// schema.
//
// rulesUnavailable says the rules fetch failed rather than returned nothing.
// Every row is then stamped RulesUnavailable and no unbound list is produced:
// an empty join and an unread one must not render the same, in the grid or in
// -o json|yaml.
//
// ProducedBy is read from the same definition the row's type comes from, and
// named from rules by ID.
func BuildColumnRows(detail *TableSchemaDetail, rules []DatasetRuleTiny, rulesUnavailable bool) (rows []ColumnRow, unbound []string) {
	var fields []SchemaFieldDisplay
	draft := false
	if detail != nil {
		fields = SchemaFieldsToDisplay(detail)
		draft = detail.MigrationState != api.CurrentOnly
	}

	inSchema := make(map[string]bool, len(fields))
	for _, f := range fields {
		inSchema[f.Field] = true
	}

	byColumn := map[string][]string{}
	nameByID := make(map[string]string, len(rules))
	for _, r := range rules {
		nameByID[r.ID.String()] = r.InstanceName
		bound := false
		if r.DatasetColumns != nil {
			for _, col := range *r.DatasetColumns {
				if !inSchema[col] {
					continue
				}
				byColumn[col] = append(byColumn[col], r.InstanceName)
				bound = true
			}
		}
		if !bound {
			unbound = append(unbound, r.InstanceName)
		}
	}
	sort.Strings(unbound)

	rows = make([]ColumnRow, 0, len(fields))
	for _, f := range fields {
		names := byColumn[f.Field]
		sort.Strings(names)
		row := ColumnRow{
			Column:           f.Field,
			Type:             f.Type,
			Nullable:         f.Nullable,
			Rules:            names,
			RulesUnavailable: rulesUnavailable,
		}
		if draft {
			row.Action = f.Action
		}
		if id := ruleOutputOwner(displayDefinition(detail.MigrationState, detail.Fields[f.Field])); id != "" {
			row.ProducedBy = &ColumnProducer{RuleID: id, Rule: nameByID[strings.ToLower(id)]}
		}
		rows = append(rows, row)
	}
	return rows, unbound
}

// ruleOutputOwner returns the dataset_rule_id of a definition's rule_output
// generated_by stamp, or "" when it carries none.
func ruleOutputOwner(def *FieldSchemaDetail) string {
	if def == nil || def.GeneratedBy == nil {
		return ""
	}
	stamp := *def.GeneratedBy
	if kind, _ := stamp["type"].(string); kind != "rule_output" {
		return ""
	}
	id, _ := stamp["dataset_rule_id"].(string)
	return id
}

// SchemaSection is the state of a table's schema. Unavailable, here and on
// every other section, carries why the fetch failed: `describe table` spans
// four backend permissions (DATASET_READ, MIGRATION_LIST, RULE_LIST,
// DATASET_DATA_READ), so a reader who holds only the first must still get the
// rest of the view rather than one error.
type SchemaSection struct {
	Unavailable string `json:"unavailable,omitempty" yaml:"unavailable,omitempty"`
	State       string `json:"state,omitempty" yaml:"state,omitempty"`
	Confirmed   *bool  `json:"confirmed,omitempty" yaml:"confirmed,omitempty"`
	TotalFields int    `json:"total_fields" yaml:"total_fields"`
	Added       int    `json:"added" yaml:"added"`
	Modified    int    `json:"modified" yaml:"modified"`
	Removed     int    `json:"removed" yaml:"removed"`
}

// RulesSection counts rule states on the fetched page. Total is the server's
// count; Truncated reports more rules than the page carries. Other counts
// states outside enabled, disabled, error and pending_validation.
type RulesSection struct {
	Unavailable       string   `json:"unavailable,omitempty" yaml:"unavailable,omitempty"`
	Total             int      `json:"total" yaml:"total"`
	Enabled           int      `json:"enabled" yaml:"enabled"`
	Disabled          int      `json:"disabled" yaml:"disabled"`
	ErrorRules        []string `json:"error_rules,omitempty" yaml:"error_rules,omitempty"`
	PendingValidation int      `json:"pending_validation" yaml:"pending_validation"`
	Other             int      `json:"other" yaml:"other"`
	Truncated         bool     `json:"truncated" yaml:"truncated"`
}

// SuggestionsSection counts pending suggestions and blockers of the draft migration.
type SuggestionsSection struct {
	Unavailable string `json:"unavailable,omitempty" yaml:"unavailable,omitempty"`
	Pending     int    `json:"pending" yaml:"pending"`
	Blocking    int    `json:"blocking" yaml:"blocking"`
}

// JobSummary is the most recent job of one kind. Updated is the job's
// updated_at as RFC3339 in UTC.
type JobSummary struct {
	ID      int    `json:"id" yaml:"id"`
	State   string `json:"state" yaml:"state"`
	Updated string `json:"updated" yaml:"updated"`
}

// NewJobSummary returns the JobSummary for a job last updated at updated;
// Updated is empty for the zero time.
func NewJobSummary(id int, state string, updated time.Time) *JobSummary {
	j := &JobSummary{ID: id, State: state}
	if !updated.IsZero() {
		j.Updated = updated.UTC().Format(time.RFC3339)
	}
	return j
}

// JobsSection carries the newest ingestion and profiling jobs, with separate
// lookup failures. RunningIngestion is fetched for a confirmed schema draft.
type JobsSection struct {
	IngestionUnavailable string                `json:"ingestion_unavailable,omitempty" yaml:"ingestion_unavailable,omitempty"`
	ProfilingUnavailable string                `json:"profiling_unavailable,omitempty" yaml:"profiling_unavailable,omitempty"`
	Ingestion            *JobSummary           `json:"ingestion,omitempty" yaml:"ingestion,omitempty"`
	Profiling            *JobSummary           `json:"profiling,omitempty" yaml:"profiling,omitempty"`
	RunningIngestion     *RunningIngestionJobs `json:"running_ingestion,omitempty" yaml:"running_ingestion,omitempty"`
}

// SevereRuleBugBlocker is one unresolved severe_rule_bug alert on this table:
// the backend's crash-loop circuit breaker fired on a rule and the alert holds
// the table's ingestion until that rule is disabled or deleted.
//
// It carries only the id. The alerts list endpoint returns AlertTinySchema,
// which has no dataset_rule_id and no dataset_rule_name (both live on the full
// AlertSchema, internal/api/client.gen.go), so the rule behind a given
// alert is not knowable from the list — ComputeBlockers pairs one with a name
// from RulesSection.ErrorRules only when there is exactly one of each.
type SevereRuleBugBlocker struct {
	AlertID int `json:"alert_id" yaml:"alert_id"`
}

// AlertsSection is a count, not an alert: `qctl get latest-alert` already
// picks the one to act on, including the dependency substitution, and a second
// implementation of that choice here would be one more request and one more
// thing to keep in step.
//
// SevereRuleBug is the one exception, and it earns it: it is the only alert
// type whose cause is a rule this view has already fetched, and naming it is
// the difference between "5 open alerts" and knowing which rule to disable.
// It is empty when the lookup is skipped (no rule in `error` and no open
// alert), when the lookup failed, and when it found nothing.
//
// SevereRuleBugTruncated is RulesSection.Truncated's counterpart for that
// slice: the lookup is bounded by one page, so len(SevereRuleBug) is a floor,
// not a count, once the server reports more rows than came back. Any line
// rendering that length as a definite number has to say so — the whole point
// of this view is to never state what it did not establish.
type AlertsSection struct {
	Unavailable            string                 `json:"unavailable,omitempty" yaml:"unavailable,omitempty"`
	Open                   int                    `json:"open" yaml:"open"`
	SevereRuleBug          []SevereRuleBugBlocker `json:"severe_rule_bug,omitempty" yaml:"severe_rule_bug,omitempty"`
	SevereRuleBugTruncated bool                   `json:"severe_rule_bug_truncated" yaml:"severe_rule_bug_truncated"`
}

// SharedWithSection lists the table's grants.
type SharedWithSection struct {
	Unavailable string         `json:"unavailable,omitempty" yaml:"unavailable,omitempty"`
	Users       []string       `json:"users" yaml:"users"`
	Members     []SharedMember `json:"members,omitempty" yaml:"members,omitempty"`
}

// SharedMember carries a grantee's levels.
type SharedMember struct {
	Email                    string `json:"email" yaml:"email"`
	PermissionLevel          string `json:"permission_level" yaml:"permission_level"`
	EffectivePermissionLevel string `json:"effective_permission_level" yaml:"effective_permission_level"`
}

// QuarantineValidation is the validation-status projection of a table's
// quarantined-row counts.
type QuarantineValidation struct {
	PassedValidation int `json:"passed_validation" yaml:"passed_validation"`
	FailedValidation int `json:"failed_validation" yaml:"failed_validation"`
}

// QuarantineHolds is the hold-state projection of a table's quarantined-row
// counts: the on_hold, approved, waiting_for_approval and held_for_approval
// buckets, and held_for_approval split by approval hold reason, each as the
// server sent it. WaitingForApproval is nil when the server sent no
// waiting_for_approval count.
type QuarantineHolds struct {
	OnHold                  int            `json:"on_hold" yaml:"on_hold"`
	Approved                int            `json:"approved" yaml:"approved"`
	WaitingForApproval      *int           `json:"waiting_for_approval,omitempty" yaml:"waiting_for_approval,omitempty"`
	HeldForApproval         int            `json:"held_for_approval" yaml:"held_for_approval"`
	HeldForApprovalByReason map[string]int `json:"held_for_approval_by_reason,omitempty" yaml:"held_for_approval_by_reason,omitempty"`
}

// TableSummary is `describe table`: what a person needs before acting on a
// table, in one screen. It is also the -o json|yaml payload, which is why
// every field is tagged for both encoders.
type TableSummary struct {
	Name               string          `json:"name" yaml:"name"`
	ID                 int             `json:"id" yaml:"id"`
	State              string          `json:"state" yaml:"state"`
	Destination        string          `json:"destination" yaml:"destination"`
	DestinationID      int             `json:"destination_id" yaml:"destination_id"`
	Location           string          `json:"location" yaml:"location"`
	DataLoadingProcess string          `json:"data_loading_process" yaml:"data_loading_process"`
	MigrationPolicy    string          `json:"migration_policy" yaml:"migration_policy"`
	TableKinds         []string        `json:"table_kinds" yaml:"table_kinds"`
	ColumnConcepts     []ColumnConcept `json:"column_concepts" yaml:"column_concepts"`
	CleanRowsCount     *int            `json:"clean_rows_count" yaml:"clean_rows_count"`
	BadRowsCount       *int            `json:"bad_rows_count" yaml:"bad_rows_count"`
	Series             *SeriesSummary  `json:"series,omitempty" yaml:"series,omitempty"`
	Period             *PeriodSummary  `json:"period,omitempty" yaml:"period,omitempty"`

	// QuarantineByValidation is the validation breakdown of BadRowsCount, or nil
	// when the counter was unavailable or not worth a request.
	//
	// These are STRICT counts — `passes_validation_ IS TRUE` and `IS FALSE`
	// (atlas cettings/cettings/controllers/data_controller/quarantine.py
	// get_quarantine_count_by_status). The bulk-op filter behind
	// `qctl rows … --status failed_validation` is `IS NOT TRUE`, i.e. FALSE or
	// NULL (quarantine.py QuarantineMixin._quarantine_status_sql), so
	// FailedValidation does not count what that action affects. The rendered
	// line does NOT hedge for that: `describe table` reports how the
	// quarantined rows split, not the reach of a later action, and for the
	// split these figures are exact.
	//
	// The two fields therefore need NOT sum to BadRowsCount: a row whose
	// `passes_validation_` is NULL is in neither bucket, and the counter also
	// applies `is_deleted_ = FALSE` (get_quarantine_count_by_status) where
	// BadRowsCount may not. Do not compute one figure from the other two.
	QuarantineByValidation *QuarantineValidation `json:"quarantine_by_validation,omitempty" yaml:"quarantine_by_validation,omitempty"`

	// QuarantineByHold is the hold-state breakdown of BadRowsCount, or nil when
	// the counter was unavailable, not worth a request, or answered without
	// on_hold, approved or held_for_approval.
	//
	// The four buckets are disjoint: keep_in_quarantine_ IS TRUE, IS FALSE, IS
	// NULL in a non-closed submitted data review, and IS NULL outside one with
	// an approval hold reason (atlas quarantine.py
	// QuarantineMixin._quarantine_status_sql, approval_hold.py ApprovalHold),
	// over live rows only (get_quarantine_count_by_status). An undecided row
	// outside a review and with no reason is in none of them, so they need NOT
	// sum to BadRowsCount. Do not compute one figure from the others.
	QuarantineByHold *QuarantineHolds `json:"quarantine_by_hold,omitempty" yaml:"quarantine_by_hold,omitempty"`

	// QuarantineNotLoaded is the count of quarantined rows that are duplicate
	// copies a reviewer did not keep, or nil when the counter was unavailable,
	// not worth a request, or answered without it. It overlaps the
	// QuarantineByHold buckets.
	QuarantineNotLoaded *int `json:"quarantine_not_loaded,omitempty" yaml:"quarantine_not_loaded,omitempty"`

	Schema            SchemaSection          `json:"schema" yaml:"schema"`
	Columns           []ColumnRow            `json:"columns" yaml:"columns"`
	UnboundRules      []string               `json:"unbound_rules,omitempty" yaml:"unbound_rules,omitempty"`
	Rules             RulesSection           `json:"rules" yaml:"rules"`
	Suggestions       SuggestionsSection     `json:"suggestions" yaml:"suggestions"`
	Jobs              JobsSection            `json:"jobs" yaml:"jobs"`
	Alerts            AlertsSection          `json:"alerts" yaml:"alerts"`
	MyPermissionLevel *api.DataSetPermission `json:"my_permission_level" yaml:"my_permission_level"`
	SharedWith        SharedWithSection      `json:"shared_with" yaml:"shared_with"`

	// PiiVisibility is the caller's visibility of the table's personal data,
	// or nil when the table read carried none.
	PiiVisibility *PiiVisibility `json:"pii_visibility" yaml:"pii_visibility"`

	// PiiColumnSources is the table's personal-data columns and why each is
	// one, or nil when the table read carried none.
	PiiColumnSources *[]PiiColumnSource `json:"pii_column_sources" yaml:"pii_column_sources"`

	Blockers []string `json:"blockers" yaml:"blockers"`
}

// ColumnConcept pairs a table column with its assigned concept.
type ColumnConcept struct {
	Column  string `json:"column" yaml:"column"`
	Concept string `json:"concept" yaml:"concept"`
}

// ComputeBlockers derives holds and next commands from the fetched summary.
func (s *TableSummary) ComputeBlockers() {
	s.Blockers = nil
	if s.State != "" && s.State != string(api.DataSetStateActive) {
		s.Blockers = append(s.Blockers, fmt.Sprintf("table state is %s", s.State))
	}
	if s.Schema.Unavailable == "" && s.Schema.State != "" && s.Schema.State != string(api.CurrentOnly) &&
		(s.Schema.Confirmed == nil || !*s.Schema.Confirmed) {
		s.Blockers = append(s.Blockers, fmt.Sprintf(
			"schema draft is waiting for confirmation — qctl describe table-schema %s", s.Name))
	}
	if s.Suggestions.Unavailable == "" && s.Suggestions.Blocking > 0 {
		s.Blockers = append(s.Blockers, fmt.Sprintf(
			"%d pending update/delete rule suggestions block the migration — qctl get rule-suggestions %s",
			s.Suggestions.Blocking, s.Name))
	}
	if s.hasRunningMigrationJobs() {
		s.Blockers = append(s.Blockers, s.Jobs.RunningIngestion.blocker())
	}
	// Before the generic alert count, because a severe_rule_bug alert is one
	// of the alerts that count includes and this is the actionable half of it.
	// Each arm's guard is a named predicate below, and the comment on each
	// predicate is where the reason for every conjunct in it lives — they were
	// split out of this switch only to keep one function's cyclomatic
	// complexity under the linter's ceiling, and not one condition moved.
	switch {
	case s.hasCrashedRule():
		s.Blockers = append(s.Blockers, s.severeRuleBugBlocker())
	case s.hasStrandedSevereRuleBugAlert():
		// D4: the alert outlived its rule's `error` state — the rule was
		// disabled without the alert being resolved, which is the
		// requeue-stranded case retry_requeue exists for. The table is still
		// held, so saying nothing here is the one outcome that misleads.
		s.Blockers = append(s.Blockers, s.strandedSevereRuleBugBlocker())
	}
	if s.Alerts.Unavailable == "" && s.Alerts.Open > 0 {
		s.Blockers = append(s.Blockers, fmt.Sprintf(
			"%d open alerts — qctl get latest-alert --table %s", s.Alerts.Open, s.Name))
	}
}

// HasConfirmedDraft reports a confirmed draft in the fetched schema.
func (s *TableSummary) HasConfirmedDraft() bool {
	return s.Schema.Unavailable == "" &&
		(s.Schema.State == string(api.DraftOnly) || s.Schema.State == string(api.DraftWithCurrent)) &&
		s.Schema.Confirmed != nil && *s.Schema.Confirmed
}

// hasCrashedRule reports that the rules qctl actually read include one in
// `error` state. Guarded on Rules.Unavailable for the same reason every other
// arm of ComputeBlockers is: a 403 on rules is not evidence that no rule
// crashed.
func (s *TableSummary) hasCrashedRule() bool {
	return s.Rules.Unavailable == "" && len(s.Rules.ErrorRules) > 0
}

// NeedsSevereRuleBugLookup reports whether the conditional severe_rule_bug
// alerts request is worth making. It is the fetch gate, and it is exported
// because fetchTableSummary in package describe is its only caller — the point
// of it living here is that the question it asks and the question
// hasStrandedSevereRuleBugAlert asks are edited side by side rather than in two
// packages, which is how they drifted apart in the first place.
//
// The errored-rule half is not redundant with the open-alert half: it survives
// an alerts count that failed. The open-alert half closes what used to be a
// documented hole — a severe_rule_bug alert whose rule is ALREADY disabled
// leaves nothing in `error` (the requeue-stranded case retry_requeue exists
// for) and was never looked up. Neither half is guarded on Alerts.Unavailable
// on purpose: fetchTableSummary assigns Alerts.Open only on the count's success
// branch, so a failed count leaves it at 0 and the guard would be dead code
// that reads like a rule.
//
// THIS GATE IS DELIBERATELY WIDER THAN hasStrandedSevereRuleBugAlert BELOW, and
// the asymmetry is the design — do not "fix" it in either direction:
//
//   - Fetch wide, so nothing is missed. Fetching and then not rendering costs
//     one request against the open-alert count's own filter plus issue_type, and
//     the result is not discarded either way: AlertsSection carries SevereRuleBug
//     into -o json|yaml, so a table whose rules page 403s or truncates would
//     otherwise lose the alert id in JSON on exactly the tables where a human
//     most needs it.
//   - Render narrow, so nothing is claimed without evidence. The stranded line
//     asserts a negative ("though no rule is in error state") and every conjunct
//     on the render gate is the evidence for one word of it.
//
// TestSevereRuleBugGates_RenderImpliesFetch and
// TestSevereRuleBugGates_FetchIsWiderThanRender hold both halves of that: the
// render gate may only fire on data this one went and fetched, and the two must
// not collapse into the same predicate.
func (s *TableSummary) NeedsSevereRuleBugLookup() bool {
	return s.hasCrashedRule() || s.Alerts.Open > 0
}

// hasStrandedSevereRuleBugAlert reports an open severe_rule_bug alert that no
// errored rule accounts for. What that arm is for is documented at its case in
// ComputeBlockers; what follows is why it carries guards hasCrashedRule does
// not.
//
// That arm claims a negative — "no rule is in error state" — so it needs every
// guard arm 1 does not: a Truncated rules page is one page of
// summaryRulePageSize, and page 1 of 200 is no more evidence about rule 201
// than a 403 is about any of them, so an errored rule past the page break
// would make both this claim and its advice wrong (disable_rule would be
// exactly the right move, not a no-op). The alerts guard is the same rule as
// the open-alert count arm at the end of ComputeBlockers, held locally rather
// than inherited from whatever the fetch gate happens to do.
//
// The empty-ErrorRules conjunct is what makes the name true read on its own.
// Inside ComputeBlockers it is redundant — this is the second arm of a switch
// whose first arm is hasCrashedRule, so reaching here with Rules.Unavailable
// empty already means len(ErrorRules) == 0 — but a documented predicate that
// is only true at one call site is the fragility this extraction removed.
func (s *TableSummary) hasStrandedSevereRuleBugAlert() bool {
	return s.Rules.Unavailable == "" && !s.Rules.Truncated && len(s.Rules.ErrorRules) == 0 &&
		s.Alerts.Unavailable == "" && len(s.Alerts.SevereRuleBug) > 0
}

// severeRuleBugBlocker renders the crash-loop hold in whichever shape the
// fetched data supports — one rule with one alert, one rule with no alert,
// several rules with no alert, and a fallback that counts crashed rules and
// blocking alerts independently. The command each shape names is the one that
// actually moves:
// `alert resolve --action disable_rule` when a single alert is identified,
// `get alerts` when the reader has to pick.
//
// Disabling is not the only exit — patching the rule to `disabled` or
// `deleted` through `qctl apply -f` clears the blocker too (atlas
// capi/capi/views/rule_view.py patch_dataset_rule, its
// `if body.state in SEVERE_RULE_BUG_SAFE_EXIT_STATES` branch), though on a table
// with a draft migration that patch is staged as a suggestion first (the
// `draft_migration_for_staging` lookup, then its 202 return) while
// the alert action never is — but one line cannot carry any of that, and the
// alert action is the shorter and the more reliable of the two. `qctl explain
// table-rule` carries the other.
func (s *TableSummary) severeRuleBugBlocker() string {
	names := s.Rules.ErrorRules
	if len(names) == 1 && len(s.Alerts.SevereRuleBug) == 1 {
		return fmt.Sprintf(
			"rule '%s' crashed and is in error state; alert %d blocks ingestion until "+
				"the rule is disabled or deleted — qctl alert resolve %d --action disable_rule",
			names[0], s.Alerts.SevereRuleBug[0].AlertID, s.Alerts.SevereRuleBug[0].AlertID)
	}
	if len(s.Alerts.SevereRuleBug) == 0 && len(names) == 1 {
		return fmt.Sprintf(
			"rule '%s' crashed and is in error state; it blocks ingestion until it is "+
				"disabled or deleted — qctl get alerts --table %s", names[0], s.Name)
	}
	if len(s.Alerts.SevereRuleBug) == 0 {
		return fmt.Sprintf(
			"%d rules crashed and are in error state (%s); they block ingestion until each "+
				"is disabled or deleted — qctl get alerts --table %s",
			len(names), joinRuleNames(names), s.Name)
	}
	// The fallback: at least one alert came back, but not the 1:1 shape above.
	// Both halves are counted independently — one rule can hold several open
	// escalations (atlas resolves severe blockers per rule across data
	// sources, resolve_severe_rule_bug_alerts_for_settings_model in
	// cettings/controllers/alert_operations.py, so a
	// multi-source table is an ordinary shape), and several rules can share a
	// single alert in the list qctl fetched. Neither count may assume its
	// plural. The alert id stays out of it: AlertTinySchema carries no
	// dataset_rule_id, so pairing it with one of several rules would be a
	// claim qctl cannot support.
	rulesPart := fmt.Sprintf("%d rules crashed and are in error state (%s)",
		len(names), joinRuleNames(names))
	subject := "each rule"
	if len(names) == 1 {
		rulesPart = fmt.Sprintf("rule '%s' crashed and is in error state", names[0])
		subject = "it"
	}
	alertsPart := fmt.Sprintf("%d severe_rule_bug alerts block", len(s.Alerts.SevereRuleBug))
	if len(s.Alerts.SevereRuleBug) == 1 {
		alertsPart = "1 severe_rule_bug alert blocks"
	}
	return fmt.Sprintf("%s; %s ingestion until %s is disabled or deleted — qctl get alerts --table %s",
		rulesPart, alertsPart, subject, s.Name)
}

// strandedSevereRuleBugBlocker names an open severe_rule_bug alert that no
// errored rule accounts for. It points at `describe alert` rather than at an
// action: the rule is already out of service, so disable_rule is a no-op here,
// and the actions this alert really offers are filtered by role and by runtime
// checks — the alert's own list is the only honest source (the same rule every
// other arm in this file follows).
func (s *TableSummary) strandedSevereRuleBugBlocker() string {
	if len(s.Alerts.SevereRuleBug) == 1 {
		id := s.Alerts.SevereRuleBug[0].AlertID
		return fmt.Sprintf(
			"alert %d (severe_rule_bug) is open and blocks ingestion, though no rule is "+
				"in error state — qctl describe alert %d", id, id)
	}
	// The count is a floor, not a total, when the lookup came back truncated:
	// the slice is one page, so "20 alerts are open" on a table with 25 of
	// them would be a precise claim that is false. The singular line above
	// needs no such hedge — one alert is never a truncated count.
	count := fmt.Sprintf("%d", len(s.Alerts.SevereRuleBug))
	if s.Alerts.SevereRuleBugTruncated {
		count = "at least " + count
	}
	return fmt.Sprintf(
		"%s severe_rule_bug alerts are open and block ingestion, though no rule is in "+
			"error state — qctl get alerts --table %s",
		count, s.Name)
}

// joinRuleNames keeps the blocker line to one readable width. BLOCKING lines
// are not run through the column truncator, so an unbounded join is the only
// thing here that could produce a 4000-character line.
func joinRuleNames(names []string) string {
	const maxNames = 3
	if len(names) <= maxNames {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(names[:maxNames], ", "), len(names)-maxNames)
}

// summaryGridRow is ColumnRow flattened for tableui, whose column selector
// reads json tags off a flat struct and cannot render a []string.
type summaryGridRow struct {
	Column   string `json:"column"`
	Type     string `json:"type"`
	Nullable string `json:"nullable"`
	Action   string `json:"action"`
	Rules    string `json:"rules"`
}

// RenderTableSummary writes the text view: a header block, what is blocking,
// the columns-and-their-rules grid, and the three commands that answer the
// questions this view deliberately does not.
func RenderTableSummary(cmd *cobra.Command, s *TableSummary) error {
	out := cmd.OutOrStdout()

	fmt.Fprintf(out, "%-13s %s (id: %d)\n", "Table:", terminal.Sanitize(s.Name), s.ID)
	fmt.Fprintf(out, "%-13s %s\n", "State:", terminal.Sanitize(s.State))
	if s.Series != nil && s.Period != nil {
		renderSeriesSummary(out, s.Series, s.Period)
	}
	fmt.Fprintf(out, "%-13s %s (id: %d) — %s\n", "Destination:", terminal.Sanitize(s.Destination), s.DestinationID, terminal.Sanitize(s.Location))
	fmt.Fprintf(out, "%-13s %s\n", "Loading:", terminal.Sanitize(s.DataLoadingProcess))
	fmt.Fprintf(out, "%-13s %s\n", "Policy:", terminal.Sanitize(s.MigrationPolicy))
	fmt.Fprintf(out, "%-13s %s\n", "Table kinds:", terminal.Sanitize(strings.Join(s.TableKinds, ", ")))
	if len(s.ColumnConcepts) > 0 {
		assignments := make([]string, 0, len(s.ColumnConcepts))
		for _, assignment := range s.ColumnConcepts {
			assignments = append(assignments, terminal.Sanitize(assignment.Column)+"="+terminal.Sanitize(assignment.Concept))
		}
		fmt.Fprintf(out, "%-17s %s\n", "Standard fields:", strings.Join(assignments, ", "))
	}
	rows := fmt.Sprintf("%s clean / %s quarantined",
		formatCount(s.CleanRowsCount), formatCount(s.BadRowsCount))
	if q := s.QuarantineByValidation; q != nil {
		// Rendered EXACT, and deliberately unhedged. These are the strict
		// `passes_validation_ IS TRUE` / `IS FALSE` counts
		// (atlas cettings/cettings/controllers/data_controller/quarantine.py
		// get_quarantine_count_by_status), so each figure is an exact count of
		// the predicate it names — which is the question this line asks.
		//
		// What a later `qctl rows … --status failed_validation` would affect is
		// a DIFFERENT question. That filter is `IS NOT TRUE`, so it reaches NULL
		// rows this bucket excludes. `describe table` does not answer that.
		//
		// The caveat that DOES apply here is the opposite one: the two figures
		// need not sum to BadRowsCount. It is documented on the field.
		rows += fmt.Sprintf(" (%d passed validation, %d failed validation)",
			q.PassedValidation, q.FailedValidation)
	}
	fmt.Fprintf(out, "%-13s %s\n", "Rows:", terminal.Sanitize(rows))
	writeQuarantineLines(out, s)
	fmt.Fprintf(out, "%-13s %s\n", "Schema:", terminal.Sanitize(formatSchemaSection(s.Schema)))
	fmt.Fprintf(out, "%-13s %s\n", "Rules:", terminal.Sanitize(formatRulesSection(s.Rules)))
	fmt.Fprintf(out, "%-13s %s\n", "Suggestions:", terminal.Sanitize(FormatSuggestionsSection(s.Suggestions)))
	fmt.Fprintf(out, "%-13s %s\n", "Ingestion:", terminal.Sanitize(formatJob(s.Jobs.Ingestion, s.Jobs.IngestionUnavailable)))
	fmt.Fprintf(out, "%-13s %s\n", "Profiling:", terminal.Sanitize(formatJob(s.Jobs.Profiling, s.Jobs.ProfilingUnavailable)))
	fmt.Fprintf(out, "%-13s %s\n", "Shared with:", terminal.Sanitize(formatSharedWith(s.SharedWith)))
	fmt.Fprintf(out, "%-13s %s\n", "My access:", terminal.Sanitize(s.formatMyPermission()))
	fmt.Fprintf(out, "%-13s %s\n", "PII columns:", terminal.Sanitize(FormatPiiColumnSources(s.PiiColumnSources)))
	fmt.Fprintf(out, "%-13s %s\n", "Personal data:", terminal.Sanitize(FormatPiiVisibility(s.PiiVisibility)))

	if len(s.Blockers) > 0 {
		fmt.Fprintf(out, "\nBLOCKING\n")
		for _, b := range s.Blockers {
			fmt.Fprintf(out, "- %s\n", terminal.Sanitize(b))
		}
	}

	if len(s.Columns) > 0 {
		fmt.Fprintln(out)
		// The grid renderer reads the output flag; pin it to table so the grid
		// cannot be rerouted by whatever the flag holds. Same reason as
		// describe table-schema's default arm.
		_ = cmd.Flags().Set("output", "table")
		rows := make([]summaryGridRow, 0, len(s.Columns))
		draft := false
		for _, c := range s.Columns {
			if c.Action != "" {
				draft = true
			}
			rows = append(rows, summaryGridRow{
				Column: c.Column, Type: c.Type, Nullable: c.Nullable,
				Action: c.Action, Rules: formatRulesCell(c),
			})
		}
		columns := "column,type,nullable,rules"
		if draft {
			columns = "column,type,nullable,action,rules"
		}
		if err := tableui.PrintFromCmd(cmd, rows, columns); err != nil {
			return err
		}
	}

	if len(s.UnboundRules) > 0 {
		fmt.Fprintf(out, "\nRules not bound to a column: %s\n", terminal.Sanitize(strings.Join(s.UnboundRules, ", ")))
	}

	safeName := terminal.Sanitize(s.Name)
	fmt.Fprintf(out, "\n  qctl get table-schema %s   # columns only, one request\n", safeName)
	fmt.Fprintf(out, "  qctl get table-config %s   # settings model, backup, thresholds\n", safeName)
	fmt.Fprintf(out, "  qctl get table %s -o yaml  # the applyable manifest\n", safeName)
	return nil
}

// formatRulesCell joins a row's rules and marks its producer "(produces)",
// in place when the producer is already listed and appended otherwise, by its
// SHORT-ID when the rule list did not name it.
//
// "?" and not a blank cell stands for rules that could not be read: a failed
// rules fetch otherwise renders the same grid as a table with no rules bound to
// anything, and the header line saying why is rows away from the cell.
func formatRulesCell(c ColumnRow) string {
	entries := append([]string(nil), c.Rules...)
	if c.RulesUnavailable {
		entries = []string{"?"}
	}
	p := c.ProducedBy
	if p == nil {
		return strings.Join(entries, ", ")
	}
	ref := p.Rule
	if ref == "" {
		ref = rule_versions.ShortID(p.RuleID)
	}
	marked := ref + " (produces)"
	if i := slices.Index(entries, p.Rule); p.Rule != "" && i >= 0 {
		entries[i] = marked
	} else {
		entries = append(entries, marked)
	}
	return strings.Join(entries, ", ")
}

// writeQuarantineLines writes the Quarantine line when s has a hold breakdown
// and the Not loaded line when s counts a not-loaded copy.
func writeQuarantineLines(out io.Writer, s *TableSummary) {
	if h := s.QuarantineByHold; h != nil {
		fmt.Fprintf(out, "%-13s %s\n", "Quarantine:", terminal.Sanitize(FormatQuarantineHolds(*h)))
	}
	if n := s.QuarantineNotLoaded; n != nil && *n > 0 {
		fmt.Fprintf(out, "%-13s %s\n", "Not loaded:", FormatQuarantineNotLoaded(*n))
	}
}

// FormatQuarantineNotLoaded renders the count of quarantined duplicate copies
// a reviewer did not keep.
func FormatQuarantineNotLoaded(n int) string {
	noun := "copies"
	if n == 1 {
		noun = "copy"
	}
	return fmt.Sprintf("%d duplicate %s not loaded: a reviewer kept another copy", n, noun)
}

// FormatQuarantineHolds renders hold counts and their optional per-reason split.
func FormatQuarantineHolds(h QuarantineHolds) string {
	line := fmt.Sprintf("on_hold=%d approved=%d ", h.OnHold, h.Approved)
	if h.WaitingForApproval != nil {
		line += fmt.Sprintf("waiting_for_approval=%d ", *h.WaitingForApproval)
	}
	line += fmt.Sprintf("held_for_approval=%d", h.HeldForApproval)
	if len(h.HeldForApprovalByReason) == 0 {
		return line
	}
	keys := make([]string, 0, len(h.HeldForApprovalByReason))
	seen := map[string]bool{}
	for _, reason := range api.StringEnumValues["ApprovalHoldReason"] {
		if _, ok := h.HeldForApprovalByReason[reason]; ok {
			keys = append(keys, reason)
			seen[reason] = true
		}
	}
	var others []string
	for reason := range h.HeldForApprovalByReason {
		if !seen[reason] {
			others = append(others, reason)
		}
	}
	sort.Strings(others)
	parts := make([]string, 0, len(h.HeldForApprovalByReason))
	for _, reason := range append(keys, others...) {
		parts = append(parts, fmt.Sprintf("%s=%d", reason, h.HeldForApprovalByReason[reason]))
	}
	return line + " (" + strings.Join(parts, " ") + ")"
}

func formatCount(n *int) string {
	if n == nil {
		return "-"
	}
	return fmt.Sprintf("%d", *n)
}

func unavailable(reason string) string { return fmt.Sprintf("(unavailable: %s)", reason) }

// formatSharedWith renders the shared-with section as ONE line: the header
// block is a fixed-width label per row, and a multi-line value would break the
// alignment every other row depends on. Beyond maxSharedWithShown it
// truncates rather than wrapping.
func formatSharedWith(s SharedWithSection) string {
	if s.Unavailable != "" {
		return unavailable(s.Unavailable)
	}
	if len(s.Members) > 0 {
		s.Users = make([]string, 0, len(s.Members))
		for _, m := range s.Members {
			level := m.PermissionLevel
			if m.EffectivePermissionLevel != m.PermissionLevel {
				level += "; effective " + m.EffectivePermissionLevel
			}
			s.Users = append(s.Users, m.Email+" ("+level+")")
		}
	}
	if len(s.Users) == 0 {
		return "(nobody)"
	}
	if len(s.Users) <= maxSharedWithShown {
		return strings.Join(s.Users, ", ")
	}
	return fmt.Sprintf("%s, +%d more",
		strings.Join(s.Users[:maxSharedWithShown], ", "), len(s.Users)-maxSharedWithShown)
}

// maxSharedWithShown caps the shared-with line so it stays readable.
const maxSharedWithShown = 5

func formatSchemaSection(s SchemaSection) string {
	if s.Unavailable != "" {
		return unavailable(s.Unavailable)
	}
	confirmed := "not confirmed"
	if s.Confirmed != nil && *s.Confirmed {
		confirmed = "confirmed"
	}
	if s.State == string(api.CurrentOnly) {
		return fmt.Sprintf("%s (%s) — %d fields", s.State, confirmed, s.TotalFields)
	}
	return fmt.Sprintf("%s (%s) — %d fields (+%d added, ~%d modified, -%d removed)",
		s.State, confirmed, s.TotalFields, s.Added, s.Modified, s.Removed)
}

func formatRulesSection(s RulesSection) string {
	if s.Unavailable != "" {
		return unavailable(s.Unavailable)
	}
	line := fmt.Sprintf("%d (%d enabled, %d disabled", s.Total, s.Enabled, s.Disabled)
	if n := len(s.ErrorRules); n > 0 {
		line += fmt.Sprintf(", %d error", n)
	}
	if s.PendingValidation > 0 {
		line += fmt.Sprintf(", %d pending validation", s.PendingValidation)
	}
	if s.Other > 0 {
		line += fmt.Sprintf(", %d other", s.Other)
	}
	line += ")"
	if s.Truncated {
		line += " — more than one page; the grid shows the first page only"
	}
	return line
}

// FormatSuggestionsSection renders pending suggestions and migration blockers.
func FormatSuggestionsSection(s SuggestionsSection) string {
	if s.Unavailable != "" {
		return unavailable(s.Unavailable)
	}
	if s.Pending == 0 {
		return "none pending"
	}
	return fmt.Sprintf("%d pending (%d block the migration)", s.Pending, s.Blocking)
}

// formatJob renders one job line from that job's own fetch: the reason is the
// Unavailable field belonging to the same endpoint, never the other one's, so
// "none" here means the fetch succeeded and returned nothing. A job already in
// hand still outranks a reason — the two cannot both be set today, and if a
// later change makes them, what was actually fetched is the honest thing to
// print and keeps this view agreeing with -o json|yaml.
func formatJob(j *JobSummary, reason string) string {
	if j != nil {
		return fmt.Sprintf("job %d %s (%s)", j.ID, j.State, relativeTime(j.Updated))
	}
	if reason != "" {
		return unavailable(reason)
	}
	return "none"
}

// relativeTime renders the RFC3339 timestamp s with timeutil.FormatRelative,
// or returns s unchanged when it does not parse.
func relativeTime(s string) string {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return s
	}
	return timeutil.FormatRelative(t)
}

func (s *TableSummary) formatMyPermission() string {
	if s.SharedWith.Unavailable != "" {
		return unavailable(s.SharedWith.Unavailable)
	}
	if s.MyPermissionLevel != nil {
		return string(*s.MyPermissionLevel)
	}
	return "(none reported)"
}
