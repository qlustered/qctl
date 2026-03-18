package kill

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/profiling"
	"github.com/spf13/cobra"
)

// NewProfilingJobCommand creates the kill profiling-job command
func NewProfilingJobCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "profiling-job [id...]",
		Short: "Kill profiling jobs by ID or filter",
		Long: `Kill one or more running profiling jobs by ID or by filter.

Examples:
  # Kill a single job by ID
  qctl kill profiling-job 123

  # Kill multiple jobs by IDs
  qctl kill profiling-job 123 456 789

  # Kill jobs matching a filter (with confirmation)
  qctl kill profiling-job --filter table-id=5

  # Dry run to see which jobs would be killed
  qctl kill profiling-job --dry-list --filter table-id=5

  # Kill jobs with automatic confirmation
  qctl kill profiling-job --yes --filter table-id=5
`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Validate flags
			filterFlag, _ := cmd.Flags().GetString("filter")
			if err := validateProfilingRunKillFlags(cmd, args, filterFlag); err != nil {
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

			// Get flags
			dryList, _ := cmd.Flags().GetBool("dry-list")
			yes, _ := cmd.Flags().GetBool("yes")

			// Get output writers
			out := cmd.OutOrStdout()
			errOut := cmd.ErrOrStderr()

			// Mode 1: Explicit IDs
			if len(args) > 0 {
				return killProfilingByExplicitIDs(ctx, client, token, args, yes, out, errOut)
			}

			// Mode 2: Filter-based
			return killProfilingByFilter(ctx, client, token, filterFlag, dryList, yes, out, errOut)
		},
	}

	cmd.Flags().Bool("yes", false, "Skip confirmation prompt")
	cmd.Flags().String("filter", "", "Filter jobs to kill (format: key1=val1,key2=val2)")
	cmd.Flags().Bool("dry-list", false, "Show jobs that would be killed without killing them (requires --filter)")

	return cmd
}

// validateProfilingRunKillFlags validates common flags for run/kill commands.
func validateProfilingRunKillFlags(cmd *cobra.Command, args []string, filterFlag string) error {
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

// killProfilingByExplicitIDs kills jobs specified by explicit IDs.
func killProfilingByExplicitIDs(ctx *cmdutil.CommandContext, client *profiling.Client, token string, args []string, yes bool, out, errOut io.Writer) error {
	jobIDs, err := parseProfilingJobIDs(args)
	if err != nil {
		return err
	}

	if !yes {
		if err := confirmProfilingJobAction(ctx, "kill", jobIDs); err != nil {
			return err
		}
	}

	return killProfilingJobsByIDs(client, token, jobIDs, out, errOut)
}

// killProfilingByFilter kills jobs matching a filter.
func killProfilingByFilter(ctx *cmdutil.CommandContext, client *profiling.Client, token, filterFlag string, dryList, yes bool, out, errOut io.Writer) error {
	params, err := parseProfilingFilterToParams(filterFlag)
	if err != nil {
		return err
	}

	jobIDs, err := fetchProfilingJobIDsFromFilter(client, token, params)
	if err != nil {
		return err
	}

	if len(jobIDs) == 0 {
		fmt.Fprintln(out, "No jobs match the specified filter.")
		return nil
	}

	if dryList {
		printProfilingDryList(out, "kill", jobIDs)
		return nil
	}

	if !yes {
		if err := confirmProfilingJobActionBulk(ctx, "kill", jobIDs); err != nil {
			return err
		}
	}

	return killProfilingJobsByIDs(client, token, jobIDs, out, errOut)
}

// parseProfilingJobIDs parses job IDs from string arguments.
func parseProfilingJobIDs(args []string) ([]int, error) {
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

// fetchProfilingJobIDsFromFilter fetches all job IDs matching a filter.
func fetchProfilingJobIDsFromFilter(client *profiling.Client, token string, params profiling.GetProfilingJobsParams) ([]int, error) {
	var allJobIDs []int
	page := 1
	params.Limit = 100

	for {
		params.Page = page
		resp, err := client.GetProfilingJobs(token, params)
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

// printProfilingDryList prints a dry-run list of job IDs.
func printProfilingDryList(out io.Writer, action string, jobIDs []int) {
	fmt.Fprintf(out, "Jobs that would be %sed (%d total):\n", action, len(jobIDs))
	for _, id := range jobIDs {
		fmt.Fprintf(out, "  %d\n", id)
	}
}

// confirmProfilingJobAction prompts user to confirm an action on specific jobs.
func confirmProfilingJobAction(ctx *cmdutil.CommandContext, action string, jobIDs []int) error {
	fmt.Printf("Context: %s\n", ctx.Config.CurrentContext)
	fmt.Printf("Server: %s\n", ctx.ServerURL)
	fmt.Printf("\nYou are about to %s %d profiling job(s):\n", action, len(jobIDs))
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

// confirmProfilingJobActionBulk prompts user to confirm a bulk action on jobs.
func confirmProfilingJobActionBulk(ctx *cmdutil.CommandContext, action string, jobIDs []int) error {
	fmt.Printf("Context: %s\n", ctx.Config.CurrentContext)
	fmt.Printf("Server: %s\n", ctx.ServerURL)
	fmt.Printf("\nYou are about to %s %d profiling job(s) matching the filter.\n", action, len(jobIDs))

	printProfilingJobIDSample(jobIDs, 10)

	confirmed, err := cmdutil.ConfirmYesNo("\nDo you want to continue?")
	if err != nil {
		return err
	}
	if !confirmed {
		return fmt.Errorf("operation cancelled")
	}
	return nil
}

// printProfilingJobIDSample prints a sample of job IDs.
func printProfilingJobIDSample(jobIDs []int, sampleSize int) {
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

// killProfilingJobsByIDs kills profiling jobs by their IDs.
func killProfilingJobsByIDs(client *profiling.Client, accessToken string, jobIDs []int, out, errOut io.Writer) error {
	if len(jobIDs) == 0 {
		return fmt.Errorf("no jobs to kill")
	}

	successCount := 0
	var lastErr error

	for _, jobID := range jobIDs {
		resp, err := client.KillProfilingJob(accessToken, jobID)
		if err != nil {
			fmt.Fprintf(errOut, "Failed to kill job %d: %v\n", jobID, err)
			lastErr = err
			continue
		}
		successCount++
		fmt.Fprintf(out, "Successfully killed job %d: %s\n", jobID, resp.Msg)
	}

	if successCount == 0 && lastErr != nil {
		return fmt.Errorf("failed to kill any jobs: %w", lastErr)
	}

	if len(jobIDs) > 1 {
		fmt.Fprintf(out, "\nSuccessfully killed %d of %d job(s)\n", successCount, len(jobIDs))
	}

	return nil
}
