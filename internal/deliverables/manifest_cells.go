package deliverables

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/row_filters"
	"gopkg.in/yaml.v3"
)

const maxCellRow = 1048576

var a1Pattern = regexp.MustCompile(`^\$?([a-zA-Z]{1,3})\$?([0-9]+)$`)

// ManifestCell is one cell binding of spec.cells.
type ManifestCell struct {
	RunParam  *string        `yaml:"run_param,omitempty" json:"run_param,omitempty"`
	Literal   *string        `yaml:"literal,omitempty" json:"literal,omitempty"`
	Total     *ManifestTotal `yaml:"total,omitempty" json:"total,omitempty"`
	LabelCell *string        `yaml:"label_cell,omitempty" json:"label_cell,omitempty"`
	Format    *string        `yaml:"format,omitempty" json:"format,omitempty"`
}

// Keys of a spec.cells total that a spec.sheets entry does not share.
const (
	keySameRowsAs = "same_rows_as"
	keyFunc       = "func"
	keyColumn     = "column"
)

// ManifestTotal is an aggregate source for a cell binding.
type ManifestTotal struct {
	Table      string                       `yaml:"table,omitempty" json:"table,omitempty"`
	Series     *string                      `yaml:"series,omitempty" json:"series,omitempty"`
	SameRowsAs string                       `yaml:"same_rows_as,omitempty" json:"same_rows_as,omitempty"`
	Func       api.DeliverableAggregateFunc `yaml:"func" json:"func"`
	Column     *string                      `yaml:"column,omitempty" json:"column,omitempty"`
	RowFilter  *row_filters.Filter          `yaml:"row_filter,omitempty" json:"row_filter,omitempty"`
	present    map[string]bool
}

// Has reports whether the total's key was written, even as null: in the
// parsed document, or by CellsFromTree or SetCells.
func (t ManifestTotal) Has(key string) bool {
	return t.present[key]
}

// markWritten marks func, each of table and same_rows_as that is non-empty,
// and each of series, column and row_filter that is non-nil.
func (t ManifestTotal) markWritten() ManifestTotal {
	t.present = map[string]bool{keyFunc: true}
	for key, written := range map[string]bool{
		keyTable: t.Table != "", keySameRowsAs: t.SameRowsAs != "",
		keySeries: t.Series != nil, keyColumn: t.Column != nil, keyRowFilter: t.RowFilter != nil,
	} {
		if written {
			t.present[key] = true
		}
	}
	return t
}

// writtenTotalKeys returns the keys each spec.cells entry's total writes,
// null included, by cell key.
func writtenTotalKeys(data []byte) map[string]map[string]bool {
	var envelope struct {
		Spec struct {
			Cells map[string]struct {
				Total map[string]any `yaml:"total"`
			} `yaml:"cells"`
		} `yaml:"spec"`
	}
	_ = yaml.Unmarshal(data, &envelope)
	written := make(map[string]map[string]bool, len(envelope.Spec.Cells))
	for key, cell := range envelope.Spec.Cells {
		written[key] = map[string]bool{}
		for name := range cell.Total {
			written[key][name] = true
		}
	}
	return written
}

// NormalizeCellRef returns an A1 reference without absolute markers or leading
// row zeros, in upper case and within Excel's row and column limits.
func NormalizeCellRef(ref string) (string, error) {
	parts := a1Pattern.FindStringSubmatch(ref)
	if parts != nil {
		row, err := strconv.Atoi(parts[2])
		column, ok := columnIndex(parts[1])
		if err == nil && ok && row >= 1 && row <= maxCellRow {
			return ColumnLetter(column) + strconv.Itoa(row), nil
		}
	}
	return "", qerrors.NewBadUsagef("%q is not an A1 cell reference within A1:XFD%d", cellText(ref), maxCellRow)
}

func splitCellKey(key string) (string, string, error) {
	i := strings.LastIndexByte(key, '!')
	if i <= 0 {
		return "", "", qerrors.NewBadUsagef("%q must be Sheet!A1", cellText(key))
	}
	ref, err := NormalizeCellRef(key[i+1:])
	return key[:i], ref, err
}

func (m *Manifest) validateCells() error {
	if !m.Has(keyCells) {
		return nil
	}
	if m.Spec.Cells == nil {
		return qerrors.NewBadUsage("spec.cells must be a mapping; use {} for no cells")
	}
	normalized := make(map[string]ManifestCell, len(m.Spec.Cells))
	seen := map[string]string{}
	for _, key := range slices.Sorted(maps.Keys(m.Spec.Cells)) {
		sheet, ref, err := splitCellKey(key)
		if err != nil {
			return fmt.Errorf("spec.cells.%s: %w", cellText(key), err)
		}
		canonical := sheet + "!" + ref
		if other, ok := seen[canonical]; ok {
			return qerrors.NewBadUsagef("spec.cells: %q and %q both name %s", cellText(other), cellText(key), cellText(canonical))
		}
		cell := m.Spec.Cells[key]
		if err := cell.validate(ref); err != nil {
			return fmt.Errorf("spec.cells.%s: %w", cellText(key), err)
		}
		seen[canonical], normalized[canonical] = key, cell
	}
	m.Spec.Cells = normalized
	return nil
}

func (c *ManifestCell) validate(ref string) error {
	sources := 0
	for _, present := range []bool{c.RunParam != nil, c.Literal != nil, c.Total != nil} {
		if present {
			sources++
		}
	}
	if sources != 1 {
		return qerrors.NewBadUsage("set exactly one of run_param, literal or total")
	}
	if c.RunParam != nil && isBlank(*c.RunParam) {
		return qerrors.NewBadUsage("run_param must be non-blank")
	}
	if c.Literal != nil {
		if err := validateLiteral(*c.Literal); err != nil {
			return err
		}
	}
	if c.LabelCell != nil {
		label, err := NormalizeCellRef(*c.LabelCell)
		if err != nil {
			return fmt.Errorf("label_cell: %w", err)
		}
		if label == ref {
			return qerrors.NewBadUsage("label_cell must differ from the bound cell")
		}
		c.LabelCell = &label
	}
	if c.Total != nil {
		return c.Total.validate()
	}
	return nil
}

func (t ManifestTotal) validate() error {
	if err := t.validateSource(); err != nil {
		return err
	}
	if err := t.validateFunction(); err != nil {
		return err
	}
	if t.RowFilter != nil && t.SameRowsAs != "" {
		return qerrors.NewBadUsage("total.row_filter excludes total.same_rows_as, which runs over the sheet's rows")
	}
	return t.RowFilter.Validate(true, "total.row_filter")
}

func (t ManifestTotal) validateSource() error {
	if t.SameRowsAs != "" {
		if isBlank(t.SameRowsAs) || t.Table != "" || t.Series != nil {
			return qerrors.NewBadUsage("total.same_rows_as names a bound sheet and excludes table and series")
		}
	} else {
		if isBlank(t.Table) {
			return qerrors.NewBadUsage("total.table or total.same_rows_as is required; series also requires a reference table")
		}
		if id, err := strconv.Atoi(t.Table); err == nil && id <= 0 {
			return qerrors.NewBadUsage("total.table: a table ID must be positive")
		}
		if t.Series != nil && isBlank(*t.Series) {
			return qerrors.NewBadUsage("total.series must be non-blank")
		}
	}
	return nil
}

func (t ManifestTotal) validateFunction() error {
	switch t.Func {
	case api.DeliverableAggregateFuncSum, api.DeliverableAggregateFuncMin, api.DeliverableAggregateFuncMax:
		if t.Column == nil || isBlank(*t.Column) {
			return qerrors.NewBadUsage("total.column is required for sum, min and max")
		}
	case api.DeliverableAggregateFuncCount:
		if t.Column != nil && isBlank(*t.Column) {
			return qerrors.NewBadUsage("total.column must be non-blank when supplied")
		}
	default:
		return qerrors.NewBadUsage("total.func must be sum, count, min or max")
	}
	return nil
}

// CellsFromTree exports cell bindings with table IDs, series names, mirror
// sheet names, and the row filter of a total that is not a mirror.
func CellsFromTree(tree *api.DeliverableRevisionResponse, seriesNames map[uuid.UUID]string) (map[string]ManifestCell, error) {
	cells := map[string]ManifestCell{}
	sheets := map[uuid.UUID]string{}
	for _, sheet := range copied(tree.SheetBindings) {
		sheets[sheet.ID] = sheet.SheetName
	}
	for _, binding := range copied(tree.CellBindings) {
		ref, err := NormalizeCellRef(binding.CellRef)
		if err != nil {
			return nil, err
		}
		key := binding.SheetName + "!" + ref
		if _, exists := cells[key]; exists {
			return nil, qerrors.NewBadUsagef("revision has multiple bindings for cell %s; remove the duplicate before applying", cellText(key))
		}
		cell := ManifestCell{RunParam: binding.RunParam, Literal: binding.LiteralValue, Format: binding.FormatOverride}
		if binding.LabelCellRef != nil {
			label, err := NormalizeCellRef(*binding.LabelCellRef)
			if err != nil {
				return nil, err
			}
			cell.LabelCell = &label
		}
		if binding.SourceType == api.DeliverableCellSourceTypeAggregate {
			total := &ManifestTotal{Column: binding.AggregateColumn}
			if binding.AggregateFunc != nil {
				total.Func = *binding.AggregateFunc
			}
			if binding.SameRowsAsSheetBindingID != nil {
				name, ok := sheets[*binding.SameRowsAsSheetBindingID]
				if !ok {
					return nil, qerrors.NewBadUsagef("cell %s mirrors a sheet binding missing from the revision", cellText(key))
				}
				total.SameRowsAs = name
			} else {
				if binding.AggregateMemberDatasetID != nil {
					total.Table = strconv.Itoa(*binding.AggregateMemberDatasetID)
				}
				if binding.AggregateMemberSeriesID != nil {
					name, ok := seriesNames[*binding.AggregateMemberSeriesID]
					if !ok {
						name = binding.AggregateMemberSeriesID.String()
					}
					total.Series = &name
				}
				total.RowFilter = row_filters.FromAPI(binding.RowFilter)
			}
			*total = total.markWritten()
			cell.Total = total
		}
		cells[key] = cell
	}
	return cells, nil
}

// SetCells sets spec.cells when cells is non-empty and marks it present. A
// total with no marks gets marks for func, each of table and same_rows_as that
// is non-empty, and each of series, column and row_filter that is non-nil.
func (m *Manifest) SetCells(cells map[string]ManifestCell) {
	if len(cells) == 0 {
		m.Spec.Cells = nil
		delete(m.present, keyCells)
		return
	}
	m.Spec.Cells = make(map[string]ManifestCell, len(cells))
	for key, cell := range cells {
		if cell.Total != nil && cell.Total.present == nil {
			total := cell.Total.markWritten()
			cell.Total = &total
		}
		m.Spec.Cells[key] = cell
	}
	if m.present == nil {
		m.present = map[string]bool{}
	}
	m.present[keyCells] = true
}
