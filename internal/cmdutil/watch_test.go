package cmdutil

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// watchCmd is a command carrying --timeout with the given default, its
// flags parsed from args.
func watchCmd(t *testing.T, def time.Duration, args ...string) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "watch-thing"}
	AddWatchTimeoutFlag(cmd, def)
	if err := cmd.ParseFlags(args); err != nil {
		t.Fatalf("ParseFlags(%q) error = %v", args, err)
	}
	return cmd
}

func TestAddWatchTimeoutFlagHelpNamesTheDefault(t *testing.T) {
	for _, tc := range []struct {
		def      time.Duration
		defValue string
		help     string
	}{
		{10 * time.Minute, "10m0s", "(default 10m0s)"},
		{0, "0s", ""},
	} {
		cmd := &cobra.Command{Use: "x"}
		AddWatchTimeoutFlag(cmd, tc.def)
		f := cmd.Flags().Lookup("timeout")
		if want := "Stop waiting after this long; 0 means no limit (QCTL_WATCH_TIMEOUT overrides the default)"; f.Usage != want {
			t.Errorf("usage = %q, want %q", f.Usage, want)
		}
		if f.Value.Type() != "duration" || f.DefValue != tc.defValue {
			t.Errorf("--timeout is %s defaulting to %s, want a duration defaulting to %s", f.Value.Type(), f.DefValue, tc.defValue)
		}
		usage := cmd.Flags().FlagUsages()
		if tc.help != "" && !strings.Contains(usage, tc.help) {
			t.Errorf("help does not print %q: %s", tc.help, usage)
		}
		if tc.help == "" && strings.Contains(usage, "(default") {
			t.Errorf("help prints a default for no limit: %s", usage)
		}
	}
}

func TestWatchBudgetPrecedence(t *testing.T) {
	const unset = "<unset>"
	for _, tc := range []struct {
		name string
		def  time.Duration
		env  string
		args []string
		want time.Duration
	}{
		{"flag beats env", 0, "10m", []string{"--timeout", "5s"}, 5 * time.Second},
		{"flag beats the default", 10 * time.Minute, unset, []string{"--timeout", "30s"}, 30 * time.Second},
		{"explicit 0 is no limit", 10 * time.Minute, unset, []string{"--timeout", "0"}, 0},
		{"explicit 0 beats env", 0, "10m", []string{"--timeout=0s"}, 0},
		{"env beats the default", 10 * time.Minute, "90s", nil, 90 * time.Second},
		{"env beats a default of no limit", 0, "90s", nil, 90 * time.Second},
		{"env 0 is no limit", 10 * time.Minute, "0", nil, 0},
		{"empty env is unset", 10 * time.Minute, "", nil, 10 * time.Minute},
		{"default", 10 * time.Minute, unset, nil, 10 * time.Minute},
		{"default of no limit", 0, unset, nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.env == unset {
				t.Setenv("QCTL_WATCH_TIMEOUT", "")
				os.Unsetenv("QCTL_WATCH_TIMEOUT")
			} else {
				t.Setenv("QCTL_WATCH_TIMEOUT", tc.env)
			}
			got, err := WatchBudget(watchCmd(t, tc.def, tc.args...))
			if err != nil {
				t.Fatalf("WatchBudget() error = %v", err)
			}
			if got != tc.want {
				t.Errorf("WatchBudget() = %s, want %s", got, tc.want)
			}
		})
	}
}

// TestWatchBudgetIgnoresStdin: a reader a prompt could use does not change
// the budget.
func TestWatchBudgetIgnoresStdin(t *testing.T) {
	t.Setenv("QCTL_WATCH_TIMEOUT", "")
	SetReader(strings.NewReader(""))
	defer ResetReader()
	if got, err := WatchBudget(watchCmd(t, 0)); err != nil || got != 0 {
		t.Errorf("CanPrompt()=%v: WatchBudget() = %s, %v; want no limit", CanPrompt(), got, err)
	}
	ResetReader()
	if got, err := WatchBudget(watchCmd(t, 0)); err != nil || got != 0 {
		t.Errorf("CanPrompt()=%v: WatchBudget() = %s, %v; want no limit", CanPrompt(), got, err)
	}
}

func TestWatchBudgetRejectsBadValuesWithExitTwo(t *testing.T) {
	for _, tc := range []struct {
		name  string
		env   string
		args  []string
		names string
	}{
		{"negative flag", "", []string{"--timeout", "-5s"}, "--timeout"},
		{"negative flag even with a valid env", "10m", []string{"--timeout=-1m"}, "--timeout"},
		{"env without a unit", "60", nil, "QCTL_WATCH_TIMEOUT"},
		{"env garbage", "soon", nil, "QCTL_WATCH_TIMEOUT"},
		{"negative env", "-5s", nil, "QCTL_WATCH_TIMEOUT"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("QCTL_WATCH_TIMEOUT", tc.env)
			_, err := WatchBudget(watchCmd(t, 0, tc.args...))
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Fatalf("exit code = %d (err %v), want %d", got, err, qerrors.ExitBadUsage)
			}
			if !strings.Contains(err.Error(), tc.names) {
				t.Errorf("error %q does not name %s", err, tc.names)
			}
		})
	}
}

func TestWatchBudgetNeedsATimeoutFlag(t *testing.T) {
	t.Setenv("QCTL_WATCH_TIMEOUT", "")
	if _, err := WatchBudget(&cobra.Command{Use: "bare"}); err == nil {
		t.Error("no error for a command without --timeout")
	}
}

// startWait runs StartWait on a bare command and returns its stderr.
func startWait(t *testing.T, spec WaitSpec) (context.Context, *Waiter, *bytes.Buffer) {
	t.Helper()
	var stderr bytes.Buffer
	cmd := &cobra.Command{Use: "x"}
	cmd.SetErr(&stderr)
	ctx, w := StartWait(cmd, spec)
	t.Cleanup(w.Stop)
	return ctx, w, &stderr
}

func TestStartWaitBanner(t *testing.T) {
	for _, tc := range []struct {
		name string
		spec WaitSpec
		want string
	}{
		{"no budget, no next", WaitSpec{Kind: WatchStream, Doing: "watching ingestion jobs every 5s"},
			"watching ingestion jobs every 5s\n"},
		{"budget and next", WaitSpec{Budget: 90 * time.Second, Doing: "waiting for dry-run-job 7 to finish (now running)",
			Next: "qctl get dry-run-job 7 --watch"},
			"waiting for dry-run-job 7 to finish (now running), for up to 1m30s; resume with: qctl get dry-run-job 7 --watch\n"},
		{"self-timed budget is printed", WaitSpec{Budget: 15 * time.Minute, SelfTimed: true, Doing: "waiting for the export"},
			"waiting for the export, for up to 15m0s\n"},
		{"check with", WaitSpec{Doing: "waiting for alert 3", Next: "qctl get latest-alert", NextVerb: "check with"},
			"waiting for alert 3; check with: qctl get latest-alert\n"},
		{"note", WaitSpec{Budget: time.Minute, Doing: "waiting for the export", Next: "qctl download data orders",
			Note: "re-running this command queues the export again"},
			"waiting for the export, for up to 1m0s; resume with: qctl download data orders; re-running this command queues the export again\n"},
		{"note without next", WaitSpec{Doing: "waiting", Note: "nothing to resume"}, "waiting; nothing to resume\n"},
		{"sanitized", WaitSpec{Doing: "waiting for job \x1b[2J"}, "waiting for job \\x1b[2J\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, stderr := startWait(t, tc.spec)
			if got := stderr.String(); got != tc.want {
				t.Errorf("banner = %q, want %q", got, tc.want)
			}
		})
	}
}

// awaitDone fails the test if done is not closed within a few seconds.
func awaitDone(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("not done")
	}
}

func TestEndedStreamTimeoutIsCleanWithAResumeLine(t *testing.T) {
	for _, loopErr := range []error{nil, context.DeadlineExceeded, fmt.Errorf("fetch: %w", context.DeadlineExceeded)} {
		ctx, w, stderr := startWait(t, WaitSpec{Kind: WatchStream, Budget: 10 * time.Millisecond,
			Doing: "watching ingestion jobs every 5s", Next: "qctl get ingestion-jobs --watch"})
		awaitDone(t, ctx.Done())
		if err := w.Ended(loopErr); err != nil {
			t.Errorf("Ended(%v) = %v, want nil", loopErr, err)
		}
		want := "watching ingestion jobs every 5s, for up to 10ms; resume with: qctl get ingestion-jobs --watch\n" +
			"timed out after 10ms; resume with: qctl get ingestion-jobs --watch\n"
		if got := stderr.String(); got != want {
			t.Errorf("stderr = %q, want %q", got, want)
		}
	}
}

// TestEndedAtTheDeadlineKeepsAnOutcomeTheLoopReached: a loop that failed or
// finished as the budget ran out did not give up, so its result stands.
func TestEndedAtTheDeadlineKeepsAnOutcomeTheLoopReached(t *testing.T) {
	rejected := qerrors.NewUnauthorized("stream closed: Authentication expired or revoked.")
	for _, tc := range []struct {
		name    string
		kind    WaitKind
		loopErr error
	}{
		{"stream rejected", WatchStream, rejected},
		{"wait rejected", WaitUntilDone, rejected},
		{"wait failed", WaitUntilDone, errors.New("dry-run-job 7 ended in error")},
		{"wait finished", WaitUntilDone, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, w, stderr := startWait(t, WaitSpec{Kind: tc.kind, Budget: 10 * time.Millisecond, Next: "qctl x"})
			awaitDone(t, ctx.Done())
			stderr.Reset()
			if err := w.Ended(tc.loopErr); err != tc.loopErr {
				t.Errorf("Ended(%v) = %v, want the loop's own result", tc.loopErr, err)
			}
			if stderr.Len() != 0 {
				t.Errorf("Ended wrote %q to stderr", stderr)
			}
		})
	}
}

func TestEndedStreamTimeoutWithoutNext(t *testing.T) {
	ctx, w, stderr := startWait(t, WaitSpec{Kind: WatchStream, Budget: 10 * time.Millisecond, Doing: "watching"})
	awaitDone(t, ctx.Done())
	if err := w.Ended(nil); err != nil {
		t.Fatalf("Ended() = %v", err)
	}
	if got, want := stderr.String(), "watching, for up to 10ms\ntimed out after 10ms\n"; got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
}

var errStillRunning = errors.New("dry-run-job 7 is still running; the job was not stopped")

// stillRunning is errStillRunning as a loop gives up with it, wrapping ctx's
// cause.
func stillRunning(ctx context.Context) error {
	return qerrors.Caused(errStillRunning.Error(), context.Cause(ctx))
}

func TestEndedWaitTimeoutExits124(t *testing.T) {
	const doing = "waiting for dry-run-job 7 to finish (now running)"
	for _, tc := range []struct {
		name    string
		spec    WaitSpec
		loopErr func(ctx context.Context) error
		want    string
	}{
		{"loop description and next",
			WaitSpec{Next: "qctl get dry-run-job 7 --watch"},
			func(ctx context.Context) error { return fmt.Errorf("wrapped: %w", stillRunning(ctx)) },
			"timed out after 10ms: wrapped: dry-run-job 7 is still running; the job was not stopped\n\n" +
				"Resume with: qctl get dry-run-job 7 --watch"},
		{"check with",
			WaitSpec{Next: "qctl get dry-run-job 7", NextVerb: "check with"},
			stillRunning,
			"timed out after 10ms: dry-run-job 7 is still running; the job was not stopped\n\n" +
				"Check with: qctl get dry-run-job 7"},
		{"no next",
			WaitSpec{},
			stillRunning,
			"timed out after 10ms: dry-run-job 7 is still running; the job was not stopped"},
		{"the context's error is described from Doing",
			WaitSpec{Doing: doing, Next: "qctl get dry-run-job 7 --watch"},
			func(ctx context.Context) error { return ctx.Err() },
			"timed out after 10ms: still waiting for dry-run-job 7 to finish (now running)\n\n" +
				"Resume with: qctl get dry-run-job 7 --watch"},
		{"the context's cause is described from Doing",
			WaitSpec{Doing: doing},
			context.Cause,
			"timed out after 10ms: still waiting for dry-run-job 7 to finish (now running)"},
		{"a wrapped context error keeps its message",
			WaitSpec{Doing: doing},
			func(ctx context.Context) error { return fmt.Errorf("poll: %w", ctx.Err()) },
			"timed out after 10ms: poll: context deadline exceeded"},
		{"the context's error and no Doing",
			WaitSpec{},
			func(ctx context.Context) error { return ctx.Err() },
			"timed out after 10ms: the wait did not finish"},
		{"no doubled phrase",
			WaitSpec{Next: "qctl x"},
			func(ctx context.Context) error {
				return fmt.Errorf("timed out after 10ms waiting for the export: %w", stillRunning(ctx))
			},
			"timed out after 10ms waiting for the export: dry-run-job 7 is still running; the job was not stopped\n\n" +
				"Resume with: qctl x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := tc.spec
			spec.Kind = WaitUntilDone
			spec.Budget = 10 * time.Millisecond
			ctx, w, stderr := startWait(t, spec)
			awaitDone(t, ctx.Done())
			stderr.Reset()

			loopErr := tc.loopErr(ctx)
			ended := w.Ended(loopErr)
			if ended == nil {
				t.Fatalf("Ended(%v) = nil, want a timeout", loopErr)
			}
			err := fmt.Errorf("command: %w", ended)
			if got := qerrors.GetExitCode(err); got != qerrors.ExitTimeout {
				t.Errorf("exit code = %d, want %d", got, qerrors.ExitTimeout)
			}
			if got := ended.Error(); got != tc.want {
				t.Errorf("message = %q, want %q", got, tc.want)
			}
			if !errors.Is(err, loopErr) {
				t.Error("errors.Is does not reach the loop's error")
			}
			if stderr.Len() != 0 {
				t.Errorf("Ended wrote %q to stderr; the error is printed once, by Exit", stderr)
			}
		})
	}
}

func TestEndedPassesThroughWithoutSignalOrDeadline(t *testing.T) {
	_, w, _ := startWait(t, WaitSpec{Kind: WaitUntilDone, Budget: time.Hour, Next: "qctl x"})
	if err := w.Ended(nil); err != nil {
		t.Errorf("Ended(nil) = %v", err)
	}
	if err := w.Ended(errStillRunning); err != errStillRunning {
		t.Errorf("Ended(err) = %v, want err itself", err)
	}
}

// TestSelfTimedWaitHasNoDeadline: the loop enforces a self-timed budget, so
// the context must not cut it short, and Ended leaves the loop's error alone.
func TestSelfTimedWaitHasNoDeadline(t *testing.T) {
	ctx, w, _ := startWait(t, WaitSpec{Budget: time.Millisecond, SelfTimed: true})
	if _, ok := ctx.Deadline(); ok {
		t.Error("self-timed wait's context has a deadline")
	}
	if err := w.Ended(errStillRunning); err != errStillRunning {
		t.Errorf("Ended(err) = %v, want err itself", err)
	}
}

// TestEndedIgnoresAParentDeadline: only the wait's own budget is a timeout;
// a deadline the command's context already carried is not this wait's.
func TestEndedIgnoresAParentDeadline(t *testing.T) {
	parent, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	cmd := &cobra.Command{Use: "x"}
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetContext(parent)
	ctx, w := StartWait(cmd, WaitSpec{Kind: WatchStream, Budget: time.Hour})
	defer w.Stop()
	awaitDone(t, ctx.Done())
	if err := w.Ended(errStillRunning); err != errStillRunning {
		t.Errorf("Ended(err) = %v, want err itself", err)
	}
}

func TestStartWaitSkipsIgnoredSignals(t *testing.T) {
	for _, tc := range []struct {
		name    string
		ignored []os.Signal
		want    []os.Signal
	}{
		{"none ignored", nil, []os.Signal{syscall.SIGINT, syscall.SIGTERM}},
		{"SIGINT ignored, as in a backgrounded job", []os.Signal{syscall.SIGINT}, []os.Signal{syscall.SIGTERM}},
		{"both ignored", []os.Signal{syscall.SIGINT, syscall.SIGTERM}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			orig := signalIgnored
			signalIgnored = func(s os.Signal) bool {
				for _, i := range tc.ignored {
					if s == i {
						return true
					}
				}
				return false
			}
			defer func() { signalIgnored = orig }()

			_, w, _ := startWait(t, WaitSpec{})
			if !reflect.DeepEqual(w.armed, tc.want) {
				t.Errorf("armed %v, want %v", w.armed, tc.want)
			}
			w.Stop()
			w.Stop() // idempotent
		})
	}
}

// resumeRoot is a stand-in for qctl's tree: root persistent flags like the
// real ones (a count among them) and a leaf with a flag of every shape
// RenderFlag handles. Its root is not named qctl, and ResumeCommand must
// still say qctl.
func resumeRoot(run func(*cobra.Command)) *cobra.Command {
	root := &cobra.Command{Use: "qctl.test"}
	root.PersistentFlags().StringP("output", "o", "table", "")
	root.PersistentFlags().StringP("org", "O", "", "")
	root.PersistentFlags().CountP("verbose", "v", "")
	get := &cobra.Command{Use: "get"}
	leaf := &cobra.Command{
		Use:  "dry-run-job",
		Args: cobra.ArbitraryArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			run(c)
			return nil
		},
	}
	leaf.Flags().Bool("watch", false, "")
	leaf.Flags().Bool("deleted", true, "")
	leaf.Flags().String("search", "", "")
	leaf.Flags().StringArray("state", nil, "")
	leaf.Flags().StringSlice("cols", nil, "")
	leaf.Flags().Int("limit", 100, "")
	leaf.Flags().Float64("ratio", 0, "")
	leaf.Flags().String("color", "auto", "")
	leaf.Flags().Lookup("color").NoOptDefVal = "always"
	AddWatchTimeoutFlag(leaf, 0)
	get.AddCommand(leaf)
	root.AddCommand(get)
	return root
}

// invocation is what a parsed command line amounted to.
type invocation struct {
	args  []string
	flags map[string]string
}

// runResume executes resumeRoot on args and returns ResumeCommand's line
// (with drop) and what the command saw.
func runResume(t *testing.T, args []string, drop ...string) (string, invocation) {
	t.Helper()
	var line string
	var seen invocation
	root := resumeRoot(func(c *cobra.Command) {
		line = ResumeCommand(c, drop...)
		seen = invocation{args: c.Flags().Args(), flags: map[string]string{}}
		c.Flags().Visit(func(f *pflag.Flag) { seen.flags[f.Name] = f.Value.String() })
	})
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute(%q) error = %v", args, err)
	}
	return line, seen
}

func TestResumeCommand(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		drop []string
		want string
	}{
		{"path, positional, sorted flags including inherited ones",
			[]string{"get", "dry-run-job", "7", "--watch", "-o", "json", "--org", "Acme Corp", "-vv", "--timeout", "5m"}, nil,
			"qctl get dry-run-job 7 --org 'Acme Corp' --output json --timeout 5m0s --verbose=2 --watch"},
		{"drop",
			[]string{"get", "dry-run-job", "7", "--watch", "--timeout", "5m", "-o", "yaml"}, []string{"watch", "timeout"},
			"qctl get dry-run-job 7 --output yaml"},
		{"nothing set", []string{"get", "dry-run-job"}, nil, "qctl get dry-run-job"},
		{"bools both ways", []string{"get", "dry-run-job", "--watch", "--deleted=false"}, nil,
			"qctl get dry-run-job --deleted=false --watch"},
		{"embedded single quote and empty string",
			[]string{"get", "dry-run-job", "--search", "it's", "--org", ""}, nil,
			`qctl get dry-run-job --org '' --search 'it'\''s'`},
		{"repeatable flags repeat",
			[]string{"get", "dry-run-job", "--state", "a b", "--state", "c", "--cols", "x,y"}, nil,
			"qctl get dry-run-job --cols x --cols y --state 'a b' --state c"},
		{"optional-value flag keeps its value attached",
			[]string{"get", "dry-run-job", "--color=never", "-v"}, nil,
			"qctl get dry-run-job --color=never --verbose=1"},
		{"numbers", []string{"get", "dry-run-job", "--limit", "-5", "--ratio", "0.5"}, nil,
			"qctl get dry-run-job --limit -5 --ratio 0.5"},
		{"a dash-led positional goes after --",
			[]string{"get", "dry-run-job", "--search", "x", "--", "-7"}, nil,
			"qctl get dry-run-job --search x -- -7"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := runResume(t, tc.args, tc.drop...)
			if got != tc.want {
				t.Errorf("ResumeCommand() =\n  %s\nwant\n  %s", got, tc.want)
			}
		})
	}
}

// TestResumeCommandReparses pastes ResumeCommand's line into a POSIX shell,
// feeds the words it produces back through the same command tree, and
// requires the second run to see exactly what the first one did. A count or
// optional-value flag rendered "--verbose 2" would come back as verbose=1 and
// a stray positional "2".
func TestResumeCommandReparses(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh to paste the line into")
	}
	for _, args := range [][]string{
		{"get", "dry-run-job", "my table", "7", "-vvv", "--color=never", "-O", "Acme Corp",
			"--search", `it's a "test" $HOME ` + "`x` * ~ ; & | > !", "--state", "a b", "--state", "",
			"--cols", "x,y", "--deleted=false", "--watch", "--limit", "-5", "--timeout", "1m30s", "-o", "json"},
		{"get", "dry-run-job", "--", "-7", "--looks-like-a-flag"},
		{"get", "dry-run-job", "--search", ""},
	} {
		line, first := runResume(t, args)
		out, err := exec.Command(sh, "-c", `printf '%s\0' `+line).Output()
		if err != nil {
			t.Fatalf("sh rejected %s: %v", line, err)
		}
		words := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
		if words[0] != "qctl" {
			t.Fatalf("line %s starts with %q, want qctl", line, words[0])
		}
		_, second := runResume(t, words[1:])
		if !reflect.DeepEqual(first, second) {
			t.Errorf("%s\nre-parsed as %+v\nwant         %+v", line, second, first)
		}
	}
}
