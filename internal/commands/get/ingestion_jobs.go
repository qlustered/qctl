package get

import (
	"context"
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/ingestion"
	"github.com/qlustered/qctl/internal/output"
	"github.com/spf13/cobra"
)

// validIngestionJobSortFields mirrors api.IngestionJobOrderBy exactly, because
// parseIngestionJobsParams' value is cast straight to that enum and sent as
// order_by — the server is the only thing that enforces it. Spelled with the
// generated constants so a regenerated client that renames or drops a member
// breaks the build instead of silently drifting back to the state this
// replaced, where "table_id", "created_at", "started_at" and "finished_at"
// passed validation here and were then rejected by the API.
var validIngestionJobSortFields = []string{
	string(api.IngestionJobOrderByAlertItemID),
	string(api.IngestionJobOrderByBadRowsCount),
	string(api.IngestionJobOrderByCleanRowsCount),
	string(api.IngestionJobOrderByDatasetID),
	string(api.IngestionJobOrderByDatasetName),
	string(api.IngestionJobOrderByDeletedLinesCount),
	string(api.IngestionJobOrderByFileName),
	string(api.IngestionJobOrderByID),
	string(api.IngestionJobOrderByIsDryRun),
	string(api.IngestionJobOrderByKey),
	string(api.IngestionJobOrderByMsg),
	string(api.IngestionJobOrderByState),
	string(api.IngestionJobOrderByStoredItemID),
	string(api.IngestionJobOrderByTryCount),
	string(api.IngestionJobOrderByUpdatedAt),
	string(api.IngestionJobOrderByWhodunitID),
}

// ingestionJobsDefaultColumns are the table columns without --columns.
const ingestionJobsDefaultColumns = "id,dataset_id,state,kind,file_name,updated_at"

// NewIngestionJobsCommand creates the get ingestion-jobs command
func NewIngestionJobsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:         "ingestion-jobs",
		Short:       "List ingestion jobs",
		Long:        `List all ingestion jobs in the current organization.`,
		Annotations: catalog.For("ingestion-job"),
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cmdutil.ValidateOptionalTableSelector(cmd, ""); err != nil {
				return err
			}
			// Validate conflicting flags
			if err := cmdutil.ValidateConflictingFlags(cmd, []string{"state", "states"}); err != nil {
				return err
			}

			// An interval below 1 used to reach time.NewTicker and panic.
			watchInterval, err := cmdutil.PositiveSecondsFlag(cmd, "watch-interval")
			if err != nil {
				return err
			}
			watch, watching, err := lwWatchFlags(cmd, watchInterval)
			if err != nil {
				return err
			}

			// Parse and validate params
			params, err := parseIngestionJobsParams(cmd)
			if err != nil {
				return err
			}

			// Bootstrap auth context
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}
			datasetID, haveTable, err := cmdutil.OptionalTableID(ctx, cmd, "")
			if err != nil {
				return err
			}
			if haveTable {
				params.DatasetID = &datasetID
			}

			// Create client
			client, err := ingestion.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			if err != nil {
				return err
			}
			token := ctx.Credential.AccessToken

			if watching {
				return watchIngestionJobs(client, token, params, cmd, watch)
			}

			// Get and print results
			return fetchAndPrintIngestionJobs(client, token, params, cmd, ctx)
		},
	}

	addIngestionJobsFlags(cmd)
	return cmd
}

// parseIngestionJobsParams extracts and validates parameters from command
// flags, except the --table/--table-id filter.
func parseIngestionJobsParams(cmd *cobra.Command) (ingestion.GetIngestionJobsParams, error) {
	var params ingestion.GetIngestionJobsParams

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

	// State filters
	states, err := stateFlags(cmd, ingestion.JobStates())
	if err != nil {
		return params, err
	}
	params.States = states

	if sortBy != "" {
		if err := validateSortField(sortBy, validIngestionJobSortFields); err != nil {
			return params, err
		}
	}

	// Other filters
	if err := parseIngestionJobFilters(cmd, &params); err != nil {
		return params, err
	}

	return params, nil
}

// parseIngestionJobFilters handles the non-state filter flags other than the
// table selector. An ID flag the user passed is applied or refused — never
// dropped, which would widen the listing to the whole organization without
// saying so.
func parseIngestionJobFilters(cmd *cobra.Command, params *ingestion.GetIngestionJobsParams) error {
	dataSourceID, haveDataSourceID, err := cmdutil.PositiveIDFlag(cmd, "cloud-source-id")
	if err != nil {
		return err
	}
	if haveDataSourceID {
		params.DataSourceID = &dataSourceID
	}
	fileID, haveFileID, err := cmdutil.PositiveIDFlag(cmd, "file-id")
	if err != nil {
		return err
	}
	if haveFileID {
		params.StoredItemID = &fileID
	}
	rawKinds, _ := cmd.Flags().GetStringArray("kind")
	if params.Kinds, err = ingestion.ParseJobKinds("--kind", rawKinds); err != nil {
		return err
	}

	if cmd.Flags().Changed("is-dry-run") {
		isDryRun, _ := cmd.Flags().GetBool("is-dry-run")
		params.IsDryRun = &isDryRun
	}
	if createdBy, _ := cmd.Flags().GetString("created-by"); createdBy != "" {
		id, err := cmdutil.ParseUUIDArg(createdBy, "--created-by user ID")
		if err != nil {
			return err
		}
		params.WhodunitID = &id
	}
	return nil
}

// validateSortField checks if the sort field is valid.
//
// A rejected --order-by is a bad-usage error (exit 2), not a generic one
// (exit 1), matching --columns: both are typos in what the user typed, and a
// script that reads exit 1 cannot tell them from a server failure it should
// retry. Callers may wrap this with fmt.Errorf("...: %w", err) — the code
// survives unwrapping — but never with %v, which demotes it to 1.
func validateSortField(sortBy string, validFields []string) error {
	for _, field := range validFields {
		if sortBy == field {
			return nil
		}
	}
	return qerrors.NewBadUsagef("invalid sort field: %s (valid: %v)", sortBy, validFields)
}

// setDefaultColumns sets default columns for table output format.
func setDefaultColumns(cmd *cobra.Command, defaultColumns string) {
	outputFormat, _ := cmd.Flags().GetString("output")
	if outputFormat == string(output.FormatTable) || outputFormat == "" {
		columnsFlag, _ := cmd.Flags().GetString("columns")
		if columnsFlag == "" {
			cmd.Flags().Set("columns", defaultColumns)
		}
	}
}

// watchIngestionJobs polls the ingestion-jobs page, writing each change as
// the List the one-shot listing prints.
func watchIngestionJobs(
	client *ingestion.Client,
	accessToken string,
	params ingestion.GetIngestionJobsParams,
	cmd *cobra.Command,
	watch lwWatch,
) error {
	setDefaultColumns(cmd, ingestionJobsDefaultColumns)

	printer, err := output.NewPrinterFromCmdWithMarkdown(cmd, []string{"msg"})
	if err != nil {
		return fmt.Errorf("failed to create output printer: %w", err)
	}
	return watchList(cmd, printer, "ingestion jobs", watch, func(waitCtx context.Context) (any, error) {
		resp, err := client.GetIngestionJobsContext(waitCtx, accessToken, params)
		if err != nil {
			return nil, fmt.Errorf("failed to get ingestion jobs: %w", err)
		}
		return ingestionJobsPage(params, resp).list(resp.Results), nil
	})
}

// ingestionJobsPage is where resp sits among the ingestion jobs params asks for.
func ingestionJobsPage(params ingestion.GetIngestionJobsParams, resp *ingestion.IngestionJobsPage) listPage {
	return listPage{noun: "ingestion jobs", page: &params.Page, limit: &params.Limit, total: resp.TotalRows, more: resp.Next != nil}
}

// fetchAndPrintIngestionJobs fetches and prints ingestion jobs.
func fetchAndPrintIngestionJobs(
	client *ingestion.Client,
	accessToken string,
	params ingestion.GetIngestionJobsParams,
	cmd *cobra.Command,
	ctx *cmdutil.CommandContext,
) error {
	setDefaultColumns(cmd, ingestionJobsDefaultColumns)

	resp, err := client.GetIngestionJobs(accessToken, params)
	if err != nil {
		return fmt.Errorf("failed to get ingestion jobs: %w", err)
	}

	if len(resp.Results) == 0 {
		skip, err := printEmptyResult(cmd, ctx, "ingestion jobs", []ingestion.IngestionJobTiny{}, resp.TotalRows)
		if err != nil {
			return err
		}
		if skip {
			return nil
		}
	}
	printContextBanner(cmd, ctx)

	// Use markdown rendering for the msg field
	printer, err := output.NewPrinterFromCmdWithMarkdown(cmd, []string{"msg"})
	if err != nil {
		return fmt.Errorf("failed to create output printer: %w", err)
	}

	return printListPage(cmd, resp.Results, ingestionJobsPage(params, resp), func(data any) error {
		if err := printer.Print(data); err != nil {
			return fmt.Errorf("failed to print output: %w", err)
		}
		return nil
	})
}

// addIngestionJobsFlags adds all flags for the ingestion-jobs command.
func addIngestionJobsFlags(cmd *cobra.Command) {
	// Pagination control
	cmd.Flags().Int("limit", 100, "Maximum number of ingestion jobs to return")
	cmd.Flags().Int("page", 1, "Page number")

	// Sorting
	// Built from the allowlist so --help can never advertise a field the
	// validator (and therefore the API) rejects.
	cmd.Flags().String("order-by", "id",
		"Order by field ("+strings.Join(validIngestionJobSortFields, ", ")+")")
	cmd.Flags().Bool("reverse", false, "Reverse the sort order")

	// Filtering (server-side)
	cmd.Flags().String("state", "", "Filter by single state ("+strings.Join(ingestion.JobStates(), ", ")+")")
	cmd.Flags().String("states", "", "Filter by multiple states (comma-separated)")
	cmdutil.AddTableFlags(cmd, "Filter by table")
	cmdutil.RefInt(cmd.Flags(), "cloud-source-id", "cloud-source", "Filter by cloud source model ID")
	cmdutil.RefInt(cmd.Flags(), "file-id", "file", "Filter by file (stored item): its jobs and its parts' jobs")
	cmd.Flags().StringArray("kind", nil, "Filter by kind (repeatable): "+strings.Join(ingestion.JobKinds(), ", "))
	cmd.Flags().Bool("is-dry-run", false, "Filter by dry run status")
	cmd.Flags().String("created-by", "", "Filter by creator user UUID")

	// Watch mode
	cmd.Flags().Bool("watch", false, "Watch for changes (polls every --watch-interval seconds)")
	cmd.Flags().Int("watch-interval", 5, "Interval in seconds for watch mode")
	cmdutil.AddWatchTimeoutFlag(cmd, 0)

	// Output control
	cmd.Flags().String("columns", "", "Comma-separated list of columns to display (table format only)\n"+
		"Available: "+strings.Join(cmdutil.DeclareColumns(cmd, []ingestion.IngestionJobTiny(nil)), ", "))
}
