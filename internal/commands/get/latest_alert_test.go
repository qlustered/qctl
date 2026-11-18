package get

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

// setupLatestAlertTestCommand builds a root command carrying the global flags
// the real root registers, with `get latest-alert` mounted under it.
func setupLatestAlertTestCommand() *cobra.Command {
	rootCmd := &cobra.Command{Use: "qctl"}
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format (table|json|yaml|name)")
	rootCmd.PersistentFlags().Bool("no-headers", false, "Don't print column headers")
	rootCmd.PersistentFlags().String("columns", "", "Comma-separated list of columns to display")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "Maximum width for table columns")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "Show secret fields in plaintext")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "Allow non-localhost http://")
	rootCmd.PersistentFlags().CountP("verbose", "v", "Enable verbose output")

	getCmd := &cobra.Command{Use: "get", Short: "Display resources"}
	getCmd.AddCommand(NewLatestAlertCommand())
	rootCmd.AddCommand(getCmd)
	return rootCmd
}

// menuStatsWithAlert is the fixture for a table that has something to look at.
func menuStatsWithAlert() api.MenuStatsSchema {
	alertID := 4821
	msg := "new column `region` needs review"
	alertType := api.AlertType("needs_review_migration")
	return api.MenuStatsSchema{
		UnresolvedAlertsCount:         2,
		AlertCountThatUserCantResolve: 0,
		ErrorsCount:                   0,
		LatestAlertItemID:             &alertID,
		LatestAlertItemMsg:            &msg,
		LatestAlertItemType:           &alertType,
	}
}

// runLatestAlert wires a mock server and executes `get latest-alert` with args.
// The fixture parameter is deliberately not named `stats` -- that is the domain
// package's name in this file's package.
func runLatestAlert(t *testing.T, payload api.MenuStatsSchema, args ...string) (string, error) {
	t.Helper()
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/menu-stats", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, payload)
	})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{"id": 42, "name": "orders"})
	})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"total_rows": 1, "page": 1,
			"results": []interface{}{map[string]interface{}{"id": 42, "name": "orders", "state": "active"}},
		})
	})

	cmd := setupLatestAlertTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(append([]string{"get", "latest-alert"}, args...))
	err := cmd.Execute()
	return buf.String(), err
}

func TestLatestAlertTextOutput(t *testing.T) {
	out, err := runLatestAlert(t, menuStatsWithAlert(), "--table-id", "42")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, want := range []string{
		"4821",
		"needs_review_migration",
		"new column `region` needs review",
		"qctl describe alert 4821",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\ngot:\n%s", want, out)
		}
	}
}

func TestLatestAlertAcceptsAPositionalTable(t *testing.T) {
	out, err := runLatestAlert(t, menuStatsWithAlert(), "42")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "4821") {
		t.Errorf("positional table did not reach the endpoint; got:\n%s", out)
	}
}

// The rendered view names the table, and a positional NAME must reach that
// slot -- otherwise the positional form renders a blank where --table renders
// a name. A numeric positional is an ID and must not be printed as one.
func TestLatestAlertPositionalNameReachesTheView(t *testing.T) {
	named, err := runLatestAlert(t, menuStatsWithAlert(), "orders")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(named, "orders") {
		t.Errorf("output does not name the table; got:\n%s", named)
	}

	byID, err := runLatestAlert(t, menuStatsWithAlert(), "42")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// label() renders "id 42" when tableName is "" (the correct outcome for a
	// numeric positional) and "42 (id: 42)" when tableName was wrongly set to
	// the positional's raw string (the regression this test guards against) --
	// see renderOptions.label() in latest_alert.go. Assert both directions so
	// a broken strconv.Atoi guard cannot pass by leaving neither string typed.
	if !strings.Contains(byID, "id 42") {
		t.Errorf("a numeric positional should render as an id; got:\n%s", byID)
	}
	if strings.Contains(byID, "42 (id: 42)") {
		t.Errorf("a numeric positional is an ID, not a name; got:\n%s", byID)
	}
}

// TestLatestAlertTableNameResolves drives the primary documented path,
// --table <name> -- every EXAMPLE in latest-alert.md uses it, yet every other
// test in this file passes --table-id and never exercises name resolution at
// all. The mock server serves the real /datasets name lookup (the same
// pattern get/warnings_test.go and get/table_rule_test.go use), so this
// exercises cmdutil.ResolveTableIDFromFlags's name path for real rather than
// stubbing the resolved id -- and it is the only test that reaches
// renderOptions.label()'s named branch and latestAlertPayload.TableName.
func TestLatestAlertTableNameResolves(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	var lookedUpName string
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		lookedUpName = r.URL.Query().Get("name")
		testutil.RespondJSON(w, http.StatusOK, api.DatasetsListSchema{
			Results: []api.DataSetTinySchema{{ID: 42, Name: "orders"}},
		})
	})

	var gotDatasetID string
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/menu-stats", func(w http.ResponseWriter, r *http.Request) {
		gotDatasetID = r.URL.Query().Get("dataset_id")
		testutil.RespondJSON(w, http.StatusOK, menuStatsWithAlert())
	})

	cmd := setupLatestAlertTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"get", "latest-alert", "--table", "orders"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if lookedUpName != "orders" {
		t.Errorf("--table orders must query name=orders, queried %q", lookedUpName)
	}
	if gotDatasetID != "42" {
		t.Errorf("the resolved id must reach the menu-stats request: dataset_id = %q, want \"42\"", gotDatasetID)
	}
	out := buf.String()
	if !strings.Contains(out, "orders (id: 42)") {
		t.Errorf("output must carry the named label (\"orders (id: 42)\"), not the --table-id fallback\ngot:\n%s", out)
	}
}

func TestLatestAlertEmptyStateExitsZero(t *testing.T) {
	empty := api.MenuStatsSchema{UnresolvedAlertsCount: 0}
	out, err := runLatestAlert(t, empty, "--table-id", "42")
	if err != nil {
		t.Fatalf("an empty state is an answer, not an error: %v", err)
	}
	if !strings.Contains(out, "none") {
		t.Errorf("output should say there is no alert\ngot:\n%s", out)
	}
	if strings.Contains(out, "describe alert") {
		t.Errorf("no alert means no next-action line\ngot:\n%s", out)
	}
}

func TestLatestAlertNameOutputIsAnAlertRef(t *testing.T) {
	out, err := runLatestAlert(t, menuStatsWithAlert(), "--table-id", "42", "-o", "name")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if strings.TrimSpace(out) != "alert/4821" {
		t.Errorf("-o name = %q, want %q", strings.TrimSpace(out), "alert/4821")
	}
}

func TestLatestAlertJSONOmitsNextActionLine(t *testing.T) {
	out, err := runLatestAlert(t, menuStatsWithAlert(), "--table-id", "42", "-o", "json")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("output is not valid JSON: %v\ngot:\n%s", err, out)
	}
	if payload["latest_alert_item_id"] != float64(4821) {
		t.Errorf("latest_alert_item_id = %v, want 4821", payload["latest_alert_item_id"])
	}
	if payload["table_id"] != float64(42) {
		t.Errorf("table_id = %v, want 42", payload["table_id"])
	}
	if strings.Contains(out, "qctl describe alert") {
		t.Error("the next-action line is a text-view affordance, not payload")
	}
}

func TestLatestAlertRequiresTableSelector(t *testing.T) {
	_, err := runLatestAlert(t, menuStatsWithAlert())
	if err == nil {
		t.Fatal("want an error with no table selector")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
}

func TestLatestAlertExitOnChangeRequiresWatch(t *testing.T) {
	_, err := runLatestAlert(t, menuStatsWithAlert(), "--table-id", "42", "--exit-on-change")
	if err == nil {
		t.Fatal("want an error for --exit-on-change without --watch")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
}

func TestLatestAlertRejectsSubSecondInterval(t *testing.T) {
	_, err := runLatestAlert(t, menuStatsWithAlert(), "--table-id", "42", "--watch", "--watch-interval", "0")
	if err == nil {
		t.Fatal("want an error for --watch-interval 0")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
}

// The interval rule lives in cmdutil now; latest-alert must report it with the
// same wording as every other watch command, value included.
func TestLatestAlertIntervalErrorNamesFlagAndValue(t *testing.T) {
	_, err := runLatestAlert(t, menuStatsWithAlert(), "--table-id", "42", "--watch", "--watch-interval", "-3")
	if err == nil {
		t.Fatal("want an error for --watch-interval -3")
	}
	if !strings.Contains(err.Error(), "--watch-interval must be at least 1 second, got -3") {
		t.Errorf("error should name the flag, the rule and the value, got: %v", err)
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
}
