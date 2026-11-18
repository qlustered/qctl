package alerts

import (
	"fmt"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/output"
)

// ResolveObsoletes reports whether resolving an issue held by cp directly
// turns cp obsolete: cp is non-nil and neither executing nor terminal.
func ResolveObsoletes(cp *api.ChangeProposalSummarySchema) bool {
	if cp == nil {
		return false
	}
	switch cp.State {
	case api.ChangeProposalStatePendingReview, api.ChangeProposalStateChangesRequested,
		api.ChangeProposalStateDraft:
		return true
	default:
		return false
	}
}

// RenderHeldByPlain renders the HELD BY CHANGE PROPOSAL footer section for an
// issue of kind noun ("alert" or "warning"), as plain lines. Returns "" when
// cp is nil.
func RenderHeldByPlain(cp *api.ChangeProposalSummarySchema, noun string) string {
	if cp == nil {
		return ""
	}
	line := fmt.Sprintf("  %s (%s)", cp.ID, cp.State)
	if ResolveObsoletes(cp) {
		line += fmt.Sprintf(" — resolving this %s directly turns the proposal obsolete", noun)
	}
	return "\nHELD BY CHANGE PROPOSAL\n" + line + "\n" +
		fmt.Sprintf("Next: qctl describe change-proposal %s\n", cp.ID)
}

// RenderHeldBy is RenderHeldByPlain as a YAML comment block.
func RenderHeldBy(cp *api.ChangeProposalSummarySchema, noun string) string {
	return output.YAMLComment(RenderHeldByPlain(cp, noun))
}

// HeldByResolveWarning is the stderr warning a direct resolve of issue
// <noun>/<id> prints when ResolveObsoletes(cp), or "" otherwise.
func HeldByResolveWarning(cp *api.ChangeProposalSummarySchema, noun string, id int) string {
	if !ResolveObsoletes(cp) {
		return ""
	}
	return fmt.Sprintf("warning: %s %d is held by change proposal %s (%s); resolving it directly turns that proposal obsolete\n",
		noun, id, cp.ID, cp.State)
}
