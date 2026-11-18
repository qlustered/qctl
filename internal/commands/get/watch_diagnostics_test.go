package get

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/client"
	"github.com/qlustered/qctl/internal/ingestion"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestWatchPollRoutesSuccessfulHTTPRetriesAndVerboseLogs(t *testing.T) {
	for _, tc := range []struct {
		format    output.Format
		tty       bool
		verbosity int
	}{
		{output.FormatTable, true, 0},
		{output.FormatTable, false, 0},
		{output.FormatJSON, false, 0},
		{output.FormatYAML, false, 0},
		{output.FormatName, false, 0},
		{output.FormatTable, true, client.VerbosityStructured},
		{output.FormatTable, true, client.VerbosityCurlRedacted},
		{output.FormatTable, true, client.VerbosityCurlFull},
	} {
		t.Run(fmt.Sprintf("%s/tty=%t/verbosity=%d", tc.format, tc.tty, tc.verbosity), func(t *testing.T) {
			client.SetRequestPolicy(client.RequestPolicy{Retries: 1})
			client.SetRetryWait(func(context.Context, time.Duration) error { return nil })
			var fallback bytes.Buffer
			client.SetRetryOutput(&fallback)
			t.Cleanup(func() {
				client.ResetRequestPolicy()
				client.ResetRetryWait()
				client.ResetRetryOutput()
			})
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				i := requests.Add(1)
				if i == 2 {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				state := "pending"
				if i == 4 {
					state = "running"
				}
				if i == 5 {
					state = "finished"
				}
				testutil.RespondJSON(w, http.StatusOK, map[string]any{
					"results": []any{map[string]any{"id": 1, "state": state}},
				})
			}))
			defer server.Close()
			c, err := ingestion.NewClient(server.URL, testOrgID, tc.verbosity)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var out, errOut bytes.Buffer
			notices := &errOut
			if tc.tty {
				notices = &out
			}
			printer := output.NewPrinter(output.Options{
				Format: tc.format, Writer: &out, Columns: []string{"id", "state"}, NameKind: "ingestion-job",
			})
			polls := 0
			err = watchPoll(ctx, printer.NewStream(tc.tty, true), notices, time.Millisecond, func(ctx context.Context) (any, error) {
				polls++
				if polls == 5 {
					cancel()
					return nil, nil
				}
				page, err := c.GetIngestionJobsContext(ctx, "test-token", ingestion.GetIngestionJobsParams{})
				if err != nil {
					return nil, err
				}
				return output.NewList(page.Results, nil, nil, nil, nil), nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if requests.Load() != 5 || polls != 5 || fallback.Len() != 0 {
				t.Fatalf("requests=%d polls=%d fallback=%q", requests.Load(), polls, fallback.String())
			}
			if got := notices.String(); strings.Count(got, "retrying in 1s (attempt 2 of 2)") != 1 || strings.Contains(got, "poll failed") {
				t.Errorf("internally successful retry diagnostics = %q", got)
			}
			if tc.verbosity > 0 {
				requestMarker := "GET"
				if tc.verbosity >= client.VerbosityCurlRedacted {
					requestMarker = "curl"
				}
				for _, want := range []string{requestMarker, "503 Service Unavailable", "200 OK"} {
					if !strings.Contains(out.String(), want) {
						t.Errorf("missing %q in %q", want, out.String())
					}
				}
				if tc.verbosity < client.VerbosityCurlFull && strings.Contains(out.String(), "test-token") {
					t.Errorf("redacted diagnostics leaked token: %q", out.String())
				}
				if strings.Contains(out.String(), "\033[") {
					t.Errorf("redraw crossed verbose logs: %q", out.String())
				}
				if strings.Count(out.String(), "ID  STATE\n") != 3 {
					t.Errorf("verbose notices changed snapshot suppression: %q", out.String())
				}
				return
			}
			if tc.format == output.FormatTable {
				want := "ID  STATE\n1   pending\n"
				if tc.tty {
					want += "  server answered 503 Service Unavailable; retrying in 1s (attempt 2 of 2)\n"
				}
				want += "ID  STATE\n1   running\n"
				if tc.tty {
					want += "\033[2A\033[J"
				}
				want += "ID  STATE\n1   finished\n"
				if out.String() != want {
					t.Errorf("output = %q, want %q", out.String(), want)
				}
			} else {
				marker := "\n"
				if tc.format == output.FormatYAML {
					marker = "---\n"
				}
				if strings.Count(out.String(), marker) != 3 || strings.Contains(out.String(), "retrying") {
					t.Errorf("stream must have three snapshots and no diagnostics: %q", out.String())
				}
			}
		})
	}
}
