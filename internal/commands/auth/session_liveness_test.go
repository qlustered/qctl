package auth

import (
	"testing"
	"time"

	qauth "github.com/qlustered/qctl/internal/auth"
)

func tokenFor(delegationID string, expiresIn time.Duration, revokedAgo time.Duration) qauth.APIToken {
	token := qauth.APIToken{
		DelegationID: delegationID,
		ExpiresAt:    livenessNow.Add(expiresIn),
	}
	if revokedAgo != 0 {
		at := livenessNow.Add(-revokedAgo)
		token.RevokedAt = &at
	}
	return token
}

// livenessNow is fixed rather than time.Now(): every verdict here turns on a
// comparison against "now", and a clock that moves between building the
// fixtures and classifying them is exactly how a boundary case becomes flaky.
var livenessNow = time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)

// TestDeriveSessionLiveness_ReducesEachDelegationsTokens pins the whole
// vocabulary in one place, including the shapes the CLI cannot currently
// produce but the wire allows: the schema is a list of tokens per delegation,
// so a reader must not be able to assume the single row the exchange mints.
func TestDeriveSessionLiveness_ReducesEachDelegationsTokens(t *testing.T) {
	soon := livenessNow.Add(2 * time.Hour)
	later := livenessNow.Add(20 * time.Hour)
	dead := livenessNow.Add(-48 * time.Hour)

	tokens := []qauth.APIToken{
		// One live token: the ordinary session.
		tokenFor("live", 2*time.Hour, 0),
		// Two live tokens: the later expiry is the one that matters, and it is
		// deliberately listed SECOND so a reducer that keeps the first fails.
		tokenFor("two-live", 2*time.Hour, 0),
		tokenFor("two-live", 20*time.Hour, 0),
		// A dead token beside a live one is still a live session.
		tokenFor("mixed", -48*time.Hour, 0),
		tokenFor("mixed", 2*time.Hour, 0),
		// A live token beside a REVOKED one that outlives it: the session is
		// active, and the expiry it reports has to be the live token's, not the
		// later one belonging to a credential nobody can use.
		tokenFor("live-beside-revoked", 2*time.Hour, 0),
		tokenFor("live-beside-revoked", 20*time.Hour, time.Hour),
		// Expired: nothing revoked it, its time simply ran out.
		tokenFor("expired", -48*time.Hour, 0),
		// Revoked: somebody ended the credential. Its expires_at is still in
		// the FUTURE, which is why liveness cannot be read off expiry alone.
		tokenFor("revoked", 20*time.Hour, time.Hour),
		// Revoked and expired together stays revoked only if EVERY token was
		// revoked; here one merely expired, so the session reads expired.
		tokenFor("mixed-dead", 20*time.Hour, time.Hour),
		tokenFor("mixed-dead", -48*time.Hour, 0),
	}

	got := deriveSessionLiveness(tokens, livenessNow)

	wantStatus := map[string]sessionStatus{
		"live":                statusActive,
		"two-live":            statusActive,
		"mixed":               statusActive,
		"expired":             statusExpired,
		"revoked":             statusRevoked,
		"mixed-dead":          statusExpired,
		"live-beside-revoked": statusActive,
	}
	for delegationID, want := range wantStatus {
		if got[delegationID].Status != want {
			t.Errorf("%s status = %q, want %q", delegationID, got[delegationID].Status, want)
		}
	}
	// A delegation no token names is absent, which livenessFor reads as
	// unknown -- the map itself must not invent a verdict for it.
	if _, ok := got["tokenless"]; ok {
		t.Error("a delegation with no tokens got an entry; it must fall through to unknown")
	}
	if live := livenessFor(got, "tokenless"); live.Status != statusUnknown || live.ExpiresAt != nil {
		t.Errorf("livenessFor(absent) = %+v, want unknown with no expiry", live)
	}
	// A nil map is what a failed lookup hands back, and every session must
	// classify as "cannot tell" rather than as the zero-value empty string.
	if live := livenessFor(nil, "live"); live.Status != statusUnknown {
		t.Errorf("livenessFor(nil map) = %q, want %q", live.Status, statusUnknown)
	}

	wantExpiry := map[string]time.Time{
		"two-live": later,
		"live":     soon,
		"expired":  dead,
		// The expiry reported for a dead session is the latest one it had, not
		// the first to lapse: that is when the session actually stopped working.
		"mixed-dead": livenessNow.Add(20 * time.Hour),
	}
	for delegationID, want := range wantExpiry {
		if at := got[delegationID].ExpiresAt; at == nil || !at.Equal(want) {
			t.Errorf("%s expires_at = %v, want %v", delegationID, at, want)
		}
	}
	// An ACTIVE session reports the expiry of a token that can authenticate,
	// never the later expiry of a revoked one sitting beside it.
	if at := got["live-beside-revoked"].ExpiresAt; at == nil || !at.Equal(soon) {
		t.Errorf("live-beside-revoked expires_at = %v, want the live token's %v, not the revoked token's %v",
			at, soon, later)
	}
}

func TestDeriveSessionLiveness_FirstUseAndExpiredUnused(t *testing.T) {
	early := livenessNow.Add(-48 * time.Hour)
	later := livenessNow.Add(-24 * time.Hour)
	used := tokenFor("session", -time.Hour, 0)
	used.FirstUsedAt = &later
	earlierUsed := tokenFor("session", -2*time.Hour, time.Hour)
	earlierUsed.FirstUsedAt = &early

	cases := []struct {
		name   string
		tokens []qauth.APIToken
		first  *time.Time
		unused bool
	}{
		{name: "missing token"},
		{name: "fresh unused", tokens: []qauth.APIToken{tokenFor("session", time.Hour, 0)}},
		{name: "expired unused", tokens: []qauth.APIToken{tokenFor("session", -time.Hour, 0)}, unused: true},
		{name: "expiry boundary", tokens: []qauth.APIToken{tokenFor("session", 0, 0)}, unused: true},
		{name: "revoked before expiry", tokens: []qauth.APIToken{tokenFor("session", time.Hour, time.Hour)}},
		{name: "revoked after expiry", tokens: []qauth.APIToken{tokenFor("session", -time.Hour, time.Hour)}, unused: true},
		{name: "expired used", tokens: []qauth.APIToken{used}, first: &later},
		{name: "all expired unused", tokens: []qauth.APIToken{
			tokenFor("session", -time.Hour, 0), tokenFor("session", -2*time.Hour, time.Hour),
		}, unused: true},
		{name: "one still awaiting expiry", tokens: []qauth.APIToken{
			tokenFor("session", -time.Hour, 0), tokenFor("session", time.Hour, time.Hour),
		}},
		{name: "one used among unused", tokens: []qauth.APIToken{
			tokenFor("session", -time.Hour, 0), used,
		}, first: &later},
		{name: "earliest use includes revoked tokens", tokens: []qauth.APIToken{used, earlierUsed}, first: &early},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := livenessFor(deriveSessionLiveness(tc.tokens, livenessNow), "session")
			if got.ExpiredUnused != tc.unused {
				t.Errorf("ExpiredUnused = %v, want %v", got.ExpiredUnused, tc.unused)
			}
			if tc.first == nil {
				if got.FirstUsedAt != nil {
					t.Errorf("FirstUsedAt = %v, want nil", got.FirstUsedAt)
				}
			} else if got.FirstUsedAt == nil || !got.FirstUsedAt.Equal(*tc.first) {
				t.Errorf("FirstUsedAt = %v, want %v", got.FirstUsedAt, tc.first)
			}
		})
	}
}
