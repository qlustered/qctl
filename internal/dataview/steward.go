package dataview

import "github.com/qlustered/qctl/internal/api"

// displacedFieldName is the steward_overrides marker key holding the value an
// active steward overwrite replaced. The marker's other key, target_value, is
// the live cell value the row already carries, so it is not read.
const displacedFieldName = "base_displaced_value"

// stewardOverridesAt returns row rowIdx's steward_overrides marker, keyed by
// the response's field names. It is nil when the page carries no markers, the
// row's entry is null, or the list is shorter than the page.
func stewardOverridesAt(markers *[]map[string]map[string]interface{}, rowIdx int) map[string]map[string]interface{} {
	if markers == nil || rowIdx >= len(*markers) {
		return nil
	}
	return (*markers)[rowIdx]
}

// stewardOverride builds one cell's marker. The displaced value is formatted
// as a converted cell of its column would be, so a percent column reads
// 12.50% → 13.00% rather than 0.125 → 13.00% — except text on a percent
// column, which prints as sent. The row log records a converted percent as a
// JSON number; text there is source or steward input in unknown units, and
// scaling it by 100 would print 1500 as 150000.00%. The zero format prints the
// value as sent.
func stewardOverride(field string, pin map[string]interface{}, format api.DisplayFormat) *StewardOverride {
	override := &StewardOverride{Field: escapeCell(field)}
	displaced := pin[displacedFieldName]
	if displaced == nil {
		return override
	}
	override.HasDisplaced = true
	if _, text := displaced.(string); text && format.Kind == api.DisplayFormatKindPercent {
		override.Displaced = formatCell(displaced)
	} else {
		override.Displaced = formatDisplayCell(displaced, format, false)
	}
	return override
}
