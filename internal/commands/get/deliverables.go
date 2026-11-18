package get

import (
	"strings"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/deliverables"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/pkg/tableui"
	"github.com/spf13/cobra"
)

var validDeliverableSortFields = []string{"created_at", "name", "updated_at"}

// NewDeliverablesCommand creates the get deliverables command.
func NewDeliverablesCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deliverables",
		Short: "List deliverables",
		Long: `List the current organization's deliverables, most recently updated first.

Deliverables in the trash are left out unless --include-archived or --archived
asks for them. --table keeps the deliverables whose enabled revision reads the
table, or that have a run, not cancelled, reading it.

The table view shows each deliverable's slug, whether a revision is enabled,
and its latest run's state and parameters. JSON and YAML print the API records
in a List envelope; -o name prints one deliverable/<uuid> per deliverable.`,
		Annotations: catalog.For("deliverable"),
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			params, err := parseDeliverablesParams(cmd)
			if err != nil {
				return err
			}
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}
			if tableID, ok, err := cmdutil.OptionalTableID(ctx, cmd, ""); err != nil {
				return err
			} else if ok {
				params.MemberDatasetID = tableID
			}
			c := deliverables.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			page, err := c.List(ctx.Credential.AccessToken, params)
			if err != nil {
				return err
			}
			var items []deliverables.ListItem
			if page.Results != nil {
				items = *page.Results
			}
			if len(items) == 0 {
				skip, err := printEmptyResult(cmd, ctx, "deliverables", []deliverables.Display{}, page.TotalRows)
				if err != nil || skip {
					return err
				}
			}
			printContextBanner(cmd, ctx)
			lp := listPage{noun: "deliverables", page: &params.Page, limit: &params.Limit, total: page.TotalRows}
			return printListPage(cmd, items, lp, func(data any) error {
				if list, ok := data.(output.List); ok {
					format, _ := cmd.Flags().GetString("output")
					return output.WriteRaw(cmd.OutOrStdout(), format, list)
				}
				columns := deliverables.Columns
				if params.IncludeArchived || params.OnlyArchived {
					columns = deliverables.TrashColumns
				}
				return tableui.PrintFromCmd(cmd, deliverables.Rows(items), columns)
			})
		},
	}
	cmd.Flags().String("search", "", "Search names and slugs (minimum 3 characters)")
	cmd.Flags().Bool("include-archived", false, "Include deliverables in the trash")
	cmd.Flags().Bool("archived", false, "List only deliverables in the trash")
	cmdutil.AddTableFlags(cmd, "Filter by table")
	cmd.Flags().String("order-by", "updated_at", "Sort by updated_at, created_at, or name")
	cmd.Flags().Bool("reverse", false, "Sort ascending instead of the default descending (newest first, or Z to A by name)")
	cmd.Flags().Int("limit", 20, "Maximum deliverables per page")
	cmd.Flags().Int("page", 1, "Page number")
	cmd.Flags().String("columns", "", "Comma-separated table columns. Available: "+strings.Join(cmdutil.DeclareColumns(cmd, []deliverables.Display(nil)), ", "))
	return cmd
}

func parseDeliverablesParams(cmd *cobra.Command) (deliverables.ListParams, error) {
	var params deliverables.ListParams
	page, limit, err := paginationFlags(cmd)
	if err != nil {
		return params, err
	}
	params.Page, params.Limit = page, limit
	if params.Search, err = parseSearchFlag(cmd); err != nil {
		return params, err
	}
	params.OrderBy, _ = cmd.Flags().GetString("order-by")
	if err := validateSortField(params.OrderBy, validDeliverableSortFields); err != nil {
		return params, err
	}
	params.Reverse, _ = cmd.Flags().GetBool("reverse")
	if err := cmdutil.ValidateConflictingFlags(cmd, []string{"archived", "include-archived"}); err != nil {
		return params, err
	}
	params.IncludeArchived, _ = cmd.Flags().GetBool("include-archived")
	params.OnlyArchived, _ = cmd.Flags().GetBool("archived")
	return params, cmdutil.ValidateOptionalTableSelector(cmd, "")
}
