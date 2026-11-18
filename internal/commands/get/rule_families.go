package get

import (
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/pkg/fuzzy"
	"github.com/qlustered/qctl/internal/pkg/tableui"
	"github.com/qlustered/qctl/internal/rule_families"
	"github.com/qlustered/qctl/internal/rule_versions"
	"github.com/spf13/cobra"
)

var validRuleFamilySortFields = []string{
	"slug", "impact_score", "is_builtin", "created_at",
}

// NewRuleFamiliesCommand creates the get rules command (rule families)
func NewRuleFamiliesCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rules",
		Short: "List rule families",
		Long: `List the rule families in the current organization's rule catalog. The table
shows one row for each family's primary revision, plus one for a revision newer
than the default; -o json and -o yaml emit a List of the family objects.`,
		Annotations: catalog.For("rule"),
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Parse and validate params
			params, err := parseRuleFamiliesParams(cmd)
			if err != nil {
				return err
			}

			// Bootstrap auth context
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}

			// Create client and fetch results
			client := rule_families.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			resp, err := client.GetRuleFamilies(ctx.Credential.AccessToken, params)
			if err != nil {
				return fmt.Errorf("failed to get rules: %w", err)
			}

			if len(resp.Results) == 0 {
				skip, err := printEmptyResultWithHint(cmd, ctx, "rules", []rule_families.RuleFamilyDisplay{}, resp.TotalRows,
					func() string { return ruleSearchHint(ctx, client, params) })
				if err != nil {
					return err
				}
				if skip {
					return nil
				}
			}

			printContextBanner(cmd, ctx)

			lp := listPage{noun: "rules", page: &params.Page, limit: &params.Limit, total: resp.TotalRows, more: resp.Next != nil}
			if err := printListPage(cmd, resp.Results, lp, func(data any) error {
				return printRuleFamiliesResults(cmd, data)
			}); err != nil {
				return err
			}

			// Every SHORT-ID on screen becomes locally expandable: the backend
			// cannot search by id, so this is what lets a user paste one back
			// into `qctl get rule <short-id>` or a TableRule manifest. Seeded
			// after printing, not before: aliasstore.Open() takes bbolt's
			// exclusive flock (up to aliasstore.openTimeout = 5s under
			// contention from a concurrent qctl process), and this command
			// must show its results before it risks sitting on that lock.
			rememberRuleFamilyShortIDs(ctx, resp.Results)

			return nil
		},
	}

	addRuleFamiliesFlags(cmd)
	return cmd
}

// parseRuleFamiliesParams extracts and validates parameters from command flags.
func parseRuleFamiliesParams(cmd *cobra.Command) (rule_families.GetRuleFamiliesParams, error) {
	var params rule_families.GetRuleFamiliesParams

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
	search, err := parseSearchFlag(cmd)
	if err != nil {
		return params, err
	}
	if search != "" {
		params.SearchQuery = &search
	}
	excludeBuiltin, _ := cmd.Flags().GetBool("exclude-builtin")
	params.ExcludeBuiltin = excludeBuiltin

	// Validate sort field
	if sortBy != "" {
		if err := validateSortField(sortBy, validRuleFamilySortFields); err != nil {
			return params, err
		}
	}

	return params, nil
}

// printRuleFamiliesResults prints rule families through their display
// projection, one row per revision, and a List of them as the API sent them.
func printRuleFamiliesResults(cmd *cobra.Command, data any) error {
	if results, ok := data.([]rule_families.RuleFamilyItem); ok {
		data = rule_families.ToDisplayList(results)
	}
	return tableui.PrintFromCmd(cmd, data, "slug,release,state,tags,author,short_id")
}

// addRuleFamiliesFlags adds all flags for the rules (rule families) command.
func addRuleFamiliesFlags(cmd *cobra.Command) {
	// Pagination control
	cmd.Flags().Int("limit", 1000, "Maximum number of rule families to return")
	cmd.Flags().Int("page", 1, "Page number")

	// Sorting
	cmd.Flags().String("order-by", "impact_score", "Order by field (slug, impact_score, is_builtin, created_at)")
	cmd.Flags().Bool("reverse", false, "Reverse the sort order")

	// Filtering
	cmd.Flags().String("search", "", "Search rule families by name")
	cmd.Flags().Bool("exclude-builtin", false, "Exclude built-in rule families")

	// Output control
	cmd.Flags().String("columns", "", "Comma-separated list of columns to display (table format only)\n"+
		"Available: "+strings.Join(cmdutil.DeclareColumns(cmd, []rule_families.RuleFamilyDisplay(nil)), ", "))
}

// ruleSearchHint answers the two questions an empty --search leaves open: does
// the catalog hold anything at all, and did the user nearly name it? It re-runs
// the same listing without --search and nothing else, so both the count and the
// suggestions describe the scope the user actually asked for -- --exclude-builtin
// stays applied and cannot suggest a rule the user excluded.
//
// Every failure here returns "": the primary message already stands on its own,
// and an empty list is not the place to introduce a new way to fail.
func ruleSearchHint(ctx *cmdutil.CommandContext, client *rule_families.Client, params rule_families.GetRuleFamiliesParams) string {
	if params.SearchQuery == nil || *params.SearchQuery == "" {
		return ""
	}

	unfiltered := params
	unfiltered.SearchQuery = nil
	resp, err := client.GetRuleFamilies(ctx.Credential.AccessToken, unfiltered)
	if err != nil {
		return ""
	}

	// TotalRows counts across pages; len(Results) is capped by --limit.
	total := len(resp.Results)
	if resp.TotalRows != nil {
		total = *resp.TotalRows
	}

	var b strings.Builder
	if total == 1 {
		b.WriteString("1 rule matches without --search.\n")
	} else {
		fmt.Fprintf(&b, "%d rules match without --search.\n", total)
	}
	if s := suggestRuleSlugs(*params.SearchQuery, resp.Results); len(s) > 0 {
		fmt.Fprintf(&b, "\nDid you mean: %s\n", strings.Join(s, ", "))
	}
	return b.String()
}

// suggestRuleSlugs matches the query against both the slug and the name of
// every family -- the two fields the backend itself searches -- but always
// suggests the slug, because that is what --search and `get rule` take.
func suggestRuleSlugs(query string, families []rule_families.RuleFamilyItem) []string {
	candidates := make([]fuzzy.Candidate, 0, len(families)*2)
	for _, f := range families {
		if f.Slug == "" {
			continue
		}
		candidates = append(candidates, fuzzy.Candidate{Match: f.Slug, Value: f.Slug})
		if f.Name != "" && f.Name != f.Slug {
			candidates = append(candidates, fuzzy.Candidate{Match: f.Name, Value: f.Slug})
		}
	}
	return fuzzy.SuggestFrom(query, candidates)
}

// rememberRuleFamilyShortIDs seeds the short-ID cache with every revision this
// listing is about to display, primary and secondary alike.
func rememberRuleFamilyShortIDs(ctx *cmdutil.CommandContext, families []rule_families.RuleFamilyItem) {
	ids := make([]string, 0, len(families))
	for _, f := range families {
		if f.PrimaryRevision != nil {
			ids = append(ids, f.PrimaryRevision.ID.String())
		}
		if f.SecondaryRevision != nil {
			ids = append(ids, f.SecondaryRevision.ID.String())
		}
	}
	rule_versions.RememberRevisionShortIDs(ctx.ServerURL, ctx.OrganizationID, ids)
}
