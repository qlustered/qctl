package apply

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/rule_merges"
	"github.com/qlustered/qctl/internal/terminal"
)

// lineageCandidates is the template key of each kept item of the preview
// that has no reason and pairs with a table rule.
func (t *templateApply) lineageCandidates() []string {
	var keys []string
	for _, item := range t.plan.Kept {
		if item.Reason == nil && item.TargetObjectID != nil && *item.TargetObjectID != "" {
			keys = append(keys, oneLine(templateItemKey(item)))
		}
	}
	return keys
}

// editedOnTable is the template key of each kept item of the preview whose
// reason is edited_on_table.
func (t *templateApply) editedOnTable() []string {
	var keys []string
	for _, item := range t.plan.Kept {
		if item.Reason != nil && *item.Reason == api.RuleMergeItemReasonEditedOnTable {
			keys = append(keys, oneLine(templateItemKey(item)))
		}
	}
	return keys
}

// recordLineage ends an apply that nothingToApply stops. When
// lineageCandidates names an item, it applies the preview with
// t.resolutions without asking, returning the applied merge. It writes a note
// and returns nothingToDo's result instead when editedOnTable names an item,
// applyBlockers lists a blocker, or the server refuses the apply with 403 or
// 409.
func (t *templateApply) recordLineage() (*applyResult, error) {
	keys := t.lineageCandidates()
	if len(keys) == 0 {
		return t.nothingToDo()
	}
	if edited := t.editedOnTable(); len(edited) > 0 {
		fmt.Fprintf(t.human, "\nLineage not recorded: %s %s edited on the table, and applying this merge would record "+
			"the edit as the template's base, so the next change to the entry would replace the rule and undo the edit. "+
			"A kept entry the template only adopted gets no lineage from this run, and a change to it plans conflict "+
			"adopted_differs (resolve it with take_source in --resolutions).\n",
			terminal.Sanitize(strings.Join(edited, ", ")), pluralWord(len(edited), "was", "were"))
		return t.nothingToDo()
	}
	if blockers := t.applyBlockers(); len(blockers) > 0 {
		fmt.Fprintf(t.human, "\nLineage not recorded: %s\n", terminal.Sanitize(blockerReason(blockers, t.preview.TableID)))
		return t.nothingToDo()
	}

	applied, body, err := t.preview.Client.Apply(
		t.ctx.Credential.AccessToken, t.preview.TableID, t.preview.Merge.ID, t.resolutions)
	if err != nil {
		var apiErr *rule_merges.APIError
		if errors.As(err, &apiErr) &&
			(apiErr.StatusCode == http.StatusForbidden || apiErr.StatusCode == http.StatusConflict) {
			fmt.Fprintf(t.human, "\nLineage not recorded: the server refused the apply.\n%s\n",
				terminal.Sanitize(t.explainApplyError(err).Error()))
			return t.nothingToDo()
		}
		return nil, err
	}
	switch applied.State {
	case api.DatasetCopyStateApplied, api.DatasetCopyStatePartiallyApplied:
		fmt.Fprintf(t.human, "\nRecorded the template's lineage for %s (rule merge %d: %s): a later change to %s "+
			"plans replaced, not conflict adopted_differs.\n", terminal.Sanitize(strings.Join(keys, ", ")), applied.ID,
			terminal.Sanitize(string(applied.State)), pluralWord(len(keys), "its entry", "their entries"))
	default:
		fmt.Fprintf(t.human, "\nLineage not recorded yet: rule merge %d is %s. Next: qctl describe rule-merge %d --table-id %d\n",
			applied.ID, terminal.Sanitize(string(applied.State)), applied.ID, t.preview.TableID)
	}
	return mergeResult(applied.ID, body), nil
}
