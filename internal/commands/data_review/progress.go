package data_review //nolint:revive // Package name mirrors the hyphenated command.

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/data_reviews"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/spf13/cobra"
)

type progressResponse struct {
	CountByState       *data_reviews.StateCounts        `json:"count_by_state"`
	SignoffProgress    *data_reviews.SignoffProgress    `json:"signoff_progress"`
	RequestStateCounts *data_reviews.RequestStateCounts `json:"request_state_counts,omitempty"`
}

func newProgressCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "progress",
		Short: "Show row counts, request states and reviewer sign-off progress",
		Long: `Show data-review progress for one table.

--group scopes the row counts to one submission and adds its request-state counts
for the approver (you) and sender views. Holds take precedence over active flags,
then approvals, then pending rows. Your own eligible approval puts the row in
your approved bucket; the sender's approved bucket requires the full threshold.
Sign-off progress is always table-wide because that endpoint has no
submission-group filter.`,
		Annotations: cmdutil.WithFormats(catalog.For("data-review"), "table", "json", "yaml"),
		Args:        cobra.NoArgs,
		RunE:        runProgress,
	}
	cmdutil.AddTableFlags(cmd, "Table whose data-review progress to show")
	cmdutil.RefString(cmd.Flags(), "group", "data-review-group", "Scope row counts and show personal request-state counts for this submission-group UUID (sign-off remains table-wide)")
	return cmd
}

func runProgress(cmd *cobra.Command, _ []string) error {
	groupID, err := groupFlag(cmd)
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
	tableID, err := cmdutil.ResolveTableIDFromFlags(ctx, cmd)
	if err != nil {
		return err
	}

	client := data_reviews.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	counts, err := client.CountByState(ctx.Credential.AccessToken, tableID, groupID)
	if err != nil {
		return fmt.Errorf("count-by-state endpoint failed: %w", err)
	}
	result := progressResponse{CountByState: counts}
	if groupID != "" {
		result.RequestStateCounts, err = client.RequestStateCounts(ctx.Credential.AccessToken, tableID, groupID)
		if err != nil {
			return fmt.Errorf("request-state-counts endpoint failed: %w", err)
		}
	}
	progress, err := client.SignoffProgress(ctx.Credential.AccessToken, tableID)
	if err != nil {
		return fmt.Errorf("signoff-progress endpoint failed: %w", err)
	}
	result.SignoffProgress = progress

	if format, ok := structuredOutput(cmd); ok {
		return output.WriteRaw(cmd.OutOrStdout(), format, result)
	}
	return printProgress(cmd, result, groupID)
}

// groupFlag reads an optional --group as a canonical submission-group UUID,
// "" when it was not given. progress and the bulk decisions share it.
func groupFlag(cmd *cobra.Command) (string, error) {
	groupID, _ := cmd.Flags().GetString("group")
	if !cmd.Flags().Changed("group") {
		return "", nil
	}
	if groupID == "" {
		return "", qerrors.NewBadUsage("--group cannot be empty: give a full submission-group UUID")
	}
	parsed, err := uuid.Parse(groupID)
	if err != nil {
		return "", qerrors.NewBadUsagef("invalid --group %q: expected a full UUID", groupID)
	}
	return parsed.String(), nil
}

func printProgress(cmd *cobra.Command, result progressResponse, groupID string) error {
	scope := "whole table"
	if groupID != "" {
		scope = "submission " + groupID
	}
	if _, err := fmt.Fprintf(cmd.OutOrStdout(),
		"rows: awaiting_decision %d · rejected %d · accepted %d  (scope: %s)\n",
		intValue(result.CountByState.AwaitingDecision), intValue(result.CountByState.Rejected),
		intValue(result.CountByState.Accepted), scope); err != nil {
		return err
	}

	if counts := result.RequestStateCounts; counts != nil {
		a, s := counts.Approver, counts.Sender
		if _, err := fmt.Fprintf(cmd.OutOrStdout(),
			"approver (you): to_review %d · flagged_by_me %d · approved %d · on_hold %d\n"+
				"sender: waiting %d · flagged %d · approved %d · on_hold %d\n",
			intValue(a.ToReview), intValue(a.FlaggedByMe), intValue(a.Approved), intValue(a.OnHold),
			intValue(s.Waiting), intValue(s.Flagged), intValue(s.Approved), intValue(s.OnHold)); err != nil {
			return err
		}
	}

	p := result.SignoffProgress
	if p.MyTotal == 0 {
		_, err := fmt.Fprintf(cmd.OutOrStdout(),
			"sign-off: done %d of %d (remaining %d) · you: no reviews assigned to you\n",
			p.Done, p.Total, p.Remaining)
		return err
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(),
		"sign-off: done %d of %d (remaining %d) · you: %d of %d (remaining %d)\n",
		p.Done, p.Total, p.Remaining, p.MyDone, p.MyTotal, p.MyRemaining)
	return err
}

func intValue(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}
