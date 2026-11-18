package get

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
	"github.com/qlustered/qctl/internal/review_policies"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

func setupDataReviewPolicyTestCommand() *cobra.Command {
	rootCmd := &cobra.Command{Use: "qctl"}
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "Output format")
	rootCmd.PersistentFlags().Bool("no-headers", false, "")
	rootCmd.PersistentFlags().String("columns", "", "")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "")
	rootCmd.PersistentFlags().CountP("verbose", "v", "")
	getCmd := &cobra.Command{Use: "get"}
	getCmd.AddCommand(NewDataReviewPolicyCommand())
	rootCmd.AddCommand(getCmd)
	return rootCmd
}

func dataReviewPolicyJSON(source string) map[string]interface{} {
	return map[string]interface{}{
		"id":                       "22222222-3333-4444-5555-666666666666",
		"organization_id":          testOrgID,
		"dataset_id":               nil,
		"requires_review":          true,
		"min_reviewers":            3,
		"designated_reviewer_ids":  []string{"7f3c2a9e-1111-2222-3333-444455556666"},
		"auto_release_on_approval": true,
		"source":                   source,
	}
}

func TestGetDataReviewPolicyOrgTable(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/data-review-policy", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, dataReviewPolicyJSON("organization"))
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "token")

	cmd := setupDataReviewPolicyTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"get", "data-review-policy"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	for _, want := range []string{"organization", "3", "SOURCE", "MIN-REVIEWERS"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("output missing %q:\n%s", want, buf.String())
		}
	}
}

func TestGetDataReviewPolicyYAMLRoundTrip(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/data-review-policy", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, dataReviewPolicyJSON("dataset"))
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "token")

	cmd := setupDataReviewPolicyTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"get", "data-review-policy", "--table-id", "42", "-o", "yaml"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	var m review_policies.DataReviewPolicyManifest
	if err := pkgmanifest.StrictUnmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("yaml output is not apply-compatible: %v\n%s", err, buf.String())
	}
	if m.Kind != "DataReviewPolicy" || m.APIVersion != pkgmanifest.APIVersionV1 {
		t.Errorf("bad kind/apiVersion: %s %s", m.Kind, m.APIVersion)
	}
	if m.Spec.DatasetID != 42 {
		t.Errorf("spec.dataset_id = %d, want 42", m.Spec.DatasetID)
	}
	if m.Spec.RequiresReview == nil || !*m.Spec.RequiresReview {
		t.Errorf("spec.requires_review not set")
	}
	if len(m.Spec.DesignatedReviewerIDs) != 1 || m.Spec.DesignatedReviewerIDs[0] != "7f3c2a9e-1111-2222-3333-444455556666" {
		t.Errorf("spec.designated_reviewer_ids = %v, want the fixture UUID", m.Spec.DesignatedReviewerIDs)
	}
	if errs := m.Validate(); len(errs) != 0 {
		t.Errorf("round-tripped manifest fails validation: %v", errs)
	}
}

func TestGetDataReviewPolicyOrgYAML(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/data-review-policy", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, dataReviewPolicyJSON("organization"))
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "token")

	cmd := setupDataReviewPolicyTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"get", "data-review-policy", "-o", "yaml"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	var m review_policies.DataReviewPolicyManifest
	if err := pkgmanifest.StrictUnmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("yaml output is not apply-compatible: %v\n%s", err, buf.String())
	}
	if m.Metadata.Name != "org-data-review-policy" {
		t.Errorf("metadata.name = %q, want %q", m.Metadata.Name, "org-data-review-policy")
	}
	if m.Spec.DatasetID != 0 {
		t.Errorf("spec.dataset_id = %d, want 0 (org manifest must not carry a dataset scope)", m.Spec.DatasetID)
	}
	if m.Status == nil || m.Status.Source != "organization" {
		t.Errorf("status.source missing or wrong, got %+v", m.Status)
	}
}

func TestGetDataReviewPolicyInvalidTableID(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "token")

	cmd := setupDataReviewPolicyTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"get", "data-review-policy", "--table-id", "0"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected an error for --table-id 0, got nil")
	}
	if !strings.Contains(err.Error(), "--table-id must be a positive integer") {
		t.Errorf("expected error containing %q, got: %v", "--table-id must be a positive integer", err)
	}
	assertBadUsageExit(t, err)
}

// assertBadUsageExit pins the exit code, which is the half of the contract a
// message assertion cannot see: a downgrade from 2 to 1 makes "you typed the
// flags wrong" indistinguishable from "the server failed" while every
// substring assertion keeps passing.
func assertBadUsageExit(t *testing.T, err error) {
	t.Helper()
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (ExitBadUsage) for %v", got, qerrors.ExitBadUsage, err)
	}
}

// newPolicyTestEnv is the shared setup for both review-policy get commands:
// an isolated config/credential pair pointed at a fresh mock server.
func newPolicyTestEnv(t *testing.T) *testutil.MockAPIServer {
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

func execGetDataReviewPolicy(t *testing.T, args ...string) error {
	t.Helper()
	cmd := setupDataReviewPolicyTestCommand()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(append([]string{"get", "data-review-policy"}, args...))
	return cmd.Execute()
}

// registerTableNameLookup serves GET /datasets as a name search returning one
// table, and records the name that was queried. The fixture's name is the
// numeric string "42" while its ID is 7: the one case an overloaded
// name-or-ID flag can never serve, and one a strconv.Atoi fast path cannot
// fake its way through.
func registerTableNameLookup(t *testing.T, mock *testutil.MockAPIServer, lookedUp *string) {
	t.Helper()
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets",
		func(w http.ResponseWriter, r *http.Request) {
			*lookedUp = r.URL.Query().Get("name")
			testutil.RespondJSON(w, http.StatusOK, api.DatasetsListSchema{
				Results: []api.DataSetTinySchema{{ID: 7, Name: "42"}},
			})
		})
}

// rejectTableNameLookup fails the test if a name lookup is attempted at all —
// what proves --table-id went straight to the ID without a round trip.
func rejectTableNameLookup(t *testing.T, mock *testutil.MockAPIServer) {
	t.Helper()
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets",
		func(w http.ResponseWriter, r *http.Request) {
			t.Error("--table-id must not trigger a name lookup")
		})
}

func TestGetDataReviewPolicyTableFlagIsAName(t *testing.T) {
	mock := newPolicyTestEnv(t)
	var lookedUp string
	registerTableNameLookup(t, mock, &lookedUp)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/7/data-review-policy",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, dataReviewPolicyJSON("dataset"))
		})

	if err := execGetDataReviewPolicy(t, "--table", "42"); err != nil {
		t.Fatalf("--table must be looked up as a name: %v", err)
	}
	if lookedUp != "42" {
		t.Errorf("--table 42 must query name=42, queried %q", lookedUp)
	}
}

func TestGetDataReviewPolicyTableIDFlag(t *testing.T) {
	mock := newPolicyTestEnv(t)
	rejectTableNameLookup(t, mock)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/7/data-review-policy",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, dataReviewPolicyJSON("dataset"))
		})

	if err := execGetDataReviewPolicy(t, "--table-id", "7"); err != nil {
		t.Fatalf("--table-id must select by ID: %v", err)
	}
}
