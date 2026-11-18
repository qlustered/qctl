// Package user_submitted is the shared kernel behind the ad-hoc alert and
// warning verbs — `qctl alert submit|retract` and `qctl warning submit|retract`.
//
// Both kinds POST and DELETE the SAME two request schemas against two pairs of
// routes that differ only in the word "alerts"/"warnings". Everything that is
// not the route lives here so the two verbs cannot drift: the caps, the
// request builders, the per-row result rendering and the exit-code decision.
//
// It is deliberately cobra-free and imports nothing but internal/api and
// internal/errors, so internal/alerts and internal/warnings can both depend on
// the schemas without depending on each other or on the command layer.
package user_submitted //nolint:revive // Matches the backend's own name for this feature.

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

// Kind names the aggregate table a request is aimed at. It exists so one
// renderer can write "alert/8817" or "warning/8817" without a bool argument
// whose polarity the next reader has to look up.
type Kind string

const (
	// KindAlert holds the row in quarantine until the flag is retracted or the
	// flagged value changes.
	KindAlert Kind = "alert"
	// KindWarning is advisory: it never holds a row.
	KindWarning Kind = "warning"
)

// The backend's own caps, mirrored here so an over-cap request is a usage
// error the user can read instead of a 422 they have to decode. Same reasoning
// as comment_thread.maxTargetRows.
const (
	// MaxRows is UserSubmittedIssueCreateSchema.rows max_length and
	// UserSubmittedIssueRetractSchema.row_ids max_length — the same number on
	// both schemas.
	MaxRows = 500
	// MaxCommentChars is UserSubmittedIssueCreateSchema.comment max_length.
	// Pydantic counts characters, so this is compared in runes.
	MaxCommentChars = 2000
)

// Type aliases to the generated schemas (frozen cross-phase contract), so the
// command layer and both domain clients name one set of types.
type (
	CreateSchema  = api.UserSubmittedIssueCreateSchema
	CreateResult  = api.UserSubmittedIssueCreateResponseSchema
	Result        = api.UserSubmittedIssueResultSchema
	RowTarget     = api.UserSubmittedIssueRowSchema
	Suggestion    = api.ValueRecommendationTinySchema
	RetractSchema = api.UserSubmittedIssueRetractSchema
	RetractResult = api.UserSubmittedIssueRetractResponseSchema
	RetractRow    = api.UserSubmittedIssueRetractResultSchema
)

// SubmitRequest is what a submit verb reads off its flags, before any of it
// has been checked.
type SubmitRequest struct {
	Rows    []int
	Fields  []string
	Comment string
	Suggest []string
}

// Build validates the request and returns the wire body.
//
// Targets are the cross product of --row and --field: every named field on
// every named row, which is comment-thread create's rule and the reason the
// two arcs read the same. The wire also allows a different field set per row;
// qctl does not expose that — submit one issue per shape instead.
//
// Unlike a comment thread there is NO whole-row form: field_names is
// min_length=1 on every row, so a fieldless request is a usage error rather
// than a differently-shaped one.
func (r SubmitRequest) Build() (CreateSchema, error) {
	if err := validateRows(r.Rows, "--row is required: name at least one quarantined row to flag"); err != nil {
		return CreateSchema{}, err
	}
	if len(r.Fields) == 0 {
		return CreateSchema{}, qerrors.NewBadUsage(
			"--field is required: a user-submitted issue always names at least one cell (there is no whole-row form)")
	}
	for _, field := range r.Fields {
		if strings.TrimSpace(field) == "" {
			return CreateSchema{}, qerrors.NewBadUsage("--field must name a field, got an empty value")
		}
	}
	// Trim once, here: this local is both what gets checked and what goes on
	// the wire, so the cap cannot come to mean something different from what is
	// sent. There is no canonical_comment on the backend — issue_pattern folds
	// record.comment raw — so leaving the whitespace on
	// `--comment "$(cat note.txt)"` would silently mint a SECOND aggregate for
	// what is one objection. Measuring the trimmed value also keeps the cap
	// exactly the backend's: a comment the server would accept is not refused
	// here over whitespace that never reaches it.
	comment := strings.TrimSpace(r.Comment)
	if comment == "" {
		return CreateSchema{}, qerrors.NewBadUsage("--comment is empty: a user-submitted issue is the sentence you write on it")
	}
	if n := utf8.RuneCountInString(comment); n > MaxCommentChars {
		return CreateSchema{}, qerrors.NewBadUsagef(
			"--comment is %d characters, over the %d-character limit", n, MaxCommentChars)
	}

	body := CreateSchema{Comment: comment, Rows: make([]RowTarget, 0, len(r.Rows))}
	for _, row := range r.Rows {
		// A fresh slice per row: the backend canonicalises (dedupes and sorts)
		// field_names server-side, but sharing one backing array across rows
		// would let a future in-place edit reach every target at once.
		names := append([]string(nil), r.Fields...)
		body.Rows = append(body.Rows, RowTarget{RowID: row, FieldNames: names})
	}

	suggestions, err := buildSuggestions(r.Suggest)
	if err != nil {
		return CreateSchema{}, err
	}
	body.SuggestedValues = suggestions
	return body, nil
}

// buildSuggestions maps --suggest to the wire's optional recommendation list.
//
// `strong` is sent explicitly, and it is always true. The schema defaults the
// field to true and the view materialises that default on write
// (`[item.model_dump() for item in request_body.suggested_values]`), so an
// omitted `strong` is not "no claim" — it is already stored as a strong one.
// qctl states it rather than inheriting it: true is the right reading of a
// value a human typed by hand — they are asserting it is probably the valid
// value — and pinning it client-side survives a change to the backend's
// default. It does not touch aggregate identity: the signature folds only
// `[item["value"] ...]` into issue_pattern, never `strong`.
//
// The value list itself IS a signature ingredient (canonical_suggested_values
// sorts it for exactly that reason), so an empty list must be sent as null
// rather than [] — nil here, which marshals to null.
func buildSuggestions(values []string) (*[]Suggestion, error) {
	if len(values) == 0 {
		return nil, nil
	}
	out := make([]Suggestion, 0, len(values))
	for _, value := range values {
		// Only the exact empty string is refused, and the value is never
		// trimmed: a suggested value is DATA and whitespace can legitimately be
		// part of it, whereas a field NAME cannot be whitespace. That is why
		// this check and the TrimSpace one on --field differ on purpose.
		if value == "" {
			return nil, qerrors.NewBadUsage("--suggest must carry a value, got an empty one")
		}
		strong := true
		out = append(out, Suggestion{Value: value, Strong: &strong})
	}
	return &out, nil
}

// RetractRequest is what a retract verb reads off its argument and flags.
type RetractRequest struct {
	// IssueItemID is AlertItem.id / WarningItem.id — the same id
	// `qctl get alerts` and `qctl get warnings` print, NOT the per-row record
	// UUID.
	IssueItemID int
	Rows        []int
}

// Build validates the request and returns the wire body.
func (r RetractRequest) Build() (RetractSchema, error) {
	if r.IssueItemID <= 0 {
		return RetractSchema{}, qerrors.NewBadUsagef(
			"expected a positive alert/warning id, got %d", r.IssueItemID)
	}
	if err := validateRows(r.Rows, "--row is required: name at least one row to retract the flag from"); err != nil {
		return RetractSchema{}, err
	}
	return RetractSchema{IssueItemID: r.IssueItemID, RowIds: append([]int(nil), r.Rows...)}, nil
}

// validateRows applies the three row-list rules both schemas share.
//
// Uniqueness is not a qctl invention: both schemas carry a
// require_unique_row_ids validator, and its docstring says why — a repeat is
// answered twice in `results`, one row with two entries, which no client can
// reconcile against the ids it sent.
func validateRows(rows []int, emptyMessage string) error {
	switch {
	case len(rows) == 0:
		return qerrors.NewBadUsage(emptyMessage)
	case len(rows) > MaxRows:
		return qerrors.NewBadUsagef("--row was given %d times, over the %d-row limit", len(rows), MaxRows)
	}
	seen := make(map[int]bool, len(rows))
	for _, row := range rows {
		// Row ids are database ids, so a non-positive one cannot match
		// anything. Left to the backend it comes back as a row_not_found
		// result, which does not read as "you typed the flag wrong".
		if row <= 0 {
			return qerrors.NewBadUsagef("--row must be a positive row id, got %d", row)
		}
		if seen[row] {
			return qerrors.NewBadUsagef("--row %d was given twice; each row may appear only once", row)
		}
		seen[row] = true
	}
	return nil
}

// describeRow renders one row for a message: "row 1042 (amount, currency)",
// or just "row 1042" when the field list is empty — which is what a
// field_invalid result carries.
func describeRow(rowID int, fieldNames []string) string {
	if len(fieldNames) == 0 {
		return fmt.Sprintf("row %d", rowID)
	}
	return fmt.Sprintf("row %d (%s)", rowID, strings.Join(fieldNames, ", "))
}
