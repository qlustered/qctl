package root

import (
	"io"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/agentskill"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// noServerError is how Bootstrap fails when no context, flag or environment
// variable names a server.
const noServerError = "no API server specified"

// runRootWithoutContext executes the real tree with a HOME that holds no qctl
// config, so a command that reaches Bootstrap fails there. It resets every flag
// the run changed on the executed command: the tree is a package singleton, so
// a value left set would leak into the next invocation of the same command.
func runRootWithoutContext(t *testing.T, args ...string) error {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(agentskill.CodexHomeEnv, t.TempDir())
	t.Setenv(agentskill.ClaudeConfigDirEnv, t.TempDir())
	t.Setenv("QCTL_SERVER", "")
	t.Setenv("QCTL_ORG", "")
	config.SetPathOverride("")

	cmd := GetRootCmd()
	cmd.SetArgs(args)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	var executed *cobra.Command
	t.Cleanup(func() {
		cmd.SetArgs(nil)
		cmd.SetOut(nil)
		cmd.SetErr(nil)
		if executed == nil {
			return
		}
		executed.Flags().VisitAll(func(f *pflag.Flag) {
			if !f.Changed {
				return
			}
			var err error
			if slice, ok := f.Value.(pflag.SliceValue); ok {
				var def []string
				if inner := strings.Trim(f.DefValue, "[]"); inner != "" {
					def = strings.Split(inner, ",")
				}
				err = slice.Replace(def)
			} else {
				err = f.Value.Set(f.DefValue)
			}
			if err != nil {
				t.Errorf("resetting --%s: %v", f.Name, err)
			}
			f.Changed = false
		})
	})
	executed, err := cmd.ExecuteC()
	return err
}

// A `get` check that reads only flags and arguments must not wait for
// Bootstrap: with no context configured, Bootstrap's no-server error (exit 1)
// would hide the usage error (exit 2). The controls are valid invocations of
// the same commands, which must still get as far as Bootstrap.
func TestGetUsageErrorsPrecedeBootstrap(t *testing.T) {
	cases := []struct {
		args []string
		want int
	}{
		{[]string{"get", "rules", "--search", "ab"}, qerrors.ExitBadUsage},
		{[]string{"get", "rules"}, qerrors.ExitGenericError},

		{[]string{"get", "tables", "--destination-id", "-1"}, qerrors.ExitBadUsage},
		{[]string{"get", "tables", "--table-kind", ""}, qerrors.ExitBadUsage},
		{[]string{"get", "tables", "--state", "x", "--states", "y"}, qerrors.ExitBadUsage},
		{[]string{"get", "tables"}, qerrors.ExitGenericError},

		{[]string{"get", "orgs", "--page", "0"}, qerrors.ExitBadUsage},
		{[]string{"get", "orgs"}, qerrors.ExitGenericError},

		{[]string{"get", "table-kinds", "--search", "ab"}, qerrors.ExitBadUsage},
		{[]string{"get", "table-kinds"}, qerrors.ExitGenericError},

		{[]string{"get", "standard-fields", "--table-kind", ""}, qerrors.ExitBadUsage},
		{[]string{"get", "standard-fields"}, qerrors.ExitGenericError},
		{[]string{"get", "standard-fields", "--table-kind", "car"}, qerrors.ExitGenericError},
		{[]string{"get", "concepts", "--table-kind", ""}, qerrors.ExitBadUsage},
		{[]string{"get", "concepts"}, qerrors.ExitGenericError},

		{[]string{"get", "destinations", "--order-by", "x"}, qerrors.ExitBadUsage},
		{[]string{"get", "destinations"}, qerrors.ExitGenericError},

		{[]string{"get", "error-incidents", "--limit", "0"}, qerrors.ExitBadUsage},
		{[]string{"get", "error-incidents"}, qerrors.ExitGenericError},

		{[]string{"get", "data", "orders", "--comment-thread-id", "nope"}, qerrors.ExitBadUsage},
		{[]string{"get", "data", "orders"}, qerrors.ExitGenericError},

		{[]string{"get", "rule-reviews", "--page", "0"}, qerrors.ExitBadUsage},
		{[]string{"get", "rule-reviews"}, qerrors.ExitGenericError},

		{[]string{"get", "history", "--since", "garbage"}, qerrors.ExitBadUsage},
		{[]string{"get", "history", "--author", "a@example.com", "--mine"}, qerrors.ExitBadUsage},
		{[]string{"get", "history", "--model-id", "0"}, qerrors.ExitBadUsage},
		{[]string{"get", "history", "--since", "24h", "--author", "a@example.com", "--table", "orders"}, qerrors.ExitGenericError},

		{[]string{"get", "alerts", "--order-by", "nope"}, qerrors.ExitBadUsage},
		{[]string{"get", "alerts", "--is-muted", "--is-row-level"}, qerrors.ExitBadUsage},
		{[]string{"get", "alerts", "--order-by", "id", "--table", "orders"}, qerrors.ExitGenericError},

		{[]string{"get", "warnings", "--data-source-id", "0"}, qerrors.ExitBadUsage},
		{[]string{"get", "warnings", "--data-source-id", "3", "--table", "orders"}, qerrors.ExitGenericError},

		{[]string{"get", "cloud-sources", "--state", "x", "--states", "y"}, qerrors.ExitBadUsage},
		{[]string{"get", "cloud-sources", "--chunk-size", "0"}, qerrors.ExitBadUsage},
		{[]string{"get", "cloud-sources", "--limit", "-1"}, qerrors.ExitBadUsage},
		{[]string{"get", "cloud-sources", "--limit", "0", "--table", "orders"}, qerrors.ExitGenericError},

		{[]string{"get", "composite-run-jobs", "--group-id", "nope"}, qerrors.ExitBadUsage},
		{[]string{"get", "composite-run-jobs", "--group-id", "550e8400-e29b-41d4-a716-446655440000"}, qerrors.ExitGenericError},

		{[]string{"get", "composite-dry-run-jobs", "--state", "bogus"}, qerrors.ExitBadUsage},
		{[]string{"get", "composite-dry-run-jobs", "--revision-id", "nope"}, qerrors.ExitBadUsage},
		{[]string{"get", "composite-dry-run-jobs", "--state", "error", "--table", "orders"}, qerrors.ExitGenericError},

		{[]string{"get", "files", "--cloud-source", "s3"}, qerrors.ExitBadUsage},
		{[]string{"get", "files", "--cloud-source-id", "0"}, qerrors.ExitBadUsage},
		{[]string{"get", "files", "--limit", "-1"}, qerrors.ExitBadUsage},
		{[]string{"get", "files", "--cloud-source", "s3", "--table", "orders"}, qerrors.ExitGenericError},

		{[]string{"get", "ingestion-jobs", "--watch-interval", "0"}, qerrors.ExitBadUsage},
		{[]string{"get", "ingestion-jobs", "--created-by", "nope"}, qerrors.ExitBadUsage},
		{[]string{"get", "ingestion-jobs", "--watch-interval", "5", "--table", "orders"}, qerrors.ExitGenericError},

		{[]string{"get", "profiling-jobs", "--search", "ab"}, qerrors.ExitBadUsage},
		{[]string{"get", "profiling-jobs", "--watch-interval", "0"}, qerrors.ExitBadUsage},
		{[]string{"get", "profiling-jobs", "--search", "abc"}, qerrors.ExitGenericError},

		{[]string{"get", "queue-messages", "--order-by", "x"}, qerrors.ExitBadUsage},
		{[]string{"get", "queue-messages", "--watch-interval", "0"}, qerrors.ExitBadUsage},
		{[]string{"get", "queue-messages", "--table", "orders"}, qerrors.ExitGenericError},

		{[]string{"get", "rule-revisions", "--table-kind", ""}, qerrors.ExitBadUsage},
		{[]string{"get", "rule-revisions", "--table-kind", "car", "--table-kind", " "}, qerrors.ExitBadUsage},
		{[]string{"get", "rule-revisions", "--for-table", "orders", "--table-kind", ""}, qerrors.ExitBadUsage},
		{[]string{"get", "rule-revisions", "--table-kind", "car"}, qerrors.ExitGenericError},
		{[]string{"get", "rule-revisions", "--for-table", "orders", "--table-kind", "car"}, qerrors.ExitGenericError},

		{[]string{"get", "table", ""}, qerrors.ExitBadUsage},
		{[]string{"get", "table", "orders", "--watch"}, qerrors.ExitBadUsage},
		{[]string{"get", "table", "orders"}, qerrors.ExitGenericError},
		{[]string{"get", "standard-field", ""}, qerrors.ExitBadUsage},
		{[]string{"get", "standard-field", "email"}, qerrors.ExitGenericError},
		{[]string{"get", "concept", ""}, qerrors.ExitBadUsage},
		{[]string{"get", "concept", "email"}, qerrors.ExitGenericError},
		{[]string{"get", "rule", ""}, qerrors.ExitBadUsage},
		{[]string{"get", "rule", "email_check"}, qerrors.ExitGenericError},
		{[]string{"get", "rule-upgrades", ""}, qerrors.ExitBadUsage},
		{[]string{"get", "rule-upgrades", "email_check"}, qerrors.ExitGenericError},
		{[]string{"get", "table-kind", ""}, qerrors.ExitBadUsage},
		{[]string{"get", "table-kind", "car"}, qerrors.ExitGenericError},
		{[]string{"get", "table-rule", "", "--table", "orders"}, qerrors.ExitBadUsage},
		{[]string{"get", "table-rule", "email_check", "--table", "orders"}, qerrors.ExitGenericError},
	}

	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			err := runRootWithoutContext(t, tc.args...)
			if err == nil {
				t.Fatalf("qctl %s exited 0", strings.Join(tc.args, " "))
			}
			if got := qerrors.GetExitCode(err); got != tc.want {
				t.Errorf("exit code = %d, want %d; error: %v", got, tc.want, err)
			}
			// Exit 1 alone would also describe a valid invocation that some
			// other failure stopped short of Bootstrap.
			if reached := strings.Contains(err.Error(), noServerError); reached != (tc.want == qerrors.ExitGenericError) {
				t.Errorf("reached Bootstrap = %v, want %v; error: %v", reached, !reached, err)
			}
		})
	}
}
