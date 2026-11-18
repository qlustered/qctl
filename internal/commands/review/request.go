package review

import (
	"errors"
	"fmt"
	"io"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/dry_runs"
	"github.com/qlustered/qctl/internal/rule_reviews"
	"github.com/qlustered/qctl/internal/rule_versions"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// requestOptions holds the flags bound to fields. The table selector is not
// among them: cmdutil.AddTableFlags registers --table/--table-id unbound, and
// cmdutil.OptionalTableID reads them straight off the command.
type requestOptions struct {
	release     string
	title       string
	description string
	reviewers   []string
	dryRunJob   int
}

func newRequestCommand() *cobra.Command {
	opts := requestOptions{}

	cmd := &cobra.Command{
		Use:   "request <rule>",
		Short: "Request a review for a rule revision",
		Long: `Request a review for a submitted rule revision.

Creating a review transitions the revision from draft to in_review. At most
one active (open/changes_requested) review may exist per (revision, table)
scope — a conflict reports the existing review's id.

-o json|yaml prints the new review (after --dry-run-job links its job) and
moves the result lines to stderr; -o name prints rule-review/<uuid>.

The rule may be a name, short id, or full UUID; use --release when the rule
has multiple releases. Scope the review with --table (a table name) or
--table-id (a table ID); omit both for an org-wide review.

Examples:
  qctl review request dedupe-emails --release 2.1.0
  qctl review request dedupe-emails --table orders --reviewer alice@corp.com
  qctl review request dedupe-emails --table-id 42
  qctl review request 550e8400-e29b-41d4-a716-446655440000 --title "Ship it"`,
		Annotations: cmdutil.WithArgKinds(cmdutil.WithFormats(catalog.For("rule-review"), "table", "json", "yaml", "name"), "rule"),
		Args:        cobra.ExactArgs(1),
		RunE:        func(cmd *cobra.Command, args []string) error { return runRequest(cmd, args[0], opts) },
	}

	cmd.Flags().StringVar(&opts.release, "release", "", "Disambiguate when the rule has multiple releases")
	cmd.Flags().StringVar(&opts.title, "title", "", "Review title, 1-200 chars (default \"Review <name>@<release>\")")
	cmd.Flags().StringVar(&opts.description, "description", "", "Justification or description (max 4000 chars)")
	cmdutil.AddTableFlags(cmd, "Table (dataset)")
	cmd.Flags().StringArrayVar(&opts.reviewers, "reviewer", nil, "Initial reviewer: email, \"First Last\", or UUID (repeatable)")
	cmdutil.RefIntVar(cmd.Flags(), &opts.dryRunJob, "dry-run-job", "dry-run-job", "Link this dry-run job (must exist in this org) to the new review")
	return cmd
}

func runRequest(cmd *cobra.Command, input string, opts requestOptions) error {
	if err := cmdutil.ValidateOptionalTableSelector(cmd, ""); err != nil {
		return err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	// Resolved before the dry-run-job lookup and the rule resolution below: a
	// contradictory --table/--table-id is caught by OptionalTableID's own
	// local check, and a --table name lookup fails either way, so neither of
	// those two GETs should be paid for just to be discarded.
	datasetID, haveTable, err := cmdutil.OptionalTableID(ctx, cmd, "")
	if err != nil {
		return err
	}
	dryRunJob, err := requestDryRunJob(cmd, ctx, opts.dryRunJob)
	if err != nil {
		return err
	}
	rvClient, err := rule_versions.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}
	resolved, err := resolveRequestRule(rvClient, ctx, input, opts.release)
	if err != nil {
		return err
	}
	reviewClient := rule_reviews.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	createOpts, err := requestCreateOptions(ctx, reviewClient, resolved, opts, datasetID, haveTable)
	if err != nil {
		return err
	}
	created, err := reviewClient.CreateReview(ctx.Credential.AccessToken, resolved.ID, createOpts)
	if err != nil {
		return mapRequestError(err)
	}
	name, rel := resolved.Name, resolved.Release
	if created.RuleRevision != nil {
		name, rel = created.RuleRevision.Name, created.RuleRevision.Release
	}
	out := cmdutil.NewResultWriter(cmd)
	// created.ID is an openapi_types.UUID and cannot carry a control byte;
	// Status, name and release are free-form server strings and can.
	fmt.Fprintf(out.Human, "rule-review/%s created (status: %s, approvals: %d/%d)\n", created.ID, terminal.Sanitize(string(created.Status)), created.CurrentApprovals, created.RequiredApprovals)
	fmt.Fprintf(out.Human, "rule %s@%s is now in_review\n", terminal.Sanitize(name), terminal.Sanitize(rel))
	linked, err := linkRequestDryRun(out.Human, ctx, reviewClient, created.ID.String(), dryRunJob)
	if err != nil {
		// The review exists either way, and its id is what the recovery
		// command in the error needs.
		if writeErr := out.Write(created, "rule-review/"+created.ID.String()); writeErr != nil {
			return writeErr
		}
		return err
	}
	if linked != nil {
		created = linked
	}
	fmt.Fprintf(out.Human, "Next: qctl describe rule-review %s\n", created.ID)
	return out.Write(created, "rule-review/"+created.ID.String())
}

func requestDryRunJob(cmd *cobra.Command, ctx *cmdutil.CommandContext, jobID int) (*int, error) {
	if !cmd.Flags().Changed("dry-run-job") {
		return nil, nil
	}
	client := dry_runs.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if _, err := client.GetDryRunJob(ctx.Credential.AccessToken, jobID); err != nil {
		return nil, mapDryRunJobLookupError(err, jobID)
	}
	return &jobID, nil
}

func resolveRequestRule(client *rule_versions.Client, ctx *cmdutil.CommandContext, input, release string) (*rule_versions.ResolvedRule, error) {
	resolved, err := client.ResolveRuleFull(ctx.Credential.AccessToken, input, release)
	if err != nil {
		return nil, err
	}
	if resolved.Name == "" {
		detail, err := client.GetRuleRevisionDetails(ctx.Credential.AccessToken, resolved.ID)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch rule revision %s: %w", resolved.ID, err)
		}
		resolved.Name, resolved.Release = detail.Name, detail.Release
	}
	return resolved, nil
}

func requestCreateOptions(ctx *cmdutil.CommandContext, client *rule_reviews.Client, resolved *rule_versions.ResolvedRule, opts requestOptions, datasetID int, haveTable bool) (rule_reviews.CreateReviewOptions, error) {
	createOpts := rule_reviews.CreateReviewOptions{Title: opts.title, Description: opts.description}
	if createOpts.Title == "" {
		createOpts.Title = fmt.Sprintf("Review %s@%s", resolved.Name, resolved.Release)
	}
	// datasetID/haveTable come from OptionalTableID, resolved by the caller
	// before the dry-run-job lookup and rule resolution — see runRequest.
	if haveTable {
		createOpts.DatasetID = &datasetID
	}
	ids, _, err := cmdutil.ResolveUsers(client.FetchUsers, ctx.Credential.AccessToken, opts.reviewers)
	if err != nil {
		return createOpts, err
	}
	createOpts.ReviewerIDs = ids
	return createOpts, nil
}

// linkRequestDryRun links jobID to the new review and returns the review the
// link answered with, or nil when there is no job to link.
func linkRequestDryRun(human io.Writer, ctx *cmdutil.CommandContext, client *rule_reviews.Client, reviewID string, jobID *int) (*rule_reviews.Review, error) {
	if jobID == nil {
		return nil, nil
	}
	linked, err := client.LinkDryRun(ctx.Credential.AccessToken, reviewID, *jobID)
	if err != nil {
		return nil, fmt.Errorf(
			"review %s was created but linking dry-run job %d failed: %w\n"+
				"Recover with: qctl review link-dry-run %s %d",
			reviewID, *jobID, err, reviewID, *jobID)
	}
	fmt.Fprintf(human, "rule-review/%s dry run linked: job %d\n", reviewID, *jobID)
	return linked, nil
}

// mapRequestError converts machine-readable backend codes into actionable
// messages (overview spec, error-mapping table).
func mapRequestError(err error) error {
	var apiErr *rule_reviews.APIError
	if !errors.As(err, &apiErr) {
		return err
	}
	switch apiErr.Code {
	case "rule_review_already_open":
		id := apiErr.ContextString("review_id")
		status := apiErr.ContextString("status")
		title := apiErr.ContextString("title")
		return reword(err,
			"a review is already open for this revision and scope:\n"+
				"  rule-review/%s (status: %s) %q\n"+
				"Next: qctl describe rule-review %s  (or close it: qctl review close %s)",
			id, status, title, id, id)
	case "REVIEWER_NOT_DESIGNATED":
		return fmt.Errorf(
			"the effective review policy designates specific reviewers, and a requested reviewer is not one of them: %w", err)
	}
	if apiErr.StatusCode == 422 {
		return fmt.Errorf("%w\n(only draft or in_review revisions can be reviewed)", err)
	}
	return err
}

// rewordedError prints msg in place of err's text and unwraps to err.
type rewordedError struct {
	msg string
	err error
}

func (e *rewordedError) Error() string { return e.msg }
func (e *rewordedError) Unwrap() error { return e.err }

// reword returns an error that prints the formatted message and unwraps to err.
func reword(err error, format string, args ...any) error {
	return &rewordedError{msg: fmt.Sprintf(format, args...), err: err}
}
