package describe

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const rrDescribeOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

var (
	rrDescribeReviewUUID   = uuid.MustParse("018f3c2e-4d5a-7b89-a1c2-3d4e5f607182")
	rrDescribeRevisionUUID = uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
)

func setupRuleReviewDescribeCommand() *cobra.Command {
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
	describeCmd := &cobra.Command{Use: "describe"}
	describeCmd.AddCommand(NewRuleReviewCommand())
	rootCmd.AddCommand(describeCmd)
	return rootCmd
}

func registerRuleReviewDetail(t *testing.T, status api.ReviewRequestStatus) {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", rrDescribeOrgID)
	env.SetupCredential(endpointKey, rrDescribeOrgID, "test-token")

	datasetName := "orders"
	requestorID := uuid.MustParse("aaaaaaaa-1111-2222-3333-444444444444")
	mock.RegisterHandler("GET", "/api/orgs/"+rrDescribeOrgID+"/rule-reviews/"+rrDescribeReviewUUID.String(),
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.ReviewDetailResponseSchema{
				ID:                rrDescribeReviewUUID,
				RuleRevisionID:    rrDescribeRevisionUUID,
				Title:             "Review dedupe-emails@2.1.0",
				Status:            status,
				CurrentApprovals:  1,
				RequiredApprovals: 2,
				RequestorID:       requestorID,
				Requestor:         &api.UserInfoTinyDictSchema{ID: requestorID, Email: "sep@corp.com", FirstName: "Sep", LastName: "Dehpour"},
				DatasetID:         testutil.IntPtr(42),
				DatasetName:       &datasetName,
				RuleRevision: &api.CapiSchemasRuleReviewSchemaRuleRevisionTinyInfoSchema{
					ID: rrDescribeRevisionUUID, Name: "dedupe-emails", Release: "2.1.0",
				},
				EffectivePolicy: &api.EffectivePolicyResponseSchema{
					RequiresReview: true, MinReviewers: 2, RequireDryRun: true,
					RequireDryRunSuccess: true, AutoEnableOnApproval: true, Source: "dataset",
				},
			})
		})
}

func TestDescribeRuleReviewRendersPayload(t *testing.T) {
	registerRuleReviewDetail(t, api.ReviewRequestStatusChangesRequested)

	cmd := setupRuleReviewDescribeCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"describe", "rule-review", rrDescribeReviewUUID.String()})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"# RULE REVIEW " + rrDescribeReviewUUID.String(),
		"Title: Review dedupe-emails@2.1.0",
		"Scope: table orders (42)",
		"Status: changes_requested | Approvals: 1/2 | Requestor: Sep Dehpour",
		"## POLICY",
		"## NEXT ACTIONS",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n%s", want, out)
		}
	}
}

func TestDescribeRuleReviewStatusAwareActions(t *testing.T) {
	registerRuleReviewDetail(t, api.ReviewRequestStatusRejected)

	cmd := setupRuleReviewDescribeCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"describe", "rule-review", rrDescribeReviewUUID.String()})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "qctl review reopen "+rrDescribeReviewUUID.String()) {
		t.Errorf("rejected review must offer reopen\n%s", buf.String())
	}
}

func TestDescribeRuleReviewRawJSONAndYAML(t *testing.T) {
	for _, format := range []string{"json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			registerRuleReviewDetail(t, api.ReviewRequestStatusOpen)

			cmd := setupRuleReviewDescribeCommand()
			var buf bytes.Buffer
			cmd.SetOut(&buf)
			cmd.SetErr(&buf)
			cmd.SetArgs([]string{"describe", "rule-review", rrDescribeReviewUUID.String(), "-o", format})

			if err := cmd.Execute(); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			out := buf.String()
			// Raw wire keys (yaml keeps them via the JSON round-trip).
			for _, want := range []string{"rule_revision_id", "required_approvals", "dataset_name"} {
				if !strings.Contains(out, want) {
					t.Errorf("[%s] raw output missing wire key %q\n%s", format, want, out)
				}
			}
			if strings.Contains(out, "# RULE REVIEW") {
				t.Errorf("[%s] raw output must not contain the markdown payload\n%s", format, out)
			}
		})
	}
}

func TestDescribeRuleReviewHelpMentionsGuide(t *testing.T) {
	cmd := setupRuleReviewDescribeCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"describe", "rule-review", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "qctl guide review-rules") {
		t.Errorf("describe rule-review --help missing guide breadcrumb\n%s", buf.String())
	}
}
