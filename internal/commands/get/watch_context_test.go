package get

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/client"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestListWatchCancelsAnInFlightFirstPoll(t *testing.T) {
	for _, c := range lwListCommands() {
		t.Run(c.name, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()
			mock := testutil.NewMockAPIServer()
			defer mock.Close()
			key, _ := config.NormalizeEndpointKey(mock.Server.URL)
			env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
			env.SetupCredential(key, testOrgID, "test-token")
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/5", func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, map[string]any{"id": 5, "name": "orders"})
			})
			mock.RegisterHandler("GET", c.endpoint, func(w http.ResponseWriter, r *http.Request) {
				testutil.WaitForRequestCancellation(t, r)
				testutil.RespondJSON(w, http.StatusOK, map[string]any{"results": []any{}, "total_rows": 0, "page": 1})
			})
			cmd := c.build()
			var out, errOut bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&errOut)
			cmd.SetArgs(append(append([]string{"get", c.name}, c.args...), "--watch", "--timeout", "50ms", "-o", "json"))
			if err := cmd.Execute(); err != nil {
				t.Fatalf("watch = %v, want a clean timeout", err)
			}
			if out.Len() != 0 || !strings.Contains(errOut.String(), "timed out after 50ms") {
				t.Errorf("stdout = %q, stderr = %q; want no late page and a timeout", out.String(), errOut.String())
			}
		})
	}
}

func TestLatestAlertCancelsAnInFlightFirstPoll(t *testing.T) {
	for _, exitOnChange := range []bool{false, true} {
		t.Run(map[bool]string{false: "stream", true: "exit-on-change"}[exitOnChange], func(t *testing.T) {
			args := []string{"--table-id", "42", "--watch", "--timeout", "50ms", "-o", "json"}
			if exitOnChange {
				args = append(args, "--exit-on-change")
			}
			out, _, err := jawRunLatestAlert(t, func(w http.ResponseWriter, r *http.Request) {
				testutil.WaitForRequestCancellation(t, r)
				jawServeMenuStats(w, r)
			}, args...)
			want := 0
			if exitOnChange {
				want = qerrors.ExitTimeout
			}
			if code := qerrors.GetExitCode(err); code != want || out != "" {
				t.Errorf("exit = %d (%v), stdout = %q; want %d and no late snapshot", code, err, out, want)
			}
		})
	}
}

func TestWatchPollDiscardsASuccessAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var out bytes.Buffer
	stream := output.NewPrinter(output.Options{Format: output.FormatJSON, Writer: &out}).NewStream(false, true)
	polls := 0
	err := watchPoll(ctx, stream, &bytes.Buffer{}, time.Millisecond, func(context.Context) (any, error) {
		polls++
		if polls == 2 {
			cancel()
		}
		return output.NewList([]watchJob{{ID: 1, State: "queued"}}, nil, nil, nil, nil), nil
	})
	if err != nil || polls != 2 || strings.Count(out.String(), "\n") != 1 {
		t.Errorf("watch = %v, polls = %d, stdout = %q; want only the baseline", err, polls, out.String())
	}
}

func TestListWatchCancelsARetryWait(t *testing.T) {
	client.SetRequestPolicy(client.RequestPolicy{Retries: 3})
	t.Cleanup(client.ResetRequestPolicy)
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	key, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(key, testOrgID, "test-token")
	polls := 0
	mock.RegisterHandler("GET", lwListCommands()[0].endpoint, func(w http.ResponseWriter, _ *http.Request) {
		polls++
		w.Header().Set("Retry-After", "1")
		testutil.RespondError(w, http.StatusTooManyRequests, "slow down")
	})
	cmd := setupTestCommandForIngestion()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"get", "ingestion-jobs", "--watch", "--timeout", "50ms"})
	start := time.Now()
	if err := cmd.Execute(); err != nil {
		t.Fatalf("watch = %v, want clean timeout", err)
	}
	if polls != 1 || time.Since(start) > 5*time.Second {
		t.Errorf("polls = %d, elapsed = %s; retry wait outlived watch budget", polls, time.Since(start))
	}
}
