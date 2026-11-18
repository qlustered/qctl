package datasets

import (
	"fmt"
	"io"
	"strings"
)

// RunningIngestionJob identifies a running ingestion job or dry-run carrier.
type RunningIngestionJob struct {
	ID       int  `json:"id" yaml:"id"`
	IsDryRun bool `json:"is_dry_run" yaml:"is_dry_run"`
}

// RunningIngestionJobs carries the migration's running-job lookup.
type RunningIngestionJobs struct {
	Unavailable string                `json:"unavailable,omitempty" yaml:"unavailable,omitempty"`
	Jobs        []RunningIngestionJob `json:"jobs,omitempty" yaml:"jobs,omitempty"`
}

// WritePreflight lists jobs that can delay a schema migration.
func (s RunningIngestionJobs) WritePreflight(out io.Writer, tableID int) {
	if len(s.Jobs) == 0 {
		return
	}
	fmt.Fprintf(out, "Running ingestion jobs on table %d can delay the schema migration:\n", tableID)
	for _, job := range s.Jobs {
		kind := "ingestion"
		if job.IsDryRun {
			kind = "dry-run ingestion"
		}
		fmt.Fprintf(out, "  %s job %d — qctl describe ingestion-job %d\n", kind, job.ID, job.ID)
	}
	fmt.Fprintln(out, "The migration waits for running ingestion jobs, including rule-validation dry runs, to finish.")
	fmt.Fprintln(out)
}

func (s RunningIngestionJobs) blocker() string {
	commands := make([]string, 0, len(s.Jobs))
	for _, job := range s.Jobs {
		commands = append(commands, fmt.Sprintf("qctl describe ingestion-job %d", job.ID))
	}
	return "confirmed schema draft is waiting for running ingestion jobs (including dry runs) — " +
		strings.Join(commands, "; ")
}

func (s *TableSummary) hasRunningMigrationJobs() bool {
	return s.HasConfirmedDraft() && s.Jobs.RunningIngestion != nil &&
		s.Jobs.RunningIngestion.Unavailable == "" && len(s.Jobs.RunningIngestion.Jobs) > 0
}
