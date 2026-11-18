package delete

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

func setupReviewPolicyDeleteTestCommand() *cobra.Command {
	rootCmd := &cobra.Command{Use: "qctl"}
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "Output format")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "")
	rootCmd.PersistentFlags().CountP("verbose", "v", "")
	deleteCmd := &cobra.Command{Use: "delete"}
	deleteCmd.AddCommand(NewRuleReviewPolicyCommand())
	deleteCmd.AddCommand(NewDataReviewPolicyCommand())
	deleteCmd.AddCommand(NewChangeReviewPolicyCommand())
	rootCmd.AddCommand(deleteCmd)
	return rootCmd
}

func setupReviewPolicyDeleteEnv(t *testing.T) *testutil.MockAPIServer {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "token")
	return mock
}

func TestDeleteRuleReviewPolicy(t *testing.T) {
	mock := setupReviewPolicyDeleteEnv(t)

	deleted := false
	mock.RegisterHandler("DELETE", "/api/orgs/"+testOrgID+"/datasets/42/rule-review-policy", func(w http.ResponseWriter, r *http.Request) {
		deleted = true
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{"message": "deleted", "success": true})
	})

	cmd := setupReviewPolicyDeleteTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"delete", "rule-review-policy", "--table-id", "42"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !deleted {
		t.Fatal("expected DELETE request")
	}
	if want := "rule-review-policy/table-42 deleted\n"; buf.String() != want {
		t.Errorf("output = %q, want %q", buf.String(), want)
	}
}

func TestDeleteRuleReviewPolicyRequiresTable(t *testing.T) {
	_ = setupReviewPolicyDeleteEnv(t)

	cmd := setupReviewPolicyDeleteTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"delete", "rule-review-policy"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error without a table selector, got nil")
	}
	assertPolicyTableRequired(t, err)
}

func TestDeleteRuleReviewPolicyNotFound(t *testing.T) {
	mock := setupReviewPolicyDeleteEnv(t)

	mock.RegisterHandler("DELETE", "/api/orgs/"+testOrgID+"/datasets/42/rule-review-policy", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondError(w, http.StatusNotFound, "policy not found")
	})

	cmd := setupReviewPolicyDeleteTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"delete", "rule-review-policy", "--table-id", "42"})

	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error on 404, got nil")
	}
}

func TestDeleteDataReviewPolicy(t *testing.T) {
	mock := setupReviewPolicyDeleteEnv(t)

	deleted := false
	mock.RegisterHandler("DELETE", "/api/orgs/"+testOrgID+"/datasets/42/data-review-policy", func(w http.ResponseWriter, r *http.Request) {
		deleted = true
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{"message": "deleted", "success": true})
	})

	cmd := setupReviewPolicyDeleteTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"delete", "data-review-policy", "--table-id", "42"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !deleted {
		t.Fatal("expected DELETE request")
	}
	if want := "data-review-policy/table-42 deleted\n"; buf.String() != want {
		t.Errorf("output = %q, want %q", buf.String(), want)
	}
}

// registerPolicyTableNameLookup serves GET /datasets as a name search. The
// fixture's NAME is the numeric string "42" while its ID is 7: the one case an
// overloaded name-or-ID flag can never serve, and one a strconv.Atoi fast path
// cannot fake its way through.
func registerPolicyTableNameLookup(mock *testutil.MockAPIServer, lookedUp *string) {
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets",
		func(w http.ResponseWriter, r *http.Request) {
			*lookedUp = r.URL.Query().Get("name")
			testutil.RespondJSON(w, http.StatusOK, api.DatasetsListSchema{
				Results: []api.DataSetTinySchema{{ID: 7, Name: "42"}},
			})
		})
}

// runPolicyDelete drives one delete subcommand and reports whether the DELETE
// for table 7 was issued.
func runPolicyDelete(t *testing.T, mock *testutil.MockAPIServer, resource string, args ...string) (bool, error) {
	t.Helper()
	deleted := false
	mock.RegisterHandler("DELETE", "/api/orgs/"+testOrgID+"/datasets/7/"+resource,
		func(w http.ResponseWriter, r *http.Request) {
			deleted = true
			testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{"message": "deleted", "success": true})
		})

	cmd := setupReviewPolicyDeleteTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(append([]string{"delete", resource}, args...))
	return deleted, cmd.Execute()
}

func TestDeleteRuleReviewPolicyTableFlagIsAName(t *testing.T) {
	mock := setupReviewPolicyDeleteEnv(t)
	var lookedUp string
	registerPolicyTableNameLookup(mock, &lookedUp)

	deleted, err := runPolicyDelete(t, mock, "rule-review-policy", "--table", "42")
	if err != nil {
		t.Fatalf("--table must be looked up as a name: %v", err)
	}
	if lookedUp != "42" {
		t.Errorf("--table 42 must query name=42, queried %q", lookedUp)
	}
	if !deleted {
		t.Error("expected DELETE against the resolved table 7")
	}
}

func TestDeleteRuleReviewPolicyTableIDFlag(t *testing.T) {
	mock := setupReviewPolicyDeleteEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets",
		func(w http.ResponseWriter, r *http.Request) {
			t.Error("--table-id must not trigger a name lookup")
		})

	deleted, err := runPolicyDelete(t, mock, "rule-review-policy", "--table-id", "7")
	if err != nil {
		t.Fatalf("--table-id must select by ID: %v", err)
	}
	if !deleted {
		t.Error("expected DELETE against table 7")
	}
}

func TestDeleteDataReviewPolicyTableFlagIsAName(t *testing.T) {
	mock := setupReviewPolicyDeleteEnv(t)
	var lookedUp string
	registerPolicyTableNameLookup(mock, &lookedUp)

	deleted, err := runPolicyDelete(t, mock, "data-review-policy", "--table", "42")
	if err != nil {
		t.Fatalf("--table must be looked up as a name: %v", err)
	}
	if lookedUp != "42" {
		t.Errorf("--table 42 must query name=42, queried %q", lookedUp)
	}
	if !deleted {
		t.Error("expected DELETE against the resolved table 7")
	}
}

func TestDeleteDataReviewPolicyTableIDFlag(t *testing.T) {
	mock := setupReviewPolicyDeleteEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets",
		func(w http.ResponseWriter, r *http.Request) {
			t.Error("--table-id must not trigger a name lookup")
		})

	deleted, err := runPolicyDelete(t, mock, "data-review-policy", "--table-id", "7")
	if err != nil {
		t.Fatalf("--table-id must select by ID: %v", err)
	}
	if !deleted {
		t.Error("expected DELETE against table 7")
	}
}

// TestDeleteDataReviewPolicyRequiresTable is the other delete command's half of
// TestDeleteRuleReviewPolicyRequiresTable: both share one resolver, so both
// must reach the same message.
func TestDeleteDataReviewPolicyRequiresTable(t *testing.T) {
	_ = setupReviewPolicyDeleteEnv(t)

	cmd := setupReviewPolicyDeleteTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"delete", "data-review-policy"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error without a table selector, got nil")
	}
	assertPolicyTableRequired(t, err)
}

// assertPolicyTableRequired checks both halves of the contract: the message
// names both selectors and says why the table cannot be omitted, and the exit
// code is 2 so a script can tell "you typed the flags wrong" from "the server
// failed".
func assertPolicyTableRequired(t *testing.T, err error) {
	t.Helper()
	for _, want := range []string{"--table <name>", "--table-id <id>", "only table-level"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q, got: %v", want, err)
		}
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (ExitBadUsage) for %v", got, qerrors.ExitBadUsage, err)
	}
}

func TestDeleteChangeReviewPolicy(t *testing.T) {
	mock := setupReviewPolicyDeleteEnv(t)
	mock.RegisterHandler("DELETE", "/api/orgs/"+testOrgID+"/datasets/42/change-review-policy", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{"deleted": true})
	})

	cmd := setupReviewPolicyDeleteTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"delete", "change-review-policy", "--table-id", "42"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if want := "change-review-policy/table-42 deleted\n"; buf.String() != want {
		t.Errorf("output = %q, want %q", buf.String(), want)
	}
}

func TestDeleteChangeReviewPolicyWithoutAnOverrideExitsNotFound(t *testing.T) {
	mock := setupReviewPolicyDeleteEnv(t)
	mock.RegisterHandler("DELETE", "/api/orgs/"+testOrgID+"/datasets/42/change-review-policy", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{"deleted": false})
	})

	cmd := setupReviewPolicyDeleteTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"delete", "change-review-policy", "--table-id", "42"})

	err := cmd.Execute()
	if got := qerrors.GetExitCode(err); got != qerrors.ExitNotFound {
		t.Fatalf("exit code = %d, want %d (err: %v)", got, qerrors.ExitNotFound, err)
	}
	if strings.Contains(buf.String(), "deleted\n") {
		t.Errorf("reported a delete that did not happen: %q", buf.String())
	}
}

func TestDeleteChangeReviewPolicyRequiresTable(t *testing.T) {
	mock := setupReviewPolicyDeleteEnv(t)
	cmd := setupReviewPolicyDeleteTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"delete", "change-review-policy"})

	assertPolicyTableRequired(t, cmd.Execute())
	if reqs := mock.Requests(); len(reqs) != 0 {
		t.Errorf("sent requests before refusing: %v", reqs)
	}
}
