package data_review //nolint:revive // Package name mirrors the hyphenated command.

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

func closedReview() map[string]any {
	r := aReview(nil)
	r["status"] = "closed"
	r["closed_at"] = "2026-09-16T11:00:00Z"
	return r
}

func TestClose_NonInteractiveWithoutYesSendsNothing(t *testing.T) {
	requireNonInteractive(t)
	e := newTestEnv(t)
	e.respondReviewerPOST("close", http.StatusOK, closedReview())

	_, _, err := e.exec(t, "close", reviewA)
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want 2 (err: %v)", got, err)
	}
	if got := e.mock.Requests(); len(got) != 0 {
		t.Errorf("requests = %v, want none", got)
	}
}

func TestClose_YesPostsOnce(t *testing.T) {
	e := newTestEnv(t)
	bodies := e.respondReviewerPOST("close", http.StatusOK, closedReview())

	stdout, _, err := e.exec(t, "close", reviewA, "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := e.mock.Requests(), []string{"POST " + reviewerRoute("close")}; strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("requests = %v, want %v", got, want)
	}
	if len(*bodies) != 1 {
		t.Errorf("POST bodies = %d, want 1", len(*bodies))
	}
	want := "data-review/" + reviewA + " closed (status: closed)\n" +
		"Next: qctl describe data-review " + reviewA + "\n"
	if stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
}

func TestClose_PromptDeclineSendsNothing(t *testing.T) {
	e := newTestEnv(t)
	e.respondReviewerPOST("close", http.StatusOK, closedReview())
	cmdutil.SetReader(strings.NewReader("n\n"))
	defer cmdutil.ResetReader()

	_, _, err := e.exec(t, "close", reviewA)
	if !errors.Is(err, cmdutil.ErrDeclined) {
		t.Errorf("err = %v, want ErrDeclined", err)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitGenericError {
		t.Errorf("exit code = %d, want 1", got)
	}
	if got := e.mock.Requests(); len(got) != 0 {
		t.Errorf("requests = %v, want none", got)
	}
}

func TestClose_PromptAcceptPostsOnce(t *testing.T) {
	e := newTestEnv(t)
	e.respondReviewerPOST("close", http.StatusOK, closedReview())
	cmdutil.SetReader(strings.NewReader("y\n"))
	defer cmdutil.ResetReader()

	if _, _, err := e.exec(t, "close", reviewA); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := e.posts(); got != 1 {
		t.Errorf("POSTs = %d, want 1", got)
	}
}

func TestClose_PromptListsTheConsequencesThenAsks(t *testing.T) {
	want := strings.Join([]string{
		"The row stays in quarantine — this does not release it.",
		"It leaves the review queue.",
		"Its comments and whole-row decisions won't carry over if it's submitted again.",
		"This can't be undone.",
		"Close review " + reviewA + "?",
	}, "\n")
	if got := closePrompt(reviewA); got != want {
		t.Errorf("prompt =\n%s\nwant\n%s", got, want)
	}
}

func TestClose_RefusalsKeepTheirExitCodes(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   map[string]any
		want   int
	}{
		{"not active, no error code", http.StatusUnprocessableEntity,
			map[string]any{"detail": map[string]any{"msg": "DataReview is already closed (status: approved)", "error_id": ""}},
			qerrors.ExitBadUsage},
		{"forbidden", http.StatusForbidden,
			map[string]any{"detail": map[string]any{"msg": "Forbidden"}}, qerrors.ExitUnauthorized},
		{"not found", http.StatusNotFound,
			map[string]any{"detail": map[string]any{"msg": "Data review not found"}}, qerrors.ExitNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestEnv(t)
			e.respondReviewerPOST("close", tc.status, tc.body)

			_, _, err := e.exec(t, "close", reviewA, "--yes")
			if got := qerrors.GetExitCode(err); got != tc.want {
				t.Errorf("exit code = %d, want %d (err: %v)", got, tc.want, err)
			}
		})
	}
}

func TestClose_MalformedIDSendsNothing(t *testing.T) {
	e := newTestEnv(t)
	_, _, err := e.exec(t, "close", "0190aaaa", "--yes")
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want 2 (err: %v)", got, err)
	}
	if got := e.mock.Requests(); len(got) != 0 {
		t.Errorf("requests = %v, want none", got)
	}
}
