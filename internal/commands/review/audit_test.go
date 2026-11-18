package review

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

func registerAudit(mock *testutil.MockAPIServer, total int) {
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-reviews/"+testReviewUUID.String()+"/audit-log",
		func(w http.ResponseWriter, r *http.Request) {
			details := map[string]interface{}{"reviewer_count": 2, "source": "cli"}
			testutil.RespondJSON(w, http.StatusOK, api.ReviewAuditListSchema{
				AuditEntries: []api.ReviewAuditEntrySchema{{
					ID:        uuid.MustParse("dddddddd-1111-2222-3333-444444444444"),
					Action:    "auto_enable_withheld_dry_run", // NOT in the OpenAPI enum comment
					ActorID:   testReviewerUUID,
					Actor:     &api.UserInfoTinyDictSchema{ID: testReviewerUUID, Email: "alice@corp.com", FirstName: "Alice", LastName: "Chen"},
					CreatedAt: time.Now().Add(-2 * time.Hour),
					Details:   &details,
				}},
				Page: 1, Limit: 20, Total: total,
			})
		})
}

func TestAuditRendersVerbatimActionAndDetails(t *testing.T) {
	mock := newReviewTestEnv(t)
	registerAudit(mock, 1)

	cmd := setupReviewTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"review", "audit", testReviewUUID.String()})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"auto_enable_withheld_dry_run", // verbatim, never enum-validated
		"Alice Chen",
		"reviewer_count=2 source=cli", // sorted k=v pairs
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n%s", want, out)
		}
	}
}

func TestAuditPaginationHint(t *testing.T) {
	mock := newReviewTestEnv(t)
	registerAudit(mock, 45)

	cmd := setupReviewTestCommand()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"review", "audit", testReviewUUID.String()})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "More audit entries — fetch the next page with --page 2."
	if !strings.Contains(errOut.String(), want) {
		t.Errorf("stderr missing %q\n%s", want, errOut.String())
	}
}

func TestAuditNoHintOnLastPage(t *testing.T) {
	mock := newReviewTestEnv(t)
	registerAudit(mock, 1)

	cmd := setupReviewTestCommand()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"review", "audit", testReviewUUID.String()})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(errOut.String(), "More audit entries") {
		t.Errorf("unexpected hint on last page\n%s", errOut.String())
	}
}

func TestAuditRawJSON(t *testing.T) {
	mock := newReviewTestEnv(t)
	registerAudit(mock, 1)

	cmd := setupReviewTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"review", "audit", testReviewUUID.String(), "-o", "json"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	// Raw wire response: audit_entries key + open-ended action, no table headers.
	if !strings.Contains(out, `"audit_entries"`) || !strings.Contains(out, `"auto_enable_withheld_dry_run"`) {
		t.Errorf("raw JSON payload missing wire keys\n%s", out)
	}
	if strings.Contains(out, "ACTOR") {
		t.Errorf("json output must not contain table headers\n%s", out)
	}
}

func TestAuditRejectsNonPositivePagination(t *testing.T) {
	for _, tc := range []struct{ flag, value string }{
		{"page", "0"}, {"page", "-1"}, {"limit", "0"}, {"limit", "-20"},
	} {
		t.Run(tc.flag+"/"+tc.value, func(t *testing.T) {
			mock := newReviewTestEnv(t)

			calls := 0
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-reviews/"+testReviewUUID.String()+"/audit-log",
				func(w http.ResponseWriter, _ *http.Request) {
					calls++
					testutil.RespondJSON(w, http.StatusOK, api.ReviewAuditListSchema{Page: 1, Limit: 20, Total: 0})
				})

			cmd := setupReviewTestCommand()
			var buf bytes.Buffer
			cmd.SetOut(&buf)
			cmd.SetErr(&buf)
			cmd.SetArgs([]string{"review", "audit", testReviewUUID.String(), "--" + tc.flag, tc.value})

			err := cmd.Execute()
			if err == nil {
				t.Fatalf("expected an error for --%s %s", tc.flag, tc.value)
			}
			want := "--" + tc.flag + " must be a positive integer, got " + tc.value
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error should contain %q, got: %v", want, err)
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
			}
			if calls != 0 {
				t.Errorf("expected the audit-log endpoint never to be called, got %d call(s)", calls)
			}
		})
	}
}

// TestFormatAuditDetailsPrintsWholeNumbersAsIntegers: the generated client
// decodes a JSON number in details as a float64, which %v alone prints in
// exponent form from a million up.
func TestFormatAuditDetailsPrintsWholeNumbersAsIntegers(t *testing.T) {
	for _, tc := range []struct {
		value interface{}
		want  string
	}{
		{1234567.0, "rows=1234567"},
		{-1234567.0, "rows=-1234567"},
		{1.5, "rows=1.5"},
	} {
		details := map[string]interface{}{"rows": tc.value}
		if got := formatAuditDetails(&details); got != tc.want {
			t.Errorf("formatAuditDetails(%#v) = %q, want %q", tc.value, got, tc.want)
		}
	}
}

// An audit entry has no name, email or id field, so -o name used to print
// nothing at exit 0. It is now refused before any request.
func TestAuditRefusesNameOutput(t *testing.T) {
	mock := newReviewTestEnv(t)
	registerAudit(mock, 1)

	cmd := setupReviewTestCommand()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"review", "audit", testReviewUUID.String(), "-o", "name"})

	err := cmd.Execute()
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage ||
		!strings.Contains(err.Error(), `unknown output format "name"`) {
		t.Errorf("exit = %d (%v), want %d and the format refusal", code, err, qerrors.ExitBadUsage)
	}
}
