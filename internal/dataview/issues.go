package dataview

import (
	"sort"
	"strconv"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/markdown"
	"github.com/qlustered/qctl/internal/user_submitted"
)

// The two reserved columns get_data returns carrying a row's issues. Both are
// in the response's `fields` by default and the backend re-adds them even when
// --fields narrows the request (get_limited_fields), so a page always has them:
// alert_item_ids_ on quarantined rows, warning_item_ids_ on quarantined AND
// clean rows.
const (
	alertTokensFieldName   = "alert_item_ids_"
	warningTokensFieldName = "warning_item_ids_"
)

// IssueKind says which of the two token columns a reference came from.
//
// Alert ids and warning ids are separate id spaces — alert 12 and warning 12
// are different objects, served by different routes — so the kind travels with
// every id and is never dropped.
type IssueKind string

const (
	IssueAlert   IssueKind = "A"
	IssueWarning IssueKind = "W"
)

// IssueID identifies one alert or warning: the key of the message dictionary
// and of a hydration request.
type IssueID struct {
	Kind IssueKind
	ID   int
}

// Marker is the [A8817] / [W55] form rendered on a cell and in the dictionary.
func (k IssueID) Marker() string {
	return "[" + string(k.Kind) + strconv.Itoa(k.ID) + "]"
}

// Label is the dictionary key, the marker without its brackets.
func (k IssueID) Label() string {
	return string(k.Kind) + strconv.Itoa(k.ID)
}

// IssueRef is one parsed alert_item_ids_ / warning_item_ids_ token.
//
// Field is the backend FIELD NAME, not the column label, and an empty Field is
// a whole-row token rather than a missing one. Malformed marks a token whose id
// arm is not an integer: Raw is then everything qctl knows about it, and it is
// rendered verbatim instead of being dropped.
type IssueRef struct {
	Kind      IssueKind
	ID        int
	Field     string
	Raw       string
	Malformed bool
}

// Key returns the dictionary key for a parsed ref. It is meaningless for a
// malformed one, which has no id.
func (r IssueRef) Key() IssueID {
	return IssueID{Kind: r.Kind, ID: r.ID}
}

// ParseIssueToken parses one token, following atlas's parse_issue_item_token
// (common/common/types.py) rather than the web UI's stricter reader:
//
//	"8817-email"      -> alert 8817 on the email cell
//	"8817-first-name" -> alert 8817 on the first-name cell (split on the FIRST
//	                     dash only; field names may contain dashes)
//	"8817-"           -> alert 8817 on the whole row
//
// cosmosview's parseIssueToken drops that last form (issue-token.utils.ts
// rejects an empty field arm). atlas writes it — format_issue_item_token
// spells a NULL field_name as the empty string — and atlas is the contract, so
// qctl keeps it as a whole-row reference.
//
// A token whose id arm is not an integer is returned Malformed rather than
// skipped: the backend formats these with format_issue_item_token, so an id
// that does not parse means the wire format moved, and dropping it would hide
// a flagged cell behind a silently empty column.
//
// Exported because the rule-evidence view (`qctl describe row`) reads the same
// tokens out of a different route: the same ids, spelled the same way, must not
// be parsed by two readers that can drift apart.
func ParseIssueToken(kind IssueKind, token string) IssueRef {
	// strings.Cut's found result is deliberately ignored: atlas's str.partition
	// treats a token with no dash at all as a whole-row token too.
	idPart, field, _ := strings.Cut(token, "-")
	id, err := strconv.Atoi(idPart)
	if err != nil {
		return IssueRef{Kind: kind, Raw: token, Malformed: true}
	}
	return IssueRef{Kind: kind, ID: id, Field: field, Raw: token}
}

// ParseIssueTokens parses a plain token list, for the callers that receive one
// as a JSON array of strings rather than as an opaque data cell — the
// rule-evidence route's alert_item_tokens / warning_item_tokens. An empty
// token is skipped, as in a data cell.
func ParseIssueTokens(kind IssueKind, tokens []string) []IssueRef {
	refs := make([]IssueRef, 0, len(tokens))
	for _, token := range tokens {
		if token == "" {
			continue
		}
		refs = append(refs, ParseIssueToken(kind, token))
	}
	return refs
}

// parseIssueCell parses one row's value for a token column. The cell is a JSON
// array of strings, or null on a row with no issues of that kind.
func parseIssueCell(kind IssueKind, cell interface{}) []IssueRef {
	arr, ok := cell.([]interface{})
	if !ok {
		return nil
	}
	refs := make([]IssueRef, 0, len(arr))
	for _, item := range arr {
		token, ok := item.(string)
		if !ok {
			// Tokens are strings by contract (format_issue_item_token), so a
			// non-string element means the wire moved. Keep it visible as a
			// malformed token rather than coercing, say, a bare 42 into a
			// whole-row reference to an alert nobody wrote.
			refs = append(refs, IssueRef{Kind: kind, Raw: formatValue(item), Malformed: true})
			continue
		}
		if token == "" {
			continue
		}
		refs = append(refs, ParseIssueToken(kind, token))
	}
	return refs
}

// IssueIDs returns the distinct ids of one kind on this page, ascending — the
// body of a hydration request, and the order of that kind's block in the
// dictionary. Malformed refs have no id and are excluded.
func (p *Page) IssueIDs(kind IssueKind) []int {
	seen := map[int]bool{}
	var ids []int
	for _, row := range p.Rows {
		for _, ref := range row.Issues {
			if ref.Malformed || ref.Kind != kind || seen[ref.ID] {
				continue
			}
			seen[ref.ID] = true
			ids = append(ids, ref.ID)
		}
	}
	sort.Ints(ids)
	return ids
}

// IssueMsg is one hydrated alert or warning: its message, and the user flags
// recorded against it.
type IssueMsg struct {
	Msg   string
	Flags []api.UserSubmittedIssueReadSchema
}

// AttachIssueMessages records the hydrated messages on the page and fixes the
// dictionary's order: alerts ascending, then warnings ascending.
//
// An id the server returned no message for is kept in the order with no entry
// in the map — the renderer says so rather than omitting the line, because a
// missing line would read as "this alert has no message" when it means the
// server did not answer for it: the batch routes select on id plus settings
// model, narrowed for an external user by associated data sources, and answer
// nothing for an id that misses. Resolving an issue is not one of the reasons
// — those handlers never read `resolved`.
//
// The message stays the backend's Markdown, since the page is a Markdown
// document, with one exception: a ~*value*~ marker is transport for a long
// value (atlas docs/message_value_markers.md), not Markdown a reader knows, and
// a Markdown renderer would take its stars for emphasis. Each one is replaced
// by its whole value, escaped as Markdown literal text so that nothing in it
// can change the formatting around it: markdown.EscapeValueMarkers escapes
// the inline syntax, and escapeCell then writes every line break in the message
// as a literal \n, since a real one could still start a block.
//
// An answered id with live flags on this page's rows also gets an
// IssueFlaggers entry; see issueFlaggers.
func AttachIssueMessages(page *Page, alertMsgs, warningMsgs map[int]IssueMsg) {
	page.ShowIssueMessages = true
	page.IssueMessages = map[IssueID]string{}
	page.IssueFlaggers = map[IssueID][]string{}
	page.IssueOrder = nil
	rowIDs := map[string]bool{}
	for _, row := range page.Rows {
		if row.ID != "" {
			rowIDs[row.ID] = true
		}
	}
	for _, kind := range []IssueKind{IssueAlert, IssueWarning} {
		msgs := alertMsgs
		if kind == IssueWarning {
			msgs = warningMsgs
		}
		for _, id := range page.IssueIDs(kind) {
			key := IssueID{Kind: kind, ID: id}
			page.IssueOrder = append(page.IssueOrder, key)
			msg, ok := msgs[id]
			if !ok {
				continue
			}
			page.IssueMessages[key] = escapeCell(markdown.EscapeValueMarkers(msg.Msg))
			if names := issueFlaggers(msg.Flags, rowIDs); len(names) > 0 {
				page.IssueFlaggers[key] = names
			}
		}
	}
}

// issueFlaggers returns the distinct submitters, by user id, of the live flags
// (neither retracted nor detached) whose row id is in rowIDs, ordered by each
// submitter's earliest such flag. Each name is FlaggerName escaped as Markdown
// literal text and then by escapeCell.
func issueFlaggers(flags []api.UserSubmittedIssueReadSchema, rowIDs map[string]bool) []string {
	live := make([]api.UserSubmittedIssueReadSchema, 0, len(flags))
	for _, f := range flags {
		if f.RetractedAt == nil && f.DetachedAt == nil && rowIDs[strconv.Itoa(f.RowID)] {
			live = append(live, f)
		}
	}
	sort.SliceStable(live, func(i, j int) bool { return live[i].CreatedAt.Before(live[j].CreatedAt) })
	seen := map[string]bool{}
	var names []string
	for _, f := range live {
		uid := f.SubmittedBy.ID.String()
		if seen[uid] {
			continue
		}
		seen[uid] = true
		names = append(names, escapeCell(markdown.EscapeInline(user_submitted.FlaggerName(f.SubmittedBy))))
	}
	return names
}
