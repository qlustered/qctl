package data_review //nolint:revive // Package name mirrors the hyphenated command.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// assign, unassign, close and re-request write the server's response under
// -o json and name the review under -o name; every result line moves to
// stderr.
func TestReviewerVerbs_WriteTheResponse(t *testing.T) {
	cases := []struct {
		name  string
		setup func(e *testEnv)
		args  []string
	}{
		{"assign", func(e *testEnv) {
			e.respondUsers(http.StatusOK)
			e.respondReview(http.StatusOK, reviewWith("open"))
			e.respondReviewerPOST("assign", http.StatusOK, reviewWith("open", userAda))
		}, []string{"assign", reviewA, "--reviewer", userAda}},
		// Nobody is left to assign, so no POST: the review it read is the result.
		{"assign unchanged", func(e *testEnv) {
			e.respondUsers(http.StatusOK)
			e.respondReview(http.StatusOK, reviewWith("open", userAda))
		}, []string{"assign", reviewA, "--reviewer", userAda}},
		{"unassign", func(e *testEnv) {
			e.respondUsers(http.StatusOK)
			e.respondReviewerPOST("unassign", http.StatusOK, reviewWith("open"))
		}, []string{"unassign", reviewA, "--reviewer", userAda}},
		{"close", func(e *testEnv) {
			e.respondReviewerPOST("close", http.StatusOK, closedReview())
		}, []string{"close", reviewA, "--yes"}},
		{"re-request", func(e *testEnv) {
			e.respondReviewerPOST("re-request", http.StatusOK, rerequested("open", userAda))
		}, []string{"re-request", reviewA, "--reviewer", userAda}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestEnv(t)
			tc.setup(e)
			stdout, stderr, err := e.exec(t, append(tc.args, "-o", "json")...)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			var got map[string]any
			if err := json.Unmarshal([]byte(stdout), &got); err != nil {
				t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
			}
			id, _ := got["id"].(string)
			if id == "" {
				id, _ = got["data_review_id"].(string)
			}
			if id != reviewA {
				t.Errorf("document names review %q, want %s:\n%s", id, reviewA, stdout)
			}
			if !strings.Contains(stderr, "Next: qctl describe data-review "+reviewA) {
				t.Errorf("stderr missing the Next: line:\n%s", stderr)
			}

			stdout, _, err = e.exec(t, append(tc.args, "-o", "name")...)
			if err != nil {
				t.Fatalf("-o name: unexpected error: %v", err)
			}
			if want := "data-review/" + reviewA + "\n"; stdout != want {
				t.Errorf("-o name stdout = %q, want %q", stdout, want)
			}
		})
	}
}
