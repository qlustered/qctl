package email_sender //nolint:revive // Package name mirrors the command.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/email_intake"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

const addTestOrgID = "c3d4e5f6-a7b8-9012-cdef-345678901234"

const (
	addSendersPath = "/api/orgs/" + addTestOrgID + "/data-sources/42/email-intake/senders"
	addPreviewPath = "/api/orgs/" + addTestOrgID + "/data-sources/42/email-intake/welcome/preview"
)

var addUserID = uuid.MustParse("0190dddd-0000-4000-8000-000000000001")

// addTestRoot wires what root.go wires for the exit codes asserted here:
// arity and flag errors, a missing required flag and an unsupported -o value
// all exit 2, and kind/ prefixes are stripped.
func addTestRoot() *cobra.Command {
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

func addTestEnv(t *testing.T) *testutil.MockAPIServer {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", addTestOrgID)
	env.SetupCredential(endpointKey, addTestOrgID, "test-token")
	return mock
}

func addRunCmd(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := addTestRoot()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(append([]string{"email-sender", "add"}, args...))
	err := cmd.Execute()
	return out.String(), errOut.String(), err
}

// addServe answers a POST to path with code and body, and returns the request
// bodies it received.
func addServe(mock *testutil.MockAPIServer, path string, code int, body []byte) *[][]byte {
	var bodies [][]byte
	mock.RegisterHandler(http.MethodPost, path, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		bodies = append(bodies, raw)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = w.Write(body)
	})
	return &bodies
}

func addAssertRequests(t *testing.T, mock *testutil.MockAPIServer, want ...string) {
	t.Helper()
	got := mock.Requests()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %q, want %q", got, want)
	}
}

// addWire encodes v the way -o json does, so output compares byte for byte.
func addWire(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	return append(raw, '\n')
}

func addDecode(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("request body %q is not a JSON object: %v", raw, err)
	}
	return body
}

var addScheduledAt = time.Date(2026, 11, 4, 9, 30, 0, 0, time.UTC)

func addFixture(status email_intake.WelcomeStatus) email_intake.AddedSender {
	added := email_intake.AddedSender{
		UserID: addUserID, Email: "carol@example.com", FirstName: "Carol", LastName: "Smith",
		EmailDeliveryStatus: api.EmailDeliveryStatusSchema{Status: api.EmailDeliveryStatusSchemaStatusOk},
		Welcome:             email_intake.WelcomeOutcome{Status: status},
	}
	if status == email_intake.WelcomeScheduled || status == email_intake.WelcomeAlreadyScheduled {
		at := addScheduledAt
		added.Welcome.FirstScheduledAt = &at
	}
	return added
}

var addValidArgs = []string{"carol@example.com", "--first-name", "Carol", "--last-name", "Smith", "--cloud-source-id", "42"}

func TestAddUsageErrorsMakeNoRequest(t *testing.T) {
	cases := map[string][]string{
		"no email":             {"--first-name", "C", "--last-name", "S", "--cloud-source-id", "42"},
		"two emails":           {"a@x.com", "b@x.com", "--first-name", "C", "--last-name", "S", "--cloud-source-id", "42"},
		"blank email":          {"  ", "--first-name", "C", "--last-name", "S", "--cloud-source-id", "42"},
		"blank first name":     {"a@x.com", "--first-name", " ", "--last-name", "S", "--cloud-source-id", "42"},
		"blank last name":      {"a@x.com", "--first-name", "C", "--last-name", "\t", "--cloud-source-id", "42"},
		"no first name":        {"a@x.com", "--last-name", "S", "--cloud-source-id", "42"},
		"no last name":         {"a@x.com", "--first-name", "C", "--cloud-source-id", "42"},
		"no cloud source":      {"a@x.com", "--first-name", "C", "--last-name", "S"},
		"zero cloud source":    {"a@x.com", "--first-name", "C", "--last-name", "S", "--cloud-source-id", "0"},
		"wrong kind of ref":    {"a@x.com", "--first-name", "C", "--last-name", "S", "--cloud-source-id", "table/42"},
		"names needed dry-run": {"a@x.com", "--cloud-source-id", "42", "--dry-run"},
		"name with dry-run":    append(append([]string{}, addValidArgs...), "--dry-run", "-o", "name"),
		"unknown format":       append(append([]string{}, addValidArgs...), "-o", "code"),
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			mock := addTestEnv(t)
			_, _, err := addRunCmd(t, args...)
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d (err: %v)", got, qerrors.ExitBadUsage, err)
			}
			addAssertRequests(t, mock)
		})
	}
}

func TestAddPostsTrimmedFieldsAsTyped(t *testing.T) {
	mock := addTestEnv(t)
	added := addFixture(email_intake.WelcomeScheduled)
	bodies := addServe(mock, addSendersPath, http.StatusOK, addWire(t, added))

	out, errOut, err := addRunCmd(t, "  Carol@Example.com ", "--first-name", " Carol ", "--last-name", "Smith  ",
		"--cloud-source-id", "cloud-source/42")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	addAssertRequests(t, mock, "POST "+addSendersPath)
	want := map[string]any{"email": "Carol@Example.com", "first_name": "Carol", "last_name": "Smith"}
	if got := addDecode(t, (*bodies)[0]); !reflect.DeepEqual(got, want) {
		t.Errorf("body = %v, want %v", got, want)
	}
	wantOut := "email-sender/" + addUserID.String() + " added to cloud-source/42: Carol Smith <carol@example.com>\n" +
		"Welcome email scheduled; this does not confirm delivery.\n" +
		"Next: qctl get email-senders --cloud-source-id 42\n"
	if out != wantOut {
		t.Errorf("stdout = %q, want %q", out, wantOut)
	}
	if errOut != "" {
		t.Errorf("stderr = %q, want nothing", errOut)
	}
}

func TestAddWarnsOnEveryOutcomeButScheduledAndExitsZero(t *testing.T) {
	refused := api.InvalidAddress
	cases := []struct {
		status  email_intake.WelcomeStatus
		reason  *api.OutboundEmailSkipReason
		stderrs []string
	}{
		{email_intake.WelcomeAlreadyScheduled, nil, []string{
			"warning: carol@example.com already had the welcome email (first scheduled ",
			"; no new one was sent\n",
			"  To send it again: qctl email-sender resend-welcome carol@example.com --cloud-source-id 42\n",
		}},
		{email_intake.WelcomeSourceInactive, nil, []string{
			"warning: sender added, but no welcome email was sent because cloud-source/42 is inactive\n",
		}},
		{email_intake.WelcomeRecipientRefused, &refused, []string{
			"warning: sender added, but the welcome email could not be sent to carol@example.com (invalid_address)",
		}},
	}
	for _, tc := range cases {
		for _, format := range []string{"table", "json", "name"} {
			t.Run(string(tc.status)+"/"+format, func(t *testing.T) {
				mock := addTestEnv(t)
				added := addFixture(tc.status)
				added.Welcome.RefusalReason = tc.reason
				addServe(mock, addSendersPath, http.StatusOK, addWire(t, added))

				out, errOut, err := addRunCmd(t, append(append([]string{}, addValidArgs...), "-o", format)...)
				if err != nil {
					t.Fatalf("add: %v (exit %d)", err, qerrors.GetExitCode(err))
				}
				for _, want := range tc.stderrs {
					if !strings.Contains(errOut, want) {
						t.Errorf("stderr = %q, want it to contain %q", errOut, want)
					}
				}
				if strings.Contains(out, "warning") || strings.Contains(out, "scheduled;") {
					t.Errorf("stdout = %q carries a warning or the scheduled line", out)
				}
			})
		}
	}
}

func TestAddWarnsWhenTheAccountKeepsItsNames(t *testing.T) {
	mock := addTestEnv(t)
	added := addFixture(email_intake.WelcomeScheduled)
	added.FirstName, added.LastName = "Caroline", "Smith"
	addServe(mock, addSendersPath, http.StatusOK, addWire(t, added))

	_, errOut, err := addRunCmd(t, append(append([]string{}, addValidArgs...), "-o", "json")...)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	want := "warning: carol@example.com already has an account named \"Caroline Smith\"; it keeps that name, not \"Carol Smith\"\n"
	if errOut != want {
		t.Errorf("stderr = %q, want %q", errOut, want)
	}
}

func TestAddMachineOutputs(t *testing.T) {
	added := addFixture(email_intake.WelcomeScheduled)
	t.Run("json is the server body", func(t *testing.T) {
		mock := addTestEnv(t)
		body := addWire(t, added)
		addServe(mock, addSendersPath, http.StatusOK, body)
		out, _, err := addRunCmd(t, append(append([]string{}, addValidArgs...), "-o", "json")...)
		if err != nil {
			t.Fatalf("add: %v", err)
		}
		if out != string(body) {
			t.Errorf("stdout = %s, want %s", out, body)
		}
	})
	t.Run("yaml is the server body", func(t *testing.T) {
		mock := addTestEnv(t)
		addServe(mock, addSendersPath, http.StatusOK, addWire(t, added))
		out, _, err := addRunCmd(t, append(append([]string{}, addValidArgs...), "-o", "yaml")...)
		if err != nil {
			t.Fatalf("add: %v", err)
		}
		var got map[string]any
		if err := yaml.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("stdout is not YAML: %v\n%s", err, out)
		}
		if got["user_id"] != addUserID.String() || got["welcome"].(map[string]any)["status"] != "scheduled" {
			t.Errorf("yaml = %v", got)
		}
	})
	t.Run("name is the sender reference", func(t *testing.T) {
		mock := addTestEnv(t)
		addServe(mock, addSendersPath, http.StatusOK, addWire(t, added))
		out, errOut, err := addRunCmd(t, append(append([]string{}, addValidArgs...), "-o", "name")...)
		if err != nil {
			t.Fatalf("add: %v", err)
		}
		if want := "email-sender/" + addUserID.String() + "\n"; out != want {
			t.Errorf("stdout = %q, want %q", out, want)
		}
		if errOut != "" {
			t.Errorf("stderr = %q, want nothing", errOut)
		}
	})
}

func TestAddRefusalExitCodes(t *testing.T) {
	cases := map[string]struct {
		code int
		want int
	}{
		"ops user 409":  {http.StatusConflict, qerrors.ExitBadUsage},
		"forbidden 403": {http.StatusForbidden, qerrors.ExitUnauthorized},
		"not found 404": {http.StatusNotFound, qerrors.ExitNotFound},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			mock := addTestEnv(t)
			addServe(mock, addSendersPath, tc.code, []byte(`{"detail":"An operations user cannot be an intake sender."}`))
			_, _, err := addRunCmd(t, addValidArgs...)
			if got := qerrors.GetExitCode(err); got != tc.want {
				t.Errorf("exit code = %d, want %d (err: %v)", got, tc.want, err)
			}
		})
	}
}

func TestAddDryRunPreviewsAndAddsNobody(t *testing.T) {
	preview := email_intake.WelcomePreview{
		Subject: "Send your files to Acme",
		HTML:    "<p>Hello Carol</p>",
		Text:    "Hello Carol,\r\nEmail your files to intake@example.com.\r\n",
	}
	dryArgs := append(append([]string{}, addValidArgs...), "--dry-run")

	t.Run("table prints subject and text body", func(t *testing.T) {
		mock := addTestEnv(t)
		bodies := addServe(mock, addPreviewPath, http.StatusOK, addWire(t, preview))
		out, errOut, err := addRunCmd(t, dryArgs...)
		if err != nil {
			t.Fatalf("add --dry-run: %v", err)
		}
		addAssertRequests(t, mock, "POST "+addPreviewPath)
		want := map[string]any{"email": "carol@example.com"}
		if got := addDecode(t, (*bodies)[0]); !reflect.DeepEqual(got, want) {
			t.Errorf("body = %v, want %v", got, want)
		}
		wantOut := "Subject: Send your files to Acme\n\nHello Carol,\nEmail your files to intake@example.com.\n"
		if out != wantOut {
			t.Errorf("stdout = %q, want %q", out, wantOut)
		}
		if errOut != "" {
			t.Errorf("stderr = %q, want nothing", errOut)
		}
	})
	t.Run("json is the server body", func(t *testing.T) {
		mock := addTestEnv(t)
		body := addWire(t, preview)
		addServe(mock, addPreviewPath, http.StatusOK, body)
		out, _, err := addRunCmd(t, append(dryArgs, "-o", "json")...)
		if err != nil {
			t.Fatalf("add --dry-run: %v", err)
		}
		if out != string(body) {
			t.Errorf("stdout = %s, want %s", out, body)
		}
	})
	t.Run("inactive source 409 exits 2", func(t *testing.T) {
		mock := addTestEnv(t)
		addServe(mock, addPreviewPath, http.StatusConflict,
			[]byte(`{"detail":"Activate the intake before sending a welcome."}`))
		_, _, err := addRunCmd(t, dryArgs...)
		if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
			t.Errorf("exit code = %d, want %d (err: %v)", got, qerrors.ExitBadUsage, err)
		}
		addAssertRequests(t, mock, "POST "+addPreviewPath)
	})
}
