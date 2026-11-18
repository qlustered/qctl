package get

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

func setupDataTestCommand() *cobra.Command {
	rootCmd := &cobra.Command{Use: "qctl"}
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format")
	rootCmd.PersistentFlags().Bool("no-headers", false, "")
	rootCmd.PersistentFlags().String("columns", "", "")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "")
	rootCmd.PersistentFlags().CountP("verbose", "v", "") // CountP, not BoolP
	rootCmd.PersistentFlags().StringP("org", "O", "", "")

	getCmd := &cobra.Command{Use: "get"}
	getCmd.AddCommand(NewDataCommand())
	rootCmd.AddCommand(getCmd)
	return rootCmd
}

func sampleGetData() api.DataWithCellHistorySchema {
	clean, bad := 1500, 24
	return api.DataWithCellHistorySchema{
		DatasetName:    "users_quarantine_data",
		Fields:         []string{"id", "email"},
		Labels:         []string{"User ID", "Email"},
		FieldTypes:     []api.FieldType{"integer", "text"},
		Results:        [][]interface{}{{float64(8943), "a@b.com"}},
		TotalPageCount: 1,
		TotalRows:      1,
		CleanRowsCount: &clean,
		BadRowsCount:   &bad,
	}
}

// dataTestMock wires config/creds and a mock API server, leaving route
// registration to the caller so a test can serve by-rows as well as /data.
func dataTestMock(t *testing.T) *testutil.MockAPIServer {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")
	return mock
}

// execData runs `get data` and returns output and error, for the paths where
// the error itself is what is being asserted.
func execData(t *testing.T, args []string) (string, error) {
	t.Helper()
	cmd := setupDataTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(append([]string{"get", "data"}, args...))
	err := cmd.Execute()
	return buf.String(), err
}

// runData wires config/creds/mocks and runs `get data` with the given args.
func runData(t *testing.T, args []string, handler http.HandlerFunc) string {
	t.Helper()
	mock := dataTestMock(t)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1/data", handler)

	out, err := execData(t, args)
	if err != nil {
		t.Fatalf("execute %v: %v\noutput:\n%s", args, err, out)
	}
	return out
}

func TestGetDataCommand_MarkdownDefault(t *testing.T) {
	out := runData(t, []string{"1"}, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleGetData())
	})
	for _, want := range []string{"# DATASET SUMMARY", "users_quarantine_data", "User ID", "8943"} {
		if !strings.Contains(out, want) {
			t.Errorf("markdown missing %q:\n%s", want, out)
		}
	}
}

func TestGetDataCommand_TableTreatedAsMarkdown(t *testing.T) {
	out := runData(t, []string{"1", "-o", "table"}, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleGetData())
	})
	if !strings.Contains(out, "# DATASET SUMMARY") {
		t.Errorf("-o table should still render Markdown:\n%s", out)
	}
}

// --table is the name-only escape hatch for a table whose name parses as an
// integer. The ID returned for name "42" is deliberately 7 so an accidental
// ResolveID/Atoi path cannot pass this test.
func TestGetDataCommand_TableFlagResolvesNumericName(t *testing.T) {
	mock := dataTestMock(t)
	nameCalls := 0
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		nameCalls++
		if got := r.URL.Query().Get("name"); got != "42" {
			t.Errorf("name query = %q, want 42", got)
		}
		testutil.RespondJSON(w, http.StatusOK,
			map[string]any{"total_rows": 1, "page": 1, "results": []map[string]any{{"id": 7, "name": "42", "state": "active"}}})
	})
	dataCalls := 0
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/7/data", func(w http.ResponseWriter, _ *http.Request) {
		dataCalls++
		testutil.RespondJSON(w, http.StatusOK, sampleGetData())
	})

	if out, err := execData(t, []string{"--table", "42"}); err != nil {
		t.Fatalf("get data --table 42: %v\n%s", err, out)
	}
	if nameCalls != 1 || dataCalls != 1 {
		t.Errorf("name resolves = %d, data requests = %d; want 1 each", nameCalls, dataCalls)
	}
}

func TestGetDataCommand_JSONOutput(t *testing.T) {
	out := runData(t, []string{"1", "-o", "json"}, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleGetData())
	})
	if !strings.Contains(out, `"dataset_name"`) || strings.Contains(out, "# DATASET SUMMARY") {
		t.Errorf("json output should be raw response:\n%s", out)
	}
}

func TestGetDataCommand_YAMLOutput(t *testing.T) {
	out := runData(t, []string{"1", "-o", "yaml"}, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleGetData())
	})
	if !strings.Contains(out, "dataset_name:") {
		t.Errorf("yaml output should contain dataset_name::\n%s", out)
	}
}

// wideNumbersBody is a get_data page written by hand, not marshaled from
// api.DataWithCellHistorySchema: a float64 fixture could not hold the 19-digit id this test
// is about. `big` is a BigInteger under a number format with no decimals,
// `price` a currency Decimal (atlas sends Decimals as strings) and `ratio` a
// float column.
const wideNumbersBody = `{
  "dataset_name": "ledger", "fields": ["id", "big", "price", "ratio"],
  "labels": ["", "", "", ""], "field_types": ["BigInteger", "BigInteger", "Decimal", "Float"],
  "field_display_formats": [null, {"kind": "number", "decimals": null},
    {"kind": "currency", "decimals": 2}, {"kind": "number", "decimals": null}],
  "results": [[1, 1234567890123456789, "1250.5", 100.0]],
  "total_page_count": 1, "total_rows": 1, "clean_rows_count": 1, "bad_rows_count": 0
}`

// TestGetDataKeepsIntegersExact pins every output format to the digits the
// server wrote. Decoded as float64, 1234567890123456789 read back as
// 1234567890123456800 in -o json and yaml, and as 1234567890123460000 in the
// table once a display format sent it through the 15-digit float path.
func TestGetDataKeepsIntegersExact(t *testing.T) {
	serve := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(wideNumbersBody))
	}
	const exact = "1234567890123456789"
	cases := []struct {
		format string
		want   []string
	}{
		{"table", []string{"| 1234567890123456789 |", "| $1,250.50 |", "| 100 |"}},
		{"json", []string{exact + ",", `"1250.5"`, "100.0"}},
		{"yaml", []string{"- " + exact + "\n", "- \"1250.5\"\n", "- 100\n"}},
	}
	for _, tc := range cases {
		t.Run(tc.format, func(t *testing.T) {
			out := runData(t, []string{"1", "-o", tc.format}, serve)
			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Errorf("-o %s output lacks %q:\n%s", tc.format, want, out)
				}
			}
			if strings.Contains(out, "12345678901234568") || strings.Contains(out, "123456789012346") {
				t.Errorf("-o %s rounded the 19-digit integer:\n%s", tc.format, out)
			}
		})
	}
}

// TestGetDataStructuredOutputKeepsNulls pins -o json and -o yaml to the body:
// api.DataWithCellHistorySchema has no null element in field_display_formats or labels, so
// printing the decoded page turned the note column's format into
// {"decimals": null, "kind": ""} and its label into "".
func TestGetDataStructuredOutputKeepsNulls(t *testing.T) {
	const body = `{"dataset_name": "ledger", "fields": ["note", "price"],
  "labels": [null, "Price"], "field_types": ["Text", "Decimal"],
  "field_display_formats": [null, {"kind": "currency", "decimals": 2}],
  "results": [["a<b", "1.5"]], "total_page_count": 1, "total_rows": 1}`
	serve := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}
	cases := []struct {
		format string
		want   []string
	}{
		{"json", []string{"\"labels\": [\n    null,", "\"field_display_formats\": [\n    null,", `"a<b"`}},
		{"yaml", []string{"labels:\n  - null\n", "field_display_formats:\n  - null\n"}},
	}
	for _, tc := range cases {
		t.Run(tc.format, func(t *testing.T) {
			out := runData(t, []string{"1", "-o", tc.format}, serve)
			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Errorf("-o %s output lacks %q:\n%s", tc.format, want, out)
				}
			}
			if strings.Contains(out, `kind: ""`) || strings.Contains(out, `"kind": ""`) {
				t.Errorf("-o %s printed a null format as an empty one:\n%s", tc.format, out)
			}
		})
	}
}

func TestGetDataCommand_MaxCellCharsPassthrough(t *testing.T) {
	// With the flag: backend receives max_cell_chars=50.
	out := runData(t, []string{"1", "--max-cell-chars", "50"}, func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("max_cell_chars"); got != "50" {
			t.Errorf("max_cell_chars query = %q, want 50", got)
		}
		testutil.RespondJSON(w, http.StatusOK, sampleGetData())
	})
	_ = out

	// Without the flag: max_cell_chars must be absent (backend default preserved).
	runData(t, []string{"1"}, func(w http.ResponseWriter, r *http.Request) {
		if _, present := r.URL.Query()["max_cell_chars"]; present {
			t.Errorf("max_cell_chars should be omitted when not set")
		}
		testutil.RespondJSON(w, http.StatusOK, sampleGetData())
	})
}

func TestGetDataCommand_RuleAliases(t *testing.T) {
	u := openapi_types.UUID{}
	_ = u.UnmarshalText([]byte("550e8400-e29b-41d4-a716-446655440000"))
	schema := sampleGetData()
	schema.DatasetRuleIds = &[][]openapi_types.UUID{{u}}

	out := runData(t, []string{"1", "--is-bad-data", "--include-dataset-rule-ids"}, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("include_dataset_rule_ids") != "true" {
			t.Errorf("include_dataset_rule_ids should be sent")
		}
		testutil.RespondJSON(w, http.StatusOK, schema)
	})
	if !strings.Contains(out, "Triggered Rules") || !strings.Contains(out, "[R: r-1]") {
		t.Errorf("expected aliased rule column:\n%s", out)
	}
	// The raw UUID must not leak into the payload.
	if strings.Contains(out, "550e8400-e29b-41d4-a716-446655440000") {
		t.Errorf("payload must not contain raw UUID:\n%s", out)
	}
}

// The backend puts no lower bound on get_data's page/limit and paginates with
// .limit(limit).offset((page - 1) * limit), so --limit 0 used to render an
// empty page at exit 0 and --page 0 asked for a negative offset.
func TestGetDataRejectsNonPositivePagination(t *testing.T) {
	for _, tc := range []struct{ flag, value string }{
		{"page", "0"}, {"page", "-1"}, {"limit", "0"}, {"limit", "-20"},
	} {
		t.Run(tc.flag+"/"+tc.value, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			t.Cleanup(env.Cleanup)
			mock := testutil.NewMockAPIServer()
			t.Cleanup(mock.Close)

			calls := 0
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1/data",
				func(w http.ResponseWriter, _ *http.Request) {
					calls++
					testutil.RespondJSON(w, http.StatusOK, sampleGetData())
				})

			endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
			env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
			env.SetupCredential(endpointKey, testOrgID, "test-token")

			cmd := setupDataTestCommand()
			var buf bytes.Buffer
			cmd.SetOut(&buf)
			cmd.SetErr(&buf)
			cmd.SetArgs([]string{"get", "data", "1", "--" + tc.flag, tc.value})

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
				t.Errorf("expected /data never to be called, got %d call(s)", calls)
			}
		})
	}
}

// The check must not fire on unset flags — pflag's registered default for both
// is 0, and BuildDataParams substitutes 1/20 — and a value the user did set
// must still reach the backend unchanged.
func TestGetDataPaginationPassthroughUnaffected(t *testing.T) {
	var gotQuery string
	out := runData(t, []string{"1", "--page", "2", "--limit", "5"}, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		resp := sampleGetData()
		resp.TotalPageCount = 3
		resp.TotalRows = 11
		testutil.RespondJSON(w, http.StatusOK, resp)
	})
	if !strings.Contains(gotQuery, "page=2") || !strings.Contains(gotQuery, "limit=5") {
		t.Errorf("query should carry the user's page/limit, got %q", gotQuery)
	}
	if !strings.Contains(out, "Page: 2 of 3") || !strings.Contains(out, "fetch the next page with --page 3") || strings.Contains(out, "remove --anchor-row-id") {
		t.Errorf("ordinary pagination must use the requested page and preserve its hint:\n%s", out)
	}
}

// byRowsPath is the overlay endpoint --include-comment-threads calls after the
// data page comes back.
const byRowsPath = "/comment-threads/by-rows"

// --include-comment-threads is a qctl-side second request; -o json/yaml emit
// the RAW backend response, which cannot carry it. Silently ignoring the flag
// would let a caller believe the JSON had been checked for comments.
func TestGetDataRejectsIncludeCommentThreadsWithJSON(t *testing.T) {
	for _, format := range []string{"json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			mock := dataTestMock(t)
			calls := 0
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1/data",
				func(w http.ResponseWriter, _ *http.Request) {
					calls++
					testutil.RespondJSON(w, http.StatusOK, sampleGetData())
				})

			_, err := execData(t, []string{"1", "--include-comment-threads", "-o", format})
			if err == nil {
				t.Fatalf("expected an error for --include-comment-threads with -o %s", format)
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
			}
			if calls != 0 {
				t.Errorf("expected /data never to be called, got %d call(s)", calls)
			}
		})
	}
}

// by-rows needs comment:read, which get_data does not. The flag was explicitly
// asked for, so a failure to satisfy it fails the command rather than rendering
// a column-free page that reads as "no comments".
func TestGetDataFailsWhenByRowsIsForbidden(t *testing.T) {
	mock := dataTestMock(t)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1/data",
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, sampleGetData())
		})
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/1"+byRowsPath,
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusForbidden, map[string]interface{}{
				"detail": map[string]interface{}{"msg": "comment:read required"},
			})
		})

	out, err := execData(t, []string{"1", "--include-comment-threads"})
	if err == nil {
		t.Fatalf("expected an error when by-rows is forbidden, got output:\n%s", out)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitUnauthorized {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitUnauthorized)
	}
	if strings.Contains(out, "# DATASET SUMMARY") {
		t.Errorf("a failed overlay must not render a page that reads as 'no comments':\n%s", out)
	}
}

func TestGetDataUnknownColumnKeepsExit3WithoutScopeAdvice(t *testing.T) {
	for _, flag := range []string{"fields", "filter-by-field-name", "search-by-field-name"} {
		t.Run(flag, func(t *testing.T) {
			t.Cleanup(func() { apierror.SetOrgScope("", "") })
			mock := dataTestMock(t)
			called := false
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1/data",
				func(w http.ResponseWriter, r *http.Request) {
					called = true
					if got := r.URL.Query().Get(strings.ReplaceAll(flag, "-", "_")); got != "typo" {
						t.Errorf("field query = %q, want typo", got)
					}
					testutil.RespondJSON(w, http.StatusNotFound, map[string]any{
						"detail": map[string]string{"msg": "`typo` column does not exist in the table."},
					})
				})
			out, err := execData(t, []string{"1", "--" + flag, "typo"})
			if err == nil {
				t.Fatalf("expected an unknown-column error, got output:\n%s", out)
			}
			if !called || apierror.ScopeNote() == "" {
				t.Fatal("test did not reach the scoped data request")
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitNotFound {
				t.Errorf("exit code = %d, want %d", got, qerrors.ExitNotFound)
			}
			if !strings.Contains(err.Error(), "`typo` column does not exist in the table.") {
				t.Errorf("error lost the column diagnostic: %v", err)
			}
			if strings.Contains(err.Error(), "Scoped to organization") {
				t.Errorf("unknown-column error carries scope advice: %v", err)
			}
		})
	}
}

// An empty linked table answers 404 with the reason and, when a rebuild would
// help, a RETRY_BUILD action (atlas capi/capi/views/data_view_core.py). The
// error keeps the backend's sentence, including the build's own failure text,
// and names the qctl command for the action.
func TestGetDataOnABlockedLinkedTableNamesTheRebuild(t *testing.T) {
	mock := dataTestMock(t)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1/data",
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusNotFound, map[string]any{
				"detail": map[string]any{
					"msg": "Building **orders_enriched** is blocked after repeated failures: " +
						"column amount is missing. Fix the issue, then retry the build.",
					"title":      "Build blocked",
					"error_code": "COMPOSITE_BUILD_BLOCKED",
					"error_id":   "E-1",
					"actions": []map[string]string{
						{"code": "RETRY_BUILD", "text": "Retry the build."},
						{"code": "OPEN_RUN_DETAILS", "text": "Review the run details."},
					},
				},
			})
		})

	out, err := execData(t, []string{"1"})
	if err == nil {
		t.Fatalf("expected an error for an empty linked table, got output:\n%s", out)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitNotFound {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitNotFound)
	}
	for _, want := range []string{
		"column amount is missing. Fix the issue, then retry the build.",
		"(Code: COMPOSITE_BUILD_BLOCKED, ID: E-1)\nRun `qctl run composite-run-job <table>` to rebuild the table.",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q:\n%s", want, err.Error())
		}
	}
	// The table was found; the 404 describes its build, so there is no
	// organization to confirm. ScopeNote() being non-empty is what makes the
	// absence mean something: the run did record an organization.
	if apierror.ScopeNote() == "" {
		t.Fatal("no organization scope was recorded, so the note's absence proves nothing")
	}
	if strings.Contains(err.Error(), "Scoped to organization") {
		t.Errorf("error carries the organization-scope note on a table-state 404:\n%s", err.Error())
	}
}

// A table held up by an alert the user can resolve answers 404 with a link to
// that alert's frontend route (atlas capi/capi/views/data_view_core.py). The
// terminal keeps the link's label, which names the alert, and drops the route.
func TestGetDataOnATableBlockedByAnAlertNamesTheAlert(t *testing.T) {
	mock := dataTestMock(t)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1/data",
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusNotFound, map[string]any{
				"detail": map[string]any{
					"msg":          "Please resolve [alert #12](/orgs/" + testOrgID + "/datasets/1/alerts/12)",
					"title":        "Next Step",
					"error_code":   "TABLE_BLOCKED_BY_ALERT",
					"error_id":     "E-2",
					"redirect_url": "/datasets/1/alerts/12",
				},
			})
		})

	out, err := execData(t, []string{"1"})
	if err == nil {
		t.Fatalf("expected an error for a table blocked by an alert, got output:\n%s", out)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitNotFound {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitNotFound)
	}
	if !strings.Contains(err.Error(), "Please resolve alert #12") {
		t.Errorf("error does not name the alert:\n%s", err.Error())
	}
	if strings.Contains(err.Error(), "/orgs/") {
		t.Errorf("error carries the frontend route:\n%s", err.Error())
	}
	if apierror.ScopeNote() == "" {
		t.Fatal("no organization scope was recorded, so the note's absence proves nothing")
	}
	if strings.Contains(err.Error(), "Scoped to organization") {
		t.Errorf("error carries the organization-scope note on a table-state 404:\n%s", err.Error())
	}
}

// qctl never compiles a --search-regex pattern, so the server's 422 is the
// only explanation of a refused one: it exits 2 with the server's own words.
func TestGetDataSearchRegexRefusalExitsBadUsage(t *testing.T) {
	const msg = "Regex pattern cannot be run in PostgreSQL " +
		"(invalid regular expression: regular expression is too complex)."
	mock := dataTestMock(t)
	var sent string
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1/data",
		func(w http.ResponseWriter, r *http.Request) {
			sent = r.URL.Query().Get("search_regex")
			testutil.RespondJSON(w, http.StatusUnprocessableEntity, map[string]any{
				"detail": map[string]any{"msg": msg, "error_code": "invalid_input"},
			})
		})

	out, err := execData(t, []string{"1", "--search-query", "(?:a{1000}){1000}", "--search-regex"})
	if err == nil {
		t.Fatalf("expected the server's 422 to be reported, got output:\n%s", out)
	}
	if sent != "true" {
		t.Errorf("search_regex = %q, want true", sent)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d: %v", got, qerrors.ExitBadUsage, err)
	}
	if !strings.Contains(err.Error(), msg) {
		t.Errorf("error does not carry the server's message:\n%s", err.Error())
	}
}

// The happy path: the row ids of the page just fetched are what by-rows is
// asked about, and the overlay becomes a column.
func TestGetDataIncludeCommentThreadsRendersColumn(t *testing.T) {
	mock := dataTestMock(t)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1/data",
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, sampleGetData())
		})
	var gotRowIDs []int
	threadID := openapi_types.UUID{}
	_ = threadID.UnmarshalText([]byte("0190bbbb-1111-2222-3333-444444444444"))
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/1"+byRowsPath,
		func(w http.ResponseWriter, r *http.Request) {
			var body api.ThreadsByRowsRequestSchema
			_ = json.NewDecoder(r.Body).Decode(&body)
			gotRowIDs = body.RowIds
			count, whole := 2, false
			fields := []string{"email"}
			testutil.RespondJSON(w, http.StatusOK, api.ThreadsByRowsResponseSchema{
				Threads: []api.RowThreadOverlaySchema{{
					ThreadID: threadID, RowID: 8943,
					CommentCount: &count, WholeRow: &whole, FieldNames: &fields,
				}},
			})
		})

	out, err := execData(t, []string{"1", "--include-comment-threads"})
	if err != nil {
		t.Fatalf("execute: %v\noutput:\n%s", err, out)
	}
	if len(gotRowIDs) != 1 || gotRowIDs[0] != 8943 {
		t.Errorf("by-rows row_ids = %v, want [8943]", gotRowIDs)
	}
	for _, want := range []string{
		"Comment Threads",
		"0190bbbb-1111-2222-3333-444444444444 [2] email",
		"[N] = comments in that thread",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

// Without the flag there is no second request at all — the overlay costs one,
// which is why it is opt-in.
func TestGetDataMakesNoOverlayRequestByDefault(t *testing.T) {
	mock := dataTestMock(t)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1/data",
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, sampleGetData())
		})
	byRows := 0
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/1"+byRowsPath,
		func(w http.ResponseWriter, _ *http.Request) {
			byRows++
			testutil.RespondJSON(w, http.StatusOK, api.ThreadsByRowsResponseSchema{})
		})

	out, err := execData(t, []string{"1"})
	if err != nil {
		t.Fatalf("execute: %v\noutput:\n%s", err, out)
	}
	if byRows != 0 {
		t.Errorf("by-rows called %d time(s) without the flag", byRows)
	}
	if strings.Contains(out, "Comment Threads") {
		t.Errorf("comment column must be opt-in:\n%s", out)
	}
}

// The two batch id->msg routes --include-issues hydrates from.
const (
	alertMsgsPath   = "/alerts-msgs-list/"
	warningMsgsPath = "/warnings-msgs-list/"
)

// sampleIssueData is a quarantined page carrying both token columns: alert 8817
// on the email cell, alert 8819 on the whole row, warning 55 on email.
func sampleIssueData() api.DataWithCellHistorySchema {
	clean, bad := 1500, 24
	return api.DataWithCellHistorySchema{
		DatasetName:    "users_quarantine_data",
		Fields:         []string{"id", "email", "alert_item_ids_", "warning_item_ids_"},
		Labels:         []string{"User ID", "Email", "", ""},
		FieldTypes:     []api.FieldType{"integer", "text", "text", "text"},
		Results:        [][]interface{}{{float64(8943), "a@b.com", []interface{}{"8817-email", "8819-"}, []interface{}{"55-email"}}},
		TotalPageCount: 1,
		TotalRows:      1,
		CleanRowsCount: &clean,
		BadRowsCount:   &bad,
	}
}

// The markers cost nothing: the tokens are already in the page the command
// fetched, so no flag and no second request turns them into cell markers.
func TestGetDataRendersIssueMarkersWithNoExtraRequest(t *testing.T) {
	mock := dataTestMock(t)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1/data",
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, sampleIssueData())
		})
	// Both message routes answer 200, so an unwanted hydration would SUCCEED
	// and reach the render — leaving the request assertion below as the only
	// thing that can catch it, rather than a 404 standing in for it.
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/1"+alertMsgsPath,
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, []api.AlertsMsgsListResponseSchema{})
		})
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/1"+warningMsgsPath,
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, []api.WarningsMsgsListResponseSchema{})
		})

	out, err := execData(t, []string{"1", "--is-bad-data"})
	if err != nil {
		t.Fatalf("execute: %v\noutput:\n%s", err, out)
	}
	for _, want := range []string{
		"| User ID | Email | Issues |",
		"| 8943 | a@b.com [A8817] [W55] | [A8817] email, [A8819] whole-row, [W55] email |",
		"[A<id>] = alert <id> flagged that cell, or the whole row in the Issues column",
		"Add --include-issues for the message behind each [A<id>]/[W<id>].",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	// The raw token columns are consumed, not rendered beside the data. The
	// NAMES are expected in the summary's reserved-column notice, so the guard
	// is scoped to the table itself rather than to the whole payload.
	if line := tableLineContaining(out, "alert_item_ids_"); line != "" {
		t.Errorf("raw token column leaked into the Markdown table: %q\n%s", line, out)
	}
	if strings.Contains(out, `["8817-email"`) {
		t.Errorf("raw token value leaked into the Markdown:\n%s", out)
	}
	for _, req := range mock.Requests() {
		if strings.Contains(req, "msgs-list") {
			t.Errorf("no message request may be made without the flag; saw %q", req)
		}
	}
}

// tableLineContaining returns the first Markdown table row of out that contains
// want, or "" if none does. The reserved-column notice in the summary names
// every folded and hidden column, so a test that means "this column is not
// rendered" has to look at the table and not at the whole payload.
func tableLineContaining(out, want string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "|") && strings.Contains(line, want) {
			return line
		}
	}
	return ""
}

// -o json is the raw backend response: the tokens stay exactly as the server
// spelled them, and nothing about them is rewritten.
func TestGetDataJSONKeepsTheRawTokens(t *testing.T) {
	out := runData(t, []string{"1", "-o", "json"}, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleIssueData())
	})
	for _, want := range []string{`"alert_item_ids_"`, `"8817-email"`, `"55-email"`} {
		if !strings.Contains(out, want) {
			t.Errorf("raw response should still carry %s:\n%s", want, out)
		}
	}
}

// The happy path: one request per kind over the deduped ids of the page, and
// the answers become a dictionary under the table.
func TestGetDataIncludeIssuesHydratesTheMessages(t *testing.T) {
	mock := dataTestMock(t)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1/data",
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, sampleIssueData())
		})
	var gotAlertIDs, gotWarningIDs []int
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/1"+alertMsgsPath,
		func(w http.ResponseWriter, r *http.Request) {
			var body api.AlertsMsgsListRequestSchema
			_ = json.NewDecoder(r.Body).Decode(&body)
			gotAlertIDs = body.Ids
			testutil.RespondJSON(w, http.StatusOK, []api.AlertsMsgsListResponseSchema{
				{ID: 8817, Msg: "Not a valid email"},
			})
		})
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/1"+warningMsgsPath,
		func(w http.ResponseWriter, r *http.Request) {
			var body api.WarningsMsgsListRequestSchema
			_ = json.NewDecoder(r.Body).Decode(&body)
			gotWarningIDs = body.Ids
			testutil.RespondJSON(w, http.StatusOK, []api.WarningsMsgsListResponseSchema{
				{ID: 55, Msg: "Anomalous value"},
			})
		})

	out, err := execData(t, []string{"1", "--is-bad-data", "--include-issues"})
	if err != nil {
		t.Fatalf("execute: %v\noutput:\n%s", err, out)
	}
	if len(gotAlertIDs) != 2 || gotAlertIDs[0] != 8817 || gotAlertIDs[1] != 8819 {
		t.Errorf("alert ids = %v, want [8817 8819]", gotAlertIDs)
	}
	if len(gotWarningIDs) != 1 || gotWarningIDs[0] != 55 {
		t.Errorf("warning ids = %v, want [55]", gotWarningIDs)
	}
	for _, want := range []string{
		"## ISSUES",
		"- A8817: Not a valid email",
		// The server answered nothing for 8819; the line says so rather than
		// disappearing, which would read as "this alert has no message".
		"- A8819: (no message returned by the server)",
		"- W55: Anomalous value",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

func TestGetDataIncludeIssuesShowsConflictValuesInDisplayUnits(t *testing.T) {
	mock := dataTestMock(t)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1/data", func(w http.ResponseWriter, _ *http.Request) {
		page := sampleIssueData()
		page.Fields[1] = "discount"
		page.Labels[1] = "Discount"
		page.Results[0][1] = "0.13"
		page.Results[0][2] = []any{"8817-discount", "8819-discount"}
		page.Results[0][3] = nil
		two := 2
		page.FieldDisplayFormats = []api.DisplayFormat{{}, {Kind: api.DisplayFormatKindPercent, Decimals: &two}, {}, {}}
		testutil.RespondJSON(w, http.StatusOK, page)
	})
	// These are the messages from atlas manual_override_conflict_alert and
	// Cleaner._apply_cell_overrides_post_validation, already in display units.
	msgs := []api.AlertsMsgsListResponseSchema{
		{ID: 8817, Msg: "Override conflict on 'discount': upstream now sends 9.00% (was 12.50%); keeping the fix 13.00% pending a decision."},
		{ID: 8819, Msg: "Override conflict on 'discount': pipeline now produces 9.00% (was 12.50%); keeping override target 13.00% pending adjudication."},
	}
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/1"+alertMsgsPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, msgs)
	})
	out, err := execData(t, []string{"1", "--is-bad-data", "--include-issues"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"13.00% [A8817] [A8819]", "upstream now sends 9.00% (was 12.50%)", "pipeline now produces 9.00% (was 12.50%)"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	if len(mock.Requests()) != 2 {
		t.Errorf("conflict messages need only data and the alert batch, got %v", mock.Requests())
	}
}

// The msgs-list items carry the flag records against each alert and warning.
// The dictionary names the submitters of the live ones on this page's rows,
// per kind; a flag on another row, or a detached one, names nobody.
func TestGetDataIncludeIssuesNamesTheFlaggers(t *testing.T) {
	mock := dataTestMock(t)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1/data",
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, sampleIssueData())
		})
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/1"+alertMsgsPath,
		func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"id": 8817, "msg": "Not a valid email", "user_submitted_issues": [
				{"id": "6f1c2a7e-0000-4000-8000-000000000001", "row_id": 8943, "field_names": ["email"],
				 "submitted_by": {"id": "6f1c2a7e-0000-4000-8000-0000000000aa", "email": "ann@x.com", "first_name": "Ann", "last_name": "Lee"},
				 "thread_id": "6f1c2a7e-0000-4000-8000-0000000000ff", "created_at": "2026-10-01T10:00:00Z", "retracted_at": null, "detached_at": null},
				{"id": "6f1c2a7e-0000-4000-8000-000000000002", "row_id": 9999, "field_names": ["email"],
				 "submitted_by": {"id": "6f1c2a7e-0000-4000-8000-0000000000bb", "email": "bo@x.com", "first_name": "Bo", "last_name": "Diaz"},
				 "thread_id": null, "created_at": "2026-10-02T10:00:00Z", "retracted_at": null, "detached_at": null}
			]}]`))
		})
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/1"+warningMsgsPath,
		func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"id": 55, "msg": "Anomalous value", "user_submitted_issues": [
				{"id": "6f1c2a7e-0000-4000-8000-000000000003", "row_id": 8943, "field_names": [],
				 "submitted_by": {"id": "6f1c2a7e-0000-4000-8000-0000000000cc", "email": "cy@x.com", "first_name": "Cy", "last_name": "Ng"},
				 "thread_id": null, "created_at": "2026-10-01T10:00:00Z", "retracted_at": null, "detached_at": "2026-10-03T10:00:00Z"},
				{"id": "6f1c2a7e-0000-4000-8000-000000000004", "row_id": 8943, "field_names": ["email"],
				 "submitted_by": {"id": "6f1c2a7e-0000-4000-8000-0000000000dd", "email": "dee@x.com", "first_name": "", "last_name": ""},
				 "thread_id": null, "created_at": "2026-10-02T10:00:00Z", "retracted_at": null, "detached_at": null}
			]}]`))
		})

	out, err := execData(t, []string{"1", "--is-bad-data", "--include-issues"})
	if err != nil {
		t.Fatalf("execute: %v\noutput:\n%s", err, out)
	}
	for _, want := range []string{
		"- A8817: Not a valid email — flagged by Ann Lee\n",
		"- A8819: (no message returned by the server)\n",
		"- W55: Anomalous value — flagged by dee@x.com\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "6f1c2a7e-0000-4000-8000-0000000000ff") {
		t.Errorf("a thread id leaked into the dictionary:\n%s", out)
	}
}

// Alert ids and warning ids are separate id spaces, served by separate routes.
// One request carrying both would join alert 12 to warning 12's message.
func TestGetDataIncludeIssuesAsksEachRouteOnlyAboutItsOwnKind(t *testing.T) {
	mock := dataTestMock(t)
	schema := sampleIssueData()
	schema.Results = [][]interface{}{{
		float64(8943), "a@b.com", []interface{}{"12-email"}, []interface{}{"12-email"},
	}}
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1/data",
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, schema)
		})
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/1"+alertMsgsPath,
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, []api.AlertsMsgsListResponseSchema{{ID: 12, Msg: "alert twelve"}})
		})
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/1"+warningMsgsPath,
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, []api.WarningsMsgsListResponseSchema{{ID: 12, Msg: "warning twelve"}})
		})

	out, err := execData(t, []string{"1", "--is-bad-data", "--include-issues"})
	if err != nil {
		t.Fatalf("execute: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "- A12: alert twelve") || !strings.Contains(out, "- W12: warning twelve") {
		t.Errorf("the two id spaces must stay apart:\n%s", out)
	}
}

// A kind with no ids on the page buys nothing, so it is not asked.
func TestGetDataIncludeIssuesSkipsAKindWithNoIDs(t *testing.T) {
	mock := dataTestMock(t)
	schema := sampleIssueData()
	schema.Fields = []string{"id", "email", "warning_item_ids_"}
	schema.Labels = []string{"User ID", "Email", ""}
	schema.Results = [][]interface{}{{float64(8943), "a@b.com", []interface{}{"55-email"}}}
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1/data",
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, schema)
		})
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/1"+warningMsgsPath,
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, []api.WarningsMsgsListResponseSchema{{ID: 55, Msg: "Anomalous value"}})
		})
	// The alert route answers 200 as well: a spurious call must fail this test
	// on the assertion below, not on a 404 that would also fire if the call
	// were made for some entirely different reason.
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/1"+alertMsgsPath,
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, []api.AlertsMsgsListResponseSchema{})
		})

	out, err := execData(t, []string{"1", "--include-issues"})
	if err != nil {
		t.Fatalf("execute: %v\noutput:\n%s", err, out)
	}
	for _, req := range mock.Requests() {
		if strings.Contains(req, "alerts-msgs-list") {
			t.Errorf("a clean page has no alert ids to ask about; saw %q", req)
		}
	}
	if !strings.Contains(out, "- W55: Anomalous value") {
		t.Errorf("missing the warning message:\n%s", out)
	}
}

// --include-issues is a qctl-side second request; -o json/yaml emit the RAW
// backend response, which carries the tokens but no messages. Silently ignoring
// the flag would let a caller read the tokens as the whole answer.
func TestGetDataRejectsIncludeIssuesWithJSON(t *testing.T) {
	for _, format := range []string{"json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			mock := dataTestMock(t)
			calls := 0
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1/data",
				func(w http.ResponseWriter, _ *http.Request) {
					calls++
					testutil.RespondJSON(w, http.StatusOK, sampleIssueData())
				})

			_, err := execData(t, []string{"1", "--include-issues", "-o", format})
			if err == nil {
				t.Fatalf("expected an error for --include-issues with -o %s", format)
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
			}
			if calls != 0 {
				t.Errorf("expected /data never to be called, got %d call(s)", calls)
			}
		})
	}
}

// The msgs-list routes need alert:list / warning:list, which get_data does not.
// The flag was explicitly asked for, so a refusal fails the command rather than
// rendering a page whose missing dictionary would read as "no messages".
func TestGetDataFailsWhenIssueHydrationIsForbidden(t *testing.T) {
	mock := dataTestMock(t)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1/data",
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, sampleIssueData())
		})
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/1"+alertMsgsPath,
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusForbidden, map[string]interface{}{
				"detail": map[string]interface{}{"msg": "alert:list required"},
			})
		})

	out, err := execData(t, []string{"1", "--is-bad-data", "--include-issues"})
	if err == nil {
		t.Fatalf("expected an error when the message route is forbidden, got output:\n%s", out)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitUnauthorized {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitUnauthorized)
	}
	if strings.Contains(out, "# DATASET SUMMARY") {
		t.Errorf("a failed hydration must not render a page that reads as 'no messages':\n%s", out)
	}
}

// sampleReservedData is a quarantine page carrying the reserved columns atlas
// actually returns: BAD_RECORDS_RESERVED_COLUMNS minus cettings'
// HIDDEN_FIELDS_BAD_DATA and minus row_logs_, which needs --show-row-logs.
// Seventeen of them, beside two columns of data.
func sampleReservedData() api.DataWithCellHistorySchema {
	clean, bad := 1500, 24
	return api.DataWithCellHistorySchema{
		DatasetName: "orders",
		Fields: []string{
			"id", "row_number_", "occurrence_", "initial_signature_",
			"cell_move_suggestions_", "warning_item_ids_", "alert_item_ids_",
			"cluster_id_", "confidence_score_", "awaiting_processing_",
			"processing_alert_item_id_", "keep_in_quarantine_", "suspected_duplicate_",
			"data_source_model_id", "version_id_", "passes_validation_",
			"cell_overrides_", "rule_run_id_", "email", "amount",
		},
		Labels: make([]string, 20),
		Results: [][]interface{}{{
			float64(8943), float64(12), float64(1), "9f2ac41b7d5e",
			nil, []interface{}{"55-email"}, []interface{}{"8817-email"},
			float64(91), 0.75, true,
			float64(8817), "hold", false,
			float64(3), float64(2), false,
			nil, float64(77), "a@b.com", float64(42),
		}},
		// A decided row carries a reason too; Row State prints only the decision.
		ApprovalHoldReasons: &[]api.ApprovalHoldReason{api.ApprovalHoldReasonReviewPolicy},
		TotalPageCount:      1,
		TotalRows:           1,
		CleanRowsCount:      &clean,
		BadRowsCount:        &bad,
	}
}

// approvalHoldBody is a quarantine page as atlas sends it: approval_hold_reasons
// is one entry per row, and null where nothing would hold the row.
const approvalHoldBody = `{"dataset_name": "orders",
  "fields": ["id", "keep_in_quarantine_", "suspected_duplicate_", "passes_validation_", "email"],
  "labels": [null, null, null, null, "Email"],
  "results": [[1, "undecided", true, true, "a@b.com"],
              [2, "undecided", null, true, "c@d.com"],
              [3, "release", true, false, "e@f.com"]],
  "approval_hold_reasons": ["possible_duplicate", null, "possible_duplicate"],
  "total_page_count": 1, "total_rows": 3, "clean_rows_count": 10, "bad_rows_count": 3}`

// Row State names the approval hold on an undecided row, says nothing about it
// on an undecided row whose reason is null, and prints a decided duplicate as
// its decision plus suspected-duplicate.
func TestGetDataRowStateShowsTheApprovalHold(t *testing.T) {
	out := runData(t, []string{"1", "--is-bad-data"}, serveBody([]byte(approvalHoldBody)))
	for _, want := range []string{
		"| 1 | a@b.com | valid, held-for-approval=possible_duplicate |",
		"| 2 | c@d.com | valid |",
		"| 3 | e@f.com | invalid, quarantine-hold=release, suspected-duplicate |",
		"/ held-for-approval=<reason> / suspected-duplicate /",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("payload missing %q:\n%s", want, out)
		}
	}
}

// waitingForApprovalBody is a quarantine page as atlas sends it once a data
// review is submitted: row 1 waits in it, row 2 was decided, and row 3 is
// outside it. Every row carries a reason, since atlas computes one with no
// request term.
const waitingForApprovalBody = `{"dataset_name": "orders",
  "fields": ["id", "keep_in_quarantine_", "suspected_duplicate_", "passes_validation_", "email"],
  "labels": [null, null, null, null, "Email"],
  "results": [[1, "undecided", true, true, "a@b.com"],
              [2, "hold", null, true, "c@d.com"],
              [3, "undecided", null, true, "e@f.com"]],
  "approval_hold_reasons": ["possible_duplicate", "review_policy", "review_policy"],
  "waiting_for_approval": [true, false, false],
  "total_page_count": 1, "total_rows": 3, "clean_rows_count": 10, "bad_rows_count": 3}`

// Row State prints waiting-for-approval, not the reason, for a row waiting in
// a submitted review, keeps suspected-duplicate on it, and leaves the decided
// row and the row outside the review as they were.
func TestGetDataRowStateShowsTheWaitingState(t *testing.T) {
	out := runData(t, []string{"1", "--is-bad-data"}, serveBody([]byte(waitingForApprovalBody)))
	for _, want := range []string{
		"| 1 | a@b.com | valid, waiting-for-approval, suspected-duplicate |",
		"| 2 | c@d.com | valid, quarantine-hold=hold |",
		"| 3 | e@f.com | valid, held-for-approval=review_policy |",
		"/ waiting-for-approval / held-for-approval=<reason> /",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("payload missing %q:\n%s", want, out)
		}
	}
}

func TestGetDataRowStateShowsSelfExclusionWithoutLogs(t *testing.T) {
	body := strings.TrimSuffix(waitingForApprovalBody, "}") + `,"sent_or_changed_by_me":[true,false,true]}`
	out := runData(t, []string{"1", "--is-bad-data"}, serveBody([]byte(body)))
	for _, want := range []string{
		"| 1 | a@b.com | valid, waiting-for-approval, suspected-duplicate, cannot-approve=self |",
		"| 2 | c@d.com | valid, quarantine-hold=hold |",
		"| 3 | e@f.com | valid, held-for-approval=review_policy, cannot-approve=self |",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	for _, format := range []string{"json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			out := runData(t, []string{"1", "--is-bad-data", "-o", format}, serveBody([]byte(body)))
			if !strings.Contains(out, "sent_or_changed_by_me") || strings.Contains(out, "cannot-approve=self") {
				t.Errorf("structured output lost its wire representation:\n%s", out)
			}
		})
	}
}

// -o json and -o yaml print the body, so a null reason stays null. Decoding
// into api.DataWithCellHistorySchema would not keep it: the generated element type is a plain
// string, and a null entry decodes to "".
func TestGetDataStructuredOutputKeepsNullReasons(t *testing.T) {
	for format, want := range map[string]string{
		"json": "\"approval_hold_reasons\": [\n    \"possible_duplicate\",\n    null,",
		"yaml": "approval_hold_reasons:\n  - possible_duplicate\n  - null\n",
	} {
		t.Run(format, func(t *testing.T) {
			out := runData(t, []string{"1", "--is-bad-data", "-o", format}, serveBody([]byte(approvalHoldBody)))
			if !strings.Contains(out, want) {
				t.Errorf("-o %s output lacks %q:\n%s", format, want, out)
			}
		})
	}
}

// The Markdown payload folds the reserved columns instead of rendering them:
// twenty response columns become three data columns plus Issues and Row State,
// with the signature in its own section and the rest named in the summary.
func TestGetDataFoldsTheReservedColumns(t *testing.T) {
	out := runData(t, []string{"1", "--is-bad-data"}, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleReservedData())
	})
	for _, want := range []string{
		"| id | email | amount | Issues | Row State |",
		"invalid, awaiting-processing, processing-alert=8817, quarantine-hold=hold |",
		"## ROW SIGNATURES",
		"- 8943: 9f2ac41b7d5e",
		"hidden: row_number_, occurrence_, cell_move_suggestions_",
		"Show one with --fields <name>; -o json has them all.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("payload missing %q:\n%s", want, out)
		}
	}
	// Not one of the seventeen reaches a table row. Their NAMES are expected in
	// the summary notice, so the check is scoped to the table.
	for _, name := range sampleReservedData().Fields {
		if name == "id" || name == "email" || name == "amount" {
			continue
		}
		if line := tableLineContaining(out, name); line != "" {
			t.Errorf("reserved column %q reached the table: %q", name, line)
		}
	}
}

// --fields names a reserved column and gets it back as an ordinary column. The
// backend re-adds the whole reserved set to a narrowed request, so this is a
// display decision qctl makes on its own and nothing about the request changes.
func TestGetDataFieldsShowsAReservedColumn(t *testing.T) {
	out := runData(t, []string{"1", "--is-bad-data", "--fields", "email", "--fields", "row_number_"},
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, sampleReservedData())
		})
	if !strings.Contains(out, "| id | row_number_ | email | amount | Issues | Row State |") {
		t.Errorf("--fields row_number_ did not render the column:\n%s", out)
	}
	if strings.Contains(out, "hidden: row_number_") {
		t.Errorf("a rendered column must not also be reported hidden:\n%s", out)
	}
}

// --fields takes a comma-separated list as well as repeats. Each name reaches
// the query string as its own fields parameter, and the renderer gets the same
// split list: row_number_ renders as a column only if dataview was told it was
// asked for.
func TestGetDataFieldsSplitsCommaLists(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{"comma list", []string{"--fields", "email,row_number_"}, []string{"email", "row_number_"}},
		{"mixed", []string{"--fields", "email,amount", "--fields", "row_number_"},
			[]string{"email", "amount", "row_number_"}},
		{"spaces trimmed", []string{"--fields", "email, row_number_"}, []string{"email", "row_number_"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var sent []string
			var saw bool
			args := append([]string{"1", "--is-bad-data"}, tc.args...)
			out := runData(t, args, func(w http.ResponseWriter, r *http.Request) {
				saw = true
				sent = r.URL.Query()["fields"]
				testutil.RespondJSON(w, http.StatusOK, sampleReservedData())
			})
			if !saw {
				t.Fatal("the data request never arrived; the query assertion below cannot fail")
			}
			if strings.Join(sent, "|") != strings.Join(tc.want, "|") {
				t.Errorf("fields query = %q, want %q", sent, tc.want)
			}
			if !strings.Contains(out, "| id | row_number_ | email | amount | Issues | Row State |") {
				t.Errorf("row_number_ was not rendered as a requested column:\n%s", out)
			}
		})
	}
}

// A page whose rows are wider than its field list cannot be rendered. When
// the request named --fields the error says which, since that is what an
// unchecked fields name produces; either way it is the server's response at
// fault, not the command line, so it exits 1.
func TestGetDataRowWidthErrorNamesTheRequestedFields(t *testing.T) {
	const body = `{"dataset_name": "orders", "fields": ["id", "email"], "labels": [null, null],
  "results": [[1, "a@b.com", "extra"]], "total_page_count": 1, "total_rows": 1}`
	mock := dataTestMock(t)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1/data", serveBody([]byte(body)))

	out, err := execData(t, []string{"1", "--fields", "email"})
	if err == nil {
		t.Fatalf("a row wider than its fields rendered:\n%s", out)
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitGenericError {
		t.Errorf("exit code = %d, want %d (err: %v)", code, qerrors.ExitGenericError, err)
	}
	for _, want := range []string{
		"result row 0 has 3 values but the response names 2 fields",
		`--fields sent "email"`,
		"qctl get table-schema <table> -o name",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q:\n%v", want, err)
		}
	}
}

// -o json is the raw backend response: every reserved column is still there,
// exactly as the server sent it, and none of the folding happened.
func TestGetDataJSONKeepsTheReservedColumns(t *testing.T) {
	out := runData(t, []string{"1", "--is-bad-data", "-o", "json"}, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleReservedData())
	})
	for _, want := range []string{
		`"passes_validation_"`, `"initial_signature_"`, `"row_number_"`, `"cell_overrides_"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("-o json dropped %s:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"Row State", "ROW SIGNATURES", "Reserved columns"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("-o json must not carry %q:\n%s", unwanted, out)
		}
	}
}

// stewardPinnedBody is the dataview package's pinned clean-page fixture: a
// wire body carrying steward_overrides, with a per-row null and the marker's
// keys in the server's order rather than Go's sorted-map order.
func stewardPinnedBody(t *testing.T) []byte {
	t.Helper()
	body, err := os.ReadFile("../../dataview/testdata/steward_overrides/clean_pinned.json")
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func serveBody(body []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}
}

// -o json is the wire: only the indentation may differ from the body.
func TestGetDataJSONKeepsStewardOverridesAsSent(t *testing.T) {
	body := stewardPinnedBody(t)
	out := runData(t, []string{"1", "-o", "json"}, serveBody(body))

	var got, want bytes.Buffer
	if err := json.Compact(&got, []byte(out)); err != nil {
		t.Fatalf("-o json is not JSON: %v\n%s", err, out)
	}
	if err := json.Compact(&want, body); err != nil {
		t.Fatal(err)
	}
	if got.String() != want.String() {
		t.Errorf("-o json differs from the wire body\n--- got ---\n%s\n--- want ---\n%s", got.String(), want.String())
	}
}

func TestGetDataRendersStewardOverrides(t *testing.T) {
	out := runData(t, []string{"1"}, serveBody(stewardPinnedBody(t)))
	for _, want := range []string{
		"| 2000 | East | 13.00% [H] [W55] [S] |",
		"## STEWARD OVERRIDES\n- 2000 discount: 12.50% → 13.00%\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("markdown missing %q:\n%s", want, out)
		}
	}
}

// The two reserved-column overrides hang on names the spec derives rather than
// data.go registering: --show-row-logs is read by flag NAME, and paramflags
// splits --fields by its wire name. A rename in the spec would leave GetBool
// returning a zero value with an error nobody reads, and the split keyed to a
// name no flag carries, with no build break and no other symptom. Assert both
// exist, with the types the readers use.
func TestReservedColumnOverrideFlagsExist(t *testing.T) {
	flags := NewDataCommand().Flags()
	if _, err := flags.GetStringArray("fields"); err != nil {
		t.Errorf("--fields is not a stringArray on `get data`: %v", err)
	}
	if _, err := flags.GetBool("show-row-logs"); err != nil {
		t.Errorf("--show-row-logs is not a bool on `get data`: %v", err)
	}
}
