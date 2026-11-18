package get

import (
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/pkg/tableui"
	"github.com/qlustered/qctl/internal/stored_items"
	"github.com/spf13/cobra"
)

var validFilesSortFields = []string{
	"id", "file_name", "dataset_name", "data_source_model_name",
	"bad_rows_count", "clean_rows_count", "deleted_lines_count", "created_at",
}

// errCloudSourceNeedsTable is deliberately redundant with the cmdutil guard: it
// fires first and names this command's own flags, where cmdutil can only speak
// generically for all four of its callers.
//
// Bad usage (exit 2), not a generic error — the same class of mistake as the
// cmdutil guard it duplicates.
var errCloudSourceNeedsTable = qerrors.NewBadUsage("--cloud-source requires --table or --table-id (cloud source names are unique only within a table); use --cloud-source-id to address one directly")

// NewFilesCommand creates the 'get files' command
func NewFilesCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "files",
		Short: "List files (stored items)",
		Long: `List all files in the current organization. Files are stored items that have been uploaded for ingestion.

Only active files are listed by default. --deleted switches the listing to
deleted files ONLY: the server filter is exclusive, so active and deleted files
never appear together. Use it to recover the ID that 'undelete file' needs.`,
		Annotations: catalog.For("file"),
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cmdutil.ValidateOptionalTableSelector(cmd, ""); err != nil {
				return err
			}
			if err := cmdutil.ValidateConflictingFlags(
				cmd,
				[]string{"cloud-source-id", "cloud-source"},
			); err != nil {
				return err
			}

			// As with `get cloud-sources`, 0 is this command's documented
			// "unlimited".
			limit, err := cmdutil.NonNegativeCountFlag(cmd, "limit")
			if err != nil {
				return err
			}

			// Parse and validate params
			params, err := parseFilesParams(cmd)
			if err != nil {
				return err
			}

			// Bootstrap auth context
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}
			if err := resolveFilesFilters(cmd, ctx, &params); err != nil {
				return err
			}

			// Create client and fetch results
			client, err := stored_items.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			if err != nil {
				return fmt.Errorf("failed to create client: %w", err)
			}
			results, total, err := client.GetAllStoredItems(ctx.Credential.AccessToken, params, limit)

			// Same NO_TABLE_MESSAGE 404 as `get tables`: storage_view runs the
			// same dataset-association filter
			// (atlas get_stored_items, capi/capi/views/storage_view.py).
			var hint func() string
			if err != nil {
				// A NO_TABLE_MESSAGE that arrives after earlier pages already
				// returned rows is a mid-stream failure, not an empty list --
				// converting it would report a truncated listing as complete.
				if !apierror.IsNoVisibleRows(err) || len(results) > 0 {
					return fmt.Errorf("failed to fetch files: %w", err)
				}
				results = []stored_items.StoredItemTiny{}
				hint = noVisibleRowsHint(ctx)
			}

			if len(results) == 0 {
				skip, err := printEmptyResultWithHint(cmd, ctx, "files", []stored_items.StoredItemDisplay{}, total, hint)
				if err != nil {
					return err
				}
				if skip {
					return nil
				}
			}
			printContextBanner(cmd, ctx)

			lp := listPage{noun: "files", total: total}
			if limit > 0 {
				lp.limit = &limit
			}
			return printListPage(cmd, results, lp, func(data any) error {
				return printFilesResults(cmd, data)
			})
		},
	}

	addFilesFlags(cmd)
	return cmd
}

// parseFilesParams extracts and validates parameters from command flags. It
// validates --table and --cloud-source but leaves them unapplied.
func parseFilesParams(cmd *cobra.Command) (stored_items.GetStoredItemsParams, error) {
	var params stored_items.GetStoredItemsParams

	// Sorting
	sortBy, _ := cmd.Flags().GetString("order-by")
	params.OrderBy = sortBy

	reverse, _ := cmd.Flags().GetBool("reverse")
	params.Reverse = reverse

	if sortBy != "" {
		if err := validateSortField(sortBy, validFilesSortFields); err != nil {
			return params, err
		}
	}

	tableID, haveTableID, err := cmdutil.PositiveIDFlag(cmd, "table-id")
	if err != nil {
		return params, err
	}
	if haveTableID {
		params.DatasetID = &tableID
	}
	cloudSourceID, haveCloudSourceID, err := cmdutil.PositiveIDFlag(cmd, "cloud-source-id")
	if err != nil {
		return params, err
	}
	if haveCloudSourceID {
		params.DataSourceModelID = &cloudSourceID
	}
	tableName, _ := cmd.Flags().GetString("table")
	cloudSourceName, _ := cmd.Flags().GetString("cloud-source")
	// --table-id/--table and --cloud-source-id/--cloud-source are already
	// rejected as mutually exclusive by ValidateConflictingFlags in RunE.

	if cloudSourceName != "" && !haveTableID && tableName == "" {
		return params, errCloudSourceNeedsTable
	}

	// --deleted maps to the server's is_trashed equality filter: true returns
	// deleted files only. Sent only when the user set it, so the unset case
	// leaves the default to the server.
	if cmd.Flags().Changed("deleted") {
		deleted, _ := cmd.Flags().GetBool("deleted")
		params.IsTrashed = &deleted
	}

	return params, nil
}

// resolveFilesFilters resolves --table and --cloud-source to IDs. atlas
// capi/views/storage_view.py's get_stored_items has no exact name filter — only
// a fuzzy search_query OR'ed with a substring match on the file name — so
// names are resolved client-side, BEFORE the listing request. A name that does
// not resolve exits non-zero instead of returning a whole-org listing that
// looks like a legitimate answer.
func resolveFilesFilters(cmd *cobra.Command, ctx *cmdutil.CommandContext, params *stored_items.GetStoredItemsParams) error {
	tableName, _ := cmd.Flags().GetString("table")
	cloudSourceName, _ := cmd.Flags().GetString("cloud-source")
	if tableName == "" && cloudSourceName == "" {
		return nil
	}

	// A cloud-source-by-name lookup is scoped by table and its errors name the
	// table, so --cloud-source NAME forces table resolution even when the user
	// addressed the table by ID.
	var tableID int
	if params.DatasetID != nil {
		tableID = *params.DatasetID
	}
	datasetID, datasetName, err := cmdutil.ResolveTable(ctx, tableID, tableName)
	if err != nil {
		return err
	}
	params.DatasetID = &datasetID

	if cloudSourceName != "" {
		// Passing 0 for the ID makes the auto-detect branch — which would pick a
		// cloud source nobody asked for — unreachable.
		resolvedCSID, _, err := cmdutil.ResolveCloudSource(ctx, datasetID, datasetName, 0, cloudSourceName)
		if err != nil {
			return err
		}
		params.DataSourceModelID = &resolvedCSID
	}

	return nil
}

// printFilesResults prints files through their display projection, and a List
// of them as the API sent them.
func printFilesResults(cmd *cobra.Command, data any) error {
	if results, ok := data.([]stored_items.StoredItemTiny); ok {
		data = stored_items.ToDisplayList(results)
	}
	return tableui.PrintFromCmd(cmd, data, "id,file_name,dataset_name,data_source_model_name,routing,bad_rows_count,clean_rows_count,tags,created_at")
}

// addFilesFlags adds all flags for the files command.
func addFilesFlags(cmd *cobra.Command) {
	// Server/user overrides
	cmd.Flags().String("server", "", "Override server URL")
	cmd.Flags().String("user", "", "Override user")

	// Pagination
	cmd.Flags().Int("limit", 0, "Maximum number of files to return (0 = unlimited)")

	// Sorting
	cmd.Flags().String("order-by", "id", "Order by field (id, file_name, dataset_name, data_source_model_name, bad_rows_count, clean_rows_count, deleted_lines_count, created_at)")
	cmd.Flags().Bool("reverse", false, "Reverse the sort order")

	// Filtering
	cmdutil.AddTableFlags(cmd, "Filter by table")
	cmdutil.RefInt(cmd.Flags(), "cloud-source-id", "cloud-source", "Filter by cloud source ID")
	cmd.Flags().String("cloud-source", "", "Filter by cloud source name (requires --table or --table-id)")
	cmd.Flags().Bool("deleted", false, "List deleted files only (excludes active files)")

	// Output control
	cmd.Flags().String("columns", "", "Comma-separated columns to display. Available: "+
		strings.Join(cmdutil.DeclareColumns(cmd, []stored_items.StoredItemDisplay(nil)), ", "))
}
