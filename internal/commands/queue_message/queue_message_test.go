//nolint:revive // Tests intentionally match the command package directory.
package queue_message

import (
	"bytes"
	"testing"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const testOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

// queuePath is the org-scoped prefix every route in this group hangs off.
const queuePath = "/api/orgs/" + testOrgID + "/queue-messages"

// setupQueueMessageTestCommand builds a root that behaves like the shipped one
// for the things these tests assert.
//
// cmdutil.TypeArgsErrors is not decoration: internal/commands/root/root.go
// calls it after building the tree, and it is what turns a cobra ARITY error
// into exit 2. A hand-built root without it reports exit 1 for a missing
// argument while reporting 2 for a malformed one — a split the real binary
// does not have, and an exit-code assertion written against it would be
// pinning the harness rather than the command.
func setupQueueMessageTestCommand() *cobra.Command {
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
	// Mirrors root.go: an -o value the verb does not declare exits 2.
	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		return cmdutil.ValidateOutputFormat(cmd)
	}
	rootCmd.AddCommand(NewCommand())
	cmdutil.TypeArgsErrors(rootCmd)
	return rootCmd
}

func newQueueMessageTestEnv(t *testing.T) *testutil.MockAPIServer {
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

func runQueueMessage(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := setupQueueMessageTestCommand()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(append([]string{"queue-message"}, args...))
	err := cmd.Execute()
	return out.String(), errOut.String(), err
}
