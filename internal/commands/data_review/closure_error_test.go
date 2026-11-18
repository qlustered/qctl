package data_review //nolint:revive // Package name mirrors the hyphenated command.

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/data_reviews"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

func TestClosure_StandingReject(t *testing.T) {
	for _, group := range []bool{false, true} {
		for _, tc := range []struct {
			name    string
			ids     any
			wantIDs []string
		}{
			{"one", []string{reviewA}, []string{reviewA}},
			{"multiple", []string{reviewA, reviewB}, []string{reviewA, reviewB}},
			{"missing", nil, nil},
			{"malformed", "unexpected", nil},
			{"mixed", []any{reviewA, 42, "bad\x1b[31m"}, []string{reviewA}},
		} {
			name := "close/" + tc.name
			if group {
				name = "cancel/" + tc.name
			}
			t.Run(name, func(t *testing.T) {
				e := newTestEnv(t)
				body := map[string]any{"detail": map[string]any{
					"msg": "Standing reject from server", "error_code": errorCodeStandingReject,
					"context": map[string]any{"data_review_ids": tc.ids},
				}}
				if group {
					e.respondGroupPOST("cancel", http.StatusConflict, body)
				} else {
					e.respondReviewerPOST("close", http.StatusConflict, body)
				}
				var stdout string
				var err error
				if group {
					stdout, _, err = e.execGroup(t, "cancel", groupID, "--yes", "-o", "json")
				} else {
					stdout, _, err = e.exec(t, "close", reviewA, "--yes", "-o", "json")
				}
				if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
					t.Fatalf("exit = %d, want 2: %v", got, err)
				}
				var apiErr *data_reviews.APIError
				if !errors.As(err, &apiErr) || apiErr.Code != errorCodeStandingReject {
					t.Fatalf("lost API error: %v", err)
				}
				for _, text := range append([]string{"Standing reject from server", "whole-row reject", "qctl data-review approve <review-id>", "qctl data-review withdraw <review-id>", "Only the reviewer who recorded a reject"}, tc.wantIDs...) {
					if !strings.Contains(err.Error(), text) {
						t.Errorf("error %q missing %q", err, text)
					}
				}
				if group && !strings.Contains(err.Error(), "Cancellation changed nothing") {
					t.Errorf("error = %v", err)
				}
				var hints []string
				for _, id := range tc.wantIDs {
					hints = append(hints, "  qctl describe data-review "+id)
				}
				if len(hints) > 0 && !strings.HasSuffix(err.Error(), "Inspect the blocking reviews:\n"+strings.Join(hints, "\n")) {
					t.Errorf("error = %q, want blocking review hint lines %v", err, hints)
				}
				if got := strings.Count(err.Error(), "qctl describe data-review "); got != len(hints) {
					t.Errorf("describe hints = %d, want %d", got, len(hints))
				}
				if stdout != "" {
					t.Errorf("stdout = %q, want no success output", stdout)
				}
				if got := e.posts(); got != 1 {
					t.Errorf("POSTs = %d, want 1", got)
				}
			})
		}
	}
}
