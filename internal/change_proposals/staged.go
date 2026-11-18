package change_proposals //nolint:revive // Package name mirrors the resource.

import "fmt"

const (
	applicationPending   = "pending"
	applicationApplied   = "applied"
	applicationDismissed = "dismissed"
)

// StagedAs explains the execution result's staging kind and application.
func StagedAs(p *Proposal) string {
	if p.ExecutionResult == nil {
		return ""
	}
	result := *p.ExecutionResult
	kind, _ := result["staged_as"].(string)
	if result["outcome"] != "staged" || kind == "" {
		return ""
	}
	var text string
	switch kind {
	case "draft_migration":
		switch result["application"] {
		case applicationApplied:
			text = "the suggestion was applied"
		case applicationDismissed:
			text = "the suggestion was dismissed or failed; the change was not applied"
		case applicationPending:
			text = "the change waits for the suggestion to be accepted or applied with the table's draft migration"
		}
	case "revalidation_candidate":
		switch result["application"] {
		case applicationApplied:
			text = "validation promoted the candidate and applied the change"
		case applicationDismissed:
			text = "the candidate was dismissed or failed; the change was not applied"
		case applicationPending:
			text = "validation must pass and promote the candidate to apply the change; it cannot be accepted"
		}
	default:
		return fmt.Sprintf("Staged as: %s\n", kind)
	}
	if text == "" {
		return fmt.Sprintf("Staged as: %s\n", kind)
	}
	return fmt.Sprintf("Staged as: %s — %s\n", kind, text)
}
