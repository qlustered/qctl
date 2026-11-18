package data_review //nolint:revive // Package name mirrors the hyphenated command.

import (
	"net/http"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
)

// changeReviewDenied is a change review 403 as atlas sends it.
func changeReviewDenied(operation string) map[string]any {
	return map[string]any{"detail": map[string]any{
		"msg":        "This table's change review policy does not let the member role resolve this change.",
		"error_code": "CHANGE_REVIEW_POLICY_DENIED",
		"context":    map[string]any{"operation": operation, "verb": "resolve", "denied_keys": []any{[]any{"rows"}}},
	}}
}

// A change review 403 is not about the caller's permission or grant, so the
// permission note the other 403s get would point the reader at the wrong fix.
func TestChangeReviewRefusalGetsNoPermissionNote(t *testing.T) {
	cases := map[string]struct {
		respond func(e *testEnv)
		run     func(e *testEnv, t *testing.T) error
	}{
		"accept-cell": {
			func(e *testEnv) {
				e.respondAction(amountHistoryID, http.StatusForbidden, changeReviewDenied("data_review_decide"))
			},
			func(e *testEnv, t *testing.T) error {
				_, _, err := e.exec(t, "accept-cell", reviewA, "--history-id", "811")
				return err
			},
		},
		"approve": {
			func(e *testEnv) {
				e.respondBatch("approve", http.StatusForbidden, changeReviewDenied("data_review_approve"))
			},
			func(e *testEnv, t *testing.T) error {
				_, _, err := e.exec(t, "approve", reviewA)
				return err
			},
		},
		"close": {
			func(e *testEnv) {
				e.respondReviewerPOST("close", http.StatusForbidden, changeReviewDenied("data_review_close"))
			},
			func(e *testEnv, t *testing.T) error {
				_, _, err := e.exec(t, "close", reviewA, "--yes")
				return err
			},
		},
		"group cancel": {
			func(e *testEnv) {
				e.respondGroupPOST("cancel", http.StatusForbidden, changeReviewDenied("data_review_close"))
			},
			func(e *testEnv, t *testing.T) error {
				_, _, err := e.execGroup(t, "cancel", groupID, "--yes")
				return err
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := newTestEnv(t)
			tc.respond(e)

			err := tc.run(e, t)
			if got := qerrors.GetExitCode(err); got != qerrors.ExitUnauthorized {
				t.Fatalf("exit code = %d, want %d (err: %v)", got, qerrors.ExitUnauthorized, err)
			}
			if strings.Contains(err.Error(), "DATA_REVIEW_") {
				t.Errorf("err = %q, want no permission note on a change review 403", err)
			}
			if !strings.Contains(err.Error(), "qctl get change-review-policy") {
				t.Errorf("err = %q, want the change review hint", err)
			}
		})
	}
}
