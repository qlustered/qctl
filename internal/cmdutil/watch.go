package cmdutil

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// AddWatchTimeoutFlag registers --timeout, a duration defaulting to def;
// 0 means no limit.
func AddWatchTimeoutFlag(cmd *cobra.Command, def time.Duration) {
	cmd.Flags().Duration("timeout", def,
		"Stop waiting after this long; 0 means no limit (QCTL_WATCH_TIMEOUT overrides the default)")
}

// WatchBudget returns how long cmd may wait, 0 meaning no limit: --timeout
// when it was set, else QCTL_WATCH_TIMEOUT when that is set, else
// --timeout's default. A negative --timeout or an invalid
// QCTL_WATCH_TIMEOUT is a bad-usage error.
func WatchBudget(cmd *cobra.Command) (time.Duration, error) {
	f := cmd.Flags().Lookup("timeout")
	if f == nil {
		return 0, fmt.Errorf("internal error: %s has no --timeout flag", cmd.CommandPath())
	}
	if !f.Changed {
		d, set, err := config.WatchTimeoutFromEnv()
		if err != nil {
			return 0, qerrors.Wrap(qerrors.ExitBadUsage, err, "")
		}
		if set {
			return d, nil
		}
	}
	d, err := cmd.Flags().GetDuration("timeout")
	if err != nil {
		return 0, err
	}
	if d < 0 {
		return 0, qerrors.NewBadUsagef("--timeout must not be negative, got %s (0 means no limit)", d)
	}
	return d, nil
}

// WaitKind says what a wait's end means.
type WaitKind int

const (
	// WaitUntilDone waits for one thing to end; running out of time fails it.
	WaitUntilDone WaitKind = iota
	// WatchStream observes a stream; running out of time ends it cleanly.
	WatchStream
)

// WaitSpec describes a wait to StartWait.
type WaitSpec struct {
	Kind WaitKind
	// Budget is the time limit; 0 means none. Unless SelfTimed, it becomes
	// the deadline of StartWait's context.
	Budget time.Duration
	// SelfTimed means the loop enforces Budget itself, so StartWait's
	// context gets no deadline and Budget is only printed.
	SelfTimed bool
	// Doing is the banner's opening, such as "watching ingestion jobs every 5s".
	Doing string
	// Next is the command line that picks the wait up again; "" for none.
	Next string
	// NextVerb introduces Next; "" means "resume with".
	NextVerb string
	// Note is an optional clause appended to the banner.
	Note string
}

func (s WaitSpec) nextClause() string {
	if s.Next == "" {
		return ""
	}
	verb := s.NextVerb
	if verb == "" {
		verb = "resume with"
	}
	return "; " + verb + ": " + s.Next
}

func (s WaitSpec) banner() string {
	line := s.Doing
	if s.Budget > 0 {
		line += ", for up to " + s.Budget.String()
	}
	line += s.nextClause()
	if s.Note != "" {
		line += "; " + s.Note
	}
	return line
}

var (
	// interruptGrace is how long a Waiter lets the command return after the
	// first signal before exiting the process itself; 0 disables that.
	interruptGrace = 2 * time.Second
	// exitProcess is how the grace timer ends the process.
	exitProcess = qerrors.Exit
	// afterFunc arms the grace timer.
	afterFunc = func(d time.Duration, f func()) interface{ Stop() bool } {
		return time.AfterFunc(d, f)
	}
	// signalIgnored reports whether a signal is ignored.
	signalIgnored = signal.Ignored
)

// SetInterruptGrace sets the signal grace period (tests only); 0 disables it.
func SetInterruptGrace(d time.Duration) { interruptGrace = d }

// ResetInterruptGrace restores the two-second signal grace period.
func ResetInterruptGrace() { interruptGrace = 2 * time.Second }

var (
	errWaitBudget  = fmt.Errorf("wait budget used up: %w", context.DeadlineExceeded)
	errInterrupted = fmt.Errorf("interrupted: %w", context.Canceled)
)

// Waiter is a wait started by StartWait.
type Waiter struct {
	cmd            *cobra.Command
	spec           WaitSpec
	ctx            context.Context
	cancel         context.CancelCauseFunc
	cancelDeadline context.CancelFunc
	armed          []os.Signal
	sigCh          chan os.Signal
	stop           chan struct{}
	watcherDone    chan struct{}
	stopOnce       sync.Once

	mu    sync.Mutex
	sig   os.Signal
	grace interface{ Stop() bool }
}

// StartWait writes spec's one-line banner to cmd's stderr and returns a
// context for the wait plus the Waiter that classifies how it ended.
//
// The context is cancelled at spec.Budget (unless SelfTimed) and on the first
// SIGINT or SIGTERM; a signal that signal.Ignored reports is left alone. The
// first signal also ends the Waiter's signal.Notify registration, so a second
// signal is handled as if StartWait had never been called, and starts a 2s
// timer that, unless Stop is called first, exits the process through
// qerrors.Exit with the *qerrors.Interrupted Ended would return.
func StartWait(cmd *cobra.Command, spec WaitSpec) (context.Context, *Waiter) {
	fmt.Fprintln(cmd.ErrOrStderr(), terminal.Sanitize(spec.banner()))

	parent := cmd.Context()
	if parent == nil {
		parent = context.Background()
	}
	w := &Waiter{
		cmd:            cmd,
		spec:           spec,
		cancelDeadline: func() {},
		stop:           make(chan struct{}),
		watcherDone:    make(chan struct{}),
	}
	if spec.Budget > 0 && !spec.SelfTimed {
		parent, w.cancelDeadline = context.WithTimeoutCause(parent, spec.Budget, errWaitBudget)
	}
	w.ctx, w.cancel = context.WithCancelCause(parent)

	for _, s := range []os.Signal{syscall.SIGINT, syscall.SIGTERM} {
		if !signalIgnored(s) {
			w.armed = append(w.armed, s)
		}
	}
	if len(w.armed) == 0 {
		close(w.watcherDone)
		return w.ctx, w
	}
	w.sigCh = make(chan os.Signal, 1)
	signal.Notify(w.sigCh, w.armed...)
	go w.watchSignals()
	return w.ctx, w
}

func (w *Waiter) watchSignals() {
	defer close(w.watcherDone)
	select {
	case <-w.stop:
	case sig := <-w.sigCh:
		signal.Stop(w.sigCh)
		w.mu.Lock()
		defer w.mu.Unlock()
		w.sig = sig
		w.cancel(errInterrupted)
		if interruptGrace > 0 {
			err := w.interrupted(sig)
			w.grace = afterFunc(interruptGrace, func() { exitProcess(err) })
		}
	}
}

// Stop ends the Waiter's signal.Notify registration, stops the timer a signal
// started, and cancels the wait's context. It is safe to call more than once.
func (w *Waiter) Stop() {
	w.stopOnce.Do(func() {
		if w.sigCh != nil {
			signal.Stop(w.sigCh)
		}
		close(w.stop)
		<-w.watcherDone
		w.mu.Lock()
		if w.grace != nil {
			w.grace.Stop()
		}
		w.mu.Unlock()
		w.cancel(nil)
		w.cancelDeadline()
	})
}

// Ended classifies the wait's outcome; err is what the wait loop returned.
// The loop gave up on the wait when err is a context error or wraps one, as
// qerrors.Caused(msg, context.Cause(ctx)) does, or, for a WatchStream, when
// err is nil.
//
//   - After a signal it returns a *qerrors.Interrupted for that signal,
//     whatever err is.
//   - When the context reached spec.Budget and the loop gave up, a
//     WatchStream writes "timed out after <Budget>" and the resume clause to
//     stderr and returns nil, and a WaitUntilDone returns a
//     qerrors.NewTimedOut error whose message is "timed out after <Budget>: "
//     and err's message, followed by the resume line. errors.Is reaches err
//     through it. The context's own error is described from spec.Doing
//     instead, and a message that already starts with "timed out after" is
//     not prefixed again.
//   - Otherwise it returns err.
func (w *Waiter) Ended(err error) error {
	w.mu.Lock()
	sig := w.sig
	w.mu.Unlock()
	if sig != nil {
		return w.interrupted(sig)
	}
	if !errors.Is(context.Cause(w.ctx), errWaitBudget) || !w.gaveUp(err) {
		return err
	}
	timedOut := "timed out after " + w.spec.Budget.String()
	if w.spec.Kind == WatchStream {
		fmt.Fprintln(w.cmd.ErrOrStderr(), terminal.Sanitize(timedOut+w.spec.nextClause()))
		return nil
	}

	var msg string
	switch {
	case err == w.ctx.Err() || err == context.Cause(w.ctx):
		msg = timedOut + ": " + w.stillDoing()
	case strings.HasPrefix(err.Error(), "timed out after"):
		msg = err.Error()
	default:
		msg = timedOut + ": " + err.Error()
	}
	if clause := w.spec.nextClause(); clause != "" {
		// "; resume with: X" becomes "\n\nResume with: X".
		clause = strings.TrimPrefix(clause, "; ")
		msg += "\n\n" + strings.ToUpper(clause[:1]) + clause[1:]
	}
	return qerrors.NewTimedOut(qerrors.Caused(msg, err))
}

// gaveUp reports whether err, a wait loop's result, says the loop gave up
// because its context was done.
func (w *Waiter) gaveUp(err error) bool {
	if err == nil {
		return w.spec.Kind == WatchStream
	}
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func (w *Waiter) stillDoing() string {
	if w.spec.Doing == "" {
		return "the wait did not finish"
	}
	return "still " + w.spec.Doing
}

func (w *Waiter) interrupted(sig os.Signal) *qerrors.Interrupted {
	msg := "interrupted" + w.spec.nextClause()
	if w.spec.Note != "" {
		msg += "; " + w.spec.Note
	}
	return &qerrors.Interrupted{Signal: sig, Message: msg}
}

// ResumeCommand renders the command line that ran cmd: "qctl", cmd's path
// below the root, cmd's positional arguments, then every flag
// cmd.Flags().Visit reports as set, sorted by name, except those named in
// drop (names without dashes). Flags are rendered by RenderFlag and positionals by
// ShellArg; when a positional starts with "-", the flags come first and "--"
// precedes the positionals.
func ResumeCommand(cmd *cobra.Command, drop ...string) string {
	var path []string
	for c := cmd; c.HasParent(); c = c.Parent() {
		path = append(path, c.Name())
	}
	words := []string{"qctl"}
	for i := len(path) - 1; i >= 0; i-- {
		words = append(words, path[i])
	}

	dropped := make(map[string]bool, len(drop))
	for _, name := range drop {
		dropped[name] = true
	}
	var set []*pflag.Flag
	cmd.Flags().Visit(func(f *pflag.Flag) {
		if !dropped[f.Name] {
			set = append(set, f)
		}
	})
	sort.Slice(set, func(i, j int) bool { return set[i].Name < set[j].Name })
	flags := make([]string, len(set))
	for i, f := range set {
		flags[i] = RenderFlag(f)
	}

	var positionals []string
	dashed := false
	for _, arg := range cmd.Flags().Args() {
		positionals = append(positionals, ShellArg(arg))
		dashed = dashed || strings.HasPrefix(arg, "-")
	}
	if dashed {
		words = append(append(append(words, flags...), "--"), positionals...)
	} else {
		words = append(append(words, positionals...), flags...)
	}
	return strings.Join(words, " ")
}

// RenderFlag renders f at its current value as the words that set it again,
// with values quoted by ShellArg: "--name" for a true bool and "--name=false"
// for a false one, one "--name value" per element of a slice flag, and
// "--name=value" for a flag with an optional value (NoOptDefVal, as a count
// flag has), where "--name value" would leave the value a positional.
func RenderFlag(f *pflag.Flag) string {
	name := "--" + f.Name
	if f.Value.Type() == "bool" {
		if v, err := strconv.ParseBool(f.Value.String()); err == nil && v {
			return name
		}
		return name + "=false"
	}
	sep := " "
	if f.NoOptDefVal != "" {
		sep = "="
	}
	sv, ok := f.Value.(pflag.SliceValue)
	if !ok {
		return name + sep + ShellArg(f.Value.String())
	}
	values := sv.GetSlice()
	if len(values) == 0 {
		return name + sep + ShellArg("")
	}
	words := make([]string, len(values))
	for i, v := range values {
		words[i] = name + sep + ShellArg(v)
	}
	return strings.Join(words, " ")
}
