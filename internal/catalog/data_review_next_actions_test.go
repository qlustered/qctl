package catalog_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/data_reviews"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// TestDataReviewNextActionsResolve renders `describe data-review` in every
// status and resolves each command its NEXT ACTIONS section recommends against
// the real tree. This is what stops a renamed or removed verb or flag leaving
// describe advertising a command that fails.
func TestDataReviewNextActionsResolve(t *testing.T) {
	root := realRoot(t)
	seen := map[string]bool{}
	for _, status := range []api.ReviewRequestStatus{
		api.ReviewRequestStatusOpen,
		api.ReviewRequestStatusChangesRequested,
		api.ReviewRequestStatusApproved,
		api.ReviewRequestStatusClosed,
	} {
		payload := data_reviews.RenderDetail(nextActionsView(status))
		lines := qctlLines(payload)
		if len(lines) == 0 {
			t.Errorf("a %s review's payload names no qctl command:\n%s", status, payload)
		}
		for _, line := range lines {
			cmd, err := catalog.ResolveCommandLine(root, line)
			if err != nil {
				t.Errorf("describe data-review (%s) prints %v", status, err)
				continue
			}
			seen[cmd.CommandPath()] = true
			checkRecipeArgs(t, cmd, line)
		}
	}

	// Every branch of the renderer must have been reached, or a recipe could
	// go unchecked while this test stays green.
	for _, want := range []string{
		"qctl data-review approve",
		"qctl data-review comment",
		"qctl data-review withdraw",
		"qctl data-review request",
		"qctl comment-thread reply",
		"qctl get data-review-groups",
		"qctl get data-reviews",
	} {
		if !seen[want] {
			t.Errorf("no status rendered a %q recipe; the fixture no longer reaches that branch", want)
		}
	}
}

// qctlLines returns each payload line's text from its first "qctl " on.
func qctlLines(payload string) []string {
	var lines []string
	for _, l := range strings.Split(payload, "\n") {
		if i := strings.Index(l, "qctl "); i >= 0 {
			lines = append(lines, l[i:])
		}
	}
	return lines
}

// checkRecipeArgs goes past ResolveCommandLine, which checks neither
// positional arguments nor required flags: the recipe's positionals must
// satisfy the command's Args validator, and every required flag must be
// given. The tree is shared, so the flags are read, never parsed into it.
func checkRecipeArgs(t *testing.T, cmd *cobra.Command, line string) {
	t.Helper()
	tokens := strings.Fields(strings.TrimPrefix(line, cmd.CommandPath()))
	var positionals []string
	given := map[string]bool{}
	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		if !strings.HasPrefix(tok, "--") {
			positionals = append(positionals, tok)
			continue
		}
		name, _, hasValue := strings.Cut(strings.TrimPrefix(tok, "--"), "=")
		given[name] = true
		if f := cmd.Flags().Lookup(name); f != nil && f.NoOptDefVal == "" && !hasValue {
			i++ // the flag's value
		}
	}
	if cmd.Args != nil {
		if err := cmd.Args(cmd, positionals); err != nil {
			t.Errorf("%q: arguments %q: %v", line, positionals, err)
		}
	}
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if _, required := f.Annotations[cobra.BashCompOneRequiredFlag]; required && !given[f.Name] {
			t.Errorf("%q omits the required --%s", line, f.Name)
		}
	})
}

// nextActionsView reaches every recipe branch: the caller's own standing
// decision as a reviewer (withdraw) and a thread (reply).
func nextActionsView(status api.ReviewRequestStatus) *data_reviews.DetailView {
	now := time.Now()
	reviewID := uuid.MustParse("0190aaaa-0000-4000-8000-000000000001")
	threadID := uuid.MustParse("0190aaaa-0000-4000-8000-000000000003")
	requesterID := uuid.MustParse("0190bbbb-0000-4000-8000-000000000001")
	me := uuid.MustParse("0190bbbb-0000-4000-8000-000000000002")
	requester := api.UserInfoTinyDictSchema{ID: requesterID, FirstName: "Rita", LastName: "Req", Email: "rita@corp.com"}
	changes := []data_reviews.Change{{FieldName: "amount", HistoryID: 811}}
	return &data_reviews.DetailView{
		Review: &data_reviews.Review{
			ID: reviewID, RowID: 7, DatasetID: 42, Status: status, RequiredApprovals: 2,
			Requester: &requester, Changes: &changes, ThreadID: &threadID,
			CreatedAt: now, UpdatedAt: now,
		},
		Decisions: []data_reviews.Decision{{ReviewerID: me, Decision: api.ReviewDecisionEnumApprove, CreatedAt: now}},
		Me:        &me,
	}
}
