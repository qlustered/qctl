// Package notifications is the domain client for the per-user notification
// inbox: review requests, mentions, review decisions, rule upgrades, severe
// rule bugs and validation failures, all delivered to one list.
//
// READ AND DISMISSED ARE TWO INDEPENDENT AXES. The backend keeps read_at and
// dismissed_at as separate columns, with separate single-item endpoints and
// separate bulk operations, and it never sets one from the other. Nothing in
// this package or its callers may collapse them into a single status.
package notifications

import (
	"strings"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

// Type is one notification_type value.
type Type = api.NotificationType

// Types is the complete notification_type vocabulary, in the order atlas
// declares it (common/common/types.py, class NotificationType).
//
// Every element is an api.* constant rather than a string literal, so a
// RENAMED value fails to compile. TestTypesCoversTheGeneratedEnum catches the
// other half: a value ADDED to the spec that nobody added here.
var Types = []Type{
	api.RuleUpgrade,
	api.ReviewRequested,
	api.ReviewAssigned,
	api.CommentMentioned,
	api.ExternalIssueComment,
	api.CommentReply,
	api.ReviewDecision,
	api.ValidationFailed,
	api.DataReviewRequested,
	api.DataReviewAssigned,
	api.DataReviewDecision,
	api.DataReviewCleanAffected,
	api.DataReviewGroupAssigned,
	api.SevereRuleBug,
	api.FileReadyToDownload,
	api.FileDownloadFailed,
	api.CorrectionsNotCarried,
	api.QuarantinedRows,
	api.EmailUndeliverable,
	api.ProblemOnOurSide,
	api.SeriesEscalation,
	api.PeriodSummary,
	api.SupportRequest,
	api.FileNotLoaded,
	api.ChangeProposalSubmitted,
	api.ChangeProposalDecided,
	api.ChangeProposalExecutionFailed,
	api.IssuesReportSent,
	api.PiiAccessRequested,
	api.PiiAccessRequestDecided,
	api.IssuesReportUpdated,
	api.SeriesRuleApprovalStale,
	api.DuplicateGroupRowsChanged,
	api.RowsMovedToQuarantine,
	api.DuplicateGroupDecisionChanged,
	api.DuplicateGroupResponse,
	api.DuplicateThreadShared,
	api.FileNeedsDecision,
	api.DailySummary,
}

// needsYou mirrors atlas NEEDS_YOU_NOTIFICATION_TYPES (common/common/types.py)
// for the unread badge's needs_you count.
var needsYou = map[Type]bool{
	api.CommentMentioned:        true,
	api.ReviewRequested:         true,
	api.ReviewAssigned:          true,
	api.DataReviewRequested:     true,
	api.DataReviewAssigned:      true,
	api.DataReviewGroupAssigned: true,
	api.FileReadyToDownload:     true,
	api.EmailUndeliverable:      true,
	api.SeriesEscalation:        true,
	api.SupportRequest:          true,
	api.ChangeProposalSubmitted: true,
	api.PiiAccessRequested:      true,
	api.SeriesRuleApprovalStale: true,
	api.FileNeedsDecision:       true,
}

// IsNeedsYou reports whether a type is counted in the badge's needs_you half.
// A file_needs_decision row counts only while its alert is unresolved.
func IsNeedsYou(t Type) bool { return needsYou[t] }

// TypeNames returns Types as strings, in declaration order. It is what builds
// the --type flag's help text, so the vocabulary a user is shown and the
// vocabulary the CLI accepts cannot drift apart.
func TypeNames() []string {
	out := make([]string, len(Types))
	for i, t := range Types {
		out[i] = string(t)
	}
	return out
}

// ParseTypes turns raw --type values into the enum, refusing an unknown one at
// exit 2 before any request is made.
//
// The check is not optional politeness: dismiss-by-type puts the value in the
// URL PATH, so an unchecked value is a 422 from the server rather than a
// filter the server ignores. Repeats are collapsed, because dismiss issues one
// request per type and a duplicate would spend a request dismissing nothing.
func ParseTypes(values []string) ([]Type, error) {
	if len(values) == 0 {
		return nil, nil
	}
	valid := make(map[string]Type, len(Types))
	for _, t := range Types {
		valid[string(t)] = t
	}
	seen := make(map[Type]bool, len(values))
	out := make([]Type, 0, len(values))
	for _, v := range values {
		t, ok := valid[v]
		if !ok {
			return nil, qerrors.NewBadUsagef(
				"unknown notification type %q\nvalid types: %s", v, strings.Join(TypeNames(), ", "))
		}
		if seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out, nil
}
