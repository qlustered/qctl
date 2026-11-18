package testutil

import (
	"testing"

	"github.com/qlustered/qctl/internal/auth"
	"github.com/zalando/go-keyring"
)

// KeyringStub describes the keyring a test wants the credential store to see.
// Any operation left nil uses the in-memory map in Entries, so the default
// zero value is simply "a working, empty keyring" -- which is what almost
// every test wants, and what keeps `make test` from touching the developer's
// real one.
type KeyringStub struct {
	// Entries is the in-memory keyring, keyed exactly as
	// auth.MakeCredentialKey builds it. A nil map is created on first use.
	Entries map[string]string
	// Get, Set and Delete override the in-memory behaviour for one operation,
	// which is how a test makes the keyring refuse.
	Get    func(service, user string) (string, error)
	Set    func(service, user, password string) error
	Delete func(service, user string) error
}

// StubKeyring points the credential store's keyring seam at stub for the
// duration of the test and restores the real functions afterwards.
func StubKeyring(t *testing.T, stub KeyringStub) {
	t.Helper()
	if stub.Entries == nil {
		stub.Entries = map[string]string{}
	}
	origGet, origSet, origDel := auth.KeyringGetFunc, auth.KeyringSetFunc, auth.KeyringDeleteFunc
	t.Cleanup(func() {
		auth.KeyringGetFunc, auth.KeyringSetFunc, auth.KeyringDeleteFunc = origGet, origSet, origDel
	})

	auth.KeyringGetFunc = stub.Get
	if auth.KeyringGetFunc == nil {
		auth.KeyringGetFunc = func(_, user string) (string, error) {
			if v, ok := stub.Entries[user]; ok {
				return v, nil
			}
			return "", keyring.ErrNotFound
		}
	}
	auth.KeyringSetFunc = stub.Set
	if auth.KeyringSetFunc == nil {
		auth.KeyringSetFunc = func(_, user, password string) error {
			stub.Entries[user] = password
			return nil
		}
	}
	auth.KeyringDeleteFunc = stub.Delete
	if auth.KeyringDeleteFunc == nil {
		auth.KeyringDeleteFunc = func(_, user string) error {
			if _, ok := stub.Entries[user]; !ok {
				return keyring.ErrNotFound
			}
			delete(stub.Entries, user)
			return nil
		}
	}

	// The in-memory map ignores the service, and so does every override a
	// test writes, so check it once here: a call site passing another service
	// would otherwise read and write a keyring qctl never uses, green.
	get, set, del := auth.KeyringGetFunc, auth.KeyringSetFunc, auth.KeyringDeleteFunc
	auth.KeyringGetFunc = func(service, user string) (string, error) {
		requireServiceName(t, "Get", service, user)
		return get(service, user)
	}
	auth.KeyringSetFunc = func(service, user, password string) error {
		requireServiceName(t, "Set", service, user)
		return set(service, user, password)
	}
	auth.KeyringDeleteFunc = func(service, user string) error {
		requireServiceName(t, "Delete", service, user)
		return del(service, user)
	}
}

func requireServiceName(t *testing.T, op, service, user string) {
	t.Helper()
	if service != auth.KeyringServiceName {
		t.Errorf("keyring %s(%q) used service %q, want %q", op, user, service, auth.KeyringServiceName)
	}
}
