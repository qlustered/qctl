package root

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const waiverTestPrefix = "/api/orgs/" + scopeTestOrgID + "/data-sources/42/periods/"

const waivedStatusRecord = `{"data_source_model_id":42,"period_key":"2026-09","period_start":"2026-09-01","period_end":"2026-09-30",
 "dataset_id":314,"status":"waived","is_locked":false,"not_loaded_file":null,
 "declaration":{"id":5,"kind":"waived","declared_at":"2026-10-12T10:00:00Z","declared_by_id":"aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee","reason":"Broker closed"},
 "unmodelled":"kept"}`

const nilStatusRecord = `{"data_source_model_id":42,"period_key":"2026-09","period_start":"2026-09-01","period_end":"2026-09-30",
 "dataset_id":314,"status":"nil","is_locked":false,"not_loaded_file":null,
 "declaration":{"id":4,"kind":"nil","declared_at":"2026-10-11T10:00:00Z","declared_by_id":"aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee","reason":null}}`

// waiverMethod is the HTTP method of a waiver verb.
func waiverMethod(verb string) string {
	if verb == "waive" {
		return "POST"
	}
	return "DELETE"
}

// waiverTestServer serves the test organization with a credential and resets
// both verbs' flags around the test.
func waiverTestServer(t *testing.T) *testutil.MockAPIServer {
	t.Helper()
	env := testutil.NewTestEnv(t)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	endpoint, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", scopeTestOrgID)
	env.SetupCredential(endpoint, scopeTestOrgID, "waiver-token")
	resetWaiverFlags(t)
	return mock
}

func resetWaiverFlags(t *testing.T) {
	t.Helper()
	reset := func() {
		for _, verb := range []string{"waive", "unwaive"} {
			cmd, _, err := GetRootCmd().Find([]string{"submission-status", verb})
			if err != nil {
				t.Fatal(err)
			}
			names := []string{"cloud-source-id", "period"}
			if verb == "waive" {
				names = append(names, "reason")
			}
			for _, name := range names {
				f := cmd.Flags().Lookup(name)
				if err := f.Value.Set(f.DefValue); err != nil {
					t.Fatal(err)
				}
				f.Changed = false
			}
		}
	}
	reset()
	t.Cleanup(reset)
}

// withWaiverReason adds a --reason to a waive invocation that has none.
func withWaiverReason(args []string) []string {
	if len(args) == 0 || args[0] != "waive" || slices.Contains(args, "--reason") {
		return args
	}
	return append(slices.Clone(args), "--reason", "Broker closed")
}

func runWaiver(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	args = withWaiverReason(args)
	cmd := GetRootCmd()
	var out, stderr bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&stderr)
	cmd.SetArgs(append([]string{"submission-status"}, args...))
	t.Cleanup(func() {
		cmd.SetOut(nil)
		cmd.SetErr(nil)
		cmd.SetArgs(nil)
		resetPersistentFlag(t, cmd, "output", "table")
	})
	_, err := executeC()
	return out.String(), stderr.String(), err
}

func TestSubmissionStatusWaiverFormats(t *testing.T) {
	for _, tt := range []struct{ verb, record, status string }{
		{"waive", waivedStatusRecord, "waived"},
		{"unwaive", nilStatusRecord, "nil"},
	} {
		for _, format := range []string{"table", "json", "yaml"} {
			t.Run(tt.verb+"/"+format, func(t *testing.T) {
				mock := waiverTestServer(t)
				path := waiverTestPrefix + "2026-09/waive"
				mock.RegisterHandler(waiverMethod(tt.verb), path, func(w http.ResponseWriter, r *http.Request) {
					if got := r.Header.Get("Authorization"); got != "Bearer waiver-token" {
						t.Errorf("Authorization = %q", got)
					}
					testutil.RespondJSON(w, http.StatusOK, json.RawMessage(tt.record))
				})
				out, stderr, err := runWaiver(t, tt.verb, "--cloud-source-id", "cloud-source/42", "--period", "2026-09", "-o", format)
				if err != nil {
					t.Fatal(err)
				}
				if got, want := mock.Requests(), []string{waiverMethod(tt.verb) + " " + path}; !reflect.DeepEqual(got, want) {
					t.Errorf("requests = %v, want %v", got, want)
				}
				line := `cloud-source/42 period "2026-09" (2026-09-01 to 2026-09-30): ` + tt.status + ", declared "
				switch format {
				case "table":
					if !strings.HasPrefix(out, line) || !strings.HasSuffix(out, ", table 314\n") || stderr != "" {
						t.Errorf("stdout = %q, stderr = %q", out, stderr)
					}
				case "json":
					assertSeriesJSON(t, out, tt.record)
					if !strings.HasPrefix(stderr, line) {
						t.Errorf("stderr = %q", stderr)
					}
				default:
					testutil.AssertYAMLReadsAsJSON(t, out, tt.record)
					if !strings.HasPrefix(stderr, line) {
						t.Errorf("stderr = %q", stderr)
					}
				}
			})
		}
	}
}

// The raw body is printed, so a field qctl does not model survives.
func TestSubmissionStatusWaiveJSONKeepsUnmodelledFields(t *testing.T) {
	mock := waiverTestServer(t)
	mock.RegisterHandler("POST", waiverTestPrefix+"2026-09/waive", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(waivedStatusRecord))
	})
	out, _, err := runWaiver(t, "waive", "--cloud-source-id", "42", "--period", "2026-09", "-o", "json")
	if err != nil || !strings.Contains(out, `"unmodelled": "kept"`) {
		t.Fatalf("stdout = %q, error = %v", out, err)
	}
}

func TestSubmissionStatusUnwaiveNoLongerExpected(t *testing.T) {
	for _, format := range []string{"table", "json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			mock := waiverTestServer(t)
			mock.RegisterHandler("DELETE", waiverTestPrefix+"2026-09/waive", func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			})
			out, stderr, err := runWaiver(t, "unwaive", "--cloud-source-id", "42", "--period", "2026-09", "-o", format)
			if qerrors.GetExitCode(err) != 0 {
				t.Fatalf("exit = %d, error = %v", qerrors.GetExitCode(err), err)
			}
			text, other := out, stderr
			if format != "table" {
				text, other = stderr, out
			}
			if !strings.Contains(text, "no longer expected") || !strings.Contains(text, `"2026-09"`) || other != "" {
				t.Errorf("stdout = %q, stderr = %q", out, stderr)
			}
		})
	}
}

// The key goes to the server as typed, as one path segment: no case folding,
// and a slash escaped rather than read as a separator.
func TestSubmissionStatusWaiverSendsThePeriodAsTyped(t *testing.T) {
	for _, verb := range []string{"waive", "unwaive"} {
		for _, tt := range []struct{ period, escaped string }{
			{"2026-q3", "2026-q3"}, {"FY/Z", "FY%2FZ"}, {"Week 1", "Week%201"},
		} {
			t.Run(verb+"/"+tt.period, func(t *testing.T) {
				mock := waiverTestServer(t)
				var escaped string
				mock.RegisterHandler(waiverMethod(verb), waiverTestPrefix+tt.period+"/waive", func(w http.ResponseWriter, r *http.Request) {
					escaped = r.URL.EscapedPath()
					testutil.RespondJSON(w, http.StatusOK, json.RawMessage(waivedStatusRecord))
				})
				if _, _, err := runWaiver(t, verb, "--cloud-source-id", "42", "--period", tt.period); err != nil {
					t.Fatal(err)
				}
				if want := waiverTestPrefix + tt.escaped + "/waive"; escaped != want {
					t.Errorf("escaped path = %q, want %q", escaped, want)
				}
			})
		}
	}
}

func TestSubmissionStatusWaiverRefusalsKeepExitCode(t *testing.T) {
	for _, tt := range []struct {
		verbs  []string
		status int
		code   string
		exit   int
	}{
		{[]string{"waive"}, 409, "SUBMISSION_PERIOD_NOT_EXPECTED", 2},
		{[]string{"waive"}, 409, "SUBMISSION_ALREADY_DECLARED", 2},
		{[]string{"waive", "unwaive"}, 409, "PERIOD_LOCKED", 2},
		{[]string{"waive", "unwaive"}, 422, "SERIES_PERIOD_KEY_INVALID", 2},
		{[]string{"waive", "unwaive"}, 404, "", 3},
		{[]string{"waive", "unwaive"}, 403, "", 4},
		{[]string{"waive", "unwaive"}, 500, "", 1},
	} {
		for _, verb := range tt.verbs {
			t.Run(fmt.Sprintf("%s/%d/%s", verb, tt.status, tt.code), func(t *testing.T) {
				mock := waiverTestServer(t)
				mock.RegisterHandler(waiverMethod(verb), waiverTestPrefix+"2026-09/waive", func(w http.ResponseWriter, _ *http.Request) {
					testutil.RespondJSON(w, tt.status, map[string]any{"detail": map[string]any{
						"msg": "server refusal", "error_code": tt.code,
					}})
				})
				out, _, err := runWaiver(t, verb, "--cloud-source-id", "42", "--period", "2026-09")
				if qerrors.GetExitCode(err) != tt.exit || out != "" {
					t.Fatalf("exit = %d, stdout = %q, error = %v", qerrors.GetExitCode(err), out, err)
				}
				if got := len(mock.Requests()); got != 1 {
					t.Errorf("requests = %v", mock.Requests())
				}
			})
		}
	}
}

func TestSubmissionStatusWaiverArgumentsBeforeBootstrap(t *testing.T) {
	for _, verb := range []string{"waive", "unwaive"} {
		for _, args := range [][]string{
			{}, {"--cloud-source-id", "42"}, {"--period", "2026-09"},
			{"--cloud-source-id", "0", "--period", "2026-09"}, {"--cloud-source-id", "-1", "--period", "2026-09"},
			{"--cloud-source-id", "table/42", "--period", "2026-09"},
			{"--cloud-source-id", "42", "--period", ""}, {"--cloud-source-id", "42", "--period", " 2026-09"},
			{"--cloud-source-id", "42", "--period", "2026-09\n"}, {"--cloud-source-id", "42", "--period", strings.Repeat("x", 201)},
			{"42", "--cloud-source-id", "42", "--period", "2026-09"},
			{"--cloud-source-id", "42", "--period", "2026-09", "-o", "name"},
		} {
			t.Run(verb+"/"+strings.Join(args, " "), func(t *testing.T) {
				resetWaiverFlags(t)
				err := runOutsideGetWithoutContext(t, append([]string{"submission-status"}, withWaiverReason(append([]string{verb}, args...))...)...)
				if qerrors.GetExitCode(err) != 2 {
					t.Fatalf("exit = %d, error = %v", qerrors.GetExitCode(err), err)
				}
			})
		}
	}
}

// waive requires a non-blank --reason; it is checked before bootstrap.
func TestSubmissionStatusWaiveReasonBeforeBootstrap(t *testing.T) {
	for _, reason := range [][]string{{}, {"--reason", ""}, {"--reason", "  \t"}} {
		t.Run(strings.Join(reason, " "), func(t *testing.T) {
			resetWaiverFlags(t)
			args := append([]string{"submission-status", "waive", "--cloud-source-id", "42", "--period", "2026-09"}, reason...)
			err := runOutsideGetWithoutContext(t, args...)
			if qerrors.GetExitCode(err) != 2 {
				t.Fatalf("exit = %d, error = %v", qerrors.GetExitCode(err), err)
			}
		})
	}
}

// waive sends the trimmed --reason as the request body.
func TestSubmissionStatusWaiveSendsTheReason(t *testing.T) {
	mock := waiverTestServer(t)
	var body map[string]any
	mock.RegisterHandler("POST", waiverTestPrefix+"2026-09/waive", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(waivedStatusRecord))
	})
	if _, _, err := runWaiver(t, "waive", "--cloud-source-id", "42", "--period", "2026-09", "--reason", "  No business written \n"); err != nil {
		t.Fatal(err)
	}
	if want := map[string]any{"reason": "No business written"}; !reflect.DeepEqual(body, want) {
		t.Errorf("body = %v, want %v", body, want)
	}
}
