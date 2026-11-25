package get

import (
	"bytes"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/qlustered/qctl/internal/warnings"
	"github.com/spf13/cobra"
)

// setupWarningsTestCommand builds a root carrying the global flags the real
// root registers, with `get warnings` under it. Verbose must be CountP:
// Bootstrap reads it with GetCount, and a BoolP yields verbosity 0 silently.
func setupWarningsTestCommand() *cobra.Command {
	rootCmd := &cobra.Command{Use: "qctl"}
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format (table|json|yaml|name)")
	rootCmd.PersistentFlags().Bool("no-headers", false, "Don't print column headers")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "Maximum width for table columns")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "Show secret fields in plaintext")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "Allow non-localhost http://")
	rootCmd.PersistentFlags().CountP("verbose", "v", "Enable verbose output")

	getCmd := &cobra.Command{Use: "get", Short: "Display resources"}
	getCmd.AddCommand(NewWarningsCommand())
	rootCmd.AddCommand(getCmd)
	return rootCmd
}

// warningsEnv wires /warnings and counts the calls. The counter is the point:
// a rejected filter must fail *before* the listing request, not after it. The
// mock is exposed so a test can add the routes its own case needs (the table
// name lookup, say) to the same server.
type warningsEnv struct {
	t     *testing.T
	mock  *testutil.MockAPIServer
	calls int
	query url.Values
}

func newWarningsEnv(t *testing.T) *warningsEnv {
	t.Helper()

	env := testutil.NewTestEnv(t)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(func() {
		mock.Close()
		env.Cleanup()
	})

	we := &warningsEnv{t: t, mock: mock}
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/warnings", func(w http.ResponseWriter, r *http.Request) {
		we.calls++
		we.query = r.URL.Query()
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"results": []interface{}{}, "total_rows": 0, "page": 1,
		})
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	return we
}

func (we *warningsEnv) run(args ...string) error {
	we.t.Helper()
	rootCmd := setupWarningsTestCommand()
	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs(append([]string{"get", "warnings"}, args...))
	return rootCmd.Execute()
}

// A zero ID used to drop the filter and list every warning in the org at exit
// code 0 — an answer about a scope the user never asked for.
func TestGetWarningsRejectsNonPositiveIDFilters(t *testing.T) {
	cases := []struct{ flag, value string }{
		{"--dataset-id", "0"},
		{"--dataset-id", "-2"},
		{"--data-source-id", "0"},
	}

	for _, tc := range cases {
		t.Run(tc.flag+"="+tc.value, func(t *testing.T) {
			we := newWarningsEnv(t)

			err := we.run(tc.flag, tc.value)
			if err == nil {
				t.Fatalf("expected an error for %s %s", tc.flag, tc.value)
			}
			if !strings.Contains(err.Error(), tc.flag+" must be a positive integer") {
				t.Errorf("error should name the flag and the rule, got: %v", err)
			}
			if we.calls != 0 {
				t.Errorf("expected /warnings never to be called, got %d call(s)", we.calls)
			}
		})
	}
}

func TestGetWarningsTableSelectorErrorsBeatMissingCredentials(t *testing.T) {
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)

	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "non-positive legacy alias",
			args: []string{"get", "warnings", "--dataset-id", "0"},
			want: "--dataset-id must be a positive integer",
		},
		{
			name: "legacy alias conflicts with table selector",
			args: []string{"get", "warnings", "--dataset-id", "42", "--table-id", "7"},
			want: "--dataset-id is an alias for --table-id; pass one, not both",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := setupWarningsTestCommand()
			cmd.SetArgs(tt.args)
			err := cmd.Execute()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d (ExitBadUsage)", got, qerrors.ExitBadUsage)
			}
		})
	}
	if got := len(mock.Requests()); got != 0 {
		t.Errorf("made %d request(s) without credentials", got)
	}
}

// The regression fence: positive IDs still reach the query string, both of them.
func TestGetWarningsSendsPositiveIDFilters(t *testing.T) {
	we := newWarningsEnv(t)

	if err := we.run("--dataset-id", "11", "--data-source-id", "22"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := we.query.Get("dataset_id"); got != "11" {
		t.Errorf("expected dataset_id=11, got %q", got)
	}
	if got := we.query.Get("data_source_model_id"); got != "22" {
		t.Errorf("expected data_source_model_id=22, got %q", got)
	}
}

func TestGetWarningsAcceptsTableID(t *testing.T) {
	we := newWarningsEnv(t)
	we.mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets",
		func(_ http.ResponseWriter, _ *http.Request) {
			t.Error("--table-id must not trigger a name lookup")
		})

	if err := we.run("--table-id", "42"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := we.query.Get("dataset_id"); got != "42" {
		t.Errorf("--table-id must send dataset_id=42, got %q", got)
	}
}

func TestGetWarningsAcceptsTableName(t *testing.T) {
	we := newWarningsEnv(t)
	var lookedUp string
	// The fixture's NAME is the numeric string "42" and its ID is 7 — the one
	// case an overloaded name-or-ID flag can never serve.
	we.mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets",
		func(w http.ResponseWriter, r *http.Request) {
			lookedUp = r.URL.Query().Get("name")
			testutil.RespondJSON(w, http.StatusOK, api.DatasetsListSchema{
				Results: []api.DataSetTinySchema{{ID: 7, Name: "42"}},
			})
		})

	if err := we.run("--table", "42"); err != nil {
		t.Fatalf("--table must be looked up as a name: %v", err)
	}
	if lookedUp != "42" {
		t.Errorf("--table 42 must query name=42, queried %q", lookedUp)
	}
	if got := we.query.Get("dataset_id"); got != "7" {
		t.Errorf("--table 42 must resolve to dataset_id=7, got %q", got)
	}
}

// A locally-invalid --order-by must fail before --table pays for a name
// lookup that can only be discarded. Resolving --table before
// parseWarningsParams runs makes this test fail: the trap fires because the
// lookup runs first.
func TestGetWarningsInvalidSortFieldSkipsTableLookup(t *testing.T) {
	we := newWarningsEnv(t)
	we.mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets",
		func(_ http.ResponseWriter, _ *http.Request) {
			t.Error("a bad --order-by must fail before --table is looked up")
		})

	err := we.run("--table", "nope", "--order-by", "bogus")
	if err == nil || !strings.Contains(err.Error(), "invalid sort field") {
		t.Fatalf("expected the sort-field error, got: %v", err)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (ExitBadUsage) for %v", got, qerrors.ExitBadUsage, err)
	}
	if we.calls != 0 {
		t.Errorf("expected /warnings never to be called, got %d call(s)", we.calls)
	}
}

func TestGetWarningsDatasetIDStillWorksAndIsHidden(t *testing.T) {
	we := newWarningsEnv(t)

	if err := we.run("--dataset-id", "42"); err != nil {
		t.Fatalf("the old spelling must keep working: %v", err)
	}
	if got := we.query.Get("dataset_id"); got != "42" {
		t.Errorf("--dataset-id must still send dataset_id=42, got %q", got)
	}

	flag := NewWarningsCommand().Flags().Lookup("dataset-id")
	if flag == nil {
		t.Fatal("--dataset-id must survive as an alias")
	}
	if !flag.Hidden {
		t.Error("--dataset-id must be hidden: --table-id is the documented spelling")
	}
}

// Both spellings at once is a contradiction, not a precedence question — and a
// script needs exit 2 to tell it from a retryable server failure.
func TestGetWarningsRejectsDatasetIDWithTableSelector(t *testing.T) {
	for _, selector := range [][]string{{"--table-id", "7"}, {"--table", "orders"}} {
		t.Run(selector[0], func(t *testing.T) {
			we := newWarningsEnv(t)
			we.mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets",
				func(_ http.ResponseWriter, _ *http.Request) {
					t.Error("a contradictory request must fail before any lookup")
				})

			err := we.run(append([]string{"--dataset-id", "42"}, selector...)...)
			if err == nil || !strings.Contains(err.Error(), "--dataset-id is an alias for --table-id; pass one, not both") {
				t.Fatalf("expected the alias-conflict error, got: %v", err)
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d (ExitBadUsage) for %v", got, qerrors.ExitBadUsage, err)
			}
			if we.calls != 0 {
				t.Errorf("expected /warnings never to be called, got %d call(s)", we.calls)
			}
		})
	}
}

// A warning's count is capped like a row-level alert's; the table marks it and
// -o json keeps it a number.
func TestGetWarningsMarksACappedCount(t *testing.T) {
	we := newWarningsEnv(t)
	capped := true
	results := []warnings.WarningTiny{
		{ID: 1, DatasetID: "10", DatasetName: "orders", IssueType: api.IssueTypeEnumAnomaly, Msg: "odd", Count: 1000, CountCapped: &capped},
		{ID: 2, DatasetID: "10", DatasetName: "orders", IssueType: api.IssueTypeEnumAnomaly, Msg: "odd", Count: 7},
	}
	we.mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/warnings", func(w http.ResponseWriter, _ *http.Request) {
		total, page := len(results), 1
		testutil.RespondJSON(w, http.StatusOK, warnings.WarningsListResponse{Results: results, TotalRows: &total, Page: &page})
	})

	run := func(args ...string) string {
		t.Helper()
		rootCmd := setupWarningsTestCommand()
		var stdout, stderr bytes.Buffer
		rootCmd.SetOut(&stdout)
		rootCmd.SetErr(&stderr)
		rootCmd.SetArgs(append([]string{"get", "warnings"}, args...))
		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v\n%s", err, stderr.String())
		}
		return stdout.String()
	}

	table := run("-o", "table", "--columns", "id,count")
	lines := strings.Split(strings.TrimSpace(table), "\n")
	if len(lines) != 3 {
		t.Fatalf("want a header and two rows, got:\n%s", table)
	}
	if fields := strings.Fields(lines[1]); len(fields) < 2 || fields[1] != "1000+" {
		t.Errorf("capped row count cell = %v, want 1000+:\n%s", fields, table)
	}
	if fields := strings.Fields(lines[2]); len(fields) < 2 || fields[1] != "7" {
		t.Errorf("uncapped row count cell = %v, want 7:\n%s", fields, table)
	}

	var page []map[string]interface{}
	decodeListItems(t, []byte(run("-o", "json")), &page)
	if len(page) != 2 || page[0]["count"] != float64(1000) || page[0]["count_capped"] != true {
		t.Errorf("-o json = %v, want count 1000 (a number) and count_capped true", page)
	}
}

// The warning list's table projection keeps every column the raw type has.
func TestCountCellRowsKeepsTheWarningColumns(t *testing.T) {
	rows := []warnings.WarningTiny{{ID: 1, Count: 3}}
	if got, want := output.AvailableColumns(issueCellRows(rows), nil), output.AvailableColumns(rows, nil); !reflect.DeepEqual(got, want) {
		t.Fatalf("columns = %v, want %v", got, want)
	}
}

func TestGetWarningsImpactSort(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		t.Run(map[bool]string{false: "descending", true: "ascending"}[reverse], func(t *testing.T) {
			we := newWarningsEnv(t)
			args := []string{"--order-by", "impact_score"}
			if reverse {
				args = append(args, "--reverse")
			}
			if err := we.run(args...); err != nil {
				t.Fatal(err)
			}
			if got := we.query.Get("order_by"); got != "impact_score" {
				t.Errorf("order_by = %q, want impact_score", got)
			}
			if got := we.query.Get("page"); got != "1" {
				t.Errorf("page = %q, want 1", got)
			}
			if reverse {
				if got := we.query.Get("reverse"); got != "false" {
					t.Errorf("reverse = %q, want false", got)
				}
			} else if we.query.Has("reverse") {
				t.Errorf("default must preserve server ordering, got %v", we.query)
			}
		})
	}
}

func TestGetWarningsIssueTypeIsForwarded(t *testing.T) {
	for _, issueType := range []string{"user_submitted", "new_warning_type"} {
		t.Run(issueType, func(t *testing.T) {
			we := newWarningsEnv(t)
			if err := we.run("--issue-type", issueType, "--table-id", "42", "--data-source-id", "7", "--resolved"); err != nil {
				t.Fatal(err)
			}
			for key, want := range map[string]string{
				"issue_type": issueType, "dataset_id": "42", "data_source_model_id": "7", "resolved": "true",
			} {
				if got := we.query.Get(key); got != want {
					t.Errorf("%s = %q, want %q", key, got, want)
				}
			}
		})
	}
}

func TestGetWarningsWithoutIssueTypeSendsNoFilter(t *testing.T) {
	for _, args := range [][]string{nil, {"--issue-type", ""}} {
		we := newWarningsEnv(t)
		if err := we.run(args...); err != nil {
			t.Fatal(err)
		}
		if we.query.Has("issue_type") {
			t.Errorf("empty issue type must send no filter, got %v", we.query)
		}
	}
}

func TestGetWarningsUnknownIssueTypeUsesServerRejection(t *testing.T) {
	we := newWarningsEnv(t)
	var reached bool
	we.mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/warnings", func(w http.ResponseWriter, r *http.Request) {
		reached = true
		if got := r.URL.Query().Get("issue_type"); got != "not_a_type" {
			t.Errorf("issue_type = %q, want not_a_type", got)
		}
		testutil.RespondJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"detail": []map[string]any{{"type": "enum", "loc": []any{"query", "issue_type"}, "msg": "Unknown warning issue type"}},
		})
	})
	err := we.run("--issue-type", "not_a_type")
	if !reached {
		t.Fatal("issue type was rejected before reaching the server")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d; error: %v", got, qerrors.ExitBadUsage, err)
	}
}

func TestWarningIssueTypeCompletion(t *testing.T) {
	complete, ok := NewWarningsCommand().GetFlagCompletionFunc("issue-type")
	if !ok {
		t.Fatal("--issue-type completion is not registered")
	}
	all, directive := complete(nil, nil, "")
	if directive != cobra.ShellCompDirectiveNoFileComp || !sort.StringsAreSorted(all) {
		t.Errorf("completion = %v, directive = %v; want sorted names and NoFileComp", all, directive)
	}
	if len(all) != len(api.IssueTypeWarningActions) {
		t.Errorf("completed %d types, want %d", len(all), len(api.IssueTypeWarningActions))
	}
	for _, name := range all {
		if _, exists := api.IssueTypeWarningActions[name]; !exists {
			t.Errorf("completed non-warning issue type %q", name)
		}
	}
	got, _ := complete(nil, nil, "user_")
	if !reflect.DeepEqual(got, []string{"user_submitted"}) {
		t.Errorf("completion for user_ = %v, want [user_submitted]", got)
	}
}

func TestGetWarningsImpactAndWhodunitColumns(t *testing.T) {
	we := newWarningsEnv(t)
	results := []warnings.WarningTiny{
		{ID: 1, DatasetID: "10", DatasetName: "orders", IssueType: api.IssueTypeEnumUserSubmitted, Msg: "check", Count: 1, ImpactScore: 1875.25,
			Whodunit: &api.UserInfoTinySchema{FirstName: "Dana", LastName: "Ray", Email: "dana@example.com"}},
		{ID: 2, DatasetID: "10", DatasetName: "orders", IssueType: api.IssueTypeEnumAnomaly, Msg: "odd", Count: 7, ImpactScore: 0},
	}
	we.mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/warnings", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, warnings.WarningsListResponse{
			Results: results, TotalRows: testutil.IntPtr(2), Page: testutil.IntPtr(1),
		})
	})
	run := func(args ...string) string {
		t.Helper()
		cmd := setupWarningsTestCommand()
		var stdout, stderr bytes.Buffer
		cmd.SetOut(&stdout)
		cmd.SetErr(&stderr)
		cmd.SetArgs(append([]string{"get", "warnings"}, args...))
		if err := cmd.Execute(); err != nil {
			t.Fatalf("Execute() = %v; stderr: %s", err, stderr.String())
		}
		return stdout.String()
	}
	defaultTable := run()
	if !strings.Contains(defaultTable, "IMPACT-SCORE") || !strings.Contains(defaultTable, "1875.25") {
		t.Errorf("default table lacks impact score:\n%s", defaultTable)
	}
	if strings.Contains(defaultTable, "WHODUNIT") {
		t.Errorf("whodunit should be opt-in:\n%s", defaultTable)
	}
	customTable := run("--columns", "id,impact_score,whodunit", "--max-column-width", "0")
	if !strings.Contains(customTable, "WHODUNIT") || !strings.Contains(customTable, "dana@example.com") {
		t.Errorf("custom table lacks whodunit:\n%s", customTable)
	}
	lines := strings.Split(strings.TrimSpace(customTable), "\n")
	if len(lines) != 3 || strings.Join(strings.Fields(lines[2]), " ") != "2 0" {
		t.Errorf("zero impact or absent whodunit rendered incorrectly:\n%s", customTable)
	}
	jsonOut := run("-o", "json")
	var items []warnings.WarningTiny
	decodeListItems(t, []byte(jsonOut), &items)
	if !reflect.DeepEqual(items, results) {
		t.Errorf("JSON items = %#v, want %#v", items, results)
	}
	testutil.AssertYAMLReadsAsJSON(t, run("-o", "yaml"), jsonOut)
	if got := run("-o", "name"); got != "warning/1\nwarning/2\n" {
		t.Errorf("name output = %q", got)
	}
}
