package review

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestCloseReviewCommand(t *testing.T) {
	mock := newReviewTestEnv(t)
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-reviews/"+testReviewUUID.String()+"/close",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, sampleReview(api.ReviewRequestStatusClosed))
		})

	cmd := setupReviewTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"review", "close", testReviewUUID.String()})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "rule-review/" + testReviewUUID.String() + " closed (status: closed)"
	if !strings.Contains(buf.String(), want) {
		t.Errorf("output missing %q\n%s", want, buf.String())
	}
}

func TestReopenReviewCommand(t *testing.T) {
	mock := newReviewTestEnv(t)
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-reviews/"+testReviewUUID.String()+"/reopen",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, sampleReviewDetail(api.ReviewRequestStatusOpen))
		})

	cmd := setupReviewTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"review", "reopen", testReviewUUID.String()})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "rule-review/"+testReviewUUID.String()+" reopened (status: open)") {
		t.Errorf("missing reopened line\n%s", out)
	}
	if !strings.Contains(out, "Next: qctl describe rule-review "+testReviewUUID.String()) {
		t.Errorf("missing next hint\n%s", out)
	}
}

func TestReopenSupersededReportsBackendStatus(t *testing.T) {
	mock := newReviewTestEnv(t)
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-reviews/"+testReviewUUID.String()+"/reopen",
		func(w http.ResponseWriter, r *http.Request) {
			// Slot already held by a sibling active review → lands closed.
			testutil.RespondJSON(w, http.StatusOK, sampleReviewDetail(api.ReviewRequestStatusClosed))
		})

	cmd := setupReviewTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"review", "reopen", testReviewUUID.String()})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "rule-review/" + testReviewUUID.String() +
		" superseded by an active review for the same scope (status: closed)"
	if !strings.Contains(buf.String(), want) {
		t.Errorf("output missing %q\n%s", want, buf.String())
	}
	if strings.Contains(buf.String(), "reopened (status:") {
		t.Errorf("superseded reopen must not claim 'reopened'\n%s", buf.String())
	}
}

// The status is a free-form string on the wire, not a Go enum: the JSON decode
// puts whatever the server sent into api.ReviewRequestStatus. These three
// commands echo it straight to stdout, so an ESC in it would recolour the
// terminal from a line the user reads as qctl's own.
func TestLifecycleCommandsSanitizeTheServerStatus(t *testing.T) {
	const crafted = "closed\x1b[31m\x00"
	for _, tc := range []struct {
		name  string
		route string
		verb  string
		reply func() interface{}
	}{
		{"close", "/close", "close", func() interface{} { return sampleReview(crafted) }},
		{"reopen", "/reopen", "reopen", func() interface{} { return sampleReviewDetail(crafted) }},
		{"withdraw", "/withdraw-decision", "withdraw", func() interface{} { return sampleReviewDetail(crafted) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := newReviewTestEnv(t)
			mock.RegisterHandler("POST",
				"/api/orgs/"+testOrgID+"/rule-reviews/"+testReviewUUID.String()+tc.route,
				func(w http.ResponseWriter, _ *http.Request) {
					testutil.RespondJSON(w, http.StatusOK, tc.reply())
				})

			out, err := runReviewCommand(t, "", "review", tc.verb, testReviewUUID.String())
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if strings.ContainsRune(out, '\x1b') || strings.ContainsRune(out, '\x00') {
				t.Fatalf("status reached the terminal with a live control byte: %q", out)
			}
			if !strings.Contains(out, `closed\x1b[31m\x00`) {
				t.Errorf("missing the visible escape: %q", out)
			}
		})
	}
}
