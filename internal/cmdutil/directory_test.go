package cmdutil

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/comments"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/spf13/cobra"
)

const (
	adaID   = "0190a0a0-1111-4222-8333-444444444444"
	graceID = "0190b0b0-1111-4222-8333-444444444444"
)

// countingFetch reports how often the directory was swept, which is the whole
// point of ResolveUsers: a UUID must not cost a sweep.
func countingFetch(calls *int, dir *comments.Directory, err error) func(string) (*comments.Directory, error) {
	return func(string) (*comments.Directory, error) {
		*calls++
		return dir, err
	}
}

func forbidden() error {
	return comments.HandleError(http.StatusForbidden,
		[]byte(`{"detail":{"msg":"You don't have access to view the list of users in this organization."}}`),
		"list users failed")
}

func TestResolveUsersSkipsTheSweepForUUIDs(t *testing.T) {
	calls := 0
	// The fetch would fail if it ran: an editor cannot read the directory.
	ids, dir, err := ResolveUsers(countingFetch(&calls, nil, forbidden()), "token",
		[]string{adaID, "  " + graceID + "  "})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 0 {
		t.Errorf("directory sweeps = %d, want 0", calls)
	}
	if dir != nil {
		t.Error("directory = non-nil, want nil for a skipped sweep")
	}
	if got := []string{ids[0].String(), ids[1].String()}; got[0] != adaID || got[1] != graceID {
		t.Errorf("ids = %v, want [%s %s] in the order given", got, adaID, graceID)
	}
}

func TestResolveUsersSweepsWhenAnyValueIsAName(t *testing.T) {
	calls := 0
	// An empty directory resolves nobody, which is enough to prove the sweep
	// ran: only a name could have forced it.
	_, _, err := ResolveUsers(countingFetch(&calls, new(comments.Directory), nil), "token",
		[]string{adaID, "ada@example.com"})
	if calls != 1 {
		t.Errorf("directory sweeps = %d, want 1", calls)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want 2 for an unresolvable name (err: %v)", got, err)
	}
}

func TestResolveUsersWithoutValuesAsksNothing(t *testing.T) {
	calls := 0
	ids, dir, err := ResolveUsers(countingFetch(&calls, new(comments.Directory), nil), "token", nil)
	if err != nil || ids != nil || dir != nil || calls != 0 {
		t.Errorf("ResolveUsers(nil) = %v, %v, %v after %d sweeps; want nil, nil, nil after 0", ids, dir, err, calls)
	}
}

func TestResolveUsersForbiddenNamesThePermissionAndKeepsExitFour(t *testing.T) {
	calls := 0
	_, _, err := ResolveUsers(countingFetch(&calls, nil, forbidden()), "token",
		[]string{adaID, "Ada Lovelace"})
	if got := qerrors.GetExitCode(err); got != qerrors.ExitUnauthorized {
		t.Errorf("exit code = %d, want 4 (err: %v)", got, err)
	}
	for _, want := range []string{`"Ada Lovelace"`, "org:users:list", "full UUID"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}
	// The chain must stay walkable: the exit code above is read through it.
	var apiErr *comments.APIError
	if !errors.As(err, &apiErr) {
		t.Errorf("error = %q, want an unwrappable *comments.APIError", err)
	}
}

func TestResolveUsersPassesOtherFailuresThroughUnadorned(t *testing.T) {
	calls := 0
	_, _, err := ResolveUsers(countingFetch(&calls, nil, errors.New("dial tcp: refused")), "token",
		[]string{"ada@example.com"})
	if err == nil || strings.Contains(err.Error(), "org:users:list") {
		t.Errorf("error = %v, want the transport failure without the permission note", err)
	}
}

func TestEnsureDisplayDirectoryReusesTheSweptOne(t *testing.T) {
	calls := 0
	swept := new(comments.Directory)
	cmd := &cobra.Command{}
	cmd.SetErr(&strings.Builder{})

	if got := EnsureDisplayDirectory(cmd, swept, countingFetch(&calls, nil, forbidden()), "token"); got != swept {
		t.Errorf("directory = %p, want the one already swept (%p)", got, swept)
	}
	if calls != 0 {
		t.Errorf("directory sweeps = %d, want 0", calls)
	}
}

func TestEnsureDisplayDirectoryFallsBackToUUIDsWithAWarning(t *testing.T) {
	calls := 0
	var stderr strings.Builder
	cmd := &cobra.Command{}
	cmd.SetErr(&stderr)

	got := EnsureDisplayDirectory(cmd, nil, countingFetch(&calls, nil, forbidden()), "token")
	if got == nil {
		t.Fatal("directory = nil, want an empty one: display must never fail")
	}
	if calls != 1 {
		t.Errorf("directory sweeps = %d, want 1", calls)
	}
	if !strings.HasPrefix(stderr.String(), "Warning: user directory unavailable") {
		t.Errorf("stderr = %q, want the directory warning", stderr.String())
	}
}

func TestMentionDirectoryFallsBackOnlyOnAForbiddenDirectory(t *testing.T) {
	mentionable := new(comments.Directory)
	for _, tc := range []struct {
		name         string
		dirErr       error
		wantFallback bool
	}{
		{"readable directory", nil, false},
		{"forbidden directory", forbidden(), true},
		{"server error", comments.HandleError(http.StatusInternalServerError, []byte(`{}`), "list users failed"), false},
		{"transport error", errors.New("dial tcp: refused"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dirCalls, fallbackCalls := 0, 0
			swept := new(comments.Directory)
			fetch := MentionDirectory(countingFetch(&dirCalls, swept, tc.dirErr),
				countingFetch(&fallbackCalls, mentionable, nil))
			got, err := fetch("token")
			if dirCalls != 1 {
				t.Errorf("directory sweeps = %d, want 1", dirCalls)
			}
			if tc.wantFallback {
				if err != nil || got != mentionable || fallbackCalls != 1 {
					t.Errorf("fetch = %p, %v after %d fallback reads; want the mentionable directory after 1",
						got, err, fallbackCalls)
				}
				return
			}
			if fallbackCalls != 0 {
				t.Errorf("fallback reads = %d, want 0", fallbackCalls)
			}
			if !errors.Is(err, tc.dirErr) {
				t.Errorf("err = %v, want the directory's own %v", err, tc.dirErr)
			}
		})
	}
}

// When both reads fail, ResolveUsers still reports the directory's 403 — its
// permission note and exit 4 — and says what the fallback answered.
func TestMentionDirectoryKeepsTheDirectoryRefusalWhenTheFallbackFails(t *testing.T) {
	dirCalls, fallbackCalls := 0, 0
	fetch := MentionDirectory(countingFetch(&dirCalls, nil, forbidden()),
		countingFetch(&fallbackCalls, nil, errors.New("dial tcp: refused")))
	_, _, err := ResolveUsers(fetch, "token", []string{"ada@example.com"})
	if got := qerrors.GetExitCode(err); got != qerrors.ExitUnauthorized {
		t.Errorf("exit code = %d, want 4 (err: %v)", got, err)
	}
	for _, want := range []string{"org:users:list", "mentionable users: dial tcp: refused"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to contain %q", err, want)
		}
	}
}
