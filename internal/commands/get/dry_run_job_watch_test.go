package get

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

// drwServer is a fake API serving one dry-run job, 42, over REST and over its
// progress websocket.
type drwServer struct {
	t   *testing.T
	srv *httptest.Server

	mu sync.Mutex
	// states are the job's state at each REST read; the last one repeats.
	states []string
	// restStatus, when set, answers every REST read of the job instead.
	restStatus int
	// handshake, when set, refuses the websocket upgrade with this status.
	handshake int
	// frames are sent after the upgrade; closeCode, when set, then closes the
	// stream with closeReason, and otherwise the stream stays open and silent.
	frames      []string
	onStream    func()
	closeCode   int
	closeReason string
	// extra fields for each REST answer, such as error_message.
	extra map[string]any
	// alert is the alert_item_id of ingestion job 9, 0 for none.
	alert          int
	stallRead      int
	stallIngestion bool

	restReads, dials, ingestionReads int
}

func newDRWServer(t *testing.T, s *drwServer) *drwServer {
	t.Helper()
	s.t = t
	base := "/api/orgs/" + testDryRunOrgID
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+base+"/dry-runs/42", s.serveJob)
	mux.HandleFunc("GET "+base+"/ingestion-jobs/9", s.serveIngestionJob)
	mux.HandleFunc(base+"/ws/dry-runs/42", s.serveStream)
	s.srv = httptest.NewServer(mux)
	t.Cleanup(s.srv.Close)

	t.Setenv("QCTL_WATCH_TIMEOUT", "")
	env := testutil.NewTestEnv(t)
	endpointKey, _ := config.NormalizeEndpointKey(s.srv.URL)
	env.SetupConfigWithOrg(s.srv.URL, "test@example.com", testDryRunOrgID)
	env.SetupCredential(endpointKey, testDryRunOrgID, "test-token")
	return s
}

func (s *drwServer) serveJob(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.restReads++
	if s.restReads == s.stallRead {
		testutil.WaitForRequestCancellation(s.t, r)
		return
	}
	if s.restStatus != 0 {
		testutil.RespondJSON(w, s.restStatus, map[string]any{"detail": "Dry Run Job ID 42 can not be found."})
		return
	}
	state := s.states[min(s.restReads, len(s.states))-1]
	body := map[string]any{
		"id": 42, "dataset_id": 10, "state": state, "ingestion_job_id": 9,
		"rule_run_specs": []map[string]any{{"position": 0, "dataset_rule_id": nil}},
		"sampling":       map[string]any{"alpha_pref": 0, "cap": 0, "eligible_c": 0, "eligible_q": 0, "pins_inserted": 0, "pins_missing": 0, "pins_truncated": 0},
		"metrics":        nil,
	}
	for k, v := range s.extra {
		body[k] = v
	}
	testutil.RespondJSON(w, http.StatusOK, body)
}

func (s *drwServer) serveIngestionJob(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ingestionReads++
	if s.stallIngestion {
		testutil.WaitForRequestCancellation(s.t, r)
		return
	}
	body := map[string]any{"id": 9, "state": "blocked", "is_dry_run": true, "alert_item_id": nil}
	if s.alert != 0 {
		body["alert_item_id"] = s.alert
		body["alert_item_msg"] = "Tried 3 times, giving up."
	}
	testutil.RespondJSON(w, http.StatusOK, body)
}

func (s *drwServer) serveStream(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.dials++
	handshake, frames, closeCode, closeReason := s.handshake, s.frames, s.closeCode, s.closeReason
	s.mu.Unlock()
	if handshake != 0 {
		w.WriteHeader(handshake)
		return
	}
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.t.Errorf("upgrade: %v", err)
		return
	}
	defer conn.Close()
	for _, frame := range frames {
		if err := conn.WriteMessage(websocket.TextMessage, []byte(frame)); err != nil {
			return
		}
	}
	if closeCode != 0 {
		_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(closeCode, closeReason))
	}
	if s.onStream != nil {
		s.onStream()
	}
	// Until the client goes away.
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}

func (s *drwServer) counts() (restReads, dials, ingestionReads int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.restReads, s.dials, s.ingestionReads
}

// drwRun runs `qctl get dry-run-job 42 <args>` and returns stdout, stderr and
// the error.
func drwRun(args ...string) (string, string, error) {
	cmd := setupDryRunJobTestCommand()
	// As the real root does, so stdout and stderr hold only the command's own.
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	cmd.SetArgs(append([]string{"get", "dry-run-job", "42"}, args...))
	// stderr is written to by the stream's goroutine too.
	var stdout bytes.Buffer
	var stderr testutil.LockedBuffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	err := cmd.Execute()
	return stdout.String(), stderr.String(), err
}

func drwRequireExit(t *testing.T, err error, want int, stderr string) {
	t.Helper()
	if got := qerrors.GetExitCode(err); got != want {
		t.Fatalf("exit code = %d, want %d; err = %v\nstderr:\n%s", got, want, err, stderr)
	}
}

func TestWatchDryRunJobFinishedExitsZero(t *testing.T) {
	s := newDRWServer(t, &drwServer{
		states:    []string{"running"},
		frames:    []string{`{"state": "running"}`, `{"state": "finished"}`},
		closeCode: websocket.CloseNormalClosure,
	})
	stdout, stderr, err := drwRun("--watch")
	drwRequireExit(t, err, 0, stderr)
	if want := "dry-run-job 42: running\ndry-run-job 42: finished\n"; stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
	if want := "waiting for dry-run-job 42 to finish; resume with: qctl get dry-run-job 42 --watch\n"; !strings.HasPrefix(stderr, want) {
		t.Errorf("stderr = %q, want the banner %q first", stderr, want)
	}
	if reads, dials, _ := s.counts(); reads != 1 || dials != 1 {
		t.Errorf("REST reads = %d, dials = %d; want 1 and 1", reads, dials)
	}
}

// Every terminal state but finished exits 1 with a Next line, whatever the
// output format.
func TestWatchDryRunJobFailedStatesExitOne(t *testing.T) {
	for _, tc := range []struct {
		state          string
		extra          map[string]any
		alert          int
		stallRead      int
		stallIngestion bool
		want           []string
	}{
		{state: "error", extra: map[string]any{"error_message": "Dry run crashed on **orders**"}, want: []string{
			"dry-run-job 42 ended in state error", "error message: Dry run crashed on orders", "Next: qctl describe dry-run-job 42"}},
		{state: "killed", want: []string{"dry-run-job 42 ended in state killed", "Next: qctl describe dry-run-job 42"}},
		{state: "stopped", want: []string{"dry-run-job 42 ended in state stopped", "Next: qctl describe dry-run-job 42"}},
		{state: "version_id_mismatch", want: []string{"ended in state version_id_mismatch", "Next: qctl describe dry-run-job 42"}},
		{state: "blocked", alert: 77, want: []string{
			"dry-run-job 42 ended in state blocked: alert 77 blocks it", "alert message: Tried 3 times, giving up.", "Next: qctl describe alert 77"}},
		{state: "blocked", want: []string{"dry-run-job 42 ended in state blocked", "Next: qctl describe ingestion-job 9"}},
	} {
		t.Run(tc.state, func(t *testing.T) {
			s := newDRWServer(t, &drwServer{
				states: []string{"running", tc.state},
				extra:  tc.extra,
				alert:  tc.alert,
				// The stream stays open: the terminal frame alone ends the watch.
				frames: []string{`{"state": "running"}`, `{"state": "` + tc.state + `"}`},
			})
			stdout, stderr, err := drwRun("--watch", "-o", "json", "--timeout", "3s")
			drwRequireExit(t, err, 1, stderr)
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error should contain %q, got:\n%v", want, err)
				}
			}
			if !strings.HasSuffix(err.Error(), tc.want[len(tc.want)-1]) {
				t.Errorf("error should end with its Next line, got:\n%v", err)
			}
			if want := "{\"state\":\"running\"}\n{\"state\":\"" + tc.state + "\"}\n"; stdout != want {
				t.Errorf("stdout = %q, want %q", stdout, want)
			}
			if _, _, ingestionReads := s.counts(); tc.state == "blocked" && ingestionReads != 1 {
				t.Errorf("ingestion job reads = %d, want 1", ingestionReads)
			}
		})
	}
}

// A blocked frame is judged as blocked even when the read after it finds the
// job re-queued, and the alert is still read from its ingestion job.
func TestWatchDryRunJobBlockedFrameKeepsItsAlertAfterARequeue(t *testing.T) {
	s := newDRWServer(t, &drwServer{
		states: []string{"running", "pending"},
		alert:  77,
		frames: []string{`{"state": "running"}`, `{"state": "blocked"}`},
	})
	_, stderr, err := drwRun("--watch", "--timeout", "3s")
	drwRequireExit(t, err, 1, stderr)
	if want := "Next: qctl describe alert 77"; !strings.HasSuffix(err.Error(), want) {
		t.Errorf("error should end with %q, got:\n%v", want, err)
	}
	if _, _, ingestionReads := s.counts(); ingestionReads != 1 {
		t.Errorf("ingestion job reads = %d, want 1", ingestionReads)
	}
}

// A job already in a terminal state is printed once in the watch's format and
// judged without opening the stream.
func TestWatchDryRunJobAlreadyTerminalNeverDials(t *testing.T) {
	for _, tc := range []struct {
		state, format, stdout string
		code                  int
	}{
		{"finished", "table", "dry-run-job 42: finished\n", 0},
		{"finished", "json", "{\"state\":\"finished\"}\n", 0},
		{"killed", "name", "dry-run-job/42\n", 1},
		{"partitioned", "yaml", "---\nstate: partitioned\n", 1},
	} {
		t.Run(tc.state+"/"+tc.format, func(t *testing.T) {
			s := newDRWServer(t, &drwServer{states: []string{tc.state}})
			stdout, stderr, err := drwRun("--watch", "-o", tc.format)
			drwRequireExit(t, err, tc.code, stderr)
			if stdout != tc.stdout {
				t.Errorf("stdout = %q, want %q", stdout, tc.stdout)
			}
			if !strings.HasPrefix(stderr, "waiting for dry-run-job 42 to finish; resume with:") {
				t.Errorf("stderr = %q, want the wait banner before the initial read", stderr)
			}
			if _, dials, _ := s.counts(); dials != 0 {
				t.Errorf("dials = %d, want 0", dials)
			}
		})
	}
}

func TestWatchDryRunJobNotFoundExitsThreeWithoutDialing(t *testing.T) {
	s := newDRWServer(t, &drwServer{restStatus: http.StatusNotFound})
	_, stderr, err := drwRun("--watch")
	drwRequireExit(t, err, qerrors.ExitNotFound, stderr)
	if _, dials, _ := s.counts(); dials != 0 {
		t.Errorf("dials = %d, want 0", dials)
	}
}

func TestWatchDryRunJobStreamRejectedExitsFour(t *testing.T) {
	t.Run("handshake 403", func(t *testing.T) {
		newDRWServer(t, &drwServer{states: []string{"running"}, handshake: http.StatusForbidden})
		_, stderr, err := drwRun("--watch")
		drwRequireExit(t, err, qerrors.ExitUnauthorized, stderr)
	})
	t.Run("close 1008 mid-stream", func(t *testing.T) {
		newDRWServer(t, &drwServer{
			states:      []string{"running"},
			frames:      []string{`{"state": "running"}`},
			closeCode:   websocket.ClosePolicyViolation,
			closeReason: "Authentication expired or revoked.",
		})
		_, stderr, err := drwRun("--watch")
		drwRequireExit(t, err, qerrors.ExitUnauthorized, stderr)
		if !strings.Contains(err.Error(), "Authentication expired or revoked.") {
			t.Errorf("error should quote the close reason, got: %v", err)
		}
	})
}

// A stream the server closes before a terminal frame is settled by reading the
// job again.
func TestWatchDryRunJobNormalCloseRereads(t *testing.T) {
	t.Run("now finished", func(t *testing.T) {
		newDRWServer(t, &drwServer{
			states:    []string{"running", "finished"},
			frames:    []string{`{"state": "running"}`},
			closeCode: websocket.CloseNormalClosure,
		})
		stdout, stderr, err := drwRun("--watch")
		drwRequireExit(t, err, 0, stderr)
		// The state the re-read found is written like a frame.
		if want := "dry-run-job 42: running\ndry-run-job 42: finished\n"; stdout != want {
			t.Errorf("stdout = %q, want %q", stdout, want)
		}
	})
	t.Run("still running", func(t *testing.T) {
		newDRWServer(t, &drwServer{
			states:    []string{"running"},
			frames:    []string{`{"state": "running"}`},
			closeCode: websocket.CloseNormalClosure,
		})
		_, stderr, err := drwRun("--watch")
		drwRequireExit(t, err, 1, stderr)
		want := "the server closed the stream while dry-run-job 42 is still running\n\nResume with: qctl get dry-run-job 42 --watch"
		if err.Error() != want {
			t.Errorf("error = %q, want %q", err, want)
		}
	})
}

func TestWatchDryRunJobTimeoutExits124WithAResumeLine(t *testing.T) {
	newDRWServer(t, &drwServer{states: []string{"running"}, frames: []string{`{"state": "running"}`}})
	_, stderr, err := drwRun("--watch", "--timeout", "50ms")
	drwRequireExit(t, err, qerrors.ExitTimeout, stderr)
	want := "timed out after 50ms: dry-run-job 42 is still running; the job was not stopped\n\nResume with: qctl get dry-run-job 42 --timeout 50ms --watch"
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}
}

// A connection the server drops is redialed, and the redial is announced on
// stderr at the default verbosity.
func TestWatchDryRunJobAnnouncesAReconnect(t *testing.T) {
	newDRWServer(t, &drwServer{states: []string{"running"}, frames: []string{`{"state": "running"}`},
		closeCode: websocket.CloseInternalServerErr, closeReason: "boom"})
	_, stderr, err := drwRun("--watch", "--timeout", "500ms")
	drwRequireExit(t, err, qerrors.ExitTimeout, stderr)
	want := "connection lost (websocket: close 1011 (internal server error): boom); reconnecting in 1s (attempt 1)\n"
	if !strings.Contains(stderr, want) {
		t.Errorf("stderr should announce the reconnect with %q\ngot:\n%s", want, stderr)
	}
}

func TestWatchDryRunJobTimeoutBadUsageExitsTwo(t *testing.T) {
	for _, args := range [][]string{
		{"--watch", "--timeout", "-1s"},
		{"--timeout", "5m"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			s := newDRWServer(t, &drwServer{states: []string{"running"}})
			_, stderr, err := drwRun(args...)
			drwRequireExit(t, err, qerrors.ExitBadUsage, stderr)
			if reads, dials, _ := s.counts(); reads != 0 || dials != 0 {
				t.Errorf("REST reads = %d, dials = %d; want none", reads, dials)
			}
		})
	}
}

func TestWatchDryRunJobCancelsEveryRESTRead(t *testing.T) {
	for _, tc := range []struct {
		name   string
		server *drwServer
	}{
		{"initial read", &drwServer{states: []string{"running"}, stallRead: 1}},
		{"terminal frame read", &drwServer{states: []string{"running", "error"}, frames: []string{`{"state":"error"}`}, stallRead: 2}},
		{"normal close read", &drwServer{states: []string{"running"}, closeCode: websocket.CloseNormalClosure, stallRead: 2}},
		{"blocker lookup", &drwServer{states: []string{"running", "blocked"}, frames: []string{`{"state":"blocked"}`}, stallIngestion: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			newDRWServer(t, tc.server)
			start := time.Now()
			_, stderr, err := drwRun("--watch", "--timeout", "100ms", "-o", "json")
			want := qerrors.ExitTimeout
			if tc.name == "terminal frame read" || tc.name == "blocker lookup" {
				want = qerrors.ExitGenericError
			}
			drwRequireExit(t, err, want, stderr)
			if time.Since(start) > 5*time.Second {
				t.Errorf("wait outlived its deadline: %s", time.Since(start))
			}
		})
	}
}
