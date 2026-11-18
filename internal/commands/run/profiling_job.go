package run

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/profiling"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// NewProfilingJobCommand creates the run profiling-job command
func NewProfilingJobCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "profiling-job",
		Short: "Run profiling jobs",
		Long: `Run profiling jobs by table or by filter.

The backend runs profiling jobs by table (dataset), so this command takes a table directly.

Examples:
  # Run profiling for a specific table
  qctl run profiling-job --table-id 123

  # Address the table by name instead
  qctl run profiling-job --table orders

  # Run with automatic confirmation
  qctl run profiling-job --table-id 123 --yes

  # Before the table's first schema, profile file 456 instead: it becomes the
  # table's only file enabled for training
  qctl run profiling-job --table orders --file-id 456

  # Run jobs matching a filter (with confirmation)
  qctl run profiling-job --filter table-id=5

  # Dry run to see which jobs would be affected
  qctl run profiling-job --dry-list --filter table-id=5

  # Run jobs with automatic confirmation (filter mode)
  qctl run profiling-job --yes --filter table-id=5
`,
		Annotations: cmdutil.WithFormats(catalog.For("profiling-job"), "table"),
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			filterFlag, _ := cmd.Flags().GetString("filter")
			target, err := parseProfilingRunTarget(cmd, filterFlag)
			if err != nil {
				return err
			}

			// Bootstrap auth context
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}

			// Create client
			client, err := profiling.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			if err != nil {
				return fmt.Errorf("failed to create client: %w", err)
			}
			token := ctx.Credential.AccessToken
			dryList, _ := cmd.Flags().GetBool("dry-list")
			yes, _ := cmd.Flags().GetBool("yes")

			// Get output writers
			out := cmd.OutOrStdout()
			errOut := cmd.ErrOrStderr()

			// Mode 1: Filter-based
			if target.filterParams != nil {
				return runProfilingByFilter(ctx, client, token, *target.filterParams, dryList, yes, out, errOut)
			}

			// Mode 2: Direct table. --table is a name, --table-id an ID.
			tableID, _, err := cmdutil.OptionalTableID(ctx, cmd, "")
			if err != nil {
				return err
			}
			return runProfilingByTableID(ctx, client, token, tableID, target.fileID, yes, out, errOut)
		},
	}

	cmdutil.AddTableFlags(cmd, "Table (dataset)")
	cmd.Flags().Bool("yes", false, "Skip confirmation prompt")
	cmd.Flags().String("filter", "", "Filter jobs to run (format: key1=val1,key2=val2)")
	cmd.Flags().Bool("dry-list", false, "Show jobs that would be run without running them (requires --filter)")
	cmdutil.RefInt(cmd.Flags(), "file-id", "file", "Profile this file held for the table's first schema, making it the only file enabled for training (needs a table, not --filter)")

	return cmd
}

type profilingRunTarget struct {
	filterParams *profiling.GetProfilingJobsParams
	fileID       *int
}

// parseProfilingRunTarget settles every input-only refusal before Bootstrap.
// A table name still needs authenticated resolution, but filter parameters are
// fully typed here and carried into execution rather than parsed a second time.
func parseProfilingRunTarget(cmd *cobra.Command, filterFlag string) (profilingRunTarget, error) {
	dryList, _ := cmd.Flags().GetBool("dry-list")
	if err := validateProfilingRunFlags(cmd, dryList); err != nil {
		return profilingRunTarget{}, err
	}
	if err := cmdutil.ValidateOptionalTableSelector(cmd, ""); err != nil {
		return profilingRunTarget{}, err
	}
	if !cmd.Flags().Changed("filter") {
		fileID, ok, err := cmdutil.PositiveIDFlag(cmd, "file-id")
		if err != nil || !ok {
			return profilingRunTarget{}, err
		}
		return profilingRunTarget{fileID: &fileID}, nil
	}

	params, err := parseProfilingFilterToParams(filterFlag)
	return profilingRunTarget{filterParams: &params}, err
}

// validateProfilingRunFlags validates flags for the run profiling-job command.
//
// It answers "which flags did the user type", not "which table did they mean",
// so it runs before the selector is resolved and needs no context. A table
// arrives as --table (a name) or --table-id (an ID); either one is the same
// mode as far as this function is concerned.
//
// These are bad usage, not generic failures: a script needs exit 2 to tell a
// mistyped invocation from a retryable server error, and OptionalTableID
// already reports its own "table specified more than once" that way.
func validateProfilingRunFlags(cmd *cobra.Command, dryList bool) error {
	tableProvided := cmd.Flags().Changed("table-id") || cmd.Flags().Changed("table")
	filterProvided := cmd.Flags().Changed("filter")

	if dryList && !filterProvided {
		return qerrors.NewBadUsage("--dry-list requires --filter to be specified")
	}
	if tableProvided && filterProvided {
		return qerrors.NewBadUsage("cannot specify both a table (--table/--table-id) and --filter")
	}
	if !tableProvided && !filterProvided {
		return qerrors.NewBadUsage("must specify either a table (--table/--table-id) or --filter")
	}
	if filterProvided && cmd.Flags().Changed("file-id") {
		return qerrors.NewBadUsage("--file-id needs a table (--table/--table-id), not --filter")
	}
	// OptionalTableID re-checks this, but only after Bootstrap. Kept here so
	// `--table-id 0` still answers "--table-id must be a positive integer"
	// rather than "not logged in" when there are no credentials yet.
	if _, _, err := cmdutil.PositiveIDFlag(cmd, "table-id"); err != nil {
		return err
	}
	return nil
}

// runProfilingByTableID runs profiling for a specific table ID.
// A non-nil fileID makes that file the table's only file enabled for training.
func runProfilingByTableID(ctx *cmdutil.CommandContext, client *profiling.Client, token string, tableID int, fileID *int, yes bool, out, errOut io.Writer) error {
	if !yes {
		if err := confirmProfilingTableAction(out, errOut, ctx, tableID, fileID); err != nil {
			return err
		}
	}

	resp, err := client.RunProfilingJob(token, tableID, fileID)
	if err != nil {
		fmt.Fprintf(errOut, "Failed to run profiling for table %d: %s\n", tableID, terminal.Sanitize(err.Error()))
		return profiling.ExplainNoTrainingFiles(fmt.Errorf("failed to run profiling for table %d: %w", tableID, err))
	}

	fmt.Fprintf(out, "Successfully triggered profiling for table %d: %s\n", tableID, terminal.Sanitize(resp.Msg))
	return nil
}

// confirmProfilingTableAction prompts user to confirm running profiling for a table.
func confirmProfilingTableAction(out, promptOut io.Writer, ctx *cmdutil.CommandContext, tableID int, fileID *int) error {
	fmt.Fprintf(out, "Context: %s\n", ctx.Config.CurrentContext)
	fmt.Fprintf(out, "Server: %s\n", ctx.ServerURL)
	fmt.Fprintf(out, "\nYou are about to run profiling for table %d\n", tableID)
	if fileID != nil {
		fmt.Fprintf(out, "File %d becomes the table's only file enabled for training; its other training files are disabled.\n", *fileID)
	}

	confirmed, err := cmdutil.ConfirmYesNo(promptOut, "\nDo you want to continue?")
	if err != nil {
		return err
	}
	if !confirmed {
		return fmt.Errorf("operation cancelled")
	}
	return nil
}

// runProfilingByFilter runs profiling for tables matching a filter.
func runProfilingByFilter(ctx *cmdutil.CommandContext, client *profiling.Client, token string, params profiling.GetProfilingJobsParams, dryList, yes bool, out, errOut io.Writer) error {
	tableIDs, err := fetchProfilingTableIDsFromFilter(client, token, params)
	if err != nil {
		return err
	}

	if len(tableIDs) == 0 {
		fmt.Fprintln(out, "No jobs match the specified filter.")
		return nil
	}

	if dryList {
		printProfilingTablesDryList(out, tableIDs)
		return nil
	}

	if !yes {
		if err := confirmProfilingTablesBulk(out, errOut, ctx, tableIDs); err != nil {
			return err
		}
	}

	return runProfilingForTables(client, token, tableIDs, out, errOut)
}

// parseProfilingFilterToParams converts a filter string to GetProfilingJobsParams.
func parseProfilingFilterToParams(filter string) (profiling.GetProfilingJobsParams, error) {
	params := profiling.GetProfilingJobsParams{}

	parts := strings.Split(filter, ",")
	for _, part := range parts {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			return params, qerrors.NewBadUsagef("invalid filter format: %s (expected key=value)", part)
		}

		key := strings.TrimSpace(kv[0])
		value := strings.TrimSpace(kv[1])

		if err := applyProfilingFilterParam(&params, key, value); err != nil {
			return params, err
		}
	}

	return params, nil
}

// applyProfilingFilterParam applies a single filter key-value pair to params.
func applyProfilingFilterParam(params *profiling.GetProfilingJobsParams, key, value string) error {
	switch key {
	case "table-id":
		id, err := strconv.Atoi(value)
		if err != nil {
			return qerrors.NewBadUsagef("invalid table-id: %s", value)
		}
		params.DatasetID = &id
	case "search":
		params.SearchQuery = &value
	default:
		return qerrors.NewBadUsagef("unknown filter key: %s (valid keys: table-id, search)", key)
	}
	return nil
}

// fetchProfilingTableIDsFromFilter fetches unique table IDs from jobs matching a filter.
func fetchProfilingTableIDsFromFilter(client *profiling.Client, token string, params profiling.GetProfilingJobsParams) ([]int, error) {
	tableIDSet := make(map[int]bool)
	fetchedJobs := 0
	page := 1
	params.Limit = 100
	// Atlas's ordered (OFFSET) pagination reports total_rows but not next. Use
	// the unique job primary key so page boundaries cannot repeat or skip jobs.
	params.OrderBy = "id"

	for {
		params.Page = page
		resp, err := client.GetProfilingJobs(token, params)
		if err != nil {
			if len(tableIDSet) > 0 {
				return mapKeysToSlice(tableIDSet), fmt.Errorf("partial results (got %d tables): %w", len(tableIDSet), err)
			}
			return nil, fmt.Errorf("failed to fetch jobs matching filter: %w", err)
		}

		for _, job := range resp.Results {
			tableIDSet[job.DatasetID] = true
		}
		fetchedJobs += len(resp.Results)

		if resp.TotalRows != nil {
			if fetchedJobs >= *resp.TotalRows || len(resp.Results) == 0 {
				break
			}
		} else if resp.Next == nil || len(resp.Results) == 0 {
			break
		}
		page++
	}

	return mapKeysToSlice(tableIDSet), nil
}

// mapKeysToSlice converts a map's keys to a slice.
func mapKeysToSlice(m map[int]bool) []int {
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// printProfilingTablesDryList prints a dry-run list of table IDs.
func printProfilingTablesDryList(out io.Writer, tableIDs []int) {
	fmt.Fprintf(out, "Tables that would have profiling run (%d total):\n", len(tableIDs))
	for _, id := range tableIDs {
		fmt.Fprintf(out, "  %d\n", id)
	}
}

// confirmProfilingTablesBulk prompts user to confirm running profiling for multiple tables.
func confirmProfilingTablesBulk(out, promptOut io.Writer, ctx *cmdutil.CommandContext, tableIDs []int) error {
	fmt.Fprintf(out, "Context: %s\n", ctx.Config.CurrentContext)
	fmt.Fprintf(out, "Server: %s\n", ctx.ServerURL)
	fmt.Fprintf(out, "\nYou are about to run profiling for %d table(s) matching the filter.\n", len(tableIDs))

	printProfilingTableIDSample(out, tableIDs, 10)

	confirmed, err := cmdutil.ConfirmYesNo(promptOut, "\nDo you want to continue?")
	if err != nil {
		return err
	}
	if !confirmed {
		return fmt.Errorf("operation cancelled")
	}
	return nil
}

// printProfilingTableIDSample prints a sample of table IDs.
func printProfilingTableIDSample(out io.Writer, tableIDs []int, sampleSize int) {
	if len(tableIDs) <= sampleSize {
		fmt.Fprintln(out, "\nTable IDs:")
		for _, id := range tableIDs {
			fmt.Fprintf(out, "  - %d\n", id)
		}
	} else {
		fmt.Fprintf(out, "\nSample table IDs (showing %d of %d):\n", sampleSize, len(tableIDs))
		for i := 0; i < sampleSize; i++ {
			fmt.Fprintf(out, "  - %d\n", tableIDs[i])
		}
		fmt.Fprintf(out, "  ... and %d more\n", len(tableIDs)-sampleSize)
	}
}

// runProfilingForTables runs profiling for the given table IDs.
func runProfilingForTables(client *profiling.Client, accessToken string, tableIDs []int, out, errOut io.Writer) error {
	if len(tableIDs) == 0 {
		return fmt.Errorf("no tables to run profiling for")
	}

	successCount := 0
	var lastErr error

	for _, tableID := range tableIDs {
		resp, err := client.RunProfilingJob(accessToken, tableID, nil)
		if err != nil {
			fmt.Fprintf(errOut, "Failed to run profiling for table %d: %s\n", tableID, terminal.Sanitize(err.Error()))
			lastErr = err
			continue
		}

		successCount++
		fmt.Fprintf(out, "Successfully triggered profiling for table %d: %s\n", tableID, terminal.Sanitize(resp.Msg))
	}

	if successCount == 0 && lastErr != nil {
		return profiling.ExplainNoTrainingFiles(fmt.Errorf("failed to run profiling for any tables: %w", lastErr))
	}

	if successCount > 0 && lastErr != nil {
		fmt.Fprintf(errOut, "\nPartially successful: %d of %d tables triggered\n", successCount, len(tableIDs))
		err := fmt.Errorf("failed to run profiling for %d of %d tables: %w", len(tableIDs)-successCount, len(tableIDs), lastErr)
		return profiling.ExplainNoTrainingFiles(err)
	}

	return nil
}
