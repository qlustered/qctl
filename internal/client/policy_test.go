package client

import (
	"net/http"
	"testing"
	"time"
)

// withPolicy installs p for the duration of one test and puts the shipped
// default back afterwards. The policy is process state, so every test that
// touches it must restore it or it leaks into whatever runs next in this
// package.
func withPolicy(t *testing.T, p RequestPolicy) {
	t.Helper()
	SetRequestPolicy(p)
	t.Cleanup(ResetRequestPolicy)
}

func TestResolveTimeoutPrecedence(t *testing.T) {
	tests := []struct {
		name   string
		policy RequestPolicy
		own    time.Duration
		want   time.Duration
	}{
		{
			name: "nothing set at all falls back to the shipped default",
			want: DefaultTimeout,
		},
		{
			name: "a caller's own deadline stands when the flag is unset",
			own:  300 * time.Second,
			want: 300 * time.Second,
		},
		{
			// The whole point of the flag: a user on a slow link raises it
			// and every client obeys, including the ones that never heard of
			// it.
			name:   "the flag beats the shipped default",
			policy: RequestPolicy{Timeout: 5 * time.Minute},
			want:   5 * time.Minute,
		},
		{
			name:   "the flag beats a caller's own longer deadline",
			policy: RequestPolicy{Timeout: 5 * time.Second},
			own:    300 * time.Second,
			want:   5 * time.Second,
		},
		{
			name:   "the flag beats a caller's own shorter deadline",
			policy: RequestPolicy{Timeout: 5 * time.Minute},
			own:    10 * time.Second,
			want:   5 * time.Minute,
		},
		{
			// --request-timeout has documented "0 = default" since the day it
			// was registered; a literal zero must keep meaning that rather
			// than quietly becoming "no timeout".
			name:   "an explicit zero means the default, not 'no deadline'",
			policy: RequestPolicy{Timeout: 0},
			want:   DefaultTimeout,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withPolicy(t, tt.policy)
			if got := ResolveTimeout(tt.own); got != tt.want {
				t.Errorf("ResolveTimeout(%s) = %s, want %s", tt.own, got, tt.want)
			}
		})
	}
}

func TestTimeoutOverrideReportsOnlyAnExplicitFlag(t *testing.T) {
	withPolicy(t, RequestPolicy{})
	if got, ok := TimeoutOverride(); ok {
		t.Errorf("TimeoutOverride() = (%s, true) with no flag set, want ok=false", got)
	}

	withPolicy(t, RequestPolicy{Timeout: 90 * time.Second})
	got, ok := TimeoutOverride()
	if !ok || got != 90*time.Second {
		t.Errorf("TimeoutOverride() = (%s, %v), want (1m30s, true)", got, ok)
	}
}

// TestZeroPolicyIsTheShippedBehaviour pins the promise the rollout rests on:
// until the user passes a flag, qctl talks to the server exactly as it did
// before either flag was read -- a 30-second deadline and no retry at all.
func TestZeroPolicyIsTheShippedBehaviour(t *testing.T) {
	withPolicy(t, RequestPolicy{})

	if got := ResolveTimeout(0); got != 30*time.Second {
		t.Errorf("default deadline = %s, want 30s", got)
	}
	if got := CurrentRequestPolicy().Retries; got != 0 {
		t.Errorf("default retries = %d, want 0", got)
	}

	doer, ok := NewDoer(0, nil).(*retryDoer)
	if !ok {
		t.Fatalf("NewDoer returned %T, want *retryDoer", doer)
	}
	if doer.retries != 0 {
		t.Errorf("doer retries = %d, want 0", doer.retries)
	}
}

func TestNewDoerCarriesTheResolvedDeadlineAndRetries(t *testing.T) {
	withPolicy(t, RequestPolicy{Timeout: 7 * time.Second, Retries: 3})

	doer, ok := NewDoer(300*time.Second, nil).(*retryDoer)
	if !ok {
		t.Fatalf("NewDoer returned %T, want *retryDoer", doer)
	}
	inner, ok := doer.inner.(*http.Client)
	if !ok {
		t.Fatalf("doer.inner is %T, want *http.Client", doer.inner)
	}
	if inner.Timeout != 7*time.Second {
		t.Errorf("inner timeout = %s, want 7s (the flag, not the caller's 300s)", inner.Timeout)
	}
	if doer.retries != 3 {
		t.Errorf("doer retries = %d, want 3", doer.retries)
	}
}

// TestNewDoerLeavesTransportNilWhenGivenNil is not pedantry. internal/testutil
// points a whole suite at its mock by swapping http.DefaultTransport, and
// http.Client resolves a nil Transport to whatever DefaultTransport is AT
// REQUEST TIME. Capturing it here instead would pin the real transport at
// construction and route every mocked request to the network.
func TestNewDoerLeavesTransportNilWhenGivenNil(t *testing.T) {
	withPolicy(t, RequestPolicy{})

	doer := NewDoer(0, nil).(*retryDoer)
	inner := doer.inner.(*http.Client)
	if inner.Transport != nil {
		t.Errorf("inner transport = %T, want nil so http.DefaultTransport is read per request", inner.Transport)
	}
}

// TestNewUsesTheResolvedDeadline covers the generated-client path, which is
// where all but a handful of qctl's requests go.
func TestNewUsesTheResolvedDeadline(t *testing.T) {
	withPolicy(t, RequestPolicy{Timeout: 11 * time.Second})

	c, err := New(Config{BaseURL: "https://api.example.com"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if c.config.Timeout != 11*time.Second {
		t.Errorf("client recorded timeout %s, want 11s", c.config.Timeout)
	}
}
