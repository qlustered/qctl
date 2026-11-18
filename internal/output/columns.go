package output

import (
	"reflect"
	"sort"
	"strings"

	qerrors "github.com/qlustered/qctl/internal/errors"
)

// AvailableColumns names every column a payload can render.
//
// It reflects the *type* of the display struct rather than reading rows[0],
// so an unknown --columns name fails the same way on an empty result set as
// on a full one. A blank column is a silent wrong answer, and "the list was
// empty so we could not check" is the same wrong answer with a better excuse.
//
// rows is the fallback for payloads that are not struct slices (a slice of
// maps, say). When neither source is available it returns nil, and
// ValidateColumns then validates nothing.
func AvailableColumns(data interface{}, rows []map[string]string) []string {
	if cols := structColumns(data); cols != nil {
		return cols
	}
	if len(rows) > 0 {
		keys := make([]string, 0, len(rows[0]))
		for k := range rows[0] {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return keys
	}
	return nil
}

// structColumns returns the json tag names of the struct behind data, in
// declaration order — the same order and the same names structToRow keys its
// rows by. Returns nil when data is not a struct or a slice of structs.
func structColumns(data interface{}) []string {
	if data == nil {
		return nil
	}
	t := reflect.TypeOf(data)
	for t != nil && (t.Kind() == reflect.Ptr || t.Kind() == reflect.Slice || t.Kind() == reflect.Array) {
		t = t.Elem()
	}
	if t == nil || t.Kind() != reflect.Struct {
		return nil
	}
	cols := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		if name := strings.Split(tag, ",")[0]; name != "" {
			cols = append(cols, name)
		}
	}
	if len(cols) == 0 {
		return nil
	}
	return cols
}

// ParseColumns splits a --columns value on commas, trims each name, and drops
// the empty ones.
func ParseColumns(raw string) []string {
	var columns []string
	for _, c := range strings.Split(raw, ",") {
		if c = strings.TrimSpace(c); c != "" {
			columns = append(columns, c)
		}
	}
	return columns
}

// ValidateColumns fails on the first requested column the payload cannot
// render, naming it and listing the ones it can. available == nil means the
// payload's column set is unknown; validating against an unknown set would
// reject correct input, so it is skipped.
//
// The failure is a bad-usage error (exit 2), not a generic one (exit 1): a
// typo'd --columns is flag validation, and a script that cannot tell "you
// asked for a column that does not exist" from "the API fell over" is back to
// acting on a wrong answer — the exit code is part of the answer.
func ValidateColumns(requested, available []string) error {
	if len(available) == 0 {
		return nil
	}
	known := make(map[string]bool, len(available))
	for _, c := range available {
		known[c] = true
	}
	for _, c := range requested {
		if !known[c] {
			return qerrors.NewBadUsagef("unknown column %q\n  valid: %s", c, strings.Join(available, ", "))
		}
	}
	return nil
}
