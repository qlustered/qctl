package deliverables

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

// ConfirmCellsResult holds the selected cell bindings in reference order.
type ConfirmCellsResult struct {
	Cells []ConfirmedCell
}

// ConfirmedCell holds a saved or already confirmed binding.
type ConfirmedCell struct {
	Binding   api.CellBindingResponse
	Confirmed bool
}

// Bindings returns the selected bindings in reference order.
func (r *ConfirmCellsResult) Bindings() []api.CellBindingResponse {
	bindings := make([]api.CellBindingResponse, 0, len(r.Cells))
	for _, cell := range r.Cells {
		bindings = append(bindings, cell.Binding)
	}
	return bindings
}

// ParseCellList normalizes trimmed A1 references and rejects an empty list or duplicates.
func ParseCellList(keys []string) ([]string, error) {
	if len(keys) == 0 {
		return nil, qerrors.NewBadUsage("--cell is required: name a cell by A1 reference")
	}
	refs := make([]string, 0, len(keys))
	seen := make(map[string]bool, len(keys))
	for _, key := range keys {
		ref, err := NormalizeCellRef(strings.TrimSpace(key))
		if err != nil {
			return nil, err
		}
		if seen[ref] {
			return nil, qerrors.NewBadUsagef("--cell names cell %s twice", ref)
		}
		seen[ref] = true
		refs = append(refs, ref)
	}
	return refs, nil
}

// ConfirmCells re-saves the selected flagged bindings with their stored upsert fields.
func (c *Client) ConfirmCells(token string, tree *api.DeliverableRevisionResponse, sheet string, refs []string) (*ConfirmCellsResult, error) {
	bound, err := cellsByKey(tree)
	if err != nil {
		return nil, err
	}
	refs = slices.Compact(slices.Sorted(slices.Values(refs)))
	if err := requireBoundCells(sheet, bound, refs); err != nil {
		return nil, err
	}
	result := &ConfirmCellsResult{}
	var confirmed []string
	for _, ref := range refs {
		binding := bound[sheet+"!"+ref]
		if !binding.NeedsConfirmation {
			result.Cells = append(result.Cells, ConfirmedCell{Binding: binding})
			continue
		}
		saved, err := c.PatchCellBinding(token, tree.ID, binding.ID, confirmCellBody(binding))
		if err != nil {
			err = fmt.Errorf("confirm %s: %w", cellLabel(sheet, ref), err)
			if len(confirmed) > 0 {
				err = fmt.Errorf("%w\n\nConfirmed before the failure: %s", err, strings.Join(confirmed, ", "))
			}
			return nil, err
		}
		confirmed = append(confirmed, cellText(sheet+"!"+ref))
		result.Cells = append(result.Cells, ConfirmedCell{Binding: *saved, Confirmed: true})
	}
	return result, nil
}

func requireBoundCells(sheet string, bound map[string]api.CellBindingResponse, refs []string) error {
	var missing []string
	for _, ref := range refs {
		if _, ok := bound[sheet+"!"+ref]; !ok {
			missing = append(missing, ref)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	var have []string
	for _, key := range slices.Sorted(maps.Keys(bound)) {
		if bound[key].SheetName == sheet {
			ref, _ := NormalizeCellRef(bound[key].CellRef)
			have = append(have, ref)
		}
	}
	return qerrors.NewBadUsagef("cells %s are not bound on sheet %q (bound: %s)",
		strings.Join(missing, ", "), cellText(sheet), dash(strings.Join(have, ", ")))
}

func confirmCellBody(binding api.CellBindingResponse) map[string]any {
	var filter *api.DescribedRowFilter
	if binding.RowFilter != nil {
		filter = preservedCellFilter(binding.RowFilter)
	}
	return map[string]any{
		wireSheetName: binding.SheetName, wireCellRef: binding.CellRef, wireSourceType: binding.SourceType,
		wireRunParam: binding.RunParam, wireLiteralValue: binding.LiteralValue, wireFormatOverride: binding.FormatOverride,
		wireAggregateTable: binding.AggregateMemberDatasetID, wireAggregateSeries: binding.AggregateMemberSeriesID,
		wireAggregateFunc: binding.AggregateFunc, wireAggregateColumn: binding.AggregateColumn,
		wireSameRowsAs: binding.SameRowsAsSheetBindingID, wireLabelCellRef: binding.LabelCellRef, keyRowFilter: filter,
	}
}

// FormatConfirmCells renders each selected cell's confirmation outcome.
func FormatConfirmCells(result *ConfirmCellsResult) string {
	var b strings.Builder
	for _, cell := range result.Cells {
		state := "already confirmed"
		if cell.Confirmed {
			state = "confirmed"
		}
		fmt.Fprintf(&b, "  Cell %s!%s: %s\n", cellText(cell.Binding.SheetName), cellText(cell.Binding.CellRef), state)
	}
	return b.String()
}
