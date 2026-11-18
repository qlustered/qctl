package inspect

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

// drwServer is a fake API serving one dry-run job, 42, over REST, over its
// progress websocket, and as a compact preview.
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
	// stream, and otherwise the stream stays open and silent.
	frames    []string
	onStream  func()
	closeCode int

	restReads, dials, previewReads int
}

func newDRWServer(t *testing.T, s *drwServer) *drwServer {
	t.Helper()
	s.t = t
	base := "/api/orgs/" + testOrgID
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+base+"/dry-runs/42", s.serveJob)
	mux.HandleFunc("GET "+base+"/dry-runs/42/preview/compact", s.servePreview)
	mux.HandleFunc(base+"/ws/dry-runs/42", s.serveStream)
	s.srv = httptest.NewServer(mux)
	t.Cleanup(s.srv.Close)

	t.Setenv("QCTL_WATCH_TIMEOUT", "")
	env := testutil.NewTestEnv(t)
	endpointKey, _ := config.NormalizeEndpointKey(s.srv.URL)
	env.SetupConfigWithOrg(s.srv.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")
	return s
}

func (s *drwServer) serveJob(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.restReads++
	if s.restStatus != 0 {
		testutil.RespondJSON(w, s.restStatus, map[string]any{"detail": "Dry Run Job ID 42 can not be found."})
		return
	}
	testutil.RespondJSON(w, http.StatusOK, map[string]any{
		"id": 42, "dataset_id": 10, "state": s.states[min(s.restReads, len(s.states))-1],
		"rule_run_specs": []map[string]any{{"position": 0, "dataset_rule_id": nil}},
		"sampling":       map[string]any{"alpha_pref": 0, "cap": 0, "eligible_c": 0, "eligible_q": 0, "pins_inserted": 0, "pins_missing": 0, "pins_truncated": 0},
		"metrics":        nil,
	})
}

func (s *drwServer) servePreview(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	s.previewReads++
	s.mu.Unlock()
	testutil.RespondJSON(w, http.StatusOK, map[string]any{
		"dry_run_job_id": 42, "state": "finished",
		"stats": map[string]any{"total_rows_processed": 100, "rows_with_state_change": 5, "rows_with_value_change": 3, "net_quarantine_change": 0,
			"left_stats": map[string]any{"rule_index": 0, "total_rows": 100, "clean_count": 95, "quarantine_count": 5, "failure_rate": 0.05}},
		"repro": map[string]any{"snapshot_id": 1},
	})
}

func (s *drwServer) serveStream(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.dials++
	handshake, frames, closeCode := s.handshake, s.frames, s.closeCode
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
		_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(closeCode, ""))
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

func (s *drwServer) counts() (restReads, dials, previewReads int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.restReads, s.dials, s.previewReads
}

// drwRun runs `qctl inspect dry-run-job 42 <args>` and returns stdout, stderr
// and the error.
func drwRun(args ...string) (string, string, error) {
	cmd := setupInspectTestCommand()
	// As the real root does, so stdout and stderr hold only the command's own.
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	cmd.SetArgs(append([]string{"inspect", "dry-run-job", "42"}, args...))
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

// A finished job's preview goes to stdout, its progress to stderr.
func TestFollowDryRunJobFinishedPrintsThePreview(t *testing.T) {
	for _, tc := range []struct {
		name   string
		args   []string
		banner string
	}{
		{"default budget", nil, "waiting for dry-run-job 42 to finish, for up to 10m0s; resume with: qctl inspect dry-run-job 42 --follow\n"},
		// --timeout 0 is no limit; it used to fail the dial at once.
		{"--timeout 0", []string{"--timeout", "0"}, "waiting for dry-run-job 42 to finish; resume with: qctl inspect dry-run-job 42 --follow --timeout 0s\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newDRWServer(t, &drwServer{
				states:    []string{"running"},
				frames:    []string{`{"state": "running"}`, `{"state": "finished"}`},
				closeCode: websocket.CloseNormalClosure,
			})
			stdout, stderr, err := drwRun(append([]string{"--follow"}, tc.args...)...)
			drwRequireExit(t, err, 0, stderr)
			if want := tc.banner + "dry-run-job 42: running\ndry-run-job 42: finished\n"; stderr != want {
				t.Errorf("stderr = %q, want %q", stderr, want)
			}
			if !strings.Contains(stdout, `"total_rows_processed": 100`) {
				t.Errorf("stdout should hold the preview, got: %s", stdout)
			}
			if _, dials, previews := s.counts(); dials != 1 || previews != 1 {
				t.Errorf("dials = %d, preview reads = %d; want 1 and 1", dials, previews)
			}
		})
	}
}

// Every terminal state but finished exits 1 with a Next line and no preview.
func TestFollowDryRunJobFailedStatesPrintNoPreview(t *testing.T) {
	for _, tc := range []struct{ state, next string }{
		{"error", "Next: qctl describe dry-run-job 42"},
		{"killed", "Next: qctl describe dry-run-job 42"},
		{"stopped", "Next: qctl describe dry-run-job 42"},
		{"file_missing", "Next: qctl describe dry-run-job 42"},
		{"blocked", "Next: qctl describe dry-run-job 42"}, // the job names no ingestion job here
	} {
		t.Run(tc.state, func(t *testing.T) {
			s := newDRWServer(t, &drwServer{
				states: []string{"running", tc.state},
				frames: []string{`{"state": "running"}`, `{"state": "` + tc.state + `"}`},
			})
			stdout, stderr, err := drwRun("--follow", "--timeout", "3s")
			drwRequireExit(t, err, 1, stderr)
			if want := "dry-run-job 42 ended in state " + tc.state; !strings.HasPrefix(err.Error(), want) {
				t.Errorf("error should start with %q, got:\n%v", want, err)
			}
			if !strings.HasSuffix(err.Error(), tc.next) {
				t.Errorf("error should end with %q, got:\n%v", tc.next, err)
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want no preview", stdout)
			}
			if _, _, previews := s.counts(); previews != 0 {
				t.Errorf("preview reads = %d, want 0", previews)
			}
		})
	}
}

// A job already in a terminal state is judged without opening the stream.
func TestFollowDryRunJobAlreadyTerminalNeverDials(t *testing.T) {
	for _, tc := range []struct {
		state    string
		code     int
		previews int
	}{
		{"finished", 0, 1},
		{"error", 1, 0},
	} {
		t.Run(tc.state, func(t *testing.T) {
			s := newDRWServer(t, &drwServer{states: []string{tc.state}})
			_, stderr, err := drwRun("--follow")
			drwRequireExit(t, err, tc.code, stderr)
			if want := "waiting for dry-run-job 42 to finish, for up to 10m0s; resume with: qctl inspect dry-run-job 42 --follow\n" + "dry-run-job 42: " + tc.state + "\n"; stderr != want {
				t.Errorf("stderr = %q, want %q", stderr, want)
			}
			if _, dials, previews := s.counts(); dials != 0 || previews != tc.previews {
				t.Errorf("dials = %d, preview reads = %d; want 0 and %d", dials, previews, tc.previews)
			}
		})
	}
}

func TestFollowDryRunJobRefusals(t *testing.T) {
	for _, tc := range []struct {
		name  string
		s     *drwServer
		code  int
		dials int
	}{
		{"REST 404", &drwServer{restStatus: http.StatusNotFound}, qerrors.ExitNotFound, 0},
		{"handshake 403", &drwServer{states: []string{"running"}, handshake: http.StatusForbidden}, qerrors.ExitUnauthorized, 1},
		{"close 1008", &drwServer{states: []string{"running"}, frames: []string{`{"state": "running"}`},
			closeCode: websocket.ClosePolicyViolation}, qerrors.ExitUnauthorized, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newDRWServer(t, tc.s)
			stdout, stderr, err := drwRun("--follow")
			drwRequireExit(t, err, tc.code, stderr)
			if _, dials, previews := s.counts(); dials != tc.dials || previews != 0 || stdout != "" {
				t.Errorf("dials = %d, preview reads = %d, stdout = %q; want %d, 0 and nothing", dials, previews, stdout, tc.dials)
			}
		})
	}
}

// A stream the server closes before a terminal frame is settled by reading the
// job again.
func TestFollowDryRunJobNormalCloseRereads(t *testing.T) {
	t.Run("now finished", func(t *testing.T) {
		s := newDRWServer(t, &drwServer{
			states:    []string{"running", "finished"},
			frames:    []string{`{"state": "running"}`},
			closeCode: websocket.CloseNormalClosure,
		})
		_, stderr, err := drwRun("--follow")
		drwRequireExit(t, err, 0, stderr)
		if !strings.HasSuffix(stderr, "dry-run-job 42: running\ndry-run-job 42: finished\n") {
			t.Errorf("stderr = %q, want the re-read state last", stderr)
		}
		if _, _, previews := s.counts(); previews != 1 {
			t.Errorf("preview reads = %d, want 1", previews)
		}
	})
	t.Run("still running", func(t *testing.T) {
		newDRWServer(t, &drwServer{
			states:    []string{"running"},
			frames:    []string{`{"state": "running"}`},
			closeCode: websocket.CloseNormalClosure,
		})
		stdout, stderr, err := drwRun("--follow", "--view", "full")
		drwRequireExit(t, err, 1, stderr)
		want := "the server closed the stream while dry-run-job 42 is still running\n\nResume with: qctl inspect dry-run-job 42 --follow --view full"
		if err.Error() != want || stdout != "" {
			t.Errorf("error = %q, stdout = %q; want %q and nothing", err, stdout, want)
		}
	})
}

func TestFollowDryRunJobTimeoutExits124WithAResumeLine(t *testing.T) {
	newDRWServer(t, &drwServer{states: []string{"running"}, frames: []string{`{"state": "running"}`}})
	stdout, stderr, err := drwRun("--follow", "--timeout", "50ms")
	drwRequireExit(t, err, qerrors.ExitTimeout, stderr)
	want := "timed out after 50ms: dry-run-job 42 is still running; the job was not stopped\n\nResume with: qctl inspect dry-run-job 42 --follow --timeout 50ms"
	if err.Error() != want || stdout != "" {
		t.Errorf("error = %q, stdout = %q; want %q and nothing", err, stdout, want)
	}
}

// A connection the server drops is redialed, and the redial is announced on
// stderr at the default verbosity.
func TestFollowDryRunJobAnnouncesAReconnect(t *testing.T) {
	newDRWServer(t, &drwServer{states: []string{"running"}, frames: []string{`{"state": "running"}`},
		closeCode: websocket.CloseInternalServerErr})
	_, stderr, err := drwRun("--follow", "--timeout", "500ms")
	drwRequireExit(t, err, qerrors.ExitTimeout, stderr)
	want := "connection lost (websocket: close 1011 (internal server error)); reconnecting in 1s (attempt 1)\n"
	if !strings.Contains(stderr, want) {
		t.Errorf("stderr should announce the reconnect with %q\ngot:\n%s", want, stderr)
	}
}

func TestFollowDryRunJobTimeoutBadUsageExitsTwo(t *testing.T) {
	for _, args := range [][]string{
		{"--follow", "--timeout", "-1s"},
		{"--timeout", "5m"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			s := newDRWServer(t, &drwServer{states: []string{"running"}})
			_, stderr, err := drwRun(args...)
			drwRequireExit(t, err, qerrors.ExitBadUsage, stderr)
			if reads, dials, previews := s.counts(); reads+dials+previews != 0 {
				t.Errorf("REST reads = %d, dials = %d, preview reads = %d; want none", reads, dials, previews)
			}
		})
	}
}
