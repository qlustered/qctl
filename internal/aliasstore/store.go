package aliasstore

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/qlustered/qctl/internal/config"
	bolt "go.etcd.io/bbolt"
)

// Sub-bucket names within each server-scoped bucket.
var (
	bktForward  = []byte("forward")  // key: rt + ":" + uuid  -> value: alias
	bktReverse  = []byte("reverse")  // key: alias             -> value: uuid
	bktCounters = []byte("counters") // key: resourceType      -> value: uint64 (big-endian)
)

// openTimeout bounds how long a read-write open waits for the exclusive file lock
// before failing cleanly instead of hanging on a contended database.
const openTimeout = 5 * time.Second

// ErrAliasNotFound identifies a syntactically valid alias that has no mapping
// in the requested server scope.
var ErrAliasNotFound = errors.New("unknown alias")

// Entry is one persisted alias mapping, returned by List.
type Entry struct {
	Alias        string
	UUID         string
	ResourceType ResourceType
}

// Store is a bbolt-backed, server-scoped bijection between UUIDs and short aliases.
type Store struct {
	db   *bolt.DB
	path string
}

// DefaultDBPath returns ~/.qctl/cache/aliases.db, creating its directories if needed.
func DefaultDBPath() (string, error) {
	qctlDir, err := config.EnsureQctlDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(qctlDir, "cache")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("failed to create cache directory: %w", err)
	}
	return filepath.Join(dir, "aliases.db"), nil
}

// Open opens the default DB read-write. The caller must Close it.
func Open() (*Store, error) {
	path, err := DefaultDBPath()
	if err != nil {
		return nil, err
	}
	return openAt(path, false)
}

// OpenReadOnly opens the default DB read-only (allows concurrent readers).
func OpenReadOnly() (*Store, error) {
	path, err := DefaultDBPath()
	if err != nil {
		return nil, err
	}
	return openAt(path, true)
}

// openAt opens the DB at path in the requested mode.
func openAt(path string, readOnly bool) (*Store, error) {
	opts := &bolt.Options{Timeout: openTimeout, ReadOnly: readOnly}
	db, err := bolt.Open(path, 0o600, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to open alias store at %s: %w", path, err)
	}
	return &Store{db: db, path: path}, nil
}

// Close releases the store's file handle and lock.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Allocate returns the existing alias for canonicalUUID within (serverKey, rt), or atomically
// allocates the next one. The lookup-or-create runs in a single read-write transaction.
func (s *Store) Allocate(serverKey string, rt ResourceType, canonicalUUID string) (string, error) {
	prefix, err := prefixFor(rt)
	if err != nil {
		return "", err
	}
	fwdKey := []byte(string(rt) + ":" + canonicalUUID)

	var alias string
	err = s.db.Update(func(tx *bolt.Tx) error {
		server, err := tx.CreateBucketIfNotExists([]byte(serverKey))
		if err != nil {
			return err
		}
		forward, err := server.CreateBucketIfNotExists(bktForward)
		if err != nil {
			return err
		}
		if existing := forward.Get(fwdKey); existing != nil {
			alias = string(existing)
			return nil
		}
		counters, err := server.CreateBucketIfNotExists(bktCounters)
		if err != nil {
			return err
		}
		next := decodeCounter(counters.Get([]byte(rt))) + 1
		if err := counters.Put([]byte(rt), encodeCounter(next)); err != nil {
			return err
		}
		alias = fmt.Sprintf("%s-%d", prefix, next)

		reverse, err := server.CreateBucketIfNotExists(bktReverse)
		if err != nil {
			return err
		}
		if err := forward.Put(fwdKey, []byte(alias)); err != nil {
			return err
		}
		return reverse.Put([]byte(alias), []byte(canonicalUUID))
	})
	if err != nil {
		return "", fmt.Errorf("failed to allocate alias: %w", err)
	}
	return alias, nil
}

// Resolve maps an alias back to its UUID within serverKey.
func (s *Store) Resolve(serverKey, alias string) (string, error) {
	if _, _, err := ParseAlias(alias); err != nil {
		return "", err
	}
	var uuid string
	err := s.db.View(func(tx *bolt.Tx) error {
		server := tx.Bucket([]byte(serverKey))
		if server == nil {
			return nil
		}
		reverse := server.Bucket(bktReverse)
		if reverse == nil {
			return nil
		}
		if v := reverse.Get([]byte(alias)); v != nil {
			uuid = string(v)
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("failed to resolve alias: %w", err)
	}
	if uuid == "" {
		return "", fmt.Errorf("%w %q for the current server", ErrAliasNotFound, alias)
	}
	return uuid, nil
}

// ResolveTyped resolves alias to a UUID, requiring its prefix to match rt.
func (s *Store) ResolveTyped(serverKey string, rt ResourceType, alias string) (string, error) {
	prefix, _, err := ParseAlias(alias)
	if err != nil {
		return "", err
	}
	got, err := ResourceTypeForPrefix(prefix)
	if err != nil {
		return "", err
	}
	if got != rt {
		return "", fmt.Errorf("alias %q is not a %s alias", alias, rt)
	}
	return s.Resolve(serverKey, alias)
}

// List returns all entries for serverKey, sorted by (prefix, ordinal).
func (s *Store) List(serverKey string) ([]Entry, error) {
	var entries []Entry
	err := s.db.View(func(tx *bolt.Tx) error {
		server := tx.Bucket([]byte(serverKey))
		if server == nil {
			return nil
		}
		reverse := server.Bucket(bktReverse)
		if reverse == nil {
			return nil
		}
		return reverse.ForEach(func(k, v []byte) error {
			alias := string(k)
			prefix, _, perr := ParseAlias(alias)
			rt := ResourceType("")
			if perr == nil {
				rt, _ = ResourceTypeForPrefix(prefix)
			}
			entries = append(entries, Entry{Alias: alias, UUID: string(v), ResourceType: rt})
			return nil
		})
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list aliases: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool {
		pi, oi, _ := ParseAlias(entries[i].Alias)
		pj, oj, _ := ParseAlias(entries[j].Alias)
		if pi != pj {
			return pi < pj
		}
		return oi < oj
	})
	return entries, nil
}

// Clear removes all aliases for serverKey. Clearing an unknown server is a no-op.
func (s *Store) Clear(serverKey string) error {
	err := s.db.Update(func(tx *bolt.Tx) error {
		if tx.Bucket([]byte(serverKey)) == nil {
			return nil
		}
		return tx.DeleteBucket([]byte(serverKey))
	})
	if err != nil {
		return fmt.Errorf("failed to clear aliases: %w", err)
	}
	return nil
}

// ClearAll removes every server's aliases.
func (s *Store) ClearAll() error {
	err := s.db.Update(func(tx *bolt.Tx) error {
		var names [][]byte
		_ = tx.ForEach(func(name []byte, _ *bolt.Bucket) error {
			dup := make([]byte, len(name))
			copy(dup, name)
			names = append(names, dup)
			return nil
		})
		for _, name := range names {
			if err := tx.DeleteBucket(name); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to clear all aliases: %w", err)
	}
	return nil
}

func encodeCounter(n uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, n)
	return b
}

func decodeCounter(b []byte) uint64 {
	if len(b) != 8 {
		return 0
	}
	return binary.BigEndian.Uint64(b)
}
