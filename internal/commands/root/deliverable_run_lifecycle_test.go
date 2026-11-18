package root

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
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

func transitionArgs(action string) []string {
	args := []string{"deliverable-run/41"}
	if action == "mark-delivered" {
		args = append(args, "--note", "  Emailed to the carrier  ")
	}
	return args
}

func transitionRunRecord(t *testing.T, action string) string {
	t.Helper()
	if action == "mark-delivered" {
		return replaceAll(t, signedOffRunRecord(t),
			`"state":"signed_off"`, `"state":"delivered"`,
			`"delivered_at":null`, `"delivered_at":"2026-10-10T13:00:00Z"`,
			`"delivery_note":null`, `"delivery_note":"Emailed to the carrier"`)
	}
	return replaceAll(t, describedRunRecord,
		`"state":"rendered"`, `"state":"cancelled"`,
		`"cancelled_at":null`, `"cancelled_at":"2026-10-10T13:00:00Z"`)
}

func newRunTransitionServer(t *testing.T, action string, status int, body string) *runTestServer {
	t.Helper()
	s := newRunTestServer(t, signedOffRunRecord(t))
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Contexts[cfg.CurrentContext].Protected = testutil.BoolPtr(false)
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	cmd, _, err := GetRootCmd().Find([]string{"deliverable-run", action})
	if err != nil {
		t.Fatal(err)
	}
	reset := func() {
		for _, name := range []string{"yes", "note"} {
			flag := cmd.Flags().Lookup(name)
			if flag == nil {
				continue
			}
			if err := flag.Value.Set(flag.DefValue); err != nil {
				t.Fatal(err)
			}
			flag.Changed = false
		}
	}
	reset()
	t.Cleanup(reset)
	s.RegisterHandler("POST", runTestPath+"41/"+action, func(w http.ResponseWriter, r *http.Request) {
		checkDeliverableToken(t, r)
		if r.URL.RawQuery != "" {
			t.Errorf("unexpected query: %s", r.URL.RawQuery)
		}
		if action == "cancel" && r.Body != nil {
			sent, err := io.ReadAll(r.Body)
			if err != nil || len(sent) != 0 {
				t.Errorf("cancel body = %q; err = %v, want no body", sent, err)
			}
		} else if action == "mark-delivered" {
			var sent map[string]any
			if err := json.NewDecoder(r.Body).Decode(&sent); err != nil {
				t.Error(err)
			}
			if want := map[string]any{"note": "Emailed to the carrier"}; !reflect.DeepEqual(sent, want) {
				t.Errorf("body = %v, want %v", sent, want)
			}
		}
		testutil.RespondJSON(w, status, json.RawMessage(body))
	})
	return s
}

func runTransitionTest(t *testing.T, action string, args ...string) (string, string, error) {
	t.Helper()
	cmd := GetRootCmd()
	var out, stderr bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&stderr)
	cmd.SetArgs(append([]string{"deliverable-run", action}, args...))
	t.Cleanup(func() {
		cmd.SetOut(nil)
		cmd.SetErr(nil)
		cmd.SetArgs(nil)
		resetPersistentFlag(t, cmd, "output", "table")
		resetPersistentFlag(t, cmd, "context", "")
		resetPersistentFlag(t, cmd, "retries", "0")
		config.SetContextOverride("")
	})
	_, err := executeC()
	return out.String(), stderr.String(), err
}

func TestDeliverableRunTransitionsOutputFormats(t *testing.T) {
	for _, action := range []string{"mark-delivered", "cancel"} {
		for _, format := range []string{"table", "json", "yaml", "name"} {
			t.Run(action+"/"+format, func(t *testing.T) {
				body := transitionRunRecord(t, action)
				body = strings.TrimSuffix(body, "}") + `,"future_field":{"exact":9007199254740993,"unset":null}}`
				s := newRunTransitionServer(t, action, http.StatusOK, body)
				args := append(transitionArgs(action), "--yes", "-o", format)
				out, stderr, err := runTransitionTest(t, action, args...)
				if err != nil {
					t.Fatal(err)
				}
				switch format {
				case "json":
					var got, want bytes.Buffer
					if err := json.Compact(&got, []byte(out)); err != nil {
						t.Fatal(err)
					}
					if err := json.Compact(&want, []byte(body)); err != nil {
						t.Fatal(err)
					}
					if got.String() != want.String() {
						t.Errorf("JSON differs from response: %s", out)
					}
				case "yaml":
					testutil.AssertYAMLReadsAsJSON(t, out, body)
				case "name":
					if out != "deliverable-run/41\n" {
						t.Errorf("name = %q", out)
					}
				case "table":
					verb := "cancelled"
					if action == "mark-delivered" {
						verb = "marked delivered"
					}
					if out != "deliverable-run/41 "+verb+"\n" {
						t.Errorf("table = %q", out)
					}
				}
				if want := []string{"POST " + runTestPath + "41/" + action}; !reflect.DeepEqual(s.Requests(), want) {
					t.Errorf("requests = %v, want %v", s.Requests(), want)
				}
				if stderr != "" {
					t.Errorf("stderr = %q", stderr)
				}
			})
		}
	}
}

func TestDeliverableRunTransitionsRejectEmptySuccess(t *testing.T) {
	for _, action := range []string{"mark-delivered", "cancel"} {
		t.Run(action, func(t *testing.T) {
			s := newRunTransitionServer(t, action, http.StatusOK, "null")
			args := append(transitionArgs(action), "--yes")
			out, _, err := runTransitionTest(t, action, args...)
			if qerrors.GetExitCode(err) != qerrors.ExitGenericError || out != "" || len(s.Requests()) != 1 {
				t.Fatalf("out = %q; err = %v; requests = %v, want empty-response failure", out, err, s.Requests())
			}
		})
	}
}

func TestDeliverableRunMarkDeliveredRejectsBlankNote(t *testing.T) {
	for _, note := range []string{"", " ", "\t\n"} {
		t.Run(note, func(t *testing.T) {
			s := newRunTransitionServer(t, "mark-delivered", http.StatusOK, transitionRunRecord(t, "mark-delivered"))
			args := []string{"41", "--yes"}
			if note != "" {
				args = append(args, "--note", note)
			}
			out, _, err := runTransitionTest(t, "mark-delivered", args...)
			if qerrors.GetExitCode(err) != qerrors.ExitBadUsage || !strings.Contains(err.Error(), "--note") || out != "" || len(s.Requests()) != 0 {
				t.Fatalf("out = %q; err = %v; requests = %v, want exit 2 without requests", out, err, s.Requests())
			}
		})
	}
}

func TestDeliverableRunTransitionsRejectInvalidArgs(t *testing.T) {
	for _, action := range []string{"mark-delivered", "cancel"} {
		for _, args := range [][]string{{}, {"41", "42"}, {"0"}, {"deliverable-run/0"}, {"abc"}, {"--", "-1"}, {"table/41"}, {"41", "-o", "code"}} {
			t.Run(action+"/"+strings.Join(args, " "), func(t *testing.T) {
				s := newRunTransitionServer(t, action, http.StatusOK, transitionRunRecord(t, action))
				if action == "mark-delivered" {
					args = append([]string{"--note", "Emailed to the carrier"}, args...)
				}
				out, _, err := runTransitionTest(t, action, args...)
				if qerrors.GetExitCode(err) != qerrors.ExitBadUsage || out != "" || len(s.Requests()) != 0 {
					t.Fatalf("out = %q; err = %v; requests = %v, want exit 2 without requests", out, err, s.Requests())
				}
			})
		}
	}
}

func TestDeliverableRunTransitionsConfirmation(t *testing.T) {
	for _, action := range []string{"mark-delivered", "cancel"} {
		for _, answer := range []string{"y\n", "n\n", "\n"} {
			t.Run(action+"/"+answer, func(t *testing.T) {
				s := newRunTransitionServer(t, action, http.StatusOK, transitionRunRecord(t, action))
				cmdutil.SetReader(strings.NewReader(answer))
				t.Cleanup(cmdutil.ResetReader)
				args := append(transitionArgs(action), "-o", "json")
				out, stderr, err := runTransitionTest(t, action, args...)
				if answer == "y\n" {
					if err != nil || !json.Valid([]byte(out)) || len(s.Requests()) != 1 {
						t.Fatalf("out = %q; err = %v; requests = %v", out, err, s.Requests())
					}
				} else if !errors.Is(err, cmdutil.ErrDeclined) || out != "" || len(s.Requests()) != 0 {
					t.Fatalf("out = %q; err = %v; requests = %v", out, err, s.Requests())
				}
				if !strings.Contains(stderr, "run 41") || !strings.Contains(stderr, "This cannot be undone. [y/N]:") {
					t.Errorf("prompt = %q", stderr)
				}
			})
		}
	}
}

func TestDeliverableRunTransitionsRequireConfirmationOffTTY(t *testing.T) {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		t.Skip("requires non-TTY stdin")
	}
	for _, action := range []string{"mark-delivered", "cancel"} {
		t.Run(action, func(t *testing.T) {
			s := newRunTransitionServer(t, action, http.StatusOK, transitionRunRecord(t, action))
			out, _, err := runTransitionTest(t, action, transitionArgs(action)...)
			if qerrors.GetExitCode(err) != qerrors.ExitBadUsage || !strings.Contains(err.Error(), "--yes") || out != "" || len(s.Requests()) != 0 {
				t.Fatalf("out = %q; err = %v; requests = %v, want confirmation refusal", out, err, s.Requests())
			}
		})
	}
}

func TestDeliverableRunTransitionsRequireNamedProtectedContext(t *testing.T) {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		t.Skip("requires non-TTY stdin")
	}
	for _, action := range []string{"mark-delivered", "cancel"} {
		for _, named := range []bool{false, true} {
			t.Run(action+"/"+map[bool]string{false: "unnamed", true: "named"}[named], func(t *testing.T) {
				s := newRunTransitionServer(t, action, http.StatusOK, transitionRunRecord(t, action))
				cfg, err := config.Load()
				if err != nil {
					t.Fatal(err)
				}
				cfg.Contexts[cfg.CurrentContext].Protected = nil
				if err := cfg.Save(); err != nil {
					t.Fatal(err)
				}
				args := append(transitionArgs(action), "--yes")
				if named {
					args = append(args, "--context", cfg.CurrentContext)
				}
				out, _, err := runTransitionTest(t, action, args...)
				if named {
					if err != nil || len(s.Requests()) != 1 {
						t.Fatalf("err = %v; requests = %v", err, s.Requests())
					}
				} else if qerrors.GetExitCode(err) != qerrors.ExitBadUsage || !strings.Contains(err.Error(), "--context") || out != "" || len(s.Requests()) != 0 {
					t.Fatalf("out = %q; err = %v; requests = %v, want protected refusal", out, err, s.Requests())
				}
			})
		}
	}
}

func TestDeliverableRunTransitionsPreserveRefusals(t *testing.T) {
	for _, action := range []string{"mark-delivered", "cancel"} {
		for _, tc := range []struct {
			name, code, message string
			status, exit        int
		}{
			{"not latest", "REISSUE_NOT_LATEST", "Cancel this run; a later period has been delivered.", http.StatusConflict, qerrors.ExitBadUsage},
			{"pending", "REISSUE_PENDING", "Deliver or cancel run 40 first.", http.StatusConflict, qerrors.ExitBadUsage},
			{"state", "", "The run changed; its current state refuses this transition.", http.StatusConflict, qerrors.ExitBadUsage},
			{"validation", "SCOPE_ERROR", "A delivery note is required.", http.StatusUnprocessableEntity, qerrors.ExitBadUsage},
			{"not found", "", "Run not found.", http.StatusNotFound, qerrors.ExitNotFound},
			{"permission", "", "Forbidden.", http.StatusForbidden, qerrors.ExitUnauthorized},
			{"server", "", "Service unavailable.", http.StatusServiceUnavailable, qerrors.ExitGenericError},
		} {
			t.Run(action+"/"+tc.name, func(t *testing.T) {
				body, err := json.Marshal(map[string]any{"detail": map[string]any{
					"msg": tc.message, "error_code": tc.code, "context": map[string]any{"run_id": 41},
				}})
				if err != nil {
					t.Fatal(err)
				}
				s := newRunTransitionServer(t, action, tc.status, string(body))
				args := append(transitionArgs(action), "--yes", "--retries", "2", "-o", "json")
				out, _, err := runTransitionTest(t, action, args...)
				if qerrors.GetExitCode(err) != tc.exit || !strings.Contains(err.Error(), tc.message) {
					t.Fatalf("err = %v, want message %q and exit %d", err, tc.message, tc.exit)
				}
				var apiErr *deliverables.APIError
				if !errors.As(err, &apiErr) || apiErr.StatusCode != tc.status || apiErr.Code != tc.code || apiErr.Context["run_id"] == nil {
					t.Errorf("error lost API metadata: %v", err)
				}
				if tc.status == http.StatusForbidden && !strings.Contains(err.Error(), "deliverable:admin") {
					t.Errorf("missing permission note: %v", err)
				}
				if out != "" || len(s.Requests()) != 1 {
					t.Errorf("out = %q; requests = %v, want one POST and no success", out, s.Requests())
				}
			})
		}
	}
}
