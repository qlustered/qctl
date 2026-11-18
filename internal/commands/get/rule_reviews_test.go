package get

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/rule_versions"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const reviewsTestOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

var (
	rrTestReviewUUID   = uuid.MustParse("018f3c2e-4d5a-7b89-a1c2-3d4e5f607182")
	rrTestRevisionUUID = uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
)

func setupRuleReviewsTestCommand() *cobra.Command {
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
	getCmd := &cobra.Command{Use: "get"}
	getCmd.AddCommand(NewRuleReviewsCommand())
	rootCmd.AddCommand(getCmd)
	return rootCmd
}

func newRuleReviewsTestEnv(t *testing.T) *testutil.MockAPIServer {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", reviewsTestOrgID)
	env.SetupCredential(endpointKey, reviewsTestOrgID, "test-token")
	return mock
}

func sampleInboxItem() api.ReviewListItemSchema {
	datasetName := "orders"
	return api.ReviewListItemSchema{
		ID:                    rrTestReviewUUID,
		Title:                 "Review dedupe-emails@2.1.0",
		RuleRevisionID:        rrTestRevisionUUID,
		RuleRevisionName:      "dedupe-emails",
		RuleRevisionRelease:   "2.1.0",
		DatasetID:             testutil.IntPtr(42),
		DatasetName:           &datasetName,
		Status:                api.ReviewRequestStatusOpen,
		CurrentApprovals:      1,
		RequiredApprovals:     2,
		HasDryRun:             true,
		AssignedReviewerCount: 1,
		CreatedAt:             time.Now().Add(-24 * time.Hour),
		UpdatedAt:             time.Now().Add(-1 * time.Hour),
	}
}

func TestGetRuleReviewsInboxTable(t *testing.T) {
	mock := newRuleReviewsTestEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+reviewsTestOrgID+"/rule-reviews",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.ReviewsInboxResponseSchema{
				Reviews: []api.ReviewListItemSchema{sampleInboxItem()},
				Page:    1, Limit: 20, Total: 1,
			})
		})

	cmd := setupRuleReviewsTestCommand()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"get", "rule-reviews"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{
		rrTestReviewUUID.String(), // full UUID, never truncated by us
		"dedupe-emails",
		"2.1.0",
		"orders",
		"open",
		"1/2",
		"yes",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %q\n%s", want, out.String())
		}
	}
	if strings.Contains(errOut.String(), "More reviews") {
		t.Errorf("no hint expected when total fits one page\n%s", errOut.String())
	}
}

// -o name prints rule-review/<uuid>, which every review verb takes.
func TestGetRuleReviewsInboxNamePrintsReviewRefs(t *testing.T) {
	mock := newRuleReviewsTestEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+reviewsTestOrgID+"/rule-reviews",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.ReviewsInboxResponseSchema{
				Reviews: []api.ReviewListItemSchema{sampleInboxItem()},
				Page:    1, Limit: 20, Total: 1,
			})
		})

	cmd := setupRuleReviewsTestCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"get", "rule-reviews", "-o", "name"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "rule-review/" + rrTestReviewUUID.String() + "\n"; out.String() != want {
		t.Errorf("-o name = %q, want %q", out.String(), want)
	}
}

func TestGetRuleReviewsInboxPaginationHintAndAll(t *testing.T) {
	mock := newRuleReviewsTestEnv(t)
	var gotQuery string
	mock.RegisterHandler("GET", "/api/orgs/"+reviewsTestOrgID+"/rule-reviews",
		func(w http.ResponseWriter, r *http.Request) {
			gotQuery = r.URL.RawQuery
			testutil.RespondJSON(w, http.StatusOK, api.ReviewsInboxResponseSchema{
				Reviews: []api.ReviewListItemSchema{sampleInboxItem()},
				Page:    1, Limit: 20, Total: 55,
			})
		})

	cmd := setupRuleReviewsTestCommand()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"get", "rule-reviews", "--all", "--status", "open"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(gotQuery, "my_reviews_only=false") {
		t.Errorf("--all must send my_reviews_only=false, query: %s", gotQuery)
	}
	if !strings.Contains(gotQuery, "status=open") {
		t.Errorf("--status must pass through, query: %s", gotQuery)
	}
	want := "More reviews — fetch the next page with --page 2."
	if !strings.Contains(errOut.String(), want) {
		t.Errorf("stderr missing %q\n%s", want, errOut.String())
	}
}

func TestGetRuleReviewsInbox403HintsRuleMode(t *testing.T) {
	mock := newRuleReviewsTestEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+reviewsTestOrgID+"/rule-reviews",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusForbidden, map[string]interface{}{
				"detail": map[string]interface{}{"msg": "REVIEW_EDIT required"},
			})
		})

	cmd := setupRuleReviewsTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"get", "rule-reviews"})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "--rule") {
		t.Fatalf("403 must hint at --rule mode, got: %v", err)
	}
}

func TestGetRuleReviewsPerRuleMode(t *testing.T) {
	mock := newRuleReviewsTestEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+reviewsTestOrgID+"/rule-revisions",
		func(w http.ResponseWriter, r *http.Request) {
			totalRows, page := 1, 1
			testutil.RespondJSON(w, http.StatusOK, rule_versions.RuleRevisionList{
				Results: []rule_versions.RuleRevisionTiny{{
					ID: rrTestRevisionUUID, Name: "dedupe-emails", Release: "2.1.0", State: "in_review",
				}},
				TotalRows: &totalRows,
				Page:      &page,
			})
		})
	var gotQuery string
	mock.RegisterHandler("GET", "/api/orgs/"+reviewsTestOrgID+"/rule-revisions/"+rrTestRevisionUUID.String()+"/rule-reviews",
		func(w http.ResponseWriter, r *http.Request) {
			gotQuery = r.URL.RawQuery
			datasetID := 42
			testutil.RespondJSON(w, http.StatusOK, api.ReviewListResponseSchema{
				Reviews: []api.ReviewResponseSchema{{
					ID: rrTestReviewUUID, RuleRevisionID: rrTestRevisionUUID,
					Title: "Review dedupe-emails@2.1.0", Status: api.ReviewRequestStatusOpen,
					CurrentApprovals: 0, RequiredApprovals: 2,
					DatasetID:           &datasetID,
					AssignedReviewerIds: []openapi_types.UUID{},
					RuleRevision: &api.CapiSchemasRuleReviewSchemaRuleRevisionTinyInfoSchema{
						ID: rrTestRevisionUUID, Name: "dedupe-emails", Release: "2.1.0",
					},
				}},
			})
		})

	cmd := setupRuleReviewsTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"get", "rule-reviews", "--rule", "dedupe-emails", "--status", "open"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(gotQuery, "status_filter=open") {
		t.Errorf("--status must map to status_filter, query: %s", gotQuery)
	}
	for _, want := range []string{rrTestReviewUUID.String(), "dedupe-emails", "0/2"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("output missing %q\n%s", want, buf.String())
		}
	}
}

func TestGetRuleReviewsPerRuleRejectsInboxFlags(t *testing.T) {
	_ = newRuleReviewsTestEnv(t)
	cmd := setupRuleReviewsTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"get", "rule-reviews", "--rule", "dedupe-emails", "--all"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("--all with --rule must be rejected")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (ExitBadUsage) for %v", got, qerrors.ExitBadUsage, err)
	}
}

// I1: the converse of TestGetRuleReviewsPerRuleRejectsInboxFlags. Without
// --rule this is inbox mode, and --table/--table-id/--release only make sense
// scoped to a rule. Before the fix these were silently ignored: the command
// returned the whole (unfiltered) inbox at exit 0 instead of saying so.
func TestGetRuleReviewsInboxRejectsRuleOnlyFlags(t *testing.T) {
	for _, args := range [][]string{
		{"--table", "orders"},
		{"--table-id", "7"},
		{"--release", "2.1.0"},
	} {
		t.Run(args[0], func(t *testing.T) {
			mock := newRuleReviewsTestEnv(t)
			mock.RegisterHandler("GET", "/api/orgs/"+reviewsTestOrgID+"/rule-reviews",
				func(_ http.ResponseWriter, _ *http.Request) {
					t.Error("a rule-only flag without --rule must fail before the inbox is listed")
				})
			mock.RegisterHandler("GET", "/api/orgs/"+reviewsTestOrgID+"/datasets",
				func(_ http.ResponseWriter, _ *http.Request) {
					t.Error("a rule-only flag without --rule must fail before any table lookup")
				})

			cmd := setupRuleReviewsTestCommand()
			var buf bytes.Buffer
			cmd.SetOut(&buf)
			cmd.SetErr(&buf)
			cmd.SetArgs(append([]string{"get", "rule-reviews"}, args...))

			err := cmd.Execute()
			if err == nil || !strings.Contains(err.Error(), "only applies with --rule") {
				t.Fatalf("expected the rule-only-flag error, got: %v", err)
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d (ExitBadUsage) for %v", got, qerrors.ExitBadUsage, err)
			}
		})
	}
}

func TestGetRuleReviewsInboxRawJSON(t *testing.T) {
	mock := newRuleReviewsTestEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+reviewsTestOrgID+"/rule-reviews",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.ReviewsInboxResponseSchema{
				Reviews: []api.ReviewListItemSchema{sampleInboxItem()},
				Page:    1, Limit: 20, Total: 1,
			})
		})

	cmd := setupRuleReviewsTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"get", "rule-reviews", "-o", "json"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	// Raw wire keys, not display-struct keys.
	for _, want := range []string{`"reviews"`, `"rule_revision_name"`, `"has_dry_run"`, `"total"`} {
		if !strings.Contains(out, want) {
			t.Errorf("raw JSON missing %q\n%s", want, out)
		}
	}
}

// registerRuleReviewsForTable wires the two calls a per-rule listing makes
// after the table is resolved: the rule revision lookup, and the review list
// for that revision. It records the dataset_id the listing was scoped by.
func registerRuleReviewsForTable(mock *testutil.MockAPIServer, gotDatasetID *string) {
	mock.RegisterHandler("GET", "/api/orgs/"+reviewsTestOrgID+"/rule-revisions",
		func(w http.ResponseWriter, r *http.Request) {
			totalRows, page := 1, 1
			testutil.RespondJSON(w, http.StatusOK, rule_versions.RuleRevisionList{
				Results: []rule_versions.RuleRevisionTiny{{
					ID: rrTestRevisionUUID, Name: "dedupe-emails", Release: "2.1.0", State: "in_review",
				}},
				TotalRows: &totalRows,
				Page:      &page,
			})
		})
	mock.RegisterHandler("GET", "/api/orgs/"+reviewsTestOrgID+"/rule-revisions/"+rrTestRevisionUUID.String()+"/rule-reviews",
		func(w http.ResponseWriter, r *http.Request) {
			*gotDatasetID = r.URL.Query().Get("dataset_id")
			testutil.RespondJSON(w, http.StatusOK, api.ReviewListResponseSchema{
				Reviews: []api.ReviewResponseSchema{},
			})
		})
}

func TestGetRuleReviewsTableFlagIsAName(t *testing.T) {
	mock := newRuleReviewsTestEnv(t)
	var lookedUp, gotDatasetID string
	// The fixture's NAME is the numeric string "42" and its ID is 7 — the one
	// case an overloaded name-or-ID flag can never serve.
	mock.RegisterHandler("GET", "/api/orgs/"+reviewsTestOrgID+"/datasets",
		func(w http.ResponseWriter, r *http.Request) {
			lookedUp = r.URL.Query().Get("name")
			testutil.RespondJSON(w, http.StatusOK, api.DatasetsListSchema{
				Results: []api.DataSetTinySchema{{ID: 7, Name: "42"}},
			})
		})
	registerRuleReviewsForTable(mock, &gotDatasetID)

	cmd := setupRuleReviewsTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"get", "rule-reviews", "--rule", "dedupe-emails", "--table", "42"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("--table must be looked up as a name: %v", err)
	}
	if lookedUp != "42" {
		t.Errorf("--table 42 must query name=42, queried %q", lookedUp)
	}
	if gotDatasetID != "7" {
		t.Errorf("the listing must be scoped by the resolved ID 7, got dataset_id=%q", gotDatasetID)
	}
}

func TestGetRuleReviewsTableIDFlag(t *testing.T) {
	mock := newRuleReviewsTestEnv(t)
	var gotDatasetID string
	mock.RegisterHandler("GET", "/api/orgs/"+reviewsTestOrgID+"/datasets",
		func(w http.ResponseWriter, r *http.Request) {
			t.Error("--table-id must not trigger a name lookup")
		})
	registerRuleReviewsForTable(mock, &gotDatasetID)

	cmd := setupRuleReviewsTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"get", "rule-reviews", "--rule", "dedupe-emails", "--table-id", "7"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("--table-id must select by ID: %v", err)
	}
	if gotDatasetID != "7" {
		t.Errorf("the listing must be scoped by ID 7, got dataset_id=%q", gotDatasetID)
	}
}

func TestGetRuleReviewsHelpMentionsGuide(t *testing.T) {
	cmd := setupRuleReviewsTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"get", "rule-reviews", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "qctl guide review-rules") {
		t.Errorf("get rule-reviews --help missing guide breadcrumb\n%s", buf.String())
	}
}
