package disable

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const intakePath = "/api/orgs/" + testOrgID + "/data-sources/42/email-intake"

// intakeRoot wires what root.go wires for the exit codes asserted here: flag
// errors exit 2 and kind/ prefixes are stripped from positionals.
func intakeRoot() *cobra.Command {
	rootCmd := &cobra.Command{Use: "qctl", SilenceUsage: true, SilenceErrors: true}
	rootCmd.PersistentFlags().String("server", "", "")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "")
	rootCmd.PersistentFlags().CountP("verbose", "v", "")
	rootCmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return qerrors.Wrap(qerrors.ExitBadUsage, err, "")
	})
	rootCmd.AddCommand(NewCommand())
	cmdutil.TypeArgsErrors(rootCmd)
	cmdutil.StripKindRefs(rootCmd)
	return rootCmd
}

func intakeEnv(t *testing.T) *testutil.MockAPIServer {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")
	return mock
}

func runIntake(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := intakeRoot()
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(append([]string{"disable", "email-intake"}, args...))
	err := root.Execute()
	return out.String(), err
}

// servePut answers the intake PUT with code and body, and records each
// request body it received.
func servePut(mock *testutil.MockAPIServer, code int, body any) *[]map[string]any {
	var bodies []map[string]any
	mock.RegisterHandler(http.MethodPut, intakePath, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		bodies = append(bodies, m)
		testutil.RespondJSON(w, code, body)
	})
	return &bodies
}

func intakeOff(addr *string) api.EmailIntakeSchema {
	return api.EmailIntakeSchema{EmailIntakeAddress: addr, EmailIntakeEnabled: false}
}

// Disabling sends one PUT, without the report setting unless asked, and says
// the address survives for a later enable.
func TestDisableEmailIntakeKeepsTheAddress(t *testing.T) {
	mock := intakeEnv(t)
	addr := "in-abc@intake.example.com"
	bodies := servePut(mock, http.StatusOK, intakeOff(&addr))

	out, err := runIntake(t, "cloud-source/42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reqs := mock.Requests(); len(reqs) != 1 || len(*bodies) != 1 {
		t.Fatalf("want exactly one PUT, got %v", reqs)
	}
	body := (*bodies)[0]
	if body["enabled"] != false {
		t.Errorf("enabled = %v, want false", body["enabled"])
	}
	if v, ok := body["auto_send_issues_report"]; ok && v != nil {
		t.Errorf("auto_send_issues_report = %v, want null or absent", v)
	}
	for _, want := range []string{
		"Email intake for cloud-source/42 is off.",
		"Its address in-abc@intake.example.com is kept; 'qctl enable email-intake 42' turns intake back on at it.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestDisableEmailIntakeWithNoAddressSaysSo(t *testing.T) {
	mock := intakeEnv(t)
	servePut(mock, http.StatusOK, intakeOff(nil))
	out, err := runIntake(t, "42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out, "is kept") || !strings.Contains(out, "It has no address yet") {
		t.Errorf("a source without an address must not claim one is kept:\n%s", out)
	}
}

func TestDisableEmailIntakeSendsAnExplicitReportSetting(t *testing.T) {
	mock := intakeEnv(t)
	bodies := servePut(mock, http.StatusOK, intakeOff(nil))
	if _, err := runIntake(t, "42", "--auto-send-issues-report=false"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	v, ok := (*bodies)[0]["auto_send_issues_report"]
	if !ok || v != false {
		t.Errorf("auto_send_issues_report = %v (present %v), want false", v, ok)
	}
}

func TestDisableEmailIntakeOutputFormats(t *testing.T) {
	mock := intakeEnv(t)
	addr := "in-abc@intake.example.com"
	servePut(mock, http.StatusOK, intakeOff(&addr))

	out, err := runIntake(t, "42", "-o", "yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "email_intake_address: in-abc@intake.example.com") ||
		!strings.Contains(out, "email_intake_enabled: false") {
		t.Errorf("-o yaml must print the raw intake, got:\n%s", out)
	}

	out, err = runIntake(t, "42", "-o", "name")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "cloud-source/42\n" {
		t.Errorf("-o name = %q, want cloud-source/42", out)
	}
}

func TestDisableEmailIntakeChangeReviewRefusalExitsFour(t *testing.T) {
	mock := intakeEnv(t)
	servePut(mock, http.StatusForbidden, map[string]any{"detail": map[string]any{
		"msg":        "This table's change review policy does not let the member role resolve this change.",
		"error_code": "CHANGE_REVIEW_POLICY_DENIED",
		"context":    map[string]any{"operation": "email_intake_update", "verb": "resolve", "denied_keys": []any{[]any{"enabled"}}},
	}})
	_, err := runIntake(t, "42")
	if got := qerrors.GetExitCode(err); got != qerrors.ExitUnauthorized {
		t.Errorf("exit = %d, want %d (err: %v)", got, qerrors.ExitUnauthorized, err)
	}
}
