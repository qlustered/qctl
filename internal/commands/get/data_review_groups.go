package get

import (
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/comments"
	"github.com/qlustered/qctl/internal/data_reviews"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/pkg/tableui"
	"github.com/qlustered/qctl/internal/pkg/timeutil"
	"github.com/spf13/cobra"
)

const dataReviewGroupColumns = "id,title,description,requestor,reviewers,rows,open,undecided,approved,rejected,created"

type dataReviewGroupRow struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Requestor   string `json:"requestor"`
	Reviewers   string `json:"reviewers"`
	Rows        int    `json:"rows"`
	Open        int    `json:"open"`
	Undecided   int    `json:"undecided"`
	Approved    int    `json:"approved"`
	Rejected    int    `json:"rejected"`
	Created     string `json:"created"`
}

// NewDataReviewGroupsCommand creates the get data-review-groups command.
func NewDataReviewGroupsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:         "data-review-groups",
		Short:       "List data-review submission groups for a table",
		Annotations: catalog.For("data-review-group"),
		Args:        cobra.NoArgs,
		RunE:        runDataReviewGroups,
	}
	cmdutil.AddTableFlags(cmd, "Table whose data-review submissions to list")
	cmd.Flags().Bool("include-closed", false, "Include groups with no open or changes_requested review, such as finished or cancelled submissions")
	cmd.Flags().Int("limit", 50, "Maximum groups to return (1-200)")
	cmd.Flags().Int("offset", 0, "Number of groups to skip (0 or greater)")
	cmdutil.DeclareColumns(cmd, []dataReviewGroupRow(nil))
	return cmd
}

func runDataReviewGroups(cmd *cobra.Command, _ []string) error {
	opts, err := dataReviewGroupFlags(cmd)
	if err != nil {
		return err
	}
	if err := cmdutil.ValidateTableSelectorFromFlags(cmd); err != nil {
		return err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	opts.DatasetID, err = cmdutil.ResolveTableIDFromFlags(ctx, cmd)
	if err != nil {
		return err
	}

	client := data_reviews.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	page, err := client.ListGroups(ctx.Credential.AccessToken, opts)
	if err != nil {
		return fmt.Errorf("failed to list data-review groups: %w", err)
	}
	if cmd.Flags().Changed("output") {
		format, _ := cmd.Flags().GetString("output")
		switch format {
		case "json", "yaml": //nolint:goconst // Keep output format handling local to this command.
			return output.WriteRaw(cmd.OutOrStdout(), format, page)
		case "name": //nolint:goconst // Keep output format handling local to this command.
			return printDataReviewGroupNames(cmd, page.Groups)
		}
	}
	if len(page.Groups) == 0 {
		skip, err := printEmptyResult(cmd, ctx, "data-review groups", []dataReviewGroupRow{}, &page.Total)
		if err != nil || skip {
			return err
		}
	}

	directory := cmdutil.FetchDisplayDirectory(cmd, client.FetchUsers, ctx.Credential.AccessToken)
	rows := dataReviewGroupRows(page.Groups, directory)
	printContextBanner(cmd, ctx)
	if err := tableui.PrintFromCmd(cmd, rows, dataReviewGroupColumns); err != nil {
		return err
	}
	if page.Total > opts.Offset+len(page.Groups) {
		fmt.Fprintf(cmd.ErrOrStderr(), "More data-review groups — continue with --offset %d.\n",
			opts.Offset+len(page.Groups))
	}
	return nil
}

func dataReviewGroupFlags(cmd *cobra.Command) (data_reviews.GroupsOptions, error) {
	limit, err := cmdutil.PositiveCountFlag(cmd, "limit")
	if err != nil {
		return data_reviews.GroupsOptions{}, err
	}
	if limit > 200 {
		return data_reviews.GroupsOptions{}, qerrors.NewBadUsagef("--limit must be between 1 and 200 (got %d)", limit)
	}
	offset, err := cmdutil.NonNegativeCountFlag(cmd, "offset")
	if err != nil {
		return data_reviews.GroupsOptions{}, err
	}
	includeClosed, _ := cmd.Flags().GetBool("include-closed")
	return data_reviews.GroupsOptions{IncludeClosed: includeClosed, Limit: limit, Offset: offset}, nil
}

func dataReviewGroupRows(groups []data_reviews.Group, directory *data_reviews.Directory) []dataReviewGroupRow {
	rows := make([]dataReviewGroupRow, 0, len(groups))
	for _, group := range groups {
		var description string
		if group.Description != nil {
			description = *group.Description
		}
		var reviewers []string
		if group.AssignedReviewers != nil {
			for _, user := range *group.AssignedReviewers {
				reviewers = append(reviewers, comments.UserDisplay(&user, user.ID))
			}
		}
		rows = append(rows, dataReviewGroupRow{
			ID:          group.ReviewGroupID.String(),
			Title:       group.Title,
			Description: description,
			Requestor:   directory.DisplayName(group.RequestorID),
			Reviewers:   strings.Join(reviewers, ", "),
			Rows:        group.RowCount,
			Open:        group.OpenRowCount,
			Undecided:   group.UndecidedCount,
			Approved:    group.ApprovedCount,
			Rejected:    group.RejectedCount,
			Created:     timeutil.FormatRelative(group.CreatedAt),
		})
	}
	return rows
}

func printDataReviewGroupNames(cmd *cobra.Command, groups []data_reviews.Group) error {
	for _, group := range groups {
		if _, err := fmt.Fprintf(cmd.OutOrStdout(), "data-review-group/%s\n", group.ReviewGroupID); err != nil {
			return err
		}
	}
	return nil
}
