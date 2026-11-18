package describe

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/qlustered/qctl/internal/warnings"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func setupWarningDescribeTestCommand() *cobra.Command {
	rootCmd := &cobra.Command{Use: "qctl"}
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format (table|json|yaml|name)")
	rootCmd.PersistentFlags().Bool("no-headers", false, "Don't print column headers")
	rootCmd.PersistentFlags().String("columns", "", "Comma-separated list of columns to display")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "Maximum width for table columns")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "Show secret fields in plaintext")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "Allow non-localhost http://")
	rootCmd.PersistentFlags().CountP("verbose", "v", "Verbosity level")

	describeCmd := &cobra.Command{Use: "describe", Short: "Show details of a resource"}
	describeCmd.AddCommand(NewWarningCommand())
	rootCmd.AddCommand(describeCmd)
	return rootCmd
}

func describeWarningFixture(resolved bool, actions ...api.WarningAction) warnings.WarningFull {
	fieldName := "amount"
	list := append([]api.WarningAction{}, actions...)
	return warnings.WarningFull{
		ID:                 55,
		DatasetID:          42,
		DatasetName:        "orders",
		SettingsModelID:    100,
		IssueType:          api.Anomaly,
		Msg:                "Value looks anomalous",
		Count:              1,
		Resolved:           &resolved,
		FieldName:          &fieldName,
		WarningActionsList: &list,
	}
}

func runDescribeWarning(t *testing.T, warning warnings.WarningFull, args ...string) string {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/warnings/55",
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, warning)
		})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/warnings/55/stats", respondAffectingRows(1275))

	cmd := setupWarningDescribeTestCommand()
	cmd.SetArgs(args)
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v\n%s", err, buf.String())
	}
	return buf.String()
}

// runDescribeWarningWithStats runs `describe warning 55` against a mock
// returning warning, with stats answering its stats route, and keeps the two
// streams apart.
func runDescribeWarningWithStats(t *testing.T, warning warnings.WarningFull, stats http.HandlerFunc, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/warnings/55",
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, warning)
		})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/warnings/55/stats", stats)

	cmd := setupWarningDescribeTestCommand()
	cmd.SetArgs(append([]string{"describe", "warning", "55"}, args...))
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

// The warning's exact row count moved off the warning onto its stats route;
// describe warning puts it back where the raw document carried it, a top-level
// affecting_rows_count, on all three views.
func TestDescribeWarningShowsTheStatsRowCount(t *testing.T) {
	w := describeWarningFixture(false)
	w.Count = 1000
	capped := true
	w.CountCapped = &capped

	out, stderr, err := runDescribeWarningWithStats(t, w, respondAffectingRows(1275))
	if err != nil {
		t.Fatalf("Execute() error = %v\n%s", err, stderr)
	}
	var doc struct {
		AffectingRowsCount *int `yaml:"affecting_rows_count"`
		Count              int  `yaml:"count"`
		CountCapped        bool `yaml:"count_capped"`
	}
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("output did not parse as YAML: %v\n%s", err, out)
	}
	if doc.AffectingRowsCount == nil || *doc.AffectingRowsCount != 1275 || doc.Count != 1000 || !doc.CountCapped {
		t.Errorf("affecting_rows_count / count / count_capped = %v / %d / %t, want 1275 / 1000 / true:\n%s",
			doc.AffectingRowsCount, doc.Count, doc.CountCapped, out)
	}
	if stderr != "" {
		t.Errorf("a successful stats read should warn nothing, got stderr:\n%s", stderr)
	}

	out, _, err = runDescribeWarningWithStats(t, w, respondAffectingRows(1275), "-o", "json")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(out, `"affecting_rows_count": 1275`) || !strings.Contains(out, `"count_capped": true`) {
		t.Errorf("-o json should carry affecting_rows_count and count_capped, got:\n%s", out)
	}

	out, _, err = runDescribeWarningWithStats(t, w, respondAffectingRows(1275), "-o", "table", "--columns", "id,count,affecting_rows_count")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(out, "AFFECTING-ROWS-COUNT") || !strings.Contains(out, "1275") {
		t.Errorf("-o table should carry an affecting_rows_count column, got:\n%s", out)
	}
}

// The stats read is best-effort: a failure costs the field and one stderr
// line, never the describe. The table view keeps the column, empty, so a
// --columns list that names it stays valid.
func TestDescribeWarningSurvivesAFailedStatsRead(t *testing.T) {
	for name, stats := range map[string]http.HandlerFunc{
		"500": func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondError(w, http.StatusInternalServerError, "boom")
		},
		"404": func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondError(w, http.StatusNotFound, "Warning 55 can not be found.")
		},
	} {
		for _, args := range [][]string{nil, {"-o", "json"}, {"-o", "table", "--columns", "id,affecting_rows_count"}} {
			t.Run(fmt.Sprintf("%s %v", name, args), func(t *testing.T) {
				out, stderr, err := runDescribeWarningWithStats(t, describeWarningFixture(false), stats, args...)
				if code := qerrors.GetExitCode(err); code != qerrors.ExitSuccess {
					t.Fatalf("exit code = %d, want 0 (err %v)", code, err)
				}
				if !strings.Contains(out, "55") {
					t.Errorf("the describe itself is missing:\n%s", out)
				}
				if strings.Contains(out, "affecting_rows_count") {
					t.Errorf("a failed stats read still printed a row count key:\n%s", out)
				}
				// The table header is AFFECTING-ROWS-COUNT, which the key check
				// above cannot see, so the row itself must hold only the id.
				if slices.Contains(args, "table") {
					lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
					if len(lines) < 2 ||
						!slices.Equal(strings.Fields(lines[0]), []string{"ID", "AFFECTING-ROWS-COUNT"}) ||
						!slices.Equal(strings.Fields(lines[1]), []string{"55"}) {
						t.Errorf("want the AFFECTING-ROWS-COUNT column kept and empty, got:\n%s", out)
					}
				}
				assertOneLineStatsWarning(t, stderr)
			})
		}
	}
}

func TestDescribeWarningRendersTheFooter(t *testing.T) {
	out := runDescribeWarning(t,
		describeWarningFixture(false, api.NotAnomaly, api.IgnoreColumnForFutureAnomalies),
		"describe", "warning", "55")

	for _, want := range []string{
		"# NEXT ACTIONS",
		"qctl warning resolve 55 --action not_anomaly",
		"qctl warning resolve 55 --action ignore_column_for_future_anomalies",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output should contain %q, got:\n%s", want, out)
		}
	}

	// The footer is comments, so the document must still parse as YAML.
	var parsed map[string]interface{}
	if err := yaml.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("footer broke YAML parsing: %v\n%s", err, out)
	}
	if parsed["id"] != 55 {
		t.Errorf("id = %v, want 55", parsed["id"])
	}
	// Parsing is not enough: a footer line that stopped being a comment while
	// staying key:value-shaped would parse fine and inject a phantom key. The
	// warning's own fields are all plain snake_case, so any key carrying footer
	// prose came from the footer.
	for key := range parsed {
		if strings.ContainsAny(key, " #") {
			t.Errorf("footer leaked a phantom YAML key %q, so it is no longer all comments:\n%s", key, out)
		}
	}
}

// -o json is the ONE view with no footer: it has no comment syntax, and the
// raw response is the machine view. This test used to name `table` alongside
// it and so PINNED the defect qctl-cb1 fixed -- the table view's silence was
// asserted as correct, which is why changing it would have looked like a
// regression rather than a fix.
func TestDescribeWarningFooterIsWithheldFromJSONOnly(t *testing.T) {
	out := runDescribeWarning(t,
		describeWarningFixture(false, api.NotAnomaly),
		"describe", "warning", "55", "-o", "json")
	if strings.Contains(out, "NEXT ACTIONS") {
		t.Errorf("-o json must not carry the footer, got:\n%s", out)
	}
}

func TestDescribeWarningRendersDatasetRuleIDAsUUID(t *testing.T) {
	const want = "44444444-4444-4444-4444-444444444444"
	warning := describeWarningFixture(false)
	id := uuid.MustParse(want)
	warning.DatasetRuleID = &id

	tests := []struct {
		name   string
		args   []string
		decode func([]byte, interface{}) error
	}{
		{name: "json", args: []string{"describe", "warning", "55", "-o", "json"}, decode: json.Unmarshal},
		{name: "yaml", args: []string{"describe", "warning", "55"}, decode: yaml.Unmarshal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := runDescribeWarning(t, warning, tt.args...)
			var doc struct {
				DatasetRuleID string `json:"dataset_rule_id" yaml:"dataset_rule_id"`
			}
			if err := tt.decode([]byte(out), &doc); err != nil {
				t.Fatalf("output did not decode: %v\n%s", err, out)
			}
			if doc.DatasetRuleID != want {
				t.Errorf("dataset_rule_id = %q, want %q\n%s", doc.DatasetRuleID, want, out)
			}
		})
	}
	t.Run("table", func(t *testing.T) {
		out := runDescribeWarning(t, warning, "describe", "warning", "55", "-o", "table", "--columns", "dataset_rule_id")
		if !strings.Contains(out, want) {
			t.Errorf("table should print dataset_rule_id as %q, got:\n%s", want, out)
		}
	})
}

// The defect: `-o table` returned the printer's output and never reached the
// footer, so a reader on the table view saw none of the runnable commands the
// command had already computed.
func TestDescribeWarningTableCarriesTheFooter(t *testing.T) {
	out := runDescribeWarning(t,
		describeWarningFixture(false, api.NotAnomaly, api.IgnoreColumnForFutureAnomalies),
		"describe", "warning", "55", "-o", "table")

	for _, want := range []string{
		"NEXT ACTIONS",
		"qctl warning resolve 55 --action not_anomaly",
		"qctl warning resolve 55 --action ignore_column_for_future_anomalies",
		"BULK — permanently ignores the column",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("table output should contain %q, got:\n%s", want, out)
		}
	}
}

// ignore_column_for_future_anomalies prompts, so the footer's line for it
// carries --yes exactly when describe runs where no prompt can be answered --
// on both views that print the footer. not_anomaly does not prompt and never
// gets it.
func TestDescribeWarningFooterAddsYesOnlyWithoutATerminal(t *testing.T) {
	const ignore = "qctl warning resolve 55 --action ignore_column_for_future_anomalies"
	fixture := describeWarningFixture(false, api.NotAnomaly, api.IgnoreColumnForFutureAnomalies)
	for view, args := range map[string][]string{
		"yaml":  {"describe", "warning", "55"},
		"table": {"describe", "warning", "55", "-o", "table"},
	} {
		t.Run(view+"/at a terminal", func(t *testing.T) {
			cmdutil.SetReader(strings.NewReader(""))
			t.Cleanup(cmdutil.ResetReader)
			out := runDescribeWarning(t, fixture, args...)
			if !strings.Contains(out, ignore+"\n") {
				t.Errorf("a terminal reader's line must not carry --yes, want %q in:\n%s", ignore+"\n", out)
			}
		})
		t.Run(view+"/no terminal", func(t *testing.T) {
			cmdutil.ResetReader()
			t.Cleanup(cmdutil.ResetReader)
			if cmdutil.CanPrompt() {
				t.Skip("stdin is a terminal; CanPrompt() cannot be made false here")
			}
			out := runDescribeWarning(t, fixture, args...)
			if !strings.Contains(out, ignore+" --yes\n") {
				t.Errorf("want %q in:\n%s", ignore+" --yes\n", out)
			}
			if !strings.Contains(out, "qctl warning resolve 55 --action not_anomaly\n") {
				t.Errorf("not_anomaly does not prompt and must not carry --yes:\n%s", out)
			}
		})
	}
}

// A table is not a YAML document, so the marker that makes the footer legal
// there is noise here. Asserting its absence is what stops the plain form
// being quietly swapped back for the commented one.
func TestDescribeWarningTableFooterIsNotCommented(t *testing.T) {
	out := runDescribeWarning(t,
		describeWarningFixture(false, api.NotAnomaly),
		"describe", "warning", "55", "-o", "table")

	if !strings.Contains(out, "NEXT ACTIONS") {
		t.Fatalf("no footer to check, got:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			t.Errorf("table output carries a YAML comment marker: %q\n%s", line, out)
		}
	}
}

// The table path appends the block unconditionally, so an empty case leaking a
// bare heading would print one under every warning.
func TestDescribeWarningTableHasNoFooterWithoutActions(t *testing.T) {
	for name, w := range map[string]warnings.WarningFull{
		"no actions": describeWarningFixture(false),
		"resolved":   describeWarningFixture(true, api.NotAnomaly),
	} {
		t.Run(name, func(t *testing.T) {
			out := runDescribeWarning(t, w, "describe", "warning", "55", "-o", "table")
			if strings.Contains(out, "NEXT ACTIONS") {
				t.Errorf("want no footer, got:\n%s", out)
			}
		})
	}
}

func TestDescribeWarningNoFooterWhenResolved(t *testing.T) {
	out := runDescribeWarning(t,
		describeWarningFixture(true, api.NotAnomaly),
		"describe", "warning", "55")
	if strings.Contains(out, "NEXT ACTIONS") {
		t.Errorf("a resolved warning gets no footer, got:\n%s", out)
	}
}

func TestDescribeWarningNoFooterWithoutActions(t *testing.T) {
	out := runDescribeWarning(t, describeWarningFixture(false), "describe", "warning", "55")
	if strings.Contains(out, "NEXT ACTIONS") {
		t.Errorf("a warning with no actions gets no footer, got:\n%s", out)
	}
}

// An offered action --action cannot drive is still listed, annotated.
func TestDescribeWarningFooterMarksParameterisedActions(t *testing.T) {
	out := runDescribeWarning(t,
		describeWarningFixture(false, api.NotAnomaly, api.MapValue),
		"describe", "warning", "55")
	if !strings.Contains(out, "needs extra input (new_value)") {
		t.Errorf("map_value must be marked as needing input, got:\n%s", out)
	}
}

// A WARNING carrying declared_severity was DEMOTED: the rule declared
// `blocker` and the instance's `severity: warning` policy overrode it. It
// reaches this command because `WarningSchema.body` is typed `AlertBodySchema
// | None` (atlas capi/capi/schemas/warning_schema.py) and the backend
// writes the key BEFORE it branches on `is_blocker`
// (cettings/.../rule_checker.py get_issue_exception_from_issue_context:
// `if issue_context.is_promoted:`, then `if issue_context.is_blocker:`).
func describeWarningWithDeclaredSeverity() warnings.WarningFull {
	sev := api.IssueSeverityBlocker
	w := describeWarningFixture(false, api.NotAnomaly)
	w.Body = &api.AlertBodySchema{DeclaredSeverity: &sev}
	return w
}

// This command renders the RAW response, so the value already reached the
// reader -- as one line among two dozen null siblings, with nothing saying it
// is the rule's value rather than the warning's. Both halves are asserted: the
// key must stay in the document, and the note must explain it.
func TestDescribeWarningSurfacesDeclaredSeverity(t *testing.T) {
	out := runDescribeWarning(t, describeWarningWithDeclaredSeverity(), "describe", "warning", "55")

	var doc struct {
		Body struct {
			DeclaredSeverity string `yaml:"declared_severity"`
		} `yaml:"body"`
	}
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("output did not parse as YAML: %v\n%s", err, out)
	}
	if doc.Body.DeclaredSeverity != "blocker" {
		t.Errorf("body.declared_severity = %q, want blocker:\n%s", doc.Body.DeclaredSeverity, out)
	}
	for _, want := range []string{
		"DECLARED SEVERITY",
		"the rule marked this issue blocker",
		"(warning)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output should contain %q, got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "(alert)") {
		t.Errorf("a warning's note must not name the alert class:\n%s", out)
	}
}

// The table view takes the plain form, and -o json stays the one view with no
// footer -- the same split the NEXT ACTIONS block already keeps.
func TestDescribeWarningDeclaredSeverityFollowsTheFooterSplit(t *testing.T) {
	table := runDescribeWarning(t, describeWarningWithDeclaredSeverity(), "describe", "warning", "55", "-o", "table")
	if !strings.Contains(table, "DECLARED SEVERITY") {
		t.Errorf("table view lost the note:\n%s", table)
	}
	if strings.Contains(table, "# DECLARED SEVERITY") {
		t.Errorf("table view must use the plain form, not YAML comments:\n%s", table)
	}

	asJSON := runDescribeWarning(t, describeWarningWithDeclaredSeverity(), "describe", "warning", "55", "-o", "json")
	if strings.Contains(asJSON, "DECLARED SEVERITY") {
		t.Errorf("-o json must carry no footer prose, got:\n%s", asJSON)
	}
	if !strings.Contains(asJSON, `"declared_severity": "blocker"`) {
		t.Errorf("-o json must still carry the value, got:\n%s", asJSON)
	}
}

// The negative control: without it the assertions above would pass against a
// note printed on every warning.
func TestDescribeWarningSaysNothingWhenDeclaredSeverityIsAbsent(t *testing.T) {
	out := runDescribeWarning(t, describeWarningFixture(false, api.NotAnomaly), "describe", "warning", "55")
	if strings.Contains(out, "DECLARED SEVERITY") {
		t.Errorf("output should carry no note, got:\n%s", out)
	}
}

// The two halves of the footer withhold themselves independently, exactly as
// on the alert side: NEXT ACTIONS is silent on a resolved warning, while the
// note keeps printing because the rule's severity was still overridden. The
// two tests above that assert "no footer" for a resolved or actionless warning
// name NEXT ACTIONS specifically, and this is why they have to.
func TestDescribeWarningNotesDeclaredSeverityOnAResolvedWarning(t *testing.T) {
	w := describeWarningWithDeclaredSeverity()
	resolved := true
	w.Resolved = &resolved

	out := runDescribeWarning(t, w, "describe", "warning", "55")
	if !strings.Contains(out, "DECLARED SEVERITY") {
		t.Errorf("a resolved warning lost the note:\n%s", out)
	}
	if strings.Contains(out, "NEXT ACTIONS") {
		t.Errorf("a resolved warning must still offer no actions:\n%s", out)
	}
}

// describe warning decodes a marked message in its YAML, -o json and -o table.
func TestDescribeWarningRendersAMarkedMessage(t *testing.T) {
	warning := describeWarningFixture(false)
	warning.Msg = `~|"~{~*Hyundai Sonata Hybrid Limited*~}~"|~ looks anomalous in **model**`
	const rendered = `"Hyundai Sonata Hybrid Limited" looks anomalous in model`
	for _, args := range [][]string{{}, {"-o", "json"}, {"-o", "table", "--columns", "id,msg"}} {
		out := runDescribeWarning(t, warning, append([]string{"describe", "warning", "55"}, args...)...)
		out = strings.ReplaceAll(out, `\"`, `"`) // -o json escapes the quotes
		if !strings.Contains(out, rendered) || strings.Contains(out, "~*") || strings.Contains(out, "~|") {
			t.Errorf("describe warning %v: want the message rendered, got:\n%s", args, out)
		}
	}
}

// A cell in body.rows prints as the integer the server sent: a 7-digit one in
// YAML, and one past 2^53 unrounded in JSON.
func TestDescribeWarningKeepsIntegerCells(t *testing.T) {
	var small, big api.AlertBodySchema_Rows_Item
	if err := small.UnmarshalJSON([]byte(`1234567`)); err != nil {
		t.Fatal(err)
	}
	if err := big.UnmarshalJSON([]byte(`9007199254740993`)); err != nil {
		t.Fatal(err)
	}
	w := describeWarningFixture(false)
	w.Body = &api.AlertBodySchema{Rows: &[][]api.AlertBodySchema_Rows_Item{{small, big}}}

	out := runDescribeWarning(t, w, "describe", "warning", "55")
	if !strings.Contains(out, "- - 1234567\n") {
		t.Errorf("YAML should print the cell as 1234567, got:\n%s", out)
	}
	out = runDescribeWarning(t, w, "describe", "warning", "55", "-o", "json")
	if !strings.Contains(out, "9007199254740993") {
		t.Errorf("JSON should print the cell as 9007199254740993, got:\n%s", out)
	}
}

// describe warning prints the API object, so a resolver's role reaches every
// structured view when the backend sends one, and no role key appears when it
// does not.
func TestDescribeWarningCarriesTheResolverRole(t *testing.T) {
	withRole := func(role *api.RoleEnum) warnings.WarningFull {
		w := describeWarningFixture(true)
		w.Whodunit = &api.UserInfoTinySchema{
			ID:        uuid.MustParse("00000000-0000-4000-8000-000000000002"),
			Email:     "ada@x.io",
			FirstName: "Ada",
			LastName:  "Lovelace",
			Role:      role,
		}
		return w
	}
	external := api.RoleEnumExternal

	yamlOut := runDescribeWarning(t, withRole(&external), "describe", "warning", "55", "-o", "yaml")
	var doc struct {
		Whodunit struct {
			Role string `yaml:"role"`
		} `yaml:"whodunit"`
	}
	if err := yaml.Unmarshal([]byte(yamlOut), &doc); err != nil {
		t.Fatalf("yaml: %v\n%s", err, yamlOut)
	}
	if doc.Whodunit.Role != "external" {
		t.Errorf("whodunit.role = %q, want external:\n%s", doc.Whodunit.Role, yamlOut)
	}
	if jsonOut := runDescribeWarning(t, withRole(&external), "describe", "warning", "55", "-o", "json"); !strings.Contains(jsonOut, `"role": "external"`) {
		t.Errorf("json lacks the role:\n%s", jsonOut)
	}

	for _, format := range []string{"yaml", "json"} {
		if out := runDescribeWarning(t, withRole(nil), "describe", "warning", "55", "-o", format); strings.Contains(out, "role") {
			t.Errorf("-o %s rendered a role key for a nil role:\n%s", format, out)
		}
	}
}
