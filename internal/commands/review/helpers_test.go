package review

import (
	"testing"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const testOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

var (
	testReviewUUID   = uuid.MustParse("018f3c2e-4d5a-7b89-a1c2-3d4e5f607182")
	testRevisionUUID = uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	testReviewerUUID = uuid.MustParse("bbbbbbbb-1111-2222-3333-444444444444")
)

func setupReviewTestCommand() *cobra.Command {
	rootCmd := &cobra.Command{Use: "qctl"}
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format")
	rootCmd.PersistentFlags().Bool("no-headers", false, "Don't print column headers")
	rootCmd.PersistentFlags().String("columns", "", "Columns to display")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "Maximum width for table columns")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "Show secret fields")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "Allow http://")
	rootCmd.PersistentFlags().CountP("verbose", "v", "Verbosity level") // MUST be CountP
	// Mirrors root.go: an -o value a verb does not declare exits 2.
	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		return cmdutil.ValidateOutputFormat(cmd)
	}
	rootCmd.AddCommand(NewCommand())
	return rootCmd
}

// newReviewTestEnv wires temp HOME + mock transport + config/credential.
func newReviewTestEnv(t *testing.T) *testutil.MockAPIServer {
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

func sampleReview(status api.ReviewRequestStatus) api.ReviewResponseSchema {
	return api.ReviewResponseSchema{
		ID:                  testReviewUUID,
		RuleRevisionID:      testRevisionUUID,
		Title:               "Review dedupe-emails@2.1.0",
		Status:              status,
		CurrentApprovals:    0,
		RequiredApprovals:   2,
		AssignedReviewerIds: []openapi_types.UUID{},
		CreatedAt:           time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC),
		UpdatedAt:           time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC),
		RuleRevision: &api.CapiSchemasRuleReviewSchemaRuleRevisionTinyInfoSchema{
			ID: testRevisionUUID, Name: "dedupe-emails", Release: "2.1.0",
		},
	}
}

func sampleReviewDetail(status api.ReviewRequestStatus) api.ReviewDetailResponseSchema {
	return api.ReviewDetailResponseSchema{
		ID:                  testReviewUUID,
		RuleRevisionID:      testRevisionUUID,
		Title:               "Review dedupe-emails@2.1.0",
		Status:              status,
		CurrentApprovals:    0,
		RequiredApprovals:   2,
		AssignedReviewerIds: []openapi_types.UUID{},
		CreatedAt:           time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC),
		UpdatedAt:           time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC),
		RuleRevision: &api.CapiSchemasRuleReviewSchemaRuleRevisionTinyInfoSchema{
			ID: testRevisionUUID, Name: "dedupe-emails", Release: "2.1.0",
		},
	}
}

// sampleUsersList is the /users mock payload for reviewer resolution.
func sampleUsersList() api.UsersListSchema {
	return api.UsersListSchema{
		Results: []api.UserListItemSchema{
			{ID: testReviewerUUID, Email: "alice@corp.com", FirstName: "Alice", LastName: "Chen"},
		},
	}
}
