package describe

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/testutil"
)

const summarySuggestionID = "77777777-7777-7777-7777-777777777777"

func TestDescribeTable_SuggestionBlockersMatchTheDraftGate(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fields map[string]any
		blocks int
	}{
		{"update on this draft", nil, 1},
		{"delete on this draft", map[string]any{"action": "delete"}, 1},
		{"another migration", map[string]any{"migration_model_id": 99}, 0},
		{"no migration", map[string]any{"migration_model_id": nil}, 0},
		{"validated candidate", map[string]any{
			"is_revalidation_candidate": true, "validation_dry_run_job_id": 81, "validated_at": "2026-10-05T00:00:00Z",
		}, 0},
		{"validated delete candidate", map[string]any{
			"action": "delete", "is_revalidation_candidate": true, "validation_dry_run_job_id": 81, "validated_at": "2026-10-05T00:00:00Z",
		}, 0},
		{"candidate still validating", map[string]any{
			"is_revalidation_candidate": true, "validation_dry_run_job_id": 81,
		}, 1},
		{"candidate without dry run", map[string]any{
			"is_revalidation_candidate": true, "validated_at": "2026-10-05T00:00:00Z",
		}, 1},
		{"ordinary suggestion with validation stamps", map[string]any{
			"is_revalidation_candidate": false, "validation_dry_run_job_id": 81, "validated_at": "2026-10-05T00:00:00Z",
		}, 1},
		{"decided since the list", map[string]any{"state": "dismissed"}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock, env := describeTableMock(t)
			defer mock.Close()
			defer env.Cleanup()
			detail := map[string]any{
				"id": summarySuggestionID, "instance_name": "s1", "action": "update",
				"state": "pending", "source": "user", "migration_model_id": 7,
			}
			for key, value := range tc.fields {
				detail[key] = value
			}
			if detail["action"] == "delete" {
				mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/12/rule-suggestions", func(w http.ResponseWriter, _ *http.Request) {
					body := strings.Replace(summarySuggestionsJSON, `"action": "update"`, `"action": "delete"`, 1)
					testutil.RespondJSON(w, http.StatusOK, json.RawMessage(body))
				})
			}
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/12/rule-suggestions/"+summarySuggestionID,
				func(w http.ResponseWriter, _ *http.Request) { testutil.RespondJSON(w, http.StatusOK, detail) })
			assertSummarySuggestionCounts(t, 2, tc.blocks)
		})
	}
}

func assertSummarySuggestionCounts(t *testing.T, pending, blocking int) {
	t.Helper()
	for _, format := range []string{"table", outputFormatJSON, outputFormatYAML} {
		out, stderr, err := runDescribeTable(t, "12", "-o", format)
		if err != nil {
			t.Fatalf("-o %s: %v", format, err)
		}
		if strings.Contains(stderr, "suggestions unavailable") {
			t.Fatalf("-o %s: unexpected degradation: %s", format, stderr)
		}
		if format == "table" {
			want := fmt.Sprintf("Suggestions:  %d pending (%d block the migration)", pending, blocking)
			if !strings.Contains(out, want) {
				t.Errorf("missing %q:\n%s", want, out)
			}
		} else {
			section := structuredPayload(t, format, out)["suggestions"].(map[string]any)
			for key, want := range map[string]int{"pending": pending, "blocking": blocking} {
				if got := fmt.Sprint(section[key]); got != strconv.Itoa(want) {
					t.Errorf("-o %s: suggestions.%s = %s, want %d", format, key, got, want)
				}
			}
			if section["unavailable"] != nil {
				t.Errorf("-o %s: suggestions unexpectedly unavailable: %v", format, section)
			}
		}
		if got := strings.Contains(out, "pending update/delete rule suggestions block the migration"); got != (blocking > 0) {
			t.Errorf("-o %s: suggestion blocker present = %v, want %v:\n%s", format, got, blocking > 0, out)
		}
	}
}

func TestDescribeTable_SuggestionBlockersReadEveryPage(t *testing.T) {
	mock, env := describeTableMock(t)
	defer mock.Close()
	defer env.Cleanup()
	first := make([]api.DatasetRuleSuggestionTinySchema, summaryRulePageSize)
	for i := range first {
		first[i] = api.DatasetRuleSuggestionTinySchema{
			ID: uuid.New(), Action: api.SuggestionActionCreate, State: api.SuggestionStatePending, Source: api.Profiling,
		}
	}
	pages := []int{}
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/12/rule-suggestions", func(w http.ResponseWriter, r *http.Request) {
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		pages = append(pages, page)
		if r.URL.Query().Get("state") != "pending" || r.URL.Query().Get("limit") != strconv.Itoa(summaryRulePageSize) {
			t.Errorf("unexpected query: %s", r.URL.RawQuery)
		}
		results := first
		var next *api.PaginationSchema
		switch page {
		case 1:
			next = &api.PaginationSchema{}
		case 2:
			results = []api.DatasetRuleSuggestionTinySchema{{
				ID: uuid.MustParse(summarySuggestionID), Action: api.SuggestionActionUpdate, State: api.SuggestionStatePending, Source: api.User,
			}}
		default:
			t.Errorf("unexpected page %d", page)
			results = nil
		}
		total := summaryRulePageSize + 1
		testutil.RespondJSON(w, http.StatusOK, api.DatasetRuleSuggestionListSchema{
			Results: results, TotalRows: &total, Page: &page, Next: next,
		})
	})
	assertSummarySuggestionCounts(t, summaryRulePageSize+1, 1)
	if fmt.Sprint(pages) != "[1 2 1 2 1 2]" {
		t.Errorf("pages = %v, want both pages in each format", pages)
	}
}

func TestDescribeTable_NoDraftHasNoSuggestionBlockers(t *testing.T) {
	mock, env := describeTableMock(t)
	defer mock.Close()
	defer env.Cleanup()
	base := "/api/orgs/" + testOrgID + "/datasets/12"
	mock.RegisterHandler("GET", base+"/migration-details", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(summaryProducedSchemaJSON))
	})
	mock.RegisterHandler("GET", base+"/rule-suggestions/"+summarySuggestionID, func(w http.ResponseWriter, _ *http.Request) {
		t.Error("read suggestion details despite having no draft")
		testutil.RespondError(w, http.StatusForbidden, "forbidden")
	})
	assertSummarySuggestionCounts(t, 2, 0)
}

func TestDescribeTable_DisappearedSuggestionDoesNotBlock(t *testing.T) {
	mock, env := describeTableMock(t)
	defer mock.Close()
	defer env.Cleanup()
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/12/rule-suggestions/"+summarySuggestionID,
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondError(w, http.StatusNotFound, "suggestion disappeared")
		})
	assertSummarySuggestionCounts(t, 2, 0)
}

func TestDescribeTable_UnknownSuggestionGateDegrades(t *testing.T) {
	for _, failure := range []string{"schema", "detail", "later page"} {
		t.Run(failure, func(t *testing.T) {
			mock, env := describeTableMock(t)
			defer mock.Close()
			defer env.Cleanup()
			base := "/api/orgs/" + testOrgID + "/datasets/12"
			path := base + "/migration-details"
			if failure == "detail" {
				path = base + "/rule-suggestions/" + summarySuggestionID
			}
			if failure == "later page" {
				mock.RegisterHandler("GET", base+"/rule-suggestions", func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Query().Get("page") == "1" {
						body := strings.Replace(summarySuggestionsJSON, `"page": 1`, `"page": 1, "next": {"start": 2}`, 1)
						testutil.RespondJSON(w, http.StatusOK, json.RawMessage(body))
						return
					}
					testutil.RespondError(w, http.StatusForbidden, "forbidden")
				})
			} else {
				mock.RegisterHandler("GET", path, func(w http.ResponseWriter, _ *http.Request) {
					testutil.RespondError(w, http.StatusForbidden, "forbidden")
				})
			}
			for _, format := range []string{"table", outputFormatJSON, outputFormatYAML} {
				out, stderr, err := runDescribeTable(t, "12", "-o", format)
				if err != nil {
					t.Fatalf("-o %s: a section failure must retain the summary: %v", format, err)
				}
				if !strings.Contains(stderr, "warning: suggestions unavailable:") {
					t.Errorf("-o %s: missing suggestions warning: %s", format, stderr)
				}
				if strings.Contains(out, "pending update/delete rule suggestions block the migration") {
					t.Errorf("-o %s: unknown gate claimed a blocker:\n%s", format, out)
				}
				if format == "table" {
					if !strings.Contains(out, "Suggestions:  (unavailable:") {
						t.Errorf("unknown suggestions gate must be unavailable:\n%s", out)
					}
				} else {
					section := structuredPayload(t, format, out)["suggestions"].(map[string]any)
					if section["unavailable"] == nil || fmt.Sprint(section["blocking"]) != "0" {
						t.Errorf("-o %s: unknown gate = %v", format, section)
					}
				}
			}
		})
	}
}
