package describe

import (
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/spf13/cobra"
)

func TestPositionalIDParseErrorsExitTwo(t *testing.T) {
	tests := []struct {
		name string
		new  func() *cobra.Command
	}{
		{name: "alert", new: NewAlertCommand},
		{name: "warning", new: NewWarningCommand},
		{name: "cloud source", new: NewCloudSourceCommand},
		{name: "destination", new: NewDestinationCommand},
		{name: "dry-run job", new: NewDryRunJobCommand},
		{name: "error incident", new: NewErrorIncidentCommand},
		{name: "file", new: NewFileCommand},
		{name: "ingestion job", new: NewIngestionJobCommand},
		{name: "profiling job", new: NewProfilingJobCommand},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := tt.new()
			cmd.SetArgs([]string{"not-a-number"})
			err := cmd.Execute()
			if err == nil {
				t.Fatal("expected invalid ID to fail")
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d: %v", got, qerrors.ExitBadUsage, err)
			}
		})
	}
}
