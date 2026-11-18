package deliverables

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"text/tabwriter"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
)

// PreviewResult holds live sheet rows, resolved cells and the original response.
type PreviewResult struct {
	api.RevisionPreviewResponse
	Body json.RawMessage `json:"-"`
}

// Preview previews a revision with the supplied parameters and per-sheet row limit.
func (c *Client) Preview(token string, revisionID uuid.UUID, params RunParams, maxRows int) (*PreviewResult, error) {
	apiClient, err := c.newAPIClient(token)
	if err != nil {
		return nil, err
	}
	values := map[string]interface{}(params)
	resp, err := apiClient.API.PreviewDeliverableRevisionWithResponse(context.Background(), c.orgID, revisionID,
		&api.PreviewDeliverableRevisionParams{}, api.RevisionPreviewRequest{RunParams: &values, MaxRows: &maxRows})
	if err != nil {
		return nil, revisionInvalidError(wrapAPIError(apiClient, err, "preview deliverable revision failed", "preview_deliverable_revision"))
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, revisionInvalidError(handleError(resp.StatusCode(), resp.Body, "preview deliverable revision failed", "preview_deliverable_revision"))
	}
	var result PreviewResult
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty deliverable preview response")
	}
	if err := jsonnum.Unmarshal(resp.Body, &result.RevisionPreviewResponse); err != nil {
		return nil, fmt.Errorf("decode deliverable preview: %w", err)
	}
	result.Body = resp.Body
	return &result, nil
}

// WritePreviewCounts prints each sheet's kept, left out and blocked counts.
func WritePreviewCounts(w io.Writer, result *PreviewResult) {
	for _, sheet := range copied(result.Sheets) {
		writePreviewSheetCounts(w, sheet)
	}
}

func writePreviewSheetCounts(w io.Writer, sheet api.RevisionPreviewSheet) {
	fmt.Fprintf(w, "Sheet %s (table %d): %d kept / %d left out / %d blocked; showing %d rows\n",
		cellText(sheet.SheetName), sheet.MemberDatasetID, sheet.InScopeCount,
		sheet.OutOfScopeCount, sheet.IndeterminateCount, len(sheet.Rows))
}

// FormatPreview renders sheet rows and resolved cells, naming each sheet's
// masked fields on a "Masked fields:" line above its rows and each masked
// cell's in a "(masked: …)" suffix.
func FormatPreview(result *PreviewResult) string {
	var b strings.Builder
	for _, sheet := range copied(result.Sheets) {
		writePreviewSheetCounts(&b, sheet)
		if len(sheet.Rows) == 0 {
			fmt.Fprintln(&b, "  (no preview rows)")
			continue
		}
		if fields := maskedFieldsText(sheet.MaskedFields); fields != "" {
			fmt.Fprintln(&b, "Masked fields: "+fields)
		}
		w := tabwriter.NewWriter(&b, 0, 4, 2, ' ', 0)
		for i, column := range sheet.Columns {
			if i > 0 {
				fmt.Fprint(w, "\t")
			}
			fmt.Fprint(w, cellText(column))
		}
		fmt.Fprintln(w)
		for _, row := range sheet.Rows {
			for i, column := range sheet.Columns {
				if i > 0 {
					fmt.Fprint(w, "\t")
				}
				fmt.Fprint(w, previewValue(row[column]))
			}
			fmt.Fprintln(w)
		}
		_ = w.Flush()
		fmt.Fprintln(&b)
	}
	cells := copied(result.Cells)
	if len(cells) > 0 {
		fmt.Fprintln(&b, "Cells:")
		for _, cell := range cells {
			var value interface{}
			if cell.Value != nil {
				value = *cell.Value
			}
			suffix := ""
			if fields := maskedFieldsText(cell.MaskedFields); fields != "" {
				suffix = " (masked: " + fields + ")"
			}
			fmt.Fprintf(&b, "  %s!%s = %s%s\n", cellText(cell.SheetName), cellText(cell.CellRef), previewValue(value), suffix)
		}
	}
	if len(copied(result.Sheets)) == 0 && len(cells) == 0 {
		fmt.Fprintln(&b, "No preview sheets or cells.")
	}
	return b.String()
}

func previewValue(value interface{}) string {
	if value == nil {
		return nullText
	}
	return cellText(fmt.Sprint(value))
}

// maskedFieldsText joins fields, each made terminal-safe, with ", ".
func maskedFieldsText(fields *[]string) string {
	names := copied(fields)
	for i, name := range names {
		names[i] = cellText(name)
	}
	return strings.Join(names, ", ")
}
