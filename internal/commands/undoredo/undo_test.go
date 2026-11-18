package undoredo

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/datasets"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const testOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

func setupTestCommand() *cobra.Command {
	rootCmd := &cobra.Command{Use: "qctl"}
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format")
	rootCmd.PersistentFlags().Bool("no-headers", false, "")
	rootCmd.PersistentFlags().String("columns", "", "")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "")
	rootCmd.PersistentFlags().CountP("verbose", "v", "")
	rootCmd.AddCommand(NewUndoCommand())
	rootCmd.AddCommand(NewRedoCommand())
	return rootCmd
}

type cmdEnv struct {
	env    *testutil.TestEnv
	mock   *testutil.MockAPIServer
	bodies map[string]map[string]interface{}
	code   map[string]int
	raw    map[string]string
}

func newCmdEnv(t *testing.T) *cmdEnv {
	t.Helper()
	e, mock := newBareEnv(t)
	e.env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	e.env.SetupCredential(endpointKey, testOrgID, "test-token")
	return e
}

// newBareEnv builds the mock server and an isolated HOME but writes NO config
// and NO credential, so cmdutil.Bootstrap fails on the missing context with a
// generic error (exit 1) rather than succeeding.
//
// That asymmetry is the whole point: it is what lets a test distinguish
// "ParseIntArgs ran first" (exit 2) from "Bootstrap ran first" (exit 1). A
// request-count assertion cannot -- Bootstrap issues no HTTP request either,
// so len(Requests()) == 0 holds on BOTH orderings and the guard is vacuous.
func newBareEnv(t *testing.T) (*cmdEnv, *testutil.MockAPIServer) {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	// A developer shell that exports either of these would let Bootstrap get
	// further than the missing config, blunting the exit-code distinction.
	t.Setenv("QCTL_SERVER", "")
	t.Setenv("QCTL_ORG", "")

	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)

	e := &cmdEnv{env: env, mock: mock, bodies: map[string]map[string]interface{}{},
		code: map[string]int{}, raw: map[string]string{}}

	for _, route := range []string{"undo-step", "redo-step", "undo", "redo"} {
		route := route
		mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/"+route, func(w http.ResponseWriter, r *http.Request) {
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			e.bodies[route] = body
			if code := e.code[route]; code != 0 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(code)
				_, _ = w.Write([]byte(e.raw[route]))
				return
			}
			if strings.HasSuffix(route, "-step") {
				testutil.RespondJSON(w, http.StatusOK, api.UndoRedoStepResponseSchema{
					HistoryItemsIds: []int{12}, Msg: "**max** changed from **10** to **20**.",
				})
				return
			}
			refresh := false
			testutil.RespondJSON(w, http.StatusOK, api.UndoRedoResponseSchema{ShouldRefresh: &refresh})
		})
	}
	// Name resolution for `undo table orders`.
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, datasets.DatasetsPage{
			Results: []datasets.DatasetTiny{{ID: 7, Name: "orders"}},
		})
	})

	return e, mock
}

func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := setupTestCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestUndoTableResolvesTheNameAndSendsDataView(t *testing.T) {
	env := newCmdEnv(t)

	out, err := run(t, "undo", "table", "orders", "--yes")
	if err != nil {
		t.Fatalf("undo table: %v\n%s", err, out)
	}
	if got := env.bodies["undo-step"]["page_type"]; got != "DataView" {
		t.Errorf("page_type = %v, want DataView", got)
	}
	if got := env.bodies["undo-step"]["dataset_id"]; got != float64(7) {
		t.Errorf("dataset_id = %v, want 7", got)
	}
	if !strings.Contains(out, "table orders — undo applied") {
		t.Errorf("outcome line missing:\n%s", out)
	}
}

// The step message of a percent-cell edit, as a production server sends it:
// the operands are in the column's display format, each inside a bold run with
// curly quotes, and the table is a link to a frontend route. The terminal gets
// the values and the quotes, and none of the Markdown.
func TestUndoTablePreviewPrintsTheDisplayFormattedValuesWithoutMarkdown(t *testing.T) {
	env := newCmdEnv(t)
	msg := "**“12.50%”** changed to **“13.00%”** in the **rate** field of " +
		"[orders data](/orgs/abc/datasets/7?tab=clean_data)."
	body, err := json.Marshal(api.UndoRedoStepResponseSchema{HistoryItemsIds: []int{12}, Msg: msg})
	if err != nil {
		t.Fatal(err)
	}
	env.code["undo-step"] = http.StatusOK
	env.raw["undo-step"] = string(body)

	out, err := run(t, "undo", "table", "orders", "--yes")
	if err != nil {
		t.Fatalf("exit %d: %v\n%s", qerrors.GetExitCode(err), err, out)
	}
	want := "“12.50%” changed to “13.00%” in the rate field of orders data.\n" +
		"table orders — undo applied\n"
	if out != want {
		t.Errorf("output:\n got %q\nwant %q", out, want)
	}
}

func TestUndoAndRedoPrintTheCellsLeftUntouched(t *testing.T) {
	for _, dir := range []string{"undo", "redo"} {
		t.Run(dir, func(t *testing.T) {
			env := newCmdEnv(t)
			env.code[dir] = http.StatusOK
			env.raw[dir] = `{"should_refresh":true,"msg":"**2 cells** changed since, so we left them as they are."}`

			out, err := run(t, dir, "table", "orders", "--yes")
			if err != nil {
				t.Fatalf("%s: %v\n%s", dir, err, out)
			}
			want := "max changed from 10 to 20.\ntable orders — " + dir + " applied\n" +
				"2 cells changed since, so we left them as they are.\n"
			if out != want {
				t.Errorf("output = %q, want %q", out, want)
			}
		})
	}
}

func TestEachSubcommandSendsItsOwnPageTypeAndIdField(t *testing.T) {
	for _, tc := range []struct {
		args     []string
		route    string
		pageType string
		idField  string
	}{
		{[]string{"undo", "cloud-source", "4"}, "undo-step", "DataSourceModelEdit", "data_source_model_id"},
		{[]string{"undo", "destination", "9"}, "undo-step", "DestinationEdit", "destination_id"},
		{[]string{"redo", "table", "orders"}, "redo-step", "DataView", "dataset_id"},
		{[]string{"redo", "cloud-source", "4"}, "redo-step", "DataSourceModelEdit", "data_source_model_id"},
		{[]string{"redo", "destination", "9"}, "redo-step", "DestinationEdit", "destination_id"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			env := newCmdEnv(t)
			if out, err := run(t, append(tc.args, "--yes")...); err != nil {
				t.Fatalf("%v: %v\n%s", tc.args, err, out)
			}
			body := env.bodies[tc.route]
			if body["page_type"] != tc.pageType {
				t.Errorf("page_type = %v, want %q", body["page_type"], tc.pageType)
			}
			if v, ok := body[tc.idField]; !ok || v == nil {
				t.Errorf("%s is missing from %v", tc.idField, body)
			}
		})
	}
}

// --yes skips the prompt but must NOT skip the step call: the ids it returns
// are what makes the concurrency refusal possible.
func TestYesStillIssuesTheStepCallFirst(t *testing.T) {
	env := newCmdEnv(t)
	if out, err := run(t, "undo", "table", "orders", "--yes"); err != nil {
		t.Fatalf("undo: %v\n%s", err, out)
	}
	seq := env.mock.Requests()
	stepAt, actAt := -1, -1
	for i, r := range seq {
		switch r {
		case "POST /api/orgs/" + testOrgID + "/undo-step":
			stepAt = i
		case "POST /api/orgs/" + testOrgID + "/undo":
			actAt = i
		}
	}
	if stepAt < 0 || actAt < 0 || stepAt > actAt {
		t.Errorf("request sequence = %v; the step call must run and precede the act call", seq)
	}
}

func TestNoTTYWithoutYesExitsTwoAndNeverActs(t *testing.T) {
	env := newCmdEnv(t)
	out, err := run(t, "undo", "table", "orders")
	if err == nil {
		t.Fatalf("expected a non-interactive failure\n%s", out)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
	stepped := false
	for _, r := range env.mock.Requests() {
		if strings.HasSuffix(r, "/undo") {
			t.Errorf("the act call ran without a confirmation: %v", env.mock.Requests())
		}
		if strings.HasSuffix(r, "/undo-step") {
			stepped = true
		}
	}
	// Without this, a build that failed before it ever reached the API would
	// satisfy the "never acted" half of this test for the wrong reason.
	if !stepped {
		t.Errorf("the step call never ran, so 'never acts' proves nothing: %v", env.mock.Requests())
	}
}

func TestDecliningExitsOne(t *testing.T) {
	newCmdEnv(t)
	cmdutilSetReader(t, "n\n")
	out, err := run(t, "undo", "table", "orders")
	if err == nil {
		t.Fatalf("expected the decline to fail the command\n%s", out)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitGenericError {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitGenericError)
	}
}

func TestNothingLeftToUndoExitsThree(t *testing.T) {
	env := newCmdEnv(t)
	env.code["undo-step"] = http.StatusNotFound
	env.raw["undo-step"] = `{"detail":{"msg":"There is nothing left to undo.","title":"Info"}}`

	out, err := run(t, "undo", "table", "orders", "--yes")
	if err == nil {
		t.Fatalf("expected a 404\n%s", out)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitNotFound {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitNotFound)
	}
}

func TestForbiddenExitsFour(t *testing.T) {
	env := newCmdEnv(t)
	env.code["undo-step"] = http.StatusForbidden
	env.raw["undo-step"] = `{"detail":{"msg":"You do not have permission to undo/redo this type of change."}}`

	out, err := run(t, "undo", "cloud-source", "4", "--yes")
	if err == nil {
		t.Fatalf("expected a 403\n%s", out)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitUnauthorized {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitUnauthorized)
	}
}

// The two numeric leaves call cmdutil.ParseIntArgs BEFORE cmdutil.Bootstrap, so
// a typo'd id costs no config load, no keyring read and no network.
//
// The env here is deliberately UNCONFIGURED, and that is what makes this test
// bite. Bootstrap issues no HTTP request of its own, so a request-count
// assertion passes on both orderings and proves nothing; but with no context to
// load, a Bootstrap-first build fails inside config.ResolveServer with a plain
// error -- exit 1 -- instead of ParseIntArgs' exit 2. Swap the two calls in
// cloud_source.go or destination.go and this test fails on the exit code.
func TestNonNumericIdExitsTwoBeforeBootstrap(t *testing.T) {
	for _, args := range [][]string{
		{"undo", "destination", "abc", "--yes"},
		{"undo", "cloud-source", "abc", "--yes"},
		{"redo", "destination", "abc", "--yes"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			e, _ := newBareEnv(t)
			out, err := run(t, args...)
			if err == nil {
				t.Fatalf("expected a usage error\n%s", out)
			}
			// Exit 2 here can only come from ParseIntArgs: with no config
			// written, Bootstrap's own failure is exit 1.
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d (%d means Bootstrap ran before ParseIntArgs)",
					got, qerrors.ExitBadUsage, qerrors.ExitGenericError)
			}
			if len(e.mock.Requests()) != 0 {
				t.Errorf("a client-side validation failure spent requests: %v", e.mock.Requests())
			}
		})
	}
}

// The companion to the above: with a configured env the same typo must still
// exit 2 rather than reaching the API.
func TestNonNumericIdExitsTwoWhenConfigured(t *testing.T) {
	env := newCmdEnv(t)
	out, err := run(t, "undo", "destination", "abc", "--yes")
	if err == nil {
		t.Fatalf("expected a usage error\n%s", out)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
	if len(env.mock.Requests()) != 0 {
		t.Errorf("a client-side validation failure spent requests: %v", env.mock.Requests())
	}
}

func TestJsonOutputIsOneDocumentAndTheOutcomeLineIsGone(t *testing.T) {
	newCmdEnv(t)
	out, err := run(t, "undo", "table", "orders", "--yes", "-o", "json")
	if err != nil {
		t.Fatalf("undo -o json: %v\n%s", err, out)
	}
	// cmd.SetOut and SetErr share one buffer here, so trim the preview line
	// before parsing: the assertion is that stdout carries exactly one document.
	brace := strings.Index(out, "{")
	if brace < 0 {
		t.Fatalf("no JSON document in the output at all:\n%s", out)
	}
	body := out[brace:]
	var doc map[string]interface{}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("not a single JSON document: %v\n%s", err, out)
	}
	if _, ok := doc["step"]; !ok {
		t.Errorf("step half missing: %v", doc)
	}
	if _, ok := doc["result"]; !ok {
		t.Errorf("result half missing: %v", doc)
	}
	if strings.Contains(out, "undo applied") {
		t.Errorf("the human outcome line leaked into structured output:\n%s", out)
	}
}

// cmdutilSetReader feeds a canned answer to cmdutil's confirmation prompt and
// restores stdin afterwards. Overriding the reader is also what makes
// cmdutil.CanPrompt report true, which is how the "declined" path is reachable
// in a test at all.
func cmdutilSetReader(t *testing.T, answer string) {
	t.Helper()
	cmdutil.SetReader(strings.NewReader(answer))
	t.Cleanup(cmdutil.ResetReader)
}
