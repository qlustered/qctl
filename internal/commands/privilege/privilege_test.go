package privilege

import (
	"bytes"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const testOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

const (
	requestsPath = "/api/ops/" + testOrgID + "/privilege-requests"
	usersPath    = "/api/orgs/" + testOrgID + "/users"
)

// setupPrivilegeTestCommand builds a root that behaves like the shipped one
// for the things these tests assert about exit codes.
//
// Each piece of wiring below is copied from internal/commands/root/root.go
// because an assertion here depends on it: TypeArgsErrors makes an arity
// error exit 2, the FlagErrorFunc does the same for an unknown flag, and the
// PersistentPreRunE is what turns a missing required flag (and an -o value the
// command cannot render) into exit 2. Without them the harness reports 1 where
// the binary reports 2, and an exit-code test would pin the harness.
func setupPrivilegeTestCommand() *cobra.Command {
	rootCmd := &cobra.Command{Use: "qctl", SilenceUsage: true, SilenceErrors: true}
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format")
	rootCmd.PersistentFlags().Bool("no-headers", false, "Don't print column headers")
	rootCmd.PersistentFlags().String("columns", "", "Columns to display")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "Maximum width for table columns")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "Show secret fields")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "Allow http://")
	rootCmd.PersistentFlags().CountP("verbose", "v", "Verbosity level")
	rootCmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return qerrors.Wrap(qerrors.ExitBadUsage, err, "")
	})
	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		if err := cmdutil.ValidateOutputFormat(cmd); err != nil {
			return err
		}
		if err := cmd.ValidateRequiredFlags(); err != nil {
			return qerrors.Wrap(qerrors.ExitBadUsage, err, "")
		}
		return nil
	}
	rootCmd.AddCommand(NewCommand())
	cmdutil.TypeArgsErrors(rootCmd)
	cmdutil.StripKindRefs(rootCmd)
	return rootCmd
}

func newPrivilegeTestEnv(t *testing.T) *testutil.MockAPIServer {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")
	return mock
}

// runPrivilege executes `qctl privilege <args>` with stdin (may be "") and
// returns its stdout.
func runPrivilege(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	out, _, err := runPrivilegeStreams(t, stdin, args...)
	return out, err
}

func runPrivilegeStreams(t *testing.T, stdin string, args ...string) (string, string, error) {
	t.Helper()
	cmd := setupPrivilegeTestCommand()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetArgs(append([]string{"privilege"}, args...))
	err := cmd.Execute()
	return out.String(), errOut.String(), err
}

func assertExitCode(t *testing.T, err error, want int) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error exiting %d, got nil", want)
	}
	if got := qerrors.GetExitCode(err); got != want {
		t.Errorf("exit code = %d, want %d (err: %v)", got, want, err)
	}
}

func TestPrivilegeHelpDescribesTheWorkflow(t *testing.T) {
	out, err := runPrivilege(t, "", "--help")
	if err != nil {
		t.Fatalf("qctl privilege --help failed: %v", err)
	}
	for _, want := range []string{"Available Commands:", "request", "two-person rule", "qctl get privilege-requests"} {
		if !strings.Contains(out, want) {
			t.Errorf("help does not mention %q:\n%s", want, out)
		}
	}
}

// Bare `qctl privilege` names its subcommands and exits 2, as every non-root
// parent routed through cmdutil.SubcommandRequired does.
func TestPrivilegeBareNamesItsSubcommands(t *testing.T) {
	out, err := runPrivilege(t, "", []string{}...)
	assertExitCode(t, err, qerrors.ExitBadUsage)
	if want := "qctl privilege needs a subcommand: approve, reject, request"; err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
	if out != "" {
		t.Errorf("bare qctl privilege wrote to stdout:\n%s", out)
	}
}

func TestPrivilegeUnknownSubcommandExitsTwo(t *testing.T) {
	_, err := runPrivilege(t, "", "withdraw")
	assertExitCode(t, err, qerrors.ExitBadUsage)
}
