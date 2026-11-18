package dataview

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/qlustered/qctl/internal/terminal"
)

// RuleAliaser maps a dataset-rule UUID to its persistent alias (e.g. "r-1").
type RuleAliaser interface {
	Alias(uuid string) (string, error)
}

// Render writes the LLM Markdown payload for page to w. aliaser may be nil only when
// page.ShowRuleColumn is false. An allocation error fails the render (no unstable aliases).
func Render(w io.Writer, page *Page, aliaser RuleAliaser) error {
	if page == nil {
		return fmt.Errorf("nil page")
	}
	if page.ShowRuleColumn && aliaser == nil {
		return fmt.Errorf("rule column requested but no aliaser provided")
	}

	var b strings.Builder
	writeSummary(&b, page)
	headers := tableHeaders(page)
	writeTableHeader(&b, headers)
	if err := writeTableRows(&b, page, headers, aliaser); err != nil {
		return err
	}
	writeIssueDictionary(&b, page)
	writeCellHistory(&b, page)
	writeStewardOverrides(&b, page)
	writeRowDeletes(&b, page)
	writeSignatureDictionary(&b, page)
	writeSourceFileDictionary(&b, page)
	writePaginationHint(&b, page)

	_, err := io.WriteString(w, terminal.Sanitize(b.String()))
	return err
}

func writeSummary(b *strings.Builder, page *Page) {
	b.WriteString("# DATASET SUMMARY\n")
	fmt.Fprintf(b, "Dataset: %s\n", page.DatasetName)
	fmt.Fprintf(b, "Clean Rows: %d | Bad Rows: %d\n", page.CleanRows, page.BadRows)
	fmt.Fprintf(b, "Page: %d of %d | Matching Rows: %d%s | Showing: %d",
		page.Page, page.TotalPages, page.MatchingRows, matchingRowsSuffix(page), page.Showing)
	if n := stewardPinnedCells(page); n > 0 {
		fmt.Fprintf(b, " | Steward-Pinned Cells: %d", n)
	}
	b.WriteString("\n")
	if legend := buildLegend(page); legend != "" {
		b.WriteString(legend + "\n")
	}
	if !page.ShowIssueMessages && (page.AnyAlerts || page.AnyWarnings) {
		b.WriteString("Add --include-issues for the message behind each [A<id>]/[W<id>].\n")
	}
	if line := buildReservedNotice(page); line != "" {
		b.WriteString(line + "\n")
	}
	if len(page.MaskedFields) > 0 {
		b.WriteString(maskedNotice(page.MaskedFields) + "\n")
	}
}

// maskedNotice names the fields whose values the page carries masked.
func maskedNotice(fields []string) string {
	return "Masked fields: " + strings.Join(fields, ", ") +
		" — every value of these fields is •••••, or ••••• and its last 4 characters, printed as sent and not as the column's type."
}

// buildReservedNotice discloses the reserved columns this view did not render
// raw, and names both flags that bring one back.
//
// The columns are named, not just counted: knowing that occurrence_ and
// cluster_id_ exist on this table is most of what a reader needs to decide
// whether to ask for one, and the whole set costs one line against the
// seventeen columns it replaces on a quarantine page.
func buildReservedNotice(page *Page) string {
	if len(page.Folded) == 0 && len(page.HiddenColumns) == 0 {
		return ""
	}
	var parts []string
	for _, g := range page.Folded {
		parts = append(parts, strings.Join(g.Columns, ", ")+" -> "+g.Into)
	}
	if len(page.HiddenColumns) > 0 {
		parts = append(parts, "hidden: "+strings.Join(page.HiddenColumns, ", "))
	}
	return "Reserved columns — " + strings.Join(parts, "; ") +
		". Show one with --fields <name>; -o json has them all."
}

func tableHeaders(page *Page) []string {
	headers := append([]string{}, page.Headers...)
	if page.ShowIssueCol {
		headers = append(headers, "Issues")
	}
	if page.ShowStateCol {
		headers = append(headers, "Row State")
	}
	if page.ShowRuleColumn {
		headers = append(headers, "Triggered Rules")
	}
	if page.ShowSourceCol {
		headers = append(headers, "Source Files")
	}
	if page.ShowCommentCol {
		headers = append(headers, "Comment Threads")
	}
	return headers
}

func writeTableHeader(b *strings.Builder, headers []string) {
	b.WriteString("\n| " + strings.Join(headers, " | ") + " |\n")
	seps := make([]string, len(headers))
	for i := range seps {
		seps[i] = "---"
	}
	b.WriteString("|" + strings.Join(seps, "|") + "|\n")
}

func writeTableRows(b *strings.Builder, page *Page, headers []string, aliaser RuleAliaser) error {
	aliasCache := map[string]string{}
	for _, row := range page.Rows {
		cols := make([]string, 0, len(headers))
		for _, c := range row.Cells {
			cols = append(cols, renderCell(c))
		}
		if page.ShowIssueCol {
			cols = append(cols, renderIssues(row.Issues))
		}
		if page.ShowStateCol {
			cols = append(cols, renderState(row.State))
		}
		if page.ShowRuleColumn {
			s, err := renderRules(row.RuleUUIDs, aliaser, aliasCache)
			if err != nil {
				return err
			}
			cols = append(cols, s)
		}
		if page.ShowSourceCol {
			cols = append(cols, renderSourceIDs(row.SourceFiles))
		}
		if page.ShowCommentCol {
			cols = append(cols, renderCommentThreads(row.CommentThreads))
		}
		b.WriteString("| " + strings.Join(cols, " | ") + " |\n")
	}
	return nil
}

// writeIssueDictionary writes the hydrated messages, alerts then warnings, one
// line per distinct id on the page. It is written only under --include-issues,
// and then ALWAYS — a page with no issues says so rather than printing nothing,
// which would be indistinguishable from the flag having been ignored.
//
// An id with no response is named too: the batch routes answer only for ids
// this table has an alert or warning for — narrowed, for an external user, to
// their associated data sources — so an id that misses simply is not in the
// response, and omitting its line would read as "no message". A returned empty
// message gets a separate placeholder. Being resolved is not what puts an id
// in the unanswered state; see alerts.MsgsList.
func writeIssueDictionary(b *strings.Builder, page *Page) {
	if !page.ShowIssueMessages {
		return
	}
	b.WriteString("\n## ISSUES\n")
	if len(page.IssueOrder) == 0 {
		b.WriteString("- none on this page\n")
		return
	}
	for _, key := range page.IssueOrder {
		msg, ok := page.IssueMessages[key]
		switch {
		case !ok:
			msg = "(no message returned by the server)"
		case msg == "":
			msg = "(empty message)"
		}
		if names := page.IssueFlaggers[key]; ok && len(names) > 0 {
			msg += " — flagged by " + flaggedBy(names)
		}
		fmt.Fprintf(b, "- %s: %s\n", key.Label(), msg)
	}
}

// maxFlaggerNames is how many flaggers a dictionary line names before it
// counts the rest.
const maxFlaggerNames = 3

// flaggedBy joins names, naming at most maxFlaggerNames and counting the rest
// as " +N more".
func flaggedBy(names []string) string {
	if len(names) <= maxFlaggerNames {
		return strings.Join(names, ", ")
	}
	return strings.Join(names[:maxFlaggerNames], ", ") + fmt.Sprintf(" +%d more", len(names)-maxFlaggerNames)
}

// writeStewardOverrides writes one line per steward-pinned cell, in page
// order: the row, the backend field name, the value the pin displaced and the
// cell as the table shows it.
func writeStewardOverrides(b *strings.Builder, page *Page) {
	if !page.AnyStewardOverrides {
		return
	}
	b.WriteString("\n## STEWARD OVERRIDES\n")
	for i, row := range page.Rows {
		for _, c := range row.Cells {
			pin := c.StewardOverride
			if pin == nil {
				continue
			}
			displaced := displacedNull
			if pin.HasDisplaced {
				displaced = pin.Displaced
			}
			fmt.Fprintf(b, "- %s %s: %s → %s\n", rowKey(row, i), pin.Field, displaced, c.Display)
		}
	}
}

// displacedNull stands in for a null base_displaced_value: the cell was blank
// before the pin, or no earlier value was recorded.
const displacedNull = "(null: was blank, or not recorded)"

func stewardPinnedCells(page *Page) int {
	n := 0
	for _, row := range page.Rows {
		for _, c := range row.Cells {
			if c.StewardOverride != nil {
				n++
			}
		}
	}
	return n
}

// rowKey names a row in the dictionaries below the table: its id, or its
// position on the page when the response carried no id column.
func rowKey(row Row, i int) string {
	if row.ID != "" {
		return row.ID
	}
	return "row " + strconv.Itoa(i+1)
}

// writeSignatureDictionary writes each row's initial_signature_, the hex the
// `qctl rows delete-quarantined` verb takes as --signature.
//
// A dictionary rather than a column: the value is a fixed hex digest that would
// dominate every row of the table, and the delete verbs need it verbatim, so
// abbreviating it in a cell is not an option. Keyed by the row's id — the same
// handle `qctl describe row` and `qctl rows release --row-id` take — falling
// back to the row's position on the page if the response carried no id column.
func writeSignatureDictionary(b *strings.Builder, page *Page) {
	if !page.ShowSignatures || len(page.Rows) == 0 {
		return
	}
	b.WriteString("\n## ROW SIGNATURES\n")
	for i, row := range page.Rows {
		sig := row.Signature
		if sig == "" {
			sig = "-"
		}
		fmt.Fprintf(b, "- %s: %s\n", rowKey(row, i), sig)
	}
}

// writeSourceFileDictionary writes referenced files only, each once, in stable order.
func writeSourceFileDictionary(b *strings.Builder, page *Page) {
	if page.ShowSourceCol && len(page.SourceFileOrder) > 0 {
		b.WriteString("\n## SOURCE FILES\n")
		for _, id := range page.SourceFileOrder {
			fmt.Fprintf(b, "- %d: %s\n", id, page.SourceFiles[id])
		}
	}
}

func writePaginationHint(b *strings.Builder, page *Page) {
	if page.TotalPages > page.Page {
		if page.Anchored {
			fmt.Fprintf(b, "\nMore rows available — remove --anchor-row-id and fetch the next page with --page %d.\n", page.Page+1)
			return
		}
		fmt.Fprintf(b, "\nMore rows available — fetch the next page with --page %d.\n", page.Page+1)
	}
}

func buildLegend(page *Page) string {
	var parts []string
	if page.AnyHistory {
		parts = append(parts, "[H] = Cell has history")
	}
	if page.AnyFlagHistory {
		parts = append(parts, "[F] = Cell has flag lifecycle events under CELL HISTORY")
	}
	if page.AnyTruncated {
		parts = append(parts, "[T] = Cell value was truncated")
	}
	if page.AnyUnconverted {
		parts = append(parts, "[U] = Quarantine cell still contains unconverted source text")
	}
	if page.AnyRules {
		parts = append(parts, "[R: r-N] = Row triggered dataset rule")
	}
	if page.AnyComments {
		parts = append(parts, "[N] = comments in that thread")
	}
	if page.AnyAlerts {
		parts = append(parts, "[A<id>] = alert <id> flagged that cell, or the whole row in the Issues column")
	}
	if page.AnyWarnings {
		parts = append(parts, "[W<id>] = warning <id> flagged that cell, or the whole row in the Issues column")
	}
	if page.AnyStewardOverrides {
		parts = append(parts, "[S] = value pinned by a steward overwrite; what it displaced is under STEWARD OVERRIDES")
	}
	if page.AnyDeleted {
		parts = append(parts, "Row State deleted=<kind> = the row is deleted (deleted_kind_: "+
			"person|excluded_line|absent_from_file|delta|composite); row-delete=<id> = the delete under ROW DELETES")
	}
	if page.QuarantineState {
		parts = append(parts, "Row State = valid|invalid|unvalidated (passes_validation_) "+
			"+ quarantine-hold=hold|release (blank = undecided) "+
			"/ waiting-for-approval / held-for-approval=<reason> / suspected-duplicate / awaiting-processing / processing-alert=<id>")
	}
	if page.AnySelfExcluded {
		parts = append(parts, "cannot-approve=self = you edited this row or submitted it for review; another approver must approve it. "+
			"An unmarked row still requires approval permission and any reviewer designation")
	}
	if len(parts) == 0 {
		return ""
	}
	return "Legend: " + strings.Join(parts, " | ")
}

func renderCell(c Cell) string {
	s := c.Display
	if c.HasHistory {
		s += " [H]"
	}
	if c.Truncated {
		s += " [T]"
	}
	if c.Unconverted {
		s += " [U]"
	}
	for _, issue := range c.Issues {
		s += " " + issue.Marker()
	}
	if c.StewardOverride != nil {
		s += " [S]"
	}
	if c.HasFlagHistory {
		s += " [F]"
	}
	return s
}

// RenderIssueRefs formats parsed tokens as the comma-joined
// "[A8817] email, [W55] whole-row" list the Issues column carries, with "-" for
// none and "[A?] <raw>" for a token whose id did not parse.
//
// Exported for the rule-evidence view (`qctl describe row`), which renders the
// same tokens off a different route: one formatter, so the two views cannot
// disagree about what a marker looks like.
func RenderIssueRefs(refs []IssueRef) string {
	return renderIssues(refs)
}

// renderIssues renders one row's alerts and warnings as "<marker> <target>",
// joined by ", ". The column lists EVERY token the row carries, including the
// ones no cell can show: a whole-row token, and a token naming a field this
// page does not display (a narrowed --fields, or a field the backend dropped).
//
// A malformed token — one whose id arm is not an integer — keeps its raw text
// under an unknown-id marker, so a wire-format change degrades to the raw
// column this display replaced instead of to an empty one.
func renderIssues(refs []IssueRef) string {
	if len(refs) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(refs))
	for _, ref := range refs {
		if ref.Malformed {
			parts = append(parts, "["+string(ref.Kind)+"?] "+escapeCell(ref.Raw))
			continue
		}
		target := escapeCell(ref.Field)
		if ref.Field == "" {
			target = "whole-row"
		}
		parts = append(parts, ref.Key().Marker()+" "+target)
	}
	return strings.Join(parts, ", ")
}

// renderState renders one row's folded quarantine-lifecycle tokens. A row with
// no tokens renders "-" — which on a quarantine page means the response carried
// the state columns but none of them said anything about this row.
func renderState(tokens []string) string {
	if len(tokens) == 0 {
		return "-"
	}
	return strings.Join(tokens, ", ")
}

func renderRules(uuids []string, aliaser RuleAliaser, cache map[string]string) (string, error) {
	if len(uuids) == 0 {
		return "-", nil
	}
	parts := make([]string, 0, len(uuids))
	for _, u := range uuids {
		alias, ok := cache[u]
		if !ok {
			a, err := aliaser.Alias(u)
			if err != nil {
				return "", fmt.Errorf("failed to allocate alias for rule %s: %w", u, err)
			}
			alias = a
			cache[u] = a
		}
		parts = append(parts, "[R: "+alias+"]")
	}
	return strings.Join(parts, ", "), nil
}

func renderSourceIDs(refs []SourceFileRef) string {
	if len(refs) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(refs))
	for _, r := range refs {
		parts = append(parts, strconv.FormatInt(r.StoredItemID, 10))
	}
	return strings.Join(parts, ", ")
}

// renderCommentThreads renders one row's threads as "<thread-id> [<n>] <targets>",
// joined by "; ". WholeRow and FieldNames are independent, so a thread that
// targets both shows both.
func renderCommentThreads(refs []ThreadRef) string {
	if len(refs) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(refs))
	for _, r := range refs {
		part := fmt.Sprintf("%s [%d]", r.ThreadID, r.CommentCount)
		if targets := renderThreadTargets(r); targets != "" {
			part += " " + targets
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, "; ")
}

func renderThreadTargets(r ThreadRef) string {
	fields := strings.Join(r.FieldNames, ",")
	switch {
	case r.WholeRow && fields != "":
		return "whole-row+" + fields
	case r.WholeRow:
		return "whole-row"
	default:
		return fields
	}
}
