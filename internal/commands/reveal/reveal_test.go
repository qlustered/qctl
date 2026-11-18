package reveal

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const testOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

const (
	revealPath  = "/api/orgs/" + testOrgID + "/datasets/42/pii-reveal"
	alertPath   = "/api/orgs/" + testOrgID + "/alerts/5"
	warningPath = "/api/orgs/" + testOrgID + "/warnings/6"
)

// setupTestCommand wires what root.go wires for the exit codes asserted here:
// arity and flag errors, a missing required flag and an unsupported -o value
// all exit 2, and kind/ prefixes are stripped from positionals.
func setupTestCommand() *cobra.Command {
	rootCmd := &cobra.Command{Use: "qctl", SilenceUsage: true, SilenceErrors: true}
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "Allow http://")
	rootCmd.PersistentFlags().CountP("verbose", "v", "Verbosity level")
	rootCmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return qerrors.Wrap(qerrors.ExitBadUsage, err, "")
	})
	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		if err := cmdutil.ValidateOutputFormat(cmd); err != nil {
			return err
		}
		if err := cmd.ValidateRequiredFlags(); err != nil {
			return qerrors.Wrap(qerrors.ExitBadUsage, err, "")
		}
		return nil
	}
	rootCmd.AddCommand(NewCommand())
	cmdutil.TypeArgsErrors(rootCmd)
	cmdutil.StripKindRefs(rootCmd)
	return rootCmd
}

func newTestEnv(t *testing.T) *testutil.MockAPIServer {
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

func run(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := setupTestCommand()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(append([]string{"reveal"}, args...))
	err := cmd.Execute()
	return out.String(), errOut.String(), err
}

func assertExitCode(t *testing.T, err error, want int) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error exiting %d, got nil", want)
	}
	if got := qerrors.GetExitCode(err); got != want {
		t.Errorf("exit code = %d, want %d (err: %v)", got, want, err)
	}
}

func assertRequests(t *testing.T, mock *testutil.MockAPIServer, want ...string) {
	t.Helper()
	got := mock.Requests()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %q, want %q", got, want)
	}
}

// serve answers method+path with code, body and header, and returns the
// request bodies it received.
func serve(mock *testutil.MockAPIServer, method, path string, code int, body string, header map[string]string) *[]map[string]any {
	var bodies []map[string]any
	mock.RegisterHandler(method, path, func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			raw, _ := io.ReadAll(r.Body)
			var decoded map[string]any
			_ = json.Unmarshal(raw, &decoded)
			bodies = append(bodies, decoded)
		}
		for k, v := range header {
			w.Header().Set(k, v)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = io.WriteString(w, body)
	})
	return &bodies
}

func assertBody(t *testing.T, bodies *[]map[string]any, want map[string]any) {
	t.Helper()
	if len(*bodies) != 1 {
		t.Fatalf("reveal requests = %d, want 1", len(*bodies))
	}
	if !reflect.DeepEqual((*bodies)[0], want) {
		t.Errorf("reveal body = %v, want %v", (*bodies)[0], want)
	}
}

func TestCellPrintsTheValueAloneOnStdout(t *testing.T) {
	mock := newTestEnv(t)
	bodies := serve(mock, http.MethodPost, revealPath, http.StatusOK, `{"value":"alice@example.com","event_id":91}`, nil)

	out, errOut, err := run(t, "cell", "--table-id", "42", "--row-id", "1042", "--field", "email")
	if err != nil {
		t.Fatalf("reveal cell: %v", err)
	}
	if out != "alice@example.com\n" {
		t.Errorf("stdout = %q, want the value alone", out)
	}
	if errOut != "Reveal recorded as event 91\n" {
		t.Errorf("stderr = %q", errOut)
	}
	assertBody(t, bodies, map[string]any{"kind": "cell", "row_id": float64(1042), "field_name": "email", "is_bad_data": false})
	assertRequests(t, mock, "POST "+revealPath)
}

func TestCellIsBadDataTargetsQuarantinedRows(t *testing.T) {
	mock := newTestEnv(t)
	bodies := serve(mock, http.MethodPost, revealPath, http.StatusOK, `{"value":"x","event_id":1}`, nil)

	if _, _, err := run(t, "cell", "--table-id", "42", "--row-id", "7", "--field", "phone", "--is-bad-data"); err != nil {
		t.Fatalf("reveal cell: %v", err)
	}
	assertBody(t, bodies, map[string]any{"kind": "cell", "row_id": float64(7), "field_name": "phone", "is_bad_data": true})
}

func TestCellNullPrintsNothingOnStdout(t *testing.T) {
	mock := newTestEnv(t)
	serve(mock, http.MethodPost, revealPath, http.StatusOK, `{"value":null,"event_id":92}`, nil)

	out, errOut, err := run(t, "cell", "--table-id", "42", "--row-id", "3", "--field", "email")
	if err != nil {
		t.Fatalf("reveal cell: %v", err)
	}
	if out != "" {
		t.Errorf("stdout = %q, want nothing for a null value", out)
	}
	if !strings.Contains(errOut, "row 3 field email is null") || !strings.Contains(errOut, "event 92") {
		t.Errorf("stderr = %q, want the null line naming the event", errOut)
	}
}

func TestJSONAndYAMLPrintTheServerAnswer(t *testing.T) {
	for _, tc := range []struct{ format, want string }{
		{"json", "{\n  \"event_id\": 93,\n  \"value\": null\n}\n"},
		{"yaml", "event_id: 93\nvalue: null\n"},
	} {
		t.Run(tc.format, func(t *testing.T) {
			mock := newTestEnv(t)
			serve(mock, http.MethodPost, revealPath, http.StatusOK, `{"value":null,"event_id":93}`, nil)
			out, errOut, err := run(t, "cell", "--table-id", "42", "--row-id", "3", "--field", "email", "-o", tc.format)
			if err != nil {
				t.Fatalf("reveal cell: %v", err)
			}
			if out != tc.want {
				t.Errorf("stdout = %q, want %q", out, tc.want)
			}
			if errOut != "" {
				t.Errorf("stderr = %q, want nothing", errOut)
			}
		})
	}
}

func TestCellSanitizesTerminalControls(t *testing.T) {
	mock := newTestEnv(t)
	serve(mock, http.MethodPost, revealPath, http.StatusOK, `{"value":"a\u001b[31mb","event_id":1}`, nil)
	out, _, err := run(t, "cell", "--table-id", "42", "--row-id", "1", "--field", "email")
	if err != nil {
		t.Fatalf("reveal cell: %v", err)
	}
	if strings.ContainsRune(out, '\x1b') {
		t.Errorf("stdout = %q carries a raw escape", out)
	}
}

func TestAlertReadsItsTableFromTheAlert(t *testing.T) {
	mock := newTestEnv(t)
	serve(mock, http.MethodGet, alertPath, http.StatusOK, `{"id":5,"dataset_id":42,"msg":"masked"}`, nil)
	bodies := serve(mock, http.MethodPost, revealPath, http.StatusOK,
		`{"value":"Invalid name ~{Jonathan Smithers}~","event_id":94}`, nil)

	out, errOut, err := run(t, "alert", "alert/5")
	if err != nil {
		t.Fatalf("reveal alert: %v", err)
	}
	if out != "Invalid name Jonathan Smithers\n" {
		t.Errorf("stdout = %q, want the message rendered plain", out)
	}
	if errOut != "Reveal recorded as event 94\n" {
		t.Errorf("stderr = %q", errOut)
	}
	assertBody(t, bodies, map[string]any{"kind": "alert", "alert_id": float64(5)})
	assertRequests(t, mock, "GET "+alertPath, "POST "+revealPath)
}

func TestWarningWithATableSkipsTheLookup(t *testing.T) {
	mock := newTestEnv(t)
	bodies := serve(mock, http.MethodPost, revealPath, http.StatusOK, `{"value":"w","event_id":95}`, nil)

	if _, _, err := run(t, "warning", "6", "--table-id", "42"); err != nil {
		t.Fatalf("reveal warning: %v", err)
	}
	assertBody(t, bodies, map[string]any{"kind": "warning", "warning_id": float64(6)})
	assertRequests(t, mock, "POST "+revealPath)
}

func TestWarningReadsItsTableFromTheWarning(t *testing.T) {
	mock := newTestEnv(t)
	serve(mock, http.MethodGet, warningPath, http.StatusOK, `{"id":6,"dataset_id":42,"msg":"masked"}`, nil)
	serve(mock, http.MethodPost, revealPath, http.StatusOK, `{"value":"w","event_id":96}`, nil)

	if _, _, err := run(t, "warning", "6"); err != nil {
		t.Fatalf("reveal warning: %v", err)
	}
	assertRequests(t, mock, "GET "+warningPath, "POST "+revealPath)
}

func TestUsageErrorsMakeNoRequest(t *testing.T) {
	cases := map[string][]string{
		"cell without a table":       {"cell", "--row-id", "1", "--field", "email"},
		"cell without row-id":        {"cell", "--table-id", "42", "--field", "email"},
		"cell without field":         {"cell", "--table-id", "42", "--row-id", "1"},
		"cell non-positive row-id":   {"cell", "--table-id", "42", "--row-id", "0", "--field", "email"},
		"cell blank field":           {"cell", "--table-id", "42", "--row-id", "1", "--field", "  "},
		"cell table twice":           {"cell", "orders", "--table-id", "42", "--row-id", "1", "--field", "email"},
		"cell -o name":               {"cell", "--table-id", "42", "--row-id", "1", "--field", "email", "-o", "name"},
		"alert without an id":        {"alert"},
		"alert non-numeric id":       {"alert", "abc"},
		"alert zero id":              {"alert", "0"},
		"alert of the wrong kind":    {"alert", "warning/5"},
		"alert two ids":              {"alert", "5", "6"},
		"warning table twice":        {"warning", "6", "--table", "orders", "--table-id", "42"},
		"warning blank table":        {"warning", "6", "--table", ""},
		"warning non-positive table": {"warning", "6", "--table-id", "-1"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			mock := newTestEnv(t)
			_, _, err := run(t, args...)
			assertExitCode(t, err, qerrors.ExitBadUsage)
			assertRequests(t, mock)
		})
	}
}

func TestServerRefusalsKeepTheirExitCodes(t *testing.T) {
	for _, tc := range []struct {
		name string
		code int
		body string
		exit int
	}{
		{"forbidden", http.StatusForbidden, `{"detail":"You don't have access to reveal personal data. Please contact your administrator to request access."}`, qerrors.ExitUnauthorized},
		{"not a personal-data column", http.StatusBadRequest, `{"detail":"Choose a personal-data column to reveal."}`, qerrors.ExitBadUsage},
		{"missing row", http.StatusNotFound, `{"detail":"The row is unavailable."}`, qerrors.ExitNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := newTestEnv(t)
			serve(mock, http.MethodPost, revealPath, tc.code, tc.body, nil)
			out, _, err := run(t, "cell", "--table-id", "42", "--row-id", "1", "--field", "email")
			assertExitCode(t, err, tc.exit)
			if out != "" {
				t.Errorf("stdout = %q, want nothing on a refusal", out)
			}
		})
	}
}

func TestLimitSaysWhenToRetryAndHowToLiftIt(t *testing.T) {
	mock := newTestEnv(t)
	serve(mock, http.MethodPost, revealPath, http.StatusTooManyRequests,
		`{"detail":"Personal-data reveal limit reached. Ask your administrator for a temporary unmasked access grant."}`,
		map[string]string{"Retry-After": "1500"})

	_, _, err := run(t, "alert", "5", "--table-id", "42")
	assertExitCode(t, err, qerrors.ExitGenericError)
	msg := err.Error()
	for _, want := range []string{
		"Personal-data reveal limit reached",
		"Try again in 25 minutes",
		"qctl pii-grant create --table-id 42 --to <your-email> --reason <why>",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not contain %q", msg, want)
		}
	}
	// One POST: the limit is never retried.
	assertRequests(t, mock, "POST "+revealPath)
}

func TestLimitWithoutRetryAfterSaysLater(t *testing.T) {
	mock := newTestEnv(t)
	serve(mock, http.MethodPost, revealPath, http.StatusTooManyRequests, `{"detail":"Personal-data reveal limit reached."}`, nil)

	_, _, err := run(t, "warning", "6", "--table-id", "42")
	assertExitCode(t, err, qerrors.ExitGenericError)
	if !strings.Contains(err.Error(), "Try again later, or ask an admin") {
		t.Errorf("error = %q", err)
	}
}

func TestLimitOfOneMinuteIsSingular(t *testing.T) {
	mock := newTestEnv(t)
	serve(mock, http.MethodPost, revealPath, http.StatusTooManyRequests, `{"detail":"limit"}`, map[string]string{"Retry-After": "5"})

	_, _, err := run(t, "cell", "--table-id", "42", "--row-id", "1", "--field", "email")
	if err == nil || !strings.Contains(err.Error(), "Try again in 1 minute,") {
		t.Errorf("error = %v, want the singular minute", err)
	}
}
