// Package dataview normalizes the backend get_data parallel-array response into a display-ready
// page model and renders it as an LLM-oriented Markdown payload.
package dataview

import "github.com/qlustered/qctl/internal/api"

// SourceFileRef is one provenance entry for a row (one stored item / file the row appeared in).
type SourceFileRef struct {
	StoredItemID int64
	Key          string // basename only, already escaped for single-line Markdown
}

// Cell is one rendered data cell with its annotations.
type Cell struct {
	Display        string    // escaped/sanitized; "-" for null/empty
	HasHistory     bool      // render [H]
	HasFlagHistory bool      // render [F]
	Truncated      bool      // render [T]
	Unconverted    bool      // render [U]: quarantine cell still carries source text
	Issues         []IssueID // the alerts/warnings flagging THIS cell; render [A8817] / [W55]

	StewardOverride *StewardOverride // render [S]: an active steward overwrite pins this value
}

// StewardOverride is the steward_overrides marker on one displayed cell.
type StewardOverride struct {
	Field        string // backend field name, escaped
	Displaced    string // the value the pin replaced, formatted for its column and escaped
	HasDisplaced bool   // false when base_displaced_value is null; Displaced is then empty
}

// ThreadRef is one comment thread touching a row, from the by-rows overlay.
//
// WholeRow and FieldNames are INDEPENDENT: a thread may target the row as a
// whole (a NULL field_name target) and some of its cells at the same time.
// Render both; never infer either from the other being empty.
type ThreadRef struct {
	ThreadID     string
	CommentCount int
	WholeRow     bool
	FieldNames   []string
}

// Row is one result row aligned to Page.Headers, plus its out-of-band columns.
type Row struct {
	History        []FieldHistory  // cell logs and flag events, in field-name order
	Cells          []Cell          // 1:1 with Page.Headers
	RuleUUIDs      []string        // dataset_rule_ids for this row (may be empty)
	SourceFiles    []SourceFileRef // provenance for this row (may be empty)
	CommentThreads []ThreadRef     // unresolved threads on this row (may be empty)
	Issues         []IssueRef      // alert then warning tokens on this row (may be empty)
	State          []string        // folded quarantine-lifecycle tokens (may be empty)
	ID             string          // the row's id cell, which keys ROW SIGNATURES ("" if the page has no id column)
	Signature      string          // folded initial_signature_, the hex `qctl rows delete-* --signature` takes
}

// FieldHistory is the display text for one cell's logs and flag events.
type FieldHistory struct {
	Field string
	Lines []string
}

// Page is the normalized, display-ready model for one backend page.
type Page struct {
	DatasetName  string
	CleanRows    int
	BadRows      int
	Page         int // resolved page, or the requested page when omitted by the server
	Anchored     bool
	TotalPages   int
	MatchingRows int
	Showing      int

	// LiveRows and DeletedRows split MatchingRows by deletion state; the
	// summary prints them when ShowDeletedCounts is set.
	LiveRows          int
	DeletedRows       int
	ShowDeletedCounts bool

	Headers []string
	Rows    []Row

	ShowRuleColumn  bool // render a Triggered Rules column
	ShowSourceCol   bool // render a Source Files column
	ShowCommentCol  bool // render a Comment Threads column
	ShowIssueCol    bool // render an Issues column (the response carried a token column)
	ShowStateCol    bool // render a Row State column (QuarantineState or AnyDeleted)
	QuarantineState bool // a quarantine-state column was folded, or the response carried approval_hold_reasons, waiting_for_approval or sent_or_changed_by_me
	AnyDeleted      bool // a row on the page carries a folded deleted=/row-delete= token
	ShowSignatures  bool // write a ROW SIGNATURES dictionary (the response carried initial_signature_)

	// Folded and HiddenColumns are the reserved columns this page did not
	// render raw — the input to the summary's disclosure line. A folded column
	// reaches the reader through the compact form its group names; a hidden one
	// is dropped and reachable only with --fields <name> or -o json.
	Folded        []FoldedGroup
	HiddenColumns []string

	AnyHistory      bool
	AnyFlagHistory  bool
	ShowCellHistory bool
	RawRowLogs      bool // row_logs_ is rendered as a data column
	AnyTruncated    bool
	AnyUnconverted  bool
	AnyRules        bool
	AnyComments     bool
	AnyAlerts       bool
	AnyWarnings     bool

	// RowDeletes is the response's row_deletes, in response order.
	RowDeletes []RowDeleteLine

	AnyStewardOverrides bool // a displayed cell carries a StewardOverride
	AnySelfExcluded     bool // a row carries sent_or_changed_by_me=true

	// MaskedFields is the response's masked_fields, escaped: every field the
	// viewer sees masked, displayed or not.
	MaskedFields []string

	SourceFiles     map[int64]string // referenced stored_item_id -> escaped basename
	SourceFileOrder []int64          // stable first-seen order for the dictionary

	// Set by AttachIssueMessages only (--include-issues). ShowIssueMessages is
	// what says the dictionary was asked for, so an empty page still prints the
	// section instead of looking like the flag was ignored.
	ShowIssueMessages bool
	IssueMessages     map[IssueID]string   // escaped message per hydrated id
	IssueFlaggers     map[IssueID][]string // escaped flagger names per hydrated id with live flags on this page's rows, earliest first
	IssueOrder        []IssueID            // alerts ascending, then warnings ascending
}

// FoldedGroup names one compact form and the reserved columns it consumed, so
// the disclosure line can say where a column went rather than only that it is
// gone.
type FoldedGroup struct {
	Into    string   // the compact form, named as the notice reads it: one of foldIssues/foldState/foldSignatures
	Columns []string // the reserved columns it consumed, in response order
}

// NormalizeOptions carries the request flags that affect normalization.
type NormalizeOptions struct {
	RequestedPage  int
	IncludeRuleIDs bool // --include-dataset-rule-ids
	ShowMetadata   bool // --show-metadata

	// RequestedFields is the field names the request sent. A reserved column
	// named here was explicitly asked for, so it is rendered as an ordinary
	// column instead of being folded or hidden. It does NOT narrow anything on
	// its own: the backend re-adds the whole reserved set to a narrowed request
	// (get_limited_fields), which is why the folding exists at all.
	RequestedFields []string

	// ShowRowLogs folds row_logs_ into CELL HISTORY unless explicitly named in RequestedFields.
	ShowRowLogs bool

	// DeletedRows is the --deleted-rows value the request sent, or "" when it
	// sent none. Any value but live prints the live/deleted split.
	DeletedRows api.DeletedRows
}
