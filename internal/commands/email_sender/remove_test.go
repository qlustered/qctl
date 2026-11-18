package email_sender //nolint:revive // Package name mirrors the command.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const rmTestOrgID = "d4e5f6a7-b8c9-0123-def0-456789012345"

const rmSendersPath = "/api/orgs/" + rmTestOrgID + "/data-sources/42/email-intake/senders"

var (
	rmAnaID = uuid.MustParse("0190eeee-0000-4000-8000-000000000001")
	rmBobID = uuid.MustParse("0190eeee-0000-4000-8000-000000000002")
)

var rmSendersBody = []byte(`[` +
	`{"user_id":"` + rmAnaID.String() + `","email":"ana@example.com","first_name":"Ana","last_name":"Lee",` +
	`"email_delivery_status":{"status":"ok"}},` +
	`{"user_id":"` + rmBobID.String() + `","email":"bob@example.com","first_name":"Bob","last_name":"Ray",` +
	`"email_delivery_status":{"status":"suppressed","reason":"hard_bounce","since":"2026-11-01T10:00:00Z"}}]`)

// rmTestRoot wires what root.go wires for the exit codes asserted here: flag
// errors and a missing required flag exit 2, and kind/ prefixes are stripped
// from positionals.
func rmTestRoot() *cobra.Command {
	rootCmd := &cobra.Command{Use: "qctl", SilenceUsage: true, SilenceErrors: true}
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "Allow http://")
	rootCmd.PersistentFlags().CountP("verbose", "v", "Verbosity level")
	rootCmd.SetFlagErrorFunc(cmdutil.FlagError)
	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		if err := cmdutil.ValidateOutputFormat(cmd); err != nil {
			return err
		}
		if err := cmd.ValidateRequiredFlags(); err != nil {
			return cmdutil.RequiredFlagsError(cmd, err)
		}
		return nil
	}
	rootCmd.AddCommand(NewCommand())
	cmdutil.TypeArgsErrors(rootCmd)
	cmdutil.StripKindRefs(rootCmd)
	return rootCmd
}

func rmTestEnv(t *testing.T) *testutil.MockAPIServer {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", rmTestOrgID)
	env.SetupCredential(endpointKey, rmTestOrgID, "test-token")
	return mock
}

func rmRunCmd(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := rmTestRoot()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(append([]string{"email-sender"}, args...))
	err := cmd.Execute()
	return out.String(), errOut.String(), err
}

func rmServe(mock *testutil.MockAPIServer, method, path string, code int, body []byte) {
	mock.RegisterHandler(method, path, func(w http.ResponseWriter, _ *http.Request) {
		if body == nil {
			w.WriteHeader(code)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = w.Write(body)
	})
}

func rmAssertExit(t *testing.T, err error, want int) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error exiting %d, got nil", want)
	}
	if got := qerrors.GetExitCode(err); got != want {
		t.Errorf("exit code = %d, want %d (err: %v)", got, want, err)
	}
}

func rmAssertRequests(t *testing.T, mock *testutil.MockAPIServer, want ...string) {
	t.Helper()
	got := mock.Requests()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %q, want %q", got, want)
	}
}

// rmUsageCases are refusals both sender verbs make before any request.
var rmUsageCases = map[string][]string{
	"no cloud source":         {"ana@example.com"},
	"zero cloud source":       {"ana@example.com", "--cloud-source-id", "0"},
	"negative cloud source":   {"ana@example.com", "--cloud-source-id", "-3"},
	"non-numeric source":      {"ana@example.com", "--cloud-source-id", "abc"},
	"wrong kind cloud source": {"ana@example.com", "--cloud-source-id", "table/42"},
	"wrong kind sender":       {"table/42", "--cloud-source-id", "42"},
	"malformed user id":       {"not-a-uuid", "--cloud-source-id", "42"},
	"no sender":               {"--cloud-source-id", "42"},
	"two senders":             {"ana@example.com", "bob@example.com", "--cloud-source-id", "42"},
}

func TestRemoveUsageErrorsMakeNoRequest(t *testing.T) {
	for name, args := range rmUsageCases {
		t.Run(name, func(t *testing.T) {
			mock := rmTestEnv(t)
			_, _, err := rmRunCmd(t, append([]string{"remove", "--yes"}, args...)...)
			rmAssertExit(t, err, qerrors.ExitBadUsage)
			rmAssertRequests(t, mock)
		})
	}
}

func TestRemoveResolvesTheEmailCaseInsensitivelyAndDeletesByUserID(t *testing.T) {
	mock := rmTestEnv(t)
	rmServe(mock, http.MethodGet, rmSendersPath, http.StatusOK, rmSendersBody)
	rmServe(mock, http.MethodDelete, rmSendersPath+"/"+rmBobID.String(), http.StatusNoContent, nil)

	out, _, err := rmRunCmd(t, "remove", "Bob@Example.COM", "--cloud-source-id", "cloud-source/42", "--yes")
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	rmAssertRequests(t, mock, "GET "+rmSendersPath, "DELETE "+rmSendersPath+"/"+rmBobID.String())
	for _, want := range []string{"email-sender/" + rmBobID.String(), "bob@example.com", "cloud-source/42", "kept"} {
		if !strings.Contains(out, want) {
			t.Errorf("output %q lacks %q", out, want)
		}
	}
}

func TestRemoveMachineOutputs(t *testing.T) {
	cases := map[string]func(t *testing.T, out string){
		"name": func(t *testing.T, out string) {
			if out != "email-sender/"+rmAnaID.String()+"\n" {
				t.Errorf("output = %q", out)
			}
		},
		"json": func(t *testing.T, out string) {
			var got map[string]any
			if err := json.Unmarshal([]byte(out), &got); err != nil {
				t.Fatalf("output is not JSON: %v\n%s", err, out)
			}
			want := map[string]any{"result": "removed", "cloud_source_id": float64(42),
				"user_id": rmAnaID.String(), "email": "ana@example.com"}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("output = %v, want %v", got, want)
			}
		},
		"yaml": func(t *testing.T, out string) {
			if !strings.Contains(out, "result: removed") || !strings.Contains(out, "cloud_source_id: 42") {
				t.Errorf("output = %q", out)
			}
		},
	}
	for format, check := range cases {
		t.Run(format, func(t *testing.T) {
			mock := rmTestEnv(t)
			rmServe(mock, http.MethodGet, rmSendersPath, http.StatusOK, rmSendersBody)
			rmServe(mock, http.MethodDelete, rmSendersPath+"/"+rmAnaID.String(), http.StatusNoContent, nil)
			out, _, err := rmRunCmd(t, "remove", "email-sender/"+rmAnaID.String(), "--cloud-source-id", "42", "--yes", "-o", format)
			if err != nil {
				t.Fatalf("remove: %v", err)
			}
			check(t, out)
		})
	}
}

func TestRemoveUnassignedSenderExitsThreeWithoutDeleting(t *testing.T) {
	for name, arg := range map[string]string{
		"email":   "carol@example.com",
		"user id": "0190eeee-0000-4000-8000-000000000009",
	} {
		t.Run(name, func(t *testing.T) {
			mock := rmTestEnv(t)
			rmServe(mock, http.MethodGet, rmSendersPath, http.StatusOK, rmSendersBody)
			_, _, err := rmRunCmd(t, "remove", arg, "--cloud-source-id", "42", "--yes")
			rmAssertExit(t, err, qerrors.ExitNotFound)
			rmAssertRequests(t, mock, "GET "+rmSendersPath)
		})
	}
}

func TestRemovePromptNamesTheSenderAndTheCloudSource(t *testing.T) {
	t.Cleanup(cmdutil.ResetReader)
	for answer, wantDelete := range map[string]bool{"n\n": false, "y\n": true} {
		t.Run(strings.TrimSpace(answer), func(t *testing.T) {
			mock := rmTestEnv(t)
			rmServe(mock, http.MethodGet, rmSendersPath, http.StatusOK, rmSendersBody)
			rmServe(mock, http.MethodDelete, rmSendersPath+"/"+rmAnaID.String(), http.StatusNoContent, nil)
			cmdutil.SetReader(strings.NewReader(answer))

			_, stderr, err := rmRunCmd(t, "remove", "ana@example.com", "--cloud-source-id", "42")
			for _, want := range []string{"ana@example.com", "cloud-source/42", "kept"} {
				if !strings.Contains(stderr, want) {
					t.Errorf("prompt %q lacks %q", stderr, want)
				}
			}
			if wantDelete {
				if err != nil {
					t.Fatalf("remove: %v", err)
				}
				rmAssertRequests(t, mock, "GET "+rmSendersPath, "DELETE "+rmSendersPath+"/"+rmAnaID.String())
				return
			}
			if err == nil {
				t.Fatal("a declined prompt must fail")
			}
			rmAssertRequests(t, mock, "GET "+rmSendersPath)
		})
	}
}

func TestRemoveWithoutYesOffATerminalExitsTwoWithoutDeleting(t *testing.T) {
	mock := rmTestEnv(t)
	rmServe(mock, http.MethodGet, rmSendersPath, http.StatusOK, rmSendersBody)
	_, _, err := rmRunCmd(t, "remove", "ana@example.com", "--cloud-source-id", "42")
	rmAssertExit(t, err, qerrors.ExitBadUsage)
	rmAssertRequests(t, mock, "GET "+rmSendersPath)
}

func TestRemoveRefusalExitCodes(t *testing.T) {
	cases := map[string]struct {
		code int
		body string
		want int
	}{
		"not assigned": {http.StatusNotFound, `{"detail":{"msg":"Intake sender not found."}}`, qerrors.ExitNotFound},
		"forbidden":    {http.StatusForbidden, `{"detail":{"msg":"Forbidden."}}`, qerrors.ExitUnauthorized},
		"not a broker": {http.StatusBadRequest, `{"detail":{"msg":"Email intake requires a portal data source in a table series."}}`, qerrors.ExitBadUsage},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			mock := rmTestEnv(t)
			rmServe(mock, http.MethodGet, rmSendersPath, http.StatusOK, rmSendersBody)
			rmServe(mock, http.MethodDelete, rmSendersPath+"/"+rmAnaID.String(), tc.code, []byte(tc.body))
			_, _, err := rmRunCmd(t, "remove", rmAnaID.String(), "--cloud-source-id", "42", "--yes")
			rmAssertExit(t, err, tc.want)
		})
	}
}
