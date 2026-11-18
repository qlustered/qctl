package auth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"unicode/utf8"

	qerrors "github.com/qlustered/qctl/internal/errors"
)

const (
	// TokenNameMaxLen is the backend's max_length for token_name on
	// POST /api/auth/cli/exchange (CLIExchangeRequest,
	// capi/capi/views/oauth_view.py).
	TokenNameMaxLen = 255

	// tokenNameSuffixLen is the "-" plus 8 hex characters MintTokenName appends.
	tokenNameSuffixLen = 9

	// TokenNameBaseMaxLen is the longest base MintTokenName accepts.
	TokenNameBaseMaxLen = TokenNameMaxLen - tokenNameSuffixLen
)

// MintTokenName builds the token_name qctl sends to the exchange endpoint.
//
// The backend stores this string on BOTH the Delegation and the ApiToken row
// and echoes it back from GET /orgs/{org}/users/me/delegations. It is the only
// field in that response qctl can correlate against a token it is holding: the
// exchange returns neither api_token_id nor delegation_id, and the bearer token
// itself is AES-GCM ciphertext qctl cannot decode. So the name has to be unique
// per login, and the random suffix is what makes it so — without it, two logins
// from one machine are indistinguishable and logout cannot tell which delegation
// to revoke.
//
// base is the user's --token-name when supplied, otherwise qctl@<hostname>.
func MintTokenName(base string) (string, error) {
	if base == "" {
		base = defaultTokenNameBase()
	} else if err := ValidateTokenNameBase(base); err != nil {
		return "", err
	}

	// Enforce the backend's cap here rather than trusting whichever branch
	// produced the base. A 400 from the exchange would abort the login, and a
	// name the server silently shortened would leave logout unable to match it.
	base = truncateTokenNameBase(base)

	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("failed to generate a unique token name suffix: %w", err)
	}

	return fmt.Sprintf("%s-%s", base, hex.EncodeToString(buf)), nil
}

// ValidateTokenNameBase rejects a --token-name longer than TokenNameBaseMaxLen
// with a bad-usage error.
func ValidateTokenNameBase(base string) error {
	if len(base) > TokenNameBaseMaxLen {
		return qerrors.NewBadUsagef(
			"--token-name is %d characters; the maximum is %d (qctl appends a %d-character unique suffix so it can revoke this session later)",
			len(base), TokenNameBaseMaxLen, tokenNameSuffixLen)
	}
	return nil
}

// defaultTokenNameBase names the machine, truncating rather than erroring: the
// user did not choose this string, so a long hostname must not fail the login.
func defaultTokenNameBase() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown-host"
	}
	return truncateTokenNameBase("qctl@" + host)
}

// truncateTokenNameBase caps a base at TokenNameBaseMaxLen, dropping the
// trailing partial character rather than splitting it. A half rune is invalid
// UTF-8, which encoding/json rewrites as U+FFFD on the way out: the server
// would then store bytes the local credential does not have, and logout's
// exact-name match against a delegation would silently find nothing.
func truncateTokenNameBase(base string) string {
	if len(base) <= TokenNameBaseMaxLen {
		return base
	}
	cut := TokenNameBaseMaxLen
	for cut > 0 && !utf8.RuneStart(base[cut]) {
		cut--
	}
	return base[:cut]
}
