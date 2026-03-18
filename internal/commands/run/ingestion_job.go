package run

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/ingestion"
	"github.com/spf13/cobra"
)

// NewIngestionJobCommand creates the run ingestion-job command
func NewIngestionJobCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ingestion-job [id...]",
		Short: "Run ingestion jobs",
		Long: `Run one or more ingestion jobs by ID or by filter.

Examples:
  # Run a single job by ID
  qctl run ingestion-job 123

  # Run multiple jobs by IDs
  qctl run ingestion-job 123 456 789

  # Run jobs matching a filter (with confirmation)
  qctl run ingestion-job --filter table-id=5,state=pending

  # Dry run to see which jobs would be affected
  qctl run ingestion-job --dry-list --filter table-id=5

  # Run jobs with automatic confirmation
  qctl run ingestion-job --yes --filter table-id=5,state=pending
`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Validate flags
			filterFlag, _ := cmd.Flags().GetString("filter")
			if err := validateRunKillFlags(cmd, args, filterFlag); err != nil {
				return err
			}

			// Bootstrap auth context
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}

			// Create client
			client, err := ingestion.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			if err != nil {
				return err
			}
			token := ctx.Credential.AccessToken

			// Get flags
			dryList, _ := cmd.Flags().GetBool("dry-list")
			yes, _ := cmd.Flags().GetBool("yes")

			// Get output writers
			out := cmd.OutOrStdout()
			errOut := cmd.ErrOrStderr()

			// Mode 1: Explicit IDs
			if len(args) > 0 {
				return runByExplicitIDs(ctx, client, token, args, yes, out, errOut)
			}

			// Mode 2: Filter-based
			return runByFilter(ctx, client, token, filterFlag, dryList, yes, out, errOut)
		},
	}

	cmd.Flags().Bool("yes", false, "Skip confirmation prompt")
	cmd.Flags().String("filter", "", "Filter jobs to run (format: key1=val1,key2=val2)")
	cmd.Flags().Bool("dry-list", false, "Show jobs that would be run without running them (requires --filter)")

	return cmd
}

// validateRunKillFlags validates common flags for run/kill commands.
func validateRunKillFlags(cmd *cobra.Command, args []string, filterFlag string) error {
	dryList, _ := cmd.Flags().GetBool("dry-list")

	if dryList && !cmd.Flags().Changed("filter") {
		return fmt.Errorf("--dry-list requires --filter to be specified")
	}
	if len(args) > 0 && filterFlag != "" {
		return fmt.Errorf("cannot specify both explicit IDs and --filter")
	}
	if len(args) == 0 && filterFlag == "" {
		return fmt.Errorf("must specify either job IDs or --filter")
	}
	return nil
}

// runByExplicitIDs runs jobs specified by explicit IDs.
func runByExplicitIDs(ctx *cmdutil.CommandContext, client *ingestion.Client, token string, args []string, yes bool, out, errOut io.Writer) error {
	jobIDs, err := parseJobIDs(args)
	if err != nil {
		return err
	}

	if !yes {
		if err := confirmJobAction(ctx, "run", jobIDs); err != nil {
			return err
		}
	}

	return runJobsByIDs(client, token, jobIDs, out, errOut)
}

// runByFilter runs jobs matching a filter.
func runByFilter(ctx *cmdutil.CommandContext, client *ingestion.Client, token, filterFlag string, dryList, yes bool, out, errOut io.Writer) error {
	params, err := parseFilterToParams(filterFlag)
	if err != nil {
		return err
	}

	jobIDs, err := fetchJobIDsFromFilter(client, token, params)
	if err != nil {
		return err
	}

	if len(jobIDs) == 0 {
		fmt.Fprintln(out, "No jobs match the specified filter.")
		return nil
	}

	if dryList {
		printDryList(out, "run", jobIDs)
		return nil
	}

	if !yes {
		if err := confirmJobActionBulk(ctx, "run", jobIDs); err != nil {
			return err
		}
	}

	return runJobsByIDs(client, token, jobIDs, out, errOut)
}

// parseJobIDs parses job IDs from string arguments.
func parseJobIDs(args []string) ([]int, error) {
	jobIDs := make([]int, 0, len(args))
	for _, arg := range args {
		id, err := strconv.Atoi(arg)
		if err != nil {
			return nil, fmt.Errorf("invalid job ID: %s", arg)
		}
		jobIDs = append(jobIDs, id)
	}
	return jobIDs, nil
}

// parseFilterToParams converts a filter string to GetIngestionJobsParams.
func parseFilterToParams(filter string) (ingestion.GetIngestionJobsParams, error) {
	params := ingestion.GetIngestionJobsParams{}

	parts := strings.Split(filter, ",")
	for _, part := range parts {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			return params, fmt.Errorf("invalid filter format: %s (expected key=value)", part)
		}

		key := strings.TrimSpace(kv[0])
		value := strings.TrimSpace(kv[1])

		if err := applyFilterParam(&params, key, value); err != nil {
			return params, err
		}
	}

	return params, nil
}

// applyFilterParam applies a single filter key-value pair to params.
func applyFilterParam(params *ingestion.GetIngestionJobsParams, key, value string) error {
	switch key {
	case "table-id":
		id, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid table-id: %s", value)
		}
		params.DatasetID = &id
	case "cloud-source-id":
		id, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid cloud-source-id: %s", value)
		}
		params.DataSourceID = &id
	case "stored-item-id":
		id, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid stored-item-id: %s", value)
		}
		params.StoredItemID = &id
	case "state":
		params.States = []string{value}
	case "states":
		params.States = strings.Split(value, "|")
	case "is-dry-run":
		isDryRun := value == "true"
		params.IsDryRun = &isDryRun
	case "created-by":
		params.CreatedBy = &value
	default:
		return fmt.Errorf("unknown filter key: %s", key)
	}
	return nil
}

// fetchJobIDsFromFilter fetches all job IDs matching a filter.
// This function paginates through all results since action commands need to act on all matching jobs.
func fetchJobIDsFromFilter(client *ingestion.Client, token string, params ingestion.GetIngestionJobsParams) ([]int, error) {
	var allJobIDs []int
	page := 1
	params.Limit = 100

	for {
		params.Page = page
		resp, err := client.GetIngestionJobs(token, params)
		if err != nil {
			if len(allJobIDs) > 0 {
				return allJobIDs, fmt.Errorf("partial results (got %d): %w", len(allJobIDs), err)
			}
			return nil, fmt.Errorf("failed to fetch jobs matching filter: %w", err)
		}

		for _, job := range resp.Results {
			allJobIDs = append(allJobIDs, job.ID)
		}

		if resp.Next == nil || len(resp.Results) == 0 {
			break
		}
		page++
	}

	return allJobIDs, nil
}

// printDryList prints a dry-run list of job IDs.
func printDryList(out io.Writer, action string, jobIDs []int) {
	fmt.Fprintf(out, "Jobs that would be %sed (%d total):\n", action, len(jobIDs))
	for _, id := range jobIDs {
		fmt.Fprintf(out, "  %d\n", id)
	}
}

// confirmJobAction prompts user to confirm an action on specific jobs.
func confirmJobAction(ctx *cmdutil.CommandContext, action string, jobIDs []int) error {
	fmt.Printf("Context: %s\n", ctx.Config.CurrentContext)
	fmt.Printf("Server: %s\n", ctx.ServerURL)
	fmt.Printf("\nYou are about to %s %d ingestion job(s):\n", action, len(jobIDs))
	for _, id := range jobIDs {
		fmt.Printf("  - Job ID: %d\n", id)
	}

	confirmed, err := cmdutil.ConfirmYesNo("\nDo you want to continue?")
	if err != nil {
		return err
	}
	if !confirmed {
		return fmt.Errorf("operation cancelled")
	}
	return nil
}

// confirmJobActionBulk prompts user to confirm a bulk action on jobs.
func confirmJobActionBulk(ctx *cmdutil.CommandContext, action string, jobIDs []int) error {
	fmt.Printf("Context: %s\n", ctx.Config.CurrentContext)
	fmt.Printf("Server: %s\n", ctx.ServerURL)
	fmt.Printf("\nYou are about to %s %d ingestion job(s) matching the filter.\n", action, len(jobIDs))

	printJobIDSample(jobIDs, 10)

	confirmed, err := cmdutil.ConfirmYesNo("\nDo you want to continue?")
	if err != nil {
		return err
	}
	if !confirmed {
		return fmt.Errorf("operation cancelled")
	}
	return nil
}

// printJobIDSample prints a sample of job IDs.
func printJobIDSample(jobIDs []int, sampleSize int) {
	if len(jobIDs) <= sampleSize {
		fmt.Println("\nJob IDs:")
		for _, id := range jobIDs {
			fmt.Printf("  - %d\n", id)
		}
	} else {
		fmt.Printf("\nSample job IDs (showing %d of %d):\n", sampleSize, len(jobIDs))
		for i := 0; i < sampleSize; i++ {
			fmt.Printf("  - %d\n", jobIDs[i])
		}
		fmt.Printf("  ... and %d more\n", len(jobIDs)-sampleSize)
	}
}

// runJobsByIDs runs ingestion jobs by their IDs.
func runJobsByIDs(client *ingestion.Client, accessToken string, jobIDs []int, out, errOut io.Writer) error {
	if len(jobIDs) == 0 {
		return fmt.Errorf("no jobs to run")
	}

	if len(jobIDs) == 1 {
		resp, err := client.RunIngestionJob(accessToken, jobIDs[0])
		if err != nil {
			fmt.Fprintf(errOut, "Failed to run job %d: %v\n", jobIDs[0], err)
			return fmt.Errorf("failed to run job %d: %w", jobIDs[0], err)
		}
		fmt.Fprintf(out, "Successfully triggered job %d: %s\n", jobIDs[0], resp.Msg)
		return nil
	}

	resp, err := client.RunMultipleIngestionJobs(accessToken, jobIDs)
	if err != nil {
		fmt.Fprintf(errOut, "Failed to run jobs: %v\n", err)
		return fmt.Errorf("failed to run jobs: %w", err)
	}

	fmt.Fprintf(out, "Successfully triggered %d job(s): %s\n", len(jobIDs), resp.Msg)
	return nil
}
