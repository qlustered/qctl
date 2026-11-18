package get

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/pkg/tableui"
	"github.com/qlustered/qctl/internal/pkg/timeutil"
	"github.com/qlustered/qctl/internal/rule_reviews"
	"github.com/qlustered/qctl/internal/rule_versions"
	"github.com/spf13/cobra"
)

const ruleReviewColumns = "id,title,rule,release,table,status,approvals,dry_run,updated"

// ruleReviewRow is the table-display projection of a review.
type ruleReviewRow struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Rule      string `json:"rule"`
	Release   string `json:"release"`
	Table     string `json:"table"`
	Status    string `json:"status"`
	Approvals string `json:"approvals"`
	DryRun    string `json:"dry_run"`
	Updated   string `json:"updated"`
}

// NewRuleReviewsCommand creates the get rule-reviews command.
func NewRuleReviewsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rule-reviews",
		Short: "List rule reviews (your inbox, or one rule's reviews with --rule)",
		Long: `List rule reviews.

Without --rule this is your reviews inbox (reviews where you are the author,
a reviewer, or mentioned; --all lists everyone's — admin/ops only). The inbox
requires the REVIEW_EDIT permission.

With --rule it lists all reviews of one rule revision (any role with
REVIEW_LIST), optionally filtered by --table/--table-id and --status.

Use -o json|yaml for the raw response.

New to rule reviews? Run:
  qctl guide review-rules`,
		Annotations: catalog.For("rule-review"),
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			rule, _ := cmd.Flags().GetString("rule")
			if rule == "" {
				// Rule-only flags are meaningless in inbox mode — reject loudly.
				// Silently accepting them would drop the filter the user typed
				// and answer with the whole (unfiltered) inbox at exit 0; see
				// the identical rule stated for ID filters in
				// cmdutil/flags.go's PositiveIDFlag doc comment.
				for _, ruleOnly := range []string{"table", "table-id", "release"} {
					if cmd.Flags().Changed(ruleOnly) {
						return qerrors.NewBadUsagef("--%s only applies with --rule", ruleOnly)
					}
				}
				page, limit, err := paginationFlags(cmd)
				if err != nil {
					return err
				}
				ctx, err := cmdutil.Bootstrap(cmd)
				if err != nil {
					return err
				}
				return runReviewsInbox(cmd, ctx, page, limit)
			}
			// Inbox-only flags are meaningless per rule — reject loudly.
			for _, inboxOnly := range []string{"all", "reviewer-type", "page", "limit"} {
				if cmd.Flags().Changed(inboxOnly) {
					return qerrors.NewBadUsagef("--%s only applies to inbox mode (drop --rule to use it)", inboxOnly)
				}
			}
			if err := cmdutil.ValidateOptionalTableSelector(cmd, ""); err != nil {
				return err
			}
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}
			return runReviewsForRule(cmd, ctx, rule)
		},
	}

	cmdutil.RefString(cmd.Flags(), "rule", "rule", "List reviews for this rule (name, short id, or UUID)")
	cmd.Flags().String("release", "", "Disambiguate when the rule has multiple releases (with --rule)")
	cmdutil.AddTableFlags(cmd, "With --rule: filter by table")
	cmd.Flags().String("status", "", "Filter by status (open, changes_requested, approved, rejected, closed)")
	cmd.Flags().Bool("all", false, "Inbox: list all reviews in the org, not just yours (admin/ops only)")
	cmd.Flags().String("reviewer-type", "", "Inbox: filter by your relation (author, reviewer, mentioned)")
	cmd.Flags().Int("page", 1, "Inbox: page number")
	cmd.Flags().Int("limit", 20, "Inbox: reviews per page (max 100)")
	cmdutil.DeclareColumns(cmd, []ruleReviewRow(nil))
	return cmd
}

func runReviewsInbox(cmd *cobra.Command, ctx *cmdutil.CommandContext, page, limit int) error {
	status, _ := cmd.Flags().GetString("status")
	all, _ := cmd.Flags().GetBool("all")
	reviewerType, _ := cmd.Flags().GetString("reviewer-type")

	client := rule_reviews.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	inbox, err := client.Inbox(ctx.Credential.AccessToken, rule_reviews.InboxOptions{
		Page: page, Limit: limit, Status: status, All: all, ReviewerType: reviewerType,
	})
	if err != nil {
		var apiErr *rule_reviews.APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusForbidden {
			return fmt.Errorf("the reviews inbox requires the REVIEW_EDIT permission; "+
				"to list one rule's reviews use --rule <name> (needs only REVIEW_LIST): %w", err)
		}
		return fmt.Errorf("failed to list rule reviews: %w", err)
	}

	outputFormat, _ := cmd.Flags().GetString("output")
	if cmd.Flags().Changed("output") {
		switch outputFormat {
		case "json", "yaml": //nolint:goconst // Keep output format handling local to this command.
			return output.WriteRaw(cmd.OutOrStdout(), outputFormat, inbox)
		}
	}

	if len(inbox.Reviews) == 0 {
		// list_reviews_inbox_async counts before slicing to the page (atlas
		// cettings/cettings/controllers/rule_review_controller.py, a plain count(*) over
		// base_filters with no offset/limit, versus the paginated select) -- the
		// same semantics as the shared pagination helper, so inbox.Total is a real
		// cross-page total.
		skip, err := printEmptyResult(cmd, ctx, "rule reviews", []ruleReviewRow{}, &inbox.Total)
		if err != nil {
			return err
		}
		if skip {
			return nil
		}
	}
	printContextBanner(cmd, ctx)

	rows := make([]ruleReviewRow, 0, len(inbox.Reviews))
	for _, item := range inbox.Reviews {
		rows = append(rows, ruleReviewRow{
			ID:        item.ID.String(),
			Title:     item.Title,
			Rule:      item.RuleRevisionName,
			Release:   item.RuleRevisionRelease,
			Table:     tableLabel(item.DatasetID, item.DatasetName),
			Status:    string(item.Status),
			Approvals: fmt.Sprintf("%d/%d", item.CurrentApprovals, item.RequiredApprovals),
			DryRun:    boolYesNo(item.HasDryRun),
			Updated:   timeutil.FormatRelative(item.UpdatedAt),
		})
	}
	if err := tableui.PrintFromCmd(cmd, rows, ruleReviewColumns); err != nil {
		return err
	}
	if inbox.Total > inbox.Page*inbox.Limit {
		fmt.Fprintf(cmd.ErrOrStderr(),
			"More reviews — fetch the next page with --page %d.\n", inbox.Page+1)
	}
	return nil
}

func runReviewsForRule(cmd *cobra.Command, ctx *cmdutil.CommandContext, rule string) error {
	release, _ := cmd.Flags().GetString("release")
	status, _ := cmd.Flags().GetString("status")

	rvClient, err := rule_versions.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}
	resolved, err := rvClient.ResolveRuleFull(ctx.Credential.AccessToken, rule, release)
	if err != nil {
		return err
	}

	// nil means "no table scope filter", which is why the "was it set" return
	// is what decides here rather than the ID's value.
	var datasetID *int
	if id, ok, err := cmdutil.OptionalTableID(ctx, cmd, ""); err != nil {
		return err
	} else if ok {
		datasetID = &id
	}

	client := rule_reviews.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	reviews, err := client.ListForRevision(ctx.Credential.AccessToken, resolved.ID, datasetID, status)
	if err != nil {
		return fmt.Errorf("failed to list rule reviews: %w", err)
	}

	outputFormat, _ := cmd.Flags().GetString("output")
	if cmd.Flags().Changed("output") {
		// Preserve the wire shape {"reviews": [...]}.
		raw := api.ReviewListResponseSchema{Reviews: reviews}
		switch outputFormat {
		case "json", "yaml": //nolint:goconst // Keep output format handling local to this command.
			return output.WriteRaw(cmd.OutOrStdout(), outputFormat, raw)
		}
	}

	if len(reviews) == 0 {
		// ListForRevision returns a plain slice with no total, and this
		// command has no --page/--limit in --rule mode anyway -- nil.
		skip, err := printEmptyResult(cmd, ctx, "rule reviews", []ruleReviewRow{}, nil)
		if err != nil {
			return err
		}
		if skip {
			return nil
		}
	}
	printContextBanner(cmd, ctx)

	rows := make([]ruleReviewRow, 0, len(reviews))
	for _, r := range reviews {
		name, rel := resolved.Name, resolved.Release
		if r.RuleRevision != nil {
			name, rel = r.RuleRevision.Name, r.RuleRevision.Release
		}
		rows = append(rows, ruleReviewRow{
			ID:        r.ID.String(),
			Title:     r.Title,
			Rule:      name,
			Release:   rel,
			Table:     tableLabel(r.DatasetID, nil),
			Status:    string(r.Status),
			Approvals: fmt.Sprintf("%d/%d", r.CurrentApprovals, r.RequiredApprovals),
			DryRun:    boolYesNo(r.DryRunJobID != nil),
			Updated:   timeutil.FormatRelative(r.UpdatedAt),
		})
	}
	return tableui.PrintFromCmd(cmd, rows, ruleReviewColumns)
}

// tableLabel renders the TABLE cell: "name (id)", "id", or "-" (org-wide).
func tableLabel(datasetID *int, datasetName *string) string {
	if datasetID == nil {
		return "-"
	}
	if datasetName != nil && *datasetName != "" {
		return fmt.Sprintf("%s (%d)", *datasetName, *datasetID)
	}
	return strconv.Itoa(*datasetID)
}
