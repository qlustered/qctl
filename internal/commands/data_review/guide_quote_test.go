package data_review //nolint:revive // Package name mirrors the hyphenated command.

import (
	"bytes"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/commands/guide/guidetest"
	"github.com/qlustered/qctl/internal/data_reviews"
	"github.com/spf13/cobra"
)

const (
	quoteGroupID   = "11111111-2222-4333-8444-555555555555"
	quoteReview1   = "0190bbbb-1111-4222-8333-444444444444"
	quoteReview2   = "0190cccc-1111-4222-8333-555555555555"
	quoteGuideName = "review-data"
)

// quoteCommand returns a command whose stdout and stderr land in the buffers.
func quoteCommand() (cmd *cobra.Command, stdout, stderr *bytes.Buffer) {
	cmd = &cobra.Command{}
	stdout, stderr = &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	return cmd, stdout, stderr
}

// Pins the output quoted under review-data "### 2. Submit them".
func TestGuideQuoteSubmission(t *testing.T) {
	submitted := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	group := api.ReviewGroupRefSchema{ID: uuid.MustParse(quoteGroupID), Title: "Fix Q3 amounts", CreatedAt: submitted}
	groups := []api.ReviewGroupRefSchema{group}
	reviews := []data_reviews.Review{{
		ID: uuid.MustParse(quoteReview1), RowID: 1001, Status: api.ReviewRequestStatusOpen,
		RequiredApprovals: 2, CreatedAt: submitted, ReviewGroups: &groups,
	}, {
		ID: uuid.MustParse(quoteReview2), RowID: 1002, Status: api.ReviewRequestStatusOpen,
		RequiredApprovals: 2, CreatedAt: submitted.Add(-24 * time.Hour), ReviewGroups: &groups,
	}}

	cmd, stdout, stderr := quoteCommand()
	cmdutil.AddTableFlags(cmd, "Table whose rows to submit")
	if err := cmd.Flags().Set("table", "orders"); err != nil {
		t.Fatal(err)
	}
	printSubmission(cmd.OutOrStdout(), cmd.ErrOrStderr(), reviews, submissionGroup(reviews), "Fix Q3 amounts", tableFlagRecipe(cmd, 42))
	if stderr.Len() > 0 {
		t.Errorf("unexpected stderr: %q", stderr)
	}
	guidetest.AssertQuoted(t, quoteGuideName, stdout.String())
}

// Pins the output quoted under review-data "### 3. Watch it".
func TestGuideQuoteProgress(t *testing.T) {
	awaiting, rejected, accepted := 7, 2, 11
	cmd, stdout, _ := quoteCommand()
	err := printProgress(cmd, progressResponse{
		CountByState:    &data_reviews.StateCounts{AwaitingDecision: &awaiting, Rejected: &rejected, Accepted: &accepted},
		SignoffProgress: &data_reviews.SignoffProgress{Done: 14, Total: 20, Remaining: 6},
	}, quoteGroupID)
	if err != nil {
		t.Fatal(err)
	}
	guidetest.AssertQuoted(t, quoteGuideName, stdout.String())
}

// Pins the output quoted under review-data "### 4. Decide whole rows": one
// review approved by id.
func TestGuideQuoteApproveOneReview(t *testing.T) {
	cmd, stdout, stderr := quoteCommand()
	printBulkResult(cmd, approveDecision,
		data_reviews.BatchSelector{ReviewIDs: []string{quoteReview1}},
		&data_reviews.BatchResult{Processed: []api.BatchDecisionItemSchema{{
			DataReviewID: uuid.MustParse(quoteReview1), Status: api.ReviewRequestStatusApproved,
			CurrentApprovals: 2, RequiredApprovals: 2, ReleasedRowIds: []int{1001},
		}}})
	if stderr.Len() > 0 {
		t.Errorf("unexpected stderr: %q", stderr)
	}
	guidetest.AssertQuoted(t, quoteGuideName, stdout.String())
}
