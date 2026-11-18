package deliverables

import (
	"errors"
	"strconv"
)

// bindingWriteError renders sheet and column save issues with manifest paths.
func (p *Plan) bindingWriteError(request Request, err error) error {
	switch request.step {
	case stepSheetCreate, stepSheetPatch, stepColumnCreate, stepColumnPatch, stepCellCreate, stepCellPatch:
	default:
		return err
	}
	var invalid *RevisionInvalidError
	if !errors.As(err, &invalid) || invalid.failed == "" {
		return err
	}
	formatted := *invalid
	formatted.err = err
	formatted.formatIssue = func(issue Issue) string {
		return p.formatBindingIssue(request, issue)
	}
	return &formatted
}

func (p *Plan) formatBindingIssue(request Request, issue Issue) string {
	loc := copied(issue.Loc)
	if len(loc) < 2 || loc[0] != "body" {
		return FormatIssue(issue)
	}
	path, key := p.bindingIssuePath(request, loc[1:])
	if path == "" {
		return FormatIssue(issue)
	}
	if request.step == stepSheetPatch {
		sheet := p.desiredSheet(request.Sheet)
		if sheet != nil && !sheet.spec.Has(key) {
			switch key {
			case keySeries, keyInstructionRowCount, keyRowOrder:
				path += " (from stored binding; " + key + " omitted)"
			}
		}
	}
	return path + ": " + issue.Msg
}

func (p *Plan) bindingIssuePath(request Request, loc []string) (path, key string) {
	base := sheetKey(request.Sheet)
	if request.Column != 0 {
		return columnIssuePath(p.manifestColumnKey(request), loc), ""
	}
	if request.Cell != "" {
		return cellIssuePath(request.key, loc), ""
	}
	field := loc[0]
	switch field {
	case wireSheetName:
		if len(loc) == 1 {
			return base, ""
		}
	case wireMemberDatasetID, wireMemberSeriesID, keyDataStartRow, keyInstructionRowCount:
		if len(loc) == 1 {
			key = field
			switch field {
			case wireMemberDatasetID:
				key = keyTable
			case wireMemberSeriesID:
				key = keySeries
			}
			return base + "." + key, key
		}
	case keyHeaderRows, wireSortKeys:
		key = keyHeaderRows
		if field == wireSortKeys {
			key = keyRowOrder
		}
		return listIssuePath(base+"."+key, loc), key
	}
	return "", ""
}

func columnIssuePath(base string, loc []string) string {
	if len(loc) != 1 {
		return ""
	}
	switch loc[0] {
	case wireTargetColumnIndex, wireSourceType:
		return base
	case wireSourceColumn:
		return base + ".column"
	case wireLiteralValue:
		return base + ".literal"
	case wireRunParam:
		return base + "." + wireRunParam
	case wireFormatOverride:
		return base + ".format"
	}
	return ""
}

func cellIssuePath(base string, loc []string) string {
	if len(loc) != 1 {
		return ""
	}
	switch loc[0] {
	case wireLiteralValue:
		return base + ".literal"
	case wireRunParam:
		return base + "." + wireRunParam
	case wireFormatOverride:
		return base + ".format"
	case wireLabelCellRef:
		return base + ".label_cell"
	}
	return ""
}

func listIssuePath(base string, loc []string) string {
	if len(loc) == 1 {
		return base
	}
	index, err := strconv.Atoi(loc[1])
	if err != nil || index < 0 {
		return ""
	}
	path := base + "[" + strconv.Itoa(index) + "]"
	if len(loc) == 2 {
		return path
	}
	if loc[0] == wireSortKeys && len(loc) == 3 {
		switch loc[2] {
		case "column_name":
			return path + ".column"
		case "descending":
			return path + ".descending"
		case "position":
			return path
		}
	}
	return ""
}

func (p *Plan) manifestColumnKey(request Request) string {
	if sheet := p.desiredSheet(request.Sheet); sheet != nil {
		for key := range sheet.spec.Columns {
			if index, err := ParseColumnKey(key); err == nil && index == request.Column {
				return sheetKey(request.Sheet) + "." + keyColumns + "." + key
			}
		}
	}
	return columnKey(request.Sheet, request.Column)
}
