// Package duplicate_group implements the reviewer verbs on a mirror or append
// table's duplicate groups.
package duplicate_group //nolint:revive // Package name mirrors the hyphenated command.

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/duplicate_groups"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

const resource = "duplicate-group"

// The error codes the reviewer routes answer with.
const (
	codeApproverNotDesignated   = "APPROVER_NOT_DESIGNATED"
	codeReviewerExcluded        = "DUPLICATE_DECISION_REVIEWER_EXCLUDED"
	codeGroupEnded              = "DUPLICATE_GROUP_ENDED"
	codeDecisionExists          = "DUPLICATE_DECISION_EXISTS"
	codeDecisionNotLive         = "DUPLICATE_DECISION_NOT_LIVE"
	codeDecisionNotYours        = "DUPLICATE_DECISION_NOT_YOURS"
	codeDecisionProposer        = "DUPLICATE_DECISION_PROPOSER"
	codeDecisionNotSeriesAllow  = "DUPLICATE_DECISION_NOT_SERIES_ALLOW"
	codeDecisionPending         = "DUPLICATE_DECISION_PENDING"
	codeThreadSpansSources      = "DUPLICATE_THREAD_SPANS_SOURCES"
	codeCommentDepthExceeded    = "COMMENT_DEPTH_EXCEEDED"
	approvePermissionNote       = "(deciding needs the DATA_REVIEW_APPROVE permission, which the admin, member and ops roles hold, and access to the table)"
	commentPermissionNote       = "(commenting needs the COMMENT_WRITE permission and read access to the table's data)"
	nonInteractiveConfirmSuffix = "requires confirmation; pass --yes"
)

// NewCommand creates the duplicate-group command group.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "duplicate-group",
		Short: "Decide, mark or retract a mirror or append table's duplicate groups",
		Long: `Decide the duplicate groups of a mirror or append table: rows whose key
repeats (repeated_key), identical rows on a keyless mirror table
(identical_rows), and rows a person marked as duplicates (person_marked).

  allow           allow the copies (repeated_key: up to --count)
  keep-one        keep one member and reject the rest
  not-duplicates  say a person-marked group's rows are not duplicates
  approve         approve the group's pending decision
  disagree        disagree with the group's pending decision
  dismiss         dismiss another reviewer's pending decision (admin, ops)
  withdraw        withdraw your own pending decision
  stop-series     propose to stop a series allow in every period
  allow-all       allow every repeated key of an upload
  reapply         retry unapplied duplicate group decisions
  comment         post to a group's internal discussion
  share           share a group's discussion with its data source

Mark rows as duplicates yourself, and take the mark back:

  mark     mark rows as one person_marked group, held or flagged
  retract  retract a person_marked group's mark

As a sender, answer a group with a copy on one of your cloud sources:

  respond            say the copies are separate rows, or that a corrected
                     file follows
  withdraw-response  withdraw a live response of your cloud source

Every verb takes the table with --table or --table-id; those acting on one
group take its full UUID (or duplicate-group/<uuid>) as their argument. A decision takes effect
once enough reviewers approve it and no counted reviewer disagrees; the
table's data review policy sets how many and who.`,
		Annotations: catalog.Parent(),
		RunE:        cmdutil.SubcommandRequired,
	}
	cmd.AddCommand(
		newAllowCommand(), newKeepOneCommand(), newNotDuplicatesCommand(), newApproveCommand(),
		newDisagreeCommand(), newDismissCommand(), newWithdrawCommand(), newStopSeriesCommand(), newAllowAllCommand(), newReapplyCommand(), newCommentCommand(), newShareCommand(),
		newMarkCommand(), newRetractCommand(), newRespondCommand(), newWithdrawResponseCommand(),
	)
	return cmd
}

// target is what every verb resolves before its request.
type target struct {
	ctx     *cmdutil.CommandContext
	client  *duplicate_groups.Client
	tableID int
}

func (t target) token() string { return t.ctx.Credential.AccessToken }

func resolveTarget(cmd *cobra.Command) (target, error) {
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return target{}, err
	}
	tableID, err := cmdutil.ResolveTableIDFromFlags(ctx, cmd)
	if err != nil {
		return target{}, err
	}
	return target{
		ctx:     ctx,
		client:  duplicate_groups.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity),
		tableID: tableID,
	}, nil
}

// addCommonFlags registers the table selector, --yes and, unless comment is
// "", --comment.
func addCommonFlags(cmd *cobra.Command, comment string) {
	cmdutil.AddTableFlags(cmd, "Table")
	if comment != "" {
		cmd.Flags().String("comment", "", comment)
	}
	cmd.Flags().Bool("yes", false, "Skip the confirmation prompt")
}

// commentFlag returns --comment, nil when it was not given; a given one
// that is blank or over the comment cap exits 2.
func commentFlag(cmd *cobra.Command) (*string, error) {
	if !cmd.Flags().Changed("comment") {
		return nil, nil
	}
	comment, _ := cmd.Flags().GetString("comment")
	if err := cmdutil.ValidateCommentBody(comment); err != nil {
		return nil, fmt.Errorf("--comment: %w", err)
	}
	return &comment, nil
}

// preflight parses the group argument and checks the table selector,
// before any request.
func preflight(cmd *cobra.Command, args []string) (openapi_types.UUID, error) {
	var id openapi_types.UUID
	if len(args) > 0 {
		parsed, err := duplicate_groups.ParseGroupID(args[0])
		if err != nil {
			return id, err
		}
		id = parsed
	}
	return id, cmdutil.ValidateTableSelectorFromFlags(cmd)
}

func confirm(cmd *cobra.Command, verb, question string) error {
	yes, _ := cmd.Flags().GetBool("yes")
	return cmdutil.Confirm(cmd.ErrOrStderr(), yes, verb+" "+nonInteractiveConfirmSuffix, question)
}

func groupRef(id openapi_types.UUID) string { return duplicate_groups.Ref(id) }

func decisionLabel(kind duplicate_groups.DecisionKind, count, keptMember *int) string {
	return duplicate_groups.KindLabel(kind, count, keptMember)
}

func labelOf(d *duplicate_groups.Decision) string { return duplicate_groups.DecisionLabel(d) }

func countedApprovals(d *duplicate_groups.Decision) int { return duplicate_groups.CountedApprovals(d) }

// writeDecisionResult prints a decision verb's result.
func writeDecisionResult(out io.Writer, groupID openapi_types.UUID, verb string, r *duplicate_groups.DecisionResult) {
	d := &r.Decision
	fmt.Fprintf(out, "%s %s %s [d: %s] (decision: %s, approvals: %d/%d, group: %s)\n",
		groupRef(groupID), verb, labelOf(d), d.ID, terminal.Sanitize(string(d.State)),
		countedApprovals(d), d.RequiredApprovals, terminal.Sanitize(string(r.State)))
	if d.Scope == duplicate_groups.ScopeSeries && d.Kind != duplicate_groups.KindStopSeries &&
		(d.State == duplicate_groups.StatePending || d.State == duplicate_groups.StateEffective) {
		fmt.Fprintf(out, "scope: series%s — it also governs this key in the series' later uploads and periods\n",
			periodSuffix(d.PeriodKey))
	}
	writeApply(out, &r.Apply)
}

// writeProgress prints what d still waits for, or what its enactment ended.
func writeProgress(out io.Writer, tableID int, groupID openapi_types.UUID, d *duplicate_groups.Decision) {
	switch d.State {
	case duplicate_groups.StatePending:
		if missing := d.RequiredApprovals - countedApprovals(d); missing > 0 {
			fmt.Fprintf(out, "Waiting for %d more approval(s): another reviewer runs "+
				"'qctl duplicate-group approve %s --table-id %d'\n", missing, groupID, tableID)
		}
		if held := duplicate_groups.CountedDisagreements(d); held > 0 {
			fmt.Fprintf(out, "Held by %d disagreement(s): it takes effect only once each disagreeing reviewer approves\n", held)
		}
	case duplicate_groups.StateEnacted:
		if d.Kind == duplicate_groups.KindStopSeries && d.ReplacesDecisionID != nil {
			fmt.Fprintf(out, "The series allow [d: %s] no longer applies in any period\n", d.ReplacesDecisionID)
		}
	}
}

func periodSuffix(periodKey *string) string {
	if periodKey == nil || *periodKey == "" {
		return ""
	}
	return " (from period " + terminal.Sanitize(*periodKey) + ")"
}

// writeApply prints what applying the change did in each table it reached.
func writeApply(out io.Writer, a *duplicate_groups.ApplyResult) {
	for _, p := range a.Periods {
		line := fmt.Sprintf("  table/%d %s: moved %d, released %d, reset %d, enqueued %d row(s)",
			p.DatasetID, terminal.Sanitize(string(p.Outcome)), len(p.MovedRowIds), len(p.ReleasedRowIds),
			len(p.ResetRowIds), len(p.EnqueuedRowIds))
		if p.Reason != nil && *p.Reason != "" {
			line += " — " + terminal.Sanitize(*p.Reason)
		}
		fmt.Fprintln(out, line)
	}
}

// warnUnapplied names, on stderr, each table where the change did not apply.
func warnUnapplied(cmd *cobra.Command, a *duplicate_groups.ApplyResult) {
	for _, p := range a.Periods {
		if p.Outcome != "applied" {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: table/%d was not updated (%s); "+
				"'qctl duplicate-group reapply --table-id %d' retries it\n",
				p.DatasetID, terminal.Sanitize(string(p.Outcome)), p.DatasetID)
		}
	}
}

// mapError explains the duplicate-group routes' refusals. %w keeps each
// exit code.
func mapError(err error, tableID int, groupID openapi_types.UUID) error {
	var apiErr *duplicate_groups.APIError
	if !errors.As(err, &apiErr) {
		return err
	}
	switch apiErr.Code {
	case codeApproverNotDesignated:
		return fmt.Errorf("%w\n(the table's data review policy names who may decide its duplicate groups; "+
			"'qctl get data-review-policy --table-id %d' lists them. A pending decision keeps the reviewers "+
			"named when it was proposed)", err, tableID)
	case codeReviewerExcluded:
		return fmt.Errorf("%w\n(someone who edited or requested review of the group's rows, or who marked "+
			"the group, cannot decide it; another reviewer must)", err)
	case codeGroupEnded:
		return fmt.Errorf("%w\n(an ended group takes no decision; %s shows why it ended)", err, describeCmd(tableID, groupID))
	case codeDecisionPending:
		return fmt.Errorf("%w\n(another reviewer's decision is pending on the group: approve it "+
			"'qctl duplicate-group approve %s --table-id %d' or disagree "+
			"'qctl duplicate-group disagree %s --table-id %d --reason <text>'; %s shows it)",
			err, groupID, tableID, groupID, tableID, describeCmd(tableID, groupID))
	case codeDecisionNotLive:
		return fmt.Errorf("%w\n(the decision was superseded or withdrawn meanwhile; %s shows the live ones)",
			err, describeCmd(tableID, groupID))
	case codeThreadSpansSources:
		return fmt.Errorf("%w\n(data sources: %s; a discussion can be shared with only one. "+
			"'qctl duplicate-group comment' still posts to the internal discussion)", err, contextIDs(apiErr))
	case apierror.CodeChangeProposalRequired, apierror.CodeChangeReviewPolicyDenied:
		return err
	}
	if apiErr.StatusCode == http.StatusForbidden && apiErr.Code == "" {
		return fmt.Errorf("%w\n%s", err, approvePermissionNote)
	}
	return err
}

// mapVoteError explains the refusals of the routes acting on a pending
// decision, then defers to mapError. dismissing names dismiss's permission
// in a code-less 403. %w keeps each exit code.
func mapVoteError(err error, tableID int, groupID openapi_types.UUID, dismissing bool) error {
	var apiErr *duplicate_groups.APIError
	if !errors.As(err, &apiErr) {
		return err
	}
	switch {
	case apiErr.Code == codeDecisionNotLive:
		return fmt.Errorf("%w\n(the decision is no longer the group's pending decision: it took effect, or was "+
			"replaced, withdrawn or dismissed after qctl read it; %s shows the group now)", err, describeCmd(tableID, groupID))
	case apiErr.Code == codeDecisionNotYours:
		return fmt.Errorf("%w\n(only its proposer may withdraw it; disagree with it instead, or ask an admin or "+
			"ops user to dismiss it)", err)
	case apiErr.Code == codeDecisionProposer:
		return fmt.Errorf("%w\n(you proposed it; withdraw it instead: 'qctl duplicate-group withdraw %s --table-id %d')",
			err, groupID, tableID)
	case dismissing && apiErr.StatusCode == http.StatusForbidden && apiErr.Code == "":
		return fmt.Errorf("%w\n(dismissing needs an admin or ops role and DATA_REVIEW_APPROVE on the table)", err)
	}
	return mapError(err, tableID, groupID)
}

// mapStopSeriesError explains the stop-series route's refusals, then defers
// to mapError. %w keeps each exit code.
func mapStopSeriesError(err error, tableID int, groupID openapi_types.UUID) error {
	var apiErr *duplicate_groups.APIError
	if !errors.As(err, &apiErr) {
		return err
	}
	switch apiErr.Code {
	case codeDecisionNotSeriesAllow:
		return fmt.Errorf("%w\n(no effective series allow it may stop governs the group; %s shows "+
			"'can stop the series allow')", err, describeCmd(tableID, groupID))
	case codeDecisionPending:
		return fmt.Errorf("%w\n(the group has a pending decision; it must take effect or be withdrawn or dismissed "+
			"first; %s shows it)", err, describeCmd(tableID, groupID))
	case codeDecisionNotLive:
		return fmt.Errorf("%w\n(the series allow no longer governs the group; %s shows the group now)",
			err, describeCmd(tableID, groupID))
	}
	return mapError(err, tableID, groupID)
}

// describeCmd is the quoted describe duplicate-group command for a group.
func describeCmd(tableID int, groupID openapi_types.UUID) string {
	return fmt.Sprintf("'qctl describe duplicate-group %s --table-id %d'", groupID, tableID)
}

// writeNext prints the describe line that follows a decision verb's result.
func writeNext(out io.Writer, tableID int, groupID openapi_types.UUID) {
	fmt.Fprintf(out, "Next: qctl describe duplicate-group %s --table-id %d\n", groupID, tableID)
}

// contextIDs renders context.data_source_model_ids.
func contextIDs(apiErr *duplicate_groups.APIError) string {
	raw, _ := apiErr.Context["data_source_model_ids"].([]interface{})
	ids := make([]string, 0, len(raw))
	for _, v := range raw {
		ids = append(ids, terminal.Sanitize(fmt.Sprint(v)))
	}
	if len(ids) == 0 {
		return "not named"
	}
	return strings.Join(ids, ", ")
}
