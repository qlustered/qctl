package root

import (
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/agentskill"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/spf13/pflag"
)

// runGetTablesWithoutConfig runs `qctl get tables <args>` on the real tree
// with an empty HOME and no server or org in the environment, so any command
// that reaches Bootstrap fails with "no API server specified". It restores
// every local flag of `get tables` the run changed, since runRoot resets only
// root's --output.
func runGetTablesWithoutConfig(t *testing.T, args ...string) error {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("QCTL_SERVER", "")
	t.Setenv("QCTL_ORG", "")
	t.Setenv(agentskill.CodexHomeEnv, t.TempDir())
	t.Setenv(agentskill.ClaudeConfigDirEnv, t.TempDir())

	tables, _, err := GetRootCmd().Find([]string{"get", "tables"})
	if err != nil || tables.Name() != "tables" {
		t.Fatalf("finding get tables: %v", err)
	}
	t.Cleanup(func() {
		tables.LocalFlags().VisitAll(func(f *pflag.Flag) {
			if f.Changed {
				_ = f.Value.Set(f.DefValue)
				f.Changed = false
			}
		})
	})

	_, err = runRoot(t, append([]string{"get", "tables"}, args...)...)
	return err
}

// An unknown --columns name is bad usage whatever -o says, and it is caught
// before the command looks for a config: with no context configured, the
// answer is still the column, not the missing server.
func TestUnknownColumnExitsTwoBeforeBootstrap(t *testing.T) {
	for _, args := range [][]string{
		{"--columns", "nope"},
		{"-o", "json", "--columns", "nope"},
		{"--for-rule", "x", "--columns", "nope"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			err := runGetTablesWithoutConfig(t, args...)
			if err == nil {
				t.Fatal("qctl get tables accepted an unknown column")
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d (ExitBadUsage); error: %v", code, qerrors.ExitBadUsage, err)
			}
			msg := err.Error()
			// Both displays' columns: the listing's, then the --for-rule matches'.
			if !strings.Contains(msg, `unknown column "nope"`) || !strings.Contains(msg, "valid: ") || !strings.Contains(msg, " bad_rows_count,") ||
				!strings.HasSuffix(msg, ", dataset_id, match, standard_fields, missing") {
				t.Errorf("message must name the column and list the valid ones: %q", msg)
			}
			if strings.Contains(msg, "no API server") {
				t.Errorf("the command reached Bootstrap: %q", msg)
			}
		})
	}
}

// The control for the test above: declared names, including one only the
// --for-rule display renders, pass the check and run into the missing server.
func TestDeclaredColumnStillReachesBootstrap(t *testing.T) {
	for _, args := range [][]string{
		{"--columns", "name"},
		{"-o", "json", "--columns", "name"},
		{"--for-rule", "x", "--columns", "name,match"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			err := runGetTablesWithoutConfig(t, args...)
			if err == nil {
				t.Fatal("qctl get tables succeeded with no config")
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitGenericError {
				t.Errorf("exit code = %d, want %d; error: %v", code, qerrors.ExitGenericError, err)
			}
			if !strings.Contains(err.Error(), "no API server") {
				t.Errorf("want the no-server error, got %q", err)
			}
		})
	}
}
