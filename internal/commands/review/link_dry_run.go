package review

import (
	stderrors "errors"
	"fmt"
	"strconv"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/dry_runs"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/rule_reviews"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

func newLinkDryRunCommand() *cobra.Command {
	var comment string

	cmd := &cobra.Command{
		Use:   "link-dry-run <review-id> <dry-run-job-id>",
		Short: "Link a dry-run job to a review (or one of its comments)",
		Long: `Link a dry-run job to a rule review.

Without --comment the job becomes the review's headline dry run — this is the
one the enable gate checks (require_dry_run / require_dry_run_success) and it
overwrites any previously linked job. With --comment the job is attached to a
single comment for display only, with no policy significance.

The job id is pre-validated against this org before linking (a bogus id would
otherwise fail deep in the backend). If the effective policy requires a passing
dry run and the job is not finished, a warning is printed but the link still
proceeds — a human/agent renders the final verdict.

Examples:
  qctl review link-dry-run 018f3c2e-4d5a-7b89-a1c2-3d4e5f607182 512
  qctl review link-dry-run 018f3c2e-4d5a-7b89-a1c2-3d4e5f607182 512 --comment 7a1b...`,
		Annotations: cmdutil.WithArgKinds(cmdutil.WithFormats(catalog.For("rule-review"), "table"), "rule-review", "dry-run-job"),
		Args:        cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			reviewID := args[0]
			jobID, err := parseLinkDryRunArgs(args, comment)
			if err != nil {
				return err
			}

			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}

			// Pre-validate the job: the backend link endpoints don't verify the
			// job exists or belongs to the org, so a bogus id would die on a DB
			// foreign key. Turn a missing job into a clean not-found here.
			drClient := dry_runs.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			job, err := drClient.GetDryRunJob(ctx.Credential.AccessToken, jobID)
			if err != nil {
				return mapDryRunJobLookupError(err, jobID)
			}

			reviewClient := rule_reviews.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			detail, err := reviewClient.Get(ctx.Credential.AccessToken, reviewID)
			if err != nil {
				return fmt.Errorf("failed to get rule review: %w", err)
			}

			// The gate wants a *passing* run: warn when this job is not finished.
			if detail.EffectivePolicy != nil && detail.EffectivePolicy.RequireDryRunSuccess &&
				job.State != api.IngestionJobStateFinished {
				fmt.Fprintf(cmd.ErrOrStderr(),
					"warning: job %d state is %s; the enable gate requires state=finished with no rule load errors\n",
					jobID, terminal.Sanitize(string(job.State)))
			}

			if comment != "" {
				if err := reviewClient.LinkDryRunToComment(ctx.Credential.AccessToken, reviewID, comment, jobID); err != nil {
					return fmt.Errorf("failed to link dry run to comment: %w", err)
				}
				fmt.Fprintf(cmd.OutOrStdout(),
					"rule-review/%s dry run linked to comment %s: job %d\n", detail.ID, comment, jobID)
				return nil
			}

			prior := detail.DryRunJobID
			linked, err := reviewClient.LinkDryRun(ctx.Credential.AccessToken, reviewID, jobID)
			if err != nil {
				return fmt.Errorf("failed to link dry run: %w", err)
			}
			if prior != nil && *prior != jobID {
				fmt.Fprintf(cmd.OutOrStdout(),
					"rule-review/%s dry run linked: job %d (replaced job %d)\n", linked.ID, jobID, *prior)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(),
					"rule-review/%s dry run linked: job %d\n", linked.ID, jobID)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Next: qctl describe rule-review %s\n", linked.ID)
			return nil
		},
	}

	cmd.Flags().StringVar(&comment, "comment", "",
		"Link to this comment (full UUID) instead of the review headline (display-only)")
	return cmd
}

// mapDryRunJobLookupError turns a 404 from the job pre-validation into a clear
// not-found (exit 3) that names the job; other failures pass through.
func mapDryRunJobLookupError(err error, jobID int) error {
	var exitErr *qerrors.Error
	if stderrors.As(err, &exitErr) && exitErr.ExitCode() == qerrors.ExitNotFound {
		return qerrors.NewNotFound(apierror.WithScopeNote(
			fmt.Sprintf("dry-run job %d not found in this org", jobID)))
	}
	return fmt.Errorf("failed to validate dry-run job %d: %w", jobID, err)
}

// parseLinkDryRunArgs checks the review id and --comment are UUIDs and
// returns the dry-run job id.
func parseLinkDryRunArgs(args []string, comment string) (int, error) {
	if err := cmdutil.ValidateUUIDArgs(args[:1], "review id"); err != nil {
		return 0, err
	}
	jobID, err := strconv.Atoi(args[1])
	if err != nil {
		return 0, qerrors.NewBadUsagef("invalid dry-run job id %q: expected an integer", args[1])
	}
	if comment != "" {
		if err := cmdutil.ValidateUUIDArgs([]string{comment}, "comment id"); err != nil {
			return 0, err
		}
	}
	return jobID, nil
}
