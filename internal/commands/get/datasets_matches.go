package get

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/pkg/tableui"
	"github.com/qlustered/qctl/internal/rule_upgrades"
	"github.com/qlustered/qctl/internal/rule_versions"
	"github.com/spf13/cobra"
)

type datasetMatchQuery struct {
	rule    string
	release string
	page    int
	limit   int
}

func parseDatasetMatchQuery(cmd *cobra.Command) (datasetMatchQuery, error) {
	for _, flag := range []string{"search", "order-by", "reverse", "state", "states", "destination-id", "destination-name", "name", "table-kind", "series", "needs-attention"} {
		if cmd.Flags().Changed(flag) {
			return datasetMatchQuery{}, qerrors.NewBadUsagef("--for-rule cannot be combined with --%s", flag)
		}
	}
	rule, _ := cmd.Flags().GetString("for-rule")
	if strings.TrimSpace(rule) == "" {
		return datasetMatchQuery{}, qerrors.NewBadUsage("--for-rule requires a rule name or revision ID")
	}
	release, _ := cmd.Flags().GetString("release")
	if cmd.Flags().Changed("release") && strings.TrimSpace(release) == "" {
		return datasetMatchQuery{}, qerrors.NewBadUsage("--release requires a release value")
	}
	page, limit, err := paginationFlags(cmd)
	if err != nil {
		return datasetMatchQuery{}, err
	}
	if !cmd.Flags().Changed("limit") {
		limit = 20
		if err := cmd.Flags().Set("limit", "20"); err != nil {
			return datasetMatchQuery{}, err
		}
	}
	return datasetMatchQuery{rule: rule, release: release, page: page, limit: limit}, nil
}

func getMatchingDatasets(cmd *cobra.Command) error {
	query, err := parseDatasetMatchQuery(cmd)
	if err != nil {
		return err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	client, err := rule_versions.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}
	revisionID, err := resolveDatasetMatchRevision(client, ctx.Credential.AccessToken, query)
	if err != nil {
		return fmt.Errorf("failed to resolve rule: %w", err)
	}
	resp, err := client.GetRuleRevisionDatasetMatches(ctx.Credential.AccessToken, revisionID, query.page, query.limit)
	if err != nil {
		return fmt.Errorf("failed to get table matches: %w", err)
	}
	return printDatasetMatches(cmd, ctx, query.page, query.limit, resp)
}

func resolveDatasetMatchRevision(client *rule_versions.Client, token string, query datasetMatchQuery) (string, error) {
	// A full revision UUID already identifies the requested revision.
	id, parseErr := uuid.Parse(query.rule)
	if query.release == "" && parseErr == nil && strings.EqualFold(id.String(), query.rule) {
		return client.ResolveRuleID(token, query.rule, "")
	}
	family, err := rule_upgrades.ResolveFamily(client, token, query.rule)
	if err != nil {
		return "", err
	}
	if query.release != "" {
		if rev := family.ByRelease(query.release); rev != nil {
			return rev.ID.String(), nil
		}
		return "", qerrors.NewBadUsagef("rule %q has no release %q", family.Name, query.release)
	}
	// An exact family name or slug names the default; a short revision ID
	// names the revision it resolved through.
	if query.rule != family.Name && !datasetMatchFamilyHasSlug(family, query.rule) &&
		rule_versions.IsUUIDPrefix(query.rule) &&
		strings.HasPrefix(strings.ReplaceAll(family.RevisionID, "-", ""), strings.ReplaceAll(query.rule, "-", "")) {
		return family.RevisionID, nil
	}
	if rev := family.Default(); rev != nil {
		return rev.ID.String(), nil
	}
	return "", qerrors.NewBadUsagef("rule %q has no default revision; use --release to select one", family.Name)
}

func datasetMatchFamilyHasSlug(family *rule_upgrades.ResolvedFamily, input string) bool {
	for _, rev := range family.Releases {
		if rev.Slug == input {
			return true
		}
	}
	return false
}

type datasetMatchDisplay struct {
	DatasetID      int               `json:"dataset_id"`
	Name           string            `json:"name"`
	Match          api.RuleMatchKind `json:"match"`
	StandardFields string            `json:"standard_fields"`
	Missing        string            `json:"missing"`
}

func datasetMatchesToDisplay(items []api.DatasetMatchSchema) []datasetMatchDisplay {
	rows := make([]datasetMatchDisplay, len(items))
	for i, item := range items {
		missing := make([]string, len(item.MissingConcepts))
		for j, concept := range item.MissingConcepts {
			missing[j] = concept.Slug
		}
		rows[i] = datasetMatchDisplay{
			DatasetID: item.DatasetID, Name: item.Name, Match: item.MatchKind,
			StandardFields: fmt.Sprintf("%d/%d", item.MatchedConceptCount, item.RequiredConceptCount),
			Missing:        strings.Join(missing, ", "),
		}
	}
	return rows
}

func printDatasetMatches(cmd *cobra.Command, ctx *cmdutil.CommandContext, page, limit int, resp *api.DatasetMatchListSchema) error {
	footer := func() string {
		if resp.IsGeneric {
			return "This rule requires no standard fields, so it has no table matches.\n"
		}
		return fmt.Sprintf("Match counts: Full: %d  Partial: %d\n", resp.FullCount, resp.PartialCount)
	}
	if len(resp.Results) == 0 {
		skip, err := printEmptyResultWithHint(cmd, ctx, "tables", []datasetMatchDisplay{}, resp.TotalRows, footer)
		if err != nil {
			return err
		}
		if skip {
			return nil
		}
	}
	printContextBanner(cmd, ctx)
	lp := listPage{noun: "tables", page: &page, limit: &limit, total: resp.TotalRows, noFilters: true}
	return printListPage(cmd, resp.Results, lp, func(data any) error {
		if items, ok := data.([]api.DatasetMatchSchema); ok && isTableFormat(cmd) {
			data = datasetMatchesToDisplay(items)
			if err := tableui.PrintFromCmd(cmd, data, "name,match,standard_fields,missing"); err != nil {
				return err
			}
		} else if format, _ := cmd.Flags().GetString("output"); format == string(output.FormatName) {
			// A match carries its table's id as dataset_id.
			for _, name := range output.NameRefs("table", "dataset_id", data) {
				fmt.Fprintln(cmd.OutOrStdout(), name)
			}
		} else if err := tableui.PrintFromCmd(cmd, data, ""); err != nil {
			return err
		}
		// printEmptyResultWithHint has already printed it for an empty
		// non-table page.
		if len(resp.Results) > 0 || isTableFormat(cmd) {
			fmt.Fprint(cmd.ErrOrStderr(), footer())
		}
		return nil
	})
}
