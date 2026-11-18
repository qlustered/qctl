package cmdutil

import (
	"fmt"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/datasets"
	"github.com/qlustered/qctl/internal/ingestion"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

const migrationJobsPageSize = 100

// FetchRunningIngestionJobs lists a table's running jobs, including dry runs.
func FetchRunningIngestionJobs(ctx *CommandContext, tableID int) (datasets.RunningIngestionJobs, error) {
	var jobs datasets.RunningIngestionJobs
	client, err := ingestion.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return jobs, err
	}
	seen := map[int]bool{}
	for page := 1; ; page++ {
		list, err := client.GetIngestionJobs(ctx.Credential.AccessToken, ingestion.GetIngestionJobsParams{
			DatasetID: &tableID,
			States:    []string{string(api.IngestionJobStateRunning)},
			OrderBy:   string(api.IngestionJobOrderByID),
			Page:      page,
			Limit:     migrationJobsPageSize,
		})
		if err != nil {
			return jobs, fmt.Errorf("failed to list running ingestion jobs (page %d): %w", page, err)
		}
		for _, job := range list.Results {
			if job.State != api.IngestionJobStateRunning || seen[job.ID] {
				continue
			}
			seen[job.ID] = true
			jobs.Jobs = append(jobs.Jobs, datasets.RunningIngestionJob{ID: job.ID, IsDryRun: job.IsDryRun})
		}
		if list.Next == nil {
			return jobs, nil
		}
	}
}

// WarnRunningMigrationJobs prints the running-job preflight on stderr.
func WarnRunningMigrationJobs(cmd *cobra.Command, ctx *CommandContext, tableID int) {
	jobs, err := FetchRunningIngestionJobs(ctx, tableID)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: running ingestion jobs unavailable; migration wait could not be checked: %s\n",
			terminal.Sanitize(err.Error()))
	}
	jobs.WritePreflight(cmd.ErrOrStderr(), tableID)
}
