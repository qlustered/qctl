package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/pkg/privatefile"
	"github.com/zalando/go-keyring"
)

const (
	// KeyringServiceName is the service name used for OS keyring storage
	KeyringServiceName = "qctl"

	// CredentialsFileName is the filename for fallback credential storage
	CredentialsFileName = "credentials.json"
)

// credFileMutex protects concurrent access to the credentials file
var credFileMutex sync.Mutex

// The store reaches the OS keyring through these variables rather than by
// calling github.com/zalando/go-keyring directly, so a test can make ONE
// operation fail. go-keyring's own MockInitWithError fails every operation
// with the same error, which cannot express the case Delete exists to detect:
// a delete that failed while the entry is still readable. They follow the
// same seam shape the repo already uses for config.GetConfigPath
// (internal/config) and UseContextEnsureSpecCachedFunc
// (internal/commands/config), and are exported so tests in the command
// packages can drive them too.
var (
	// KeyringGetFunc reads one entry from the OS keyring.
	KeyringGetFunc = keyring.Get
	// KeyringSetFunc writes one entry to the OS keyring.
	KeyringSetFunc = keyring.Set
	// KeyringDeleteFunc removes one entry from the OS keyring.
	KeyringDeleteFunc = keyring.Delete
)

// keyringProbe is what reading one key told us about the keyring itself.
type keyringProbe int

const (
	// keyringSilent means the keyring could not be consulted at all: no
	// Secret Service on this machine, an unsupported platform, a D-Bus
	// failure, a collection that refuses reads. It is the catch-all on
	// purpose -- on Linux there is NO sentinel for "there is no keyring
	// here": go-keyring returns whatever dbus said. Anything qctl cannot
	// positively recognise has to be treated as "no answer", because the
	// alternative is failing every logout on containers and CI.
	keyringSilent keyringProbe = iota
	// keyringAbsent means the keyring answered, and holds no such entry.
	keyringAbsent
	// keyringPresent means the keyring answered, and the entry is readable.
	keyringPresent
)

// keyringReachableProbeKey is a key no login can ever have written:
// MakeCredentialKey always joins two halves with "|", so a key without one
// cannot collide with a stored credential. Reading it is how the store asks
// "is there a keyring on this machine at all?" -- an answer of "no such
// entry" proves one is up and answering.
const keyringReachableProbeKey = "qctl-keyring-reachability-probe"

// probeKeyring reads key and reports what the answer says about the keyring.
//
// Every probe is one more keyring access, and on a locked collection some
// desktop keyring agents answer each access with its own unlock dialog. The
// probe only runs after a real operation has already failed, so this is
// bounded -- but it is the cause of a "qctl asks me to unlock twice" report.
// It is the price of telling a refusal from an absent keyring at all.
func probeKeyring(key string) keyringProbe {
	_, err := KeyringGetFunc(KeyringServiceName, key)
	switch {
	case err == nil:
		return keyringPresent
	case errors.Is(err, keyring.ErrNotFound):
		return keyringAbsent
	default:
		return keyringSilent
	}
}

// keyringRefused reports whether err came from a keyring that is up and said
// no, as opposed to one that is absent or holds nothing.
//
// The two sentinels are decided without a second call. Everything else is
// ambiguous by value -- a refusal and a machine with no Secret Service look
// identical -- so the question is settled by asking the keyring something it
// can definitely answer: if a read of a key that cannot exist comes back
// "no such entry", the keyring is up, and the original failure was its answer.
//
// A keyring that refuses reads as well as writes (a locked collection) reads
// as "not refused" here, so the store stays quiet. That is deliberate: silence
// is the pre-existing behaviour, and it is the safe side of a rule whose other
// failure mode is breaking every logout on a headless machine.
func keyringRefused(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, keyring.ErrNotFound) || errors.Is(err, keyring.ErrUnsupportedPlatform) {
		return false
	}
	return probeKeyring(keyringReachableProbeKey) != keyringSilent
}

// ErrCredentialNotFound reports that no credential is stored for the requested
// endpoint and organization. Callers MUST distinguish it from the other errors
// Retrieve can return -- an unreadable or unparseable credentials file looks
// identical otherwise, and treating that as "nothing stored" turns a real
// failure into a silent success.
var ErrCredentialNotFound = errors.New("credential not found")

// Credential represents a stored OAuth bearer token
type Credential struct {
	CreatedAt      time.Time `json:"created_at"`
	AccessToken    string    `json:"access_token"`
	ExpiresAt      time.Time `json:"expires_at"`
	OrganizationID string    `json:"organization_id"`
	TokenName      string    `json:"token_name,omitempty"` // For audit trail
}

// IsExpired returns true if the credential has expired
func (c *Credential) IsExpired() bool {
	if c.ExpiresAt.IsZero() {
		return false // No expiration set
	}
	return time.Now().After(c.ExpiresAt)
}

// CredentialStore manages credential storage and retrieval.
//
// By default it stores credentials in the OS keyring and transparently falls
// back to a 0600 file in the config directory. There are TWO fallback cases,
// not one: a keyring that is not there at all (e.g. a headless machine with no
// Secret Service/dbus), which is silent because it is the normal state on
// servers, containers and CI; and a keyring that is up, still answering reads
// of other entries, and refuses -- a downgrade rather than the documented
// default, which Store reports back as a warning for the command layer to
// print. The silent case mirrors how tools like kubectl and the Claude Code
// CLI behave, so qctl "just works" without any opt-in.
//
// preferPlaintext is the explicit opt-in surfaced via the
// QCTL_ALLOW_PLAINTEXT_TOKENS env var / --allow-plaintext-token-store flag. It
// makes Store and Retrieve go straight to the file without consulting the
// keyring; Delete deliberately still attempts the keyring, so an entry left by
// an earlier keyring login has its removal attempted (see Delete's doc
// comment). Tests keep off the real keyring through the
// KeyringGetFunc/KeyringSetFunc/KeyringDeleteFunc seam -- not through this
// field, which Delete would walk straight past.
type CredentialStore struct {
	preferPlaintext bool
}

// NewCredentialStore creates a new credential store. Pass preferPlaintext=true
// to make Store and Retrieve use the plaintext fallback file directly without
// consulting the keyring; Delete still tries the keyring either way, for the
// reason its doc comment gives.
func NewCredentialStore(preferPlaintext bool) *CredentialStore {
	return &CredentialStore{
		preferPlaintext: preferPlaintext,
	}
}

// MakeCredentialKey creates a credential key from endpoint and organization ID
// Format: {endpoint}|{org_id}
func MakeCredentialKey(endpointKey, orgID string) string {
	return fmt.Sprintf("%s|%s", endpointKey, orgID)
}

// ParseCredentialKey splits a credential key into endpoint and organization ID
func ParseCredentialKey(key string) (endpointKey, orgID string, err error) {
	parts := strings.SplitN(key, "|", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid credential key format")
	}
	return parts[0], parts[1], nil
}

// Store stores a credential in the keyring, falling back to the plaintext file.
//
// It returns a non-empty warning when that fallback was a DOWNGRADE rather than
// the documented default: a keyring that is up -- and still answering reads of
// other entries, which is the only thing that tells that apart from a machine
// that has no keyring at all -- refused the write, so the bearer token went to
// a 0600 file readable by the user's account, when the user believes it is
// under OS protection. keyringRefused draws that line; its doc comment has the
// case it deliberately leaves on the silent side. A machine with no keyring
// gets no warning -- that path is the deliberate "qctl just works on servers,
// CI and containers" behaviour, and a warning on every login there is noise
// nobody can act on.
//
// The warning is returned rather than printed so the store stays free of
// output policy, matching how logout's endSessionOnServer hands its note back
// to the command layer. Callers must decide what to do with it: a caller that
// wants to drop it has to say so.
//
// On that downgrade path Store also removes any older keyring entry under the
// same key. A surviving entry could win over the file if its recorded creation
// time is later. Store refuses the write if it cannot confirm removal.
func (cs *CredentialStore) Store(endpointKey string, cred *Credential) (string, error) {
	if cred.OrganizationID == "" {
		return "", fmt.Errorf("organization ID is required")
	}

	key := MakeCredentialKey(endpointKey, cred.OrganizationID)

	// In plaintext-only mode, skip the keyring entirely. An explicit opt-in is
	// not a downgrade, so there is nothing to warn about.
	if cs.preferPlaintext {
		return "", cs.storeToFile(key, *cred)
	}

	credJSON, err := json.Marshal(cred)
	if err != nil {
		return "", fmt.Errorf("failed to marshal credential: %w", err)
	}

	setErr := KeyringSetFunc(KeyringServiceName, key, string(credJSON))
	if setErr == nil {
		return "", nil
	}

	// The keyring did not take it — the file is the fallback either way.
	// Remember what the file held first: if the stale keyring entry below
	// cannot be removed, this write has to be undone.
	previousFile, previousFileErr := cs.retrieveFromFile(key)
	if err := cs.storeToFile(key, *cred); err != nil {
		return "", err
	}
	if !keyringRefused(setErr) {
		return "", nil
	}

	location := CredentialsFileLocation()

	// A refused overwrite can leave a readable entry under the same key.
	// Remove it after the fallback write so its recorded creation time cannot
	// make ordinary reads choose the old credential. A failed removal makes
	// the outcome uncertain, so refuse the store unless the entry is gone.
	if err := removeShadowingKeyringEntry(key); err != nil {
		// Put the file back before reporting the failure. The new token is in
		// it, and the caller treats this error as "the credential was not
		// stored": it revokes the session just minted. A keyring that cannot
		// answer a read would then send Retrieve through to the file and hand
		// every later command that revoked token -- 401s, rather than the
		// clean "not logged in" the error promises. Restoring also makes the
		// caller's claim that qctl keeps using the credential it had before
		// this login true rather than merely usually true.
		cs.restoreFallbackFile(key, previousFile, previousFileErr)
		return "", fmt.Errorf(
			"the OS keyring refused to replace or remove its older credential for this server and "+
				"organization, which could hide the new one written to %s: %w\n\n%s",
			location, err, fmt.Sprintf(
				"Remove the keyring entry (service %q, entry %q) with your OS keyring tool, or set "+
					"QCTL_ALLOW_PLAINTEXT_TOKENS=1 to keep qctl out of the keyring, then log in again.",
				KeyringServiceName, key))
	}

	return fmt.Sprintf(
		"Warning: the OS keyring refused to store the token (%v).\n"+
			"It was written to %s instead, in a file readable by your user account (mode 0600).",
		setErr, location), nil
}

// restoreFallbackFile undoes Store's fallback write, putting back whatever the
// file held under key beforehand. It is best-effort by design: it runs only on
// a path that is already returning an error, and a restore that fails leaves
// exactly the state that would have been left without it.
//
// An unreadable prior entry is left alone rather than deleted. The read failed
// before the write too, so there is no known-good state to put back, and
// removing the new token would only take away the one credential the file is
// known to hold.
func (cs *CredentialStore) restoreFallbackFile(key string, previous *Credential, previousErr error) {
	switch {
	case previousErr == nil:
		_ = cs.storeToFile(key, *previous)
	case errors.Is(previousErr, ErrCredentialNotFound):
		_ = cs.deleteFromFile(key)
	}
}

// Retrieve retrieves a credential from the keyring or fallback file.
//
// When preferPlaintext is set, only the file is read. Otherwise a newer file
// credential takes precedence over an older keyring credential. This lets a
// later command use a token stored by --allow-plaintext-token-store.
// A missing or unreadable file does not invalidate a readable keyring entry.
//
// What the keyring's failure decides is only what to say when
// NEITHER place has it. "Nothing is stored" is a fact qctl can state when the
// keyring answered and had no such entry -- but a keyring that is up and
// refused establishes nothing, and reporting that as ErrCredentialNotFound
// makes every command advise `qctl auth login`, which overwrites
// credentials.json and with it the token_name that `auth logout` needs to
// revoke a session that may still be live on the server.
func (cs *CredentialStore) Retrieve(endpointKey, orgID string) (*Credential, error) {
	key := MakeCredentialKey(endpointKey, orgID)

	// In plaintext-only mode, skip the keyring entirely to avoid picking up existing entries.
	if cs.preferPlaintext {
		return cs.retrieveFromFile(key)
	}

	credJSON, keyringErr := KeyringGetFunc(KeyringServiceName, key)
	if keyringErr == nil {
		var cred Credential
		if err := json.Unmarshal([]byte(credJSON), &cred); err != nil {
			return nil, fmt.Errorf("failed to unmarshal credential from keyring: %w", err)
		}
		fileCred, fileErr := cs.retrieveFromFile(key)
		if fileErr == nil && cs.fileSupersedes(&cred, fileCred) {
			return fileCred, nil
		}
		return &cred, nil
	}

	// Not in the keyring (or the keyring could not be consulted) — fall back to the file.
	cred, fileErr := cs.retrieveFromFile(key)
	if fileErr == nil {
		return cred, nil
	}
	if errors.Is(fileErr, ErrCredentialNotFound) && keyringRefused(keyringErr) {
		return nil, keyringRefusedReadError(keyringErr)
	}
	return nil, fileErr
}

// fileSupersedes reports whether the file's credential is the one Retrieve
// returns when both halves hold one. Without the plaintext opt-in the newer
// wins and a tie goes to the keyring.
func (cs *CredentialStore) fileSupersedes(keyringCred, fileCred *Credential) bool {
	return cs.preferPlaintext || fileCred.CreatedAt.After(keyringCred.CreatedAt)
}

func keyringRefusedReadError(keyringErr error) error {
	return fmt.Errorf(
		"the OS keyring refused to return the credential, and no fallback credential is stored: %w",
		keyringErr)
}

// StoredCopy is one distinct credential the store holds for a server and
// organization, and the halves that hold it.
type StoredCopy struct {
	Credential *Credential
	InKeyring  bool
	InFile     bool
}

// RetrieveCopies returns every distinct credential stored for endpointKey and
// orgID: two when the keyring and the fallback file hold different access
// tokens, otherwise one. Whenever Retrieve returns a credential, it is the
// first.
//
// Unlike Retrieve, it reads the keyring under the plaintext opt-in too. There,
// a keyring entry that cannot be read or parsed counts as no copy. Without the
// opt-in it fails where Retrieve fails.
func (cs *CredentialStore) RetrieveCopies(endpointKey, orgID string) ([]StoredCopy, error) {
	key := MakeCredentialKey(endpointKey, orgID)

	var keyringCred *Credential
	credJSON, keyringErr := KeyringGetFunc(KeyringServiceName, key)
	if keyringErr == nil {
		var cred Credential
		if err := json.Unmarshal([]byte(credJSON), &cred); err == nil {
			keyringCred = &cred
		} else if !cs.preferPlaintext {
			return nil, fmt.Errorf("failed to unmarshal credential from keyring: %w", err)
		}
	}

	fileCred, fileErr := cs.retrieveFromFile(key)
	if fileErr != nil {
		if keyringCred == nil && !errors.Is(fileErr, ErrCredentialNotFound) {
			return nil, fileErr
		}
		fileCred = nil
	}

	switch {
	case keyringCred == nil && fileCred == nil:
		if !cs.preferPlaintext && keyringRefused(keyringErr) {
			return nil, keyringRefusedReadError(keyringErr)
		}
		return nil, ErrCredentialNotFound
	case fileCred == nil:
		return []StoredCopy{{Credential: keyringCred, InKeyring: true}}, nil
	case keyringCred == nil:
		return []StoredCopy{{Credential: fileCred, InFile: true}}, nil
	}
	return cs.orderCopies(keyringCred, fileCred), nil
}

// orderCopies returns the copies for a credential in each half: one copy
// naming both halves when the access tokens match, otherwise two, with
// Retrieve's pick first.
func (cs *CredentialStore) orderCopies(keyringCred, fileCred *Credential) []StoredCopy {
	fileFirst := cs.fileSupersedes(keyringCred, fileCred)
	if keyringCred.AccessToken == fileCred.AccessToken {
		both := StoredCopy{Credential: keyringCred, InKeyring: true, InFile: true}
		if fileFirst {
			both.Credential = fileCred
		}
		return []StoredCopy{both}
	}
	keyringCopy := StoredCopy{Credential: keyringCred, InKeyring: true}
	fileCopy := StoredCopy{Credential: fileCred, InFile: true}
	if fileFirst {
		return []StoredCopy{fileCopy, keyringCopy}
	}
	return []StoredCopy{keyringCopy, fileCopy}
}

// HasReadableKeyringEntry reports whether the keyring holds an entry for this key.
func (cs *CredentialStore) HasReadableKeyringEntry(endpointKey, orgID string) bool {
	_, err := KeyringGetFunc(KeyringServiceName, MakeCredentialKey(endpointKey, orgID))
	return err == nil
}

// DeleteError reports a Delete that could not clear every copy of the
// credential, and records which halves it did clear.
//
// `qctl auth logout` needs the halves to say what is actually left where. A file
// that cannot be parsed is not evidence that the token is in it. Telling the
// user their logout failed, while pointing them at a file that never held
// their token, is its own kind of untrue report.
type DeleteError struct {
	// KeyringCleared reports that no copy of the credential remains in the OS
	// keyring as far as qctl can tell. "As far as qctl can tell" is the whole
	// of it: this is true in four situations, and only the first of them means
	// an entry was actually removed -- the entry was removed, there was none,
	// there is no keyring on this machine, or the keyring could not be
	// consulted to find out. A caller that renders this as "removed" is making
	// a claim the store did not make; see describeFailedDelete, which does not.
	KeyringCleared bool
	// FileCleared reports the same for the plaintext fallback file.
	FileCleared bool
	// Err is the underlying failure, or the join of both halves' failures.
	Err error
}

// Error describes the failure. The guard is not defensive padding: the type
// and all its fields are exported, so a caller outside this package can hold a
// value Delete never built -- a zero value in a table-driven test is the usual
// way -- and an unconditional deref turns that into a panic.
func (e *DeleteError) Error() string {
	if e == nil || e.Err == nil {
		return "failed to remove every copy of the credential"
	}
	return e.Err.Error()
}

// Unwrap keeps the errors.Is/As chain intact through the join, so callers can
// still match the keyring or file cause -- and so the exit code survives.
func (e *DeleteError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// Delete removes a credential from the keyring and the fallback file, and
// reports every copy it could not remove.
//
// Both halves are always attempted: a failure in one says nothing about the
// other, and a copy left behind is exactly the outcome this command exists to
// prevent. The keyring half is deliberately attempted even in plaintext mode,
// so a user who opts into QCTL_ALLOW_PLAINTEXT_TOKENS after an earlier keyring
// login still has that older entry's removal attempted -- attempted, not
// achieved: a keyring that is up and refuses makes Delete report it as
// surviving.
func (cs *CredentialStore) Delete(endpointKey, orgID string) error {
	key := MakeCredentialKey(endpointKey, orgID)

	keyringErr := cs.deleteFromKeyring(key)
	fileErr := cs.deleteFromFile(key)
	if keyringErr == nil && fileErr == nil {
		return nil
	}
	// A *DeleteError value is only ever returned through a non-nil path: a
	// typed nil in an error interface is not nil, and would turn every
	// successful logout into a failure.
	return &DeleteError{
		KeyringCleared: keyringErr == nil,
		FileCleared:    fileErr == nil,
		Err:            errors.Join(keyringErr, fileErr),
	}
}

// DeleteKeyringCopy removes only the OS keyring's copy of the credential,
// reporting a failure only when the entry demonstrably survived.
func (cs *CredentialStore) DeleteKeyringCopy(endpointKey, orgID string) error {
	return cs.deleteFromKeyring(MakeCredentialKey(endpointKey, orgID))
}

// DeleteFileCopy removes only the fallback file's copy of the credential.
func (cs *CredentialStore) DeleteFileCopy(endpointKey, orgID string) error {
	return cs.deleteFromFile(MakeCredentialKey(endpointKey, orgID))
}

// deleteFromKeyring removes the credential from the OS keyring, reporting a
// failure ONLY when the entry demonstrably survived it.
//
// A failed keyring delete is ambiguous on its own, and two of its three shapes
// are perfectly healthy: there is nothing to remove (the credential lives in
// the fallback file), or there is no keyring on this machine at all (a
// container, CI, a headless server). Only the third -- a keyring that is up and
// refused -- is worth reporting, and on Linux it cannot be told from the second
// by the error value, because go-keyring returns whatever D-Bus said with no
// sentinel of its own.
//
// So this asks the keyring rather than guessing at its error text: after a
// failure that is not one of the two benign sentinels, read the entry back. If
// it is still readable, the delete really did fail and the token really is
// still in the keyring. If the read fails too -- the entry is gone, or the
// keyring cannot be consulted -- there is nothing qctl can honestly claim
// survived.
//
// The gap this leaves is a keyring that refuses reads and writes alike (a
// locked collection): the read-back fails, so a genuinely failed delete stays
// quiet. That is the pre-existing behaviour rather than a new hole, and it is
// the safe side of the trade: the other way round, every logout on a headless
// machine would start failing.
func (cs *CredentialStore) deleteFromKeyring(key string) error {
	err := KeyringDeleteFunc(KeyringServiceName, key)
	if err == nil {
		return nil
	}
	if errors.Is(err, keyring.ErrNotFound) {
		return nil // nothing was stored under this key
	}
	if errors.Is(err, keyring.ErrUnsupportedPlatform) {
		return nil // no keyring on this platform; the token was never in one
	}
	if probeKeyring(key) != keyringPresent {
		return nil // gone, or unconsultable -- nothing qctl can claim survived
	}
	return fmt.Errorf(
		"the credential is still in the OS keyring (service %q, entry %q): %w",
		KeyringServiceName, key, err)
}

// removeShadowingKeyringEntry deletes the keyring entry Store could not
// overwrite. It is deliberately stricter than deleteFromKeyring: success needs
// the entry demonstrably gone -- the delete succeeded, or a read-back says
// there is no such entry -- because a surviving copy could hide the credential
// Store just wrote, and the caller is about to act on that credential being the
// one qctl reads. A read-back that cannot be answered proves nothing, so here,
// unlike in logout, it counts as surviving. Store reaches this only after a
// read of another key has shown the keyring answering.
func removeShadowingKeyringEntry(key string) error {
	err := KeyringDeleteFunc(KeyringServiceName, key)
	if err == nil || errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	if probeKeyring(key) == keyringAbsent {
		return nil
	}
	return err
}

// storeToFile stores a credential in the fallback file.
//
// Only a missing file is an empty store. An existing file that cannot be read
// or parsed may still contain every other server and organization's token_name,
// so replacing it would silently discard the handles auth logout needs to
// revoke those sessions. Refuse the write and leave the original bytes intact.
func (cs *CredentialStore) storeToFile(key string, cred Credential) error {
	credFileMutex.Lock()
	defer credFileMutex.Unlock()

	credPath, err := CredentialsFilePath()
	if err != nil {
		return err
	}

	if _, err := config.EnsureQctlDir(); err != nil {
		return err
	}

	// Load existing credentials. A first login has no file yet; every other
	// read outcome has to be understood before it is safe to replace the file.
	creds := make(map[string]Credential)
	existingData, readErr := os.ReadFile(credPath)
	switch {
	case readErr == nil:
		if err := json.Unmarshal(existingData, &creds); err != nil {
			return credentialsFileWriteRefusal(credPath, "parsed", err)
		}
	case os.IsNotExist(readErr):
		// Normal first run: start a new credential map.
	default:
		return credentialsFileWriteRefusal(credPath, "read", readErr)
	}

	// Add or update credential
	creds[key] = cred

	// Save back to file
	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal credentials: %w", err)
	}

	return writeCredentialsFile(credPath, data)
}

func credentialsFileWriteRefusal(credPath, operation string, cause error) error {
	return fmt.Errorf(
		"cannot update credentials file %s because it could not be %s: %w\n\n%s",
		credentialsLocation(credPath), operation, cause,
		"Moving this file aside will let qctl create a new one, but qctl will lose every "+
			"token_name handle stored there across servers and organizations. After logging in, use "+
			"'qctl auth sessions' to inspect and revoke any sessions those names identify.")
}

// writeCredentialsFile replaces credPath with data through privatefile.Write:
// atomically, at mode 0600 before a byte of the token reaches the disk, and
// through a symlinked credentials.json rather than on top of it. Its doc
// comment has the reasoning; what is specific to this file is why each
// property matters here. The token must never exist at a looser mode, which a
// pre-existing 0644 credentials.json would otherwise keep. The replacement must
// be atomic because the file holds the token_name `auth logout` needs to revoke
// a session that may still be live on the server, and a truncated file loses
// it. And a symlink must be followed, or a logout leaves the deleted token in
// the link's target while reporting success.
//
// Callers must hold credFileMutex; both (storeToFile, deleteFromFile) do.
func writeCredentialsFile(credPath string, data []byte) error {
	return privatefile.Write(credPath, data, credentialsNoun)
}

// credentialsNoun names credentials.json in privatefile's errors and its
// temporary file, ".credentials-*.tmp".
const credentialsNoun = "credentials"

// retrieveFromFile retrieves a credential from the fallback file
func (cs *CredentialStore) retrieveFromFile(key string) (*Credential, error) {
	credFileMutex.Lock()
	defer credFileMutex.Unlock()

	credPath, err := CredentialsFilePath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(credPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrCredentialNotFound
		}
		return nil, fmt.Errorf("failed to read credentials file: %w", err)
	}

	creds := make(map[string]Credential)
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, fmt.Errorf("failed to unmarshal credentials file: %w", err)
	}

	cred, ok := creds[key]
	if !ok {
		return nil, ErrCredentialNotFound
	}

	return &cred, nil
}

// StoredOrganizations returns the organization IDs the fallback credentials
// file holds for endpointKey, sorted, so `auth logout --all` can delete every
// local copy of a credential the server has just revoked.
//
// It covers the FILE half only, and that is a limitation of the OS keyring
// rather than a choice: go-keyring exposes Get, Set and Delete and nothing that
// lists a service's entries, so there is no way to ask "which organizations
// have a credential stored for this server?" of the store qctl prefers. The
// caller therefore has to union this with the organizations it can name from
// somewhere else -- the config's cached org list -- and a keyring entry for an
// organization neither source names is one qctl cannot see to delete.
//
// A missing file is not an error: it is the ordinary state on a machine whose
// credentials all live in the keyring.
func (cs *CredentialStore) StoredOrganizations(endpointKey string) ([]string, error) {
	credFileMutex.Lock()
	defer credFileMutex.Unlock()

	credPath, err := CredentialsFilePath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(credPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read credentials file: %w", err)
	}

	creds := make(map[string]Credential)
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, fmt.Errorf("failed to unmarshal credentials file: %w", err)
	}

	orgIDs := make([]string, 0, len(creds))
	for key := range creds {
		// The KEY is the authority, not the stored credential's
		// OrganizationID field: Delete is keyed on the former, so an entry
		// whose two disagree must still be deleted under the name it is
		// filed by, or it survives the logout.
		storedEndpoint, orgID, parseErr := ParseCredentialKey(key)
		if parseErr != nil || storedEndpoint != endpointKey || orgID == "" {
			continue
		}
		orgIDs = append(orgIDs, orgID)
	}
	sort.Strings(orgIDs)
	return orgIDs, nil
}

// deleteFromFile removes a credential from the fallback file
func (cs *CredentialStore) deleteFromFile(key string) error {
	credFileMutex.Lock()
	defer credFileMutex.Unlock()

	credPath, err := CredentialsFilePath()
	if err != nil {
		return err
	}
	if err := privatefile.CleanupStaleTemps(credPath, credentialsNoun); err != nil {
		return err
	}

	data, err := os.ReadFile(credPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read credentials file: %w", err)
	}

	creds := make(map[string]Credential)
	if err = json.Unmarshal(data, &creds); err != nil {
		return fmt.Errorf("failed to unmarshal credentials file: %w", err)
	}

	delete(creds, key)

	// Save back to file
	if len(creds) == 0 {
		// Nothing left to keep, so the file goes -- but "the file" is the one
		// that actually holds the token. os.Remove does not follow symlinks,
		// so removing credPath when it is a link unlinks the pointer and
		// leaves the whole credential map, including the token just deleted,
		// sitting in the link's target. Remove the target, then the link that
		// pointed at it, so no live token survives a logout that reports
		// success. A dangling link still can: through a CHAIN (a -> b -> c)
		// this removes c and the head a and leaves b pointing at nothing,
		// because only the head and the final target are ever named.
		//
		// IsSymlink rather than `target != credPath`: a plain file under a
		// symlinked HOME resolves to a different path too, and removing it a
		// second time would fail. A stat failure answers false, the safe
		// direction here -- the target is already gone, so the worst it can do
		// is leave a dangling link for the next `auth login` to replace.
		target, resolveErr := privatefile.ResolveTarget(credPath, credentialsNoun)
		if resolveErr != nil {
			return resolveErr
		}
		if err := os.Remove(target); err != nil {
			return fmt.Errorf("failed to remove the credentials file: %w", err)
		}
		if target != credPath && privatefile.IsSymlink(credPath) {
			if err := os.Remove(credPath); err != nil {
				return fmt.Errorf("failed to remove the credentials file symlink: %w", err)
			}
		}
		return nil
	}

	newData, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal credentials: %w", err)
	}

	return writeCredentialsFile(credPath, newData)
}

// CredentialsFilePath returns the path of the plaintext fallback credentials
// file. A message a user has to act on should name CredentialsFileLocation
// instead, which also says where a symlinked file really is.
func CredentialsFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}
	return filepath.Join(home, config.ConfigDirName, CredentialsFileName), nil
}

// CredentialsFileLocation names the fallback credentials file for a message
// that sends the user to inspect or remove it -- an error they resolve by hand,
// unlike a missing credential.
//
// Both write paths act on a symlinked credentials.json's target, so that is
// where the token is, and a message saying "no copy of the token is left in
// <path>" of the LINK names a file that holds nothing. When credentials.json
// is itself a symlink this names the target first and the link second; for
// anything else, including a plain file under a symlinked HOME, the path alone
// already opens the right file.
func CredentialsFileLocation() string {
	path, err := CredentialsFilePath()
	if err != nil {
		return "the credentials file"
	}
	return credentialsLocation(path)
}

func credentialsLocation(credPath string) string {
	if !privatefile.IsSymlink(credPath) {
		return credPath
	}
	target, err := privatefile.ResolveTarget(credPath, credentialsNoun)
	if err != nil || target == credPath {
		// Unresolvable, or dangling -- which a write replaces with a real file
		// at credPath -- so credPath is the only name that means anything.
		return credPath
	}
	return fmt.Sprintf("%s (linked from %s)", target, credPath)
}

// IsPlaintextAllowed checks if plaintext credential storage is allowed
func IsPlaintextAllowed() bool {
	env := os.Getenv("QCTL_ALLOW_PLAINTEXT_TOKENS")
	return env == "1" || env == "true"
}
