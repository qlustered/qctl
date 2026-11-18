package rule_reviews //nolint:revive // Match the established package under test.

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/commands/guide/guidetest"
	"github.com/qlustered/qctl/internal/testutil"
)

// Pins the describe rule-review payload quoted under review-rules "## 2.
// Understand a review", up to the "- approve (human):" line the topic elides
// after.
func TestGuideQuoteDescribeRuleReview(t *testing.T) {
	revisionID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	alice := api.UserInfoTinyDictSchema{
		ID: uuid.MustParse("aaaaaaaa-1111-2222-3333-444444444444"), FirstName: "Alice", LastName: "Doe",
	}
	bob := api.UserInfoTinyDictSchema{
		ID: uuid.MustParse("bbbbbbbb-1111-2222-3333-444444444444"), FirstName: "Bob", LastName: "Smith",
	}
	// Comment authors carry their org role; the requestor and reviewers do not.
	admin, editor := api.RoleEnumAdmin, api.RoleEnumEditor
	aliceAuthor, bobAuthor := alice, bob
	aliceAuthor.Role, bobAuthor.Role = &admin, &editor
	datasetName := "orders"
	jobStatus := api.IngestionJobStateFinished
	reviewers := []api.UserInfoTinyDictSchema{bob}
	decisions := []Decision{{
		ID:       uuid.MustParse("0190aaaa-0000-0000-0000-000000000001"),
		Decision: api.ReviewDecisionEnumApprove, Reviewer: &bob, ReviewerID: bob.ID,
		Comment:   testutil.StringPtr("LGTM"),
		CreatedAt: time.Now().Add(-150 * time.Minute),
	}}
	thread := []Comment{{
		ID:     uuid.MustParse("7a1b2c3d-4e5f-6071-8293-a4b5c6d7e8f9"),
		Author: &aliceAuthor, AuthorID: alice.ID,
		Body:      "Please add a null check to apply().",
		CreatedAt: time.Now().Add(-210 * time.Minute),
		Replies: &[]Comment{{
			ID:     uuid.MustParse("9c2d3e4f-5a6b-7c8d-9e0f-1a2b3c4d5e6f"),
			Author: &bobAuthor, AuthorID: bob.ID,
			Body:      "Done in the resubmission.",
			CreatedAt: time.Now().Add(-150 * time.Minute),
		}},
	}}
	out := RenderDetail(&ReviewDetail{
		ID:                uuid.MustParse("018f3c2e-4d5a-7b89-a1c2-3d4e5f607182"),
		Title:             "Review dedupe-emails@2.1.0",
		Status:            api.ReviewRequestStatusOpen,
		CurrentApprovals:  1,
		RequiredApprovals: 2,
		RequestorID:       alice.ID,
		Requestor:         &alice,
		AssignedReviewers: &reviewers,
		DatasetID:         testutil.IntPtr(42),
		DatasetName:       &datasetName,
		RuleRevisionID:    revisionID,
		RuleRevision: &api.CapiSchemasRuleReviewSchemaRuleRevisionTinyInfoSchema{
			ID: revisionID, Name: "dedupe-emails", Release: "2.1.0",
		},
		DryRunJobID:     testutil.IntPtr(512),
		DryRunJobStatus: &jobStatus,
		EffectivePolicy: &api.EffectivePolicyResponseSchema{
			RequiresReview: true, MinReviewers: 2, RequireDryRun: true,
			RequireDryRunSuccess: true, AutoEnableOnApproval: true, Source: "dataset",
		},
		Decisions: &decisions,
		Comments:  &thread,
	})

	lines := strings.Split(out, "\n")
	approve := slices.IndexFunc(lines, func(l string) bool { return strings.HasPrefix(l, "- approve (human):") })
	if approve < 0 || strings.TrimSpace(strings.Join(lines[approve+1:], "")) == "" {
		t.Fatalf("no \"- approve (human):\" line with lines after it for the topic's \"...\" to stand for\n%s", out)
	}
	guidetest.AssertQuoted(t, "review-rules", strings.Join(lines[:approve+1], "\n"))
}
