package delete

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
		{name: "error incident", new: NewErrorIncidentCommand},
		{name: "file", new: NewFileCommand},
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
