package get

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/stats"
	"github.com/qlustered/qctl/internal/testutil"
)

// scriptedFetch returns each response in turn, repeating the last one forever.
// Errors are expressed by putting a non-nil error at the same index.
type scriptedFetch struct {
	responses []*stats.MenuStats
	errs      []error
	calls     int
}

func (s *scriptedFetch) next(context.Context) (*stats.MenuStats, error) {
	i := s.calls
	if i >= len(s.responses) {
		i = len(s.responses) - 1
	}
	s.calls++
	if i < len(s.errs) && s.errs[i] != nil {
		return nil, s.errs[i]
	}
	return s.responses[i], nil
}

func menuStats(alertID int, unresolved int) *stats.MenuStats {
	msg := "new column `region` needs review"
	alertType := api.AlertType("needs_review_migration")
	out := &stats.MenuStats{
		UnresolvedAlertsCount: unresolved,
		LatestAlertItemMsg:    &msg,
		LatestAlertItemType:   &alertType,
	}
	if alertID != 0 {
		id := alertID
		out.LatestAlertItemID = &id
	}
	return out
}

func testWatchOptions(format string, exitOnChange bool) watchOptions {
	return watchOptions{
		render:       renderOptions{tableID: 42, tableName: "orders", format: format},
		interval:     time.Millisecond,
		exitOnChange: exitOnChange,
	}
}

func testDeps(script *scriptedFetch, out, errOut io.Writer) watchDeps {
	return watchDeps{
		fetch:  script.next,
		out:    out,
		errOut: errOut,
		now:    func() time.Time { return time.Date(2026, 7, 30, 14, 7, 44, 0, time.UTC) },
	}
}

// jawWithin is a context done after d, the watch loop's whole budget in a test.
func jawWithin(t *testing.T, d time.Duration) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	t.Cleanup(cancel)
	return ctx
}

func TestWatchEmitsOnChangedAlertID(t *testing.T) {
	script := &scriptedFetch{responses: []*stats.MenuStats{
		menuStats(4821, 2),
		menuStats(4999, 3),
	}}
	var out, errOut bytes.Buffer
	if err := runWatchLoop(jawWithin(t, 2*time.Second), testDeps(script, &out, &errOut), testWatchOptions("table", true)); err != nil {
		t.Fatalf("runWatchLoop: %v", err)
	}
	if !strings.Contains(out.String(), "4999") {
		t.Errorf("want the new alert id in output\ngot:\n%s", out.String())
	}
}

func TestWatchEmitsOnChangedCountAlone(t *testing.T) {
	script := &scriptedFetch{responses: []*stats.MenuStats{
		menuStats(4821, 2),
		menuStats(4821, 5),
	}}
	var out, errOut bytes.Buffer
	if err := runWatchLoop(jawWithin(t, 2*time.Second), testDeps(script, &out, &errOut), testWatchOptions("table", true)); err != nil {
		t.Fatalf("runWatchLoop: %v", err)
	}
	if !strings.Contains(out.String(), "5 unresolved") {
		t.Errorf("a moved count is a change even with the same alert\ngot:\n%s", out.String())
	}
}

// TestWatchEmitsNothingWhenIdentical is the regression test for the whole
// change-detection rule: identical consecutive responses are not an event, no
// matter how the counts compare to anything else.
func TestWatchEmitsNothingWhenIdentical(t *testing.T) {
	script := &scriptedFetch{responses: []*stats.MenuStats{menuStats(4821, 2)}}
	var out, errOut bytes.Buffer
	err := runWatchLoop(jawWithin(t, 30*time.Millisecond), testDeps(script, &out, &errOut), testWatchOptions("table", true))
	if err == nil || !strings.Contains(err.Error(), "no change") {
		t.Fatalf("runWatchLoop = %v, want the loop's report that nothing changed", err)
	}
	if out.Len() != 0 {
		t.Errorf("stdout must be empty when nothing changed\ngot:\n%s", out.String())
	}
	if script.calls < 2 {
		t.Errorf("want at least 2 polls, got %d", script.calls)
	}
}

func TestWatchExitOnChangeStopsAfterOneEvent(t *testing.T) {
	script := &scriptedFetch{responses: []*stats.MenuStats{
		menuStats(4821, 2),
		menuStats(4999, 3),
		menuStats(5000, 4),
	}}
	var out, errOut bytes.Buffer
	if err := runWatchLoop(jawWithin(t, 2*time.Second), testDeps(script, &out, &errOut), testWatchOptions("table", true)); err != nil {
		t.Fatalf("runWatchLoop: %v", err)
	}
	if strings.Contains(out.String(), "5000") {
		t.Errorf("--exit-on-change must stop after the first event\ngot:\n%s", out.String())
	}
}

// The loop waits on ctx, not on the interval: cancelling it ends a watch whose
// next poll is an hour away.
func TestWatchLoopReturnsWhenCtxIsCancelled(t *testing.T) {
	script := &scriptedFetch{responses: []*stats.MenuStats{menuStats(4821, 2)}}
	opts := testWatchOptions("table", false)
	opts.interval = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	var out, errOut bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- runWatchLoop(ctx, testDeps(script, &out, &errOut), opts) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("a stream watch ended by ctx returns nil, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runWatchLoop did not return after ctx was cancelled")
	}
}

// A change that arrives after the budget is spent is not reported: stdout
// stays empty and the loop says nothing changed, so a 124 never follows a
// printed change.
func TestWatchDiscardsAPollThatReturnsAfterCtxIsDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	script := &scriptedFetch{responses: []*stats.MenuStats{menuStats(4821, 2), menuStats(4999, 3)}}
	var out, errOut bytes.Buffer
	deps := testDeps(script, &out, &errOut)
	deps.fetch = func(context.Context) (*stats.MenuStats, error) {
		if script.calls == 1 {
			cancel() // the second poll returns after ctx is done
		}
		return script.next(ctx)
	}
	if err := runWatchLoop(ctx, deps, testWatchOptions("table", true)); !errors.Is(err, context.Canceled) {
		t.Errorf("runWatchLoop = %v, want cancellation without a change", err)
	}
	if out.Len() != 0 {
		t.Errorf("a late change must not be printed, got %q", out.String())
	}

	// A permanent failure from that poll keeps its exit code; a transient one
	// ends the watch as a timeout would.
	for _, tc := range []struct {
		err  error
		code int
	}{
		{qerrors.New(qerrors.ExitUnauthorized, "token expired"), qerrors.ExitUnauthorized},
		{errors.New("connection reset"), qerrors.ExitTimeout},
	} {
		ctx, cancel := context.WithCancel(context.Background())
		script := &scriptedFetch{responses: []*stats.MenuStats{menuStats(4821, 2), nil}, errs: []error{nil, tc.err}}
		deps := testDeps(script, &out, &errOut)
		deps.fetch = func(context.Context) (*stats.MenuStats, error) {
			if script.calls == 1 {
				cancel()
			}
			return script.next(ctx)
		}
		err := runWatchLoop(ctx, deps, testWatchOptions("table", true))
		cancel()
		if tc.code == qerrors.ExitTimeout {
			if err == nil || !strings.Contains(err.Error(), "no change") || !errors.Is(err, context.Canceled) {
				t.Errorf("%v: runWatchLoop = %v, want the report that nothing changed over ctx's cause", tc.err, err)
			}
			continue
		}
		if got := qerrors.GetExitCode(err); got != tc.code {
			t.Errorf("%v: exit code = %d (err %v), want %d", tc.err, got, err, tc.code)
		}
	}
}

func TestWatchJSONEmitsNDJSON(t *testing.T) {
	script := &scriptedFetch{responses: []*stats.MenuStats{
		menuStats(4821, 2),
		menuStats(4999, 3),
	}}
	var out, errOut bytes.Buffer
	if err := runWatchLoop(jawWithin(t, 30*time.Millisecond), testDeps(script, &out, &errOut), testWatchOptions("json", false)); err != nil {
		t.Fatalf("runWatchLoop: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	for i, line := range lines {
		var payload latestAlertPayload
		if err := json.Unmarshal([]byte(line), &payload); err != nil {
			t.Fatalf("line %d is not one JSON object: %v\ngot: %s", i, err, line)
		}
	}
}

func TestWatchSurvivesTransientErrorButNotAuth(t *testing.T) {
	transient := errors.New("connection reset")
	script := &scriptedFetch{
		responses: []*stats.MenuStats{menuStats(4821, 2), nil, menuStats(4999, 3)},
		errs:      []error{nil, transient, nil},
	}
	var out, errOut bytes.Buffer
	if err := runWatchLoop(jawWithin(t, 2*time.Second), testDeps(script, &out, &errOut), testWatchOptions("table", true)); err != nil {
		t.Fatalf("a transient failure must not end the watch: %v", err)
	}
	if !strings.Contains(out.String(), "4999") {
		t.Errorf("want the watch to recover and report the change\ngot:\n%s", out.String())
	}

	authErr := qerrors.New(qerrors.ExitUnauthorized, "token expired")
	script = &scriptedFetch{
		responses: []*stats.MenuStats{menuStats(4821, 2), nil},
		errs:      []error{nil, authErr},
	}
	out.Reset()
	errOut.Reset()
	err := runWatchLoop(jawWithin(t, 2*time.Second), testDeps(script, &out, &errOut), testWatchOptions("table", true))
	if err == nil {
		t.Fatal("a 401 must end the watch")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitUnauthorized {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitUnauthorized)
	}
}

// jawRunLatestAlert executes `get latest-alert` with args against a mock
// server whose menu-stats endpoint is serve, and returns stdout and stderr
// apart.
func jawRunLatestAlert(t *testing.T, serve http.HandlerFunc, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/menu-stats", serve)

	cmd := setupLatestAlertTestCommand()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(append([]string{"get", "latest-alert"}, args...))
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

// jawServeMenuStats answers every menu-stats request with menuStatsWithAlert.
func jawServeMenuStats(w http.ResponseWriter, _ *http.Request) {
	testutil.RespondJSON(w, http.StatusOK, menuStatsWithAlert())
}

// With --exit-on-change the watch waits for one thing, so running out of time
// is a failure: exit 124, nothing on stdout, and one message that says no
// change came and how to resume.
func TestWatchExitOnChangeTimeoutExits124WithEmptyStdout(t *testing.T) {
	stdout, stderr, err := jawRunLatestAlert(t, jawServeMenuStats,
		"--table-id", "42", "--watch", "--exit-on-change", "--timeout", "50ms")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitTimeout {
		t.Fatalf("exit code = %d (err %v), want %d", code, err, qerrors.ExitTimeout)
	}
	if stdout != "" {
		t.Errorf("stdout must stay empty\ngot:\n%s", stdout)
	}
	want := "timed out after 50ms: no change to the latest alert on table id 42\n\n" +
		"Resume with: qctl get latest-alert --exit-on-change --table-id 42 --timeout 50ms --watch"
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
	wantBanner := "waiting for the latest alert on table id 42 to change, checking every 3s, for up to 50ms; " +
		"resume with: qctl get latest-alert --exit-on-change --table-id 42 --timeout 50ms --watch\n"
	if stderr != wantBanner {
		t.Errorf("stderr = %q, want only the banner %q", stderr, wantBanner)
	}
}

// TestWatchWithoutExitOnChangePrintsBaselineBeforeQuietTimeout guards a claim
// that used to be wrong in the docs: without --exit-on-change, the baseline
// snapshot is written to stdout at the start of the watch, and a watch that
// runs out of time exits 0 with one stderr line saying so.
func TestWatchWithoutExitOnChangePrintsBaselineBeforeQuietTimeout(t *testing.T) {
	stdout, stderr, err := jawRunLatestAlert(t, jawServeMenuStats,
		"--table-id", "42", "--watch", "--timeout", "50ms")
	if err != nil {
		t.Fatalf("a stream watch that runs out of time exits 0, got %v (exit %d)", err, qerrors.GetExitCode(err))
	}
	if !strings.Contains(stdout, "4821") {
		t.Errorf("the baseline snapshot must be on stdout even without --exit-on-change\ngot:\n%s", stdout)
	}
	want := "watching the latest alert on table id 42 every 3s, for up to 50ms; " +
		"resume with: qctl get latest-alert --table-id 42 --timeout 50ms --watch\n" +
		"timed out after 50ms; resume with: qctl get latest-alert --table-id 42 --timeout 50ms --watch\n"
	if stderr != want {
		t.Errorf("stderr = %q, want the banner and one timeout line %q", stderr, want)
	}
}

// The banner goes to stderr, so a piped -o json stream is NDJSON only.
func TestWatchJSONStdoutIsNDJSONOnly(t *testing.T) {
	stdout, stderr, err := jawRunLatestAlert(t, jawServeMenuStats,
		"--table-id", "42", "--watch", "--timeout", "50ms", "-o", "json")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for i, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		var payload latestAlertPayload
		if err := json.Unmarshal([]byte(line), &payload); err != nil {
			t.Fatalf("stdout line %d is not one JSON object: %v\ngot: %s", i, err, line)
		}
	}
	if !strings.HasPrefix(stderr, "watching the latest alert on table id 42") {
		t.Errorf("the banner belongs on stderr\ngot:\n%s", stderr)
	}
}

// --timeout means nothing without --watch, and saying so beats ignoring it.
func TestLatestAlertTimeoutRequiresWatch(t *testing.T) {
	for _, args := range [][]string{
		{"--table-id", "42", "--timeout", "1m"},
		{"--table-id", "42", "--watch", "--timeout=-1s"},
	} {
		_, _, err := jawRunLatestAlert(t, jawServeMenuStats, args...)
		if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
			t.Errorf("%v: exit code = %d (err %v), want %d", args, code, err, qerrors.ExitBadUsage)
		}
	}
}

// SIGTERM is no longer swallowed: the watch ends as interrupted, which exits
// 143. The signal is sent from inside the first poll, once the wait is armed;
// the test never calls qerrors.Exit, so nothing re-raises it here.
func TestLatestAlertWatchSIGTERMExits143(t *testing.T) {
	cmdutil.SetInterruptGrace(0)
	t.Cleanup(cmdutil.ResetInterruptGrace)
	if runtime.GOOS == "windows" || signal.Ignored(syscall.SIGTERM) {
		t.Skip("SIGTERM cannot be delivered to this process")
	}
	var sendErr error
	sent := false
	handler := func(w http.ResponseWriter, r *http.Request) {
		if !sent {
			sent = true
			p, err := os.FindProcess(os.Getpid())
			if err == nil {
				err = p.Signal(syscall.SIGTERM)
			}
			sendErr = err
		}
		jawServeMenuStats(w, r)
	}
	_, _, err := jawRunLatestAlert(t, handler, "--table-id", "42", "--watch", "--exit-on-change", "--timeout", "1m")
	if sendErr != nil {
		t.Fatalf("sending SIGTERM: %v", sendErr)
	}
	var interrupted *qerrors.Interrupted
	if !errors.As(err, &interrupted) || interrupted.Signal != syscall.SIGTERM {
		t.Fatalf("err = %v, want a *qerrors.Interrupted for SIGTERM", err)
	}
	if code := qerrors.GetExitCode(err); code != 143 {
		t.Errorf("exit code = %d, want 143", code)
	}
	if want := "interrupted; resume with: qctl get latest-alert --exit-on-change --table-id 42 --timeout 1m0s --watch"; interrupted.Message != want {
		t.Errorf("Message = %q, want %q", interrupted.Message, want)
	}
}

func TestBackoffGrowsAndCaps(t *testing.T) {
	base := 3 * time.Second
	if got := backoffFor(base, 0); got != base {
		t.Errorf("backoffFor(0) = %v, want %v", got, base)
	}
	if got := backoffFor(base, 1); got != 6*time.Second {
		t.Errorf("backoffFor(1) = %v, want 6s", got)
	}
	if got := backoffFor(base, 20); got != 30*time.Second {
		t.Errorf("backoffFor(20) = %v, want the 30s cap", got)
	}
}

// TestBackoffNeverShortensBelowBase is the regression test for a real bug: the
// 30s ceiling used to be absolute, so a --watch-interval above 15s meant one
// failed poll would HALVE the user's configured interval (e.g. base 60s,
// failures 1 -> 30s) and every failure after that stayed there. The effective
// ceiling must be max(base, 30s), so a slow poll of a failing server is never
// faster than the healthy-server interval the user asked for.
func TestBackoffNeverShortensBelowBase(t *testing.T) {
	base := 60 * time.Second
	if got := backoffFor(base, 0); got != base {
		t.Errorf("backoffFor(base above ceiling, 0) = %v, want %v", got, base)
	}
	if got := backoffFor(base, 1); got != base {
		t.Errorf("backoffFor(base above ceiling, 1) = %v, want %v (not the 30s cap)", got, base)
	}
}
