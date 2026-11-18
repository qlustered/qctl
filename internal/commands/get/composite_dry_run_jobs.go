package get

import (
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/composite_dry_runs"
	"github.com/qlustered/qctl/internal/ingestion"
	"github.com/qlustered/qctl/internal/output"
	"github.com/spf13/cobra"
)

// validCompositeDryRunJobSortFields mirrors api.CompositeDryRunJobOrderBy
// exactly, because the value is cast straight to that enum and sent as
// order_by. Spelled with the generated constants so a regenerated client that
// renames or drops a member breaks the build.
var validCompositeDryRunJobSortFields = []string{
	string(api.CompositeDryRunJobOrderByCreatedAt),
	string(api.CompositeDryRunJobOrderByID),
	string(api.CompositeDryRunJobOrderBySnapshotAt),
	string(api.CompositeDryRunJobOrderByState),
}

// compositeDryRunJobColumns is the default table view: what was previewed, how
// far it got, and whether anything blocks enabling it.
const compositeDryRunJobColumns = "id,state,downstream_dataset_id,composite_spec_revision_id,max_spine_rows,activation_blocker_count,created_at,finished_at"

// NewCompositeDryRunJobsCommand creates the get composite-dry-run-jobs command.
func NewCompositeDryRunJobsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "composite-dry-run-jobs",
		Short: "List composite (linked-table) dry runs",
		Long: `List the dry runs this organization's composite (linked) tables were previewed with.

A composite dry run samples the spine table, joins it against the other source
tables and reports what the linked table would contain, without writing it.
` + "`qctl apply -f`" + ` of a LinkedTable starts one when the revision it converges is
not enabled and has no current successful dry run, and
` + "`qctl create composite-dry-run-job`" + ` starts one by hand. This is not the
rule dry-run family (` + "`qctl get dry-run-jobs`" + `), which previews rules on
one table.

Examples:
  qctl get composite-dry-run-jobs
  qctl get composite-dry-run-jobs --table orders_enriched
  qctl get composite-dry-run-jobs --state error --state stopped
  qctl get composite-dry-run-jobs --revision-id 550e8400-e29b-41d4-a716-446655440000`,
		Annotations: catalog.For("composite-dry-run-job"),
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cmdutil.ValidateOptionalTableSelector(cmd, ""); err != nil {
				return err
			}
			params, err := parseCompositeDryRunJobsParams(cmd)
			if err != nil {
				return err
			}

			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}
			datasetID, haveTable, err := cmdutil.OptionalTableID(ctx, cmd, "")
			if err != nil {
				return err
			}
			if haveTable {
				params.DownstreamDatasetID = &datasetID
			}

			client, err := composite_dry_runs.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			if err != nil {
				return fmt.Errorf("failed to create client: %w", err)
			}

			return fetchAndPrintCompositeDryRunJobs(client, ctx.Credential.AccessToken, params, cmd, ctx)
		},
	}

	addCompositeDryRunJobsFlags(cmd)
	return cmd
}

// parseCompositeDryRunJobsParams extracts and validates parameters from flags,
// except the --table/--table-id filter.
func parseCompositeDryRunJobsParams(cmd *cobra.Command) (composite_dry_runs.ListParams, error) {
	var params composite_dry_runs.ListParams

	sortBy, _ := cmd.Flags().GetString("order-by")
	params.OrderBy = sortBy

	reverse, _ := cmd.Flags().GetBool("reverse")
	params.Reverse = reverse

	page, limit, err := paginationFlags(cmd)
	if err != nil {
		return params, err
	}
	params.Page = page
	params.Limit = limit

	if sortBy != "" {
		if err := validateSortField(sortBy, validCompositeDryRunJobSortFields); err != nil {
			return params, err
		}
	}

	states, _ := cmd.Flags().GetStringSlice("state")
	for _, state := range states {
		if err := ingestion.ValidateJobState("--state", state); err != nil {
			return params, err
		}
	}
	params.States = states

	groupID, err := optionalUUIDFlag(cmd, "group-id")
	if err != nil {
		return params, err
	}
	params.GroupID = groupID

	revisionID, err := optionalUUIDFlag(cmd, "revision-id")
	if err != nil {
		return params, err
	}
	params.RevisionID = revisionID

	return params, nil
}

// fetchAndPrintCompositeDryRunJobs fetches and prints composite dry runs.
func fetchAndPrintCompositeDryRunJobs(
	client *composite_dry_runs.Client,
	accessToken string,
	params composite_dry_runs.ListParams,
	cmd *cobra.Command,
	ctx *cmdutil.CommandContext,
) error {
	setDefaultColumns(cmd, compositeDryRunJobColumns)

	resp, err := client.List(accessToken, params)
	if err != nil {
		return fmt.Errorf("failed to get composite dry run jobs: %w", err)
	}

	if len(resp.Results) == 0 {
		skip, err := printEmptyResult(cmd, ctx, "composite dry run jobs",
			[]composite_dry_runs.JobTiny{}, resp.TotalRows)
		if err != nil {
			return err
		}
		if skip {
			return nil
		}
	}
	printContextBanner(cmd, ctx)

	// summary is Markdown, value markers included (build_composite_markdown,
	// cmapper/cmapper/business_report.py).
	printer, err := output.NewPrinterFromCmdWithMarkdown(cmd, []string{"summary"})
	if err != nil {
		return fmt.Errorf("failed to create output printer: %w", err)
	}
	lp := listPage{noun: "composite dry run jobs", page: &params.Page, limit: &params.Limit, total: resp.TotalRows, more: resp.Next != nil}
	return printListPage(cmd, resp.Results, lp, func(data any) error {
		if err := printer.Print(data); err != nil {
			return fmt.Errorf("failed to print output: %w", err)
		}
		return nil
	})
}

// addCompositeDryRunJobsFlags adds all flags for the composite-dry-run-jobs command.
func addCompositeDryRunJobsFlags(cmd *cobra.Command) {
	// Pagination control
	cmd.Flags().Int("limit", 100, "Maximum number of composite dry run jobs to return")
	cmd.Flags().Int("page", 1, "Page number")

	// Sorting. Built from the allowlist so --help can never advertise a field
	// the validator (and therefore the API) rejects.
	cmd.Flags().String("order-by", "id",
		"Order by field ("+strings.Join(validCompositeDryRunJobSortFields, ", ")+")")
	// The endpoint's own default is descending, so this flag flips it to
	// ascending -- see the note in the domain client.
	cmd.Flags().Bool("reverse", false, "Reverse the sort order (the server's default is newest first)")

	// Filtering (server-side)
	cmd.Flags().StringSlice("state", nil,
		"Filter by state, repeatable ("+strings.Join(jobStateFilterValues, ", ")+")")
	cmdutil.AddTableFlags(cmd, "Filter by the linked (downstream) table")
	cmd.Flags().String("group-id", "", "Filter by composite spec group UUID")
	cmd.Flags().String("revision-id", "", "Filter by composite spec revision UUID")

	// Output control
	cmd.Flags().String("columns", "", "Comma-separated list of columns to display (table format only)\n"+
		"Available: "+strings.Join(cmdutil.DeclareColumns(cmd, []composite_dry_runs.JobTiny(nil)), ", "))
}
