package dataset_rules //nolint:revive // Public package name matches the resource domain.

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const rowFilterCountPath = "/api/orgs/" + testOrgID + "/datasets/3/dataset-rules/" + testRuleID + "/row-filter-test"

func TestRowFilterCountReadsTheRulesCount(t *testing.T) {
	mock, c := newInstantiateTestClient(t)
	mock.RegisterHandler("GET", rowFilterCountPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]any{
			"matched": 10000, "total": 52000, "capped": true, "predicate_errors": 3, "first_predicate_error": "boom",
		})
	})

	count, err := c.RowFilterCount("test-token", 3, testRuleID)
	if err != nil {
		t.Fatalf("RowFilterCount: %v", err)
	}
	if got, want := mock.Requests(), []string{"GET " + rowFilterCountPath}; !slices.Equal(got, want) {
		t.Errorf("requests = %v, want %v", got, want)
	}
	if count.Matched != 10000 || count.Total != 52000 || !count.Capped || count.PredicateErrors != 3 ||
		count.FirstPredicateError == nil || *count.FirstPredicateError != "boom" {
		t.Errorf("count = %+v", count)
	}
}

func TestRowFilterCountExitCodes(t *testing.T) {
	for _, tt := range []struct {
		name     string
		status   int
		body     any
		wantExit int
		wantText string
	}{
		{
			// A stored filter that no longer compiles: atlas sends no error_code.
			name:     "409",
			status:   http.StatusConflict,
			body:     map[string]any{"detail": map[string]any{"msg": "Cannot evaluate this rule's row filter: column 'region' is not in this table"}},
			wantExit: qerrors.ExitBadUsage,
			wantText: "Cannot evaluate this rule's row filter",
		},
		{
			// A detail the generated JSON409 cannot decode fails the parse.
			name:     "409 with a string detail",
			status:   http.StatusConflict,
			body:     map[string]any{"detail": "Cannot evaluate this rule's row filter"},
			wantExit: qerrors.ExitBadUsage,
		},
		{
			name:     "404",
			status:   http.StatusNotFound,
			body:     map[string]any{"detail": map[string]any{"msg": "Table Rule not found"}},
			wantExit: qerrors.ExitNotFound,
		},
		{
			name:     "403",
			status:   http.StatusForbidden,
			body:     map[string]any{"detail": map[string]any{"msg": "Permission denied"}},
			wantExit: qerrors.ExitUnauthorized,
		},
		{
			name:     "500",
			status:   http.StatusInternalServerError,
			body:     map[string]any{"detail": "Internal Server Error"},
			wantExit: qerrors.ExitGenericError,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			mock, c := newInstantiateTestClient(t)
			mock.RegisterHandler("GET", rowFilterCountPath, func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, tt.status, tt.body)
			})

			count, err := c.RowFilterCount("test-token", 3, testRuleID)
			if err == nil {
				t.Fatalf("RowFilterCount accepted a %d: %+v", tt.status, count)
			}
			if code := qerrors.GetExitCode(err); code != tt.wantExit {
				t.Errorf("exit code = %d, want %d: %v", code, tt.wantExit, err)
			}
			if tt.wantText != "" && !strings.Contains(err.Error(), tt.wantText) {
				t.Errorf("error = %q, want it to contain %q", err, tt.wantText)
			}
		})
	}
}

func TestRowFilterCountRefusesABadRuleIDWithoutARequest(t *testing.T) {
	mock, c := newInstantiateTestClient(t)
	if _, err := c.RowFilterCount("test-token", 3, "not-a-uuid"); err == nil {
		t.Fatal("RowFilterCount accepted a malformed rule ID")
	}
	if got := mock.Requests(); len(got) != 0 {
		t.Errorf("requests = %v, want none", got)
	}
}
