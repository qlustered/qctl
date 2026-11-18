package change_proposal //nolint:revive // Package name mirrors the hyphenated command.

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/qlustered/qctl/internal/alerts"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/change_proposals"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/commands/resolveexplain"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

func newApproveCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "approve <proposal-id>",
		Short: "Approve a pending_review proposal, which runs its resolution or params patch as you",
		Long: `Approve a pending_review proposal. Approving runs the proposed alert or
warning resolution, or a rule-param proposal's params patch, as you, the
approver, under your role and the table's change review policy, and the
proposal ends executed or execution_failed.

approve reads the proposal first and prints what it would run: the payload as
the AlertResolution or WarningResolution manifest it carries (for a rule-param
proposal, the params it patches the rule with), and the server's review of it
now (whether it still applies, what would refuse it, what it changes and how
that moved since it went to review; for a rule-param proposal, whether your
approval also re-approves the table series' rule set). It then asks for
confirmation (--yes skips the prompt; without a terminal --yes is required)
and sends the version and impact digest it read, so the server refuses the
approval with 409 (exit 2, nothing run) when the proposal or its impact moved
since, when it no longer applies as submitted, or when the resolution would
be refused. A proposal with a destructive action (delete_*, discard_*, the
file-ignore actions) also needs the context named with --context on a
protected context, as 'qctl alert resolve' does for that action.

Outcomes:
  executed           the resolution ran; exit 0
  execution_failed   it failed after committing part of its change, which
                     stays; exit 1 (see what moved with 'qctl get history')
  refused            it failed before changing anything: the resolution's own
                     error, with its exit code; the proposal stays pending_review

An executed rule-param proposal prints its outcome instead of the response:
applied (the rule has the new params) or staged (the patch became a rule
suggestion, named with the 'qctl describe rule-suggestion' command that shows
it, and applies once that is accepted), the suggestion's application
(pending, applied or dismissed), and whether the series rule approval was
re-recorded or, if not, why (staged, not_in_series, approver_role,
approver_table_access, no_approval, approved_params_differ,
rule_set_differs). The target line shows the rule's state and params.

An executed keyword-resolution proposal can stage a rule suggestion while the
alert or warning stays open. qctl prints what decides the suggestion.
Applying it also resolves the issue when the keyword is the issue's own value
and the applied rule still allows it; if the issue is still open once the
keyword is live, resolve it with the same payload shown above. A revalidation
candidate must finish its validation run; a suggestion on a draft migration
can be accepted or applied with that migration.

The preview goes to stderr, next to the prompt. -o json|yaml prints the
proposal and moves the result lines to stderr too; -o name prints
change-proposal/<uuid>. A proposal in any other state than pending_review is
sent without the preview or the prompt, and the server's 409 is the answer.

Examples:
  qctl change-proposal approve 0190bbbb-1111-2222-3333-444444444444
  qctl change-proposal approve 0190bbbb-1111-2222-3333-444444444444 --yes -o json`,
		Annotations: cmdutil.WithFormats(catalog.For("change-proposal"), "table", "json", "yaml", "name"),
		Args:        cobra.ExactArgs(1),
		RunE:        runApprove,
	}
	cmd.Flags().Bool("yes", false, "Approve without the confirmation prompt")
	return cmd
}

func runApprove(cmd *cobra.Command, args []string) error {
	// Before Bootstrap: a malformed id is bad usage and costs no request.
	id, err := change_proposals.ParseID(args[0])
	if err != nil {
		return err
	}
	yes, _ := cmd.Flags().GetBool("yes")
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	token := ctx.Credential.AccessToken
	client := newClient(ctx)
	detail, err := client.Get(cmd.Context(), token, id)
	if err != nil {
		return fmt.Errorf("failed to get change proposal: %w", err)
	}
	read := &detail.Proposal

	w := cmdutil.NewResultWriter(cmd)
	body := change_proposals.ApproveRequest{VersionID: read.VersionID}
	switch {
	case read.State != api.ChangeProposalStatePendingReview:
		// Only a pending_review proposal is approvable: the POST goes without
		// a preview or prompt, and the server's state refusal, which carries
		// the fresh proposal, is the answer.
	case detail.Review == nil:
		return qerrors.NewBadUsagef("the server returned no review of pending_review %s, so there is no "+
			"impact digest to approve: it predates impact digests; upgrade the server", proposalName(read))
	case detail.Review.ImpactDigest == nil:
		return qerrors.New(qerrors.ExitUnauthorized, fmt.Sprintf(
			"cannot approve %s: the server withheld its impact digest because your view of its payload is masked "+
				"(masked_fields: %s); approving needs access to the table's unmasked PII values",
			proposalName(read), terminal.Sanitize(strings.Join(change_proposals.MaskedFields(read), ", "))))
	default:
		fmt.Fprint(cmd.ErrOrStderr(), approvePreview(detail))
		if err := confirmApproval(cmd, read, yes); err != nil {
			return err
		}
		body.ImpactDigest = *detail.Review.ImpactDigest
	}

	approved, err := client.Approve(cmd.Context(), token, id, body)
	if err != nil {
		return explainApproveError(cmd, ctx, client, read, err)
	}
	writeLead(w.Human, approved, "approved")
	failed := writeExecution(w.Human, approved)
	writeStagedKeyword(w.Human, ctx, approved)
	writeNext(w.Human, approved)
	if err := w.Write(approved, proposalName(approved)); err != nil {
		return err
	}
	if failed {
		return qerrors.New(qerrors.ExitGenericError, fmt.Sprintf(
			"%s failed after committing part of its change", proposalName(approved)))
	}
	return nil
}

// approvePreview is what approving the proposal would run.
func approvePreview(d *change_proposals.Detail) string {
	p := &d.Proposal
	var b strings.Builder
	fmt.Fprintf(&b, "# change-proposal/%s — approving runs this as you\n", p.ID)
	fmt.Fprintf(&b, "State: %s, version %d\n", p.State, p.VersionID)
	target := change_proposals.Target(p)
	if target == "" {
		target = "(none recorded)"
	}
	fmt.Fprintf(&b, "Operation: %s on %s\n", p.Operation, target)
	if p.DatasetID != nil {
		fmt.Fprintf(&b, "Table: table-id %d\n", *p.DatasetID)
	}
	fmt.Fprintf(&b, "Addressed alerts: %s\n", refs("alert", p.AddressedAlertItemIds))
	fmt.Fprintf(&b, "Addressed warnings: %s\n", refs("warning", p.AddressedWarningItemIds))
	b.WriteString(change_proposals.DependsOnLine(p))
	b.WriteString(change_proposals.NoteLine(p))
	change_proposals.WritePayload(&b, p)
	change_proposals.WriteReview(&b, d)
	change_proposals.WriteOverlaps(&b, d)
	b.WriteString("\n")
	return terminal.Sanitize(b.String())
}

func refs(kind string, ids []int) string {
	if len(ids) == 0 {
		return "(none)"
	}
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = fmt.Sprintf("%s/%d", kind, id)
	}
	return strings.Join(out, ", ")
}

// confirmApproval runs the protected-context gate for a destructive payload
// action, then the approval prompt.
func confirmApproval(cmd *cobra.Command, p *change_proposals.Proposal, yes bool) error {
	action := change_proposals.PayloadAction(p)
	what := "its resolution"
	switch {
	case p.Operation == api.ChangeReviewOperationDatasetRulePatch:
		what = "its params patch"
	case action != "":
		what = "action " + action
	}
	if cmdutil.IsDestructiveAction(action) {
		if err := cmdutil.RequireNamedContext(cmd,
			fmt.Sprintf("`%s` of a proposal with action %s", cmd.CommandPath(), action)); err != nil {
			return err
		}
	}
	target := targetName(p)
	return cmdutil.Confirm(cmd.ErrOrStderr(), yes,
		fmt.Sprintf("confirmation required but stdin is not an interactive terminal — read the proposal above, "+
			"then pass --yes to approve %s and run %s on %s as you", proposalName(p), what, target),
		fmt.Sprintf("Approve %s and run %s on %s as you?", proposalName(p), what, target))
}

// writeExecution writes the execution result of an approved proposal and
// reports whether it failed.
func writeExecution(out io.Writer, p *change_proposals.Proposal) bool {
	result := map[string]interface{}{}
	if p.ExecutionResult != nil {
		result = *p.ExecutionResult
	}
	failed := p.State == api.ChangeProposalStateExecutionFailed
	switch {
	case failed:
		fmt.Fprintf(out, "Execution failed: %s\n", terminal.Sanitize(executionErrorText(result["error"])))
		if result["partial"] == true {
			line := "Partial: true — part of the change was committed and stays"
			if p.DatasetID != nil {
				line += fmt.Sprintf("; see what moved: qctl get history --table-id %d", *p.DatasetID)
			}
			fmt.Fprintln(out, line)
		}
	case p.State == api.ChangeProposalStateExecuted && p.Operation == api.ChangeReviewOperationDatasetRulePatch:
		fmt.Fprint(out, terminal.Sanitize(change_proposals.RuleOutcome(p)))
	case p.State == api.ChangeProposalStateExecuted:
		fmt.Fprintf(out, "Response: %s\n", terminal.Sanitize(responseSummary(result["response"])))
	}
	if ts, present := result["target_state"]; present {
		state := targetState(ts)
		if p.Operation == api.ChangeReviewOperationDatasetRulePatch {
			state = terminal.Sanitize(change_proposals.RuleTargetState(ts))
		}
		fmt.Fprintf(out, "Target %s now: %s\n", terminal.Sanitize(targetName(p)), state)
	}
	return failed
}

func executionErrorText(v interface{}) string {
	if v == nil {
		return "(no error recorded)"
	}
	return change_proposals.ExecutionError(v)
}

// responseSummary renders the operation's response as its set fields.
func responseSummary(v interface{}) string {
	m, ok := v.(map[string]interface{})
	if !ok {
		if v == nil {
			return "(none recorded)"
		}
		return fmt.Sprint(v)
	}
	keys := make([]string, 0, len(m))
	for k, val := range m {
		if val != nil && val != "" && val != false {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return "(nothing beyond the defaults)"
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s: %v", k, m[k])
	}
	return strings.Join(parts, ", ")
}

func targetName(p *change_proposals.Proposal) string {
	if t := change_proposals.Target(p); t != "" {
		return t
	}
	if p.Operation == api.ChangeReviewOperationDatasetRulePatch {
		return change_proposals.TargetKindRule
	}
	return "issue"
}

// targetState renders execution_result.target_state: the target's
// resolution fields, or null when the target is gone.
func targetState(v interface{}) string {
	m, ok := v.(map[string]interface{})
	switch {
	case v == nil:
		return "gone"
	case !ok:
		return terminal.Sanitize(fmt.Sprint(v))
	case m["resolved"] == true:
		return "resolved"
	case m["resolve_after_migration"] == true:
		return "resolves after migration"
	default:
		return "still open"
	}
}

// explainApproveError explains a failed approval. A CHANGE_PROPOSAL_* 409
// goes through change_proposals.Explain. Any other server error is the
// resolution's own or a refusal before it ran: it goes through the resolve
// explainers, and a re-read reports the proposal's state.
func explainApproveError(cmd *cobra.Command, ctx *cmdutil.CommandContext, client *change_proposals.Client,
	p *change_proposals.Proposal, err error,
) error {
	if change_proposals.IsProposalConflict(err) {
		return change_proposals.Explain(err)
	}
	var apiErr *change_proposals.APIError
	if !errors.As(err, &apiErr) {
		return fmt.Errorf("%w\n\nWhether the approval ran is unknown; re-read it:\n  qctl describe change-proposal %s", err, p.ID)
	}
	token := ctx.Credential.AccessToken
	action := change_proposals.PayloadAction(p)
	switch {
	case apiErr.Code == change_proposals.CodePolicyDenied:
		// The approver check refused you; the resolution never ran.
	case p.Operation == api.ChangeReviewOperationAlertResolve && p.AlertItemID != nil:
		alert, getErr := alerts.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity).GetAlert(token, *p.AlertItemID)
		if getErr != nil {
			alert = nil
		}
		err = resolveexplain.Alert(ctx, err, alert, action)
	case p.Operation == api.ChangeReviewOperationWarningResolve:
		err = resolveexplain.Warning(err, nil, action)
	}
	fresh, getErr := client.Get(cmd.Context(), token, p.ID)
	if getErr != nil {
		return err
	}
	if fresh.State == api.ChangeProposalStatePendingReview {
		return fmt.Errorf("%w\n\n%s is still pending_review (version %d): nothing was applied. Re-read it:\n  qctl describe change-proposal %s",
			err, proposalName(&fresh.Proposal), fresh.VersionID, p.ID)
	}
	return fmt.Errorf("%w\n\n%s is now %s. Re-read it:\n  qctl describe change-proposal %s",
		err, proposalName(&fresh.Proposal), terminal.Sanitize(string(fresh.State)), p.ID)
}
