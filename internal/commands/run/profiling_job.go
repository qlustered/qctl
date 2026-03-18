package run

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/profiling"
	"github.com/spf13/cobra"
)

// NewProfilingJobCommand creates the run profiling-job command
func NewProfilingJobCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "profiling-job",
		Short: "Run profiling jobs",
		Long: `Run profiling jobs by table ID or by filter.

The backend runs profiling jobs by table (dataset) ID, so this command takes a table ID directly.

Examples:
  # Run profiling for a specific table
  qctl run profiling-job --table-id 123

  # Run with automatic confirmation
  qctl run profiling-job --table-id 123 --yes

  # Run jobs matching a filter (with confirmation)
  qctl run profiling-job --filter table-id=5

  # Dry run to see which jobs would be affected
  qctl run profiling-job --dry-list --filter table-id=5

  # Run jobs with automatic confirmation (filter mode)
  qctl run profiling-job --yes --filter table-id=5
`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Get flags
			tableID, _ := cmd.Flags().GetInt("table-id")
			filterFlag, _ := cmd.Flags().GetString("filter")
			dryList, _ := cmd.Flags().GetBool("dry-list")
			yes, _ := cmd.Flags().GetBool("yes")

			// Validate flags
			if err := validateProfilingRunFlags(cmd, tableID, filterFlag, dryList); err != nil {
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

			// Get output writers
			out := cmd.OutOrStdout()
			errOut := cmd.ErrOrStderr()

			// Mode 1: Direct table ID
			if cmd.Flags().Changed("table-id") {
				return runProfilingByTableID(ctx, client, token, tableID, yes, out, errOut)
			}

			// Mode 2: Filter-based
			return runProfilingByFilter(ctx, client, token, filterFlag, dryList, yes, out, errOut)
		},
	}

	cmd.Flags().Int("table-id", 0, "Table ID to run profiling for")
	cmd.Flags().Bool("yes", false, "Skip confirmation prompt")
	cmd.Flags().String("filter", "", "Filter jobs to run (format: key1=val1,key2=val2)")
	cmd.Flags().Bool("dry-list", false, "Show jobs that would be run without running them (requires --filter)")

	return cmd
}

// validateProfilingRunFlags validates flags for the run profiling-job command.
func validateProfilingRunFlags(cmd *cobra.Command, tableID int, filterFlag string, dryList bool) error {
	tableIDProvided := cmd.Flags().Changed("table-id")
	filterProvided := cmd.Flags().Changed("filter")

	if dryList && !filterProvided {
		return fmt.Errorf("--dry-list requires --filter to be specified")
	}
	if tableIDProvided && filterProvided {
		return fmt.Errorf("cannot specify both --table-id and --filter")
	}
	if !tableIDProvided && !filterProvided {
		return fmt.Errorf("must specify either --table-id or --filter")
	}
	if tableIDProvided && tableID <= 0 {
		return fmt.Errorf("--table-id must be a positive integer")
	}
	return nil
}

// runProfilingByTableID runs profiling for a specific table ID.
func runProfilingByTableID(ctx *cmdutil.CommandContext, client *profiling.Client, token string, tableID int, yes bool, out, errOut io.Writer) error {
	if !yes {
		if err := confirmProfilingTableAction(ctx, tableID); err != nil {
			return err
		}
	}

	resp, err := client.RunProfilingJob(token, tableID)
	if err != nil {
		fmt.Fprintf(errOut, "Failed to run profiling for table %d: %v\n", tableID, err)
		return fmt.Errorf("failed to run profiling for table %d: %w", tableID, err)
	}

	fmt.Fprintf(out, "Successfully triggered profiling for table %d: %s\n", tableID, resp.Msg)
	return nil
}

// confirmProfilingTableAction prompts user to confirm running profiling for a table.
func confirmProfilingTableAction(ctx *cmdutil.CommandContext, tableID int) error {
	fmt.Printf("Context: %s\n", ctx.Config.CurrentContext)
	fmt.Printf("Server: %s\n", ctx.ServerURL)
	fmt.Printf("\nYou are about to run profiling for table %d\n", tableID)

	confirmed, err := cmdutil.ConfirmYesNo("\nDo you want to continue?")
	if err != nil {
		return err
	}
	if !confirmed {
		return fmt.Errorf("operation cancelled")
	}
	return nil
}

// runProfilingByFilter runs profiling for tables matching a filter.
func runProfilingByFilter(ctx *cmdutil.CommandContext, client *profiling.Client, token, filterFlag string, dryList, yes bool, out, errOut io.Writer) error {
	params, err := parseProfilingFilterToParams(filterFlag)
	if err != nil {
		return err
	}

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
		if err := confirmProfilingTablesBulk(ctx, tableIDs); err != nil {
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
			return params, fmt.Errorf("invalid filter format: %s (expected key=value)", part)
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
			return fmt.Errorf("invalid table-id: %s", value)
		}
		params.DatasetID = &id
	case "search":
		params.SearchQuery = &value
	default:
		return fmt.Errorf("unknown filter key: %s (valid keys: table-id, search)", key)
	}
	return nil
}

// fetchProfilingTableIDsFromFilter fetches unique table IDs from jobs matching a filter.
func fetchProfilingTableIDsFromFilter(client *profiling.Client, token string, params profiling.GetProfilingJobsParams) ([]int, error) {
	tableIDSet := make(map[int]bool)
	page := 1
	params.Limit = 100

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

		if resp.Next == nil || len(resp.Results) == 0 {
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
func confirmProfilingTablesBulk(ctx *cmdutil.CommandContext, tableIDs []int) error {
	fmt.Printf("Context: %s\n", ctx.Config.CurrentContext)
	fmt.Printf("Server: %s\n", ctx.ServerURL)
	fmt.Printf("\nYou are about to run profiling for %d table(s) matching the filter.\n", len(tableIDs))

	printProfilingTableIDSample(tableIDs, 10)

	confirmed, err := cmdutil.ConfirmYesNo("\nDo you want to continue?")
	if err != nil {
		return err
	}
	if !confirmed {
		return fmt.Errorf("operation cancelled")
	}
	return nil
}

// printProfilingTableIDSample prints a sample of table IDs.
func printProfilingTableIDSample(tableIDs []int, sampleSize int) {
	if len(tableIDs) <= sampleSize {
		fmt.Println("\nTable IDs:")
		for _, id := range tableIDs {
			fmt.Printf("  - %d\n", id)
		}
	} else {
		fmt.Printf("\nSample table IDs (showing %d of %d):\n", sampleSize, len(tableIDs))
		for i := 0; i < sampleSize; i++ {
			fmt.Printf("  - %d\n", tableIDs[i])
		}
		fmt.Printf("  ... and %d more\n", len(tableIDs)-sampleSize)
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
		resp, err := client.RunProfilingJob(accessToken, tableID)
		if err != nil {
			fmt.Fprintf(errOut, "Failed to run profiling for table %d: %v\n", tableID, err)
			lastErr = err
			continue
		}

		successCount++
		fmt.Fprintf(out, "Successfully triggered profiling for table %d: %s\n", tableID, resp.Msg)
	}

	if successCount == 0 && lastErr != nil {
		return fmt.Errorf("failed to run profiling for any tables: %w", lastErr)
	}

	if successCount > 0 && lastErr != nil {
		fmt.Fprintf(errOut, "\nPartially successful: %d of %d tables triggered\n", successCount, len(tableIDs))
	}

	return nil
}
