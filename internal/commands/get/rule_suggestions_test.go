package get

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

func TestGetRuleSuggestionsShowParamsReadsOnlyTheSelectedPage(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"flag", []string{"--show-params", "--show-kind"}},
		{"column", []string{"--columns", "id, params"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := newRuleSuggestionsTestEnv(t)
			first, second := sampleSuggestionTiny(), sampleSuggestionTiny()
			first.Action = api.SuggestionActionCreate
			first.Source = api.Profiling
			second.ID = uuid.MustParse("77777777-7777-7777-7777-777777777777")
			base := "/api/orgs/" + suggestionsTestOrgID + "/datasets/8/rule-suggestions"
			listReads, detailReads := 0, 0
			mock.RegisterHandler("GET", base, func(w http.ResponseWriter, r *http.Request) {
				listReads++
				if r.URL.Query().Get("page") != "2" || r.URL.Query().Get("limit") != "2" {
					t.Errorf("unexpected pagination: %s", r.URL.RawQuery)
				}
				testutil.RespondJSON(w, http.StatusOK, api.DatasetRuleSuggestionListSchema{
					Results: []api.DatasetRuleSuggestionTinySchema{first, second},
					Next:    &api.PaginationSchema{},
				})
			})
			for id, params := range map[string]string{
				first.ID.String():  `{"allowed":["A","B"],"enabled":false,"large":9007199254740993}`,
				second.ID.String(): `{"min":12.345,"optional":null}`,
			} {
				mock.RegisterHandler("GET", base+"/"+id, func(w http.ResponseWriter, _ *http.Request) {
					detailReads++
					testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"id":"`+id+`","params":`+params+`}`))
				})
			}
			args := append([]string{"--table-id", "8", "--page", "2", "--limit", "2", "--max-column-width", "0"}, tc.args...)
			out, stderr, err := runGetRuleSuggestions(t, args...)
			if err != nil {
				t.Fatal(err)
			}
			if listReads != 1 || detailReads != 2 {
				t.Fatalf("list reads = %d, detail reads = %d; want 1 and 2", listReads, detailReads)
			}
			for _, want := range []string{"PARAMS", `"allowed":["A","B"]`, `"enabled":false`, "9007199254740993", `"min":12.345`, `"optional":null`} {
				if !strings.Contains(out, want) {
					t.Errorf("missing %q:\n%s", want, out)
				}
			}
			for id, want := range map[string]string{first.ID.String(): "9007199254740993", second.ID.String(): "12.345"} {
				for _, line := range strings.Split(out, "\n") {
					if strings.Contains(line, id) && !strings.Contains(line, want) {
						t.Errorf("params attached to the wrong suggestion: %s", line)
					}
				}
			}
			if !strings.Contains(stderr, "--page 3") {
				t.Errorf("missing next-page hint: %s", stderr)
			}
		})
	}
}

func TestGetRuleSuggestionsSkipsUnneededParamReads(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"--show-params", "--columns", "id,rule"},
		{"--show-params", "--columns", "params", "-o", "json"},
		{"--show-params", "--columns", "params", "-o", "yaml"},
		{"--show-params", "--columns", "params", "-o", "name"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			mock := newRuleSuggestionsTestEnv(t)
			base := "/api/orgs/" + suggestionsTestOrgID + "/datasets/8/rule-suggestions"
			mock.RegisterHandler("GET", base, func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, api.DatasetRuleSuggestionListSchema{
					Results: []api.DatasetRuleSuggestionTinySchema{sampleSuggestionTiny()},
				})
			})
			reads := 0
			mock.RegisterHandler("GET", base+"/"+rsTestSuggestionUUID.String(), func(w http.ResponseWriter, _ *http.Request) {
				reads++
				w.WriteHeader(http.StatusInternalServerError)
			})
			out, _, err := runGetRuleSuggestions(t, append([]string{"--table-id", "8"}, args...)...)
			if err != nil {
				t.Fatal(err)
			}
			if reads != 0 || strings.Contains(out, "PARAMS") || strings.Contains(out, `"params"`) {
				t.Errorf("unexpected parameter lookup or output (reads %d):\n%s", reads, out)
			}
			if !strings.Contains(out, rsTestSuggestionUUID.String()) {
				t.Errorf("missing suggestion id:\n%s", out)
			}
		})
	}
}

func TestGetRuleSuggestionsParamsFailurePreservesExitCode(t *testing.T) {
	for _, code := range []int{http.StatusNotFound, http.StatusForbidden, http.StatusInternalServerError} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			mock := newRuleSuggestionsTestEnv(t)
			base := "/api/orgs/" + suggestionsTestOrgID + "/datasets/8/rule-suggestions"
			mock.RegisterHandler("GET", base, func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, api.DatasetRuleSuggestionListSchema{
					Results: []api.DatasetRuleSuggestionTinySchema{sampleSuggestionTiny()},
				})
			})
			mock.RegisterHandler("GET", base+"/"+rsTestSuggestionUUID.String(), func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, code, map[string]string{"detail": "unavailable"})
			})
			out, _, err := runGetRuleSuggestions(t, "--table-id", "8", "--show-params")
			if err == nil || qerrors.GetExitCode(err) != qerrors.GetExitCode(qerrors.FromHTTPStatus(code, "unavailable")) {
				t.Fatalf("unexpected exit code for HTTP %d: %v", code, err)
			}
			if !strings.Contains(err.Error(), rsTestSuggestionUUID.String()) || out != "" {
				t.Errorf("failure must identify the suggestion without partial stdout: %v\n%s", err, out)
			}
		})
	}
}

func TestGetRuleSuggestionsShowParamsEmptyIsNotAFilter(t *testing.T) {
	mock := newRuleSuggestionsTestEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+suggestionsTestOrgID+"/datasets/8/rule-suggestions", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.DatasetRuleSuggestionListSchema{})
	})
	_, stderr, err := runGetRuleSuggestions(t, "--table-id", "8", "--show-params")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stderr, "--show-params") {
		t.Errorf("display flag reported as a filter: %s", stderr)
	}
}

const suggestionsTestOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

var rsTestSuggestionUUID = uuid.MustParse("3f2a0000-1111-2222-3333-444444444444")

func setupRuleSuggestionsTestCommand() *cobra.Command {
	rootCmd := &cobra.Command{Use: "qctl", SilenceUsage: true, SilenceErrors: true}
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format")
	rootCmd.PersistentFlags().Bool("no-headers", false, "Don't print column headers")
	rootCmd.PersistentFlags().String("columns", "", "Columns to display")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "Maximum width for table columns")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "Show secret fields")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "Allow http://")
	rootCmd.PersistentFlags().CountP("verbose", "v", "Verbosity level")
	getCmd := &cobra.Command{Use: "get"}
	getCmd.AddCommand(NewRuleSuggestionsCommand())
	rootCmd.AddCommand(getCmd)
	return rootCmd
}

func newRuleSuggestionsTestEnv(t *testing.T) *testutil.MockAPIServer {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", suggestionsTestOrgID)
	env.SetupCredential(endpointKey, suggestionsTestOrgID, "test-token")
	return mock
}

func sampleSuggestionTiny() api.DatasetRuleSuggestionTinySchema {
	return api.DatasetRuleSuggestionTinySchema{
		ID:           rsTestSuggestionUUID,
		InstanceName: "high_confidence_dedupe",
		Action:       api.SuggestionActionUpdate,
		State:        api.SuggestionStatePending,
		Source:       api.User,
		Severity:     api.RuleSeverityBlocker,
		CreatedAt:    time.Now().Add(-2 * time.Hour),
	}
}

func runGetRuleSuggestions(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := setupRuleSuggestionsTestCommand()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(append([]string{"get", "rule-suggestions"}, args...))
	err := cmd.Execute()
	return out.String(), errOut.String(), err
}

func TestGetRuleSuggestionsDefaultsToPending(t *testing.T) {
	mock := newRuleSuggestionsTestEnv(t)
	var gotQuery string
	mock.RegisterHandler("GET", "/api/orgs/"+suggestionsTestOrgID+"/datasets/8/rule-suggestions",
		func(w http.ResponseWriter, r *http.Request) {
			gotQuery = r.URL.RawQuery
			testutil.RespondJSON(w, http.StatusOK, api.DatasetRuleSuggestionListSchema{
				Results: []api.DatasetRuleSuggestionTinySchema{sampleSuggestionTiny()},
			})
		})

	out, _, err := runGetRuleSuggestions(t, "--table-id", "8")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(gotQuery, "state=pending") {
		t.Errorf("default must filter state=pending, query: %s", gotQuery)
	}
	for _, want := range []string{rsTestSuggestionUUID.String(), "high_confidence_dedupe", "update", "pending", "user"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n%s", want, out)
		}
	}
}

func TestGetRuleSuggestionsForwardsFilters(t *testing.T) {
	mock := newRuleSuggestionsTestEnv(t)
	var gotQuery string
	mock.RegisterHandler("GET", "/api/orgs/"+suggestionsTestOrgID+"/datasets/8/rule-suggestions",
		func(w http.ResponseWriter, r *http.Request) {
			gotQuery = r.URL.RawQuery
			testutil.RespondJSON(w, http.StatusOK, api.DatasetRuleSuggestionListSchema{})
		})

	_, _, err := runGetRuleSuggestions(t, "8",
		"--state", "applied", "--state", "dismissed", "--source", "profiling")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"state=applied", "state=dismissed", "source=profiling"} {
		if !strings.Contains(gotQuery, want) {
			t.Errorf("query missing %q, got: %s", want, gotQuery)
		}
	}
	if strings.Contains(gotQuery, "state=pending") {
		t.Errorf("explicit --state must replace the pending default, query: %s", gotQuery)
	}
}

func TestGetRuleSuggestionsTableTwiceRejected(t *testing.T) {
	newRuleSuggestionsTestEnv(t)
	_, _, err := runGetRuleSuggestions(t, "8", "--table", "9")
	if err == nil || !strings.Contains(err.Error(), "table specified more than once") {
		t.Fatalf("expected table-specified-more-than-once error, got: %v", err)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (ExitBadUsage) for %v", got, qerrors.ExitBadUsage, err)
	}
}

func TestGetRuleSuggestionsRequiresTable(t *testing.T) {
	newRuleSuggestionsTestEnv(t)
	_, _, err := runGetRuleSuggestions(t)
	if err == nil || !strings.Contains(err.Error(), "table is required") {
		t.Fatalf("expected table-required error, got: %v", err)
	}
	// This command really does take the table positionally, so its guidance
	// must keep offering that — the flags-only sibling's must not.
	if !strings.Contains(err.Error(), "the positional argument") {
		t.Errorf("get rule-suggestions takes a table positionally; the error must say so: %v", err)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (ExitBadUsage) for %v", got, qerrors.ExitBadUsage, err)
	}
}

func TestGetRuleSuggestionsTableIDFlag(t *testing.T) {
	mock := newRuleSuggestionsTestEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+suggestionsTestOrgID+"/datasets/8/rule-suggestions",
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.DatasetRuleSuggestionListSchema{
				Results: []api.DatasetRuleSuggestionTinySchema{sampleSuggestionTiny()},
			})
		})

	if _, _, err := runGetRuleSuggestions(t, "--table-id", "8"); err != nil {
		t.Fatalf("--table-id must select the table by ID: %v", err)
	}
}

func TestGetRuleSuggestionsTableFlagIsAName(t *testing.T) {
	mock := newRuleSuggestionsTestEnv(t)
	var lookedUp string
	mock.RegisterHandler("GET", "/api/orgs/"+suggestionsTestOrgID+"/datasets",
		func(w http.ResponseWriter, r *http.Request) {
			lookedUp = r.URL.Query().Get("name")
			testutil.RespondJSON(w, http.StatusOK, api.DatasetsListSchema{
				Results: []api.DataSetTinySchema{{ID: 8, Name: "42"}},
			})
		})
	mock.RegisterHandler("GET", "/api/orgs/"+suggestionsTestOrgID+"/datasets/8/rule-suggestions",
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.DatasetRuleSuggestionListSchema{})
		})

	// A table named "42" is the case one overloaded flag can never serve.
	if _, _, err := runGetRuleSuggestions(t, "--table", "42"); err != nil {
		t.Fatalf("--table must be looked up as a name: %v", err)
	}
	if lookedUp != "42" {
		t.Errorf("--table 42 must query name=42, queried %q", lookedUp)
	}
}

func TestGetRuleSuggestionsPaginationHint(t *testing.T) {
	mock := newRuleSuggestionsTestEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+suggestionsTestOrgID+"/datasets/8/rule-suggestions",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.DatasetRuleSuggestionListSchema{
				Results: []api.DatasetRuleSuggestionTinySchema{sampleSuggestionTiny()},
				Next:    &api.PaginationSchema{},
			})
		})

	_, errOut, err := runGetRuleSuggestions(t, "--table-id", "8")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(errOut, "--page 2") {
		t.Errorf("stderr missing pagination hint\n%s", errOut)
	}
}

func TestGetRuleSuggestionsRawJSON(t *testing.T) {
	mock := newRuleSuggestionsTestEnv(t)
	candidate := sampleSuggestionTiny()
	yes := true
	candidate.IsRevalidationCandidate = &yes
	mock.RegisterHandler("GET", "/api/orgs/"+suggestionsTestOrgID+"/datasets/8/rule-suggestions",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.DatasetRuleSuggestionListSchema{
				Results: []api.DatasetRuleSuggestionTinySchema{candidate},
			})
		})

	out, _, err := runGetRuleSuggestions(t, "--table-id", "8", "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `"results"`) || !strings.Contains(out, `"instance_name"`) ||
		!strings.Contains(out, `"is_revalidation_candidate": true`) {
		t.Errorf("-o json must emit the raw list\n%s", out)
	}
}

func TestGetRuleSuggestionsShowsDatasetColumns(t *testing.T) {
	mock := newRuleSuggestionsTestEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+suggestionsTestOrgID+"/datasets/8/rule-suggestions",
		func(w http.ResponseWriter, r *http.Request) {
			s := sampleSuggestionTiny()
			s.DatasetColumns = &[]string{"email", "phone"}
			testutil.RespondJSON(w, http.StatusOK, api.DatasetRuleSuggestionListSchema{
				Results: []api.DatasetRuleSuggestionTinySchema{s},
			})
		})

	out, _, err := runGetRuleSuggestions(t, "--table-id", "8")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "DATASET-COLUMNS") {
		t.Errorf("default grid missing the DATASET-COLUMNS header\n%s", out)
	}
	if !strings.Contains(out, "email") || !strings.Contains(out, "phone") {
		t.Errorf("default grid missing the target columns\n%s", out)
	}
}

func TestGetRuleSuggestionsDeclaresItsColumns(t *testing.T) {
	cmd := setupRuleSuggestionsTestCommand()
	flag := cmd.Commands()[0].Commands()[0].Flags().Lookup("columns")
	if flag == nil {
		t.Fatal("get rule-suggestions must declare its own --columns flag; the inherited one documents no list")
	}
	for _, want := range []string{"id", "rule", "type", "dataset_columns", "action", "state", "source", "severity", "created"} {
		if !strings.Contains(flag.Usage, want) {
			t.Errorf("--columns usage does not list %q\n%s", want, flag.Usage)
		}
	}
}

func TestGetRuleSuggestionsRejectsUnknownColumn(t *testing.T) {
	mock := newRuleSuggestionsTestEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+suggestionsTestOrgID+"/datasets/8/rule-suggestions",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.DatasetRuleSuggestionListSchema{})
		})

	_, _, err := runGetRuleSuggestions(t, "--table-id", "8", "--columns", "dataset_colums")
	if err == nil || !strings.Contains(err.Error(), `unknown column "dataset_colums"`) {
		t.Fatalf("expected an unknown-column error even on an empty result, got %v", err)
	}
}

// TestGetRuleSuggestionsCommand_EmptyWithPositionalNamesTheTable: `get
// rule-suggestions orders` is scoped to one table, same as `get table-rules
// orders` (table_rules_test.go's EmptyWithPositionalNamesTheTable). Unlike
// table-rules, rule-suggestions also applies an implicit `--state pending`
// (ruleSuggestionListOptions in rule_suggestions.go) when the flag is never set
// — since activeFilters only echoes flags cmd.Flags().Visit() reports as
// Changed, that default must NOT appear in the message alongside the positional.
func TestGetRuleSuggestionsCommand_EmptyWithPositionalNamesTheTable(t *testing.T) {
	mock := newRuleSuggestionsTestEnv(t)

	// Name resolution: datasets.Client.ResolveID lists datasets filtered by name.
	mock.RegisterHandler("GET", "/api/orgs/"+suggestionsTestOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"results": []map[string]interface{}{{"id": 8, "name": "orders"}},
		})
	})
	mock.RegisterHandler("GET", "/api/orgs/"+suggestionsTestOrgID+"/datasets/8/rule-suggestions",
		func(w http.ResponseWriter, r *http.Request) {
			totalRows := 0
			testutil.RespondJSON(w, http.StatusOK, api.DatasetRuleSuggestionListSchema{
				Results:   []api.DatasetRuleSuggestionTinySchema{},
				TotalRows: &totalRows,
			})
		})

	out, errOut, err := runGetRuleSuggestions(t, "orders")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	want := `No rule suggestions matched "orders" in org "` + suggestionsTestOrgID + `" (context: "default").` + "\n"
	if errOut != want {
		t.Errorf("stderr = %q, want %q", errOut, want)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("stdout = %q, want empty", out)
	}
}

// `failed` is a real SuggestionState (only a revalidation candidate reaches
// it) and must be accepted; a typo must be rejected before any request.
func TestGetRuleSuggestionsAcceptsFailedState(t *testing.T) {
	mock := newRuleSuggestionsTestEnv(t)
	var gotQuery string
	mock.RegisterHandler("GET", "/api/orgs/"+suggestionsTestOrgID+"/datasets/8/rule-suggestions",
		func(w http.ResponseWriter, r *http.Request) {
			gotQuery = r.URL.RawQuery
			testutil.RespondJSON(w, http.StatusOK, api.DatasetRuleSuggestionListSchema{})
		})

	_, _, err := runGetRuleSuggestions(t, "--table-id", "8", "--state", "failed")
	if err != nil {
		t.Fatalf("--state failed must be accepted: %v", err)
	}
	if !strings.Contains(gotQuery, "state=failed") {
		t.Errorf("query missing state=failed, got: %s", gotQuery)
	}
}

func TestGetRuleSuggestionsRejectsUnknownState(t *testing.T) {
	mock := newRuleSuggestionsTestEnv(t)
	hit := false
	mock.RegisterHandler("GET", "/api/orgs/"+suggestionsTestOrgID+"/datasets/8/rule-suggestions",
		func(w http.ResponseWriter, _ *http.Request) {
			hit = true
			testutil.RespondJSON(w, http.StatusUnprocessableEntity, map[string]interface{}{})
		})

	_, _, err := runGetRuleSuggestions(t, "--table-id", "8", "--state", "pendign")
	if err == nil {
		t.Fatal("an unknown --state must be rejected client-side")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (ExitBadUsage) for %v", got, qerrors.ExitBadUsage, err)
	}
	if hit {
		t.Error("an unknown --state must be rejected without a list request")
	}
}

// The check runs before ResolveTableID, which costs an API call — the same
// ordering pagination validation already gets.
func TestGetRuleSuggestionsBadStateCostsNoNameLookup(t *testing.T) {
	mock := newRuleSuggestionsTestEnv(t)
	resolved := false
	mock.RegisterHandler("GET", "/api/orgs/"+suggestionsTestOrgID+"/datasets",
		func(w http.ResponseWriter, r *http.Request) {
			resolved = true
			testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{"results": []interface{}{}})
		})

	_, _, err := runGetRuleSuggestions(t, "--table", "orders", "--state", "nope")
	if err == nil {
		t.Fatal("expected rejection")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (ExitBadUsage) for %v", got, qerrors.ExitBadUsage, err)
	}
	if resolved {
		t.Error("a rejected --state must not cost a table name lookup")
	}
}

func TestGetRuleSuggestionsRejectsUnknownSource(t *testing.T) {
	mock := newRuleSuggestionsTestEnv(t)
	hit := false
	mock.RegisterHandler("GET", "/api/orgs/"+suggestionsTestOrgID+"/datasets/8/rule-suggestions",
		func(w http.ResponseWriter, _ *http.Request) {
			hit = true
			testutil.RespondJSON(w, http.StatusUnprocessableEntity, map[string]interface{}{})
		})

	_, _, err := runGetRuleSuggestions(t, "--table-id", "8", "--source", "profilling")
	if err == nil {
		t.Fatal("an unknown --source must be rejected client-side")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (ExitBadUsage) for %v", got, qerrors.ExitBadUsage, err)
	}
	if hit {
		t.Error("an unknown --source must be rejected without a list request")
	}
}

func TestGetRuleSuggestionsBadSourceCostsNoNameLookup(t *testing.T) {
	mock := newRuleSuggestionsTestEnv(t)
	resolved := false
	mock.RegisterHandler("GET", "/api/orgs/"+suggestionsTestOrgID+"/datasets",
		func(w http.ResponseWriter, _ *http.Request) {
			resolved = true
			testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{"results": []interface{}{}})
		})

	_, _, err := runGetRuleSuggestions(t, "--table", "orders", "--source", "nope")
	if err == nil {
		t.Fatal("expected rejection")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (ExitBadUsage) for %v", got, qerrors.ExitBadUsage, err)
	}
	if resolved {
		t.Error("a rejected --source must not cost a table name lookup")
	}
}

// generatedSuggestionEnumValues returns every string literal assigned to a
// constant of type typeName in the generated client. It parses the source
// rather than reflecting, because Go has no way to enumerate the members of a
// string-typed constant group at runtime.
func generatedSuggestionEnumValues(t *testing.T, typeName string) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "../../api/client.gen.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse client.gen.go: %v", err)
	}
	var out []string
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			ident, ok := vs.Type.(*ast.Ident)
			if !ok || ident.Name != typeName || len(vs.Values) == 0 {
				continue
			}
			lit, ok := vs.Values[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				continue
			}
			value, err := strconv.Unquote(lit.Value)
			if err != nil {
				t.Fatalf("unquote %s: %v", lit.Value, err)
			}
			out = append(out, value)
		}
	}
	return out
}

// TestSuggestionStatesCoverTheGeneratedEnum is the half a compiler cannot
// catch. Every element of suggestionStates is an api.* constant, so a RENAMED
// value fails to build; this is what catches a value ADDED to the spec that
// nobody added here — the failure mode that matters, because --state would
// then reject a state the backend considers legitimate.
func TestSuggestionStatesCoverTheGeneratedEnum(t *testing.T) {
	want := generatedSuggestionEnumValues(t, "SuggestionState")
	if len(want) < 4 {
		t.Fatalf("parsed only %d SuggestionState constants; the parse is broken", len(want))
	}
	got := append([]string(nil), validSuggestionStates...)
	sort.Strings(want)
	sort.Strings(got)
	if len(got) != len(want) {
		t.Fatalf("suggestionStates has %d values, client.gen.go declares %d\n got: %v\nwant: %v\n\n"+
			"Add the missing api.SuggestionState constant to suggestionStates.",
			len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("suggestionStates drifted from client.gen.go at %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSuggestionSourcesCoverTheGeneratedEnum(t *testing.T) {
	want := generatedSuggestionEnumValues(t, "SuggestionSource")
	if len(want) < 3 {
		t.Fatalf("parsed only %d SuggestionSource constants; the parse is broken", len(want))
	}
	got := append([]string(nil), validSuggestionSources...)
	sort.Strings(want)
	sort.Strings(got)
	if len(got) != len(want) {
		t.Fatalf("suggestionSources has %d values, client.gen.go declares %d\n got: %v\nwant: %v\n\n"+
			"Add the missing api.SuggestionSource constant to suggestionSources.",
			len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("suggestionSources drifted from client.gen.go at %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

// The rejection message and the --state help text both come from
// suggestionStates, so neither can drift from the vocabulary the CLI accepts.
func TestSuggestionStateVocabularyIsShownInDeclarationOrder(t *testing.T) {
	err := validateSuggestionStates([]string{"nope"})
	if err == nil {
		t.Fatal("expected a rejection")
	}
	const want = `invalid --state "nope" (valid: pending, applied, dismissed, failed)`
	if err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
	usage := NewRuleSuggestionsCommand().Flags().Lookup("state").Usage
	if usage != "Filter by state: pending, applied, dismissed, failed (repeatable; default: pending)" {
		t.Errorf("--state usage = %q", usage)
	}
}

func TestSuggestionSourceVocabularyIsShownInDeclarationOrder(t *testing.T) {
	err := validateSuggestionSource("nope")
	if err == nil {
		t.Fatal("expected a rejection")
	}
	const want = `invalid --source "nope" (valid: periodic, profiling, recreated_period, user)`
	if err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
	usage := NewRuleSuggestionsCommand().Flags().Lookup("source").Usage
	if usage != "Filter by source: periodic, profiling, recreated_period, user" {
		t.Errorf("--source usage = %q", usage)
	}
}

func TestGetRuleSuggestionsShowKindUsesTheListRowWithoutADetailRead(t *testing.T) {
	mock := newRuleSuggestionsTestEnv(t)
	candidate := sampleSuggestionTiny()
	yes := true
	candidate.IsRevalidationCandidate = &yes
	mock.RegisterHandler("GET", "/api/orgs/"+suggestionsTestOrgID+"/datasets/8/rule-suggestions",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.DatasetRuleSuggestionListSchema{
				Results: []api.DatasetRuleSuggestionTinySchema{candidate},
			})
		})
	detailReads := 0
	mock.RegisterHandler("GET", "/api/orgs/"+suggestionsTestOrgID+"/datasets/8/rule-suggestions/"+rsTestSuggestionUUID.String(),
		func(w http.ResponseWriter, r *http.Request) {
			detailReads++
			w.WriteHeader(http.StatusInternalServerError)
		})

	out, _, err := runGetRuleSuggestions(t, "--table-id", "8", "--show-kind")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if detailReads != 0 {
		t.Errorf("candidate classification must use the list row; got %d detail reads", detailReads)
	}
	if !strings.Contains(out, "candidate") {
		t.Errorf("a candidate row must be labeled:\n%s", out)
	}
}

func TestGetRuleSuggestionsWithoutShowKindOmitsType(t *testing.T) {
	mock := newRuleSuggestionsTestEnv(t)
	candidate := sampleSuggestionTiny()
	yes := true
	candidate.IsRevalidationCandidate = &yes
	mock.RegisterHandler("GET", "/api/orgs/"+suggestionsTestOrgID+"/datasets/8/rule-suggestions",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.DatasetRuleSuggestionListSchema{
				Results: []api.DatasetRuleSuggestionTinySchema{candidate},
			})
		})

	out, _, err := runGetRuleSuggestions(t, "--table-id", "8")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out, "TYPE") {
		t.Errorf("no TYPE column without --show-kind:\n%s", out)
	}
}

func TestGetRuleSuggestionsShowKindLabelsAFalseListFlagAsEdit(t *testing.T) {
	mock := newRuleSuggestionsTestEnv(t)
	edit := sampleSuggestionTiny()
	no := false
	edit.IsRevalidationCandidate = &no
	mock.RegisterHandler("GET", "/api/orgs/"+suggestionsTestOrgID+"/datasets/8/rule-suggestions",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.DatasetRuleSuggestionListSchema{
				Results: []api.DatasetRuleSuggestionTinySchema{edit},
			})
		})

	out, _, err := runGetRuleSuggestions(t, "--table-id", "8", "--show-kind")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "edit") {
		t.Errorf("a non-candidate row must be labeled edit:\n%s", out)
	}
}

// The generated field is optional because OpenAPI defaults are not required.
// A response from an older server therefore remains safely classifiable.
func TestGetRuleSuggestionsShowKindLabelsAnAbsentListFlagAsEdit(t *testing.T) {
	mock := newRuleSuggestionsTestEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+suggestionsTestOrgID+"/datasets/8/rule-suggestions",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.DatasetRuleSuggestionListSchema{
				Results: []api.DatasetRuleSuggestionTinySchema{sampleSuggestionTiny()},
			})
		})

	out, _, err := runGetRuleSuggestions(t, "--table-id", "8", "--show-kind")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "edit") {
		t.Errorf("an omitted candidate flag must degrade to edit:\n%s", out)
	}
}

func TestGetRuleSuggestionsShowKindLabelsANonPendingCandidate(t *testing.T) {
	mock := newRuleSuggestionsTestEnv(t)
	dismissed := sampleSuggestionTiny()
	dismissed.State = api.SuggestionStateDismissed
	yes := true
	dismissed.IsRevalidationCandidate = &yes
	mock.RegisterHandler("GET", "/api/orgs/"+suggestionsTestOrgID+"/datasets/8/rule-suggestions",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.DatasetRuleSuggestionListSchema{
				Results: []api.DatasetRuleSuggestionTinySchema{dismissed},
			})
		})

	out, _, err := runGetRuleSuggestions(t, "--table-id", "8", "--state", "dismissed", "--show-kind")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "candidate") {
		t.Errorf("a dismissed candidate must be labeled candidate, not edit:\n%s", out)
	}
}

// Under -o json|yaml|name --show-kind is inert because those formats emit the
// raw response (which now carries is_revalidation_candidate itself).
func TestGetRuleSuggestionsShowKindIsInertForStructuredOutput(t *testing.T) {
	for _, format := range []string{"json", "yaml", "name"} {
		t.Run(format, func(t *testing.T) {
			mock := newRuleSuggestionsTestEnv(t)
			mock.RegisterHandler("GET", "/api/orgs/"+suggestionsTestOrgID+"/datasets/8/rule-suggestions",
				func(w http.ResponseWriter, r *http.Request) {
					testutil.RespondJSON(w, http.StatusOK, api.DatasetRuleSuggestionListSchema{
						Results: []api.DatasetRuleSuggestionTinySchema{sampleSuggestionTiny()},
					})
				})
			out, _, err := runGetRuleSuggestions(t, "--table-id", "8", "--show-kind", "-o", format)
			if err != nil {
				t.Fatalf("--show-kind must be inert, not an error, for -o %s: %v", format, err)
			}
			if out == "" {
				t.Errorf("-o %s produced no output", format)
			}
		})
	}
}

// TestGetRuleSuggestionsSeverityIsOptInNotDefault pins both halves of the
// column's contract. It is available -- `--columns severity` prints the enum
// word the tiny schema now carries -- and it is NOT in the default grid,
// where on the draft-migration path it would only restate the live rule's
// value (atlas patch_dataset_rule, capi/capi/views/rule_view.py) that
// `get table-rules` already prints in its own SEVERITY column.
func TestGetRuleSuggestionsSeverityIsOptInNotDefault(t *testing.T) {
	register := func(t *testing.T) {
		t.Helper()
		mock := newRuleSuggestionsTestEnv(t)
		mock.RegisterHandler("GET", "/api/orgs/"+suggestionsTestOrgID+"/datasets/8/rule-suggestions",
			func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, api.DatasetRuleSuggestionListSchema{
					Results: []api.DatasetRuleSuggestionTinySchema{sampleSuggestionTiny()},
				})
			})
	}

	t.Run("opt in", func(t *testing.T) {
		register(t)
		out, _, err := runGetRuleSuggestions(t, "--table-id", "8", "--columns", "id,severity")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(out, "SEVERITY") {
			t.Errorf("--columns severity did not print the header\n%s", out)
		}
		if !strings.Contains(out, string(api.RuleSeverityBlocker)) {
			t.Errorf("--columns severity did not print %q\n%s", api.RuleSeverityBlocker, out)
		}
	})

	t.Run("not in the default grid", func(t *testing.T) {
		register(t)
		out, _, err := runGetRuleSuggestions(t, "--table-id", "8")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if strings.Contains(out, "SEVERITY") {
			t.Errorf("severity must stay opt-in, not join the default grid\n%s", out)
		}
	})
}
