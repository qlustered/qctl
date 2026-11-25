package get

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/dataset_kinds"
	"github.com/qlustered/qctl/internal/datasets"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/pkg/tableui"
	"github.com/qlustered/qctl/internal/rule_versions"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

var validRuleRevisionSortFields = []string{
	"name", "release", "created_at", "is_builtin", "id", "relevance",
}

// ruleRevisionStates is what --state accepts: none, which sends no state
// filter, then every RuleState.
var ruleRevisionStates = append([]string{"none"}, api.EnumValues[api.RuleState]()...)

// NewRuleRevisionsCommand creates the get rule-revisions command
func NewRuleRevisionsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:         "rule-revisions",
		Short:       "List rule revisions",
		Long:        `List all rule revisions in the current organization's rule catalog.`,
		Annotations: output.WithNameKind(catalog.For("rule-revision"), "rule"),
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("for-table") {
				return getMatchingRuleRevisions(cmd)
			}
			if cmd.Flags().Changed("match") {
				return qerrors.NewBadUsage("--match requires --for-table")
			}
			if err := cmdutil.ValidateConflictingFlags(cmd, []string{"table-kind", "general"}); err != nil {
				return err
			}

			// Parse and validate params
			params, err := parseRuleRevisionsParams(cmd)
			if err != nil {
				return err
			}
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}
			if err := resolveRuleRevisionTableKind(cmd, ctx, &params); err != nil {
				return err
			}

			// Create client and fetch results
			client, err := rule_versions.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			if err != nil {
				return fmt.Errorf("failed to create client: %w", err)
			}

			resp, err := client.GetRuleRevisions(ctx.Credential.AccessToken, params)
			if err != nil {
				return fmt.Errorf("failed to get rule revisions: %w", err)
			}

			if len(resp.Results) == 0 {
				skip, err := printEmptyResultWithHint(cmd, ctx, "rule revisions", []rule_versions.RuleRevisionDisplay{}, resp.TotalRows,
					func() string { return formatRuleRevisionFacets(resp) })
				if err != nil {
					return err
				}
				if skip {
					return nil
				}
			}

			printContextBanner(cmd, ctx)

			lp := listPage{noun: "rule revisions", page: &params.Page, limit: &params.Limit, total: resp.TotalRows, more: resp.Next != nil}
			if err := printListPage(cmd, resp.Results, lp, func(data any) error {
				if err := printRuleRevisionsResults(cmd, data, ctx.Verbosity); err != nil {
					return err
				}
				if isTableFormat(cmd) {
					fmt.Fprint(cmd.ErrOrStderr(), formatRuleRevisionFacets(resp))
				}
				return nil
			}); err != nil {
				return err
			}

			// Seeded after printing, not before: see rememberRuleFamilyShortIDs
			// in rule_families.go for why the ordering matters.
			rememberRuleRevisionShortIDs(ctx, resp.Results)
			return nil
		},
	}

	addRuleRevisionsFlags(cmd)
	return cmd
}

// ruleMatchBuckets maps each --match value to the bucket it requests.
var ruleMatchBuckets = map[string]api.RuleMatchBucket{
	"standard-field":                   api.RuleMatchBucketConcept,
	string(api.RuleMatchBucketConcept): api.RuleMatchBucketConcept,
	string(api.RuleMatchBucketGeneric): api.RuleMatchBucketGeneric,
}

type ruleRevisionMatchQuery struct {
	table  string
	params rule_versions.GetDatasetRuleRevisionMatchesParams
}

func parseRuleRevisionMatchQuery(cmd *cobra.Command) (ruleRevisionMatchQuery, error) {
	for _, flag := range []string{"order-by", "reverse", "state", "only-default", "has-upgrade-available", "general"} {
		if cmd.Flags().Changed(flag) {
			return ruleRevisionMatchQuery{}, qerrors.NewBadUsagef("--for-table cannot be combined with --%s", flag)
		}
	}
	table, _ := cmd.Flags().GetString("for-table")
	if strings.TrimSpace(table) == "" {
		return ruleRevisionMatchQuery{}, qerrors.NewBadUsage("--for-table requires a table name or ID")
	}
	match, _ := cmd.Flags().GetString("match")
	bucket, ok := ruleMatchBuckets[match]
	if !ok {
		return ruleRevisionMatchQuery{}, qerrors.NewBadUsagef("invalid --match %q: choose standard-field or generic", match)
	}
	if bucket == api.RuleMatchBucketGeneric && cmd.Flags().Changed("table-kind") {
		return ruleRevisionMatchQuery{}, qerrors.NewBadUsage("--match generic cannot be combined with --table-kind")
	}
	search, err := parseSearchFlag(cmd)
	if err != nil {
		return ruleRevisionMatchQuery{}, err
	}
	page, limit, err := paginationFlags(cmd)
	if err != nil {
		return ruleRevisionMatchQuery{}, err
	}
	if !cmd.Flags().Changed("limit") {
		limit = 20
		if err := cmd.Flags().Set("limit", "20"); err != nil {
			return ruleRevisionMatchQuery{}, err
		}
	}
	params := rule_versions.GetDatasetRuleRevisionMatchesParams{Match: bucket, Page: page, Limit: limit}
	if search != "" {
		params.SearchQuery = &search
	}
	return ruleRevisionMatchQuery{table: table, params: params}, nil
}

func getMatchingRuleRevisions(cmd *cobra.Command) error {
	query, err := parseRuleRevisionMatchQuery(cmd)
	if err != nil {
		return err
	}
	if err := validateRuleRevisionTableKinds(cmd); err != nil {
		return err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	if err := resolveRuleRevisionTableKinds(cmd, ctx, &query.params.ProblemDomainSlug); err != nil {
		return err
	}
	tableID, err := datasets.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity).ResolveID(ctx.Credential.AccessToken, query.table)
	if err != nil {
		return fmt.Errorf("failed to resolve table: %w", err)
	}
	client, err := rule_versions.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}
	resp, err := client.GetDatasetRuleRevisionMatches(ctx.Credential.AccessToken, tableID, query.params)
	if err != nil {
		return fmt.Errorf("failed to get rule revision matches: %w", err)
	}
	return printRuleRevisionMatches(cmd, ctx, query.params.Page, query.params.Limit, resp)
}

// printRuleRevisionMatches prints one page of matches through their display
// projection, with the match counts on stderr under table, and a List of them
// as the API sent them.
func printRuleRevisionMatches(cmd *cobra.Command, ctx *cmdutil.CommandContext, page, limit int, resp *api.RuleRevisionMatchListSchema) error {
	footer := func() string {
		return fmt.Sprintf("Match counts: Full: %d  Partial: %d  Generic: %d\n", resp.FullCount, resp.PartialCount, resp.GenericCount)
	}
	if len(resp.Results) == 0 {
		skip, err := printEmptyResultWithHint(cmd, ctx, "rule revisions", []rule_versions.RuleRevisionMatchDisplay{}, resp.TotalRows, footer)
		if err != nil {
			return err
		}
		if skip {
			return nil
		}
	}
	printContextBanner(cmd, ctx)
	lp := listPage{noun: "rule revisions", page: &page, limit: &limit, total: resp.TotalRows, more: resp.Next != nil}
	if err := printListPage(cmd, resp.Results, lp, func(data any) error {
		results, ok := data.([]api.RuleRevisionMatchSchema)
		if !ok {
			return tableui.PrintFromCmd(cmd, data, "")
		}
		cols := "slug,release,state,tags,short_id,match,standard_fields,missing"
		if ctx.Verbosity >= 1 {
			cols = "slug,release,state,tags,id,description,affected_columns,match,standard_fields,missing"
		}
		if err := tableui.PrintFromCmd(cmd, rule_versions.MatchToDisplayList(results), cols); err != nil {
			return err
		}
		if isTableFormat(cmd) {
			fmt.Fprint(cmd.ErrOrStderr(), footer())
		}
		return nil
	}); err != nil {
		return err
	}

	ids := make([]string, 0, len(resp.Results))
	for _, result := range resp.Results {
		ids = append(ids, result.ID.String())
	}
	rule_versions.RememberRevisionShortIDs(ctx.ServerURL, ctx.OrganizationID, ids)
	return nil
}

func resolveRuleRevisionTableKind(cmd *cobra.Command, ctx *cmdutil.CommandContext, params *rule_versions.GetRuleRevisionsParams) error {
	return resolveRuleRevisionTableKinds(cmd, ctx, &params.ProblemDomainSlug)
}

// validateRuleRevisionTableKinds refuses an empty or blank --table-kind.
func validateRuleRevisionTableKinds(cmd *cobra.Command) error {
	if !cmd.Flags().Changed("table-kind") {
		return nil
	}
	refs, _ := cmd.Flags().GetStringArray("table-kind")
	// GetStringArray reads a lone --table-kind "" back as no values at all.
	if len(refs) == 0 {
		refs = []string{""}
	}
	for _, ref := range refs {
		if strings.TrimSpace(ref) == "" {
			return qerrors.NewBadUsage("--table-kind requires a table-kind slug, name, or ID")
		}
	}
	return nil
}

func resolveRuleRevisionTableKinds(cmd *cobra.Command, ctx *cmdutil.CommandContext, slugsOut **[]string) error {
	if !cmd.Flags().Changed("table-kind") {
		return nil
	}
	refs, _ := cmd.Flags().GetStringArray("table-kind")
	client := dataset_kinds.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	slugs := make([]string, 0, len(refs))
	for _, ref := range refs {
		id, err := client.ResolveDatasetKindID(ctx.Credential.AccessToken, ref)
		if err != nil {
			return fmt.Errorf("failed to resolve table kind %q: %w", ref, err)
		}
		kind, err := client.GetDatasetKind(ctx.Credential.AccessToken, id)
		if err != nil {
			return fmt.Errorf("failed to get table kind %q: %w", ref, err)
		}
		slugs = append(slugs, kind.Slug)
	}
	*slugsOut = &slugs
	return nil
}

// parseRuleRevisionsParams extracts and validates parameters from command flags.
func parseRuleRevisionsParams(cmd *cobra.Command) (rule_versions.GetRuleRevisionsParams, error) {
	var params rule_versions.GetRuleRevisionsParams

	// Sorting
	sortBy, _ := cmd.Flags().GetString("order-by")
	params.OrderBy = sortBy

	reverse, _ := cmd.Flags().GetBool("reverse")
	params.Reverse = reverse

	// Pagination
	page, limit, err := paginationFlags(cmd)
	if err != nil {
		return params, err
	}
	params.Page = page
	params.Limit = limit

	// Filters
	if err := parseRuleRevisionFilters(cmd, &params); err != nil {
		return params, err
	}

	// Validate sort field
	if sortBy != "" {
		if err := validateSortField(sortBy, validRuleRevisionSortFields); err != nil {
			return params, err
		}
	}

	return params, validateRuleRevisionTableKinds(cmd)
}

// parseRuleRevisionFilters handles rule revision filter flags.
func parseRuleRevisionFilters(cmd *cobra.Command, params *rule_versions.GetRuleRevisionsParams) error {
	search, err := parseSearchFlag(cmd)
	if err != nil {
		return err
	}
	if search != "" {
		params.SearchQuery = &search
	}
	if general, _ := cmd.Flags().GetBool("general"); general {
		params.GeneralOnly = &general
	}
	if state, _ := cmd.Flags().GetString("state"); state != "" {
		if err := validateStateValue("--state", state, ruleRevisionStates); err != nil {
			return err
		}
		if state != "none" {
			params.StateFilter = &state
		}
	}
	if val, _ := cmd.Flags().GetString("only-default"); val != "" {
		b, err := strconv.ParseBool(val)
		if err != nil {
			return qerrors.NewBadUsagef("invalid value %q for --only-default: must be true or false", val)
		}
		params.OnlyDefault = &b
	} else if params.StateFilter != nil && *params.StateFilter != string(api.RuleStateEnabled) {
		b := false
		params.OnlyDefault = &b
	}
	if val, _ := cmd.Flags().GetString("has-upgrade-available"); val != "" {
		b, err := strconv.ParseBool(val)
		if err != nil {
			return qerrors.NewBadUsagef("invalid value %q for --has-upgrade-available: must be true or false", val)
		}
		params.HasUpgradeAvailable = &b
	}
	return nil
}

// printRuleRevisionsResults prints rule revisions through their display
// projection, with the default columns for verbosity, and a List of them as
// the API sent them.
func printRuleRevisionsResults(cmd *cobra.Command, data any, verbosity int) error {
	defaultCols := ruleDefaultColumns
	if verbosity >= 1 {
		defaultCols = ruleVerboseColumns
	}
	if results, ok := data.([]rule_versions.RuleRevisionTiny); ok {
		data = rule_versions.ToDisplayList(results)
	}
	return tableui.PrintFromCmd(cmd, data, defaultCols)
}

// addRuleRevisionsFlags adds all flags for the rule-revisions command.
func addRuleRevisionsFlags(cmd *cobra.Command) {
	// Pagination control
	cmd.Flags().Int("limit", 100, "Maximum number of rule revisions to return")
	cmd.Flags().Int("page", 1, "Page number")
	cmdutil.RefString(cmd.Flags(), "for-table", "table", "List default revisions matching a table name or ID (default limit: 20)")
	cmd.Flags().String("match", "standard-field", "Match bucket with --for-table (standard-field|generic)")

	// Sorting
	cmd.Flags().String("order-by", "name", "Order by field (id, name, release, is_builtin, created_at, relevance; relevance ranks --search matches and falls back to name without --search)")
	cmd.Flags().Bool("reverse", false, "Reverse the sort order")

	// Filtering
	cmd.Flags().String("state", "", "Filter by state ("+strings.Join(ruleRevisionStates, ", ")+"; none = no filter)")
	cmd.Flags().String("search", "", "Search rule revisions (also works with --for-table; minimum 3 characters)")
	cmdutil.RefStringArray(cmd.Flags(), "table-kind", "table-kind", "Filter by table kind (slug, name, or ID); repeat to match any kind, also with --for-table")
	cmd.Flags().Bool("general", false, "Only revisions with no required standard fields")
	cmd.Flags().String("only-default", "", "Limit to default revisions (true|false; server default: true; non-enabled --state implies false)")
	cmd.Flags().String("has-upgrade-available", "", "Filter by upgrade availability (true|false)")

	// Output control
	listed, matched := []rule_versions.RuleRevisionDisplay(nil), []rule_versions.RuleRevisionMatchDisplay(nil)
	cmdutil.DeclareColumns(cmd, listed, matched)
	cmd.Flags().String("columns", "", "Comma-separated list of columns to display (table format only)\n"+
		"Available: "+strings.Join(output.AvailableColumns(listed, nil), ", ")+"; with --for-table: "+
		strings.Join(output.AvailableColumns(matched, nil), ", "))
}

func formatRuleRevisionFacets(resp *rule_versions.RuleRevisionList) string {
	var b strings.Builder
	b.WriteString("Table-kind counts:\n")
	for _, facet := range resp.ProblemDomainFacets {
		fmt.Fprintf(&b, "  %s (%s): %d\n", terminal.Sanitize(facet.Name), terminal.Sanitize(facet.Slug), facet.Count)
	}
	fmt.Fprintf(&b, "  General: %d\n", resp.GeneralCount)
	return b.String()
}

// rememberRuleRevisionShortIDs seeds the short-ID cache with every revision
// this listing is about to display. See rememberRuleFamilyShortIDs in
// rule_families.go: a SHORT-ID the user can see is a SHORT-ID they can paste,
// and only the local cache makes that true.
func rememberRuleRevisionShortIDs(ctx *cmdutil.CommandContext, results []rule_versions.RuleRevisionTiny) {
	ids := make([]string, 0, len(results))
	for _, r := range results {
		ids = append(ids, r.ID.String())
	}
	rule_versions.RememberRevisionShortIDs(ctx.ServerURL, ctx.OrganizationID, ids)
}
