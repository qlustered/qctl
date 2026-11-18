package get

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/spf13/cobra"
)

const emailSendersURL = "/api/orgs/" + testOrgID + "/data-sources/42/email-intake/senders"

const (
	emailSenderAna = "0190eeee-0000-4000-8000-000000000001"
	emailSenderBob = "0190eeee-0000-4000-8000-000000000002"
)

var emailSendersBody = []byte(`[` +
	`{"user_id":"` + emailSenderAna + `","email":"ana@example.com","first_name":"Ana","last_name":"Lee",` +
	`"email_delivery_status":{"status":"ok","reason":null,"since":null}},` +
	`{"user_id":"` + emailSenderBob + `","email":"bob@example.com","first_name":"Bob","last_name":"Ray",` +
	`"email_delivery_status":{"status":"suppressed","reason":"hard_bounce","since":"2026-11-01T10:00:00Z"}}]`)

// emailSendersRun runs get email-senders under a root that, like root.go,
// types arity and flag errors and a missing required flag as exit 2.
func (e *dataReviewGetEnv) emailSendersRun(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	root := setupTestCommandForTable(NewEmailSendersCommand())
	root.SilenceUsage, root.SilenceErrors = true, true
	root.SetFlagErrorFunc(cmdutil.FlagError)
	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		if err := cmdutil.ValidateOutputFormat(cmd); err != nil {
			return err
		}
		if err := cmdutil.ValidateDeclaredColumns(cmd); err != nil {
			return err
		}
		if err := cmd.ValidateRequiredFlags(); err != nil {
			return cmdutil.RequiredFlagsError(cmd, err)
		}
		return nil
	}
	cmdutil.TypeArgsErrors(root)
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(append([]string{"get", "email-senders"}, args...))
	err := root.Execute()
	return stdout.String(), stderr.String(), err
}

func emailSenderLines(s string) []string {
	var lines []string
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func TestGetEmailSendersTableInServerOrder(t *testing.T) {
	e := newDataReviewGetEnv(t)
	servePrivilege(e, emailSendersURL, http.StatusOK, emailSendersBody)

	out, _, err := e.emailSendersRun(t, "--cloud-source-id", "cloud-source/42")
	if err != nil {
		t.Fatalf("get email-senders: %v", err)
	}
	lines := emailSenderLines(out)
	if len(lines) != 3 {
		t.Fatalf("want a header and two rows, got:\n%s", out)
	}
	for _, want := range []string{"EMAIL", "FIRST-NAME", "LAST-NAME", "DELIVERY", "SINCE"} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("header %q lacks %q", lines[0], want)
		}
	}
	if strings.Contains(lines[0], "USER-ID") || strings.Contains(lines[0], "REASON") {
		t.Errorf("header %q shows an opt-in column", lines[0])
	}
	if !strings.Contains(lines[1], "ana@example.com") || !strings.Contains(lines[1], "ok") {
		t.Errorf("first row %q is not ana, ok", lines[1])
	}
	if !strings.Contains(lines[2], "bob@example.com") || !strings.Contains(lines[2], "suppressed") {
		t.Errorf("second row %q is not bob, suppressed", lines[2])
	}
}

func TestGetEmailSendersOptInColumns(t *testing.T) {
	e := newDataReviewGetEnv(t)
	servePrivilege(e, emailSendersURL, http.StatusOK, emailSendersBody)

	out, _, err := e.emailSendersRun(t, "--cloud-source-id", "42", "--columns", "email,user_id,reason")
	if err != nil {
		t.Fatalf("get email-senders: %v", err)
	}
	lines := emailSenderLines(out)
	if len(lines) != 3 {
		t.Fatalf("want a header and two rows, got:\n%s", out)
	}
	for _, want := range []string{"bob@example.com", emailSenderBob, "hard_bounce"} {
		if !strings.Contains(lines[2], want) {
			t.Errorf("row %q lacks %q", lines[2], want)
		}
	}
}

func TestGetEmailSendersNamePrintsEachRef(t *testing.T) {
	e := newDataReviewGetEnv(t)
	servePrivilege(e, emailSendersURL, http.StatusOK, emailSendersBody)
	out, _, err := e.emailSendersRun(t, "--cloud-source-id", "42", "-o", "name")
	if err != nil {
		t.Fatalf("get email-senders: %v", err)
	}
	if want := "email-sender/" + emailSenderAna + "\nemail-sender/" + emailSenderBob + "\n"; out != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}

func TestGetEmailSendersJSONIsAListOfTheWireRecords(t *testing.T) {
	e := newDataReviewGetEnv(t)
	servePrivilege(e, emailSendersURL, http.StatusOK, emailSendersBody)
	out, _, err := e.emailSendersRun(t, "--cloud-source-id", "42", "-o", "json")
	if err != nil {
		t.Fatalf("get email-senders: %v", err)
	}
	var list struct {
		Kind  string           `json:"kind"`
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if list.Kind != "List" || len(list.Items) != 2 {
		t.Fatalf("list = %+v", list)
	}
	status, _ := list.Items[1]["email_delivery_status"].(map[string]any)
	if list.Items[1]["user_id"] != emailSenderBob || status["reason"] != "hard_bounce" {
		t.Errorf("items = %v", list.Items)
	}
}

func TestGetEmailSendersEmpty(t *testing.T) {
	e := newDataReviewGetEnv(t)
	servePrivilege(e, emailSendersURL, http.StatusOK, []byte(`[]`))
	out, stderr, err := e.emailSendersRun(t, "--cloud-source-id", "42")
	if err != nil {
		t.Fatalf("get email-senders: %v", err)
	}
	if out != "" || !strings.Contains(stderr, "No email senders") {
		t.Errorf("stdout = %q, stderr = %q", out, stderr)
	}
}

func TestGetEmailSendersUsageErrorsMakeNoRequest(t *testing.T) {
	cases := map[string][]string{
		"no cloud source":       {},
		"zero cloud source":     {"--cloud-source-id", "0"},
		"negative cloud source": {"--cloud-source-id", "-1"},
		"wrong kind":            {"--cloud-source-id", "table/42"},
		"not a number":          {"--cloud-source-id", "abc"},
		"positional":            {"42", "--cloud-source-id", "42"},
		"unknown column":        {"--cloud-source-id", "42", "--columns", "bogus"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			e := newDataReviewGetEnv(t)
			_, _, err := e.emailSendersRun(t, args...)
			if err == nil {
				t.Fatal("expected an error")
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit = %d, want 2 (err: %v)", got, err)
			}
			if reqs := e.mock.Requests(); len(reqs) != 0 {
				t.Errorf("requests = %q, want none", reqs)
			}
		})
	}
}

func TestGetEmailSendersRefusalExitCodes(t *testing.T) {
	cases := map[string]struct {
		code int
		want int
	}{
		"not a broker source": {http.StatusBadRequest, qerrors.ExitBadUsage},
		"forbidden":           {http.StatusForbidden, qerrors.ExitUnauthorized},
		"no such source":      {http.StatusNotFound, qerrors.ExitNotFound},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := newDataReviewGetEnv(t)
			servePrivilege(e, emailSendersURL, tc.code, []byte(`{"detail":{"msg":"refused"}}`))
			_, _, err := e.emailSendersRun(t, "--cloud-source-id", "42")
			if err == nil {
				t.Fatal("expected an error")
			}
			if got := qerrors.GetExitCode(err); got != tc.want {
				t.Errorf("exit = %d, want %d (err: %v)", got, tc.want, err)
			}
		})
	}
}
