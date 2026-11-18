package data_review //nolint:revive // Package name mirrors the hyphenated command.

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/data_reviews"
)

const errorCodeStandingReject = "REVIEW_HAS_STANDING_REJECT"

func explainStandingReject(err error, apiErr *data_reviews.APIError, group bool) error {
	var reviews []string
	var hints []string
	ids, _ := apiErr.Context["data_review_ids"].([]any)
	for _, raw := range ids {
		value, ok := raw.(string)
		if !ok {
			continue
		}
		id, parseErr := uuid.Parse(value)
		if parseErr == nil {
			reviews = append(reviews, "data-review/"+id.String())
			hints = append(hints, "  qctl describe data-review "+id.String())
		}
	}
	message := "Another reviewer has a standing whole-row reject."
	if len(reviews) > 0 {
		message += " Affected reviews: " + strings.Join(reviews, ", ") + "."
	}
	if group {
		message += " Cancellation changed nothing."
	}
	message += " Ask each rejecting reviewer to approve the row with qctl data-review approve <review-id> or withdraw their reject with qctl data-review withdraw <review-id>, then retry. Only the reviewer who recorded a reject can clear it."
	if len(hints) > 0 {
		message += "\nInspect the blocking reviews:\n" + strings.Join(hints, "\n")
	}
	return fmt.Errorf("%w\n%s", err, message)
}
