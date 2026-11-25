package rows

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestThirdPartyRowRoutes(t *testing.T) {
	for _, tc := range []struct {
		verb, path, response, bodyPart, query string
		flags                                 []string
	}{
		{"delete-quarantined", "datasets/42/delete-rows", `{"affected_rows":1}`, testSignatureBody, "is_bad_data=true", []string{"--signature", "deadbeef", "--exclude-from-future-uploads", "--reason", testReason}},
		{"delete-quarantined", "datasets/42/delete-rows", `{"affected_rows":1}`, testStatusBody, "is_bad_data=true&quarantine_status=on_hold", []string{"--status", "on_hold", "--reason", testReason}},
		{"delete-quarantined", "datasets/42/delete-all-bad-rows", `{"affected_rows":1}`, testCommentBody, "", []string{"--all", "--reason", testReason}},
		{"send-to-quarantine", "datasets/42/move-rows-to-quarantine", `{"moved":1}`, `"quarantine_hold":"hold"`, "", []string{"--row-id", "7"}},
		{"send-to-quarantine", "datasets/42/move-all-rows-to-quarantine", `{"moved":1}`, `"quarantine_hold":"hold"`, "", []string{"--all"}},
		{"reprocess-clean", "datasets/42/move-rows-to-quarantine", `{"moved":1}`, `"quarantine_hold":"undecided"`, "", []string{"--row-id", "7"}},
		{"reprocess-clean", "datasets/42/move-all-rows-to-quarantine", `{"moved":1}`, `"quarantine_hold":"undecided"`, "", []string{"--all"}},
		{"reprocess-quarantined", "datasets/42/reprocess-bad-rows", `{"affected_rows":1}`, `"ids":[7]`, "", []string{"--row-id", "7"}},
		{"reprocess-quarantined", "datasets/42/reprocess-all-bad-rows", `{"affected_rows":1}`, "", "", []string{"--all"}},
		{"hold", "datasets/42/update-keep-in-quarantine", `{"affected_rows":1}`, `"quarantine_hold":"hold"`, "", []string{"--row-id", "7"}},
		{"release", "datasets/42/update-keep-in-quarantine", `{"affected_rows":1}`, `"quarantine_hold":"release"`, "", []string{"--row-id", "7"}},
		{"reset-hold", "datasets/42/update-keep-in-quarantine", `{"affected_rows":1}`, `"quarantine_hold":"undecided"`, "", []string{"--row-id", "7"}},
		{"search-replace", "datasets/42/search-and-replace", `{"matched_rows":1,"replaced_rows":1}`, `"dry_run":false`, "", []string{"--search", "old", "--replace", "new"}},
		{"set-cell", "datasets/42/modify-clean-cell", `{"result":"ok"}`, `"new_value":"CA"`, "", []string{"--row-id", "7", "--field", "state", "--value", "CA"}},
		{"check-unique", "datasets/42/check-column-uniqueness", `{"is_unique":true,"reason":null}`, `"columns":["order_id"]`, "", []string{"--column", "order_id"}},
		{"delete-quarantined", "data-sources/7/delete-rows", `{"result":"ok"}`, testSignatureBody, "dataset_id=42&is_bad_data=true", []string{"--cloud-source-id", "7", "--signature", "deadbeef", "--exclude-from-future-uploads", "--reason", testReason}},
		{"delete-quarantined", "data-sources/7/delete-all-bad-rows", `{"affected_rows":1}`, testCommentBody, "dataset_id=42", []string{"--cloud-source-id", "7", "--all", "--reason", testReason}},
		{"reprocess-quarantined", "data-sources/7/reprocess-all-bad-rows", `{"affected_rows":1}`, "", "dataset_id=42", []string{"--cloud-source-id", "7", "--all"}},
		{"hold", "data-sources/7/update-keep-in-quarantine", `{"affected_rows":1}`, `"quarantine_hold":"hold"`, "dataset_id=42", []string{"--cloud-source-id", "7", "--all"}},
		{"release", "data-sources/7/update-keep-in-quarantine", `{"affected_rows":1}`, `"quarantine_hold":"release"`, "dataset_id=42", []string{"--cloud-source-id", "7", "--all"}},
		{"reset-hold", "data-sources/7/update-keep-in-quarantine", `{"affected_rows":1}`, `"quarantine_hold":"undecided"`, "dataset_id=42", []string{"--cloud-source-id", "7", "--all"}},
		{"search-replace", "datasets/42/search-and-replace", `{"matched_rows":1}`, `"data_source_model_id":7`, "", []string{"--cloud-source-id", "7", "--search", "old", "--replace", "new"}},
	} {
		t.Run(tc.verb+"/"+tc.path+"/"+strings.Join(tc.flags, "_"), func(t *testing.T) {
			mock := newRowsTestEnv(t)
			got := record(mock, "POST", "/api/orgs/"+testOrgID+"/third-party/"+tc.path, tc.response)
			args := append([]string{"rows", tc.verb, "42", "--shared"}, tc.flags...)
			if tc.verb != "check-unique" {
				args = append(args, "--yes")
			}
			out, stderr, err := runRows(t, args...)
			if err != nil {
				t.Fatalf("%v\n%s", err, stderr)
			}
			if got.hits != 1 || !strings.Contains(got.body, tc.bodyPart) || got.query != tc.query {
				t.Fatalf("request: hits=%d body=%s query=%s", got.hits, got.body, got.query)
			}
			if tc.verb != "check-unique" && !strings.Contains(out+stderr, "shared") {
				t.Fatalf("output lost the provider: %s\n%s", out, stderr)
			}
			for _, req := range mock.Requests() {
				if !strings.Contains(req, "/third-party/") {
					t.Fatalf("used a first-party route: %s", req)
				}
			}
		})
	}
}

func TestThirdPartyRowsResolveSharedNames(t *testing.T) {
	for _, tableArgs := range [][]string{{"shared orders"}, {"--table", "42"}} {
		t.Run(strings.Join(tableArgs, "_"), func(t *testing.T) {
			mock := newRowsTestEnv(t)
			lookups := 0
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/third-party/datasets", func(w http.ResponseWriter, r *http.Request) {
				lookups++
				name := r.URL.Query().Get("name")
				if name != tableArgs[len(tableArgs)-1] {
					t.Errorf("lookup name = %q", name)
				}
				testutil.RespondJSON(w, http.StatusOK, map[string]any{"results": []map[string]any{{"id": 42, "name": name, "state": "active"}}})
			})
			got := record(mock, "POST", "/api/orgs/"+testOrgID+"/third-party/datasets/42/move-all-rows-to-quarantine", `{"moved":1}`)
			args := append([]string{"rows", "send-to-quarantine"}, tableArgs...)
			args = append(args, "--shared", "--all", "--yes")
			out, stderr, err := runRows(t, args...)
			if err != nil || lookups != 1 || got.hits != 1 {
				t.Fatalf("err=%v lookups=%d writes=%d\n%s", err, lookups, got.hits, stderr)
			}
			if !strings.Contains(out+stderr, "--shared") || strings.Contains(stderr, "qctl describe table") {
				t.Fatalf("incorrect follow-up: %s\n%s", out, stderr)
			}
		})
	}
}

func TestThirdPartyRowsRefuseUnsupportedOptionsLocally(t *testing.T) {
	for _, flags := range [][]string{
		{"send-to-quarantine", "--shared=invalid", "--all"},
		{"hold", "--shared", "--cloud-source", "nightly", "--all"},
		{"ignore-rule", "--shared", "--cloud-source-id", "7", "--row-id", "8", "--rule", "email"},
		{"unignore-rule", "--shared", "--cloud-source-id", "7", "--row-id", "8", "--rule", "email"},
	} {
		t.Run(strings.Join(flags, "_"), func(t *testing.T) {
			mock := newRowsTestEnv(t)
			_, _, err := runRows(t, append([]string{"rows"}, flags...)...)
			if qerrors.GetExitCode(err) != 2 || len(mock.Requests()) != 0 {
				t.Fatalf("err=%v, requests=%v", err, mock.Requests())
			}
		})
	}
}

func TestThirdPartyNameMissNeverFallsBackToOwnedTable(t *testing.T) {
	mock := newRowsTestEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/third-party/datasets", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"results":[]}`))
	})
	_, _, err := runRows(t, "rows", "send-to-quarantine", "orders", "--shared", "--all", "--yes")
	if qerrors.GetExitCode(err) != 3 || !strings.Contains(err.Error(), "shared") {
		t.Fatalf("err = %v, want a shared-table name miss with exit 3", err)
	}
	for _, req := range mock.Requests() {
		if !strings.HasPrefix(req, "GET ") || !strings.Contains(req, "/third-party/datasets") {
			t.Fatalf("unexpected request after name miss: %s", req)
		}
	}
}

func TestThirdPartyRowErrorsKeepExitCodes(t *testing.T) {
	for _, status := range []int{403, 404, 422, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			mock := newRowsTestEnv(t)
			mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/third-party/datasets/42/move-all-rows-to-quarantine", func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, status, json.RawMessage(`{"detail":{"msg":"provider refused"}}`))
			})
			_, _, err := runRows(t, "rows", "reprocess-clean", "42", "--shared", "--all", "--yes")
			want := map[int]int{403: 4, 404: 3, 422: 2, 500: 1}[status]
			if qerrors.GetExitCode(err) != want || !strings.Contains(err.Error(), "provider refused") || len(mock.Requests()) != 1 {
				t.Fatalf("err=%v, want exit %d and one request", err, want)
			}
		})
	}
}

func TestThirdPartyConfirmationCountsAndRecipes(t *testing.T) {
	mock := newRowsTestEnv(t)
	got := record(mock, "GET", "/api/orgs/"+testOrgID+"/third-party/datasets/42/quarantine/count-by-status", `{"on_hold":3}`)
	ctx, err := cmdutil.Bootstrap(setupRowsTestCommand())
	if err != nil {
		t.Fatal(err)
	}
	sc := scope{thirdParty: true, kind: scopeCloudSource, tableID: 42, tableName: "42", cloudSourceID: 7}
	var warnings bytes.Buffer
	n := sizedStatusCount(&warnings, ctx, sc, api.QuarantineStatusOnHold, holdReleaseSupport)
	query, _ := url.ParseQuery(got.query)
	if n == nil || *n != 3 || got.hits != 1 || query.Get("data_source_model_id") != "7" {
		t.Fatalf("count=%v hits=%d query=%s warnings=%s", n, got.hits, got.query, &warnings)
	}
	line := sc.getDataLine(true)
	if !strings.Contains(line, "--table 42 --shared") || !strings.Contains(line, "--data-source-model-id 7") {
		t.Fatalf("recipe = %s", line)
	}
}
