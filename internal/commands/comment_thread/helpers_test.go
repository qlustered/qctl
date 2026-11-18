//nolint:revive // Tests intentionally match the command package directory.
package comment_thread

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const testOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

var (
	testThreadUUID  = uuid.MustParse("0190bbbb-1111-2222-3333-444444444444")
	testAuthorUUID  = uuid.MustParse("0190dddd-1111-2222-3333-444444444444")
	testCommentUUID = uuid.MustParse("0190cccc-1111-2222-3333-444444444444")
	testUserUUID    = uuid.MustParse("0190eeee-1111-2222-3333-444444444444")

	// threadCommentsPath is the comment collection of testThreadUUID on
	// dataset 42. A var, not a const, so the id is spelled once: a const
	// cannot call uuid.MustParse, and a second copy of the literal is a copy
	// that can drift.
	threadCommentsPath = "/api/orgs/" + testOrgID + "/datasets/42/comment-threads/" +
		testThreadUUID.String() + "/comments"
)

func ptr[T any](v T) *T { return &v }

// setupThreadTestCommand builds the root the real one builds, minus the
// commands this package does not exercise. The persistent flags are the ones
// cmdutil.Bootstrap and the output plumbing read; --verbose MUST be CountP.
func setupThreadTestCommand() *cobra.Command {
	rootCmd := &cobra.Command{Use: "qctl"}
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format")
	rootCmd.PersistentFlags().Bool("no-headers", false, "Don't print column headers")
	rootCmd.PersistentFlags().String("columns", "", "Columns to display")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "Maximum width for table columns")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "Show secret fields")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "Allow http://")
	rootCmd.PersistentFlags().CountP("verbose", "v", "Verbosity level")
	// Mirrors root.go: an -o value a verb does not declare exits 2.
	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		return cmdutil.ValidateOutputFormat(cmd)
	}
	rootCmd.AddCommand(NewCommand())
	return rootCmd
}

// newThreadTestEnv wires temp HOME + mock transport + config/credential.
func newThreadTestEnv(t *testing.T) *testutil.MockAPIServer {
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

// registerTableResolve answers the name-only lookup --table <name> makes, and
// counts it so a test can assert it never happened. Every test in this arc
// happens to point at the same table, which is not a reason to hard-code it.
//
//nolint:unparam // name/id are the helper's contract, not this test's constants.
func registerTableResolve(mock *testutil.MockAPIServer, name string, id int) *int {
	calls := 0
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, _ *http.Request) {
		calls++
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(
			`{"total_rows":1,"page":1,"results":[{"id":`+strconv.Itoa(id)+`,"name":"`+name+`","state":"active"}]}`))
	})
	return &calls
}

// registerCreate answers the create route on dataset 42, capturing the request
// body as generic JSON — the wire shape is what these tests assert on, and a
// decode into the generated struct would paper over a null/[] mistake.
func registerCreate(mock *testutil.MockAPIServer, gotBody *map[string]interface{},
	result api.CommentThreadCreateResponseSchema) {
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/42/comment-threads",
		func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(gotBody)
			testutil.RespondJSON(w, http.StatusCreated, result)
		})
}

// registerAddComment answers the add-comment route, capturing the request body
// as generic JSON — the mention tests assert on the cc line's exact bytes, and
// a decode into the generated struct would hide a null/"" mistake.
func registerAddComment(mock *testutil.MockAPIServer, gotBody *map[string]interface{}) {
	mock.RegisterHandler("POST", threadCommentsPath, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(gotBody)
		testutil.RespondJSON(w, http.StatusCreated, sampleThreadComment())
	})
}

// registerUsersList answers the org membership sweep --mention resolves against.
func registerUsersList(mock *testutil.MockAPIServer) {
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/users", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.UsersListSchema{
			Results: []api.UserListItemSchema{
				{ID: testUserUUID, Email: "alice@corp.com", FirstName: "Alice", LastName: "Chen"},
			},
		})
	})
}

func sampleThreadComment() api.CommentResponseSchema {
	return api.CommentResponseSchema{
		ID:        testCommentUUID,
		ThreadID:  testThreadUUID,
		AuthorID:  testAuthorUUID,
		Body:      "please look",
		CreatedAt: time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC),
	}
}

func sampleCreateResult(dropped []api.DroppedTargetSchema) api.CommentThreadCreateResponseSchema {
	created := time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC)
	result := api.CommentThreadCreateResponseSchema{
		Thread: api.CommentThreadResponseSchema{
			ID:          testThreadUUID,
			CreatedAt:   created,
			UpdatedAt:   created,
			CreatedByID: testAuthorUUID,
			Targets: &[]api.CommentThreadTargetResponseSchema{
				{RowID: 1042, FieldName: ptr("amount")},
			},
		},
	}
	if dropped != nil {
		result.DroppedTargets = &dropped
	}
	return result
}

// runThreadCommandSplit executes the tree with stdout and stderr kept apart:
// the dropped-target warning is the one output this package writes to stderr,
// and a combined buffer cannot prove it went there.
func runThreadCommandSplit(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	return runThreadCommandWithStdin(t, "", args...)
}

// runThreadCommand is the combined-output form for tests that only care that
// the command succeeded or failed. stdin feeds "--body-file -".
func runThreadCommand(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	stdout, stderr, err := runThreadCommandWithStdin(t, stdin, args...)
	return stdout + stderr, err
}

func runThreadCommandWithStdin(t *testing.T, stdin string, args ...string) (string, string, error) {
	t.Helper()
	cmd := setupThreadTestCommand()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(bytes.NewBufferString(stdin))
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), errOut.String(), err
}
