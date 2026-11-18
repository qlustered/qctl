package get

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/composite_run_jobs"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/ingestion"
	"github.com/qlustered/qctl/internal/output"
	"github.com/spf13/cobra"
)

// validCompositeRunJobSortFields mirrors api.CompositeRunJobOrderBy exactly,
// because the value is cast straight to that enum and sent as order_by.
// Spelled with the generated constants so a regenerated client that renames or
// drops a member breaks the build rather than letting this list drift into
// advertising a field the API rejects.
var validCompositeRunJobSortFields = []string{
	string(api.CompositeRunJobOrderByCreatedAt),
	string(api.CompositeRunJobOrderByFinishedAt),
	string(api.CompositeRunJobOrderByID),
	string(api.CompositeRunJobOrderByReason),
	string(api.CompositeRunJobOrderByStartedAt),
	string(api.CompositeRunJobOrderByState),
	string(api.CompositeRunJobOrderByUpdatedAt),
}

// jobStateFilterValues is what a `state` query parameter typed as
// IngestionJobState accepts: the whole enum. Both composite listing routes
// declare that type (get_composite_run_jobs, composite_run_job_view.py;
// list_composite_dry_runs, composite_dry_run_view.py), so a state their jobs
// never reach is still accepted by the server, and qctl accepts it too and
// matches nothing rather than inventing a stricter contract than the API.
//
// A composite run job only ever reaches eight of these — created, pending,
// running, requeued, finished, stopped, error and killed, written across
// cettings/.../composite_controller/composite_run_job_controller.py
// (create_composite_run_job_from_msg, claim_composite_run_job_for_run,
// reset_composite_run_job_for_requeue, mark_composite_run_job_killed_as_wedged,
// record_composite_run_finished, record_composite_run_stopped,
// record_composite_run_error_minimal) and cmapper/.../composite_recompute_job.py
// (run_composite_run_job).
var jobStateFilterValues = ingestion.JobStates()

// compositeRunJobColumns is the default table view: what the run was, what it
// was for, and how far it got.
const compositeRunJobColumns = "id,state,reason,changed_dataset_id,completed_target_count,affected_target_count,created_at,finished_at"

// NewCompositeRunJobsCommand creates the get composite-run-jobs command.
func NewCompositeRunJobsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "composite-run-jobs",
		Short: "List composite recompute runs",
		Long: `List the composite recompute history this organization's tables produced.

A composite run job is one rebuild of a composite (linked) table after
something it depends on changed -- a finished upstream ingest, a clean-value
overwrite, an undo, a newly enabled composite spec revision -- or on request,
through 'qctl run composite-run-job'. It is a real run, not a preview: this is
not the dry-run family (` + "`qctl get dry-run-jobs`" + `,
` + "`qctl describe dry-run-job`" + `), which previews rules before they are applied.

Examples:
  qctl get composite-run-jobs
  qctl get composite-run-jobs --state error --state killed
  qctl get composite-run-jobs --table orders
  qctl get composite-run-jobs --reason revision_enabled
  qctl get composite-run-jobs --group-id 550e8400-e29b-41d4-a716-446655440000`,
		Annotations: catalog.For("composite-run-job"),
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cmdutil.ValidateOptionalTableSelector(cmd, ""); err != nil {
				return err
			}
			params, err := parseCompositeRunJobsParams(cmd)
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
				params.ChangedDatasetID = &datasetID
			}

			client, err := composite_run_jobs.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			if err != nil {
				return fmt.Errorf("failed to create client: %w", err)
			}

			return fetchAndPrintCompositeRunJobs(client, ctx.Credential.AccessToken, params, cmd, ctx)
		},
	}

	addCompositeRunJobsFlags(cmd)
	return cmd
}

// parseCompositeRunJobsParams extracts and validates parameters from flags,
// except the --table/--table-id filter.
func parseCompositeRunJobsParams(cmd *cobra.Command) (composite_run_jobs.GetCompositeRunJobsParams, error) {
	var params composite_run_jobs.GetCompositeRunJobsParams

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
		if err := validateSortField(sortBy, validCompositeRunJobSortFields); err != nil {
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

	if reason, _ := cmd.Flags().GetString("reason"); reason != "" {
		params.Reason = &reason
	}

	return params, nil
}

// optionalUUIDFlag parses a UUID-typed string flag, nil when it is empty. A
// malformed value is bad usage (exit 2).
func optionalUUIDFlag(cmd *cobra.Command, name string) (*openapi_types.UUID, error) {
	raw, _ := cmd.Flags().GetString(name)
	if raw == "" {
		return nil, nil
	}
	parsed, err := uuid.Parse(raw)
	if err != nil {
		return nil, qerrors.NewBadUsagef("invalid --%s %q: must be a UUID", name, raw)
	}
	id := parsed
	return &id, nil
}

// fetchAndPrintCompositeRunJobs fetches and prints composite run jobs.
func fetchAndPrintCompositeRunJobs(
	client *composite_run_jobs.Client,
	accessToken string,
	params composite_run_jobs.GetCompositeRunJobsParams,
	cmd *cobra.Command,
	ctx *cmdutil.CommandContext,
) error {
	setDefaultColumns(cmd, compositeRunJobColumns)

	resp, err := client.GetCompositeRunJobs(accessToken, params)
	if err != nil {
		return fmt.Errorf("failed to get composite run jobs: %w", err)
	}

	if len(resp.Results) == 0 {
		skip, err := printEmptyResult(cmd, ctx, "composite run jobs",
			[]composite_run_jobs.CompositeRunJobTiny{}, resp.TotalRows)
		if err != nil {
			return err
		}
		if skip {
			return nil
		}
	}
	printContextBanner(cmd, ctx)

	// error_message is usually a stringified exception cut at 2000 characters,
	// and an alert's str() is its Markdown message, value markers included
	// (atlas cettings/cettings/controllers/composite_controller/composite_run_job_controller.py).
	printer, err := output.NewPrinterFromCmdWithMarkdown(cmd, []string{"error_message"})
	if err != nil {
		return fmt.Errorf("failed to create output printer: %w", err)
	}
	lp := listPage{noun: "composite run jobs", page: &params.Page, limit: &params.Limit, total: resp.TotalRows, more: resp.Next != nil}
	return printListPage(cmd, resp.Results, lp, func(data any) error {
		if err := printer.Print(data); err != nil {
			return fmt.Errorf("failed to print output: %w", err)
		}
		return nil
	})
}

// addCompositeRunJobsFlags adds all flags for the composite-run-jobs command.
func addCompositeRunJobsFlags(cmd *cobra.Command) {
	// Pagination control
	cmd.Flags().Int("limit", 100, "Maximum number of composite run jobs to return")
	cmd.Flags().Int("page", 1, "Page number")

	// Sorting. Built from the allowlist so --help can never advertise a field
	// the validator (and therefore the API) rejects.
	cmd.Flags().String("order-by", "id",
		"Order by field ("+strings.Join(validCompositeRunJobSortFields, ", ")+")")
	// The endpoint's own default is descending, so this flag flips it to
	// ascending -- see the note in the domain client.
	cmd.Flags().Bool("reverse", false, "Reverse the sort order (the server's default is newest first)")

	// Filtering (server-side)
	cmd.Flags().StringSlice("state", nil,
		"Filter by state, repeatable ("+strings.Join(jobStateFilterValues, ", ")+")")
	cmdutil.AddTableFlags(cmd, "Filter by the changed table")
	cmd.Flags().String("group-id", "", "Filter by composite spec group UUID (named as a target, or resolved as one)")
	cmd.Flags().String("reason", "", "Filter by invalidation reason (exact match; see qctl explain composite-run-job)")

	// Output control
	cmd.Flags().String("columns", "", "Comma-separated list of columns to display (table format only)\n"+
		"Available: "+strings.Join(cmdutil.DeclareColumns(cmd, []composite_run_jobs.CompositeRunJobTiny(nil)), ", "))
}
