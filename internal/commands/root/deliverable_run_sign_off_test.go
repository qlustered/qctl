package root

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/deliverables"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"golang.org/x/term"
)

const signOffRequest = "POST " + runTestPath + "41/sign-off"

type signOffConfirmationReader func([]byte) (int, error)

func (read signOffConfirmationReader) Read(p []byte) (int, error) { return read(p) }

func signedOffRunRecord(t *testing.T) string {
	t.Helper()
	return replaceAll(t, describedRunRecord,
		`"state":"rendered"`, `"state":"signed_off"`,
		`"signed_off_at":null`, `"signed_off_at":"2026-10-10T12:00:00Z"`)
}

func newSignOffTestServer(t *testing.T, status int, body string) *runTestServer {
	t.Helper()
	s := newRunTestServer(t, describedRunRecord)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Contexts[cfg.CurrentContext].Protected = testutil.BoolPtr(false)
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	cmd, _, err := GetRootCmd().Find([]string{"deliverable-run", "sign-off"})
	if err != nil {
		t.Fatal(err)
	}
	reset := func() {
		flag := cmd.Flags().Lookup("yes")
		if err := flag.Value.Set(flag.DefValue); err != nil {
			t.Fatal(err)
		}
		flag.Changed = false
	}
	reset()
	t.Cleanup(reset)
	s.RegisterHandler("POST", runTestPath+"41/sign-off", func(w http.ResponseWriter, r *http.Request) {
		checkDeliverableToken(t, r)
		var sent map[string]any
		if err := json.NewDecoder(r.Body).Decode(&sent); err != nil {
			t.Error(err)
		}
		if want := map[string]any{"expected_attempt_id": float64(2)}; !reflect.DeepEqual(sent, want) {
			t.Errorf("body = %v, want %v", sent, want)
		}
		testutil.RespondJSON(w, status, json.RawMessage(body))
	})
	return s
}

func runSignOffTest(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := GetRootCmd()
	var out, stderr bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&stderr)
	cmd.SetArgs(append([]string{"deliverable-run", "sign-off"}, args...))
	t.Cleanup(func() {
		cmd.SetOut(nil)
		cmd.SetErr(nil)
		cmd.SetArgs(nil)
		resetPersistentFlag(t, cmd, "output", "table")
		resetPersistentFlag(t, cmd, "context", "")
		config.SetContextOverride("")
	})
	_, err := executeC()
	return out.String(), stderr.String(), err
}

func TestDeliverableRunSignOffPreviewAndAttempt(t *testing.T) {
	s := newSignOffTestServer(t, http.StatusOK, signedOffRunRecord(t))
	readiness := replaceAll(t, describedRunReadiness,
		`"waived":0`, `"waived":2`,
		`"unbound_carried":[]`, `"unbound_carried":[{"sheet_name":"Old sheet","unwaived":0,"waived":3}]`,
		`"waived_without_sheet":0`, `"waived_without_sheet":4`)
	s.answerReadiness(t, http.StatusOK, readiness)
	out, stderr, err := runSignOffTest(t, "deliverable-run/41", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if out != "deliverable-run/41 signed off; its report is locked\n" {
		t.Errorf("out = %q", out)
	}
	for _, want := range []string{
		"Sign off deliverable-run/41 (issue #2, new period)", "Render attempt: 2",
		"Rendered: 2026-10-01T13:00:00Z", "carrier_id=9007199254740993, period=2026-09",
		`sheet "Alpha": count = 12`, `sheet "Zeta": count = 9007199254740993`,
		"Alpha!B1: sum(premium) = 1,234.50", "Left out (waived): 11",
		"Readiness of run 41 (rendered): no errors or blocked rows", "Old sheet",
		"Waivers on no sheet: 4", "Table 42 is period 2026-08", "Signing off locks this report.",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("preview missing %q:\n%s", want, stderr)
		}
	}
	if got, want := s.Requests(), []string{runReadRequest, readinessRequest, signOffRequest}; !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %v, want %v", got, want)
	}
	if !reflect.DeepEqual(s.readinessQueries, []string{"limit=1"}) {
		t.Errorf("readiness queries = %v", s.readinessQueries)
	}
}

func TestDeliverableRunSignOffOutputFormats(t *testing.T) {
	for _, format := range []string{"json", "yaml", "name"} {
		t.Run(format, func(t *testing.T) {
			body := strings.TrimSuffix(strings.TrimSpace(signedOffRunRecord(t)), "}") + `,"future_field":9007199254740993}`
			newSignOffTestServer(t, http.StatusOK, body)
			out, stderr, err := runSignOffTest(t, "41", "--yes", "-o", format)
			if err != nil {
				t.Fatal(err)
			}
			switch format {
			case "name":
				if out != "deliverable-run/41\n" {
					t.Errorf("out = %q", out)
				}
			case "yaml":
				testutil.AssertYAMLReadsAsJSON(t, out, body)
			default:
				var want bytes.Buffer
				if err := json.Indent(&want, []byte(body), "", "  "); err != nil {
					t.Fatal(err)
				}
				if out != want.String()+"\n" {
					t.Errorf("out = %q, want original response body", out)
				}
			}
			if !strings.Contains(stderr, "Params:") || !strings.Contains(stderr, "Left out (waived): 0") {
				t.Errorf("stderr lacks preview: %q", stderr)
			}
		})
	}
}

func TestDeliverableRunSignOffConfirmation(t *testing.T) {
	for _, answer := range []string{"y\n", "n\n"} {
		t.Run(strings.TrimSpace(answer), func(t *testing.T) {
			s := newSignOffTestServer(t, http.StatusOK, signedOffRunRecord(t))
			cmdutil.SetReader(strings.NewReader(answer))
			t.Cleanup(cmdutil.ResetReader)
			out, stderr, err := runSignOffTest(t, "41", "-o", "json")
			if answer == "y\n" {
				if err != nil || !json.Valid([]byte(out)) || len(s.Requests()) != 3 {
					t.Fatalf("out = %q; err = %v; requests = %v", out, err, s.Requests())
				}
			} else if !errors.Is(err, cmdutil.ErrDeclined) || out != "" || len(s.Requests()) != 2 {
				t.Fatalf("out = %q; err = %v; requests = %v", out, err, s.Requests())
			}
			preview := strings.Index(stderr, "Left out (waived): 0")
			prompt := strings.Index(stderr, "Sign off run 41, attempt 2, and lock its report? [y/N]:")
			if preview < 0 || prompt <= preview {
				t.Errorf("preview must precede the prompt: %q", stderr)
			}
		})
	}
}

func TestDeliverableRunSignOffRequiresConfirmationOffTTY(t *testing.T) {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		t.Skip("requires non-TTY stdin")
	}
	s := newSignOffTestServer(t, http.StatusOK, signedOffRunRecord(t))
	out, stderr, err := runSignOffTest(t, "41")
	if qerrors.GetExitCode(err) != qerrors.ExitBadUsage || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("error = %v, want exit 2 naming --yes", err)
	}
	if out != "" || !strings.Contains(stderr, "Left out (waived):") || len(s.Requests()) != 2 {
		t.Errorf("out = %q; stderr = %q; requests = %v", out, stderr, s.Requests())
	}
}

func TestDeliverableRunSignOffDoesNotApproveAnAttemptThatChangedAtConfirmation(t *testing.T) {
	s := newSignOffTestServer(t, http.StatusConflict,
		`{"detail":{"msg":"Review attempt 3 before signing off.","error_code":"STALE_ATTEMPT"}}`)
	latest := describedRunRecord
	s.RegisterHandler("GET", runTestPath+"41", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(latest))
	})
	answer := strings.NewReader("y\n")
	cmdutil.SetReader(signOffConfirmationReader(func(p []byte) (int, error) {
		latest = replaceAll(t, describedRunRecord, `"attempt_id":2`, `"attempt_id":3`)
		return answer.Read(p)
	}))
	t.Cleanup(cmdutil.ResetReader)
	out, stderr, err := runSignOffTest(t, "41", "-o", "json")
	if qerrors.GetExitCode(err) != qerrors.ExitBadUsage || !strings.Contains(err.Error(), "review its current render before retrying") {
		t.Fatalf("err = %v, want stale attempt refusal with exit 2", err)
	}
	if out != "" || !strings.Contains(stderr, "Render attempt: 2") || strings.Contains(stderr, "Render attempt: 3") {
		t.Errorf("out = %q; preview = %q", out, stderr)
	}
	if want := []string{runReadRequest, readinessRequest, signOffRequest}; !reflect.DeepEqual(s.Requests(), want) {
		t.Errorf("requests = %v, want %v with no refresh or retry", s.Requests(), want)
	}
}

func TestDeliverableRunSignOffPreviewWithoutTotals(t *testing.T) {
	s := newSignOffTestServer(t, http.StatusOK, signedOffRunRecord(t))
	run := replaceAll(t, liveDeliverableRun, `"attempt_id":1`, `"attempt_id":2`)
	s.RegisterHandler("GET", runTestPath+"41", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(run))
	})
	_, stderr, err := runSignOffTest(t, "41", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "Totals (last render): none recorded") || !strings.Contains(stderr, "Left out (waived): 0") {
		t.Errorf("preview = %q", stderr)
	}
}

func TestDeliverableRunSignOffRequiresNamedProtectedContext(t *testing.T) {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		t.Skip("requires non-TTY stdin")
	}
	for _, named := range []bool{false, true} {
		t.Run(map[bool]string{false: "unnamed", true: "named"}[named], func(t *testing.T) {
			s := newSignOffTestServer(t, http.StatusOK, signedOffRunRecord(t))
			cfg, err := config.Load()
			if err != nil {
				t.Fatal(err)
			}
			cfg.Contexts[cfg.CurrentContext].Protected = nil
			if err := cfg.Save(); err != nil {
				t.Fatal(err)
			}
			args := []string{"41", "--yes"}
			if named {
				args = append(args, "--context", cfg.CurrentContext)
			}
			_, _, err = runSignOffTest(t, args...)
			if named {
				if err != nil || len(s.Requests()) != 3 {
					t.Fatalf("err = %v; requests = %v", err, s.Requests())
				}
			} else if qerrors.GetExitCode(err) != qerrors.ExitBadUsage || !strings.Contains(err.Error(), "--context") || len(s.Requests()) != 0 {
				t.Fatalf("err = %v; requests = %v, want protected refusal and no requests", err, s.Requests())
			}
		})
	}
}

func TestDeliverableRunSignOffRefusals(t *testing.T) {
	for _, tc := range []struct {
		name, body   string
		status, exit int
		want         []string
	}{
		{"blocked", `{"detail":{"msg":"Live released rows drifted; re-render required.","error_code":"SIGNOFF_BLOCKED","context":{"blocked_awaiting_review":3,"blocked_failing_rules":2,"blocked_indeterminate":1,"waived":4,"carried_unbound":5}}}`,
			http.StatusConflict, qerrors.ExitBadUsage, []string{"Live released rows drifted", `"blocked_awaiting_review": 3`, `"blocked_failing_rules": 2`, `"blocked_indeterminate": 1`, `"waived": 4`, `"carried_unbound": 5`, "qctl deliverable-run readiness 41"}},
		{"blocked without context", `{"detail":{"msg":"Run changed during validation.","error_code":"SIGNOFF_BLOCKED"}}`,
			http.StatusConflict, qerrors.ExitBadUsage, []string{"Run changed during validation", "qctl deliverable-run readiness 41"}},
		{"stale attempt", `{"detail":{"msg":"You reviewed attempt 2; the run is now attempt 3.","error_code":"STALE_ATTEMPT"}}`,
			http.StatusConflict, qerrors.ExitBadUsage, []string{"attempt 2", "attempt 3", "review its current render before retrying", "qctl describe deliverable-run 41"}},
		{"permission", `{"detail":{"msg":"Forbidden."}}`, http.StatusForbidden, qerrors.ExitUnauthorized,
			[]string{"deliverable:run:signoff", "admin, member, ops", "Editor and agent roles cannot sign off"}},
		{"period order", `{"detail":{"msg":"Deliver run 40 first.","error_code":"REISSUE_PENDING"}}`,
			http.StatusConflict, qerrors.ExitBadUsage, []string{"Deliver run 40 first"}},
		{"validation", `{"detail":{"msg":"Invalid attempt.","error_code":"SCOPE_ERROR","context":{"loc":["body","expected_attempt_id"]}}}`,
			http.StatusUnprocessableEntity, qerrors.ExitBadUsage, []string{"Invalid attempt"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newSignOffTestServer(t, tc.status, tc.body)
			out, _, err := runSignOffTest(t, "41", "--yes", "-o", "json")
			if code := qerrors.GetExitCode(err); code != tc.exit {
				t.Fatalf("exit = %d, want %d; err = %v", code, tc.exit, err)
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error lacks %q: %v", want, err)
				}
			}
			var apiErr *deliverables.APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != tc.status {
				t.Errorf("error lost APIError: %v", err)
			}
			if out != "" || len(s.Requests()) != 3 {
				t.Errorf("out = %q; requests = %v", out, s.Requests())
			}
		})
	}
}

func TestDeliverableRunSignOffRefusesNonRenderedRun(t *testing.T) {
	for _, state := range []string{"open", "stale", "signed_off", "delivered", "cancelled"} {
		t.Run(state, func(t *testing.T) {
			s := newSignOffTestServer(t, http.StatusOK, signedOffRunRecord(t))
			run := replaceAll(t, describedRunRecord, `"state":"rendered"`, `"state":"`+state+`"`)
			s.RegisterHandler("GET", runTestPath+"41", func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, json.RawMessage(run))
			})
			out, _, err := runSignOffTest(t, "41", "--yes")
			if qerrors.GetExitCode(err) != qerrors.ExitBadUsage || !strings.Contains(err.Error(), "only rendered runs") {
				t.Fatalf("err = %v, want state refusal with exit 2", err)
			}
			if out != "" || !reflect.DeepEqual(s.Requests(), []string{runReadRequest}) {
				t.Errorf("out = %q; requests = %v", out, s.Requests())
			}
		})
	}
}

func TestDeliverableRunSignOffReadFailureStopsBeforePost(t *testing.T) {
	for _, path := range []string{runTestPath + "41", runTestPath + "41/readiness"} {
		t.Run(path, func(t *testing.T) {
			s := newSignOffTestServer(t, http.StatusOK, signedOffRunRecord(t))
			s.RegisterHandler("GET", path, func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusNotFound, map[string]any{"detail": map[string]any{"msg": "Run not found."}})
			})
			out, _, err := runSignOffTest(t, "41", "--yes")
			if qerrors.GetExitCode(err) != qerrors.ExitNotFound || out != "" {
				t.Fatalf("out = %q; err = %v, want exit 3", out, err)
			}
			for _, request := range s.Requests() {
				if request == signOffRequest {
					t.Fatal("sign-off sent after a failed read")
				}
			}
		})
	}
}

func TestDeliverableRunSignOffRejectsInvalidArgsBeforeRequests(t *testing.T) {
	for _, args := range [][]string{{}, {"41", "42"}, {"0"}, {"deliverable-run/0"}, {"abc"}, {"--", "-1"}, {"table/41"}, {"41", "-o", "code"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			s := newSignOffTestServer(t, http.StatusOK, signedOffRunRecord(t))
			out, _, err := runSignOffTest(t, args...)
			if qerrors.GetExitCode(err) != qerrors.ExitBadUsage || out != "" || len(s.Requests()) != 0 {
				t.Fatalf("out = %q; err = %v; requests = %v, want exit 2 before any request", out, err, s.Requests())
			}
		})
	}
}
