package describe

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/qlustered/qctl/internal/alerts"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/dataset_rules"
	"github.com/qlustered/qctl/internal/datasets"
	"github.com/qlustered/qctl/internal/ingestion"
	"github.com/qlustered/qctl/internal/profiling"
	"github.com/qlustered/qctl/internal/rule_suggestions"
	"github.com/spf13/cobra"
)

// summaryRulePageSize bounds the rules fetch. A table with more rules than
// this still renders; RulesSection.Truncated says the grid saw one page.
const summaryRulePageSize = 200

// summarySevereAlertPageSize bounds the severe_rule_bug lookup. A table with
// more than this many independently crashed rules is not a table anyone is
// triaging from a summary; the generic open-alert count still carries the rest.
const summarySevereAlertPageSize = 20

// fetchTableSummary assembles `describe table`, retaining unavailable sections.
//
//nolint:gocyclo // One linear fan-out over eight sections; the ordering and the shared soft-degrade contract between them are exactly what a reader needs to see in one place, so it stays a single sequence rather than eight helpers that would have to be read together anyway.
func fetchTableSummary(cmd *cobra.Command, ctx *cmdutil.CommandContext, tableID int) (*datasets.TableSummary, error) {
	token := ctx.Credential.AccessToken
	warn := func(section string, err error) string {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s unavailable: %v\n", section, err)
		// The stored value renders inline in an aligned single-line header
		// ("Rules:        (unavailable: ...)") and round-trips through -o
		// json|yaml, so it must stay one line even though err.Error() — an
		// apierror.OperationalError.Format() for a real API failure — joins
		// title, message, "(Code: X)" and an auth hint with "\n". The full
		// multi-line message above, on stderr, is where that detail belongs.
		return strings.ReplaceAll(err.Error(), "\n", "; ")
	}

	dsClient := datasets.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	table, err := dsClient.GetDataset(token, tableID)
	if err != nil {
		return nil, fmt.Errorf("failed to get table: %w", err)
	}

	s := &datasets.TableSummary{
		TableKinds:         []string{},
		ColumnConcepts:     []datasets.ColumnConcept{},
		Name:               table.Name,
		ID:                 table.ID,
		State:              string(table.State),
		Destination:        table.DestinationName,
		DestinationID:      table.DestinationID,
		Location:           fmt.Sprintf("%s.%s.%s", table.DatabaseName, table.SchemaName, table.TableName),
		DataLoadingProcess: string(table.DataLoadingProcess),
		MigrationPolicy:    string(table.MigrationPolicy),
		CleanRowsCount:     table.CleanRowsCount,
		BadRowsCount:       table.BadRowsCount,
		PiiVisibility:      datasets.NewPiiVisibility(table.PiiVisibility),
	}
	if sources := datasets.NewPiiColumnSources(table.PiiColumnSources); sources != nil {
		s.PiiColumnSources = &sources
	}
	s.Series, s.Period = datasets.SummarizeSeries(table.Series, table.Period)
	if table.ProblemDomains != nil {
		slugs := make([]string, 0, len(*table.ProblemDomains))
		for _, domain := range *table.ProblemDomains {
			slugs = append(slugs, domain.Slug)
		}
		s.TableKinds = slugs
	}
	if table.ColumnConcepts != nil {
		for _, assignment := range *table.ColumnConcepts {
			s.ColumnConcepts = append(s.ColumnConcepts, datasets.ColumnConcept{Column: assignment.DatasetCol, Concept: assignment.Concept.Slug})
		}
	}

	// Conditional, and only when there is something to break down. A table with
	// no quarantined rows would get "(0 passed validation, 0 failed validation)"
	// for the cost of a round trip.
	//
	// warn's return value is the string the other sections park on their
	// section struct; these two have nowhere to park it — each breakdown renders
	// only when it exists — so the stderr line is the whole of the degrade, and
	// nil QuarantineByValidation / QuarantineByHold are the render gates.
	if table.BadRowsCount != nil && *table.BadRowsCount > 0 {
		if counts, err := dsClient.GetQuarantineCountByStatus(token, tableID, datasets.QuarantineCountScope{}); err != nil {
			warn("quarantine counts", err)
		} else {
			s.QuarantineByValidation = &datasets.QuarantineValidation{
				PassedValidation: intOrZero(counts.PassedValidation),
				FailedValidation: intOrZero(counts.FailedValidation),
			}
			s.QuarantineByHold = quarantineHolds(counts)
			s.QuarantineNotLoaded = counts.NotLoaded
		}
	}

	var detail *datasets.TableSchemaDetail
	if detail, err = dsClient.GetSchemaDetails(token, tableID); err != nil {
		s.Schema.Unavailable = warn("schema", err)
		detail = nil
	} else {
		confirmed := detail.Confirmed
		s.Schema = datasets.SchemaSection{
			State:       string(detail.MigrationState),
			Confirmed:   &confirmed,
			TotalFields: len(detail.Fields),
		}
		if detail.DiffSummary.TotalFields != nil {
			s.Schema.TotalFields = *detail.DiffSummary.TotalFields
		}
		s.Schema.Added = intOrZero(detail.DiffSummary.AddedCount)
		s.Schema.Modified = intOrZero(detail.DiffSummary.ModifiedCount)
		s.Schema.Removed = intOrZero(detail.DiffSummary.RemovedCount)
	}

	var rules []datasets.DatasetRuleTiny
	rulesClient, err := dataset_rules.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		s.Rules.Unavailable = warn("rules", err)
	} else if page, err := rulesClient.GetDatasetRules(token, tableID,
		dataset_rules.GetDatasetRulesParams{Limit: summaryRulePageSize}); err != nil {
		s.Rules.Unavailable = warn("rules", err)
	} else {
		rules = page.Results
		s.Rules.Total = len(page.Results)
		if page.TotalRows != nil {
			s.Rules.Total = *page.TotalRows
			s.Rules.Truncated = *page.TotalRows > len(page.Results)
		}
		for _, r := range page.Results {
			switch r.State {
			case api.DatasetRuleStateEnabled:
				s.Rules.Enabled++
			case api.DatasetRuleStateDisabled:
				s.Rules.Disabled++
			case api.DatasetRuleStateError:
				s.Rules.ErrorRules = append(s.Rules.ErrorRules, r.InstanceName)
			case api.DatasetRuleStatePendingValidation:
				s.Rules.PendingValidation++
			default:
				// validation_failed, deleted — and whatever
				// the backend adds. Counted rather than dropped so the RULES
				// line reconciles with Total (D3).
				s.Rules.Other++
			}
		}
	}

	// Both halves of the join can be missing independently; BuildColumnRows
	// tolerates either, so it runs unconditionally. It is told whether the
	// rules half failed, so a grid whose RULES cells are blank because nobody
	// could read the rules is not rendered as a table with no rules.
	s.Columns, s.UnboundRules = datasets.BuildColumnRows(detail, rules, s.Rules.Unavailable != "")

	sugClient, err := rule_suggestions.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		s.Suggestions.Unavailable = warn("suggestions", err)
	} else {
		s.Suggestions, err = fetchSummarySuggestions(sugClient, token, tableID, detail)
		if err != nil {
			s.Suggestions.Unavailable = warn("suggestions", err)
		}
	}

	// limit=1 is the newest job: both list endpoints default to reverse=true
	// with no order_by, which the shared paginator resolves to primary key
	// descending (atlas get_paginated_results_by_primary_key,
	// capi/capi/views/pagination.py).
	ingClient, err := ingestion.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		s.Jobs.IngestionUnavailable = warn("ingestion jobs", err)
	} else if page, err := ingClient.GetIngestionJobs(token,
		ingestion.GetIngestionJobsParams{DatasetID: &tableID, Limit: 1}); err != nil {
		s.Jobs.IngestionUnavailable = warn("ingestion jobs", err)
	} else if len(page.Results) > 0 {
		j := page.Results[0]
		s.Jobs.Ingestion = datasets.NewJobSummary(j.ID, string(j.State), j.UpdatedAt)
	}

	if s.HasConfirmedDraft() {
		jobs, err := cmdutil.FetchRunningIngestionJobs(ctx, tableID)
		if err != nil {
			jobs.Unavailable = warn("running ingestion jobs", err)
		}
		s.Jobs.RunningIngestion = &jobs
	}

	// Independent of the ingestion fetch above: a transient failure there must
	// not skip a profiling fetch that would otherwise succeed, and must not be
	// reported on this one. Each endpoint stamps its own Unavailable field, so
	// a profiling 500 can never make a successful-but-empty ingestion fetch
	// render as "(unavailable: ...)".
	profClient, err := profiling.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		s.Jobs.ProfilingUnavailable = warn("profiling jobs", err)
	} else if page, err := profClient.GetProfilingJobs(token,
		profiling.GetProfilingJobsParams{DatasetID: &tableID, Limit: 1}); err != nil {
		s.Jobs.ProfilingUnavailable = warn("profiling jobs", err)
	} else if len(page.Results) > 0 {
		j := page.Results[0]
		s.Jobs.Profiling = datasets.NewJobSummary(j.ID, string(j.State), j.UpdatedAt)
	}

	unresolved := false
	alertsClient := alerts.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if page, err := alertsClient.GetAlerts(token, alerts.GetAlertsParams{
		DatasetID: &tableID, Resolved: &unresolved, Limit: 1,
	}); err != nil {
		s.Alerts.Unavailable = warn("alerts", err)
	} else if page.TotalRows != nil {
		s.Alerts.Open = *page.TotalRows
	} else {
		s.Alerts.Open = len(page.Results)
	}

	// A second alerts call.
	// It names the blocker behind a held table — the difference between
	// "5 open alerts" and knowing which alert to act on.
	//
	// The gate is TableSummary.NeedsSevereRuleBugLookup (D4), which lives on the
	// summary next to the render gate it is deliberately wider than. Why each
	// half of it exists, and why the asymmetry must not be "fixed" in either
	// direction, is documented there — this call site is not the place to
	// re-derive it, and was the place it drifted from.
	//
	// Its own warning line, never s.Alerts.Unavailable: that field means "the
	// open-alert count could not be read", and stamping it here would erase a
	// count that was fetched successfully a few lines up.
	if s.NeedsSevereRuleBugLookup() {
		issueType := api.AlertTypeSevereRuleBug
		if page, err := alertsClient.GetAlerts(token, alerts.GetAlertsParams{
			DatasetID: &tableID, Resolved: &unresolved, IssueType: &issueType,
			Limit: summarySevereAlertPageSize,
		}); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: severe_rule_bug alerts unavailable: %v\n", err)
		} else {
			for _, a := range page.Results {
				s.Alerts.SevereRuleBug = append(s.Alerts.SevereRuleBug,
					datasets.SevereRuleBugBlocker{AlertID: a.ID})
			}
			// Same shape as the rules page above: the server's total against
			// what one page returned. Without it the stranded-alert line would
			// render a capped length as a definite count.
			if page.TotalRows != nil {
				s.Alerts.SevereRuleBugTruncated = *page.TotalRows > len(page.Results)
			}
		}
	}

	if small, err := dsClient.GetDatasetSmall(token, tableID); err != nil {
		s.SharedWith.Unavailable = warn("shared-with", err)
	} else {
		s.MyPermissionLevel = small.MyPermissionLevel
		for _, u := range small.Users {
			s.SharedWith.Users = append(s.SharedWith.Users, u.Email)
			s.SharedWith.Members = append(s.SharedWith.Members, datasets.SharedMember{
				Email: u.Email, PermissionLevel: string(u.PermissionLevel), EffectivePermissionLevel: string(u.EffectivePermissionLevel),
			})
		}
	}

	s.ComputeBlockers()
	return s, nil
}

func fetchSummarySuggestions(client *rule_suggestions.Client, token string, tableID int, schema *datasets.TableSchemaDetail) (datasets.SuggestionsSection, error) {
	var section datasets.SuggestionsSection
	var rows []api.DatasetRuleSuggestionTinySchema
	for page := 1; ; page++ {
		list, err := client.List(token, tableID, rule_suggestions.ListOptions{
			States: []string{string(api.SuggestionStatePending)},
			Page:   page,
			Limit:  summaryRulePageSize,
		})
		if err != nil {
			return section, fmt.Errorf("failed to list pending rule suggestions (page %d): %w", page, err)
		}
		rows = append(rows, list.Results...)
		if list.Next == nil {
			break
		}
	}
	section.Pending = len(rows)
	if schema == nil {
		return section, fmt.Errorf("cannot determine migration blockers without the schema")
	}
	if schema.MigrationState != api.DraftOnly && schema.MigrationState != api.DraftWithCurrent {
		return section, nil
	}
	blocking, err := countSummarySuggestionBlockers(client, token, tableID, rows, schema.MigrationID)
	if err != nil {
		return section, err
	}
	section.Blocking = blocking
	return section, nil
}

func countSummarySuggestionBlockers(client *rule_suggestions.Client, token string, tableID int, rows []api.DatasetRuleSuggestionTinySchema, migrationID int) (int, error) {
	var details []api.DatasetRuleSuggestionSchema
	for _, row := range rows {
		if row.State != api.SuggestionStatePending || (row.Action != api.SuggestionActionUpdate && row.Action != api.SuggestionActionDelete) {
			continue
		}
		detail, err := client.Get(token, tableID, row.ID.String())
		if err != nil {
			var apiErr *rule_suggestions.APIError
			if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
				continue
			}
			return 0, fmt.Errorf("failed to read pending rule suggestion %s: %w", row.ID, err)
		}
		details = append(details, *detail)
	}
	gate := alerts.BuildSuggestionGate(details, &migrationID)
	return len(gate.Blockers), nil
}

// quarantineHolds projects the hold buckets out of counts, or returns nil when
// on_hold, approved or held_for_approval is missing from the response.
func quarantineHolds(counts *datasets.QuarantineCounts) *datasets.QuarantineHolds {
	if counts.OnHold == nil || counts.Approved == nil || counts.HeldForApproval == nil {
		return nil
	}
	h := &datasets.QuarantineHolds{
		OnHold:             *counts.OnHold,
		Approved:           *counts.Approved,
		WaitingForApproval: counts.WaitingForApproval,
		HeldForApproval:    *counts.HeldForApproval,
	}
	if counts.HeldForApprovalByReason != nil {
		h.HeldForApprovalByReason = *counts.HeldForApprovalByReason
	}
	return h
}

func intOrZero(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}
