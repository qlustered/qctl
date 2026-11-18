package deliverables

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
)

// ColumnSuggestionsResult is the accepted mappings and their original response body.
type ColumnSuggestionsResult struct {
	*api.ApplyColumnSuggestionsResponse
	EmptyColumns []EmptyColumn   `json:"empty_columns"`
	Body         json.RawMessage `json:"-"`
}

// EmptyColumn is an unbound template column with an optional matching hint.
type EmptyColumn struct {
	TargetColumnIndex int                          `json:"target_column_index"`
	HeaderLabel       string                       `json:"header_label"`
	Candidate         *api.ColumnCandidateResponse `json:"candidate"`
}

// AcceptColumnSuggestions saves a sheet's confident column matches.
func (c *Client) AcceptColumnSuggestions(token string, sheetBindingID uuid.UUID) (*ColumnSuggestionsResult, error) {
	apiClient, err := c.newAPIClient(token)
	if err != nil {
		return nil, err
	}
	const failed = "accept deliverable column suggestions failed"
	resp, err := apiClient.API.ApplyDeliverableColumnSuggestionsWithResponse(context.Background(), c.orgID, sheetBindingID, &api.ApplyDeliverableColumnSuggestionsParams{})
	if err != nil {
		return nil, wrapAPIError(apiClient, err, failed, "apply_deliverable_column_suggestions")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, failed, "apply_deliverable_column_suggestions")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty column suggestions response")
	}
	var result *ColumnSuggestionsResult
	if err := jsonnum.Unmarshal(resp.Body, &result); err != nil {
		return nil, fmt.Errorf("decode column suggestions response: %w", err)
	}
	if result == nil || result.ApplyColumnSuggestionsResponse == nil {
		return nil, fmt.Errorf("unexpected empty column suggestions response")
	}
	result.Body = resp.Body
	return result, nil
}

// ResolveDraftTarget selects an explicit revision or the deliverable's only draft.
func (c *Client) ResolveDraftTarget(token string, deliverableID uuid.UUID, release, revision string) (Revision, error) {
	target, _, err := c.resolveTarget(token, deliverableID, release, revision,
		"create a draft revision before editing its bindings")
	return target, err
}

// FormatColumnSuggestions renders counts, current mappings and empty-column hints.
func FormatColumnSuggestions(result *ColumnSuggestionsResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Created: %d\nReplaced: %d (removed suggested bindings)\n", result.CreatedCount, result.ReplacedCount)
	columns := slices.Clone(result.ColumnBindings)
	slices.SortFunc(columns, func(a, b api.SuggestedColumnBindingResponse) int {
		return cmp.Compare(a.TargetColumnIndex, b.TargetColumnIndex)
	})
	fmt.Fprintf(&b, "\nCurrent column bindings (%d):\n", len(columns))
	for _, column := range columns {
		source := sourceText(column.SourceType, column.SourceColumn, column.RunParam, column.LiteralValue)
		fmt.Fprintf(&b, "  Column %d (%s) <- %s [%s]\n", column.TargetColumnIndex, ColumnLetter(column.TargetColumnIndex), cellText(source), column.ID)
		fmt.Fprintf(&b, "    Label: %s; origin: %s; match: %s; needs confirmation: %t; format: %s\n",
			cellText(pointerText(column.TargetLabel)), cellText(string(column.Origin)), cellText(pointerText(column.MatchReason)),
			column.NeedsConfirmation, cellText(pointerText(column.FormatOverride)))
		for _, warning := range copied(column.Warnings) {
			fmt.Fprintf(&b, "    Warning: %s\n", cellText(FormatIssue(warning)))
		}
	}
	empty := slices.Clone(result.EmptyColumns)
	slices.SortFunc(empty, func(a, b EmptyColumn) int {
		return cmp.Compare(a.TargetColumnIndex, b.TargetColumnIndex)
	})
	fmt.Fprintf(&b, "\nEmpty columns (%d):\n", len(empty))
	for _, column := range empty {
		fmt.Fprintf(&b, "  Column %d (%s): %s", column.TargetColumnIndex, ColumnLetter(column.TargetColumnIndex), cellText(column.HeaderLabel))
		if candidate := column.Candidate; candidate != nil {
			fmt.Fprintf(&b, "; hint: %s (%s, score %g; not applied)", cellText(candidate.SourceColumn), cellText(string(candidate.MatchReason)), candidate.Score)
		}
		fmt.Fprintln(&b)
	}
	return b.String()
}
