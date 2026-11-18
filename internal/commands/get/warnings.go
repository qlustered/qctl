package get

import (
	"fmt"
	"sort"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/warnings"
	"github.com/spf13/cobra"
)

var validWarningSortFields = []string{
	"id", "dataset_name", "dataset_id", "data_source_model_id",
	"data_source_model_name", "issue_type", "count", "impact_score", "msg", "created_at", "assigned_user",
}

// NewWarningsCommand creates the get warnings command
func NewWarningsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:         "warnings",
		Short:       "List warnings",
		Long:        `List all warnings in the current organization.`,
		Annotations: catalog.For("warning"),
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Parse and validate params
			params, err := parseWarningsParams(cmd)
			if err != nil {
				return err
			}

			// Bootstrap auth context
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}
			if err := applyIssueTableFilter(ctx, cmd, &params.DatasetID); err != nil {
				return err
			}

			// Create client and fetch results
			client := warnings.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			resp, err := client.GetWarnings(ctx.Credential.AccessToken, params)
			if err != nil {
				return fmt.Errorf("failed to get warnings: %w", err)
			}

			if len(resp.Results) == 0 {
				skip, err := printEmptyResult(cmd, ctx, "warnings", []warnings.WarningTiny{}, resp.TotalRows)
				if err != nil {
					return err
				}
				if skip {
					return nil
				}
			}
			printContextBanner(cmd, ctx)

			lp := listPage{noun: "warnings", page: &params.Page, limit: &params.Limit, total: resp.TotalRows, more: resp.Next != nil}
			return printListPage(cmd, resp.Results, lp, func(data any) error {
				return printWarningsResults(cmd, data, heldIssueColumns(warningColumns, params.HasChangeProposal, params.ChangeProposalStates))
			})
		},
	}

	addWarningsFlags(cmd)
	return cmd
}

// parseWarningsParams extracts and validates parameters from command flags. It
// validates --table and --table-id but leaves them unapplied.
func parseWarningsParams(cmd *cobra.Command) (warnings.GetWarningsParams, error) {
	var params warnings.GetWarningsParams

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

	if sortBy != "" {
		if err := validateSortField(sortBy, validWarningSortFields); err != nil {
			return params, err
		}
	}

	// Filters
	if err := parseWarningFilters(cmd, &params); err != nil {
		return params, err
	}

	return params, nil
}

// parseWarningFilters handles warning filter flags. An ID flag the user passed
// is applied or refused — never dropped, which would widen the listing to the
// whole organization without saying so.
func parseWarningFilters(cmd *cobra.Command, params *warnings.GetWarningsParams) error {
	search, err := parseSearchFlag(cmd)
	if err != nil {
		return err
	}
	if search != "" {
		params.SearchQuery = &search
	}

	if cmd.Flags().Changed("resolved") {
		resolved, _ := cmd.Flags().GetBool("resolved")
		params.Resolved = &resolved
	}
	if issueType, _ := cmd.Flags().GetString("issue-type"); issueType != "" {
		warningType := api.IssueTypeEnum(issueType)
		params.IssueType = &warningType
	}

	datasetID, haveDatasetID, err := validateIssueTableSelectorFlags(cmd)
	if err != nil {
		return err
	}
	if haveDatasetID {
		params.DatasetID = &datasetID
	}

	dataSourceID, haveDataSourceID, err := cmdutil.PositiveIDFlag(cmd, "data-source-id")
	if err != nil {
		return err
	}
	if haveDataSourceID {
		params.DataSourceModelID = &dataSourceID
	}

	params.HasChangeProposal, params.ChangeProposalStates, err = parseChangeProposalFilters(cmd)
	return err
}

// warningColumns are the default `get warnings` columns.
const warningColumns = "id,dataset_name,issue_type,count,impact_score,msg,created_at"

// printWarningsResults prints the warnings, with defaultColumns for table format.
func printWarningsResults(cmd *cobra.Command, data any, defaultColumns string) error {
	setDefaultColumns(cmd, defaultColumns)

	// Use markdown rendering for the msg field
	printer, err := output.NewPrinterFromCmdWithMarkdown(cmd, []string{"msg"})
	if err != nil {
		return fmt.Errorf("failed to create output printer: %w", err)
	}

	if results, ok := data.([]warnings.WarningTiny); ok && isTableFormat(cmd) {
		data = issueCellRows(results)
	}
	if err := printer.Print(data); err != nil {
		return fmt.Errorf("failed to print output: %w", err)
	}

	return nil
}

// completeWarningIssueType completes warning issue types from generated metadata.
func completeWarningIssueType(_ *cobra.Command, _ []string, prefix string) ([]string, cobra.ShellCompDirective) {
	var names []string
	for name := range api.IssueTypeWarningActions {
		if strings.HasPrefix(name, prefix) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, cobra.ShellCompDirectiveNoFileComp
}

// addWarningsFlags adds all flags for the warnings command.
func addWarningsFlags(cmd *cobra.Command) {
	// Pagination control
	cmd.Flags().Int("limit", 100, "Maximum number of warnings to return")
	cmd.Flags().Int("page", 1, "Page number")

	// Sorting
	cmd.Flags().String("order-by", "id", "Order by field ("+strings.Join(validWarningSortFields, ", ")+")")
	cmd.Flags().Bool("reverse", false, "Reverse the sort order")

	// Filtering (server-side)
	cmd.Flags().Bool("resolved", false, "Filter by resolution status (default: false)")
	cmd.Flags().String("issue-type", "", "Filter by issue type (e.g. user_submitted); validated by the server so newly added types are accepted")
	if err := cmd.RegisterFlagCompletionFunc("issue-type", completeWarningIssueType); err != nil {
		panic(err)
	}
	cmdutil.AddTableFlags(cmd, "Filter by table")
	// --dataset-id was this command's only spelling; keep it working for
	// scripts, hidden so --table-id is the one --help teaches. MarkHidden, not
	// MarkDeprecated: a per-invocation stderr notice is noise on a filter that
	// mostly runs unattended.
	cmdutil.RefInt(cmd.Flags(), "dataset-id", "table", "Deprecated alias for --table-id")
	if err := cmd.Flags().MarkHidden("dataset-id"); err != nil {
		panic(err) // a hidden flag that is not registered is a programming error
	}
	cmdutil.RefInt(cmd.Flags(), "data-source-id", "cloud-source", "Filter by data source ID")
	cmd.Flags().String("search", "", "Search warnings by query")
	addChangeProposalFilterFlags(cmd, "warnings")

	// Output control
	cmd.Flags().String("columns", "", "Comma-separated list of columns to display (table format only)\n"+
		"Available: "+strings.Join(cmdutil.DeclareColumns(cmd, []warnings.WarningTiny(nil)), ", "))
}
