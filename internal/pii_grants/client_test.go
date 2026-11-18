package pii_grants //nolint:revive // Established package-path style in this repo.

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

func TestParseDuration(t *testing.T) {
	cases := []struct {
		raw  string
		want Duration
	}{{"1h", api.N1H}, {" 8H ", api.N8H}, {"24h", api.N24H}, {"7D", api.N7D}}
	for _, c := range cases {
		got, err := ParseDuration(c.raw)
		if err != nil || got != c.want {
			t.Errorf("ParseDuration(%q) = %q, %v; want %q", c.raw, got, err, c.want)
		}
	}
	for _, raw := range []string{"", "2h", "1d", "60m"} {
		if _, err := ParseDuration(raw); qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
			t.Errorf("ParseDuration(%q) err = %v, want exit 2", raw, err)
		}
	}
}

func TestParseRevokeReason(t *testing.T) {
	for _, name := range RevokeReasonNames() {
		got, err := ParseRevokeReason(strings.ToUpper(name))
		if err != nil || string(got) != name {
			t.Errorf("ParseRevokeReason(%q) = %q, %v", strings.ToUpper(name), got, err)
		}
	}
	if _, err := ParseRevokeReason("bored"); qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
		t.Errorf("ParseRevokeReason(bored) err = %v, want exit 2", err)
	}
}

func TestResolveGrantee(t *testing.T) {
	a := uuid.MustParse("0190cccc-0000-4000-8000-000000000001")
	b := uuid.MustParse("0190cccc-0000-4000-8000-000000000002")
	members := []Member{{ID: a, Email: "Alice@Example.com"}, {ID: b, Email: "bob@example.com"}}

	if got, err := ResolveGrantee(members, " alice@example.COM ", "table/42"); err != nil || got != a {
		t.Errorf("ResolveGrantee = %s, %v; want %s", got, err, a)
	}
	if _, err := ResolveGrantee(members, "carol@example.com", "table/42"); qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
		t.Errorf("no match err = %v, want exit 2", err)
	}
	dup := []Member{members[0], members[1], {ID: b, Email: "alice@example.com"}}
	_, err := ResolveGrantee(dup, "alice@example.com", "table/42")
	if qerrors.GetExitCode(err) != qerrors.ExitBadUsage || !strings.Contains(err.Error(), a.String()) {
		t.Errorf("ambiguous err = %v, want exit 2 naming both ids", err)
	}
}
