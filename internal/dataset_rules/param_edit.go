package dataset_rules //nolint:revive // Public package name matches the established resource domain.

import (
	"encoding/json"
	"reflect"
	"slices"
	"sort"
	"strings"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
	"github.com/qlustered/qctl/internal/pkg/piimask"
	"github.com/qlustered/qctl/internal/terminal"
)

// ParamEdit is a set of --set-param and --unset-param edits to a rule's
// params.
type ParamEdit struct {
	// Set holds each --set-param value by key.
	Set map[string]interface{}
	// Unset is the --unset-param keys, in flag order.
	Unset []string
}

// ParseParamEdit reads --set-param key=value and --unset-param key values. A
// value is read as JSON when it parses as one and as the literal string
// otherwise. A value without '=', an empty key, or a key both set and unset
// is bad usage (exit 2).
func ParseParamEdit(set, unset []string) (ParamEdit, error) {
	var e ParamEdit
	if len(set) > 0 {
		e.Set = map[string]interface{}{}
	}
	for _, kv := range set {
		k, v, found := strings.Cut(kv, "=")
		if !found || k == "" {
			return e, qerrors.NewBadUsagef("invalid --set-param %q: expected key=value", kv)
		}
		e.Set[k] = ParseParamValue(v)
	}
	for _, key := range unset {
		if key == "" {
			return e, qerrors.NewBadUsage("--unset-param cannot contain an empty key")
		}
		if _, conflicts := e.Set[key]; conflicts {
			return e, qerrors.NewBadUsagef("key %q cannot be both set and unset in one invocation", key)
		}
	}
	e.Unset = append([]string(nil), unset...)
	return e, nil
}

// IsZero reports whether e edits nothing.
func (e ParamEdit) IsZero() bool {
	return len(e.Set) == 0 && len(e.Unset) == 0
}

// Apply returns params with e's values set and its keys removed, leaving
// params unchanged. A key to unset that params lacks is bad usage (exit 2);
// owner names params in that message.
func (e ParamEdit) Apply(params map[string]interface{}, owner string) (map[string]interface{}, error) {
	merged := make(map[string]interface{}, len(params)+len(e.Set))
	for k, v := range params {
		merged[k] = v
	}
	for k, v := range e.Set {
		merged[k] = v
	}
	for _, k := range e.Unset {
		if _, ok := merged[k]; !ok {
			return nil, qerrors.NewBadUsagef("--unset-param %s: %s has no param %q (it has: %s)",
				k, owner, k, paramKeys(params))
		}
		delete(merged, k)
	}
	return merged, nil
}

// RefuseMaskedCarriedParams returns bad usage (exit 2) when a param of live
// that the edit neither sets nor unsets holds masked text at any depth, naming
// each such key; owner names live in the message. set and unset are the edit's
// keys.
func RefuseMaskedCarriedParams(owner string, live, set map[string]interface{}, unset []string) error {
	var keys []string
	for k, v := range live {
		if _, edited := set[k]; edited || slices.Contains(unset, k) {
			continue
		}
		if len(piimask.Paths(k, v)) > 0 {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return nil
	}
	sort.Strings(keys)
	return qerrors.NewBadUsagef("%s's params are masked for you (%s stands in for a value you cannot see), "+
		"and this edit would send that text back as the value of: %s. Give each a value with --set-param or "+
		"remove it with --unset-param; leaving it unchanged takes someone with access to the table's unmasked PII values",
		owner, piimask.Mask, terminal.Sanitize(strings.Join(keys, ", ")))
}

func paramKeys(params map[string]interface{}) string {
	if len(params) == 0 {
		return "none"
	}
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}

// ParseParamValue reads a flag value as JSON so numbers and booleans keep
// their type, falling back to the literal string. A parameter that is the
// text "true" is spelled '"true"'.
func ParseParamValue(v string) interface{} {
	var out interface{}
	if json.Valid([]byte(v)) {
		decoder := json.NewDecoder(strings.NewReader(v))
		decoder.UseNumber()
		if err := decoder.Decode(&out); err == nil {
			return out
		}
	}
	return v
}

// SameParamValue reports whether a and b are equal once each is encoded to
// JSON and decoded with jsonnum.Unmarshal.
func SameParamValue(a, b interface{}) bool {
	na, errA := normalizeJSON(a)
	nb, errB := normalizeJSON(b)
	if errA != nil || errB != nil {
		return false
	}
	return reflect.DeepEqual(na, nb)
}

func normalizeJSON(v interface{}) (interface{}, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out interface{}
	err = jsonnum.Unmarshal(raw, &out)
	return out, err
}
