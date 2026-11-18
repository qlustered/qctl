package describe

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/alerts"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/change_proposals"
	"github.com/qlustered/qctl/internal/client"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/comments"
	"github.com/qlustered/qctl/internal/dataset_rules"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/pkg/timeutil"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/qlustered/qctl/internal/warnings"
	"github.com/spf13/cobra"
)

// NewChangeProposalCommand creates the describe change-proposal command.
func NewChangeProposalCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "change-proposal <proposal-id>",
		Short: "Show one change proposal, its target, its payload, its review and next actions",
		Long: `Show one change proposal: its state, its target — the issue it would resolve,
or the table rule whose params it would change — with that target's state now,
the addressed alerts and warnings, the proposal it depends on, the payload
rendered as the AlertResolution or WarningResolution manifest it carries (for
a rule-param proposal, the params it would patch the rule with), the server's
review of it now (whether it still applies, what would refuse it, what
approving it changes — for a rule, the params diff, the series rule approval
and the validation dry run — the resources it acts on, and how that moved
since it went to review), the other live proposals of the table
that act on the same resources, the people on it, the decision, the execution
result, and the steps you may take now.

The proposal id is the full UUID printed by 'qctl get change-proposals'.

The default output is a self-describing text payload designed for both humans
and AI agents. Use -o json|yaml for the raw API response.

Examples:
  qctl describe change-proposal 0190bbbb-1111-2222-3333-444444444444
  qctl describe change-proposal 0190bbbb-1111-2222-3333-444444444444 -o json`,
		Annotations: cmdutil.WithFormats(catalog.For("change-proposal"), "table", "json", "yaml"),
		Args:        cobra.ExactArgs(1),
		RunE:        runDescribeChangeProposal,
	}
	return cmd
}

func runDescribeChangeProposal(cmd *cobra.Command, args []string) error {
	// Before Bootstrap: a malformed id is bad usage (exit 2) and costs no request.
	id, err := change_proposals.ParseID(args[0])
	if err != nil {
		return err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	token := ctx.Credential.AccessToken
	detail, err := change_proposals.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity).
		Get(cmd.Context(), token, id)
	if err != nil {
		return fmt.Errorf("failed to get change proposal: %w", err)
	}

	if cmd.Flags().Changed("output") {
		outputFormat, _ := cmd.Flags().GetString("output")
		switch outputFormat {
		case outputFormatJSON, outputFormatYAML:
			return output.WriteRaw(cmd.OutOrStdout(), outputFormat, detail)
		}
	}

	view := proposalView{
		Detail:    detail,
		Proposal:  &detail.Proposal,
		Target:    lookUpProposalTarget(cmd, ctx, token, &detail.Proposal),
		Directory: cmdutil.FetchDisplayDirectory(cmd, proposalDirectory(ctx), token),
	}
	_, err = fmt.Fprint(cmd.OutOrStdout(), renderProposal(&view))
	return err
}

// proposalDirectory sweeps the org's users for naming the people on a
// proposal.
func proposalDirectory(ctx *cmdutil.CommandContext) func(string) (*comments.Directory, error) {
	return func(accessToken string) (*comments.Directory, error) {
		orgID, err := uuid.Parse(ctx.OrganizationID)
		if err != nil {
			return nil, fmt.Errorf("invalid organization id %q: %w", ctx.OrganizationID, err)
		}
		apiClient, err := client.New(client.Config{
			BaseURL: ctx.ServerURL, AccessToken: accessToken, Verbosity: ctx.Verbosity,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create API client: %w", err)
		}
		return comments.FetchUsers(apiClient, orgID)
	}
}

// proposalView is everything renderProposal prints.
type proposalView struct {
	Detail    *change_proposals.Detail
	Proposal  *change_proposals.Proposal // &Detail.Proposal
	Target    proposalTarget
	Directory *comments.Directory
}

// proposalTarget is the target issue or rule as read now.
type proposalTarget struct {
	Ref         string // alert/<id>, warning/<id> or rule/<uuid>; "" when the proposal names none
	Kind        string // alert, warning or rule when Ref is "" and the target was deleted
	Status      string // open, resolved, resolves after migration, a rule's state, gone, unknown
	Resolved    bool
	ResolvedAt  *time.Time
	IssueType   string
	DatasetName string
	Holder      *api.ChangeProposalSummarySchema
	RuleName    string // the rule's instance name
}

// noneListed stands in for an empty list.
const noneListed = "(none)"

// Target statuses.
const (
	targetOpen      = "open"
	targetResolved  = "resolved"
	targetMigration = "resolves after migration"
	targetGone      = "gone"
	targetUnknown   = "unknown"
	targetDeleted   = string(api.DatasetRuleStateDeleted)
)

// lookUpProposalTarget reads the target issue or rule. A proposal of a
// proposable operation with no target id, or a 404, reports the target gone;
// any other failure warns once on stderr and reports its status unknown.
func lookUpProposalTarget(cmd *cobra.Command, ctx *cmdutil.CommandContext, token string, p *change_proposals.Proposal) proposalTarget {
	t := proposalTarget{Ref: change_proposals.Target(p)}
	var err error
	switch {
	case p.AlertItemID != nil:
		var a *alerts.AlertFull
		a, err = alerts.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity).GetAlert(token, *p.AlertItemID)
		if err == nil {
			t.fill(a.Resolved, a.ResolvedAt, string(a.IssueType), a.DatasetName, a.ChangeProposal)
			if a.ResolveAfterMigration != nil && *a.ResolveAfterMigration && !t.Resolved {
				t.Status = targetMigration
			}
		}
	case p.WarningItemID != nil:
		var w *warnings.WarningFull
		w, err = warnings.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity).GetWarning(token, *p.WarningItemID)
		if err == nil {
			t.fill(w.Resolved, w.ResolvedAt, string(w.IssueType), w.DatasetName, w.ChangeProposal)
		}
	case p.DatasetRuleID != nil:
		var rules *dataset_rules.Client
		if rules, err = dataset_rules.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity); err == nil {
			var r *dataset_rules.DatasetRuleDetail
			if r, err = rules.GetDatasetRuleDetailContext(cmd.Context(), token, p.DatasetRuleID.String()); err == nil {
				t.Status, t.RuleName = string(r.State), r.InstanceName
			}
		}
	default:
		if t.Kind = change_proposals.TargetKind(p.Operation); t.Kind != "" {
			t.Status = targetGone
		}
		return t
	}
	switch {
	case err == nil:
	case qerrors.GetExitCode(err) == qerrors.ExitNotFound:
		t.Status = targetGone
	default:
		t.Status = targetUnknown
		fmt.Fprintf(cmd.ErrOrStderr(), "Warning: could not read %s; its current state is unknown: %v\n", t.Ref, err)
	}
	return t
}

func (t *proposalTarget) fill(resolved *bool, at *time.Time, issueType, datasetName string, holder *api.ChangeProposalSummarySchema) {
	t.Resolved = resolved != nil && *resolved
	t.Status = targetOpen
	if t.Resolved {
		t.Status = targetResolved
		t.ResolvedAt = at
	}
	t.IssueType, t.DatasetName, t.Holder = issueType, datasetName, holder
}

// renderProposal renders the agent-facing text payload.
func renderProposal(v *proposalView) string {
	p := v.Proposal
	var b strings.Builder
	fmt.Fprintf(&b, "# change-proposal/%s\n", p.ID)
	phase := "terminal"
	if change_proposals.IsLive(p.State) {
		phase = "live"
	}
	fmt.Fprintf(&b, "State: %s (%s)\n", p.State, phase)
	fmt.Fprintf(&b, "Operation: %s\n", p.Operation)
	renderProposalTarget(&b, v)
	renderProposalTable(&b, v)
	fmt.Fprintf(&b, "Addressed alerts: %s\n", issueRefs("alert", p.AddressedAlertItemIds))
	fmt.Fprintf(&b, "Addressed warnings: %s\n", issueRefs("warning", p.AddressedWarningItemIds))
	b.WriteString(change_proposals.DependsOnLine(p))
	b.WriteString(change_proposals.NoteLine(p))
	fmt.Fprintf(&b, "Version: %d\n", p.VersionID)
	fmt.Fprintf(&b, "Created: %s by %s\n", proposalTime(p.CreatedAt), v.Directory.DisplayName(p.CreatedByID))
	fmt.Fprintf(&b, "Updated: %s\n", proposalTime(p.UpdatedAt))
	fmt.Fprintf(&b, "Authors: %s\n", authors(v.Directory, p.AuthorIds, v.Detail.Authors))
	fmt.Fprintf(&b, "Policy keys: %s\n", policyKeys(p.PolicyKeys))

	mapped := change_proposals.WritePayload(&b, p)
	change_proposals.WriteReview(&b, v.Detail)
	change_proposals.WriteOverlaps(&b, v.Detail)
	renderProposalDecision(&b, v)
	renderProposalExecution(&b, p)
	renderProposalNextActions(&b, p, mapped)
	return terminal.Sanitize(b.String())
}

func renderProposalTarget(b *strings.Builder, v *proposalView) {
	t := v.Target
	switch {
	case t.Ref == "" && t.Kind != "":
		fmt.Fprintf(b, "Target: a deleted %s (the server cleared its id) — now: %s\n", t.Kind, t.Status)
	case t.Ref == "":
		b.WriteString("Target: (none recorded)\n")
		return
	default:
		renderTargetIssue(b, v)
	}
	p := v.Proposal
	if !change_proposals.IsLive(p.State) || p.State == api.ChangeProposalStateExecuting {
		return
	}
	switch {
	case p.Operation == api.ChangeReviewOperationDatasetRulePatch && (t.Status == targetGone || t.Status == targetDeleted):
		b.WriteString("Note: the target rule is deleted or gone, so the next step taken on this proposal turns it " +
			"obsolete and is refused with 409 CHANGE_PROPOSAL_STATE_CONFLICT.\n")
	case p.Operation != api.ChangeReviewOperationDatasetRulePatch && (t.Resolved || t.Status == targetGone):
		b.WriteString("Note: the target is resolved or gone, so the next step taken on this proposal turns it " +
			"obsolete and is refused with 409 CHANGE_PROPOSAL_STATE_CONFLICT.\n")
	}
}

func renderTargetIssue(b *strings.Builder, v *proposalView) {
	t := v.Target
	status := t.Status
	if t.ResolvedAt != nil {
		status += " " + proposalTime(*t.ResolvedAt)
	}
	if t.IssueType != "" {
		status += fmt.Sprintf(" (issue type %s)", t.IssueType)
	}
	ref := t.Ref
	if t.RuleName != "" {
		ref += fmt.Sprintf(" (%s)", t.RuleName)
	}
	fmt.Fprintf(b, "Target: %s — now: %s\n", ref, status)
	if t.Holder != nil {
		holder := fmt.Sprintf("change-proposal/%s (%s)", t.Holder.ID, t.Holder.State)
		if t.Holder.ID == v.Proposal.ID {
			holder = "this proposal"
		}
		fmt.Fprintf(b, "Held by: %s\n", holder)
	}
}

func renderProposalTable(b *strings.Builder, v *proposalView) {
	p := v.Proposal
	switch {
	case p.DatasetID != nil && v.Target.DatasetName != "":
		fmt.Fprintf(b, "Table: %s (table-id %d)\n", v.Target.DatasetName, *p.DatasetID)
	case p.DatasetID != nil:
		fmt.Fprintf(b, "Table: table-id %d\n", *p.DatasetID)
	default:
		b.WriteString("Table: (none recorded)\n")
	}
	if p.DataSourceModelID != nil {
		fmt.Fprintf(b, "Cloud source: %d\n", *p.DataSourceModelID)
	}
}

func renderProposalDecision(b *strings.Builder, v *proposalView) {
	p := v.Proposal
	b.WriteString("\n## DECISION\n")
	if p.DecisionReason != nil && *p.DecisionReason != "" {
		fmt.Fprintf(b, "Decision reason: %q\n", *p.DecisionReason)
	} else {
		b.WriteString("Decision reason: (none)\n")
	}
	if p.ApprovedByID == nil {
		b.WriteString("Approved: (not approved)\n")
		return
	}
	approval := "by " + v.Directory.DisplayName(*p.ApprovedByID)
	if p.ApproverRole != nil && *p.ApproverRole != "" {
		approval += fmt.Sprintf(" (role %s)", *p.ApproverRole)
	}
	if p.ApprovedAt != nil {
		approval += " at " + proposalTime(*p.ApprovedAt)
	}
	fmt.Fprintf(b, "Approved: %s\n", approval)
}

// resultErrorKey is execution_result's error key.
const resultErrorKey = "error"

// renderProposalExecution writes the execution timestamps and result. The
// result's response, error, partial, settled_after_timeout and target_state
// keys are rendered as lines, and for a rule-param proposal the keys of
// change_proposals.RuleOutcomeKeys too; any other key is rendered as raw YAML.
func renderProposalExecution(b *strings.Builder, p *change_proposals.Proposal) {
	b.WriteString("\n## EXECUTION\n")
	fmt.Fprintf(b, "Started: %s\n", optionalTime(p.ExecutionStartedAt, "(not started)"))
	fmt.Fprintf(b, "Executed: %s\n", optionalTime(p.ExecutedAt, "(not executed)"))
	if p.ExecutionResult == nil {
		b.WriteString("Result: (none)\n")
		return
	}
	result := *p.ExecutionResult
	rule := p.Operation == api.ChangeReviewOperationDatasetRulePatch
	stagedAs := change_proposals.StagedAs(p)
	rest := map[string]interface{}{}
	for k, val := range result {
		switch k {
		case "response", resultErrorKey, "partial", "settled_after_timeout", "target_state":
		default:
			if k == "staged_as" && stagedAs != "" {
				continue
			}
			if !rule || !change_proposals.RuleOutcomeKeys[k] {
				rest[k] = val
			}
		}
	}
	renderExecutionOutcome(b, p, result)
	if rule {
		b.WriteString(change_proposals.RuleOutcome(p))
	} else {
		b.WriteString(stagedAs)
	}
	if ts, present := result["target_state"]; present {
		if rule {
			fmt.Fprintf(b, "Target at settlement: %s\n", change_proposals.RuleTargetState(ts))
		} else if m, ok := ts.(map[string]interface{}); ok {
			fmt.Fprintf(b, "Target at settlement: %s\n", flatMap(m))
		} else if ts == nil {
			b.WriteString("Target at settlement: gone\n")
		} else {
			rest["target_state"] = ts
		}
	}
	if len(rest) > 0 {
		b.WriteString("Other result fields:\n")
		change_proposals.WriteYAMLBlock(b, rest)
	}
}

func renderExecutionOutcome(b *strings.Builder, p *change_proposals.Proposal, result map[string]interface{}) {
	errVal, resp := result[resultErrorKey], result["response"]
	switch {
	case errVal != nil:
		fmt.Fprintf(b, "Result: FAILED — %s\n", change_proposals.ExecutionError(errVal))
	case resp != nil && p.Operation == api.ChangeReviewOperationDatasetRulePatch:
		b.WriteString("Result: succeeded (-o yaml carries the response: the rule, or the staged suggestion)\n")
	case resp != nil:
		b.WriteString("Result: succeeded — response:\n")
		change_proposals.WriteYAMLBlock(b, resp)
	default:
		b.WriteString("Result: (no response recorded)\n")
	}
	if settled, _ := result["settled_after_timeout"].(bool); settled {
		b.WriteString("Settled after timeout: true — the execution outlived its timeout and was settled from " +
			"the target's state, without the operation's own result\n")
	}
	partial, present := result["partial"]
	if !present {
		return
	}
	switch partial {
	case true:
		line := "Partial: true — part of the change was committed before the failure"
		if p.DatasetID != nil {
			line += fmt.Sprintf("; see what moved: qctl get history --table-id %d", *p.DatasetID)
		}
		b.WriteString(line + "\n")
	case false:
		b.WriteString("Partial: false\n")
	default:
		fmt.Fprintf(b, "Partial: %v — whether any of the change was committed is not known\n", partial)
	}
}

// proposalActionLabels label each allowed action's NEXT ACTIONS line.
var proposalActionLabels = map[api.ChangeProposalAction]string{
	api.ChangeProposalActionEdit:           "edit:           ",
	api.ChangeProposalActionSubmit:         "submit:         ",
	api.ChangeProposalActionWithdraw:       "withdraw:       ",
	api.ChangeProposalActionApprove:        "approve:        ",
	api.ChangeProposalActionReject:         "reject:         ",
	api.ChangeProposalActionRequestChanges: "request changes:",
	api.ChangeProposalActionRerunDryRun:    "rerun dry run:  ",
}

// renderProposalNextActions writes one command per allowed action, in the
// server's order. mapped reports whether PAYLOAD shows a manifest.
func renderProposalNextActions(b *strings.Builder, p *change_proposals.Proposal, mapped bool) {
	b.WriteString("\n## NEXT ACTIONS\n")
	if len(p.AllowedActions) == 0 {
		b.WriteString("(none — you may take no step on this proposal in its current state)\n")
		return
	}
	for _, action := range p.AllowedActions {
		command, ok := change_proposals.NextCommand(action, p)
		label, labelled := proposalActionLabels[action]
		if !ok || !labelled {
			fmt.Fprintf(b, "- %s: (qctl has no command for this action)\n", action)
			continue
		}
		switch {
		case action == api.ChangeProposalActionEdit && p.Operation == api.ChangeReviewOperationDatasetRulePatch:
			command += " (or --unset-param <key>), merged into the PAYLOAD params above"
		case action == api.ChangeProposalActionEdit && mapped:
			command = "save the PAYLOAD manifest above, change it, then " + command
		case action == api.ChangeProposalActionEdit:
			command = "write the changed payload as a resolution manifest, then " + command
		case action == api.ChangeProposalActionApprove:
			command += " (runs the payload as you, the approver)"
		}
		fmt.Fprintf(b, "- %s %s\n", label, command)
	}
}

func issueRefs(kind string, ids []int) string {
	if len(ids) == 0 {
		return noneListed
	}
	refs := make([]string, len(ids))
	for i, id := range ids {
		refs[i] = fmt.Sprintf("%s/%d", kind, id)
	}
	return strings.Join(refs, ", ")
}

// authors names each of ids, followed by the CLI sessions (delegations) that
// withAuthors lists for that user, e.g. "Alice (via session ci-bot)". A
// session with no name is shown by its id.
func authors(d *comments.Directory, ids []uuid.UUID, withAuthors []change_proposals.Author) string {
	if len(ids) == 0 {
		return noneListed
	}
	delegations := make(map[uuid.UUID][]api.DelegationSummarySchema, len(withAuthors))
	for _, a := range withAuthors {
		delegations[a.UserID] = a.Delegations
	}
	names := make([]string, len(ids))
	for i, id := range ids {
		names[i] = d.DisplayName(id)
		if via := delegations[id]; len(via) > 0 {
			sessions := make([]string, len(via))
			for j, s := range via {
				sessions[j] = s.ID.String()
				if s.Name != nil && *s.Name != "" {
					sessions[j] = *s.Name
				}
			}
			noun := "session"
			if len(via) > 1 {
				noun = "sessions"
			}
			names[i] += fmt.Sprintf(" (via %s %s)", noun, strings.Join(sessions, ", "))
		}
	}
	return strings.Join(names, ", ")
}

func policyKeys(keys [][]string) string {
	if len(keys) == 0 {
		return noneListed
	}
	out := make([]string, len(keys))
	for i, key := range keys {
		out[i] = strings.Join(key, "/")
	}
	return strings.Join(out, ", ")
}

// flatMap renders a map as "k: v, k: v" in key order.
func flatMap(m map[string]interface{}) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s: %v", k, m[k])
	}
	return strings.Join(parts, ", ")
}

func proposalTime(t time.Time) string {
	return fmt.Sprintf("%s (%s)", t.UTC().Format(time.RFC3339), timeutil.FormatRelative(t))
}

func optionalTime(t *time.Time, absent string) string {
	if t == nil {
		return absent
	}
	return proposalTime(*t)
}
