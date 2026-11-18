package get

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestGetDataAnchorUsesResolvedPage(t *testing.T) {
	for _, tc := range []struct {
		name     string
		pageArgs []string
		resolved int
		wantNext bool
	}{
		{"default page", nil, 3, true},
		{"explicit page overridden", []string{"--page", "99"}, 3, true},
		{"last page", []string{"--page", "99"}, 4, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			args := append([]string{"1", "--anchor-row-id", "8943", "--limit", "7", "--is-bad-data",
				"--search-query", "example", "--order-by", "~email"}, tc.pageArgs...)
			out := runData(t, args, func(w http.ResponseWriter, r *http.Request) {
				calls++
				for key, want := range map[string]string{
					"anchor_row_id": "8943", "limit": "7", "is_bad_data": "true",
					"search_query": "example", "order_by": "~email",
				} {
					if got := r.URL.Query().Get(key); got != want {
						t.Errorf("query %s = %q, want %q", key, got, want)
					}
				}
				resp := sampleGetData()
				resp.Page = &tc.resolved
				resp.TotalPageCount = 4
				resp.TotalRows = 24
				testutil.RespondJSON(w, http.StatusOK, resp)
			})
			if calls != 1 {
				t.Errorf("data requests = %d, want 1", calls)
			}
			wantPage := "Page: 3 of 4"
			if !tc.wantNext {
				wantPage = "Page: 4 of 4"
			}
			if !strings.Contains(out, wantPage) || !strings.Contains(out, "8943") {
				t.Errorf("missing resolved page or anchor row:\n%s", out)
			}
			if got := strings.Contains(out, "More rows available"); got != tc.wantNext {
				t.Errorf("next-page hint present = %v, want %v:\n%s", got, tc.wantNext, out)
			}
			if tc.wantNext && !strings.Contains(out, "remove --anchor-row-id and fetch the next page with --page 4") {
				t.Errorf("hint must remove the anchor and continue from the resolved page:\n%s", out)
			}
		})
	}
}

func TestGetDataAnchorStructuredOutputKeepsResolvedPage(t *testing.T) {
	outputs := make(map[string]string)
	for _, format := range []string{"json", "yaml"} {
		outputs[format] = runData(t, []string{"1", "--anchor-row-id", "8943", "--page", "99", "-o", format},
			func(w http.ResponseWriter, _ *http.Request) {
				resp := sampleGetData()
				page := 3
				resp.Page = &page
				resp.TotalPageCount = 4
				testutil.RespondJSON(w, http.StatusOK, resp)
			})
	}
	var resp struct {
		Page    int     `json:"page"`
		Results [][]any `json:"results"`
	}
	if err := json.Unmarshal([]byte(outputs["json"]), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Page != 3 || len(resp.Results) != 1 || resp.Results[0][0] != float64(8943) {
		t.Errorf("raw output lost resolved page or row: %+v", resp)
	}
	testutil.AssertYAMLReadsAsJSON(t, outputs["yaml"], outputs["json"])
}

func TestGetDataAnchorNotFoundNamesTheSelection(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{"default clean", nil, []string{"clean rows (--is-bad-data=false)"}},
		{"quarantine filters", []string{"--is-bad-data", "--search-query", "example", "--stored-item-id", "7",
			"--filter-by-field-name", "email", "--filter-by-field-value", "a@b.com", "--alert-item-id", "8817"},
			[]string{"quarantined rows (--is-bad-data)", `--search-query "example"`, "--stored-item-id 7",
				`--filter-by-field-name "email"`, `--filter-by-field-value "a@b.com"`, "--alert-item-id 8817"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := dataTestMock(t)
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1/data", func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusNotFound, map[string]any{"detail": "The anchor row is not in the filtered rows."})
			})
			args := append([]string{"1", "--anchor-row-id", "8943", "--page", "99", "--limit", "7",
				"--fields", "email", "--show-row-logs", "--order-by", "email", "-o", "json"}, tc.args...)
			out, err := execData(t, args)
			if qerrors.GetExitCode(err) != qerrors.ExitNotFound {
				t.Fatalf("error = %v, want exit 3; output:\n%s", err, out)
			}
			for _, want := range append(tc.want, "The anchor row is not in the filtered rows.",
				"Requested row 8943 in table 1", "The row may exist outside this selection") {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error missing %q: %v", want, err)
				}
			}
			for _, unwanted := range []string{"--anchor-row-id", "--page", "--limit", "--fields", "--show-row-logs", "--order-by", "--output"} {
				if strings.Contains(err.Error(), unwanted) {
					t.Errorf("error lists display/navigation option %q as a filter: %v", unwanted, err)
				}
			}
			if calls := mock.Requests(); len(calls) != 1 {
				t.Errorf("requests = %v, want only the selected bucket's data request", calls)
			}
		})
	}
}

func TestGetDataAnchorAdviceOnlyOnAnchoredNotFound(t *testing.T) {
	for _, tc := range []struct {
		name   string
		args   []string
		status int
		code   int
	}{
		{"ordinary not-found", nil, http.StatusNotFound, qerrors.ExitNotFound},
		{"anchored forbidden", []string{"--anchor-row-id", "8943"}, http.StatusForbidden, qerrors.ExitUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := dataTestMock(t)
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1/data", func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, tc.status, map[string]any{"detail": "Server diagnostic"})
			})
			_, err := execData(t, append([]string{"1"}, tc.args...))
			if qerrors.GetExitCode(err) != tc.code {
				t.Fatalf("error = %v, want exit %d", err, tc.code)
			}
			if !strings.Contains(err.Error(), "Server diagnostic") || strings.Contains(err.Error(), "outside this selection") {
				t.Errorf("unexpected diagnostic: %v", err)
			}
		})
	}
}

func TestGetDataAnchorRejectsNonPositiveValuesBeforeLookup(t *testing.T) {
	for _, tc := range []struct{ flag, value string }{
		{"anchor-row-id", "0"}, {"anchor-row-id", "-1"}, {"limit", "0"}, {"limit", "-1"},
	} {
		t.Run(tc.flag+"/"+tc.value, func(t *testing.T) {
			mock := dataTestMock(t)
			_, err := execData(t, []string{"orders", "--anchor-row-id", "8943", "--" + tc.flag, tc.value})
			if qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
				t.Fatalf("error = %v, want exit 2", err)
			}
			if !strings.Contains(err.Error(), "--"+tc.flag+" must be a positive integer") {
				t.Errorf("unexpected diagnostic: %v", err)
			}
			if calls := mock.Requests(); len(calls) != 0 {
				t.Errorf("invalid flag caused requests: %v", calls)
			}
		})
	}
}
