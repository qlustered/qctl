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
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

type conceptSuggestionRow struct {
	Column        string `json:"column"`
	StandardField string `json:"standard_field"`
	Source        string `json:"source"`
	MatchedText   string `json:"matched_text"`
	Similarity    string `json:"similarity"`
	TableCount    int    `json:"table_count"`
}

// NewStandardFieldSuggestionsCommand creates the get standard-field-suggestions command.
func NewStandardFieldSuggestionsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:         "standard-field-suggestions [table-id-or-name]",
		Aliases:     []string{"concept-suggestions"},
		Short:       "Suggest standard fields for a table's unassigned columns",
		Annotations: cmdutil.WithArgKinds(catalog.For("standard-field"), "table"),
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
			result, err := datasets.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity).ConceptSuggestions(ctx.Credential.AccessToken, id)
			if err != nil {
				return err
			}
			return printStandardFieldSuggestions(cmd, ctx, result)
		},
	}
	cmdutil.AddTableFlags(cmd, "Table (dataset)")
	cmd.Flags().String("columns", "", "Comma-separated table columns ("+strings.Join(cmdutil.DeclareColumns(cmd, []conceptSuggestionRow(nil)), ",")+")")
	return cmd
}

func printStandardFieldSuggestions(cmd *cobra.Command, ctx *cmdutil.CommandContext, result *api.DatasetColumnConceptSuggestionsSchema) error {
	format, _ := cmd.Flags().GetString("output")
	switch format {
	case "json", "yaml": //nolint:goconst // Keep output format handling local to this command.
		return output.WriteRaw(cmd.OutOrStdout(), format, result)
	case "name": //nolint:goconst // Keep output format handling local to this command.
		for _, column := range result.Columns {
			for _, suggestion := range column.Suggestions {
				if _, err := fmt.Fprintln(cmd.OutOrStdout(), terminal.Sanitize(column.DatasetCol+"/"+suggestion.Concept.Slug)); err != nil {
					return err
				}
			}
		}
		return nil
	}
	rows := standardFieldSuggestionRows(result.Columns)
	if len(rows) == 0 {
		skip, err := printEmptyResult(cmd, ctx, "standard field suggestions", rows, nil)
		if err != nil || skip {
			return err
		}
	}
	printContextBanner(cmd, ctx)
	if err := tableui.PrintFromCmd(cmd, rows, "column,standard_field,source,matched_text,similarity,table_count"); err != nil {
		return err
	}
	if result.SimilarityUnavailable != nil && *result.SimilarityUnavailable {
		_, err := fmt.Fprintln(cmd.ErrOrStderr(), "Similarity suggestions unavailable; name and other-table matches are shown.")
		return err
	}
	return nil
}

func standardFieldSuggestionRows(columns []api.ColumnConceptSuggestionsSchema) []conceptSuggestionRow {
	rows := make([]conceptSuggestionRow, 0)
	for _, column := range columns {
		if len(column.Suggestions) == 0 {
			rows = append(rows, conceptSuggestionRow{Column: column.DatasetCol})
		}
		for _, suggestion := range column.Suggestions {
			row := conceptSuggestionRow{Column: column.DatasetCol, StandardField: suggestion.Concept.Slug, Source: string(suggestion.Source), TableCount: suggestion.TableCount}
			if suggestion.MatchedText != nil {
				row.MatchedText = *suggestion.MatchedText
			}
			if suggestion.Similarity != nil {
				row.Similarity = fmt.Sprintf("%.3f", *suggestion.Similarity)
			}
			rows = append(rows, row)
		}
	}
	return rows
}
