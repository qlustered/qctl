// Package piimask recognizes the text the server writes in place of a PII
// value masked for the caller.
package piimask

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/qlustered/qctl/internal/pkg/jsonnum"
)

// Mask is the text a masked value starts with.
const Mask = "•••••"

// maskRunes and shownRunes are the two lengths, in characters, of a masked
// value: Mask alone, or Mask followed by the value's last 4 characters.
const (
	maskRunes  = 5
	shownRunes = 4
)

// IsMasked reports whether s is Mask alone or Mask followed by 4 characters.
func IsMasked(s string) bool {
	if !strings.HasPrefix(s, Mask) {
		return false
	}
	n := utf8.RuneCountInString(s)
	return n == maskRunes || n == maskRunes+shownRunes
}

// Paths returns, sorted, the path of every string in v's JSON encoding, an
// object key or a value at any depth, that IsMasked. root names v itself; a
// key extends a path with ".key" and an array index with "[i]". A v that does
// not encode to JSON has no paths.
func Paths(root string, v interface{}) []string {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var decoded interface{}
	if err := jsonnum.Unmarshal(raw, &decoded); err != nil {
		return nil
	}
	var paths []string
	walk(root, decoded, &paths)
	slices.Sort(paths)
	return slices.Compact(paths)
}

func walk(path string, v interface{}, paths *[]string) {
	switch t := v.(type) {
	case string:
		if IsMasked(t) {
			*paths = append(*paths, path)
		}
	case map[string]interface{}:
		for k, item := range t {
			child := path + "." + k
			if path == "" {
				child = k
			}
			if IsMasked(k) {
				*paths = append(*paths, child)
			}
			walk(child, item, paths)
		}
	case []interface{}:
		for i, item := range t {
			walk(path+"["+strconv.Itoa(i)+"]", item, paths)
		}
	}
}
