package client

import (
	"net/http"
	"sync"
	"time"
)

// RequestPolicy is the transport policy for one qctl invocation: how long a
// single HTTP attempt may take, and how many times a *safe* request is
// repeated when it fails. It is resolved once, in root's PersistentPreRunE,
// from --request-timeout and --retries.
//
// Why this is package state rather than two more parameters on every
// constructor. Both flags are global, and the ~195 places that build a domain
// client are nowhere near a cobra command: they call
// `datasets.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)` and
// friends. Threading the policy through all 31 domain constructors would make
// every constructor written after this one able to opt out of both flags by
// saying nothing -- which is exactly how the 25 hard-coded 30-second timeouts
// this replaces accumulated. Resolving here means a client added tomorrow
// honours both flags without knowing they exist. apierror.SetVerbosity and
// apierror.SetOrgScope are the same shape for the same reason, set from the
// same hook.
//
// The zero value is the shipped default and is what every test sees unless it
// says otherwise: no timeout override (so DefaultTimeout, or the caller's own
// deliberate timeout, stands) and no retries at all.
type RequestPolicy struct {
	// Timeout is --request-timeout: the deadline for a single HTTP attempt.
	// Zero means the user did not ask for one, which is also what
	// `--request-timeout 0` means -- the flag has documented "0 = default"
	// since it was registered, so a literal zero keeps meaning that rather
	// than quietly becoming kubectl's "no timeout".
	//
	// A non-zero value wins over a caller's own timeout, including a
	// deliberately longer one: stored_items asks for 300s because it moves
	// files, but a user who says --request-timeout 30s is asking for a
	// shorter leash everywhere, and a package that silently kept its own
	// would make the flag a suggestion.
	Timeout time.Duration

	// Retries is --retries: attempts *after* the first, for safe requests
	// only. Zero -- the default -- means no retry, so qctl's on-the-wire
	// behaviour is unchanged for anyone who does not ask.
	Retries int
}

var (
	policyMu sync.RWMutex
	policy   RequestPolicy
)

// SetRequestPolicy records the policy for this invocation. Root's
// PersistentPreRunE is the only non-test caller.
func SetRequestPolicy(p RequestPolicy) {
	policyMu.Lock()
	defer policyMu.Unlock()
	policy = p
}

// CurrentRequestPolicy returns the policy in force.
func CurrentRequestPolicy() RequestPolicy {
	policyMu.RLock()
	defer policyMu.RUnlock()
	return policy
}

// ResetRequestPolicy restores the shipped default (tests only).
func ResetRequestPolicy() { SetRequestPolicy(RequestPolicy{}) }

// TimeoutOverride reports the user's explicit --request-timeout, if there is
// one. Callers whose own deadline is not a request deadline -- the WebSocket
// handshake, say -- use this rather than ResolveTimeout, so that leaving the
// flag alone leaves their own value alone.
func TimeoutOverride() (time.Duration, bool) {
	t := CurrentRequestPolicy().Timeout
	return t, t > 0
}

// ResolveTimeout returns the deadline for a single HTTP attempt.
//
// Precedence: an explicit --request-timeout, then the caller's own deliberate
// timeout, then DefaultTimeout. Pass 0 for own to mean "no opinion" -- which
// is what every domain client passes, because 30 seconds was only ever
// DefaultTimeout written out 25 more times.
func ResolveTimeout(own time.Duration) time.Duration {
	if t, ok := TimeoutOverride(); ok {
		return t
	}
	if own > 0 {
		return own
	}
	return DefaultTimeout
}

// Doer is the request interface shared by the generated client
// (api.HttpRequestDoer) and the handful of packages that speak HTTP without
// it. Both kinds of caller get their client from NewDoer, so neither can
// drift away from the resolved policy.
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// NewDoer builds the HTTP client for this invocation.
//
// own is the caller's own per-attempt deadline, 0 meaning "no opinion"; see
// ResolveTimeout for how it is reconciled with --request-timeout. transport is
// the round tripper to send through, nil meaning http.DefaultTransport --
// which is what internal/testutil swaps to point a suite at its mock, so
// reading it here rather than capturing it earlier is load-bearing.
//
// The returned Doer applies --retries; see retryDoer for what it will and will
// not repeat.
func NewDoer(own time.Duration, transport http.RoundTripper) Doer {
	return &retryDoer{
		inner: &http.Client{
			Timeout:   ResolveTimeout(own),
			Transport: transport,
		},
		retries: CurrentRequestPolicy().Retries,
	}
}
