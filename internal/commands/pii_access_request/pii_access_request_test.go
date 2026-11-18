package pii_access_request //nolint:revive // Package name mirrors the command.

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const testOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"
const requestsPath = "/api/orgs/" + testOrgID + "/datasets/42/pii-access-requests"
const requestJSON = `{"id":7,"dataset_id":42,"state":"open","requested_duration":"8h","reason":"audit","created_at":"2026-11-04T12:00:00Z","expires_at":"2026-11-11T12:00:00Z","deny_reason":null,"grant_expires_at":null}`

func setupTestCommand() *cobra.Command {
	rootCmd := &cobra.Command{Use: "qctl", SilenceUsage: true, SilenceErrors: true}
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format")
	rootCmd.PersistentFlags().Bool("no-headers", false, "Don't print column headers")
	rootCmd.PersistentFlags().String("columns", "", "Columns to display")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "Maximum width for table columns")
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
	cmd.SetArgs(append([]string{"pii-access-request"}, args...))
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

func TestRequestAndWithdrawProviders(t *testing.T) {
	for _, shared := range []bool{false, true} {
		for _, verb := range []string{"request", "ask", "withdraw"} {
			t.Run(verb+"/shared="+fmtBool(shared), func(t *testing.T) {
				mock := newTestEnv(t)
				path, code, state := requestsPath, http.StatusCreated, "open"
				if shared {
					path = strings.Replace(path, "/datasets/", "/third-party/datasets/", 1)
				}
				if verb == "withdraw" {
					path += "/7/withdraw"
					code = http.StatusOK
					state = "withdrawn"
				}
				mock.RegisterHandler("POST", path, func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get("Authorization") != "Bearer test-token" {
						t.Error("missing bearer token")
					}
					if verb != "withdraw" {
						var body map[string]any
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
							t.Error(err)
						}
						if !reflect.DeepEqual(body, map[string]any{"duration": "8h", "reason": "audit"}) {
							t.Errorf("body = %v", body)
						}
					}
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(code)
					_, _ = w.Write([]byte(strings.Replace(requestJSON, `"open"`, `"`+state+`"`, 1)))
				})
				args := []string{verb, "--table-id", "42", "--yes"}
				if verb == "withdraw" {
					args = append(args, "pii-access-request/7")
				} else {
					args = append(args, "--duration", "8H", "--reason", "  audit  ")
				}
				if shared {
					args = append(args, "--shared")
				}
				out, _, err := run(t, args...)
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(out, "pii-access-request/7 "+state) {
					t.Errorf("output = %q", out)
				}
				if shared && !strings.Contains(out, "--shared") {
					t.Errorf("missing provider in next command: %q", out)
				}
				assertRequests(t, mock, "POST "+path)
			})
		}
	}
}

func fmtBool(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func TestUsageErrorsMakeNoRequests(t *testing.T) {
	cases := [][]string{
		{"request", "--reason", "r", "--yes"},
		{"request", "--table-id", "42", "--yes"},
		{"request", "--table-id", "42", "--reason", "  ", "--yes"},
		{"request", "--table-id", "42", "--reason", strings.Repeat("x", 4001), "--yes"},
		{"request", "--table-id", "42", "--reason", "r", "--duration", "2h", "--yes"},
		{"request", "--table-id", "42", "--table", "orders", "--reason", "r", "--yes"},
		{"withdraw", "0", "--table-id", "42", "--yes"},
		{"withdraw", "-1", "--table-id", "42", "--yes"},
		{"withdraw", "abc", "--table-id", "42", "--yes"},
		{"withdraw", "table/7", "--table-id", "42", "--yes"},
		{"withdraw", "7", "--yes"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args[:1], " ")+strings.Join(args[1:2], ""), func(t *testing.T) {
			mock := newTestEnv(t)
			_, _, err := run(t, args...)
			assertExitCode(t, err, 2)
			assertRequests(t, mock)
		})
	}
}

func TestRequestRefusalsPreserveCodeAndRetryAfter(t *testing.T) {
	for _, status := range []int{400, 403, 404, 409, 422, 429, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			mock := newTestEnv(t)
			mock.RegisterHandler("POST", requestsPath, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", "3600")
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"detail":{"msg":"server refusal"}}`))
			})
			_, _, err := run(t, "ask", "--table-id", "42", "--reason", "r", "--yes")
			want := map[int]int{400: 2, 403: 4, 404: 3, 409: 2, 422: 2, 429: 1, 500: 1}[status]
			assertExitCode(t, err, want)
			if !strings.Contains(err.Error(), "server refusal") {
				t.Errorf("lost message: %v", err)
			}
			if status == 429 {
				var throttle *apierror.ThrottleError
				if !errors.As(err, &throttle) || throttle.RetryAfter != time.Hour {
					t.Errorf("lost Retry-After: %v", err)
				}
				if !strings.Contains(err.Error(), "1h0m0s") {
					t.Errorf("missing delay: %v", err)
				}
			}
			assertRequests(t, mock, "POST "+requestsPath)
		})
	}
}

func TestMutationsRequireConfirmation(t *testing.T) {
	for _, args := range [][]string{{"ask", "--reason", "audit"}, {"withdraw", "7"}} {
		mock := newTestEnv(t)
		_, _, err := run(t, append(args, "--table-id", "42")...)
		assertExitCode(t, err, 2)
		assertRequests(t, mock)
	}
	mock := newTestEnv(t)
	cmdutil.SetReader(strings.NewReader("n\n"))
	defer cmdutil.ResetReader()
	_, stderr, err := run(t, "ask", "--table-id", "42", "--reason", "audit")
	if !strings.Contains(stderr, "Ask for unmasked access to table/42") {
		t.Errorf("prompt = %q", stderr)
	}
	if !errors.Is(err, cmdutil.ErrDeclined) {
		t.Errorf("err = %v", err)
	}
	assertRequests(t, mock)
}

func TestMutationFormatsAndDefaultDuration(t *testing.T) {
	for _, format := range []string{"json", "yaml", "name"} {
		t.Run(format, func(t *testing.T) {
			mock := newTestEnv(t)
			mock.RegisterHandler("POST", requestsPath, func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body["duration"] != "1h" {
					t.Errorf("default duration = %v", body["duration"])
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(201)
				_, _ = w.Write([]byte(requestJSON))
			})
			out, _, err := run(t, "ask", "--table-id", "42", "--reason", "audit", "--yes", "-o", format)
			if err != nil {
				t.Fatal(err)
			}
			if format == "name" {
				if out != "pii-access-request/7\n" {
					t.Errorf("name = %q", out)
				}
				return
			}
			if format == "json" {
				var value map[string]any
				if err := json.Unmarshal([]byte(out), &value); err != nil {
					t.Fatal(err)
				}
				if value["id"] != float64(7) || value["state"] != "open" {
					t.Errorf("json = %s", out)
				}
			} else if !strings.Contains(out, "state: open") {
				t.Errorf("yaml = %s", out)
			}
		})
	}
}

func TestWithdrawRefusals(t *testing.T) {
	for _, shared := range []bool{false, true} {
		for _, status := range []int{http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity} {
			t.Run(http.StatusText(status)+"/shared="+fmtBool(shared), func(t *testing.T) {
				mock := newTestEnv(t)
				path := requestsPath + "/7/withdraw"
				if shared {
					path = strings.Replace(path, "/datasets/", "/third-party/datasets/", 1)
				}
				mock.RegisterHandler("POST", path, func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(status)
					_, _ = w.Write([]byte(`{"detail":{"msg":"cannot withdraw"}}`))
				})
				args := []string{"withdraw", "7", "--table-id", "42", "--yes"}
				if shared {
					args = append(args, "--shared")
				}
				_, _, err := run(t, args...)
				want := 2
				if status == 404 {
					want = 3
				}
				assertExitCode(t, err, want)
				if !strings.Contains(err.Error(), "cannot withdraw") {
					t.Errorf("err = %v", err)
				}
				assertRequests(t, mock, "POST "+path)
			})
		}
	}
}

func TestSharedRequestResolvesTableNameInSharedListing(t *testing.T) {
	mock := newTestEnv(t)
	listing := "/api/orgs/" + testOrgID + "/third-party/datasets"
	mock.RegisterHandler("GET", listing, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("name") != "42" {
			t.Errorf("query = %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"id":42,"name":"42"}],"total_rows":1}`))
	})
	path := strings.Replace(requestsPath, "/datasets/", "/third-party/datasets/", 1)
	mock.RegisterHandler("POST", path, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(201)
		_, _ = w.Write([]byte(requestJSON))
	})
	_, _, err := run(t, "request", "--shared", "--table", "42", "--reason", "audit", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	assertRequests(t, mock, "GET "+listing, "POST "+path)
}
