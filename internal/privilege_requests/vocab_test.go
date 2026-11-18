package privilege_requests //nolint:revive // Match the established package under test.

import (
	"slices"
	"sort"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

// assertCoversSpecEnum pins a vocabulary to the spec's own value list.
// api.StringEnumValues is regenerated from the raw spec by the same
// `make generate-client` that regenerates the constants, so a value the spec
// ADDS turns this red; a value it renames already fails to compile, because
// the vocabularies are built from api.* constants.
func assertCoversSpecEnum(t *testing.T, schema string, got []string) {
	t.Helper()
	spec, ok := api.StringEnumValues[schema]
	if !ok || len(spec) == 0 {
		t.Fatalf("api.StringEnumValues has no %q entry; this check cannot fail as written", schema)
	}
	want := append([]string(nil), spec...)
	sort.Strings(want)
	if !slices.Equal(got, want) {
		t.Errorf("%s vocabulary = %v, the spec declares %v\n"+
			"Add the missing api.* constant to vocab.go.", schema, got, want)
	}
}

func TestStatusNamesCoverTheSpecEnum(t *testing.T) {
	assertCoversSpecEnum(t, "PrivilegeRequestStatus", StatusNames())
}

func TestActionNamesCoverTheSpecEnum(t *testing.T) {
	assertCoversSpecEnum(t, "PrivilegeAction", ActionNames())
}

// The wire spelling is what a user is shown and what reaches the server, so
// the capitalised statuses stay capitalised.
func TestNamesAreTheSortedWireSpellings(t *testing.T) {
	if got, want := StatusNames(), []string{"Approved", "Expired", "Pending", "Rejected"}; !slices.Equal(got, want) {
		t.Errorf("StatusNames() = %v, want %v", got, want)
	}
	if got, want := ActionNames(), []string{"demote", "escalate"}; !slices.Equal(got, want) {
		t.Errorf("ActionNames() = %v, want %v", got, want)
	}
}

func TestParseStatusIsCaseInsensitiveAndCanonical(t *testing.T) {
	for in, want := range map[string]api.PrivilegeRequestStatus{
		"Pending":  api.PrivilegeRequestStatusPending,
		"pending":  api.PrivilegeRequestStatusPending,
		"PENDING":  api.PrivilegeRequestStatusPending,
		"approved": api.PrivilegeRequestStatusApproved,
		"rEjEcTeD": api.PrivilegeRequestStatusRejected,
		"expired":  api.PrivilegeRequestStatusExpired,
	} {
		got, err := ParseStatus(in)
		if err != nil {
			t.Errorf("ParseStatus(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParseStatus(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseActionIsCaseInsensitiveAndCanonical(t *testing.T) {
	for in, want := range map[string]api.PrivilegeAction{
		"escalate": api.Escalate,
		"Escalate": api.Escalate,
		"DEMOTE":   api.Demote,
		"demote":   api.Demote,
	} {
		got, err := ParseAction(in)
		if err != nil {
			t.Errorf("ParseAction(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParseAction(%q) = %q, want %q", in, got, want)
		}
	}
}

// An unknown value is refused locally at exit 2. The near misses matter most:
// a trailing space or a plausible synonym must not slip through as a prefix
// or fuzzy match.
func TestParseRejectsUnknownAtExitTwo(t *testing.T) {
	for _, in := range []string{"", "open", "Pending ", "approve", "deny", "withdrawn"} {
		if _, err := ParseStatus(in); err == nil {
			t.Errorf("ParseStatus(%q) accepted a value outside the enum", in)
		} else if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
			t.Errorf("ParseStatus(%q) exit code %d, want %d", in, got, qerrors.ExitBadUsage)
		}
	}
	for _, in := range []string{"", "promote", "escalat", "revoke", "escalate "} {
		if _, err := ParseAction(in); err == nil {
			t.Errorf("ParseAction(%q) accepted a value outside the enum", in)
		} else if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
			t.Errorf("ParseAction(%q) exit code %d, want %d", in, got, qerrors.ExitBadUsage)
		}
	}
}

func TestActionOf(t *testing.T) {
	for _, tc := range []struct {
		name, oldValue, newValue, want string
	}{
		{"escalation", "false", "true", "escalate"},
		{"demotion", "true", "false", "demote"},
		{"no change is not an action", "true", "true", "true→true"},
		{"another permission key's values pass through verbatim", "viewer", "admin", "viewer→admin"},
		{"only the exact wire strings map", "False", "True", "False→True"},
		{"empty values stay visible", "", "", "→"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ActionOf(tc.oldValue, tc.newValue); got != tc.want {
				t.Errorf("ActionOf(%q, %q) = %q, want %q", tc.oldValue, tc.newValue, got, tc.want)
			}
		})
	}
}
