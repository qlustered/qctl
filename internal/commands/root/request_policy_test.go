package root

import (
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/client"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

// resetRequestPolicyFlags undoes what runRoot's own cleanup does not reach.
// Both flags live on the package-singleton tree, and pflag leaves Changed
// true after a Set, so a value left behind would be read by every later test
// in this binary -- and, worse, by the process-wide client.RequestPolicy they
// feed.
func resetRequestPolicyFlags(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		for name, zero := range map[string]string{"request-timeout": "0s", "retries": "0"} {
			if f := GetRootCmd().PersistentFlags().Lookup(name); f != nil {
				_ = f.Value.Set(zero)
				f.Changed = false
			}
		}
		client.ResetRequestPolicy()
	})
}

// TestRequestPolicyFlagsAreRead is the whole point of the change: before it,
// grepping the tree for either flag name found the registration, one entry in
// nonFilterFlags, and two comments saying it was read nowhere.
//
// api-resources is offline -- no server, no credential -- and the hook under
// test runs in PersistentPreRunE, before any RunE could reach out.
func TestRequestPolicyFlagsAreRead(t *testing.T) {
	resetRequestPolicyFlags(t)

	if _, err := runRoot(t, "api-resources", "--request-timeout", "90s", "--retries", "3"); err != nil {
		t.Fatalf("api-resources error = %v", err)
	}

	got := client.CurrentRequestPolicy()
	if got.Timeout != 90*time.Second {
		t.Errorf("policy timeout = %s, want 1m30s", got.Timeout)
	}
	if got.Retries != 3 {
		t.Errorf("policy retries = %d, want 3", got.Retries)
	}
}

// TestRequestPolicyDefaultsToTodaysBehaviour: an invocation that passes
// neither flag must leave qctl talking to the server exactly as it did before
// either was read.
func TestRequestPolicyDefaultsToTodaysBehaviour(t *testing.T) {
	resetRequestPolicyFlags(t)
	client.SetRequestPolicy(client.RequestPolicy{Timeout: time.Hour, Retries: 9})

	if _, err := runRoot(t, "api-resources"); err != nil {
		t.Fatalf("api-resources error = %v", err)
	}

	if got := client.CurrentRequestPolicy(); got != (client.RequestPolicy{}) {
		t.Errorf("policy = %+v, want the zero policy", got)
	}
	if got := client.ResolveTimeout(0); got != client.DefaultTimeout {
		t.Errorf("resolved deadline = %s, want %s", got, client.DefaultTimeout)
	}
}

// TestNegativeRequestPolicyFlagsExitTwo. A Duration flag parses "-5s"
// happily, and an http.Client given a negative timeout does not fail --
// it treats every request as already expired and reports a deadline error
// that names no flag. Every other client-side validation failure in qctl
// exits 2; these two do as well.
func TestNegativeRequestPolicyFlagsExitTwo(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"negative timeout", []string{"api-resources", "--request-timeout", "-5s"}},
		{"negative retries", []string{"api-resources", "--retries", "-1"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetRequestPolicyFlags(t)

			_, err := runRoot(t, tt.args...)
			if err == nil {
				t.Fatal("error = nil, want a usage failure")
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d (%v)", got, qerrors.ExitBadUsage, err)
			}
			// Refused, not silently clamped: a policy recorded here would
			// reach every client in the invocation.
			if got := client.CurrentRequestPolicy(); got != (client.RequestPolicy{}) {
				t.Errorf("policy = %+v after a refusal, want the zero policy", got)
			}
		})
	}
}
