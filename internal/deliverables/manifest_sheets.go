package deliverables

import (
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
	"github.com/qlustered/qctl/internal/row_filters"
	"gopkg.in/yaml.v3"
)

// Keys of a spec.sheets entry.
const (
	keyTable               = "table"
	keySeries              = "series"
	keyHeaderRows          = "header_rows"
	keyDataStartRow        = "data_start_row"
	keyInstructionRowCount = "instruction_row_count"
	keyRowOrder            = "row_order"
	keyColumns             = "columns"
	keyRowFilter           = "row_filter"
)

const (
	maxColumnIndex      = 16384
	maxHeaderRows       = 3
	maxInstructionRows  = 10
	maxColumnLetterSize = 3
)

// ManifestSheet is one sheet binding of spec.sheets.
type ManifestSheet struct {
	Table               string                    `yaml:"table" json:"table"`
	Series              *string                   `yaml:"series,omitempty" json:"series,omitempty"`
	HeaderRows          []int                     `yaml:"header_rows" json:"header_rows"`
	DataStartRow        int                       `yaml:"data_start_row" json:"data_start_row"`
	InstructionRowCount *int                      `yaml:"instruction_row_count,omitempty" json:"instruction_row_count,omitempty"`
	RowOrder            []ManifestSortKey         `yaml:"row_order,omitempty" json:"row_order,omitempty"`
	RowFilter           *row_filters.Filter       `yaml:"row_filter,omitempty" json:"row_filter,omitempty"`
	Columns             map[string]ManifestColumn `yaml:"columns" json:"columns"`
	present             map[string]bool
	byIndex             map[int]ManifestColumn
}

// UnmarshalJSON decodes a sheet with a string table reference or integer ID.
func (s *ManifestSheet) UnmarshalJSON(data []byte) error {
	type plainSheet ManifestSheet
	var sheet plainSheet
	wire := struct {
		*plainSheet
		Table json.RawMessage `json:"table"`
	}{plainSheet: &sheet}
	if err := pkgmanifest.StrictUnmarshal(data, &wire); err != nil {
		return err
	}
	if len(wire.Table) > 0 && string(wire.Table) != nullText {
		if err := json.Unmarshal(wire.Table, &sheet.Table); err != nil {
			id, err := strconv.ParseInt(string(wire.Table), 10, 64)
			if err != nil {
				return qerrors.NewBadUsage("table must be a table name or integer ID")
			}
			sheet.Table = strconv.FormatInt(id, 10)
		}
	}
	*s = ManifestSheet(sheet)
	return nil
}

// ManifestSortKey is one row_order entry.
type ManifestSortKey struct {
	Column     string `yaml:"column" json:"column"`
	Descending bool   `yaml:"descending,omitempty" json:"descending,omitempty"`
}

// ManifestColumn is one column binding of a sheet's columns.
type ManifestColumn struct {
	Column   string  `yaml:"column,omitempty" json:"column,omitempty"`
	RunParam string  `yaml:"run_param,omitempty" json:"run_param,omitempty"`
	Literal  *string `yaml:"literal,omitempty" json:"literal,omitempty"`
	Format   *string `yaml:"format,omitempty" json:"format,omitempty"`
}

// Has reports whether the sheet's key was written, even as null: in the
// parsed document, or by SheetsFromTree or SetSheets.
func (s ManifestSheet) Has(key string) bool {
	return s.present[key]
}

// ColumnsByIndex returns Columns keyed by 1-based column index, or nil when
// columns was not written.
func (s ManifestSheet) ColumnsByIndex() map[int]ManifestColumn {
	return s.byIndex
}

// ParseColumnKey returns the 1-based column index a key names: letters A to
// XFD in either case, or a decimal index 1 to 16384 without leading zeros.
// Any other key is a bad-usage error.
func ParseColumnKey(key string) (int, error) {
	if len(key) > 1 && key[0] == '0' && strings.Trim(key, "0123456789") == "" {
		return 0, qerrors.NewBadUsagef("%q is not a column: numeric indexes cannot have leading zeros", key)
	}
	index, ok := columnIndex(key)
	if !ok {
		return 0, qerrors.NewBadUsagef("%q is not a column: use a letter from A to XFD or an index from 1 to %d", key, maxColumnIndex)
	}
	return index, nil
}

func columnIndex(key string) (int, bool) {
	if key == "" {
		return 0, false
	}
	if key[0] >= '0' && key[0] <= '9' {
		index, err := strconv.Atoi(key)
		return index, err == nil && key[0] != '0' && index <= maxColumnIndex
	}
	if len(key) > maxColumnLetterSize {
		return 0, false
	}
	index := 0
	for _, r := range key {
		if r >= 'a' && r <= 'z' {
			r -= 'a' - 'A'
		}
		if r < 'A' || r > 'Z' {
			return 0, false
		}
		index = index*26 + int(r-'A') + 1
	}
	return index, index <= maxColumnIndex
}

// ColumnLetter returns the spreadsheet letters of a 1-based column index, e.g.
// C for 3, or "" for an index below 1.
func ColumnLetter(index int) string {
	name := ""
	for index > 0 {
		index--
		name = string(rune('A'+index%26)) + name
		index /= 26
	}
	return name
}

// writtenSheetKeys returns the keys each spec.sheets entry of data writes,
// null included.
func writtenSheetKeys(data []byte) map[string]map[string]bool {
	var envelope struct {
		Spec struct {
			Sheets map[string]map[string]any `yaml:"sheets"`
		} `yaml:"spec"`
	}
	_ = yaml.Unmarshal(data, &envelope)
	written := make(map[string]map[string]bool, len(envelope.Spec.Sheets))
	for name, sheet := range envelope.Spec.Sheets {
		written[name] = map[string]bool{}
		for key := range sheet {
			written[name][key] = true
		}
	}
	return written
}

// validateSheets checks spec.sheets and fills each sheet's column indexes.
func (m *Manifest) validateSheets() error {
	if !m.Has(keySheets) {
		return nil
	}
	if m.Spec.Sheets == nil {
		return qerrors.NewBadUsage("spec.sheets must be a mapping; use {} for no sheets")
	}
	for _, name := range slices.Sorted(maps.Keys(m.Spec.Sheets)) {
		if name == "" {
			return qerrors.NewBadUsage("spec.sheets: a sheet name cannot be empty")
		}
		sheet := m.Spec.Sheets[name]
		byIndex, err := sheet.validate()
		if err != nil {
			return fmt.Errorf("%s.%w", sheetFieldPath("spec.sheets", name), err)
		}
		sheet.byIndex = byIndex
		m.Spec.Sheets[name] = sheet
	}
	return nil
}

// validate checks one sheet and returns its columns keyed by index. Error
// messages start with the key they name.
func (s ManifestSheet) validate() (map[int]ManifestColumn, error) {
	if isBlank(s.Table) {
		return nil, qerrors.NewBadUsagef("%s is required", keyTable)
	}
	if id, err := strconv.Atoi(s.Table); err == nil && id <= 0 {
		return nil, qerrors.NewBadUsagef("%s: a table ID must be positive", keyTable)
	}
	if s.Series != nil && isBlank(*s.Series) {
		return nil, qerrors.NewBadUsagef("%s must be non-blank; use null to clear it", keySeries)
	}
	if err := s.validateRows(); err != nil {
		return nil, err
	}
	if err := s.validateRowOrder(); err != nil {
		return nil, err
	}
	if err := s.RowFilter.Validate(true, keyRowFilter); err != nil {
		return nil, err
	}
	return s.validateColumns()
}

func (s ManifestSheet) validateRows() error {
	if !s.Has(keyDataStartRow) {
		return qerrors.NewBadUsagef("%s is required", keyDataStartRow)
	}
	if s.DataStartRow < 1 {
		return qerrors.NewBadUsagef("%s must be at least 1", keyDataStartRow)
	}
	if s.DataStartRow > maxCellRow {
		return qerrors.NewBadUsagef("%s must be at most %d (Excel's last row)", keyDataStartRow, maxCellRow)
	}
	if err := s.validateHeaderRows(); err != nil {
		return err
	}
	if s.Has(keyInstructionRowCount) && (s.InstructionRowCount == nil || *s.InstructionRowCount < 0 || *s.InstructionRowCount > maxInstructionRows) {
		return qerrors.NewBadUsagef("%s must be 0–%d; omit it to keep the stored value", keyInstructionRowCount, maxInstructionRows)
	}
	return nil
}

func (s ManifestSheet) validateHeaderRows() error {
	if s.HeaderRows == nil {
		return qerrors.NewBadUsagef("%s is required; use [] for none", keyHeaderRows)
	}
	if len(s.HeaderRows) > maxHeaderRows {
		return qerrors.NewBadUsagef("%s has at most %d rows", keyHeaderRows, maxHeaderRows)
	}
	for i, row := range s.HeaderRows {
		switch {
		case row < 1:
			return qerrors.NewBadUsagef("%s[%d] must be at least 1", keyHeaderRows, i)
		case row > maxCellRow:
			return qerrors.NewBadUsagef("%s[%d] must be at most %d (Excel's last row)", keyHeaderRows, i, maxCellRow)
		case i > 0 && row <= s.HeaderRows[i-1]:
			return qerrors.NewBadUsagef("%s must be strictly increasing", keyHeaderRows)
		case row >= s.DataStartRow:
			return qerrors.NewBadUsagef("%s[%d] must be above %s %d", keyHeaderRows, i, keyDataStartRow, s.DataStartRow)
		}
	}
	return nil
}

func (s ManifestSheet) validateRowOrder() error {
	if !s.Has(keyRowOrder) {
		return nil
	}
	if s.RowOrder == nil {
		return qerrors.NewBadUsagef("%s must be a list; use [] for none", keyRowOrder)
	}
	seen := map[string]bool{}
	for i, key := range s.RowOrder {
		if key.Column == "" {
			return qerrors.NewBadUsagef("%s[%d].column is required", keyRowOrder, i)
		}
		if isBlank(key.Column) {
			return qerrors.NewBadUsagef("%s[%d].column must be non-blank", keyRowOrder, i)
		}
		if seen[key.Column] {
			return qerrors.NewBadUsagef("%s[%d]: column %q is listed twice", keyRowOrder, i, key.Column)
		}
		seen[key.Column] = true
	}
	return nil
}

func (s ManifestSheet) validateColumns() (map[int]ManifestColumn, error) {
	if !s.Has(keyColumns) {
		return nil, nil
	}
	if s.Columns == nil {
		return nil, qerrors.NewBadUsagef("%s must be a mapping; use {} for none", keyColumns)
	}
	byIndex := make(map[int]ManifestColumn, len(s.Columns))
	named := map[int]string{}
	for _, key := range slices.Sorted(maps.Keys(s.Columns)) {
		index, err := ParseColumnKey(key)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", sheetFieldPath(keyColumns, key), err)
		}
		if other, ok := named[index]; ok {
			return nil, qerrors.NewBadUsagef("%s: %s and %s both name column %s", keyColumns, cellText(other), cellText(key), ColumnLetter(index))
		}
		column := s.Columns[key]
		if err := column.validate(sheetFieldPath(keyColumns, key)); err != nil {
			return nil, err
		}
		named[index] = key
		byIndex[index] = column
	}
	return byIndex, nil
}

func (c ManifestColumn) validate(path string) error {
	sources := 0
	for _, selected := range []bool{c.Column != "", c.RunParam != "", c.Literal != nil} {
		if selected {
			sources++
		}
	}
	switch {
	case sources != 1:
		return qerrors.NewBadUsagef("%s: set exactly one of column, run_param or literal", path)
	case c.Column != "" && isBlank(c.Column):
		return qerrors.NewBadUsagef("%s.column must be non-blank", path)
	case c.RunParam != "" && isBlank(c.RunParam):
		return qerrors.NewBadUsagef("%s.run_param must be non-blank", path)
	case c.Literal != nil:
		if err := validateLiteral(*c.Literal); err != nil {
			return fmt.Errorf("%s.%w", path, err)
		}
	}
	return nil
}

// SheetsFromTree returns one sheet per sheet binding of tree, named by its
// sheet_name: the table as its ID, the series by seriesNames or else its UUID,
// the row order by position, the row filter when there is one, and the
// columns by letter.
func SheetsFromTree(tree *api.DeliverableRevisionResponse, seriesNames map[uuid.UUID]string) map[string]ManifestSheet {
	sheets := map[string]ManifestSheet{}
	for _, binding := range copied(tree.SheetBindings) {
		count := binding.InstructionRowCount
		sheet := ManifestSheet{
			Table:               strconv.Itoa(binding.MemberDatasetID),
			HeaderRows:          append([]int{}, binding.HeaderRows...),
			DataStartRow:        binding.DataStartRow,
			InstructionRowCount: &count,
			RowFilter:           row_filters.FromAPI(binding.RowFilter),
			Columns:             map[string]ManifestColumn{},
		}
		if binding.MemberSeriesID != nil {
			name, ok := seriesNames[*binding.MemberSeriesID]
			if !ok {
				name = binding.MemberSeriesID.String()
			}
			sheet.Series = &name
		}
		keys := copied(binding.SortKeys)
		slices.SortFunc(keys, func(a, b api.SortKeySchema) int { return cmp.Compare(a.Position, b.Position) })
		for _, key := range keys {
			sheet.RowOrder = append(sheet.RowOrder, ManifestSortKey{Column: key.ColumnName, Descending: key.Descending != nil && *key.Descending})
		}
		for _, source := range copied(binding.ColumnBindings) {
			column := ManifestColumn{}
			switch source.SourceType {
			case api.DeliverableColumnSourceTypeLiteral:
				column.Literal = source.LiteralValue
			case api.DeliverableColumnSourceTypeRunParam:
				column.RunParam = derefText(source.RunParam)
			default:
				column.Column = derefText(source.SourceColumn)
			}
			if source.FormatOverride != nil {
				format := *source.FormatOverride
				column.Format = &format
			}
			sheet.Columns[ColumnLetter(source.TargetColumnIndex)] = column
		}
		sheets[binding.SheetName] = sheet.markWritten()
	}
	return sheets
}

func derefText(text *string) string {
	if text == nil {
		return ""
	}
	return *text
}

// markWritten marks table, header_rows, data_start_row, each of series,
// instruction_row_count, row_filter and columns that is non-nil, and row_order
// when it is non-empty. It indexes the columns, skipping a key that names no
// column.
func (s ManifestSheet) markWritten() ManifestSheet {
	s.present = map[string]bool{keyTable: true, keyHeaderRows: true, keyDataStartRow: true}
	if s.Series != nil {
		s.present[keySeries] = true
	}
	if s.InstructionRowCount != nil {
		s.present[keyInstructionRowCount] = true
	}
	if s.RowFilter != nil {
		s.present[keyRowFilter] = true
	}
	if len(s.RowOrder) > 0 {
		s.present[keyRowOrder] = true
	}
	s.byIndex = nil
	if s.Columns != nil {
		s.present[keyColumns] = true
		s.byIndex = make(map[int]ManifestColumn, len(s.Columns))
		for key, column := range s.Columns {
			if index, ok := columnIndex(key); ok {
				s.byIndex[index] = column
			}
		}
	}
	return s
}

// SetSheets sets spec.sheets and marks it present. A sheet with no marks gets
// marks for table, header_rows, data_start_row, each of series,
// instruction_row_count, row_filter and columns that is non-nil, and row_order
// when it is non-empty, and its columns indexed, skipping a key that names no
// column. An empty map leaves spec.sheets unset, as marshalling omits it.
func (m *Manifest) SetSheets(sheets map[string]ManifestSheet) {
	if len(sheets) == 0 {
		m.Spec.Sheets = nil
		delete(m.present, keySheets)
		return
	}
	m.Spec.Sheets = make(map[string]ManifestSheet, len(sheets))
	for name, sheet := range sheets {
		if sheet.present == nil {
			sheet = sheet.markWritten()
		}
		m.Spec.Sheets[name] = sheet
	}
	if m.present == nil {
		m.present = map[string]bool{}
	}
	m.present[keySheets] = true
}
