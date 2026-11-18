package inspect

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const compositeDryRunExamplesPath = "/composite-dry-runs/5512/examples"

// compositeExamplesBody carries a BigInteger join key: past 2^53, so any
// float64 round trip changes its last digits.
const compositeExamplesBody = `{"results":[{"id":1024,"composite_dry_run_job_id":5512,"spine_dataset_id":11,` +
	`"spine_row_id":7821,"bucket":"blocked_zero_match","source_alias":"claims",` +
	`"join_values":{"policy_id":12345678901234567890},"matched_upstream_row_ids":{"claims":[]},` +
	`"quarantined_upstream_row_ids":{"claims":[]},"created_at":"2026-09-22T10:01:34Z"}],` +
	`"page":1,"total_rows":59,"next":{"start":null,"end":null}}`

// runInspectCompositeDryRunJob drives the command against a mock server that
// answers the examples route with status and body. It returns stdout and
// stderr separately and the query string the client sent.
func runInspectCompositeDryRunJob(t *testing.T, status int, body string, args ...string) (string, string, url.Values, error) {
	t.Helper()

	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	var sent url.Values
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+compositeDryRunExamplesPath, func(w http.ResponseWriter, r *http.Request) {
		sent = r.URL.Query()
		if status != http.StatusOK {
			testutil.RespondError(w, status, body)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	})

	cmd := setupInspectTestCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs(append([]string{"inspect", "composite-dry-run-job", "5512"}, args...))

	err := cmd.Execute()
	return stdout.String(), stderr.String(), sent, err
}

// TestInspectCompositeDryRunJob_PrintsTheBodyAsSent: join values are data
// cells, so the BigInteger key must print with every digit in both formats.
func TestInspectCompositeDryRunJob_PrintsTheBodyAsSent(t *testing.T) {
	for _, tc := range []struct {
		format string
		args   []string
		want   string
	}{
		{format: "json (default)", want: `"policy_id": 12345678901234567890`},
		{format: "yaml", args: []string{"-o", "yaml"}, want: "policy_id: 12345678901234567890\n"},
	} {
		out, _, _, err := runInspectCompositeDryRunJob(t, http.StatusOK, compositeExamplesBody, tc.args...)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", tc.format, err)
		}
		if !strings.Contains(out, tc.want) {
			t.Errorf("%s: output should contain %q, got:\n%s", tc.format, tc.want, out)
		}
		if !strings.Contains(out, "blocked_zero_match") {
			t.Errorf("%s: output lost the bucket:\n%s", tc.format, out)
		}
	}
}

func TestInspectCompositeDryRunJob_SendsItsFilters(t *testing.T) {
	_, _, sent, err := runInspectCompositeDryRunJob(t, http.StatusOK, compositeExamplesBody,
		"--bucket", "blocked_zero_match", "--source", "claims", "--page", "3", "--limit", "5")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for param, want := range map[string]string{
		"bucket":       "blocked_zero_match",
		"source_alias": "claims",
		"page":         "3",
		"limit":        "5",
	} {
		if got := sent.Get(param); got != want {
			t.Errorf("%s = %q, want %q", param, got, want)
		}
	}
}

// TestInspectCompositeDryRunJob_MoreAvailableHint: the hint goes to stderr, so
// stdout stays one parseable document. It is printed when total_rows indicates
// more rows exist, or when the server sends next and total_rows is absent.
func TestInspectCompositeDryRunJob_MoreAvailableHint(t *testing.T) {
	bodyWithoutNext := strings.Replace(compositeExamplesBody, `,"next":{"start":null,"end":null}`, "", 1)
	if bodyWithoutNext == compositeExamplesBody {
		t.Fatal("fixture no longer carries next; test cases would pass vacuously")
	}

	bodyWithoutTotal := strings.Replace(compositeExamplesBody, `,"total_rows":59`, "", 1)
	if bodyWithoutTotal == compositeExamplesBody {
		t.Fatal("fixture no longer carries total_rows; test cases would pass vacuously")
	}

	bodyWithoutTotalOrNext := strings.Replace(bodyWithoutTotal, `,"next":{"start":null,"end":null}`, "", 1)
	if bodyWithoutTotalOrNext == bodyWithoutTotal {
		t.Fatal("fixture no longer carries next; test cases would pass vacuously")
	}

	for _, tc := range []struct {
		name         string
		body         string
		page         int
		wantHint     bool
		wantNextPage int
	}{
		{
			name:         "page 2 with total_rows, no next: total indicates more exist",
			body:         bodyWithoutNext,
			page:         2,
			wantHint:     true,
			wantNextPage: 3,
		},
		{
			name:     "page 3 with total_rows, no next: total indicates no more (60 >= 59)",
			body:     bodyWithoutNext,
			page:     3,
			wantHint: false,
		},
		{
			name:     "no total_rows, no next: cannot decide, no hint",
			body:     bodyWithoutTotalOrNext,
			page:     2,
			wantHint: false,
		},
		{
			name:         "no total_rows but next present: fallback to next",
			body:         bodyWithoutTotal,
			page:         1,
			wantHint:     true,
			wantNextPage: 2,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, errOut, _, err := runInspectCompositeDryRunJob(t, http.StatusOK, tc.body, "--page", fmt.Sprint(tc.page))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantHint {
				if !strings.Contains(errOut, fmt.Sprintf("use --page %d", tc.wantNextPage)) {
					t.Errorf("stderr should carry hint for page %d, got: %q", tc.wantNextPage, errOut)
				}
				if strings.Contains(out, "More available") {
					t.Errorf("the hint leaked into stdout:\n%s", out)
				}
			} else if strings.Contains(errOut, "More available") {
				t.Errorf("hint should not appear, got: %q", errOut)
			}
		})
	}
}

func TestInspectCompositeDryRunJob_BadUsageExitsTwo(t *testing.T) {
	cases := map[string][]string{
		"zero page":      {"--page", "0"},
		"negative limit": {"--limit", "-1"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			_, _, sent, err := runInspectCompositeDryRunJob(t, http.StatusOK, compositeExamplesBody, args...)
			if err == nil {
				t.Fatalf("expected an error for %v", args)
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d; err = %v", got, qerrors.ExitBadUsage, err)
			}
			if sent != nil {
				t.Errorf("a request escaped validation: %v", sent)
			}
		})
	}
}

// TestInspectCompositeDryRunJob_NotFoundExitsThree: a missing job and another
// organization's job are the same 404 (get_composite_dry_run_examples,
// capi/capi/views/composite_dry_run_view.py).
func TestInspectCompositeDryRunJob_NotFoundExitsThree(t *testing.T) {
	_, _, _, err := runInspectCompositeDryRunJob(t, http.StatusNotFound,
		"composite dry run job 5512 not found in this organization.")
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitNotFound {
		t.Errorf("exit code = %d, want %d; err = %v", got, qerrors.ExitNotFound, err)
	}
}
