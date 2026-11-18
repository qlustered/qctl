package auth

import (
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	qerrors "github.com/qlustered/qctl/internal/errors"
)

var mintedSuffix = regexp.MustCompile(`-[0-9a-f]{8}$`)

func TestMintTokenName_DefaultBaseIsQctlAtHostname(t *testing.T) {
	got, err := MintTokenName("")
	if err != nil {
		t.Fatalf("MintTokenName(\"\") error = %v", err)
	}
	if !strings.HasPrefix(got, "qctl@") {
		t.Errorf("MintTokenName(\"\") = %q, want a \"qctl@\" prefix", got)
	}
	if !mintedSuffix.MatchString(got) {
		t.Errorf("MintTokenName(\"\") = %q, want a -<8 hex> suffix", got)
	}
}

func TestMintTokenName_KeepsExplicitBaseAsPrefix(t *testing.T) {
	got, err := MintTokenName("my-laptop")
	if err != nil {
		t.Fatalf("MintTokenName error = %v", err)
	}
	if !strings.HasPrefix(got, "my-laptop-") {
		t.Errorf("MintTokenName(\"my-laptop\") = %q, want a \"my-laptop-\" prefix", got)
	}
	if !mintedSuffix.MatchString(got) {
		t.Errorf("MintTokenName(\"my-laptop\") = %q, want a -<8 hex> suffix", got)
	}
}

// The suffix is the whole point: two logins from one machine must produce two
// distinguishable delegations, or logout cannot tell them apart.
func TestMintTokenName_IsUniquePerCall(t *testing.T) {
	seen := make(map[string]bool, 100)
	for i := 0; i < 100; i++ {
		got, err := MintTokenName("same-base")
		if err != nil {
			t.Fatalf("MintTokenName error = %v", err)
		}
		if seen[got] {
			t.Fatalf("MintTokenName produced a duplicate: %q", got)
		}
		seen[got] = true
	}
}

func TestMintTokenName_RejectsOverlongBaseWithExitCode2(t *testing.T) {
	_, err := MintTokenName(strings.Repeat("x", TokenNameBaseMaxLen+1))
	if err == nil {
		t.Fatal("MintTokenName with an overlong base returned nil error")
	}
	if got := qerrors.GetExitCode(err); got != 2 {
		t.Errorf("exit code = %d, want 2", got)
	}
}

// The backend caps token_name at 255; a base at the documented limit plus the
// suffix must still fit.
func TestMintTokenName_MaxLengthBaseStillFits(t *testing.T) {
	got, err := MintTokenName(strings.Repeat("x", TokenNameBaseMaxLen))
	if err != nil {
		t.Fatalf("MintTokenName at the base limit error = %v", err)
	}
	if len(got) != TokenNameMaxLen {
		t.Errorf("len = %d, want exactly %d", len(got), TokenNameMaxLen)
	}
}

// The name is the ONLY handle logout has on a session, so the bytes qctl sends
// to the server must be exactly the bytes it keeps locally. Truncating a long
// hostname by bytes can cut a multi-byte character in half; encoding/json then
// marshals the invalid tail as U+FFFD, the server stores those bytes instead,
// and logout's exact-name match silently finds nothing to revoke.
func TestTruncateTokenNameBase_CutsOnARuneBoundary(t *testing.T) {
	// Two-byte runes after a five-byte prefix put the byte limit mid-character.
	long := "qctl@" + strings.Repeat("é", TokenNameBaseMaxLen)

	got := truncateTokenNameBase(long)

	if len(got) > TokenNameBaseMaxLen {
		t.Errorf("len = %d, want <= %d", len(got), TokenNameBaseMaxLen)
	}
	if !utf8.ValidString(got) {
		t.Errorf("truncateTokenNameBase(...) = %q, which is not valid UTF-8", got)
	}
	if !strings.HasPrefix(long, got) {
		t.Errorf("truncateTokenNameBase(...) = %q, which is not a prefix of its input", got)
	}
	// Reaching a boundary costs at most one rune; anything more is over-trimming.
	if len(got) < TokenNameBaseMaxLen-utf8.UTFMax {
		t.Errorf("len = %d, truncation gave up more of the name than a rune boundary requires", len(got))
	}
}

func TestTruncateTokenNameBase_LeavesAFittingNameAlone(t *testing.T) {
	const base = "qctl@box"
	if got := truncateTokenNameBase(base); got != base {
		t.Errorf("truncateTokenNameBase(%q) = %q, want it unchanged", base, got)
	}
}

// MintTokenName owns the backend's 255-character cap itself rather than
// trusting whatever produced the base to have respected it.
func TestMintTokenName_DefaultNameFitsTheBackendCap(t *testing.T) {
	got, err := MintTokenName("")
	if err != nil {
		t.Fatalf("MintTokenName(\"\") error = %v", err)
	}
	if len(got) > TokenNameMaxLen {
		t.Errorf("len = %d, want <= %d", len(got), TokenNameMaxLen)
	}
	if !utf8.ValidString(got) {
		t.Errorf("MintTokenName(\"\") = %q, which is not valid UTF-8", got)
	}
}
