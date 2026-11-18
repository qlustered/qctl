package rows

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const rowsSeriesID = "5a1e5000-0000-4000-8000-000000000001"

func registerSeriesRowScope(mock *testutil.MockAPIServer) {
	table := `{"id":42,"name":"orders","state":"active","series":{"id":"` + rowsSeriesID + `","name":"monthly"},"period":{"period_key":"2026-09"}}`
	source := `{"id":7,"name":"monthly-source","dataset_id":40,"table_series_id":"` + rowsSeriesID + `","routing_mode":"uploader_chooses"}`
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"total_rows":1,"page":1,"results":[`+table+`]}`))
	})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(table))
	})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/data-sources/7", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(source))
	})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/data-sources", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"total_rows":1,"page":1,"results":[`+source+`]}`))
	})
}

func TestRowsSeriesSourceTargetsSelectedPeriod(t *testing.T) {
	cases := []struct {
		verb, route, body string
		selector          []string
		population        string
	}{
		{"delete-quarantined", "delete-rows", testSignatureBody, []string{"--signature", "deadbeef"}, "true"},
		{"delete-quarantined", "delete-all-bad-rows", testCommentBody, []string{"--all"}, ""},
		{"reprocess-quarantined", "reprocess-all-bad-rows", "", []string{"--all"}, ""},
		{"hold", "update-keep-in-quarantine", `{"ids":[11],"quarantine_hold":"hold"}`, []string{"--row-id", "11"}, ""},
		{"hold", "update-keep-in-quarantine", `{"ids":null,"quarantine_hold":"hold","quarantine_status":"on_hold"}`, []string{"--status", "on_hold"}, ""},
		{"hold", "update-keep-in-quarantine", `{"ids":null,"quarantine_hold":"hold"}`, []string{"--all"}, ""},
		{"release", "update-keep-in-quarantine", `{"ids":[11],"quarantine_hold":"release"}`, []string{"--row-id", "11"}, ""},
		{"release", "update-keep-in-quarantine", `{"ids":null,"quarantine_hold":"release","quarantine_status":"on_hold"}`, []string{"--status", "on_hold"}, ""},
		{"release", "update-keep-in-quarantine", `{"ids":null,"quarantine_hold":"release"}`, []string{"--all"}, ""},
		{"reset-hold", "update-keep-in-quarantine", `{"ids":[11],"quarantine_hold":"undecided"}`, []string{"--row-id", "11"}, ""},
		{"reset-hold", "update-keep-in-quarantine", `{"ids":null,"quarantine_hold":"undecided","quarantine_status":"on_hold"}`, []string{"--status", "on_hold"}, ""},
		{"reset-hold", "update-keep-in-quarantine", `{"ids":null,"quarantine_hold":"undecided"}`, []string{"--all"}, ""},
	}
	for _, tc := range cases {
		for _, refs := range []struct {
			name, tableRef string
			args           []string
		}{
			{"ids", "42", []string{"--table-id", "42", "--cloud-source-id", "7"}},
			{"names", "orders", []string{"--table", "orders", "--cloud-source", "monthly-source"}},
			{"positional", "42", []string{"42", "--cloud-source-id", "7"}},
		} {
			t.Run(tc.verb+"/"+strings.Join(tc.selector, " ")+"/"+refs.name, func(t *testing.T) {
				mock := newRowsTestEnv(t)
				registerSeriesRowScope(mock)
				got := record(mock, "POST", "/api/orgs/"+testOrgID+"/data-sources/7/"+tc.route, `{"result":"ok","affected_rows":2}`)
				args := append([]string{"rows", tc.verb}, refs.args...)
				args = append(args, tc.selector...)
				if strings.HasPrefix(tc.verb, "delete-") {
					args = append(args, "--reason", testReason)
				}
				stdout, _, err := runRows(t, append(args, "--yes")...)
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				query, err := url.ParseQuery(got.query)
				if err != nil {
					t.Fatal(err)
				}
				if got.hits != 1 || query.Get("dataset_id") != "42" || query.Get("is_bad_data") != tc.population {
					t.Errorf("mutation: hits %d, query %q; want selected period 42 and population %q", got.hits, got.query, tc.population)
				}
				if got.body != tc.body {
					t.Errorf("body = %s, want %s", got.body, tc.body)
				}
				if !strings.Contains(stdout, "qctl get data "+refs.tableRef+" --data-source-model-id 7") {
					t.Errorf("follow-up lost the selected period or source: %s", stdout)
				}
			})
		}
	}
}

func TestRowsSeriesSourceTemplateStillSendsDatasetID(t *testing.T) {
	mock := newRowsTestEnv(t)
	registerSeriesRowScope(mock)
	got := record(mock, "POST", "/api/orgs/"+testOrgID+"/data-sources/7/reprocess-all-bad-rows", `{"result":"ok","affected_rows":2}`)
	if _, _, err := runRows(t, "rows", "reprocess-quarantined", "--table-id", "40", "--cloud-source-id", "7", "--all", "--yes"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.hits != 1 || got.query != "dataset_id=40" {
		t.Errorf("mutation: hits %d, query %q; want dataset_id=40", got.hits, got.query)
	}
}

func TestRowsSeriesSourceRefusesNonMemberBeforeMutation(t *testing.T) {
	for _, table := range []string{
		`{"id":42,"name":"outside","state":"active"}`,
		`{"id":42,"name":"other-series","state":"active","series":{"id":"5a1e5000-0000-4000-8000-000000000002"},"period":{"period_key":"2026-09"}}`,
	} {
		t.Run(table, func(t *testing.T) {
			mock := newRowsTestEnv(t)
			registerSeriesRowScope(mock)
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42", func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, json.RawMessage(table))
			})
			got := record(mock, "POST", "/api/orgs/"+testOrgID+"/data-sources/7/delete-all-bad-rows", `{"result":"ok"}`)
			_, _, err := runRows(t, "rows", "delete-quarantined", "--reason", testReason, "--table-id", "42", "--cloud-source-id", "7", "--all", "--yes")
			if qerrors.GetExitCode(err) != 2 || !strings.Contains(err.Error(), "not table") {
				t.Fatalf("error = %v (exit %d), want non-member exit 2", err, qerrors.GetExitCode(err))
			}
			if got.hits != 0 {
				t.Errorf("mutations = %d, want 0", got.hits)
			}
		})
	}
}

func TestRowsSourceRequiresExplicitTable(t *testing.T) {
	for _, verb := range []string{"delete-quarantined", "reprocess-quarantined", "hold", "release", "reset-hold"} {
		t.Run(verb, func(t *testing.T) {
			newRowsTestEnv(t)
			args := []string{"rows", verb, "--cloud-source-id", "7"}
			if strings.HasPrefix(verb, "delete-") {
				args = append(args, "--reason", testReason)
			}
			args = append(args, "--all")
			_, _, err := runRows(t, append(args, "--yes")...)
			if qerrors.GetExitCode(err) != 2 || !strings.Contains(err.Error(), "--table") {
				t.Fatalf("error = %v (exit %d), want missing-table exit 2", err, qerrors.GetExitCode(err))
			}
		})
	}
}

func TestRowsSeriesSourceReleasePreservesApprovalProgress(t *testing.T) {
	mock := newRowsTestEnv(t)
	registerSeriesRowScope(mock)
	got := record(mock, "POST", "/api/orgs/"+testOrgID+"/data-sources/7/update-keep-in-quarantine",
		approvalAnswer(2, 1, []int{11}, approvalEntry(11, 2, 2), approvalEntry(12, 1, 2)))
	stdout, stderr, err := runRows(t, "rows", "release", "--table-id", "42", "--cloud-source-id", "7", "--all", "--yes", "-o", "json")
	assertExitZero(t, err, stderr)
	var result struct {
		AffectedRows   int   `json:"affected_rows"`
		SkippedRows    int   `json:"skipped_rows"`
		ReleasedRowIDs []int `json:"released_row_ids"`
		Approvals      []struct {
			Current  int `json:"current_approvals"`
			Required int `json:"required_approvals"`
		} `json:"approvals"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatal(err)
	}
	if got.query != "dataset_id=42" || result.AffectedRows != 2 || result.SkippedRows != 1 ||
		len(result.ReleasedRowIDs) != 1 || result.ReleasedRowIDs[0] != 11 || len(result.Approvals) != 2 ||
		result.Approvals[1].Current != 1 || result.Approvals[1].Required != 2 {
		t.Errorf("query %q, result %s: lost period or approval progress", got.query, stdout)
	}
}

func TestRowsSeriesSourceSurfacesServerRefusal(t *testing.T) {
	mock := newRowsTestEnv(t)
	registerSeriesRowScope(mock)
	message := "The target table does not belong to this cloud source."
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/data-sources/7/delete-all-bad-rows", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("dataset_id") != "42" {
			t.Error("missing selected period")
		}
		testutil.RespondJSON(w, http.StatusBadRequest, map[string]interface{}{"detail": map[string]string{"msg": message}})
	})
	stdout, _, err := runRows(t, "rows", "delete-quarantined", "--reason", testReason, "--table-id", "42", "--cloud-source-id", "7", "--all", "--yes")
	if qerrors.GetExitCode(err) != 2 || !strings.Contains(err.Error(), message) {
		t.Fatalf("error = %v (exit %d), want server refusal at exit 2", err, qerrors.GetExitCode(err))
	}
	if strings.Contains(stdout, "affected") {
		t.Errorf("reported success after server refusal: %s", stdout)
	}
}
