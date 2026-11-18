package resolveexplain

import (
	"fmt"
	"strings"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/rule_suggestions"
)

// StagedKeyword is a keyword change that a resolve staged as a rule
// suggestion instead of applying it.
type StagedKeyword struct {
	SuggestionID string
	DatasetID    int
	// Candidate reports whether the suggestion is a revalidation candidate;
	// nil when the suggestion could not be read.
	Candidate *bool
	// ReadErr is why the suggestion could not be read.
	ReadErr error
}

// Staged returns the keyword change a resolve on table datasetID staged as
// suggestion id, or nil when id is nil. It reads the suggestion to tell a
// revalidation candidate from a draft-migration suggestion, and keeps a failed
// read in ReadErr rather than returning it.
func Staged(ctx *cmdutil.CommandContext, datasetID int, id *openapi_types.UUID) *StagedKeyword {
	if id == nil {
		return nil
	}
	staged := &StagedKeyword{SuggestionID: id.String(), DatasetID: datasetID}
	if ctx == nil || ctx.Credential == nil {
		staged.ReadErr = fmt.Errorf("no credential to read it with")
		return staged
	}
	client, err := rule_suggestions.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		staged.ReadErr = err
		return staged
	}
	suggestion, err := client.Get(ctx.Credential.AccessToken, datasetID, staged.SuggestionID)
	if err != nil {
		staged.ReadErr = err
		return staged
	}
	candidate := suggestion.IsRevalidationCandidate != nil && *suggestion.IsRevalidationCandidate
	staged.Candidate = &candidate
	return staged
}

// Report is the outcome of the resolve of noun issueID that staged s, ending
// in a newline. resolved is the issue's state on the re-read after the
// resolve, and readErr that read's failure; rerun is the command that sends
// the resolve again.
func (s *StagedKeyword) Report(noun string, issueID int, resolved bool, readErr error, rerun string) string {
	var b strings.Builder
	switch {
	case readErr != nil:
		fmt.Fprintf(&b, "%s %d — keyword change staged as rule suggestion %s\n", noun, issueID, s.SuggestionID)
		fmt.Fprintf(&b, "  (could not re-read the %s to confirm the outcome: %v)\n", noun, readErr)
	case resolved:
		fmt.Fprintf(&b, "%s %d resolved; its keyword change was staged as rule suggestion %s, not applied\n",
			noun, issueID, s.SuggestionID)
		b.WriteString(s.decision())
		return b.String()
	default:
		fmt.Fprintf(&b, "%s %d — keyword change staged as rule suggestion %s; the %s stays open\n",
			noun, issueID, s.SuggestionID, noun)
	}
	b.WriteString(s.decision())
	b.WriteString(s.finish(noun, issueID, rerun))
	return b.String()
}

// decision is the indented lines naming what decides the suggestion and the
// command that acts on it or follows it.
func (s *StagedKeyword) decision() string {
	var b strings.Builder
	describe := fmt.Sprintf("qctl describe rule-suggestion %s --table-id %d", s.SuggestionID, s.DatasetID)
	switch {
	case s.Candidate == nil:
		reason := strings.Join(strings.Fields(fmt.Sprint(s.ReadErr)), " ")
		fmt.Fprintf(&b, "  (could not read the suggestion: %s)\n", reason)
		fmt.Fprintf(&b, "  See what decides it with:\n    %s\n", describe)
	case *s.Candidate:
		b.WriteString("  It is a revalidation candidate: the rule's change has to pass a validation run,\n")
		b.WriteString("  which decides it. It cannot be accepted. Follow it with:\n")
		fmt.Fprintf(&b, "    %s\n", describe)
	default:
		b.WriteString("  It waits on the table's draft migration. Accept it, or apply the migration,\n")
		b.WriteString("  which attempts to apply it too:\n")
		fmt.Fprintf(&b, "    qctl suggestion accept %s --table-id %d\n", s.SuggestionID, s.DatasetID)
	}
	return b.String()
}

// finish is the indented lines saying how to resolve noun issueID once the
// keyword is live, where rerun is the command that sends the resolve again.
func (s *StagedKeyword) finish(noun string, issueID int, rerun string) string {
	verb := "Accepting it"
	switch {
	case s.Candidate == nil:
		verb = "Accepting or applying it"
	case *s.Candidate:
		verb = "Applying it"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "  %s also resolves the %s when the keyword is the %s's own value\n", verb, noun, noun)
	b.WriteString("  and the applied rule still allows it. Once the keyword is live, re-read\n")
	fmt.Fprintf(&b, "  the %s and, if it is still open, resolve it again:\n", noun)
	fmt.Fprintf(&b, "    qctl describe %s %d\n", noun, issueID)
	fmt.Fprintf(&b, "    %s\n", rerun)
	return b.String()
}
