package kill

import (
	"fmt"
	"io"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/ingestion"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// NewIngestionJobCommand creates the kill ingestion-job command
func NewIngestionJobCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ingestion-job [id...]",
		Short: "Kill ingestion jobs by ID or filter",
		Long: `Kill one or more running ingestion jobs by ID or by filter.

Examples:
  # Kill a single job by ID
  qctl kill ingestion-job 123

  # Kill multiple jobs by IDs
  qctl kill ingestion-job 123 456 789

  # Kill jobs matching a filter (with confirmation)
  qctl kill ingestion-job --filter table-id=5,state=running

  # Dry run to see which jobs would be killed
  qctl kill ingestion-job --dry-list --filter table-id=5,state=running

  # Kill jobs with automatic confirmation
  qctl kill ingestion-job --yes --filter table-id=5,state=running
`,
		Annotations: cmdutil.WithFormats(catalog.For("ingestion-job"), "table"),
		Args:        cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			filterFlag, _ := cmd.Flags().GetString("filter")
			target, err := parseIngestionJobTarget(cmd, args, filterFlag)
			if err != nil {
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
			if len(target.jobIDs) > 0 {
				return killByExplicitIDs(ctx, client, token, target.jobIDs, yes, out, errOut)
			}

			// Mode 2: Filter-based
			return killByFilter(ctx, client, token, target.filterParams, dryList, yes, out, errOut)
		},
	}

	cmd.Flags().Bool("yes", false, "Skip confirmation prompt")
	cmd.Flags().String("filter", "", "Filter jobs to kill (format: key1=val1,key2=val2)")
	cmd.Flags().Bool("dry-list", false, "Show jobs that would be killed without killing them (requires --filter)")

	return cmd
}

type ingestionJobTarget struct {
	jobIDs       []int
	filterParams ingestion.GetIngestionJobsParams
}

// parseIngestionJobTarget settles every input-only refusal before Bootstrap.
// The parsed result is carried into execution so authentication cannot change
// which error an invalid invocation reports.
func parseIngestionJobTarget(cmd *cobra.Command, args []string, filterFlag string) (ingestionJobTarget, error) {
	if err := validateRunKillFlags(cmd, args); err != nil {
		return ingestionJobTarget{}, err
	}
	if len(args) > 0 {
		jobIDs, err := cmdutil.ParseIntArgs(args, "job ID")
		return ingestionJobTarget{jobIDs: jobIDs}, err
	}
	params, err := ingestion.ParseFilter(filterFlag)
	return ingestionJobTarget{filterParams: params}, err
}

// validateRunKillFlags validates common flags for run/kill commands.
func validateRunKillFlags(cmd *cobra.Command, args []string) error {
	dryList, _ := cmd.Flags().GetBool("dry-list")
	filterProvided := cmd.Flags().Changed("filter")

	if dryList && !filterProvided {
		return qerrors.NewBadUsage("--dry-list requires --filter to be specified")
	}
	if len(args) > 0 && filterProvided {
		return qerrors.NewBadUsage("cannot specify both explicit IDs and --filter")
	}
	if len(args) == 0 && !filterProvided {
		return qerrors.NewBadUsage("must specify either job IDs or --filter")
	}
	return nil
}

// killByExplicitIDs kills jobs specified by explicit IDs.
func killByExplicitIDs(ctx *cmdutil.CommandContext, client *ingestion.Client, token string, jobIDs []int, yes bool, out, errOut io.Writer) error {
	if !yes {
		if err := confirmJobAction(out, errOut, ctx, "kill", jobIDs); err != nil {
			return err
		}
	}

	return killJobsByIDs(client, token, jobIDs, out, errOut)
}

// killByFilter kills jobs matching a filter.
func killByFilter(ctx *cmdutil.CommandContext, client *ingestion.Client, token string, params ingestion.GetIngestionJobsParams, dryList, yes bool, out, errOut io.Writer) error {
	jobIDs, err := client.MatchingJobIDs(token, params)
	if err != nil {
		return err
	}

	if len(jobIDs) == 0 {
		fmt.Fprintln(out, "No jobs match the specified filter.")
		return nil
	}

	if dryList {
		printDryList(out, "kill", jobIDs)
		return nil
	}

	if !yes {
		if err := confirmJobActionBulk(out, errOut, ctx, "kill", jobIDs); err != nil {
			return err
		}
	}

	return killJobsByIDs(client, token, jobIDs, out, errOut)
}

// printDryList prints a dry-run list of job IDs.
func printDryList(out io.Writer, action string, jobIDs []int) {
	fmt.Fprintf(out, "Jobs that would be %sed (%d total):\n", action, len(jobIDs))
	for _, id := range jobIDs {
		fmt.Fprintf(out, "  %d\n", id)
	}
}

// confirmJobAction prompts user to confirm an action on specific jobs.
func confirmJobAction(out, promptOut io.Writer, ctx *cmdutil.CommandContext, action string, jobIDs []int) error {
	fmt.Fprintf(out, "Context: %s\n", ctx.Config.CurrentContext)
	fmt.Fprintf(out, "Server: %s\n", ctx.ServerURL)
	fmt.Fprintf(out, "\nYou are about to %s %d ingestion job(s):\n", action, len(jobIDs))
	for _, id := range jobIDs {
		fmt.Fprintf(out, "  - Job ID: %d\n", id)
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

// confirmJobActionBulk prompts user to confirm a bulk action on jobs.
func confirmJobActionBulk(out, promptOut io.Writer, ctx *cmdutil.CommandContext, action string, jobIDs []int) error {
	fmt.Fprintf(out, "Context: %s\n", ctx.Config.CurrentContext)
	fmt.Fprintf(out, "Server: %s\n", ctx.ServerURL)
	fmt.Fprintf(out, "\nYou are about to %s %d ingestion job(s) matching the filter.\n", action, len(jobIDs))

	printJobIDSample(out, jobIDs, 10)

	confirmed, err := cmdutil.ConfirmYesNo(promptOut, "\nDo you want to continue?")
	if err != nil {
		return err
	}
	if !confirmed {
		return fmt.Errorf("operation cancelled")
	}
	return nil
}

// printJobIDSample prints a sample of job IDs.
func printJobIDSample(out io.Writer, jobIDs []int, sampleSize int) {
	if len(jobIDs) <= sampleSize {
		fmt.Fprintln(out, "\nJob IDs:")
		for _, id := range jobIDs {
			fmt.Fprintf(out, "  - %d\n", id)
		}
	} else {
		fmt.Fprintf(out, "\nSample job IDs (showing %d of %d):\n", sampleSize, len(jobIDs))
		for i := 0; i < sampleSize; i++ {
			fmt.Fprintf(out, "  - %d\n", jobIDs[i])
		}
		fmt.Fprintf(out, "  ... and %d more\n", len(jobIDs)-sampleSize)
	}
}

// killJobsByIDs kills ingestion jobs by their IDs.
func killJobsByIDs(client *ingestion.Client, accessToken string, jobIDs []int, out, errOut io.Writer) error {
	if len(jobIDs) == 0 {
		return fmt.Errorf("no jobs to kill")
	}

	if len(jobIDs) == 1 {
		resp, err := client.KillIngestionJob(accessToken, jobIDs[0])
		if err != nil {
			fmt.Fprintf(errOut, "Failed to kill job %d: %s\n", jobIDs[0], terminal.Sanitize(err.Error()))
			return fmt.Errorf("failed to kill job %d: %w", jobIDs[0], err)
		}
		fmt.Fprintf(out, "Successfully killed job %d: %s\n", jobIDs[0], terminal.Sanitize(resp.Msg))
		return nil
	}

	resp, err := client.KillMultipleIngestionJobs(accessToken, jobIDs)
	if err != nil {
		fmt.Fprintf(errOut, "Failed to kill jobs: %s\n", terminal.Sanitize(err.Error()))
		return fmt.Errorf("failed to kill jobs: %w", err)
	}

	fmt.Fprintf(out, "Successfully killed %d job(s): %s\n", len(jobIDs), terminal.Sanitize(resp.Msg))
	return nil
}
