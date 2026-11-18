package data_review //nolint:revive // Package name mirrors the hyphenated command.

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/data_reviews"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// maxSubmitRowIDs is the backend's bound on one submission
// (SubmitBatchSchema.row_ids, max_length=500).
const maxSubmitRowIDs = 500

func newRequestCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "request",
		Short: "Submit quarantined rows for data review",
		Long: `Submit one or more quarantined rows of a table for data review, as one
submission (a data-review group).

Name the rows with --row-id, repeated or comma-separated: 1 to 500 distinct
positive ids. A repeated id is refused before anything is sent. --title names
the submission (surrounding spaces are trimmed); --description explains it.
--reviewer asks specific people (email, "First Last" or UUID, repeatable);
every name is resolved before anything is submitted.

If the server drops a reviewer who is not an active organization member,
qctl names them on stderr for each affected review and still succeeds.

A row whose review is not closed (open, changes_requested or approved) keeps
that review: the backend adds it to the new submission instead of opening a
second one, recomputes its status and takes the required approvals from the
table's current policy. Such rows are marked (re-submitted). A row whose
review was closed gets a new review.

The first line names the new submission; each row then gets one line with its
review id, which is what every other data-review verb takes. -o json|yaml
prints {group, reviews} instead and moves those lines to stderr; -o name
prints data-review/<uuid> for each row.

Uses the data-review batch submission endpoint, which requires ` + api.OperationPermissions["submit_data_review_batch"] + `
and access to the table or assigned sources. External callers use --table-id
and full reviewer UUIDs; names and emails require the org user directory.

Examples:
  qctl data-review request --table orders --row-id 1001,1002 --title "Fix Q3 amounts"
  qctl data-review request --table-id 42 --row-id 1001 --title "Email typo" --description "Customer called in" --reviewer ada@example.com`,
		Annotations: cmdutil.WithFormats(catalog.For("data-review"), "table", "json", "yaml", "name"),
		Args:        cobra.NoArgs,
		RunE:        runRequest,
	}
	cmdutil.AddTableFlags(cmd, "Table whose rows to submit")
	cmd.Flags().IntSlice("row-id", nil, "Row to submit, by id (repeatable or comma-separated; 1-500 distinct ids)")
	cmd.Flags().String("title", "", "Submission title (required)")
	cmd.Flags().String("description", "", "Submission description")
	cmd.Flags().StringArray("reviewer", nil, "Reviewer to ask: email, \"First Last\", or UUID (repeatable)")
	_ = cmd.MarkFlagRequired("row-id")
	_ = cmd.MarkFlagRequired("title")
	return cmd
}

func runRequest(cmd *cobra.Command, _ []string) error {
	if err := cmdutil.ValidateTableSelectorFromFlags(cmd); err != nil {
		return err
	}
	rowIDs, err := submitRowIDs(cmd)
	if err != nil {
		return err
	}
	rawTitle, _ := cmd.Flags().GetString("title")
	title := strings.TrimSpace(rawTitle)
	if title == "" {
		return qerrors.NewBadUsage("--title cannot be blank: name the submission")
	}
	description, _ := cmd.Flags().GetString("description")
	reviewers, _ := cmd.Flags().GetStringArray("reviewer")

	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	tableID, err := cmdutil.ResolveTableIDFromFlags(ctx, cmd)
	if err != nil {
		return err
	}
	client := data_reviews.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	token := ctx.Credential.AccessToken

	reviewerIDs, dir, err := cmdutil.ResolveUsers(client.FetchUsers, token, reviewers)
	if err != nil {
		return err
	}

	reviews, err := client.Submit(token, data_reviews.SubmitOptions{
		DatasetID:   tableID,
		RowIDs:      rowIDs,
		Title:       title,
		Description: description,
		ReviewerIDs: reviewerIDs,
	})
	if err != nil {
		var apiErr *data_reviews.APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusForbidden {
			return fmt.Errorf("%w\n(requesting review also needs access to the table or assigned sources)",
				apierror.WithPermissionNote(err, apiErr.StatusCode, "submit_data_review_batch"))
		}
		return err
	}
	out := cmdutil.NewResultWriter(cmd)
	group := submissionGroup(reviews)
	printSubmission(out.Human, cmd.ErrOrStderr(), reviews, group, title, tableFlagRecipe(cmd, tableID))
	names := make([]string, 0, len(reviews))
	for i := range reviews {
		reportUnassignedReviewers(cmd.ErrOrStderr(), reviewerIDs, &reviews[i], dir)
		names = append(names, "data-review/"+reviews[i].ID.String())
	}
	if reviews == nil {
		reviews = []data_reviews.Review{}
	}
	return out.Write(submission{Group: group, Reviews: reviews}, names...)
}

// submission is a request's -o json|yaml document: the new submission group,
// null when the response names none, and every row's review.
type submission struct {
	Group   *api.ReviewGroupRefSchema `json:"group"`
	Reviews []data_reviews.Review     `json:"reviews"`
}

// submitRowIDs enforces the backend's row_ids contract before any request:
// 1..500 positive ids. A duplicate is refused rather than dropped, because a
// submission is exact and a repeated id is almost always a typo for another.
func submitRowIDs(cmd *cobra.Command) ([]int, error) {
	ids, _ := cmd.Flags().GetIntSlice("row-id")
	if len(ids) == 0 {
		return nil, qerrors.NewBadUsage("--row-id names no rows: pass at least one row id")
	}
	seen := make(map[int]bool, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return nil, qerrors.NewBadUsagef("--row-id must be a positive integer, got %d", id)
		}
		if seen[id] {
			return nil, qerrors.NewBadUsagef("--row-id %d is given more than once", id)
		}
		seen[id] = true
	}
	if len(ids) > maxSubmitRowIDs {
		return nil, qerrors.NewBadUsagef(
			"%d row ids given; one submission takes at most %d, so split the list", len(ids), maxSubmitRowIDs)
	}
	return ids, nil
}

// resolveReviewers turns --reviewer values into user UUIDs. A value that
// names nobody, or several people, exits 2. Only an email or a name needs the
// directory, so naming every reviewer by UUID reads none — which is the only
// way the editor role, who may request a review but not list users, can name
// one at all.
func resolveReviewers(client *data_reviews.Client, token string, reviewers []string) ([]openapi_types.UUID, error) {
	ids, _, err := cmdutil.ResolveUsers(client.FetchUsers, token, reviewers)
	return ids, err
}

// tableFlagRecipe repeats the selector the user gave, so the Next line works
// as typed: a --table name stays a name.
func tableFlagRecipe(cmd *cobra.Command, tableID int) string {
	if cmd.Flags().Changed("table") {
		name, _ := cmd.Flags().GetString("table")
		return "--table " + cmdutil.ShellArg(terminal.Sanitize(name))
	}
	return "--table-id " + strconv.Itoa(tableID)
}

func printSubmission(out, errOut io.Writer, reviews []data_reviews.Review, group *api.ReviewGroupRefSchema, title, tableFlag string) {
	if group == nil {
		// The group id rides only inside each review's review_groups, so a
		// response without one still created a submission; it cannot be named.
		fmt.Fprintf(errOut, "warning: the response names no submission group; "+
			"list them with qctl get data-review-groups %s\n", tableFlag)
		fmt.Fprintf(out, "data-review-group created %q: %d review(s)\n", terminal.Sanitize(title), len(reviews))
	} else {
		fmt.Fprintf(out, "data-review-group/%s created %q: %d review(s)\n",
			group.ID, terminal.Sanitize(group.Title), len(reviews))
	}
	for i := range reviews {
		printSubmittedReview(out, &reviews[i], group)
	}
	if group != nil {
		fmt.Fprintf(out, "Next: qctl get data-reviews %s --group %s\n", tableFlag, group.ID)
	}
}

func printSubmittedReview(out io.Writer, r *data_reviews.Review, group *api.ReviewGroupRefSchema) {
	suffix := ""
	if group != nil && r.CreatedAt.Before(group.CreatedAt) {
		// The backend reuses a row's active review rather than opening a
		// second one; a review older than the submission is one of those.
		suffix = " (re-submitted)"
	}
	fmt.Fprintf(out, "data-review/%s row %d (status: %s, approvals required: %d)%s\n",
		r.ID, r.RowID, terminal.Sanitize(string(r.Status)), r.RequiredApprovals, suffix)
}

// submissionGroup is the newest review_groups entry of the first review: a
// reused review also lists every earlier submission it belonged to.
func submissionGroup(reviews []data_reviews.Review) *api.ReviewGroupRefSchema {
	if len(reviews) == 0 || reviews[0].ReviewGroups == nil {
		return nil
	}
	var newest *api.ReviewGroupRefSchema
	groups := *reviews[0].ReviewGroups
	for i := range groups {
		if newest == nil || groups[i].CreatedAt.After(newest.CreatedAt) {
			newest = &groups[i]
		}
	}
	return newest
}
