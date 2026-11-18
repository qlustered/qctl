package auth

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
)

const copiesEndpoint, copiesOrgID = "api.example.com", "b2c3d4e5-f6a7-8901-bcde-f23456789012"

// memoryKeyring points the keyring seam at an in-memory map and returns it.
func memoryKeyring(t *testing.T) map[string]string {
	t.Helper()
	entries := map[string]string{}
	swapKeyring(t,
		func(_, user string) (string, error) {
			value, ok := entries[user]
			if !ok {
				return "", keyring.ErrNotFound
			}
			return value, nil
		},
		func(_, user, value string) error {
			entries[user] = value
			return nil
		},
		func(_, user string) error {
			if _, ok := entries[user]; !ok {
				return keyring.ErrNotFound
			}
			delete(entries, user)
			return nil
		},
	)
	return entries
}

// seedCopies stores keyringToken in the keyring an hour ago and fileToken in
// the fallback file now, the way a keyring login followed by a login with
// --allow-plaintext-token-store leaves them. An empty token skips that half.
func seedCopies(t *testing.T, entries map[string]string, keyringToken, fileToken string) {
	t.Helper()
	if keyringToken != "" {
		cred := newTestCredential(copiesOrgID, keyringToken)
		cred.CreatedAt = time.Now().Add(-time.Hour)
		data, err := json.Marshal(cred)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		entries[MakeCredentialKey(copiesEndpoint, copiesOrgID)] = string(data)
	}
	if fileToken != "" {
		if _, err := NewCredentialStore(true).Store(copiesEndpoint, newTestCredential(copiesOrgID, fileToken)); err != nil {
			t.Fatalf("store file credential: %v", err)
		}
	}
}

func copyTokens(copies []StoredCopy) []string {
	tokens := make([]string, 0, len(copies))
	for _, c := range copies {
		tokens = append(tokens, c.Credential.AccessToken)
	}
	return tokens
}

func TestRetrieveCopies_ReturnsBothDistinctCredentialsRetrievesPickFirst(t *testing.T) {
	for _, plaintext := range []bool{false, true} {
		t.Run(map[bool]string{false: "keyring mode", true: "plaintext opt-in"}[plaintext], func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			entries := memoryKeyring(t)
			seedCopies(t, entries, "keyring-old", "file-new")

			store := NewCredentialStore(plaintext)
			copies, err := store.RetrieveCopies(copiesEndpoint, copiesOrgID)
			if err != nil {
				t.Fatalf("RetrieveCopies() error = %v", err)
			}
			if len(copies) != 2 {
				t.Fatalf("RetrieveCopies() = %v, want both tokens", copyTokens(copies))
			}
			picked, err := store.Retrieve(copiesEndpoint, copiesOrgID)
			if err != nil {
				t.Fatalf("Retrieve() error = %v", err)
			}
			if copies[0].Credential.AccessToken != picked.AccessToken {
				t.Errorf("first copy = %q, want Retrieve's pick %q", copies[0].Credential.AccessToken, picked.AccessToken)
			}
			if !copies[0].InFile || copies[0].InKeyring || !copies[1].InKeyring || copies[1].InFile {
				t.Errorf("halves = %+v, want the file copy then the keyring copy, one half each", copies)
			}
		})
	}
}

// Under the opt-in Retrieve reads the file alone, so the file copy is the one
// commands authenticate with even when the keyring's is newer.
func TestRetrieveCopies_PlaintextOptInPutsTheFileFirstWhateverItsAge(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	entries := memoryKeyring(t)
	seedCopies(t, entries, "", "file-old")
	newer := newTestCredential(copiesOrgID, "keyring-new")
	newer.CreatedAt = time.Now().Add(time.Hour)
	if _, err := NewCredentialStore(false).Store(copiesEndpoint, newer); err != nil {
		t.Fatalf("store keyring credential: %v", err)
	}

	copies, err := NewCredentialStore(true).RetrieveCopies(copiesEndpoint, copiesOrgID)
	if err != nil {
		t.Fatalf("RetrieveCopies() error = %v", err)
	}
	if got := copyTokens(copies); len(got) != 2 || got[0] != "file-old" {
		t.Errorf("RetrieveCopies() = %v, want [file-old keyring-new]", got)
	}
	copies, err = NewCredentialStore(false).RetrieveCopies(copiesEndpoint, copiesOrgID)
	if err != nil {
		t.Fatalf("RetrieveCopies() error = %v", err)
	}
	if got := copyTokens(copies); len(got) != 2 || got[0] != "keyring-new" {
		t.Errorf("without the opt-in RetrieveCopies() = %v, want [keyring-new file-old]", got)
	}
}

func TestRetrieveCopies_OneTokenInBothHalvesIsOneCopy(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	entries := memoryKeyring(t)
	seedCopies(t, entries, "same-token", "same-token")

	copies, err := NewCredentialStore(false).RetrieveCopies(copiesEndpoint, copiesOrgID)
	if err != nil {
		t.Fatalf("RetrieveCopies() error = %v", err)
	}
	if len(copies) != 1 || !copies[0].InKeyring || !copies[0].InFile {
		t.Errorf("RetrieveCopies() = %+v, want one copy held by both halves", copies)
	}
}

// Logout reads through RetrieveCopies, so under the opt-in it must see the
// keyring entry Delete is about to remove.
func TestRetrieveCopies_PlaintextOptInStillReadsTheKeyring(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	entries := memoryKeyring(t)
	seedCopies(t, entries, "keyring-only", "")

	copies, err := NewCredentialStore(true).RetrieveCopies(copiesEndpoint, copiesOrgID)
	if err != nil {
		t.Fatalf("RetrieveCopies() error = %v", err)
	}
	if got := copyTokens(copies); len(got) != 1 || got[0] != "keyring-only" || !copies[0].InKeyring {
		t.Errorf("RetrieveCopies() = %+v, want the keyring copy", copies)
	}
}

// Under the opt-in a keyring problem never becomes a logout failure: a refusal
// and an unparseable entry both read as no copy, as they did when logout never
// asked the keyring. Without the opt-in both fail, exactly as Retrieve does.
func TestRetrieveCopies_KeyringProblemsFailOnlyWithoutTheOptIn(t *testing.T) {
	refused := errors.New("org.freedesktop.DBus.Error.AccessDenied")
	// What the keyring answers for the credential's own entry.
	cases := map[string]struct {
		value string
		err   error
	}{
		"refused":     {err: refused},
		"unparseable": {value: "{not json"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			swapKeyring(t, func(_, user string) (string, error) {
				if user == keyringReachableProbeKey {
					return "", keyring.ErrNotFound
				}
				return tc.value, tc.err
			}, nil, nil)

			_, err := NewCredentialStore(true).RetrieveCopies(copiesEndpoint, copiesOrgID)
			if !errors.Is(err, ErrCredentialNotFound) {
				t.Errorf("opt-in RetrieveCopies() error = %v, want ErrCredentialNotFound", err)
			}

			_, want := NewCredentialStore(false).Retrieve(copiesEndpoint, copiesOrgID)
			_, got := NewCredentialStore(false).RetrieveCopies(copiesEndpoint, copiesOrgID)
			if got == nil || errors.Is(got, ErrCredentialNotFound) || want == nil || got.Error() != want.Error() {
				t.Errorf("RetrieveCopies() error = %v, want Retrieve's %v", got, want)
			}
		})
	}
}

// A readable keyring copy is not blocked by a file qctl cannot parse, the same
// rule Retrieve applies, and under the opt-in too: the file's bytes are left
// for Delete to report.
func TestRetrieveCopies_AnUnreadableFileDoesNotHideTheKeyringCopy(t *testing.T) {
	for _, plaintext := range []bool{false, true} {
		t.Run(map[bool]string{false: "keyring mode", true: "plaintext opt-in"}[plaintext], func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			entries := memoryKeyring(t)
			seedCopies(t, entries, "keyring-token", "")
			path, err := CredentialsFilePath()
			if err != nil {
				t.Fatalf("CredentialsFilePath() error = %v", err)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			if err := os.WriteFile(path, []byte("{not json"), 0600); err != nil {
				t.Fatalf("write: %v", err)
			}

			copies, err := NewCredentialStore(plaintext).RetrieveCopies(copiesEndpoint, copiesOrgID)
			if err != nil {
				t.Fatalf("RetrieveCopies() error = %v", err)
			}
			if got := copyTokens(copies); len(got) != 1 || got[0] != "keyring-token" {
				t.Errorf("RetrieveCopies() = %v, want [keyring-token]", got)
			}
		})
	}
}

func TestDeleteOneCopy_LeavesTheOtherHalfAlone(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	entries := memoryKeyring(t)
	seedCopies(t, entries, "keyring-token", "file-token")
	store := NewCredentialStore(false)

	if err := store.DeleteFileCopy(copiesEndpoint, copiesOrgID); err != nil {
		t.Fatalf("DeleteFileCopy() error = %v", err)
	}
	copies, err := store.RetrieveCopies(copiesEndpoint, copiesOrgID)
	if err != nil {
		t.Fatalf("RetrieveCopies() error = %v", err)
	}
	if got := copyTokens(copies); len(got) != 1 || got[0] != "keyring-token" {
		t.Errorf("after DeleteFileCopy = %v, want only the keyring copy", got)
	}

	seedCopies(t, entries, "", "file-token")
	if err := store.DeleteKeyringCopy(copiesEndpoint, copiesOrgID); err != nil {
		t.Fatalf("DeleteKeyringCopy() error = %v", err)
	}
	copies, err = store.RetrieveCopies(copiesEndpoint, copiesOrgID)
	if err != nil {
		t.Fatalf("RetrieveCopies() error = %v", err)
	}
	if got := copyTokens(copies); len(got) != 1 || got[0] != "file-token" {
		t.Errorf("after DeleteKeyringCopy = %v, want only the file copy", got)
	}
}
