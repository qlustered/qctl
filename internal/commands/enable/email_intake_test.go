package enable

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
	root.SetArgs(append([]string{"enable", "email-intake"}, args...))
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

func intakeOn(auto bool) api.EmailIntakeSchema {
	addr := "in-abc@intake.example.com"
	return api.EmailIntakeSchema{EmailIntakeAddress: &addr, EmailIntakeEnabled: true, AutoSendIssuesReport: auto}
}

// Without --auto-send-issues-report the PUT carries null, which keeps the
// server's current setting; the prefixed positional reaches the same source.
func TestEnableEmailIntakeLeavesTheReportSettingAloneWithoutTheFlag(t *testing.T) {
	mock := intakeEnv(t)
	bodies := servePut(mock, http.StatusOK, intakeOn(false))

	out, err := runIntake(t, "cloud-source/42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(*bodies) != 1 {
		t.Fatalf("want one PUT, got requests %v", mock.Requests())
	}
	body := (*bodies)[0]
	if body["enabled"] != true {
		t.Errorf("enabled = %v, want true", body["enabled"])
	}
	if v, ok := body["auto_send_issues_report"]; ok && v != nil {
		t.Errorf("auto_send_issues_report = %v, want null or absent", v)
	}
	for _, want := range []string{
		"Email intake for cloud-source/42 is on: in-abc@intake.example.com",
		"Senders who have not had the email-intake welcome are scheduled one, unless the source is inactive or their address is refused.",
		"Issues reports: not sent automatically",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// An explicit false must reach the server as false, not be dropped as the
// zero value.
func TestEnableEmailIntakeSendsAnExplicitReportSetting(t *testing.T) {
	for _, tc := range []struct {
		flag string
		want bool
	}{
		{"--auto-send-issues-report=false", false},
		{"--auto-send-issues-report", true},
	} {
		t.Run(tc.flag, func(t *testing.T) {
			mock := intakeEnv(t)
			bodies := servePut(mock, http.StatusOK, intakeOn(tc.want))
			if _, err := runIntake(t, "42", tc.flag); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(*bodies) != 1 {
				t.Fatalf("want one PUT, got requests %v", mock.Requests())
			}
			v, ok := (*bodies)[0]["auto_send_issues_report"]
			if !ok || v != tc.want {
				t.Errorf("auto_send_issues_report = %v (present %v), want %v", v, ok, tc.want)
			}
		})
	}
}

func TestEnableEmailIntakeOutputFormats(t *testing.T) {
	mock := intakeEnv(t)
	servePut(mock, http.StatusOK, intakeOn(true))

	out, err := runIntake(t, "42", "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("-o json is not JSON: %v\n%s", err, out)
	}
	if got["email_intake_address"] != "in-abc@intake.example.com" || got["email_intake_enabled"] != true ||
		got["auto_send_issues_report"] != true {
		t.Errorf("-o json = %v, want the raw intake", got)
	}

	out, err = runIntake(t, "42", "-o", "name")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "cloud-source/42\n" {
		t.Errorf("-o name = %q, want cloud-source/42", out)
	}
}

func TestEnableEmailIntakeRejectsABadIDBeforeAnyRequest(t *testing.T) {
	for _, arg := range []string{"abc", "0", "table/42"} {
		t.Run(arg, func(t *testing.T) {
			mock := intakeEnv(t)
			_, err := runIntake(t, arg)
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit = %d, want %d (err: %v)", got, qerrors.ExitBadUsage, err)
			}
			if reqs := mock.Requests(); len(reqs) != 0 {
				t.Errorf("no request may be sent, got %v", reqs)
			}
		})
	}
}

func TestEnableEmailIntakeServerRefusalsKeepTheirExitCodes(t *testing.T) {
	for name, tc := range map[string]struct {
		code int
		body any
		want int
	}{
		"change review": {http.StatusForbidden, map[string]any{"detail": map[string]any{
			"msg":        "This table's change review policy does not let the member role resolve this change.",
			"error_code": "CHANGE_REVIEW_POLICY_DENIED",
			"context":    map[string]any{"operation": "email_intake_update", "verb": "resolve", "denied_keys": []any{[]any{"enabled"}}},
		}}, qerrors.ExitUnauthorized},
		"not a portal series source": {http.StatusBadRequest, map[string]any{
			"detail": "Email intake requires a portal data source in a table series.",
		}, qerrors.ExitBadUsage},
	} {
		t.Run(name, func(t *testing.T) {
			mock := intakeEnv(t)
			servePut(mock, tc.code, tc.body)
			_, err := runIntake(t, "42")
			if got := qerrors.GetExitCode(err); got != tc.want {
				t.Errorf("exit = %d, want %d (err: %v)", got, tc.want, err)
			}
		})
	}
}
