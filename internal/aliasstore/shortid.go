package aliasstore

import (
	"errors"
	"fmt"
	"os"
	"strings"

	bolt "go.etcd.io/bbolt"

	"github.com/qlustered/qctl/internal/config"
)

// bktShortIDs is a sub-bucket of each server bucket holding
//
//	key: rt + ":" + orgID + ":" + <32 hex digits, no dashes>  ->  value: canonical UUID
//
// It is separate from bktReverse so `qctl cache list` and `qctl cache resolve`
// -- which walk bktReverse and require a <prefix>-<ordinal> shape -- never see
// these entries. Keys are org-scoped as well as server-scoped: an 8-hex short
// ID is only 32 bits, and a cache that answered across orgs could silently
// point at another org's rule.
var bktShortIDs = []byte("shortids")

// normalizeHex reduces a UUID or a UUID prefix to its comparable form:
// lowercase hex with dashes stripped. It is the same normalization
// findUUIDPrefixMatches applies in the rule resolvers.
func normalizeHex(idOrPrefix string) string {
	return strings.ToLower(strings.ReplaceAll(idOrPrefix, "-", ""))
}

// shortIDKey builds the lookup key. orgID is lowercased because callers hold it
// in two forms -- the raw string from the config file, and the canonical form of
// a parsed uuid.UUID -- and a case skew between the writer and the reader would
// be a permanent silent cache miss. Only the case is normalized, not the dashes:
// unlike the UUID being expanded, orgID is a scope, and collapsing "a-b" and
// "ab" into one key space would widen a scope rather than narrow it.
func shortIDKey(rt ResourceType, orgID, hex string) []byte {
	return []byte(string(rt) + ":" + strings.ToLower(orgID) + ":" + hex)
}

// PutShortIDs records every uuid under its full hex form so any prefix of it
// can be expanded later. Re-recording a known UUID is a no-op overwrite.
func (s *Store) PutShortIDs(serverKey, orgID string, rt ResourceType, uuids []string) error {
	if len(uuids) == 0 {
		return nil
	}
	err := s.db.Update(func(tx *bolt.Tx) error {
		server, err := tx.CreateBucketIfNotExists([]byte(serverKey))
		if err != nil {
			return err
		}
		short, err := server.CreateBucketIfNotExists(bktShortIDs)
		if err != nil {
			return err
		}
		for _, u := range uuids {
			hex := normalizeHex(u)
			if hex == "" {
				continue
			}
			if err := short.Put(shortIDKey(rt, orgID, hex), []byte(u)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to record short IDs: %w", err)
	}
	return nil
}

// LookupShortID expands a short ID -- or any other prefix of a UUID -- to the
// full UUID it was recorded under.
//
// Returns (uuid, true, nil) on a unique match. A prefix matching nothing, or
// matching more than one UUID, is reported as ("", false, nil): the caller
// falls back to a live listing, which is the only place a useful ambiguity
// message (with rule names and releases) can be built.
func (s *Store) LookupShortID(serverKey, orgID string, rt ResourceType, prefix string) (string, bool, error) {
	hex := normalizeHex(prefix)
	if hex == "" {
		return "", false, nil
	}
	key := shortIDKey(rt, orgID, hex)

	var found string
	var matches int
	err := s.db.View(func(tx *bolt.Tx) error {
		server := tx.Bucket([]byte(serverKey))
		if server == nil {
			return nil
		}
		short := server.Bucket(bktShortIDs)
		if short == nil {
			return nil
		}
		c := short.Cursor()
		for k, v := c.Seek(key); k != nil && strings.HasPrefix(string(k), string(key)); k, v = c.Next() {
			matches++
			if matches > 1 {
				return nil
			}
			found = string(v)
		}
		return nil
	})
	if err != nil {
		return "", false, fmt.Errorf("failed to look up short ID: %w", err)
	}
	if matches != 1 {
		return "", false, nil
	}
	return found, true, nil
}

// RememberShortIDs opens the default store read-write and records uuids for
// (serverURL, orgID, rt). Callers treat a failure as unimportant -- the cache
// is an optimization over listing the catalog, never the only path.
func RememberShortIDs(serverURL, orgID string, rt ResourceType, uuids []string) error {
	if len(uuids) == 0 {
		return nil
	}
	serverKey, err := config.NormalizeEndpointKey(serverURL)
	if err != nil {
		return err
	}
	store, err := Open()
	if err != nil {
		return err
	}
	defer store.Close()
	return store.PutShortIDs(serverKey, orgID, rt, uuids)
}

// ExpandShortID opens the default store read-only and expands prefix.
// A store that does not exist yet is a miss, not an error.
func ExpandShortID(serverURL, orgID string, rt ResourceType, prefix string) (string, bool, error) {
	serverKey, err := config.NormalizeEndpointKey(serverURL)
	if err != nil {
		return "", false, err
	}
	store, err := OpenReadOnly()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	defer store.Close()
	return store.LookupShortID(serverKey, orgID, rt, prefix)
}
