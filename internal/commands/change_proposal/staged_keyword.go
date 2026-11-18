package change_proposal //nolint:revive // Package name mirrors the hyphenated command.

import (
	"fmt"
	"io"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/alerts"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/change_proposals"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/commands/resolveexplain"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/qlustered/qctl/internal/warnings"
)

// writeStagedKeyword reports a keyword change staged by an approved resolution.
func writeStagedKeyword(out io.Writer, ctx *cmdutil.CommandContext, p *change_proposals.Proposal) {
	if p.State != api.ChangeProposalStateExecuted || p.ExecutionResult == nil || p.DatasetID == nil {
		return
	}
	if writeRecordedStaging(out, p) {
		return
	}
	var noun string
	var issueID int
	switch {
	case p.Operation == api.ChangeReviewOperationAlertResolve && p.AlertItemID != nil:
		noun, issueID = "alert", *p.AlertItemID
	case p.Operation == api.ChangeReviewOperationWarningResolve && p.WarningItemID != nil:
		noun, issueID = "warning", *p.WarningItemID
	default:
		return
	}
	response, _ := (*p.ExecutionResult)["response"].(map[string]interface{})
	id, _ := response["dataset_rule_suggestion_id"].(string)
	suggestionID, err := uuid.Parse(id)
	if err != nil {
		return
	}
	staged := resolveexplain.Staged(ctx, *p.DatasetID, &suggestionID)
	token := ctx.Credential.AccessToken
	resolved := false
	if noun == "alert" {
		var issue *alerts.AlertFull
		issue, err = alerts.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity).GetAlert(token, issueID)
		resolved = err == nil && issue.Resolved != nil && *issue.Resolved
	} else {
		var issue *warnings.WarningFull
		issue, err = warnings.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity).GetWarning(token, issueID)
		resolved = err == nil && issue.Resolved != nil && *issue.Resolved
	}
	rerun := fmt.Sprintf("qctl %s resolve -f <file.yaml> # the same PAYLOAD shown above", noun)
	fmt.Fprint(out, terminal.Sanitize(staged.Report(noun, issueID, resolved, err, rerun)))
}

func writeRecordedStaging(out io.Writer, p *change_proposals.Proposal) bool {
	explanation := change_proposals.StagedAs(p)
	if explanation == "" || p.Operation == api.ChangeReviewOperationDatasetRulePatch {
		return false
	}
	fmt.Fprint(out, terminal.Sanitize(explanation))
	result := *p.ExecutionResult
	id, _ := result["dataset_rule_suggestion_id"].(string)
	if id == "" {
		response, _ := result["response"].(map[string]interface{})
		id, _ = response["dataset_rule_suggestion_id"].(string)
	}
	if suggestionID, err := uuid.Parse(id); err == nil {
		fmt.Fprintf(out, "Staged suggestion: %s — see it: qctl describe rule-suggestion %s --table-id %d\n",
			suggestionID, suggestionID, *p.DatasetID)
	}
	return true
}
