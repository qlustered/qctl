package rows

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestMoveResults(t *testing.T) {
	for _, form := range []struct {
		verb string
		all  bool
	}{
		{"send-to-quarantine", false},
		{"send-to-quarantine", true},
		{"reprocess-clean", false},
		{"reprocess-clean", true},
	} {
		for _, tc := range []struct {
			name                    string
			moved, already, missing int
		}{
			{"singular", 1, 0, 0},
			{"plural", 8, 0, 0},
			{"mixed", 8, 2, 1},
			{"all missing", 0, 0, 2},
			{"all already", 0, 2, 0},
			{"already and missing", 0, 2, 1},
			{"empty", 0, 0, 0},
		} {
			t.Run(fmt.Sprintf("%s/all=%t/%s", form.verb, form.all, tc.name), func(t *testing.T) {
				mock := newRowsTestEnv(t)
				route := "move-rows-to-quarantine"
				args := []string{"rows", form.verb, "orders", "--yes"}
				if form.all {
					route = "move-all-rows-to-quarantine"
					args = append(args, "--all")
				} else {
					for id := 1; id <= max(1, tc.moved+tc.already+tc.missing); id++ {
						args = append(args, "--row-id", fmt.Sprint(id))
					}
				}
				record(mock, "POST", "/api/orgs/"+testOrgID+"/datasets/42/"+route,
					fmt.Sprintf(`{"result":"ok","moved":%d,"already_in_quarantine":%d,"not_found":%d}`, tc.moved, tc.already, tc.missing))
				stdout, stderr, err := runRows(t, args...)
				allMissing := !form.all && tc.moved == 0 && tc.already == 0 && tc.missing > 0
				wantCode := 0
				if allMissing {
					wantCode = 3
				}
				if code := qerrors.GetExitCode(err); code != wantCode {
					t.Fatalf("exit = %d, want %d: %v", code, wantCode, err)
				}
				if allMissing {
					if stdout != "" || !strings.Contains(err.Error(), "2 rows not found in table orders") ||
						!strings.Contains(err.Error(), "0 rows moved to quarantine; 0 already in quarantine") {
						t.Fatalf("all-missing result: stdout=%q, error=%v", stdout, err)
					}
					if strings.Contains(stderr, "Warning:") {
						t.Fatalf("missing count duplicated outside error: %q", stderr)
					}
					return
				}
				noun := "rows"
				if tc.moved == 1 {
					noun = "row"
				}
				intent := ""
				if form.verb == "reprocess-clean" {
					intent = " for reprocessing"
				}
				headline := fmt.Sprintf("%d %s moved to quarantine%s in table orders (id: 42)\n", tc.moved, noun, intent)
				if !strings.HasPrefix(stdout, headline) || strings.Contains(stderr, headline) {
					t.Errorf("headline streams: stdout=%q stderr=%q", stdout, stderr)
				}
				if (tc.already > 0) != strings.Contains(stdout, fmt.Sprintf("%d already in quarantine; skipped by this request (hold decision unchanged).", tc.already)) ||
					strings.Contains(stderr, "already in quarantine") {
					t.Errorf("skipped count streams: stdout=%q stderr=%q", stdout, stderr)
				}
				missingNoun := "rows"
				if tc.missing == 1 {
					missingNoun = "row"
				}
				warning := fmt.Sprintf("Warning: %d %s not found in table orders (id: 42).\n", tc.missing, missingNoun)
				if (tc.missing > 0) != strings.Contains(stderr, warning) || strings.Contains(stdout, "not found") {
					t.Errorf("missing count streams: stdout=%q stderr=%q", stdout, stderr)
				}
				if form.verb == "reprocess-clean" {
					if !strings.Contains(stdout, "Movement does not confirm completed validation.") ||
						!strings.Contains(stdout, "Rows may remain quarantined after validation depending on validation results and table/review policy.") ||
						!strings.Contains(stdout, "  qctl get data orders\n") {
						t.Errorf("reprocessing follow-up: %q", stdout)
					}
					if (tc.moved == 0) != strings.Contains(stdout, "No rows were sent for reprocessing.") {
						t.Errorf("no-op result: %q", stdout)
					}
					if (tc.already > 0) != strings.Contains(stdout, "These rows were not reprocessed by this request. To reprocess them, use:\n  qctl rows reprocess-quarantined orders --row-id <row-id> --yes\n") {
						t.Errorf("skipped-row follow-up: %q", stdout)
					}
				} else if !strings.Contains(stdout, "  qctl get data orders --is-bad-data\n") {
					t.Errorf("quarantine follow-up: %q", stdout)
				}
			})
		}
	}
}

func TestMoveResults_MissingCountsAreNotZero(t *testing.T) {
	for _, verb := range []string{"send-to-quarantine", "reprocess-clean"} {
		for _, body := range []string{`{"result":"ok"}`, `{"result":"ok","not_found":1}`} {
			t.Run(verb+body, func(t *testing.T) {
				mock := newRowsTestEnv(t)
				record(mock, "POST", "/api/orgs/"+testOrgID+"/datasets/42/move-rows-to-quarantine", body)
				stdout, _, err := runRows(t, "rows", verb, "orders", "--row-id", "7", "--yes")
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(stdout, "server did not return a moved count") || strings.Contains(stdout, "0 rows") {
					t.Errorf("absent counts fabricated: %q", stdout)
				}
			})
		}
	}
}

// Both follow-ups are runnable commands and both must retain the fact that the
// user supplied a NAME, even when that name is accepted by strconv.Atoi.
func TestMoveResults_NumericTableNameStaysANameInFollowUps(t *testing.T) {
	mock := newRowsTestEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("name"); got != "42" {
			t.Errorf("resolved name = %q, want 42", got)
		}
		testutil.RespondJSON(w, http.StatusOK,
			map[string]any{"total_rows": 1, "page": 1, "results": []map[string]any{{"id": 7, "name": "42", "state": "active"}}})
	})
	record(mock, "POST", "/api/orgs/"+testOrgID+"/datasets/7/move-rows-to-quarantine",
		`{"result":"ok","moved":0,"already_in_quarantine":1,"not_found":0}`)

	stdout, _, err := runRows(t, "rows", "reprocess-clean", "--table", "42", "--row-id", "7", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"qctl rows reprocess-quarantined --table 42 --row-id <row-id>",
		"qctl get data --table 42",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("numeric table name follow-up missing %q:\n%s", want, stdout)
		}
	}
}

func TestMoveResults_ReprocessSkippedSuggestionQuotesTableName(t *testing.T) {
	const tableName = "a$b;c"
	mock := newRowsTestEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("name"); got != tableName {
			t.Errorf("resolved name = %q, want %q", got, tableName)
		}
		testutil.RespondJSON(w, http.StatusOK,
			map[string]any{"total_rows": 1, "page": 1, "results": []map[string]any{{"id": 42, "name": tableName, "state": "active"}}})
	})
	record(mock, "POST", "/api/orgs/"+testOrgID+"/datasets/42/move-rows-to-quarantine",
		`{"result":"ok","moved":0,"already_in_quarantine":1,"not_found":0}`)

	stdout, _, err := runRows(t, "rows", "reprocess-clean", "--table", tableName, "--row-id", "7", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	var suggestion string
	for _, line := range strings.Split(stdout, "\n") {
		if strings.HasPrefix(line, "  qctl rows reprocess-quarantined ") {
			suggestion = strings.TrimPrefix(line, "  ")
			break
		}
	}
	if suggestion == "" {
		t.Fatalf("reprocess-quarantined suggestion missing:\n%s", stdout)
	}

	// Replace the human-facing placeholder with a concrete id before parsing
	// the line; an unquoted table metacharacter must still make shellWords fail.
	words := shellWords(t, strings.Replace(suggestion, "<row-id>", "7", 1))
	want := []string{"qctl", "rows", "reprocess-quarantined", tableName, "--row-id", "7", "--yes"}
	if !slices.Equal(words, want) {
		t.Errorf("suggestion splits to %q, want %q", words, want)
	}
}

func TestMoveResults_KeepHTTPErrorExitCodes(t *testing.T) {
	for _, verb := range []string{"send-to-quarantine", "reprocess-clean"} {
		for _, all := range []bool{false, true} {
			for _, tc := range []struct{ status, code int }{{404, 3}, {403, 4}, {422, 2}, {500, 1}} {
				t.Run(fmt.Sprintf("%s/all=%t/status=%d", verb, all, tc.status), func(t *testing.T) {
					mock := newRowsTestEnv(t)
					route := "move-rows-to-quarantine"
					args := []string{"rows", verb, "orders", "--yes"}
					if all {
						route = "move-all-rows-to-quarantine"
						args = append(args, "--all")
					} else {
						args = append(args, "--row-id", "7")
					}
					mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/42/"+route, func(w http.ResponseWriter, r *http.Request) {
						testutil.RespondJSON(w, tc.status, map[string]any{"detail": map[string]any{"msg": "move refused", "error_code": "MOVE_REFUSED"}})
					})
					stdout, _, err := runRows(t, args...)
					if code := qerrors.GetExitCode(err); code != tc.code {
						t.Fatalf("exit = %d, want %d: %v", code, tc.code, err)
					}
					if stdout != "" {
						t.Errorf("HTTP error printed success: %q", stdout)
					}
				})
			}
		}
	}
}
