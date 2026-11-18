package change_proposal //nolint:revive // Package name mirrors the hyphenated command.

import (
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/change_proposals"
)

// TestApprovePreviewShowsTheNote: the approver reads the proposer's note
// before deciding.
func TestApprovePreviewShowsTheNote(t *testing.T) {
	p := change_proposals.Proposal{ID: [16]byte{1}, State: api.ChangeProposalStatePendingReview, VersionID: 3}
	note := "Broker confirmed the code"
	p.Note = &note
	if got := approvePreview(&change_proposals.Detail{Proposal: p}); !strings.Contains(got, "Proposer note: \"Broker confirmed the code\"\n") {
		t.Errorf("preview missing the note:\n%s", got)
	}
	p.Note = nil
	if got := approvePreview(&change_proposals.Detail{Proposal: p}); !strings.Contains(got, "Proposer note: (none)\n") {
		t.Errorf("preview missing the empty note line:\n%s", got)
	}
}
