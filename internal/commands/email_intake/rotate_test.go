package email_intake //nolint:revive // Package name mirrors the command.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const (
	testOrgID   = "b2c3d4e5-f6a7-8901-bcde-f23456789012"
	intakePath  = "/api/orgs/" + testOrgID + "/data-sources/42/email-intake"
	sendersPath = intakePath + "/senders"
	rotatePath  = intakePath + "/rotate"
	oldAddress  = "in-old@intake.example.com"
	newAddress  = "in-new@intake.example.com"
)

var (
	getIntake  = "GET " + intakePath
	getSenders = "GET " + sendersPath
	postRotate = "POST " + rotatePath
)

// setupTestCommand wires what root.go wires for the exit codes asserted here:
// flag errors exit 2 and kind/ prefixes are stripped from positionals.
func setupTestCommand() *cobra.Command {
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

func newTestEnv(t *testing.T) *testutil.MockAPIServer {
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

func run(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	root := setupTestCommand()
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(append([]string{"email-intake", "rotate"}, args...))
	err := root.Execute()
	return out.String(), errOut.String(), err
}

// answer feeds the confirmation prompt.
func answer(t *testing.T, reply string) {
	t.Helper()
	cmdutil.SetReader(strings.NewReader(reply))
	t.Cleanup(cmdutil.ResetReader)
}

func intake(addr *string) api.EmailIntakeSchema {
	return api.EmailIntakeSchema{EmailIntakeAddress: addr, EmailIntakeEnabled: true}
}

func ptr(s string) *string { return &s }

func serveIntake(mock *testutil.MockAPIServer, addr *string) {
	mock.RegisterHandler(http.MethodGet, intakePath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, intake(addr))
	})
}

func serveSenders(mock *testutil.MockAPIServer, code int, emails ...string) {
	mock.RegisterHandler(http.MethodGet, sendersPath, func(w http.ResponseWriter, _ *http.Request) {
		if code != http.StatusOK {
			testutil.RespondJSON(w, code, map[string]any{"detail": "boom"})
			return
		}
		senders := make([]api.EmailIntakeSenderSchema, 0, len(emails))
		for _, e := range emails {
			senders = append(senders, api.EmailIntakeSenderSchema{
				UserID: uuid.New(), Email: e, FirstName: "F", LastName: "L",
				EmailDeliveryStatus: api.EmailDeliveryStatusSchema{},
			})
		}
		testutil.RespondJSON(w, http.StatusOK, senders)
	})
}

// serveRotate answers the rotate POST and records each request body.
func serveRotate(mock *testutil.MockAPIServer, code int, body any) *[]map[string]any {
	var bodies []map[string]any
	mock.RegisterHandler(http.MethodPost, rotatePath, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		bodies = append(bodies, m)
		testutil.RespondJSON(w, code, body)
	})
	return &bodies
}

func assertRequests(t *testing.T, mock *testutil.MockAPIServer, want ...string) {
	t.Helper()
	if got := mock.Requests(); !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %q, want %q", got, want)
	}
}

func assertExit(t *testing.T, err error, want int) {
	t.Helper()
	if got := qerrors.GetExitCode(err); got != want {
		t.Errorf("exit = %d, want %d (err: %v)", got, want, err)
	}
}

// With no address there is nothing to rotate: refuse locally, before the
// senders read and before any prompt, as the backend's 409 would.
func TestRotateWithoutAnAddressExitsTwoBeforePrompting(t *testing.T) {
	mock := newTestEnv(t)
	serveIntake(mock, nil)
	answer(t, "y\n")

	_, stderr, err := run(t, "42")
	assertExit(t, err, qerrors.ExitBadUsage)
	if err == nil || !strings.Contains(err.Error(), "Enable email intake before rotating its address") {
		t.Errorf("error should mirror the backend's message, got %v", err)
	}
	assertRequests(t, mock, getIntake)
	if strings.Contains(stderr, "Rotate the email intake address") {
		t.Errorf("no prompt may be shown, stderr:\n%s", stderr)
	}
}

// The prompt names the senders who will get the new address and says mail to
// the old one is dropped; a yes sends email_senders: true explicitly.
func TestRotatePromptNamesTheSendersAndNotifiesThem(t *testing.T) {
	mock := newTestEnv(t)
	serveIntake(mock, ptr(oldAddress))
	serveSenders(mock, http.StatusOK, "a@x.example", "b@y.example")
	bodies := serveRotate(mock, http.StatusOK, intake(ptr(newAddress)))
	answer(t, "y\n")

	out, stderr, err := run(t, "cloud-source/42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertRequests(t, mock, getIntake, getSenders, postRotate)
	for _, want := range []string{
		"Mail sent to " + oldAddress + " will be dropped.",
		"The new address will be emailed to 2 senders (a@x.example, b@y.example).",
		"Rotate the email intake address of cloud-source/42? [y/N]",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q:\n%s", want, stderr)
		}
	}
	if !reflect.DeepEqual((*bodies)[0], map[string]any{"email_senders": true}) {
		t.Errorf("rotate body = %v, want {email_senders: true}", (*bodies)[0])
	}
	if !strings.Contains(out, oldAddress+" -> "+newAddress) {
		t.Errorf("output must show old -> new, got:\n%s", out)
	}
}

func TestRotateNoNotifySendersSendsFalse(t *testing.T) {
	mock := newTestEnv(t)
	serveIntake(mock, ptr(oldAddress))
	serveSenders(mock, http.StatusOK, "a@x.example")
	bodies := serveRotate(mock, http.StatusOK, intake(ptr(newAddress)))

	out, _, err := run(t, "42", "--no-notify-senders", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual((*bodies)[0], map[string]any{"email_senders": false}) {
		t.Errorf("rotate body = %v, want {email_senders: false}", (*bodies)[0])
	}
	if !strings.Contains(out, "The senders were not emailed the new address.") {
		t.Errorf("output must say the senders were not emailed, got:\n%s", out)
	}
}

// A failed senders read must not block the rotation: warn and prompt without
// names.
func TestRotateWarnsWhenTheSendersCannotBeRead(t *testing.T) {
	mock := newTestEnv(t)
	serveIntake(mock, ptr(oldAddress))
	serveSenders(mock, http.StatusInternalServerError)
	serveRotate(mock, http.StatusOK, intake(ptr(newAddress)))
	answer(t, "y\n")

	_, stderr, err := run(t, "42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertRequests(t, mock, getIntake, getSenders, postRotate)
	for _, want := range []string{
		"Warning: could not list the senders of cloud-source/42",
		"The new address will be emailed to each of the source's senders.",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q:\n%s", want, stderr)
		}
	}
}

func TestRotateDeclinedSendsNothing(t *testing.T) {
	mock := newTestEnv(t)
	serveIntake(mock, ptr(oldAddress))
	serveSenders(mock, http.StatusOK)
	serveRotate(mock, http.StatusOK, intake(ptr(newAddress)))
	answer(t, "n\n")

	_, stderr, err := run(t, "42")
	assertExit(t, err, qerrors.ExitGenericError)
	assertRequests(t, mock, getIntake, getSenders)
	if !strings.Contains(stderr, "The source has no senders to email the new address to.") {
		t.Errorf("stderr must say there are no senders:\n%s", stderr)
	}
}

func TestRotateWithoutATerminalNeedsYes(t *testing.T) {
	mock := newTestEnv(t)
	serveIntake(mock, ptr(oldAddress))
	serveSenders(mock, http.StatusOK)
	serveRotate(mock, http.StatusOK, intake(ptr(newAddress)))

	_, stderr, err := run(t, "42")
	assertExit(t, err, qerrors.ExitBadUsage)
	assertRequests(t, mock, getIntake)
	if strings.Contains(stderr, "will be dropped") {
		t.Errorf("a refused run must not print the prompt's context:\n%s", stderr)
	}
}

// An off intake's new address accepts no mail, so emailing it to the senders
// is refused before any prompt; --no-notify-senders rotates it silently.
func TestRotateAnOffIntakeRefusesToNotifySenders(t *testing.T) {
	mock := newTestEnv(t)
	mock.RegisterHandler(http.MethodGet, intakePath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.EmailIntakeSchema{EmailIntakeAddress: ptr(oldAddress)})
	})
	serveSenders(mock, http.StatusOK, "a@x.example")
	bodies := serveRotate(mock, http.StatusOK, api.EmailIntakeSchema{EmailIntakeAddress: ptr(newAddress)})
	answer(t, "y\n")

	_, _, err := run(t, "42", "--yes")
	assertExit(t, err, qerrors.ExitBadUsage)
	assertRequests(t, mock, getIntake)

	if _, _, err := run(t, "42", "--yes", "--no-notify-senders"); err != nil {
		t.Fatalf("--no-notify-senders on an off intake: %v", err)
	}
	if len(*bodies) != 1 || !reflect.DeepEqual((*bodies)[0], map[string]any{"email_senders": false}) {
		t.Errorf("rotate bodies = %v, want one {email_senders: false}", *bodies)
	}
}

func TestRotateOutputFormats(t *testing.T) {
	mock := newTestEnv(t)
	serveIntake(mock, ptr(oldAddress))
	serveSenders(mock, http.StatusOK)
	serveRotate(mock, http.StatusOK, intake(ptr(newAddress)))

	out, _, err := run(t, "42", "--yes", "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("-o json is not JSON: %v\n%s", err, out)
	}
	if got["email_intake_address"] != newAddress {
		t.Errorf("-o json = %v, want the raw intake with the new address", got)
	}

	out, _, err = run(t, "42", "--yes", "-o", "name")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "cloud-source/42\n" {
		t.Errorf("-o name = %q, want cloud-source/42", out)
	}
}

func TestRotateChangeReviewRefusalExitsFour(t *testing.T) {
	mock := newTestEnv(t)
	serveIntake(mock, ptr(oldAddress))
	serveSenders(mock, http.StatusOK)
	serveRotate(mock, http.StatusForbidden, map[string]any{"detail": map[string]any{
		"msg":        "This table's change review policy does not let the member role resolve this change.",
		"error_code": "CHANGE_REVIEW_POLICY_DENIED",
		"context":    map[string]any{"operation": "email_intake_rotate", "verb": "resolve", "denied_keys": []any{[]any{"address"}}},
	}})

	_, _, err := run(t, "42", "--yes")
	assertExit(t, err, qerrors.ExitUnauthorized)
}

func TestRotateRejectsABadIDBeforeAnyRequest(t *testing.T) {
	mock := newTestEnv(t)
	_, _, err := run(t, "abc", "--yes")
	assertExit(t, err, qerrors.ExitBadUsage)
	assertRequests(t, mock)
}
