package warning

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const testOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

// setupWarningTestCommand builds the root the real one builds, minus the
// commands this package does not exercise. The persistent flags are the ones
// cmdutil.Bootstrap and the output plumbing read; --verbose MUST be CountP.
func setupWarningTestCommand() *cobra.Command {
	rootCmd := &cobra.Command{Use: "qctl"}
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format")
	rootCmd.PersistentFlags().Bool("no-headers", false, "")
	rootCmd.PersistentFlags().String("columns", "", "")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "")
	rootCmd.PersistentFlags().CountP("verbose", "v", "")
	// Mirrors root.go: an -o value a verb does not declare exits 2.
	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		return cmdutil.ValidateOutputFormat(cmd)
	}
	rootCmd.AddCommand(NewCommand())
	return rootCmd
}

func newWarningTestEnv(t *testing.T) *testutil.MockAPIServer {
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

// runWarningCommandSplit keeps stdout and stderr apart: the partial-failure
// block is the one output this package writes to stderr, and a combined buffer
// cannot prove it went there.
func runWarningCommandSplit(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := setupWarningTestCommand()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

// registerTableResolveForWarnings answers the name-only lookup `--table orders`
// makes, resolving it to dataset 42 — the id the routes below are registered
// on — and counts it so a test can assert it never happened.
func registerTableResolveForWarnings(mock *testutil.MockAPIServer) *int {
	calls := 0
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, _ *http.Request) {
		calls++
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(
			`{"total_rows":1,"page":1,"results":[{"id":42,"name":"orders","state":"active"}]}`))
	})
	return &calls
}

func intPtr(v int) *int       { return &v }
func strPtr(v string) *string { return &v }
