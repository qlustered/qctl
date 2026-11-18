package pollwait

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	qerrors "github.com/qlustered/qctl/internal/errors"
)

// stubClock returns a clock that only moves when Run sleeps, and the sleeps
// it was asked for.
func stubClock() (func() time.Time, func(time.Duration), *[]time.Duration) {
	now := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	var sleeps []time.Duration
	return func() time.Time { return now },
		func(d time.Duration) { sleeps = append(sleeps, d); now = now.Add(d) },
		&sleeps
}

// sequence returns a poll that answers with the next of values, repeating the
// last, and the number of polls made. A value that is an error is returned as
// the poll's error.
func sequence(values ...any) (func(context.Context) (string, error), *int) {
	polls := 0
	return func(context.Context) (string, error) {
		v := values[min(polls, len(values)-1)]
		polls++
		if err, ok := v.(error); ok {
			return "", err
		}
		return v.(string), nil
	}, &polls
}

func options(timeout time.Duration, progress *[]string) Options[string] {
	now, sleep, _ := stubClock()
	return Options[string]{
		Timeout:  timeout,
		Now:      now,
		Sleep:    sleep,
		Settled:  func(s string) bool { return s == "done" },
		Changed:  func(prev, next string) bool { return prev != next },
		Progress: func(s string) { *progress = append(*progress, s) },
	}
}

func TestRunReturnsTheSettledValueAndReportsOnlyChanges(t *testing.T) {
	var progress []string
	poll, polls := sequence("queued", "queued", "running", "running", "done")
	got, expired, err := Run(context.Background(), poll, options(time.Minute, &progress))
	if err != nil || expired != nil {
		t.Fatalf("Run = (%q, %v, %v), want the settled value", got, expired, err)
	}
	if got != "done" || *polls != 5 {
		t.Errorf("got %q after %d polls, want done after 5", got, *polls)
	}
	if want := []string{"queued", "running"}; !reflect.DeepEqual(progress, want) {
		t.Errorf("progress = %q, want %q", progress, want)
	}
}

func TestRunStopsOnAPermanentError(t *testing.T) {
	var progress []string
	notFound := qerrors.NewNotFound("gone")
	poll, polls := sequence("queued", notFound, "done")
	_, expired, err := Run(context.Background(), poll, options(time.Minute, &progress))
	if !errors.Is(err, notFound) || expired != nil {
		t.Fatalf("Run = (%v, %v), want the permanent error", expired, err)
	}
	if *polls != 2 {
		t.Errorf("polls = %d, want 2", *polls)
	}
}

func TestRunRetriesATransientError(t *testing.T) {
	var progress []string
	poll, polls := sequence(errors.New("connection reset"), "done")
	got, expired, err := Run(context.Background(), poll, options(time.Minute, &progress))
	if err != nil || expired != nil || got != "done" || *polls != 2 {
		t.Fatalf("Run = (%q, %v, %v) after %d polls, want done after 2", got, expired, err, *polls)
	}
}

func TestRunReportsTheLastValueAndErrorAtTheTimeout(t *testing.T) {
	var progress []string
	transient := errors.New("connection reset")
	poll, _ := sequence("running", transient)
	_, expired, err := Run(context.Background(), poll, options(10*time.Second, &progress))
	if err != nil || expired == nil {
		t.Fatalf("Run = (%v, %v), want an expiry", expired, err)
	}
	if expired.Last != "running" || !errors.Is(expired.LastErr, transient) {
		t.Errorf("expired = %+v, want Last running and LastErr %v", *expired, transient)
	}
}

func TestRunSleepsTheIntervalWithoutOverrunningTheTimeout(t *testing.T) {
	now, sleep, sleeps := stubClock()
	poll, _ := sequence("running")
	_, expired, _ := Run(context.Background(), poll, Options[string]{
		Timeout: 35 * time.Second,
		Now:     now,
		Sleep:   sleep,
		Settled: func(string) bool { return false },
	})
	if expired == nil {
		t.Fatal("want an expiry")
	}
	want := []time.Duration{}
	for range 15 {
		want = append(want, 2*time.Second)
	}
	want = append(want, 5*time.Second)
	if !reflect.DeepEqual(*sleeps, want) {
		t.Errorf("sleeps = %v, want %v", *sleeps, want)
	}
}

func TestRunDoesNotPollOnceDoneIsClosed(t *testing.T) {
	var progress []string
	poll, polls := sequence("running")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cancel()
	opts := options(time.Minute, &progress)
	_, expired, err := Run(ctx, poll, opts)
	if !errors.Is(err, ErrStopped) || expired != nil {
		t.Fatalf("Run = (%v, %v), want ErrStopped", expired, err)
	}
	if *polls != 0 {
		t.Errorf("polls = %d, want 0", *polls)
	}
}

// Done closing while Run sleeps ends the wait when the sleep returns, before
// the next poll.
func TestRunStopsAfterASleepDuringWhichDoneClosed(t *testing.T) {
	now, sleep, sleeps := stubClock()
	poll, polls := sequence("running")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, expired, err := Run(ctx, poll, Options[string]{
		Timeout: time.Minute,
		Now:     now,
		Sleep: func(d time.Duration) {
			sleep(d)
			cancel()
		},
		Settled: func(string) bool { return false },
	})
	if !errors.Is(err, ErrStopped) || expired != nil {
		t.Fatalf("Run = (%v, %v), want ErrStopped", expired, err)
	}
	if *polls != 1 || len(*sleeps) != 1 {
		t.Errorf("polls = %d and sleeps = %v, want one of each", *polls, *sleeps)
	}
}

// A sleep that has not returned does not hold Run once Done closes.
func TestRunDoesNotWaitOutASleepOnceDoneCloses(t *testing.T) {
	now, _, _ := stubClock()
	poll, polls := sequence("running")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	result := make(chan error, 1)
	go func() {
		_, _, err := Run(ctx, poll, Options[string]{
			Timeout: time.Minute,
			Now:     now,
			Sleep: func(time.Duration) {
				cancel()
				<-release
			},
			Settled: func(string) bool { return false },
		})
		result <- err
	}()
	select {
	case err := <-result:
		if !errors.Is(err, ErrStopped) {
			t.Fatalf("Run error = %v, want ErrStopped", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run waited for the sleep after Done closed")
	}
	if *polls != 1 {
		t.Errorf("polls = %d, want 1", *polls)
	}
}

func TestRunCancelsAnInFlightPollAtItsBudget(t *testing.T) {
	progress := 0
	_, expired, err := Run(t.Context(), func(ctx context.Context) (string, error) {
		select {
		case <-ctx.Done():
		case <-time.After(5 * time.Second):
			t.Fatal("poll did not receive the wait deadline")
		}
		return "done", nil
	}, Options[string]{
		Timeout: 50 * time.Millisecond,
		Now:     time.Now, Sleep: time.Sleep,
		Settled:  func(s string) bool { return s == "done" },
		Progress: func(string) { progress++ },
	})
	if err != nil || expired == nil || expired.Last != "" || progress != 0 {
		t.Fatalf("Run = (%v, %v), progress = %d; want expiry without a late value", expired, err, progress)
	}
}

func TestRunCancelsAnInFlightPollWithItsParent(t *testing.T) {
	parent, cancel := context.WithCancel(t.Context())
	defer cancel()
	_, expired, err := Run(parent, func(ctx context.Context) (string, error) {
		cancel()
		<-ctx.Done()
		return "done", nil
	}, options(time.Minute, &[]string{}))
	if err != ErrStopped || expired != nil {
		t.Fatalf("Run = (%v, %v), want ErrStopped", expired, err)
	}
}
