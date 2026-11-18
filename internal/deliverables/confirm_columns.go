package deliverables

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/terminal"
)

// ConfirmColumnsResult is the named column bindings of one sheet binding, in
// column order.
type ConfirmColumnsResult struct {
	Columns []ConfirmedColumn
}

// ConfirmedColumn is one named column binding: the PATCH response when
// Confirmed, else the binding the sheet binding held.
type ConfirmedColumn struct {
	Binding   api.ColumnBindingResponse
	Confirmed bool
}

// Bindings returns the Binding of each of r.Columns, in order.
func (r *ConfirmColumnsResult) Bindings() []api.ColumnBindingResponse {
	bindings := make([]api.ColumnBindingResponse, 0, len(r.Columns))
	for _, column := range r.Columns {
		bindings = append(bindings, column.Binding)
	}
	return bindings
}

// ParseColumnList returns the 1-based index of each key, in order, as
// ParseColumnKey reads it after trimming surrounding spaces. An empty list, a
// key ParseColumnKey refuses, or two keys naming one column, is bad usage.
func ParseColumnList(keys []string) ([]int, error) {
	if len(keys) == 0 {
		return nil, qerrors.NewBadUsage("--column is required: name a column by letter or 1-based index")
	}
	indexes := make([]int, 0, len(keys))
	seen := make(map[int]string, len(keys))
	for _, key := range keys {
		key = strings.TrimSpace(key)
		index, err := ParseColumnKey(key)
		if err != nil {
			return nil, err
		}
		if first, ok := seen[index]; ok {
			return nil, qerrors.NewBadUsagef("--column names column %s twice (%q and %q)", ColumnLetter(index), first, key)
		}
		seen[index] = key
		indexes = append(indexes, index)
	}
	return indexes, nil
}

// ConfirmColumns re-saves selected flagged column bindings with their stored upsert fields.
func (c *Client) ConfirmColumns(token string, sheet api.SheetBindingResponse, indexes []int) (*ConfirmColumnsResult, error) {
	bound := make(map[int]api.ColumnBindingResponse)
	for _, binding := range copied(sheet.ColumnBindings) {
		bound[binding.TargetColumnIndex] = binding
	}
	indexes = slices.Compact(slices.Sorted(slices.Values(indexes)))
	if err := requireBound(sheet.SheetName, bound, indexes); err != nil {
		return nil, err
	}
	result := &ConfirmColumnsResult{}
	var confirmed []string
	for _, index := range indexes {
		binding := bound[index]
		if !binding.NeedsConfirmation {
			result.Columns = append(result.Columns, ConfirmedColumn{Binding: binding})
			continue
		}
		saved, err := c.PatchColumnBinding(token, sheet.ID, binding.ID, confirmBody(binding))
		if err != nil {
			err = fmt.Errorf("confirm column %s: %w", ColumnLetter(index), err)
			if len(confirmed) > 0 {
				err = fmt.Errorf("%w\n\nConfirmed before the failure: %s", err, strings.Join(confirmed, ", "))
			}
			return nil, err
		}
		confirmed = append(confirmed, ColumnLetter(index))
		result.Columns = append(result.Columns, ConfirmedColumn{Binding: *saved, Confirmed: true})
	}
	return result, nil
}

// requireBound is bad usage when bound lacks any of indexes, naming those and
// bound's own indexes as column letters, and nil otherwise.
func requireBound(sheetName string, bound map[int]api.ColumnBindingResponse, indexes []int) error {
	var missing []string
	for _, index := range indexes {
		if _, ok := bound[index]; !ok {
			missing = append(missing, ColumnLetter(index))
		}
	}
	if len(missing) == 0 {
		return nil
	}
	subject := fmt.Sprintf("column %s is", missing[0])
	if len(missing) > 1 {
		subject = fmt.Sprintf("columns %s are", strings.Join(missing, ", "))
	}
	have := "it has no column bindings"
	if len(bound) > 0 {
		letters := make([]string, 0, len(bound))
		for _, index := range slices.Sorted(maps.Keys(bound)) {
			letters = append(letters, ColumnLetter(index))
		}
		have = "bound: " + strings.Join(letters, ", ")
	}
	return qerrors.NewBadUsagef("%s not bound on sheet %q (%s)", subject, terminal.Sanitize(sheetName), have)
}

// confirmBody is binding's whole upsert body, null where binding holds null.
func confirmBody(binding api.ColumnBindingResponse) map[string]any {
	body := map[string]any{
		wireTargetColumnIndex: binding.TargetColumnIndex,
		wireSourceType:        binding.SourceType,
		wireSourceColumn:      binding.SourceColumn,
		wireRunParam:          binding.RunParam,
		wireFormatOverride:    binding.FormatOverride,
	}
	if binding.SourceType == api.DeliverableColumnSourceTypeLiteral {
		body[wireLiteralValue] = binding.LiteralValue
	}
	return body
}

// FormatConfirmColumns renders one line per column of result, saying whether
// it was confirmed or already confirmed, and each of its binding's warnings.
func FormatConfirmColumns(result *ConfirmColumnsResult) string {
	var b strings.Builder
	for _, column := range result.Columns {
		binding := column.Binding
		state := "already confirmed"
		if column.Confirmed {
			state = "confirmed"
		}
		fmt.Fprintf(&b, "  Column %d (%s) <- %s: %s\n", binding.TargetColumnIndex, ColumnLetter(binding.TargetColumnIndex),
			cellText(sourceText(binding.SourceType, binding.SourceColumn, binding.RunParam, binding.LiteralValue)), state)
		for _, warning := range copied(binding.Warnings) {
			fmt.Fprintf(&b, "    Warning: %s\n", cellText(FormatIssue(warning)))
		}
	}
	return b.String()
}

// sourceText names a column binding's source.
func sourceText(sourceType api.DeliverableColumnSourceType, sourceColumn, runParam, literalValue *string) string {
	if sourceType == api.DeliverableColumnSourceTypeLiteral {
		return "literal " + strconv.Quote(derefText(literalValue))
	}
	if sourceType == api.DeliverableColumnSourceTypeRunParam {
		return "run parameter " + pointerText(runParam)
	}
	return "column " + pointerText(sourceColumn)
}
