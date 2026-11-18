package get

import (
	"slices"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/pkg/tableui"
	"github.com/qlustered/qctl/internal/table_series"
	"github.com/spf13/cobra"
)

// NewTableSeriesSuggestionCommand creates the get table-series-suggestion command.
func NewTableSeriesSuggestionCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "table-series-suggestion [file-name]",
		Short: "Suggest a series name and period for a file name",
		Long: `Ask the server which series name, frequency and period a file name suggests,
and the display name and table name that period's table would get.

Only the file name's last path segment is sent; the file is never read.
--series-name, --frequency and --period-key override what the file name
suggests. Give a file name, one of those flags, or both. Nothing is written.

PERIOD-SOURCE says whether the period came from a period named in the file
name (file_name) or from an export date (export_date); it is "-" for a
--period-key. NAME and TABLE-NAME are "-" without both a series name and a
period. JSON and YAML print the server's object.`,
		Example: `  qctl get table-series-suggestion "Orders export 2026-11-03.csv"
  qctl get table-series-suggestion orders_2026_Q3.xlsx --series-name "Quarterly orders"`,
		Annotations: cmdutil.WithFormats(catalog.For("table-series"), "table", "json", "yaml"),
		Args:        cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			request, err := tableSeriesSuggestionRequest(cmd, args)
			if err != nil {
				return err
			}
			if err := validateColumnsFlag(cmd, []table_series.SuggestionDisplay(nil)); err != nil {
				return err
			}
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}
			c := table_series.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			suggestion, err := c.Suggest(ctx.Credential.AccessToken, request)
			if err != nil {
				return err
			}
			format, _ := cmd.Flags().GetString("output")
			if format == string(output.FormatJSON) || format == string(output.FormatYAML) {
				return output.WriteRaw(cmd.OutOrStdout(), format, suggestion)
			}
			printContextBanner(cmd, ctx)
			return tableui.PrintFromCmd(cmd, []table_series.SuggestionDisplay{table_series.SuggestionRow(suggestion)}, table_series.SuggestionColumns)
		},
	}
	cmd.Flags().String("series-name", "", "Series name to use instead of the one the file name suggests")
	cmd.Flags().String("frequency", "", "Period frequency: "+strings.Join(api.EnumValues[api.SeriesFrequency](), "|"))
	cmd.Flags().String("period-key", "", "Period to use instead of the one the file name suggests (2026-10 or 2026-Q4)")
	cmd.Flags().String("columns", "", "Comma-separated table columns ("+strings.Join(cmdutil.DeclareColumns(cmd, []table_series.SuggestionDisplay(nil)), ",")+")")
	return cmd
}

// tableSeriesSuggestionRequest builds the suggestion body from args and flags, or returns a usage error.
func tableSeriesSuggestionRequest(cmd *cobra.Command, args []string) (api.TableSeriesSuggestionRequestSchema, error) {
	var request api.TableSeriesSuggestionRequestSchema
	if len(args) == 1 {
		name := fileNameSegment(args[0])
		if name == "" {
			return request, qerrors.NewBadUsagef("file name %q ends in a path separator", args[0])
		}
		if len([]rune(name)) > 1024 {
			return request, qerrors.NewBadUsage("file name must be at most 1024 characters")
		}
		request.FileName = &name
	}
	if cmd.Flags().Changed("series-name") {
		name, _ := cmd.Flags().GetString("series-name")
		if name == "" || len([]rune(name)) > 200 {
			return request, qerrors.NewBadUsage("--series-name must be 1–200 characters")
		}
		request.SeriesName = &name
	}
	if cmd.Flags().Changed("frequency") {
		value, _ := cmd.Flags().GetString("frequency")
		values := api.EnumValues[api.SeriesFrequency]()
		if !slices.Contains(values, value) {
			return request, qerrors.NewBadUsagef("--frequency must be one of: %s", strings.Join(values, ", "))
		}
		frequency := api.SeriesFrequency(value)
		request.Frequency = &frequency
	}
	if cmd.Flags().Changed("period-key") {
		key, _ := cmd.Flags().GetString("period-key")
		if err := table_series.ValidatePeriodKey(key); err != nil {
			return request, err
		}
		request.PeriodKey = &key
	}
	if request == (api.TableSeriesSuggestionRequestSchema{}) {
		return request, qerrors.NewBadUsage("give a file name, --series-name, --frequency or --period-key")
	}
	return request, nil
}

// fileNameSegment returns path's text after its last "/" or "\".
func fileNameSegment(path string) string {
	return path[strings.LastIndexAny(path, `/\`)+1:]
}
