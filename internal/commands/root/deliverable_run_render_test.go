package root

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/deliverables"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const renderRequest = "POST " + runTestPath + "41/render"

// renderRunJSON is run 41 in state at render attempt attempt, rendered at
// renderedAt ("" for never), with render_error renderError ("" for none).
func renderRunJSON(state string, attempt int, renderedAt, renderError string) string {
	quoteOrNull := func(s string) string {
		if s == "" {
			return "null"
		}
		return fmt.Sprintf("%q", s)
	}
	return fmt.Sprintf(`{"id":41,"deliverable_id":%q,"revision_id":%q,"revision_mode":"new_period","state":%q,`+
		`"attempt_id":%d,"sequence":1,"allow_empty":false,"run_params":{},"rendered_at":%s,"render_error":%s}`,
		liveDeliverableID, enabledRevisionID, state, attempt, quoteOrNull(renderedAt), quoteOrNull(renderError))
}

// renderTestServer answers run 41's render request with status and body, and
// each read of run 41 with the next of polls, repeating the last.
func renderTestServer(t *testing.T, status int, body string, polls ...string) *deliverableTestServer {
	t.Helper()
	s := newDeliverableTestServer(t)
	resetCommandFlags(t, []string{"deliverable-run", "render"}, "wait", "wait-timeout")
	s.RegisterHandler("POST", runTestPath+"41/render", func(w http.ResponseWriter, r *http.Request) {
		checkDeliverableToken(t, r)
		testutil.RespondJSON(w, status, json.RawMessage(body))
	})
	stubRenderPolls(t, s.MockAPIServer, polls...)
	return s
}

// stubRenderPolls answers each read of run 41 with the next of polls,
// repeating the last, and stubs the wait's clock so polling costs no time.
func stubRenderPolls(t *testing.T, mock *testutil.MockAPIServer, polls ...string) {
	t.Helper()
	now := time.Date(2026, 10, 1, 13, 0, 0, 0, time.UTC)
	deliverables.SetRenderWaitClock(func() time.Time { return now }, func(d time.Duration) { now = now.Add(d) })
	t.Cleanup(deliverables.ResetRenderWaitClock)
	if len(polls) == 0 {
		return
	}
	n := 0
	mock.RegisterHandler("GET", runTestPath+"41", func(w http.ResponseWriter, r *http.Request) {
		checkDeliverableToken(t, r)
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(polls[min(n, len(polls)-1)]))
		n++
	})
}

// resetCommandFlags resets the named flags of the command at path to their
// defaults, now and when t ends.
func resetCommandFlags(t *testing.T, path []string, names ...string) {
	t.Helper()
	cmd, _, err := GetRootCmd().Find(path)
	if err != nil {
		t.Fatal(err)
	}
	reset := func() {
		for _, name := range names {
			f := cmd.Flags().Lookup(name)
			if err := f.Value.Set(f.DefValue); err != nil {
				t.Fatal(err)
			}
			f.Changed = false
		}
	}
	reset()
	t.Cleanup(reset)
}

func runRender(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := GetRootCmd()
	var out, stderr bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&stderr)
	cmd.SetArgs(append([]string{"deliverable-run", "render"}, args...))
	t.Cleanup(func() {
		cmd.SetOut(nil)
		cmd.SetErr(nil)
		cmd.SetArgs(nil)
		resetPersistentFlag(t, cmd, "output", "table")
	})
	_, err := executeC()
	return out.String(), stderr.String(), err
}

const (
	renderedAt0 = "2026-10-01T12:00:00Z"
	renderedAt1 = "2026-10-01T13:00:00Z"
)

var requestedRender = renderRunJSON("rendered", 2, renderedAt0, "")

func TestDeliverableRunRenderWithoutWait(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"table", []string{"41"}, "deliverable-run/41 render requested\n  state   : rendered (Draft ready)\nNext: qctl describe deliverable-run 41\n"},
		{"kind ref", []string{"deliverable-run/41"}, "deliverable-run/41 render requested\n  state   : rendered (Draft ready)\nNext: qctl describe deliverable-run 41\n"},
		{"name", []string{"41", "-o", "name"}, "deliverable-run/41\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := renderTestServer(t, http.StatusOK, requestedRender)
			out, _, err := runRender(t, tc.args...)
			if err != nil {
				t.Fatal(err)
			}
			if out != tc.want {
				t.Errorf("out = %q, want %q", out, tc.want)
			}
			if got, want := s.Requests(), []string{renderRequest}; !reflect.DeepEqual(got, want) {
				t.Errorf("requests = %v, want %v", got, want)
			}
		})
	}
	t.Run("json", func(t *testing.T) {
		renderTestServer(t, http.StatusOK, requestedRender)
		out, _, err := runRender(t, "41", "-o", "json")
		if err != nil {
			t.Fatal(err)
		}
		assertSeriesJSON(t, out, requestedRender)
	})
}

func TestDeliverableRunRenderWaitsForTheAttemptAfterTheRequest(t *testing.T) {
	s := renderTestServer(t, http.StatusOK, requestedRender,
		renderRunJSON("rendered", 2, renderedAt0, ""),
		renderRunJSON("rendered", 3, renderedAt0, ""),
		renderRunJSON("rendered", 3, renderedAt1, ""),
	)
	out, stderr, err := runRender(t, "41", "--wait")
	if err != nil {
		t.Fatalf("%v\nstderr: %s", err, stderr)
	}
	want := "deliverable-run/41 render requested\n  state   : rendered (Draft ready)\n" +
		"deliverable-run/41 rendered\n  attempt : 3\n  rendered: " + renderedAt1 + "\nNext: qctl describe deliverable-run 41\n"
	if out != want {
		t.Errorf("out mismatch:\n--- got ---\n%s--- want ---\n%s", out, want)
	}
	wantErr := "waiting for the render of deliverable run 41, for up to 15m0s; check with: qctl describe deliverable-run 41\n" +
		"deliverable run 41: waiting for a worker to pick up the render\n" +
		"deliverable run 41: render attempt 3 running\n"
	if stderr != wantErr {
		t.Errorf("stderr mismatch:\n--- got ---\n%s--- want ---\n%s", stderr, wantErr)
	}
	if n := len(s.Requests()); n != 4 {
		t.Errorf("requests = %v, want the POST and three polls", s.Requests())
	}
}

// Under -o json stdout is the request's answer, and how the render ended goes
// to stderr.
func TestDeliverableRunRenderWaitKeepsStructuredStdout(t *testing.T) {
	renderTestServer(t, http.StatusOK, requestedRender, renderRunJSON("rendered", 3, renderedAt1, ""))
	out, stderr, err := runRender(t, "41", "--wait", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	assertSeriesJSON(t, out, requestedRender)
	if !strings.Contains(stderr, "deliverable-run/41 rendered\n") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestDeliverableRunRenderWaitFailures(t *testing.T) {
	for _, tc := range []struct {
		name string
		poll string
		code int
		want string
	}{
		{"render error", renderRunJSON("rendered", 3, renderedAt0, "DeliverableStateError: render refused: in-scope rows are blocked and unwaived"),
			qerrors.ExitGenericError, "that older file can still be signed off"},
		{"cancelled meanwhile", renderRunJSON("cancelled", 2, renderedAt0, ""), qerrors.ExitGenericError, "deliverable run 41 is cancelled"},
		{"never picked up", renderRunJSON("rendered", 2, renderedAt0, ""), qerrors.ExitTimeout, "stays queued until a worker takes it"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			renderTestServer(t, http.StatusOK, requestedRender, tc.poll)
			out, _, err := runRender(t, "41", "--wait", "--wait-timeout", "10")
			if code := qerrors.GetExitCode(err); code != tc.code || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("exit = %d, want %d; err = %v", code, tc.code, err)
			}
			if strings.Contains(out, "rendered\n  attempt") {
				t.Errorf("out = %q, want no rendered outcome", out)
			}
		})
	}
}

func TestDeliverableRunRenderRefusal(t *testing.T) {
	s := renderTestServer(t, http.StatusConflict,
		`{"detail":"render can only be requested while a run is open, rendered, or stale; run 41 is delivered."}`)
	out, _, err := runRender(t, "41", "--wait")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage || out != "" {
		t.Fatalf("out = %q; exit = %d; %v", out, code, err)
	}
	if got, want := s.Requests(), []string{renderRequest}; !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %v, want %v", got, want)
	}
}

func TestDeliverableRunRenderLocalErrorsBeforeCredentials(t *testing.T) {
	for _, args := range [][]string{
		{"0"}, {"run-41"}, {"41", "--wait-timeout", "10"}, {"41", "--wait", "--wait-timeout", "0"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			resetCommandFlags(t, []string{"deliverable-run", "render"}, "wait", "wait-timeout")
			err := runOutsideGetWithoutContext(t, append([]string{"deliverable-run", "render"}, args...)...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit = %d, want 2: %v", code, err)
			}
		})
	}
}

func TestCreateDeliverableRunRenderWaits(t *testing.T) {
	s := newCreateRunServer(t, nil, http.StatusOK, createdNewPeriodRun)
	created := renderRunJSON("open", 0, "", "")
	s.RegisterHandler("POST", runTestPath+"57/render", func(w http.ResponseWriter, r *http.Request) {
		checkDeliverableToken(t, r)
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(strings.Replace(created, `"id":41`, `"id":57`, 1)))
	})
	stubRenderPolls(t, s.MockAPIServer)
	polls := 0
	s.RegisterHandler("GET", runTestPath+"57", func(w http.ResponseWriter, _ *http.Request) {
		body := renderRunJSON("open", 1, "", "")
		if polls > 0 {
			body = renderRunJSON("rendered", 1, renderedAt1, "")
		}
		polls++
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(strings.Replace(body, `"id":41`, `"id":57`, 1)))
	})
	out, stderr, err := runCreateRun(t, "", "monthly-bordereau", "--period", "2026-09", "--render", "--wait")
	if err != nil {
		t.Fatalf("%v\nstderr: %s", err, stderr)
	}
	for _, want := range []string{"deliverable-run/57 created\n", "deliverable-run/57 render requested\n  state   : open (In progress)\n",
		"deliverable-run/57 rendered\n  attempt : 1\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("out = %q, want %q in it", out, want)
		}
	}
	if !strings.Contains(stderr, "deliverable run 57: render attempt 1 running") {
		t.Errorf("stderr = %q", stderr)
	}
	if n := s.posts(); n != 2 {
		t.Errorf("POSTs = %d, want the create and the render", n)
	}
}

func TestCreateDeliverableRunRenderMachineFormatPrintsTheCreateAnswerAlone(t *testing.T) {
	s := newCreateRunServer(t, nil, http.StatusOK, createdNewPeriodRun)
	s.RegisterHandler("POST", runTestPath+"57/render", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(createdNewPeriodRun))
	})
	out, _, err := runCreateRun(t, "", "monthly-bordereau", "--period", "2026-09", "--render", "-o", "name")
	if err != nil || out != "deliverable-run/57\n" {
		t.Fatalf("out = %q, err = %v", out, err)
	}
	if n := s.posts(); n != 2 {
		t.Errorf("POSTs = %d, want the create and the render", n)
	}
}

func TestCreateDeliverableRunWaitRequiresRender(t *testing.T) {
	resetCreateRunFlags(t)
	err := runOutsideGetWithoutContext(t, "create", "deliverable-run", liveDeliverableID, "--period", "2026-09", "--wait")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage || !strings.Contains(err.Error(), "--wait requires --render") {
		t.Fatalf("exit = %d, want 2: %v", code, err)
	}
}

func TestCreateDeliverableRunRefusesABadWaitTimeout(t *testing.T) {
	for _, args := range [][]string{{"--render", "--wait", "--wait-timeout", "0"}, {"--render", "--wait-timeout", "10"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			resetCreateRunFlags(t)
			err := runOutsideGetWithoutContext(t, append([]string{"create", "deliverable-run", liveDeliverableID, "--period", "2026-09"}, args...)...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage || !strings.Contains(err.Error(), "--wait-timeout") {
				t.Fatalf("exit = %d, want 2: %v", code, err)
			}
		})
	}
}
