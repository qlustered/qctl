package auth

import (
	"context"
	"time"

	qauth "github.com/qlustered/qctl/internal/auth"
)

// sessionStatus is what qctl can say about whether a listed CLI session can
// still authenticate anything.
//
// The server does not answer that question and cannot be made to: a Delegation
// row -- what `auth sessions` lists -- carries created_at and revoked_at and no
// expiry column at all, and list_my_delegations filters on revoked_at IS NULL
// and nothing else. Liveness lives one table down, on the API tokens minted
// under the delegation, so every value below is qctl's own reading of that
// join rather than a field off the wire.
type sessionStatus string

const (
	// statusActive: at least one token under this delegation is unrevoked and
	// has not yet expired. This is the only value that means the session can
	// reach the org.
	statusActive sessionStatus = "active"

	// statusExpired: the delegation has tokens, none can authenticate, and not
	// every one of them was revoked -- so at least one simply timed out. CLI
	// tokens last 24 hours by default, and the delegation behind them never
	// expires, which is why this is the ordinary state of accumulated clutter.
	statusExpired sessionStatus = "expired"

	// statusRevoked: the delegation has tokens, none can authenticate, and every
	// one of them was revoked. Distinct from statusExpired rather than a shade
	// of it: DELETE /users/me/api-tokens/{id} kills a token without touching its
	// delegation, so this says somebody deliberately ended the credential and
	// left the delegation row behind. qctl calls neither that route nor
	// anything that produces this state, so only a direct API call reaches it --
	// but printing "expired" over a deliberate revocation is a claim the output
	// would have no way to take back.
	statusRevoked sessionStatus = "revoked"

	// statusUnknown: qctl could not classify this session. Two causes, and the
	// warning on stderr is what tells them apart: no token row names the
	// delegation at all, or the token lookup failed and NO session could be
	// classified. The first is unreachable through qctl today -- the two routes
	// that construct a Delegation each construct exactly one ApiToken with it --
	// which is why it is reported as "cannot tell" rather than folded into
	// statusExpired. Nothing bulk-revokes an unknown: see revokeExpiredSessions.
	statusUnknown sessionStatus = "unknown"
)

// sessionLiveness holds a delegation's token status and usage.
type sessionLiveness struct {
	Status        sessionStatus
	ExpiresAt     *time.Time
	FirstUsedAt   *time.Time
	ExpiredUnused bool
}

// fetchSessionLiveness reads the org's API tokens and reduces them to one
// verdict per delegation.
func fetchSessionLiveness(
	ctx context.Context,
	client *qauth.Client,
	accessToken, orgID string,
) (map[string]sessionLiveness, error) {
	tokens, err := client.ListAPITokens(ctx, accessToken, orgID)
	if err != nil {
		return nil, err
	}
	return deriveSessionLiveness(tokens, time.Now()), nil
}

// deriveSessionLiveness groups tokens by the delegation they belong to and
// classifies each group. Delegations no token names are simply absent, which
// livenessFor reads as statusUnknown -- the map cannot distinguish "I looked
// and found nothing" from "I never looked", so the caller that failed to look
// is the one that has to say so.
func deriveSessionLiveness(tokens []qauth.APIToken, now time.Time) map[string]sessionLiveness {
	byDelegation := make(map[string][]qauth.APIToken, len(tokens))
	for _, token := range tokens {
		byDelegation[token.DelegationID] = append(byDelegation[token.DelegationID], token)
	}

	out := make(map[string]sessionLiveness, len(byDelegation))
	for delegationID, group := range byDelegation {
		live := classifyTokens(group, now)
		live.FirstUsedAt, live.ExpiredUnused = classifyTokenUsage(group, now)
		out[delegationID] = live
	}
	return out
}

// classifyTokenUsage returns the earliest first use and whether every token expired unused.
func classifyTokenUsage(tokens []qauth.APIToken, now time.Time) (*time.Time, bool) {
	var firstUsedAt *time.Time
	allExpired := len(tokens) > 0
	for _, token := range tokens {
		if token.FirstUsedAt != nil && (firstUsedAt == nil || token.FirstUsedAt.Before(*firstUsedAt)) {
			firstUsedAt = token.FirstUsedAt
		}
		if token.ExpiresAt.After(now) {
			allExpired = false
		}
	}
	return firstUsedAt, allExpired && firstUsedAt == nil
}

// classifyTokens reduces one delegation's tokens to a single verdict. The
// group is never empty: it is built from the tokens themselves.
func classifyTokens(tokens []qauth.APIToken, now time.Time) sessionLiveness {
	var latest, latestLive *time.Time
	allRevoked := true

	for _, token := range tokens {
		expires := token.ExpiresAt
		if token.RevokedAt == nil {
			allRevoked = false
		}
		if latest == nil || expires.After(*latest) {
			latest = &expires
		}
		// A token that authenticates is one nothing revoked whose expiry is
		// still ahead of us. Either half alone is not enough: a revoked token
		// keeps its future expires_at, and an expired one is usually unrevoked.
		if token.RevokedAt != nil || !expires.After(now) {
			continue
		}
		if latestLive == nil || expires.After(*latestLive) {
			latestLive = &expires
		}
	}

	switch {
	case latestLive != nil:
		return sessionLiveness{Status: statusActive, ExpiresAt: latestLive}
	case allRevoked:
		return sessionLiveness{Status: statusRevoked, ExpiresAt: latest}
	default:
		return sessionLiveness{Status: statusExpired, ExpiresAt: latest}
	}
}

// livenessFor reads one delegation's verdict out of the join, defaulting to
// statusUnknown. The default is what makes a nil map -- the shape a failed
// lookup hands back -- classify every session as "cannot tell" instead of as
// the zero-value empty string.
func livenessFor(byDelegation map[string]sessionLiveness, delegationID string) sessionLiveness {
	if live, ok := byDelegation[delegationID]; ok {
		return live
	}
	return sessionLiveness{Status: statusUnknown}
}
