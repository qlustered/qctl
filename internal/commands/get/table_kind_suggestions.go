package get

import (
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/datasets"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/pkg/tableui"
	"github.com/spf13/cobra"
)

type tableKindSuggestionRow struct {
	Slug                  string `json:"slug"`
	Name                  string `json:"name"`
	MatchedCount          int    `json:"matched_count"`
	StandardFieldCount    int    `json:"standard_field_count"`
	MatchedStandardFields string `json:"matched_standard_fields"`
}

// NewTableKindSuggestionsCommand creates the get table-kind-suggestions command.
func NewTableKindSuggestionsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:         "table-kind-suggestions [table-id-or-name]",
		Short:       "Suggest table kinds from a table's assigned standard fields",
		Annotations: cmdutil.WithArgKinds(catalog.For("table-kind"), "table"),
		Args:        cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			positional := ""
			if len(args) > 0 {
				positional = args[0]
			}
			if err := cmdutil.ValidateTableSelector(cmd, positional); err != nil {
				return err
			}
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}
			id, err := cmdutil.ResolveTableID(ctx, cmd, positional)
			if err != nil {
				return err
			}
			result, err := datasets.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity).TableKindSuggestions(ctx.Credential.AccessToken, id)
			if err != nil {
				return err
			}
			return printTableKindSuggestions(cmd, ctx, result)
		},
	}
	cmdutil.AddTableFlags(cmd, "Table (dataset)")
	cmd.Flags().String("columns", "", "Comma-separated table columns ("+strings.Join(cmdutil.DeclareColumns(cmd, []tableKindSuggestionRow(nil)), ",")+")")
	return cmd
}

func printTableKindSuggestions(cmd *cobra.Command, ctx *cmdutil.CommandContext, result *api.DatasetProblemDomainSuggestionsSchema) error {
	format, _ := cmd.Flags().GetString("output")
	switch format {
	case "json", "yaml": //nolint:goconst // Keep output format handling local to this command.
		return output.WriteRaw(cmd.OutOrStdout(), format, result)
	case "name": //nolint:goconst // Keep output format handling local to this command.
		for _, item := range result.Results {
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "table-kind/%s\n", item.ProblemDomain.ID); err != nil {
				return err
			}
		}
		return nil
	}
	rows := make([]tableKindSuggestionRow, 0, len(result.Results))
	for _, item := range result.Results {
		slugs := make([]string, 0, len(item.MatchedConcepts))
		for _, concept := range item.MatchedConcepts {
			slugs = append(slugs, concept.Slug)
		}
		rows = append(rows, tableKindSuggestionRow{Slug: item.ProblemDomain.Slug, Name: item.ProblemDomain.Name, MatchedCount: item.MatchedConceptCount, StandardFieldCount: item.DomainConceptCount, MatchedStandardFields: strings.Join(slugs, ",")})
	}
	if len(rows) == 0 {
		skip, err := printEmptyResult(cmd, ctx, "table kind suggestions", rows, nil)
		if err != nil || skip {
			return err
		}
	}
	printContextBanner(cmd, ctx)
	return tableui.PrintFromCmd(cmd, rows, "slug,name,matched_count,standard_field_count,matched_standard_fields")
}
