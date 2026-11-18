package data_review //nolint:revive // Package name mirrors the hyphenated command.

import (
	"bytes"
	"testing"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const testOrgID = "b2c3d4e5-f6a7-4901-bcde-f23456789012"

type testEnv struct {
	mock *testutil.MockAPIServer
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")
	return &testEnv{mock: mock}
}

func testRoot() *cobra.Command {
	root := &cobra.Command{Use: "qctl", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().String("server", "", "")
	root.PersistentFlags().String("user", "", "")
	root.PersistentFlags().StringP("output", "o", "table", "")
	root.PersistentFlags().Bool("no-headers", false, "")
	root.PersistentFlags().String("columns", "", "")
	root.PersistentFlags().Int("max-column-width", 80, "")
	root.PersistentFlags().Bool("allow-plaintext-secrets", false, "")
	root.PersistentFlags().Bool("allow-insecure-http", false, "")
	root.PersistentFlags().CountP("verbose", "v", "")
	// Mirrors root.go: cobra's own flag errors exit 2, not 1.
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return qerrors.Wrap(qerrors.ExitBadUsage, err, "")
	})
	// Mirrors root.go: a missing required flag exits 2, not 1, and so does an
	// -o value the verb does not declare.
	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		if err := cmd.ValidateRequiredFlags(); err != nil {
			return qerrors.Wrap(qerrors.ExitBadUsage, err, "")
		}
		return cmdutil.ValidateOutputFormat(cmd)
	}
	root.AddCommand(NewCommand(), NewGroupCommand())
	cmdutil.TypeArgsErrors(root)
	// Mirrors root.go: a kind/ prefix the argument's slot accepts is stripped.
	cmdutil.StripKindRefs(root)
	return root
}

// exec runs `qctl data-review <args...>` against the mock server.
func (e *testEnv) exec(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	return e.execPath(t, append([]string{"data-review"}, args...)...)
}

// execGroup runs `qctl data-review-group <args...>` against the mock server.
func (e *testEnv) execGroup(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	return e.execPath(t, append([]string{"data-review-group"}, args...)...)
}

func (e *testEnv) execPath(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	root := testRoot()
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(args)
	err := root.Execute()
	return stdout.String(), stderr.String(), err
}
