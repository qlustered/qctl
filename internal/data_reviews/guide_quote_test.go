package data_reviews //nolint:revive // Match the established package under test.

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/commands/guide/guidetest"
)

// Pins the describe payload quoted under review-data "### 2. Read one review":
// the passage through the approval meter, and the first NEXT ACTIONS line.
func TestGuideQuoteDescribeDataReview(t *testing.T) {
	now := time.Now()
	alice := rdUser(uuid.MustParse("0190dddd-0000-4000-8000-000000000001"), "Alice", "Chen")
	bob := rdUser(uuid.MustParse("0190dddd-0000-4000-8000-000000000002"), "Bob", "Ng")
	requester := rdUser(uuid.MustParse("0190dddd-0000-4000-8000-000000000003"), "Rita", "Req")
	changes := []Change{
		{
			FieldName: "amount", HistoryID: 811,
			OriginalValue: rdPtr("10"), EditedValue: rdPtr("100"),
			AffectedRowCount: rdPtr(1),
			Decision:         rdPtr(api.ReviewDecisionEnumRequestChanges),
			DecidedByID:      &bob.ID, DecidedBy: &bob,
			DecidedAt: rdPtr(now.Add(-150 * time.Minute)),
			Comment:   rdPtr("too large"),
		},
		{
			FieldName: "email", HistoryID: 812,
			EditedValue: rdPtr("ada@example.com"), AffectedRowCount: rdPtr(1),
		},
	}
	reviewers := []api.UserInfoTinyDictSchema{alice, bob}
	groups := []api.ReviewGroupRefSchema{{
		ID:        uuid.MustParse("11111111-2222-4333-8444-555555555555"),
		Title:     "Fix Q3 amounts",
		CreatedAt: now.Add(-60 * time.Hour),
	}}
	reason := api.CellVeto
	out := RenderDetail(&DetailView{
		Review: &Review{
			ID:    uuid.MustParse("0190bbbb-1111-4222-8333-444444444444"),
			RowID: 1001, DatasetID: 42,
			Status: api.ReviewRequestStatusChangesRequested, RequiredApprovals: 2,
			Requester: &requester, AssignedReviewers: &reviewers,
			ReleaseBlockReason: &reason,
			Changes:            &changes, ReviewGroups: &groups,
			CreatedAt: now.Add(-60 * time.Hour), UpdatedAt: now,
		},
		Decisions:        []Decision{{ReviewerID: alice.ID, Decision: api.ReviewDecisionEnumApprove, CreatedAt: now.Add(-90 * time.Minute)}},
		ApprovalProgress: &RowOverlay{CurrentApprovals: rdPtr(1), RequiredApprovals: rdPtr(2)},
	})

	// The topic cuts from the approvals tally to NEXT ACTIONS, and after the
	// first action, writing each cut as a "..." line.
	head, rest, ok := strings.Cut(out, "\n\n## COMMENTS")
	if !ok {
		t.Fatalf("no approvals tally in the payload\n%s", out)
	}
	_, actions, ok := strings.Cut(rest, "\n## NEXT ACTIONS\n")
	if !ok {
		t.Fatalf("no NEXT ACTIONS section in the payload\n%s", out)
	}
	firstAction, _, _ := strings.Cut(actions, "\n")
	if firstAction == "" {
		t.Fatalf("NEXT ACTIONS no longer opens with an action\n%s", out)
	}
	guidetest.AssertQuoted(t, "review-data", head+"\n...")
	guidetest.AssertQuoted(t, "review-data", "...\n## NEXT ACTIONS\n"+firstAction+"\n...")
}
