package run

import (
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestIngestionJobLocalErrorsBeatMissingCredentials(t *testing.T) {
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)

	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "invalid positional ID", args: []string{"run", "ingestion-job", "not-a-number", "--yes"}, want: "invalid job ID"},
		{name: "missing target", args: []string{"run", "ingestion-job"}, want: "must specify either job IDs or --filter"},
		{name: "conflicting modes", args: []string{"run", "ingestion-job", "123", "--filter", "table-id=1"}, want: "cannot specify both explicit IDs and --filter"},
		{name: "dry list without filter", args: []string{"run", "ingestion-job", "123", "--dry-list"}, want: "--dry-list requires --filter"},
		{name: "empty filter", args: []string{"run", "ingestion-job", "--filter", ""}, want: "invalid filter format"},
		{name: "invalid filter format", args: []string{"run", "ingestion-job", "--filter", "badformat"}, want: "invalid filter format"},
		{name: "unknown filter key", args: []string{"run", "ingestion-job", "--filter", "nope=value"}, want: "unknown filter key"},
		{name: "invalid filter ID", args: []string{"run", "ingestion-job", "--filter", "table-id=nope"}, want: "invalid table-id"},
		{name: "invalid filter state", args: []string{"run", "ingestion-job", "--filter", "state=failed"}, want: `invalid state: "failed" (valid: blocked,`},
		{name: "invalid filter states member", args: []string{"run", "ingestion-job", "--filter", "states=pending|failed"}, want: `invalid states: "failed"`},
		{name: "empty filter states member", args: []string{"run", "ingestion-job", "--filter", "states=pending|"}, want: `invalid states: ""`},
		{name: "invalid filter is-dry-run", args: []string{"run", "ingestion-job", "--filter", "is-dry-run=yes"}, want: `invalid is-dry-run: "yes"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := setupRunTestCommand()
			cmd.SetArgs(tt.args)
			err := cmd.Execute()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d: %v", got, qerrors.ExitBadUsage, err)
			}
		})
	}

	cmd := setupRunTestCommand()
	cmd.SetArgs([]string{"run", "ingestion-job", "123", "--yes"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "not logged in") {
		t.Fatalf("valid target = %v, want not-logged-in control error", err)
	}
	if got := qerrors.GetExitCode(err); got == qerrors.ExitBadUsage {
		t.Fatalf("valid target exit code = %d (ExitBadUsage); credential-free control was not proved", got)
	}
	if got := len(mock.Requests()); got != 0 {
		t.Errorf("made %d request(s) without credentials", got)
	}
}

func TestProfilingJobLocalErrorsBeatMissingCredentials(t *testing.T) {
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)

	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "invalid table ID", args: []string{"run", "profiling-job", "--table-id", "0", "--yes"}, want: "--table-id must be a positive integer"},
		{name: "conflicting table selectors", args: []string{"run", "profiling-job", "--table", "orders", "--table-id", "1"}, want: "table specified more than once"},
		{name: "missing target", args: []string{"run", "profiling-job"}, want: "must specify either a table"},
		{name: "conflicting modes", args: []string{"run", "profiling-job", "--table-id", "1", "--filter", "table-id=1"}, want: "cannot specify both a table"},
		{name: "dry list without filter", args: []string{"run", "profiling-job", "--table-id", "1", "--dry-list"}, want: "--dry-list requires --filter"},
		{name: "empty filter", args: []string{"run", "profiling-job", "--filter", ""}, want: "invalid filter format"},
		{name: "invalid filter format", args: []string{"run", "profiling-job", "--filter", "badformat"}, want: "invalid filter format"},
		{name: "unknown filter key", args: []string{"run", "profiling-job", "--filter", "nope=value"}, want: "unknown filter key"},
		{name: "invalid filter ID", args: []string{"run", "profiling-job", "--filter", "table-id=nope"}, want: "invalid table-id"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := setupRunProfilingTestCommand()
			cmd.SetArgs(tt.args)
			err := cmd.Execute()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d: %v", got, qerrors.ExitBadUsage, err)
			}
		})
	}

	validTargets := [][]string{
		{"run", "profiling-job", "--table-id", "123", "--yes"},
		{"run", "profiling-job", "--filter", "table-id=1", "--yes"},
	}
	for _, args := range validTargets {
		cmd := setupRunProfilingTestCommand()
		cmd.SetArgs(args)
		err := cmd.Execute()
		if err == nil || !strings.Contains(err.Error(), "not logged in") {
			t.Fatalf("valid target %v = %v, want not-logged-in control error", args, err)
		}
		if got := qerrors.GetExitCode(err); got == qerrors.ExitBadUsage {
			t.Fatalf("valid target %v exit code = %d (ExitBadUsage); credential-free control was not proved", args, got)
		}
	}
	if got := len(mock.Requests()); got != 0 {
		t.Errorf("made %d request(s) without credentials", got)
	}
}
