package dataview

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/terminal"
)

const metadataFieldName = "metadata_"

// Normalize validates and aligns a DataSchema into a display-ready Page model.
func Normalize(schema *api.DataWithCellHistorySchema, opts NormalizeOptions) (*Page, error) {
	if schema == nil {
		return nil, fmt.Errorf("nil data response")
	}
	if len(schema.Fields) == 0 {
		return nil, fmt.Errorf("data response has no fields")
	}
	layout := newColumnLayout(schema.Fields, opts)
	layout.masked = maskedSet(schema.MaskedFields)
	if len(layout.fields) == 0 {
		return nil, fmt.Errorf("data response has no data fields")
	}

	if reasons := schema.ApprovalHoldReasons; reasons != nil && len(*reasons) != len(schema.Results) {
		return nil, fmt.Errorf("data response has %d approval_hold_reasons for %d result rows",
			len(*reasons), len(schema.Results))
	}
	if waiting := schema.WaitingForApproval; waiting != nil && len(*waiting) != len(schema.Results) {
		return nil, fmt.Errorf("data response has %d waiting_for_approval entries for %d result rows",
			len(*waiting), len(schema.Results))
	}
	if err := ValidateSelfExclusion(schema); err != nil {
		return nil, err
	}
	if err := checkRowWidths(schema, opts.RequestedFields); err != nil {
		return nil, err
	}
	if events := schema.CellFlagEvents; events != nil && len(*events) != len(schema.Results) {
		return nil, fmt.Errorf("data response has %d cell_flag_events entries for %d result rows",
			len(*events), len(schema.Results))
	}

	page := newPage(schema, opts, buildHeaders(layout, schema.Labels), layout)
	normalizeRows(page, schema, layout)
	attachCellHistory(page, schema, layout, opts)
	page.Showing = len(page.Rows)
	return page, nil
}

// checkRowWidths returns an error for the first row whose width differs from
// the response's field list. When that row is the wider and requested is not
// empty, the error names the requested fields.
func checkRowWidths(schema *api.DataWithCellHistorySchema, requested []string) error {
	for i, row := range schema.Results {
		if len(row) == len(schema.Fields) {
			continue
		}
		err := fmt.Errorf("result row %d has %d values but the response names %d fields: the server's "+
			"rows and field list disagree, so the page cannot be rendered (-o json prints it as sent)",
			i, len(row), len(schema.Fields))
		if len(row) < len(schema.Fields) || len(requested) == 0 {
			return err
		}
		return fmt.Errorf("%w\n\n--fields sent %s. A server that selects each requested name as sent "+
			"widens every row when one name reads as more than one column. "+
			"Check each name against `qctl get table-schema <table> -o name`",
			err, quoteNames(requested))
	}
	return nil
}

func quoteNames(names []string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = strconv.Quote(n)
	}
	return strings.Join(quoted, ", ")
}

// columnLayout splits the response's parallel columns into the ones rendered as
// data and the ones consumed out of band (provenance, the two issue-token
// columns, the quarantine-state columns and the signature), and maps every
// display column back to its index in schema.Fields — which is the index
// has_history and dataset_rule_ids are aligned to.
type columnLayout struct {
	fields    []string // display column FIELD names, in display order
	origIndex []int    // display index -> index in schema.Fields
	metadata  int      // schema.Fields index of metadata_, or -1
	alerts    int      // schema.Fields index of alert_item_ids_, or -1
	warnings  int      // schema.Fields index of warning_item_ids_, or -1
	id        int      // schema.Fields index of id, or -1
	signature int      // schema.Fields index of initial_signature_, or -1

	// state holds the schema.Fields index of each stateFieldNames entry the
	// response carried, in stateFieldNames order; absent ones are -1.
	state []int

	// decision is the schema.Fields index of keep_in_quarantine_, or -1,
	// whether that column is folded or shown.
	decision int
	logs     int // folded row_logs_ index, or -1

	deletedKind int // folded deleted_kind_ index, or -1
	rowDelete   int // folded row_delete_id_ index, or -1

	masked map[string]bool // the response's masked_fields

	// foldedInto maps each compact form to the reserved columns it consumed,
	// and hidden lists the dropped ones — both in response order.
	foldedInto map[string][]string
	hidden     []string
}

// newColumnLayout decides each column's fate.
//
// metadata_ is consumed only under --show-metadata, which is also the only
// request that asks the backend for it. Every other reserved column is
// classified by reserved.go: shown as data, folded into a compact form, or
// hidden. They are in every get_data page — the backend re-adds the whole
// reserved set even when --fields narrows the request — and rendering them raw
// is what this display replaces. -o json and -o yaml still carry all of them,
// untouched.
//
// A column named in --fields is rendered as an ordinary data column.
func newColumnLayout(fields []string, opts NormalizeOptions) columnLayout {
	unlocked := unlockedColumns(opts)
	layout := columnLayout{
		metadata: -1, alerts: -1, warnings: -1, id: -1, signature: -1, decision: -1, logs: -1,
		deletedKind: -1, rowDelete: -1,
	}
	layout.state = make([]int, len(stateFieldNames))
	for i := range layout.state {
		layout.state[i] = -1
	}

	for j, name := range fields {
		if name == metadataFieldName {
			// Not a table column: the backend appends it to `fields` only when
			// show_metadata asked for it, so there is nothing to hide here.
			if opts.ShowMetadata {
				layout.metadata = j
			}
			continue
		}
		if name == keepInQuarantineField {
			layout.decision = j
		}
		if name == rowLogsFieldName && opts.ShowRowLogs && !unlocked[name] {
			layout.fold(name, j)
			continue
		}
		switch fateOf(name, unlocked) {
		case reservedFolded:
			layout.fold(name, j)
			continue
		case reservedHidden:
			layout.hidden = append(layout.hidden, name)
			continue
		}
		if name == idFieldName {
			layout.id = j
		}
		layout.fields = append(layout.fields, name)
		layout.origIndex = append(layout.origIndex, j)
	}
	return layout
}

// fold records where one folded column's values live and which compact form
// consumes it. A folded name reaches the reader through that form, so it is
// grouped under it rather than listed as hidden.
func (l *columnLayout) fold(name string, j int) {
	into := foldIssues
	switch name {
	case alertTokensFieldName:
		l.alerts = j
	case warningTokensFieldName:
		l.warnings = j
	case signatureFieldName:
		l.signature = j
		into = foldSignatures
	case rowLogsFieldName:
		l.logs = j
		into = foldHistory
	case deletedKindField:
		l.deletedKind = j
		into = foldState
	case rowDeleteIDField:
		l.rowDelete = j
		into = foldState
	default:
		for i, stateName := range stateFieldNames {
			if stateName == name {
				l.state[i] = j
				into = foldState
			}
		}
	}
	if l.foldedInto == nil {
		l.foldedInto = map[string][]string{}
	}
	l.foldedInto[into] = append(l.foldedInto[into], name)
}

// foldedGroups returns the folded columns grouped by the form that consumed
// them, in the order the payload presents those forms.
func (l columnLayout) foldedGroups() []FoldedGroup {
	var groups []FoldedGroup
	for _, into := range foldOrder {
		if cols := l.foldedInto[into]; len(cols) > 0 {
			groups = append(groups, FoldedGroup{Into: into, Columns: cols})
		}
	}
	return groups
}

// withoutStateFold moves the Row State group's columns to hidden, for a page
// that renders no Row State column.
func withoutStateFold(folded []FoldedGroup, hidden []string) ([]FoldedGroup, []string) {
	var kept []FoldedGroup
	var moved []string
	for _, g := range folded {
		if g.Into == foldState {
			moved = append(moved, g.Columns...)
			continue
		}
		kept = append(kept, g)
	}
	if len(moved) == 0 {
		return folded, hidden
	}
	return kept, append(moved, hidden...)
}

// hasState reports whether any quarantine-state column is folded into Row
// State. All five are on the quarantine table only, so this is false on a clean
// page.
func (l columnLayout) hasState() bool {
	for _, j := range l.state {
		if j >= 0 {
			return true
		}
	}
	return false
}

// unlockedColumns is the set of reserved columns explicitly named in --fields.
func unlockedColumns(opts NormalizeOptions) map[string]bool {
	unlocked := make(map[string]bool, len(opts.RequestedFields))
	for _, name := range opts.RequestedFields {
		unlocked[strings.TrimSpace(name)] = true
	}
	return unlocked
}

func buildHeaders(layout columnLayout, labels []string) []string {
	headers := make([]string, len(layout.fields))
	for i, name := range layout.fields {
		label := ""
		if origIdx := layout.origIndex[i]; origIdx < len(labels) {
			label = labels[origIdx]
		}
		if strings.TrimSpace(label) == "" {
			label = name
		}
		headers[i] = escapeCell(label)
	}
	return headers
}

func newPage(schema *api.DataWithCellHistorySchema, opts NormalizeOptions, headers []string, layout columnLayout) *Page {
	page := opts.RequestedPage
	if schema.Page != nil {
		page = *schema.Page
	}
	quarantineState := layout.hasState() || schema.ApprovalHoldReasons != nil ||
		schema.WaitingForApproval != nil || schema.SentOrChangedByMe != nil
	anyDeleted := anyDeletedRow(layout, schema)
	deletedRows := derefInt(schema.DeletedRowsCount)
	folded, hidden := layout.foldedGroups(), layout.hidden
	if !quarantineState && !anyDeleted {
		folded, hidden = withoutStateFold(folded, hidden)
	}
	return &Page{
		DatasetName:  schema.DatasetName,
		CleanRows:    derefInt(schema.CleanRowsCount),
		BadRows:      derefInt(schema.BadRowsCount),
		Page:         page,
		Anchored:     schema.Page != nil,
		TotalPages:   schema.TotalPageCount,
		MatchingRows: schema.TotalRows,
		LiveRows:     derefInt(schema.LiveRowsCount),
		DeletedRows:  deletedRows,
		ShowDeletedCounts: schema.LiveRowsCount != nil && schema.DeletedRowsCount != nil &&
			(deletedRows > 0 || (opts.DeletedRows != "" && opts.DeletedRows != api.DeletedRowsLive)),
		Headers:         headers,
		ShowRuleColumn:  opts.IncludeRuleIDs && schema.DatasetRuleIds != nil,
		ShowSourceCol:   layout.metadata >= 0,
		ShowIssueCol:    layout.alerts >= 0 || layout.warnings >= 0,
		ShowStateCol:    quarantineState || anyDeleted,
		QuarantineState: quarantineState,
		AnyDeleted:      anyDeleted,
		RowDeletes:      rowDeleteLines(schema.RowDeletes),
		ShowSignatures:  layout.signature >= 0,
		Folded:          folded,
		HiddenColumns:   hidden,
		SourceFiles:     map[int64]string{},
		MaskedFields:    escapedNames(schema.MaskedFields),
	}
}

func escapedNames(names *[]string) []string {
	if names == nil {
		return nil
	}
	escaped := make([]string, len(*names))
	for i, name := range *names {
		escaped[i] = escapeCell(name)
	}
	return escaped
}

// maskedSet is the set of field names the response carries masked.
func maskedSet(names *[]string) map[string]bool {
	if names == nil {
		return nil
	}
	set := make(map[string]bool, len(*names))
	for _, name := range *names {
		set[name] = true
	}
	return set
}

func normalizeRows(page *Page, schema *api.DataWithCellHistorySchema, layout columnLayout) {
	truncated := parseTruncatedCells(schema.TruncatedCells)
	for i, origRow := range schema.Results {
		page.Rows = append(page.Rows, normalizeRow(page, schema, layout, truncated, i, origRow))
	}
}

func normalizeRow(
	page *Page,
	schema *api.DataWithCellHistorySchema,
	layout columnLayout,
	truncated map[int]map[string]bool,
	rowIdx int,
	origRow []interface{},
) Row {
	var row Row
	if page.ShowIssueCol {
		row.Issues = rowIssues(page, layout, origRow)
	}
	row.Cells = normalizeCells(page, schema, layout, truncated, rowIdx, origRow, row.Issues)
	if page.ShowRuleColumn {
		row.RuleUUIDs = ruleUUIDsForRow(schema.DatasetRuleIds, rowIdx)
		if len(row.RuleUUIDs) > 0 {
			page.AnyRules = true
		}
	}
	if page.ShowSourceCol {
		row.SourceFiles = parseProvenance(origRow[layout.metadata])
		addSourceFiles(page, row.SourceFiles)
	}
	if page.ShowStateCol {
		row.State = append(deletionTokens(layout, origRow),
			rowState(layout, origRow, approvalHoldReasonAt(schema.ApprovalHoldReasons, rowIdx),
				waitingForApprovalAt(schema.WaitingForApproval, rowIdx))...)
		if schema.SentOrChangedByMe != nil && (*schema.SentOrChangedByMe)[rowIdx] {
			row.State = append(row.State, "cannot-approve=self")
			page.AnySelfExcluded = true
		}
	}
	if layout.id >= 0 {
		// Escaped like the provenance keys and thread field names: it is a
		// backend value on a Markdown line, and a newline in it would split
		// the ROW SIGNATURES entry it keys.
		row.ID = escapeCell(formatValue(origRow[layout.id]))
	}
	if page.ShowSignatures {
		row.Signature = escapeCell(formatValue(origRow[layout.signature]))
	}
	return row
}

// rowState folds the quarantine-lifecycle columns, and the row's
// approval_hold_reasons and waiting_for_approval entries, into the tokens the
// Row State column renders, in stateFieldNames order.
//
// The validation verdict is always emitted, including "valid": a quarantined
// row that passes validation is exactly as notable as one that fails, and the
// three states are not interchangeable downstream — see validationToken. The
// two remaining booleans print only when TRUE, since NULL and FALSE both mean
// the row is not in that state; processing_alert_item_id_ prints only when it
// holds an id; and keep_in_quarantine_'s slot prints a hold decision or an
// approval state — see decisionToken.
//
// suspected-duplicate is left out of a row that prints
// held-for-approval=possible_duplicate.
func rowState(layout columnLayout, origRow []interface{}, reason string, waiting bool) []string {
	var tokens []string
	heldAsDuplicate := false
	for i, name := range stateFieldNames {
		j := layout.state[i]
		if name == keepInQuarantineField {
			if t := decisionToken(layout, j >= 0, origRow, reason, waiting); t != "" {
				tokens = append(tokens, t)
				heldAsDuplicate = t == heldForApprovalPrefix+string(api.ApprovalHoldReasonPossibleDuplicate)
			}
			continue
		}
		if j < 0 {
			continue
		}
		v := origRow[j]
		switch name {
		case passesValidationField:
			tokens = append(tokens, validationToken(v))
		case processingAlertIDField:
			if s := formatValue(v); s != "" {
				tokens = append(tokens, "processing-alert="+escapeCell(s))
			}
		default:
			if name == suspectedDuplicateFld && heldAsDuplicate {
				continue
			}
			if b, ok := v.(bool); ok && b {
				tokens = append(tokens, stateTokens[name])
			}
		}
	}
	return tokens
}

// decisionToken is the Row State token for keep_in_quarantine_'s slot: the
// hold decision when the row has one and folded is set; for an undecided row,
// waiting-for-approval when waiting is set, else held-for-approval=<reason>
// when reason is set; and "" otherwise.
//
// folded says keep_in_quarantine_ is folded into Row State; a column the
// request named in --fields is shown raw, so its decision is not repeated here.
// A decided row prints neither approval state, whatever entries it carries. The
// reason is printed verbatim, as quarantineHoldToken prints a hold name it does
// not know.
func decisionToken(layout columnLayout, folded bool, origRow []interface{}, reason string, waiting bool) string {
	if layout.decision < 0 {
		return ""
	}
	if hold := quarantineHoldToken(origRow[layout.decision]); hold != "" {
		if folded {
			return hold
		}
		return ""
	}
	if waiting {
		return waitingForApprovalToken
	}
	if reason == "" {
		return ""
	}
	return heldForApprovalPrefix + escapeCell(reason)
}

// approvalHoldReasonAt returns row i's approval_hold_reasons entry: "" for a
// null entry, an absent list or an index past its end.
func approvalHoldReasonAt(reasons *[]api.ApprovalHoldReason, i int) string {
	if reasons == nil || i < 0 || i >= len(*reasons) {
		return ""
	}
	return string((*reasons)[i])
}

// waitingForApprovalAt returns row i's waiting_for_approval entry: false for a
// null entry, an absent list or an index past its end.
func waitingForApprovalAt(waiting *[]bool, i int) bool {
	if waiting == nil || i < 0 || i >= len(*waiting) {
		return false
	}
	return (*waiting)[i]
}

// quarantineHoldToken names the manual hold decision on a quarantined row.
//
// The cell is no longer a boolean. get_data renders keep_in_quarantine_
// through a CASE in its SELECT list rather than after the fetch, so the grid
// and the CSV download cannot disagree, and what arrives is a QuarantineHold
// name: `hold`, `release` or `undecided` (atlas capi/capi/views/
// data_view_core.py, _quarantine_hold_label_expr). That is the same vocabulary
// `qctl get data --quarantine-hold` filters on and `qctl rows hold`/`release`/
// `reset-hold` write, so the token is greppable against both.
//
// `undecided` prints NOTHING. It is not a decision but the absence of one —
// `qctl rows reset-hold` exists to put a row back into it — so a token for it
// would cost a payload built for agents a cell's width per row while saying
// only what the blank already says. Nothing is lost: `hold` and `release` each
// print, so a blank here IS undecided — unlike the boolean fold this replaced,
// where a blank meant FALSE **or** NULL and those two are not the same row
// afterwards (a released row is exempt from the table's quarantine policy the
// moment the column is non-NULL; an undecided one is not). It parts company
// with validationToken, which spells all three of ITS states, on purpose:
// `valid` on a row that is still in quarantine is a surprising fact worth
// naming, while a row carrying no hold decision is the unremarkable case.
//
// The DDL column is still a nullable boolean, but the API owns the mapping of
// that storage detail to the enum. Anything other than an enum name is printed
// verbatim: a value this build has no name for is exactly what silently
// vanished before, and the reader is better served by an odd-looking token
// than by a blank. In particular, do not translate a legacy boolean here — it
// would hide a backend read path that failed to apply the enum projection.
func quarantineHoldToken(v interface{}) string {
	name := formatValue(v)
	if name == "" || name == string(api.QuarantineHoldUndecided) {
		return ""
	}
	return quarantineHoldPrefix + escapeCell(name)
}

// validationToken names all three states of passes_validation_.
//
// NULL is its own answer, not a false. RESERVED_COLUMNS declares the column
// NOT NULL, but the backend's own handling is what settles this and it treats
// NULL as a live third case throughout — do not read the declaration as saying
// the state cannot occur.
// The read path filters `passes_validation_ IS FALSE`, so --passes-validation=false
// does NOT return a NULL row; the bulk-op path filters `IS NOT TRUE`, so
// `qctl rows … --status failed_validation` DOES affect one; and describe
// table's counter is strict, so it lands in neither bucket. Collapsing NULL
// into "invalid" would state the first of those three and contradict the rest.
func validationToken(v interface{}) string {
	b, ok := v.(bool)
	switch {
	case !ok:
		return "unvalidated"
	case b:
		return "valid"
	default:
		return "invalid"
	}
}

// rowIssues parses one row's alert and warning tokens, alerts first.
func rowIssues(page *Page, layout columnLayout, origRow []interface{}) []IssueRef {
	var refs []IssueRef
	if layout.alerts >= 0 {
		refs = append(refs, parseIssueCell(IssueAlert, origRow[layout.alerts])...)
	}
	if layout.warnings >= 0 {
		refs = append(refs, parseIssueCell(IssueWarning, origRow[layout.warnings])...)
	}
	// A malformed token raises neither flag: these gate the legend lines that
	// explain the [A<id>]/[W<id>] markers, and a malformed ref produces none —
	// it is rendered as the raw token the backend sent.
	for _, ref := range refs {
		switch {
		case ref.Malformed:
		case ref.Kind == IssueAlert:
			page.AnyAlerts = true
		case ref.Kind == IssueWarning:
			page.AnyWarnings = true
		}
	}
	return refs
}

// issuesByField indexes a row's refs by the cell they flag. Whole-row and
// malformed refs are in neither index: they reach the reader through the
// Issues column, which lists every ref the row has.
func issuesByField(refs []IssueRef) map[string][]IssueID {
	byField := map[string][]IssueID{}
	for _, ref := range refs {
		if ref.Malformed || ref.Field == "" {
			continue
		}
		byField[ref.Field] = append(byField[ref.Field], ref.Key())
	}
	return byField
}

func normalizeCells(
	page *Page,
	schema *api.DataWithCellHistorySchema,
	layout columnLayout,
	truncated map[int]map[string]bool,
	rowIdx int,
	origRow []interface{},
	issues []IssueRef,
) []Cell {
	byField := issuesByField(issues)
	pins := stewardOverridesAt(schema.StewardOverrides, rowIdx)
	cells := make([]Cell, len(layout.fields))
	for i, field := range layout.fields {
		origIdx := layout.origIndex[i]
		unconverted := cellNotConverted(schema.NotConverted, rowIdx, origIdx)
		format := displayFormatAt(schema.FieldDisplayFormats, origIdx)
		if layout.masked[field] {
			// A masked value is text, whatever the column's type.
			format = api.DisplayFormat{}
		}
		cell := Cell{
			Display:     formatDisplayCell(origRow[origIdx], format, unconverted),
			Unconverted: unconverted,
			Issues:      byField[field],
		}
		if unconverted {
			page.AnyUnconverted = true
		}
		if pin, ok := pins[field]; ok {
			cell.StewardOverride = stewardOverride(field, pin, format)
			page.AnyStewardOverrides = true
		}
		if cellHasHistory(schema.HasHistory, rowIdx, origIdx) {
			cell.HasHistory = true
			page.AnyHistory = true
		}
		if names, ok := truncated[rowIdx]; ok && names[field] {
			cell.Truncated = true
			page.AnyTruncated = true
		}
		cells[i] = cell
	}
	return cells
}

func cellNotConverted(rows *[][]int, rowIdx, fieldIdx int) bool {
	if rows == nil || rowIdx < 0 || rowIdx >= len(*rows) {
		return false
	}
	for _, index := range (*rows)[rowIdx] {
		if index == fieldIdx {
			return true
		}
	}
	return false
}

func displayFormatAt(formats []api.DisplayFormat, index int) api.DisplayFormat {
	if index < 0 || index >= len(formats) {
		return api.DisplayFormat{}
	}
	return formats[index]
}

func addSourceFiles(page *Page, refs []SourceFileRef) {
	for _, ref := range refs {
		if _, seen := page.SourceFiles[ref.StoredItemID]; !seen {
			page.SourceFiles[ref.StoredItemID] = ref.Key
			page.SourceFileOrder = append(page.SourceFileOrder, ref.StoredItemID)
		}
	}
}

func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

func cellHasHistory(h *[][]bool, i, j int) bool {
	if h == nil || i >= len(*h) {
		return false
	}
	row := (*h)[i]
	if j < 0 || j >= len(row) {
		return false
	}
	return row[j]
}

func ruleUUIDsForRow(d *[][]openapi_types.UUID, i int) []string {
	if d == nil || i >= len(*d) {
		return nil
	}
	ids := (*d)[i]
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, id.String())
	}
	return out
}

// parseTruncatedCells builds rowIndex -> set(fieldName). Malformed entries are ignored.
func parseTruncatedCells(tc *[][]interface{}) map[int]map[string]bool {
	out := map[int]map[string]bool{}
	if tc == nil {
		return out
	}
	for _, pair := range *tc {
		if len(pair) != 2 {
			continue
		}
		idx, ok := wholeNumber(pair[0])
		if !ok {
			continue
		}
		name, ok := pair[1].(string)
		if !ok {
			continue
		}
		i := int(idx)
		if out[i] == nil {
			out[i] = map[string]bool{}
		}
		out[i][name] = true
	}
	return out
}

// parseProvenance parses a metadata_ cell (array of {stored_item_id, key}) into refs.
func parseProvenance(cell interface{}) []SourceFileRef {
	arr, ok := cell.([]interface{})
	if !ok {
		return nil
	}
	var refs []SourceFileRef
	for _, item := range arr {
		obj, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		id, ok := wholeNumber(obj["stored_item_id"])
		if !ok {
			continue
		}
		key, _ := obj["key"].(string)
		refs = append(refs, SourceFileRef{StoredItemID: id, Key: escapeCell(key)})
	}
	return refs
}

// wholeNumber reads an integer the response carries in an untyped position,
// whichever way the body was decoded.
func wholeNumber(v interface{}) (int64, bool) {
	switch x := v.(type) {
	case json.Number:
		n, err := x.Int64()
		return n, err == nil
	case float64:
		return int64(x), x == math.Trunc(x) && math.Abs(x) < 1<<53
	default:
		return 0, false
	}
}

// isIntegerLiteral reports whether a JSON number is written as an integer:
// only such a literal is exact past 2^53, and only a float carries . e or E.
func isIntegerLiteral(n json.Number) bool {
	return !strings.ContainsAny(n.String(), ".eE")
}

// formatCell formats a backend value and returns "-" for null/empty; the result is escaped.
func formatCell(v interface{}) string {
	s := formatValue(v)
	if s == "" {
		return "-"
	}
	return escapeCell(s)
}

func formatValue(v interface{}) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case float64:
		if x == math.Trunc(x) && !math.IsInf(x, 0) && math.Abs(x) < 1e15 {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'g', -1, 64)
	case json.Number:
		// An integer is printed exactly. Any other number came from a float
		// column and prints as the float it is, so "100.0" still reads 100.
		if isIntegerLiteral(x) {
			return x.String()
		}
		if f, err := x.Float64(); err == nil {
			return formatValue(f)
		}
		return x.String()
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(b)
	}
}

// EscapeCell keeps a value from breaking the Markdown table it sits in:
// newlines become the two characters \n and a pipe is escaped.
//
// Exported for the rule-evidence view (`qctl describe row`), which builds the
// same kind of pipe table from a different route.
func EscapeCell(s string) string {
	return escapeCell(s)
}

// escapeCell collapses CR/LF to a literal \n and escapes Markdown pipe characters so one backend
// row always occupies exactly one Markdown row.
func escapeCell(s string) string {
	s = terminal.Sanitize(s)
	s = strings.ReplaceAll(s, "\r\n", `\n`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "\r", `\n`)
	return strings.ReplaceAll(s, "|", `\|`)
}

// AttachCommentThreads correlates a by-rows overlay onto the page's rows.
//
// The overlay is keyed by row id, and the only place a row id appears in a
// get_data response is the id column — so its absence is a hard error rather
// than an empty column, which would read as "this row has no discussion".
//
// page must be the Normalize result for this same schema: normalizeRows emits
// exactly one Row per schema.Results entry, in order, so row i of one is row i
// of the other.
func AttachCommentThreads(page *Page, schema *api.DataWithCellHistorySchema, overlays []api.RowThreadOverlaySchema) error {
	idIdx, err := idColumnIndex(schema)
	if err != nil {
		return err
	}
	byRow := map[int][]ThreadRef{}
	for _, o := range overlays {
		byRow[o.RowID] = append(byRow[o.RowID], threadRef(o))
	}
	for i := range page.Rows {
		rowID, err := rowIDAt(schema, i, idIdx)
		if err != nil {
			return err
		}
		page.Rows[i].CommentThreads = byRow[rowID]
		if len(page.Rows[i].CommentThreads) > 0 {
			page.AnyComments = true
		}
	}
	page.ShowCommentCol = true
	return nil
}

// threadRef converts one overlay entry. Every optional field may be null on
// the wire; WholeRow and FieldNames are independent and neither is inferred
// from the other being absent.
func threadRef(o api.RowThreadOverlaySchema) ThreadRef {
	ref := ThreadRef{ThreadID: o.ThreadID.String()}
	if o.CommentCount != nil {
		ref.CommentCount = *o.CommentCount
	}
	if o.WholeRow != nil {
		ref.WholeRow = *o.WholeRow
	}
	if o.FieldNames != nil {
		ref.FieldNames = make([]string, 0, len(*o.FieldNames))
		for _, name := range *o.FieldNames {
			// Escaped here, like provenance keys: field names are backend data
			// and a pipe in one would split the Markdown row.
			ref.FieldNames = append(ref.FieldNames, escapeCell(name))
		}
	}
	return ref
}

// RowIDs returns the row id of every result row, in page order — the input to
// a by-rows overlay request (comment threads, data reviews).
func RowIDs(schema *api.DataWithCellHistorySchema) ([]int, error) {
	idIdx, err := idColumnIndex(schema)
	if err != nil {
		return nil, err
	}
	ids := make([]int, 0, len(schema.Results))
	for i := range schema.Results {
		id, err := rowIDAt(schema, i, idIdx)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func idColumnIndex(schema *api.DataWithCellHistorySchema) (int, error) {
	if schema == nil {
		return -1, fmt.Errorf("cannot correlate row overlays: nil data response")
	}
	for j, name := range schema.Fields {
		if name == idFieldName {
			return j, nil
		}
	}
	return -1, fmt.Errorf("cannot correlate row overlays: the data response has no %q column", idFieldName)
}

// rowIDAt reads one row's id. A row whose id cannot be read is an error, never
// a silent 0 — a 0 would correlate that row onto whatever thread targets row 0.
func rowIDAt(schema *api.DataWithCellHistorySchema, rowIdx, idIdx int) (int, error) {
	if rowIdx < 0 || rowIdx >= len(schema.Results) {
		return 0, fmt.Errorf("cannot correlate row overlays: the data response has no result row %d", rowIdx)
	}
	row := schema.Results[rowIdx]
	if idIdx < 0 || idIdx >= len(row) {
		return 0, fmt.Errorf("cannot correlate row overlays: result row %d has no %q value", rowIdx, idFieldName)
	}
	id, err := toRowID(row[idIdx])
	if err != nil {
		return 0, fmt.Errorf("cannot correlate row overlays: result row %d has an unusable %q value: %w", rowIdx, idFieldName, err)
	}
	return id, nil
}

// toRowID converts a wire row id to an int. encoding/json decodes a JSON
// number into interface{} as float64, a decoder configured with UseNumber
// yields json.Number, and a quoted id arrives as a string.
func toRowID(v interface{}) (int, error) {
	switch x := v.(type) {
	case nil:
		return 0, fmt.Errorf("value is null")
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) || x != math.Trunc(x) {
			return 0, fmt.Errorf("%v is not a whole number", x)
		}
		return int(x), nil
	case json.Number:
		n, err := x.Int64()
		if err != nil {
			return 0, fmt.Errorf("%q is not a whole number: %w", x.String(), err)
		}
		return int(n), nil
	case string:
		n, err := strconv.Atoi(x)
		if err != nil {
			return 0, fmt.Errorf("%q is not a whole number: %w", x, err)
		}
		return n, nil
	case int:
		return x, nil
	case int64:
		return int(x), nil
	default:
		return 0, fmt.Errorf("%v is not a row id", v)
	}
}
