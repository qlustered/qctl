package privilege_requests //nolint:revive // Match the established package under test.

import (
	"net/http"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
)

const usersPath = "/api/orgs/" + testOrgID + "/users"

const usersJSON = `{"results":[` +
	`{"id":"` + testTargetID + `","email":"tom@example.com","first_name":"Tom","last_name":"Target"},` +
	`{"id":"99999999-0000-0000-0000-000000000002","email":"tom.b@example.com","first_name":"Tom","last_name":"Target"}` +
	`],"next":null}`

func TestCheckTargetUserRefusesBlankAtExitTwo(t *testing.T) {
	for _, input := range []string{"", "   ", "\t"} {
		err := CheckTargetUser(input)
		if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
			t.Errorf("CheckTargetUser(%q) exit = %d (err %v), want %d", input, got, err, qerrors.ExitBadUsage)
		}
	}
	if err := CheckTargetUser("tom@example.com"); err != nil {
		t.Errorf("CheckTargetUser refused a real selector: %v", err)
	}
}

// A UUID is the one selector that works from any org, so it must never cost
// a directory sweep — and never be checked against a directory it may not be in.
func TestResolveTargetUserPassesAUUIDThroughWithNoRequest(t *testing.T) {
	c, mock := newTestClient(t)
	users := record(t, mock, http.MethodGet, usersPath, http.StatusOK, usersJSON)

	id, dir, err := c.ResolveTargetUser("tok", testTargetID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != targetID || dir != nil {
		t.Errorf("got (%s, %v), want (%s, nil)", id, dir, targetID)
	}
	if users.calls != 0 {
		t.Errorf("made %d directory requests for a UUID, want 0", users.calls)
	}
}

// CheckTargetUser trims before it checks, so a padded UUID must still skip the
// sweep rather than reach the directory and fail to match a UUID-shaped name.
// The paddings avoid a 38-byte total: uuid.Parse reads any 38-byte string as
// a braced UUID without checking the braces, so one leading and one trailing
// character would pass whether or not the input is trimmed.
func TestResolveTargetUserPassesAPaddedUUIDThroughWithNoRequest(t *testing.T) {
	for _, input := range []string{" " + testTargetID, testTargetID + "\n", "  " + testTargetID + "\t"} {
		c, mock := newTestClient(t)
		users := record(t, mock, http.MethodGet, usersPath, http.StatusOK, usersJSON)

		id, dir, err := c.ResolveTargetUser("tok", input)
		if err != nil {
			t.Errorf("ResolveTargetUser(%q): unexpected error: %v", input, err)
			continue
		}
		if id != targetID || dir != nil {
			t.Errorf("ResolveTargetUser(%q) = (%s, %v), want (%s, nil)", input, id, dir, targetID)
		}
		if users.calls != 0 {
			t.Errorf("ResolveTargetUser(%q) made %d directory requests, want 0", input, users.calls)
		}
	}
}

func TestResolveTargetUserResolvesAnEmailAndReturnsTheDirectory(t *testing.T) {
	c, mock := newTestClient(t)
	users := record(t, mock, http.MethodGet, usersPath, http.StatusOK, usersJSON)

	id, dir, err := c.ResolveTargetUser("tok", "TOM@example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != targetID {
		t.Errorf("resolved to %s, want %s", id, targetID)
	}
	if dir == nil {
		t.Error("directory not returned for the caller to reuse")
	}
	if users.calls != 1 {
		t.Errorf("made %d directory requests, want 1", users.calls)
	}
}

func TestResolveTargetUserUnresolvableIsExitTwo(t *testing.T) {
	cases := map[string]string{
		"no match":  "nobody@example.com",
		"ambiguous": "Tom Target",
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			c, mock := newTestClient(t)
			record(t, mock, http.MethodGet, usersPath, http.StatusOK, usersJSON)
			_, _, err := c.ResolveTargetUser("tok", input)
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit = %d (err %v), want %d", got, err, qerrors.ExitBadUsage)
			}
		})
	}
}

// A failed sweep keeps the server's exit code through the wrap.
func TestResolveTargetUserKeepsTheDirectoryFailuresExitCode(t *testing.T) {
	c, mock := newTestClient(t)
	record(t, mock, http.MethodGet, usersPath, http.StatusForbidden, `{"detail":{"msg":"nope"}}`)

	_, _, err := c.ResolveTargetUser("tok", "tom@example.com")
	if got := qerrors.GetExitCode(err); got != qerrors.ExitUnauthorized {
		t.Errorf("exit = %d (err %v), want %d", got, err, qerrors.ExitUnauthorized)
	}
}
